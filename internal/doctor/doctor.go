// Package doctor confere o ambiente antes (ou depois) de uma execução e diz,
// em português, o que está certo, o que merece atenção e o que vai falhar.
// Não altera nada.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mtrad/internal/ftbq"
	"mtrad/internal/glossary"
	"mtrad/internal/hardware"
	"mtrad/internal/route"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
)

type Level int

const (
	OK Level = iota
	Warn
	Fail
)

func (l Level) String() string { return [...]string{"ok", "aviso", "erro"}[l] }

type Check struct {
	Level Level
	Name  string
	Msg   string
}

// Deps isola o que toca o mundo de fora, para os testes.
type Deps struct {
	LookPath  func(string) (string, error)
	Exec      func(ctx context.Context, name string, args ...string) error
	Get       func(ctx context.Context, url string) (int, error)
	Getenv    func(string) string
	FreeBytes func(path string) (uint64, error)
	// Writable tenta criar e apagar um arquivo temporário na pasta.
	Writable func(dir string) error
}

func DefaultDeps() Deps {
	return Deps{
		LookPath: exec.LookPath,
		Exec: func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		},
		Get: func(ctx context.Context, url string) (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return 0, err
			}
			resp.Body.Close()
			return resp.StatusCode, nil
		},
		Getenv:    os.Getenv,
		FreeBytes: freeBytes,
		Writable: func(dir string) error {
			f, err := os.CreateTemp(dir, ".mtrad-doctor-*")
			if err != nil {
				return err
			}
			name := f.Name()
			f.Close()
			return os.Remove(name)
		},
	}
}

type Input struct {
	Work      string
	Route     *route.Config     // nil = sem arquivo de configuração
	Translate *translate.Config // idem
	// Hardware é o que `hardware.Detect` mediu (nil = não conferir o computador).
	Hardware *hardware.Info
	Deps     Deps
}

type runner struct {
	checks []Check
}

func (r *runner) add(l Level, name, format string, a ...any) {
	r.checks = append(r.checks, Check{l, name, fmt.Sprintf(format, a...)})
}

// Worst devolve o pior nível encontrado.
func Worst(cs []Check) Level {
	w := OK
	for _, c := range cs {
		if c.Level > w {
			w = c.Level
		}
	}
	return w
}

func Run(ctx context.Context, in Input) []Check {
	r := &runner{}
	d := in.Deps
	game := r.instance(in)
	r.stages(in.Work)
	r.errorsLog(in.Work)
	r.glossary(in.Work)
	r.hardware(in)
	r.engines(ctx, in, d)
	r.disk(in.Work, d)
	r.writable("pasta de trabalho", in.Work, d)
	if game != "" {
		r.game(game, d)
	}
	return r.checks
}

func (r *runner) instance(in Input) (game string) {
	var inst schema.Instance
	p := filepath.Join(in.Work, "instance.json")
	if err := schema.ReadJSON(p, &inst, schema.VInstance); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.add(Warn, "instância", "ainda não há %s: rode `mtrad version -i PASTA_DA_INSTANCIA` (ou `mtrad run`)", p)
		} else {
			r.add(Fail, "instância", "%v", err)
		}
		return ""
	}
	r.add(OK, "instância", "Minecraft %s, %s %s, formato de pacote %d.%d", inst.McVersion, inst.Loader, inst.LoaderVersion, inst.PackFormat.Major, inst.PackFormat.Minor)
	for _, w := range inst.Warnings {
		r.add(Warn, "instância", "%s", w)
	}
	if st, err := os.Stat(inst.GameDir); err != nil || !st.IsDir() {
		r.add(Fail, "pasta do jogo", "%s não existe", inst.GameDir)
		return ""
	}
	return inst.GameDir
}

func (r *runner) stages(work string) {
	files, _ := filepath.Glob(filepath.Join(work, "*.done.json"))
	sort.Strings(files)
	if len(files) == 0 {
		return
	}
	var stale []string
	for _, f := range files {
		var dn schema.Done
		if err := schema.ReadJSON(f, &dn, schema.VDone); err != nil {
			r.add(Fail, "etapas", "%v", err)
			continue
		}
		check := func(kind string, m map[string]string) {
			var names []string
			for n := range m {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				got, err := schema.FileSHA256(filepath.Join(work, n))
				switch {
				case err != nil:
					stale = append(stale, fmt.Sprintf("%s: %s sumiu", dn.Stage, n))
				case got != m[n]:
					stale = append(stale, fmt.Sprintf("%s: %s (%s) mudou depois que a etapa terminou", dn.Stage, n, kind))
				}
			}
		}
		check("saída", dn.Outputs)
	}
	if len(stale) == 0 {
		r.add(OK, "etapas", "%d etapas concluídas e os arquivos delas estão como ficaram", len(files))
		return
	}
	for _, s := range stale {
		r.add(Warn, "etapas", "%s; rode a etapa de novo ou use --force", s)
	}
}

func (r *runner) errorsLog(work string) {
	p := filepath.Join(work, "errors.jsonl")
	by := map[string]int{}
	total := 0
	err := schema.Each(p, schema.VErrors, func(e schema.ErrRec) error {
		by[e.Stage]++
		total++
		return nil
	})
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		r.add(Fail, "errors.jsonl", "%v", err)
	case total == 0:
		r.add(OK, "errors.jsonl", "vazio")
	default:
		var parts []string
		for s, n := range by {
			parts = append(parts, fmt.Sprintf("%s %d", s, n))
		}
		sort.Strings(parts)
		r.add(Warn, "errors.jsonl", "%d registros (%s); leia %s", total, strings.Join(parts, ", "), p)
	}
}

func (r *runner) glossary(work string) {
	g, err := glossary.Load(filepath.Join(work, "glossary.json"))
	if err != nil {
		r.add(Warn, "glossário", "não há glossário (opcional, mas ajuda com nomes de itens e blocos): `mtrad glossary --vanilla`")
		return
	}
	r.add(OK, "glossário", "%d termos, versão %s", len(g.Terms), g.Version)
}

func (r *runner) engines(ctx context.Context, in Input, d Deps) {
	if in.Translate == nil || in.Route == nil {
		r.add(Warn, "motores", "sem arquivo de configuração (-c): não conferi os motores de tradução")
		return
	}
	if err := in.Route.Validate(); err != nil {
		r.add(Fail, "rota", "%v", err)
	}
	names := make([]string, 0, len(in.Translate.Engines))
	for n := range in.Translate.Engines {
		names = append(names, n)
	}
	sort.Strings(names)
	need := []string{in.Route.Default}
	for _, ru := range in.Route.Rules {
		need = append(need, ru.Engine)
	}
	for _, n := range need {
		if _, ok := in.Translate.Engines[n]; !ok && n != "" {
			r.add(Fail, "rota", "a rota usa o motor %q, que não está em translate.engines", n)
		}
	}
	for _, n := range names {
		r.engine(ctx, n, in.Translate.Engines[n], d)
	}
}

func (r *runner) engine(ctx context.Context, name string, c translate.EngineConfig, d Deps) {
	label := "motor " + name
	probe := func(url string) {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		code, err := d.Get(cctx, url)
		switch {
		case err != nil:
			r.add(Warn, label, "%s não respondeu (o servidor está ligado?): %v", url, err)
		case code == 401 || code == 403:
			r.add(Warn, label, "%s respondeu %d: o servidor recusou a chave", url, code)
		case code >= 200 && code < 300:
			r.add(OK, label, "%s respondeu", url)
		default:
			r.add(Warn, label, "%s respondeu %d", url, code)
		}
	}
	switch c.Type {
	case "echo":
		r.add(Warn, label, "motor de teste: copia o texto sem traduzir")
	case "openai":
		if c.BaseURL == "" {
			r.add(Fail, label, "falta base_url")
			return
		}
		if c.Model == "" {
			r.add(Warn, label, "sem model: alguns servidores exigem")
		}
		if c.APIKeyEnv != "" && d.Getenv(c.APIKeyEnv) == "" {
			r.add(Fail, label, "a variável de ambiente %s (chave da API) está vazia", c.APIKeyEnv)
			return
		}
		probe(strings.TrimRight(c.BaseURL, "/") + "/models")
	case "libretranslate":
		if c.BaseURL == "" {
			r.add(Fail, label, "falta base_url")
			return
		}
		probe(strings.TrimRight(c.BaseURL, "/") + "/languages")
	case "worker":
		if len(c.Command) == 0 {
			r.add(Fail, label, "falta command")
			return
		}
		if _, err := d.LookPath(c.Command[0]); err != nil {
			r.add(Fail, label, "%s não está no PATH", c.Command[0])
			return
		}
		if len(c.Command) > 1 && strings.HasSuffix(c.Command[1], ".py") {
			if _, err := os.Stat(c.Command[1]); err != nil {
				r.add(Fail, label, "o script %s não existe (o caminho é relativo à pasta em que você roda o mtrad)", c.Command[1])
				return
			}
		}
		joined := strings.Join(c.Command, " ")
		if strings.Contains(joined, "--backend ct2") {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if err := d.Exec(cctx, c.Command[0], "-c", "import ctranslate2"); err != nil {
				r.add(Warn, label, "o Python não conseguiu importar ctranslate2 (pip install ctranslate2 sentencepiece)")
				return
			}
		}
		r.add(OK, label, "comando encontrado: %s", c.Command[0])
	default:
		r.add(Fail, label, "tipo desconhecido %q", c.Type)
	}
}

func (r *runner) disk(work string, d Deps) {
	dir := nearestDir(work)
	if dir == "" {
		return
	}
	free, err := d.FreeBytes(dir)
	if err != nil {
		return
	}
	if free < 1<<30 {
		r.add(Warn, "disco", "só %d MiB livres perto de %s", free>>20, work)
		return
	}
	r.add(OK, "disco", "%d GiB livres", free>>30)
}

// nearestDir sobe até a primeira pasta que existe.
func nearestDir(p string) string {
	for {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}

func (r *runner) writable(name, path string, d Deps) {
	if d.Writable == nil {
		return
	}
	dir := nearestDir(path)
	if dir == "" {
		return
	}
	if err := d.Writable(dir); err != nil {
		r.add(Fail, name, "não consigo gravar em %s: %v", dir, err)
		return
	}
	r.add(OK, name, "dá para gravar em %s", dir)
}

func (r *runner) game(game string, d Deps) {
	r.writable("pasta resourcepacks", filepath.Join(game, "resourcepacks"), d)
	mods, _ := filepath.Glob(filepath.Join(game, "mods", "*.jar"))
	if len(mods) == 0 {
		r.add(Warn, "mods", "nenhum .jar em %s", filepath.Join(game, "mods"))
	} else {
		r.add(OK, "mods", "%d jars", len(mods))
	}
	switch era := ftbq.Era(game); era {
	case "snbt", "json5":
		r.add(OK, "FTB Quests", "encontrado (textos em arquivos de idioma: %s)", era)
	case "inline":
		r.add(OK, "FTB Quests", "encontrado, com o texto dentro dos capítulos (1.20.1 ou antes): o mtrad troca cada texto por {chave} e grava as chaves no resource pack; o install reescreve os capítulos (guarda .bak)")
	}
	b, err := os.ReadFile(filepath.Join(game, "options.txt"))
	if err != nil {
		r.add(Warn, "options.txt", "ainda não existe: abra o jogo uma vez")
		return
	}
	opts := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ":"); ok {
			opts[k] = v
		}
	}
	if lang := opts["lang"]; lang != "pt_br" {
		r.add(Warn, "idioma do jogo", "está em %q: troque para Português (Brasil) em Opções > Idioma", lang)
	} else {
		r.add(OK, "idioma do jogo", "pt_br")
	}
	zip := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	if _, err := os.Stat(zip); err == nil {
		if strings.Contains(opts["resourcePacks"], "file/mtrad-pt_br.zip") {
			r.add(OK, "pacote", "mtrad-pt_br.zip existe e está ativado")
		} else {
			r.add(Warn, "pacote", "mtrad-pt_br.zip existe mas não está ativado: Opções > Pacotes de recursos")
		}
	}
}
