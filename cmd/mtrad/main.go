// mtrad: traduz os textos de mods do Minecraft (Fabric) em etapas separadas.
// Cada etapa lê e grava arquivos em uma pasta de trabalho (-w).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"mtrad/internal/activate"
	"mtrad/internal/diff"
	"mtrad/internal/doctor"
	"mtrad/internal/evalset"
	"mtrad/internal/extract"
	"mtrad/internal/ftbq"
	"mtrad/internal/glossary"
	"mtrad/internal/install"
	"mtrad/internal/pack"
	"mtrad/internal/patchouli"
	"mtrad/internal/protect"
	"mtrad/internal/route"
	"mtrad/internal/runlog"
	"mtrad/internal/scan"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
	"mtrad/internal/validate"
	"mtrad/internal/vanilla"
	"mtrad/internal/version"
)

const usage = `uso: mtrad <etapa> [opções]

etapas, na ordem:
  version    -i PASTA_DA_INSTANCIA   descobre versão, loader e formato do pacote
  scan                               lista os jars e os arquivos de idioma
  extract                            lê os textos dos arquivos de idioma (en_us/pt_br)
  patchouli                          lê os textos dos livros do Patchouli
  ftbq                               lê os textos das missões do FTB Quests
  diff                               separa o que falta traduzir (todas as fontes)
  glossary   --tsv ARQ [--en ARQ --pt ARQ | --vanilla [--prism-data PASTA]]   monta o glossário
  protect    [--style brackets|xml|curly]    troca %s, §a, tags por marcas
  route      -c CONFIG               escolhe o motor de cada texto
  translate  -c CONFIG [--limit N]   traduz (pode ser interrompido e retomado)
  validate                           confere as traduções
  pack       [--out ARQ.zip]         monta o resource pack (e os arquivos do FTB Quests em work/out)
  install    [--overwrite] [--dry-run]       copia os arquivos do FTB Quests para a pasta do jogo
  run        -i PASTA -c CONFIG [--install [--overwrite]] [--activate]  todas as etapas em sequência
  activate   [--no-lang] [--dry-run] [--force]   liga o pacote e o idioma pt_br no options.txt (com o jogo fechado)
  verify     [--pack ARQ.zip]        confere o pacote montado contra os originais (documentos do Patchouli, chaves de idioma, pack.mcmeta)
  init       [-i PASTA] [-o ARQ] [--engine llama|ollama|gemini|libre|ct2]   cria o arquivo de configuração (sem -i não lê instância)
  doctor     [-c CONFIG]             confere o ambiente e diz o que falta
  evalset    -o PASTA [-n 500] [--seed S]   cria um conjunto de teste com traduções humanas
  logs       [-n N]                  resume o registro da última execução (ou da N-ésima anterior)
  unlock                             remove a trava de uma execução que caiu
  ui         [-c ARQ] [-w PASTA] [--demo]    abre o menu interativo (o mesmo que rodar "mtrad" sem argumentos num terminal)
  review     export|import|set|drop|status   correções à mão dos reprovados (CSV; mtrad review -h)
  config     [-c ARQ] show|get|set|add|remove|unset   edita o mtrad.json sem sed (mtrad config -h)

opção comum: -w PASTA_DE_TRABALHO (padrão: work)

códigos de saída: 0 ok, 1 erro, 2 uso errado, 3 terminou com itens falhos
`

// Config é o arquivo de configuração (JSON).
type Config struct {
	// Fontes de texto: lang, patchouli, ftbq. Vazio = todas.
	Sources []string `json:"sources,omitempty"`
	// Só traduz (only_mods) ou deixa de traduzir (skip_mods) os textos desses
	// mods, pelo id do mod ("kubejs" para kubejs/assets). O que já tem
	// tradução continua sendo reaproveitado.
	OnlyMods     []string         `json:"only_mods,omitempty"`
	SkipMods     []string         `json:"skip_mods,omitempty"`
	ProtectStyle string           `json:"protect_style,omitempty"`
	Route        route.Config     `json:"route"`
	Translate    translate.Config `json:"translate"`
	// Work e LastInstance só a interface (mtrad ui) usa: a pasta de trabalho
	// escolhida no setup e a última instância aberta. As etapas na linha de
	// comando continuam usando -w.
	Work         string `json:"work,omitempty"`
	LastInstance string `json:"last_instance,omitempty"`
	// progress recebe o andamento da tradução (a interface usa; vazio = a
	// linha "traduzidos N de M" no terminal).
	progress func(done, total int)
	Pack     struct {
		Description string `json:"description,omitempty"`
		Out         string `json:"out,omitempty"`
	} `json:"pack"`
	Glossary struct {
		TSV       []string `json:"tsv,omitempty"`
		En        string   `json:"vanilla_en,omitempty"`
		Pt        string   `json:"vanilla_pt,omitempty"`
		Vanilla   bool     `json:"vanilla,omitempty"`    // acha en_us/pt_br do Minecraft sozinho
		PrismData []string `json:"prism_data,omitempty"` // pastas de dados do Prism, se não forem as de costume
	} `json:"glossary"`
}

func loadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return parseConfig(b, path)
}

// parseConfig lê e confere um mtrad.json: campos desconhecidos, fontes, e as
// referências entre motores (reserva, motor de nova tentativa).
func parseConfig(b []byte, name string) (Config, error) {
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", name, err)
	}
	if c.ProtectStyle == "" {
		c.ProtectStyle = protect.StyleBrackets
	}
	if _, err := enabledSources(c.Sources); err != nil {
		return c, fmt.Errorf("%s: %w", name, err)
	}
	if err := c.Translate.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", name, err)
	}
	return c, nil
}

var allSources = []string{"lang", "patchouli", "ftbq"}

// sourceStage é a etapa que gera cada fonte e os arquivos dela.
var sourceStage = map[string]struct {
	stage string
	files []string
}{
	"lang":      {"extract", []string{"strings.jsonl"}},
	"patchouli": {"patchouli", []string{"strings.patchouli.jsonl", "docs.patchouli.jsonl"}},
	"ftbq":      {"ftbq", []string{"strings.ftbq.jsonl", "docs.ftbq.jsonl"}},
}

func enabledSources(list []string) ([]string, error) {
	if len(list) == 0 {
		return allSources, nil
	}
	seen := map[string]bool{}
	for _, n := range list {
		if _, ok := sourceStage[n]; !ok {
			return nil, fmt.Errorf("fonte desconhecida %q em sources (use lang, patchouli, ftbq)", n)
		}
		seen[n] = true
	}
	var out []string
	for _, n := range allSources { // ordem fixa
		if seen[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

type exitErr struct {
	code int
	msg  string
}

func (e exitErr) Error() string { return e.msg }

// Partial diz que a execução terminou, mas com itens falhos ou incompleta
// (código de saída 3); o registro da execução escreve "incompleto", não "erro".
func (e exitErr) Partial() bool { return e.code == schema.ExitPartial }

func partial(format string, a ...any) error {
	return exitErr{schema.ExitPartial, fmt.Sprintf(format, a...)}
}

func main() {
	if len(os.Args) < 2 && interactive() {
		// sem argumentos, num terminal: abre o menu
		if err := stageUI(nil); err != nil {
			var ee exitErr
			if errors.As(err, &ee) && ee.msg != "" {
				fmt.Fprintln(os.Stderr, ee.msg)
				os.Exit(ee.code)
			}
			fmt.Fprintln(os.Stderr, "erro:", err)
			os.Exit(schema.ExitError)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		io.WriteString(os.Stderr, usage)
		os.Exit(schema.ExitUsage)
	}
	if os.Args[1] == "--version" || os.Args[1] == "-V" {
		fmt.Println(versionLine())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		// depois do primeiro Ctrl+C o programa termina com calma (salva e monta o
		// pacote parcial); um segundo Ctrl+C volta ao comportamento normal e mata
		<-ctx.Done()
		stop()
	}()
	var rl *runlog.Log
	if registrado(os.Args[1]) {
		var lerr error
		if rl, lerr = runlog.Start(workDir(os.Args[2:]), os.Args, toolVersion); lerr != nil {
			fmt.Fprintln(os.Stderr, "aviso: não consegui abrir o registro em work/logs:", lerr)
		}
	}
	err := dispatch(ctx, os.Args[1], os.Args[2:])
	rl.Close(err)
	stop()
	if err == nil {
		return
	}
	var ee exitErr
	if errors.As(err, &ee) {
		fmt.Fprintln(os.Stderr, ee.msg)
		os.Exit(ee.code)
	}
	fmt.Fprintln(os.Stderr, "erro:", err)
	os.Exit(schema.ExitError)
}

type common struct {
	work   string
	config string
	force  bool
}

func newFlags(name string, c *common) *flag.FlagSet {
	fs := newFlagsNoForce(name, c)
	fs.BoolVar(&schema.SkipFreshness, "force", false, "ignora o aviso de arquivo alterado")
	return fs
}

// newFlagsNoForce são as opções comuns sem --force, para a etapa que dá outro
// sentido a ele (activate: não conferir se o jogo está aberto). Registrar
// --force duas vezes no mesmo conjunto faz o flag entrar em pânico.
func newFlagsNoForce(name string, c *common) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&c.work, "w", "work", "pasta de trabalho")
	fs.StringVar(&c.config, "c", "", "arquivo de configuração")
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return exitErr{schema.ExitUsage, ""}
	}
	return nil
}

func dispatch(ctx context.Context, cmd string, args []string) error {
	var c common
	switch cmd {
	case "version":
		fs := newFlags(cmd, &c)
		inst := fs.String("i", "", "pasta da instância do Prism")
		mc := fs.String("mc", "", "versão do Minecraft (se não achar sozinho)")
		loader := fs.String("loader", "", "loader")
		pf := fs.String("pack-format", "", "formato do pacote, por exemplo 34 ou 97.1")
		if err := parse(fs, args); err != nil {
			return err
		}
		if *inst == "" {
			return exitErr{schema.ExitUsage, "falta -i com a pasta da instância"}
		}
		return withLock(c.work, func() error { return stageVersion(c.work, *inst, *mc, *loader, *pf) })
	case "scan", "extract", "patchouli", "ftbq", "diff":
		fs := newFlags(cmd, &c)
		if err := parse(fs, args); err != nil {
			return err
		}
		var cfg Config
		if cmd == "diff" && c.config != "" { // only_mods e skip_mods
			var err error
			if cfg, err = loadConfig(c.config); err != nil {
				return err
			}
		}
		return withLock(c.work, func() error { return runStage(cmd, c.work, cfg) })
	case "glossary":
		fs := newFlags(cmd, &c)
		var tsv multi
		fs.Var(&tsv, "tsv", "arquivo inglês<TAB>português (repetível)")
		en := fs.String("en", "", "en_us.json do Minecraft")
		pt := fs.String("pt", "", "pt_br.json do Minecraft")
		van := fs.Bool("vanilla", false, "acha en_us e pt_br do Minecraft no Prism sozinho")
		var roots multi
		fs.Var(&roots, "prism-data", "pasta de dados do Prism (repetível; com --vanilla)")
		if err := parse(fs, args); err != nil {
			return err
		}
		if *van && (*en != "" || *pt != "") {
			return exitErr{schema.ExitUsage, "use --vanilla ou --en/--pt, não os dois"}
		}
		return withLock(c.work, func() error {
			return stageGlossary(c.work, glossaryArgs{TSV: tsv, En: *en, Pt: *pt, Vanilla: *van, Roots: roots})
		})
	case "protect":
		fs := newFlags(cmd, &c)
		style := fs.String("style", protect.StyleBrackets, "estilo das marcas")
		if err := parse(fs, args); err != nil {
			return err
		}
		return withLock(c.work, func() error { return runStage(cmd, c.work, Config{ProtectStyle: *style}) })
	case "route", "translate":
		fs := newFlags(cmd, &c)
		limit := fs.Int("limit", 0, "traduz só os N primeiros (teste)")
		if err := parse(fs, args); err != nil {
			return err
		}
		if c.config == "" {
			return exitErr{schema.ExitUsage, "falta -c com o arquivo de configuração"}
		}
		cfg, err := loadConfig(c.config)
		if err != nil {
			return err
		}
		cfg.Translate.Limit = *limit
		return withLock(c.work, func() error { return runStageCtx(ctx, cmd, c.work, cfg) })
	case "validate":
		fs := newFlags(cmd, &c)
		if err := parse(fs, args); err != nil {
			return err
		}
		return withLock(c.work, func() error { return runStage(cmd, c.work, Config{}) })
	case "pack":
		fs := newFlags(cmd, &c)
		out := fs.String("out", "", "arquivo .zip de saída")
		desc := fs.String("description", "", "descrição do pacote")
		if err := parse(fs, args); err != nil {
			return err
		}
		cfg := Config{}
		cfg.Pack.Out, cfg.Pack.Description = *out, *desc
		return withLock(c.work, func() error { return runStage(cmd, c.work, cfg) })
	case "install":
		fs := newFlags(cmd, &c)
		ow := fs.Bool("overwrite", false, "substitui arquivos diferentes (guarda o antigo em .bak)")
		dry := fs.Bool("dry-run", false, "só mostra o que faria")
		if err := parse(fs, args); err != nil {
			return err
		}
		return withLock(c.work, func() error { return stageInstall(c.work, *ow, *dry) })
	case "doctor":
		fs := newFlags(cmd, &c)
		if err := parse(fs, args); err != nil {
			return err
		}
		return stageDoctor(ctx, c.work, c.config)
	case "init":
		fs := newFlags(cmd, &c)
		inst := fs.String("i", "", "pasta da instância do Prism (opcional no init)")
		out := fs.String("o", "mtrad.json", "arquivo de configuração a criar")
		engine := fs.String("engine", "llama", "llama, ollama, gemini, libre ou ct2")
		model := fs.String("model", "", "nome do modelo (o padrão depende do motor)")
		overwrite := fs.Bool("overwrite", false, "substitui o arquivo se já existir")
		if err := parse(fs, args); err != nil {
			return err
		}
		return stageInit(*inst, *out, *engine, *model, *overwrite)
	case "run":
		fs := newFlags(cmd, &c)
		inst := fs.String("i", "", "pasta da instância do Prism")
		mc := fs.String("mc", "", "versão do Minecraft")
		doInstall := fs.Bool("install", false, "no fim, copia os arquivos do FTB Quests para o jogo (sem sobrescrever, a menos que use --overwrite)")
		ow := fs.Bool("overwrite", false, "com --install: substitui arquivos diferentes (guarda o antigo em .bak)")
		doActivate := fs.Bool("activate", false, "no fim, liga o pacote e o idioma pt_br no options.txt (o jogo precisa estar fechado)")
		limit := fs.Int("limit", 0, "traduz só os N primeiros (teste)")
		if err := parse(fs, args); err != nil {
			return err
		}
		if *inst == "" || c.config == "" {
			return exitErr{schema.ExitUsage, "falta -i e -c"}
		}
		cfg, err := loadConfig(c.config)
		if err != nil {
			return err
		}
		if *ow && !*doInstall {
			return exitErr{schema.ExitUsage, "--overwrite só vale junto com --install"}
		}
		cfg.Translate.Limit = *limit
		return withLock(c.work, func() error {
			return runAll(ctx, c.work, *inst, *mc, cfg, runOpts{Install: *doInstall, Overwrite: *ow, Activate: *doActivate})
		})
	case "activate":
		fs := newFlagsNoForce(cmd, &c)
		noLang := fs.Bool("no-lang", false, "só liga o pacote, sem mudar o idioma")
		dry := fs.Bool("dry-run", false, "só mostra o que mudaria")
		force := fs.Bool("force", false, "não confere se o jogo está aberto")
		if err := parse(fs, args); err != nil {
			return err
		}
		var cfg Config
		if c.config != "" {
			var err error
			if cfg, err = loadConfig(c.config); err != nil {
				return err
			}
		}
		return withLock(c.work, func() error { return stageActivate(os.Stdout, c.work, cfg, !*noLang, *dry, *force) })
	case "evalset":
		fs := newFlags(cmd, &c)
		out := fs.String("o", "", "pasta de trabalho do conjunto de teste")
		n := fs.Int("n", 500, "quantidade de textos")
		seed := fs.String("seed", "mtrad", "semente do sorteio")
		if err := parse(fs, args); err != nil {
			return err
		}
		if *out == "" {
			return exitErr{schema.ExitUsage, "falta -o"}
		}
		k, err := evalset.Build(c.work, *out, *n, *seed)
		if err != nil {
			return err
		}
		fmt.Printf("evalset: %d textos em %s; agora rode protect, route, translate e validate com -w %s e depois python3 tools/eval.py %s\n", k, *out, *out, *out)
		return nil
	case "logs":
		fs := newFlags(cmd, &c)
		n := fs.Int("n", 0, "0 = a última execução, 1 = a anterior, ...")
		if err := parse(fs, args); err != nil {
			return err
		}
		return stageLogs(c.work, *n)
	case "review":
		fs := newFlags(cmd, &c)
		if err := fs.Parse(args); err != nil {
			io.WriteString(os.Stderr, reviewUsage)
			return exitErr{schema.ExitUsage, ""}
		}
		return withLock(c.work, func() error { return stageReview(os.Stdout, c.work, fs.Args()) })
	case "config":
		fs := flag.NewFlagSet("config", flag.ContinueOnError)
		path := fs.String("c", "mtrad.json", "arquivo de configuração")
		if err := fs.Parse(args); err != nil {
			io.WriteString(os.Stderr, configUsage)
			return exitErr{schema.ExitUsage, ""}
		}
		return stageConfig(os.Stdout, *path, fs.Args())
	case "ui":
		return stageUI(args)
	case "verify":
		fs := newFlags(cmd, &c)
		zp := fs.String("pack", "", "arquivo .zip a conferir (padrão: o da instância)")
		if err := parse(fs, args); err != nil {
			return err
		}
		return stageVerify(os.Stdout, c.work, *zp)
	case "unlock":
		fs := newFlags(cmd, &c)
		if err := parse(fs, args); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(c.work, ".lock")); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("trava removida")
		return nil
	}
	io.WriteString(os.Stderr, usage)
	return exitErr{schema.ExitUsage, "etapa desconhecida: " + cmd}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func withLock(work string, fn func() error) error {
	un, err := schema.Lock(work)
	if err != nil {
		return err
	}
	defer un()
	return fn()
}

func stageVersion(work, inst, mc, loader, pf string) error {
	// a etapa version abre uma nova execução: zera os avisos da anterior
	_ = os.Remove(filepath.Join(work, "errors.jsonl"))
	o := version.Options{InstanceDir: inst, McVersion: mc, Loader: loader}
	if pf != "" {
		maj, min, _ := strings.Cut(pf, ".")
		var err error
		if o.PackMajor, err = strconv.Atoi(maj); err != nil {
			return exitErr{schema.ExitUsage, "--pack-format inválido: " + pf}
		}
		if min != "" {
			if o.PackMinor, err = strconv.Atoi(min); err != nil {
				return exitErr{schema.ExitUsage, "--pack-format inválido: " + pf}
			}
		}
	}
	in, err := version.Run(work, o)
	if err != nil {
		return err
	}
	fmt.Printf("version: Minecraft %s, %s %s, formato de pacote %d.%d, pasta do jogo %s\n", in.McVersion, in.Loader, in.LoaderVersion, in.PackFormat.Major, in.PackFormat.Minor, in.GameDir)
	for _, w := range in.Warnings {
		fmt.Println("  aviso:", w)
	}
	return nil
}

type glossaryArgs struct {
	TSV     []string
	En, Pt  string
	Vanilla bool
	Roots   []string
}

func stageGlossary(work string, a glossaryArgs) error {
	var terms []glossary.Term
	for _, f := range a.TSV {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		t, err := glossary.ParseTSV(b)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		terms = append(terms, t...)
	}
	if (a.En == "") != (a.Pt == "") {
		return exitErr{schema.ExitUsage, "--en e --pt vão juntos"}
	}
	var eb, pb []byte
	if a.En != "" {
		var err error
		if eb, err = os.ReadFile(a.En); err != nil {
			return err
		}
		if pb, err = os.ReadFile(a.Pt); err != nil {
			return err
		}
	}
	if a.Vanilla {
		var in schema.Instance
		if err := schema.ReadJSON(filepath.Join(work, "instance.json"), &in, schema.VInstance); err != nil {
			return fmt.Errorf("--vanilla precisa do instance.json (rode version antes): %w", err)
		}
		home, _ := os.UserHomeDir()
		f := vanilla.Find(in, vanilla.Roots(in, a.Roots, home))
		for _, n := range f.Notes {
			fmt.Println("  aviso:", n)
		}
		if f.En != nil && f.Pt != nil {
			fmt.Printf("  vanilla: en_us de %s\n           pt_br de %s\n", f.EnFrom, f.PtFrom)
			eb, pb = f.En, f.Pt
		} else if len(terms) == 0 {
			return exitErr{schema.ExitUsage, "não achei os textos do Minecraft e não há --tsv; use --en e --pt com os arquivos"}
		}
	}
	if eb != nil {
		t, err := glossary.FromLangFiles(eb, pb)
		if err != nil {
			return err
		}
		// os termos escritos à mão vêm depois e ganham
		terms = append(t, terms...)
	}
	if len(terms) == 0 {
		return exitErr{schema.ExitUsage, "nenhum termo: use --tsv, --vanilla ou --en/--pt"}
	}
	g := glossary.New(terms)
	if err := g.Save(filepath.Join(work, "glossary.json")); err != nil {
		return err
	}
	fmt.Printf("glossary: %d termos, versão %s\n", len(g.Terms), g.Version)
	return nil
}

func stageInstall(work string, overwrite, dry bool) error {
	res, err := install.Run(work, overwrite, dry)
	if err != nil {
		return err
	}
	if len(res.Items) == 0 {
		fmt.Println("install: nada para instalar (o pack não gerou arquivos em work/out/ftbquests)")
		return nil
	}
	for _, it := range res.Items {
		line := fmt.Sprintf("  %s: %s", it.Rel, it.Action)
		if it.Bak != "" {
			line += " → " + it.Bak
		}
		if it.Note != "" {
			line += " (" + it.Note + ")"
		}
		fmt.Println(line)
	}
	fmt.Printf("install: %d arquivos em %s\n", len(res.Items), res.GameDir)
	refused := 0
	for _, it := range res.Items {
		if it.Action == install.Refused {
			refused++
		}
	}
	if res.Conflicts > 0 || refused > 0 {
		return partial("install: %d arquivos já existem e são diferentes (use --overwrite), %d recusados", res.Conflicts, refused)
	}
	return nil
}

// verifyLines é o resumo de pack.Verify em linhas de texto.
func verifyLines(rep pack.VerifyReport) []string {
	l := []string{fmt.Sprintf("verify: %s", rep.Zip),
		fmt.Sprintf("verify: %d documentos do Patchouli iguais aos originais fora dos textos traduzidos (%d não estão no pacote e ficam em inglês), %d arquivos de idioma com %d chaves", rep.Docs-rep.DocsBad, rep.DocsMissing, rep.LangFiles, rep.Keys)}
	for _, p := range rep.Problems {
		l = append(l, "  problema: "+p)
	}
	return l
}

func stageVerify(w io.Writer, work, zipPath string) error {
	rep, err := pack.Verify(work, zipPath)
	if err != nil {
		return err
	}
	for _, l := range verifyLines(rep) {
		fmt.Fprintln(w, l)
	}
	if len(rep.Problems) > 0 {
		return partial("verify: %d problemas no pacote", len(rep.Problems))
	}
	fmt.Fprintln(w, "verify: nenhum problema: o pacote só mudou os textos que o mtrad traduziu")
	return nil
}

func stageActivate(w io.Writer, work string, cfg Config, setLang, dry, force bool) error {
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(work, "instance.json"), &in, schema.VInstance); err != nil {
		return fmt.Errorf("não achei o instance.json em %s (rode a etapa version antes): %w", work, err)
	}
	file := "mtrad-pt_br.zip"
	if cfg.Pack.Out != "" {
		// só vale se o pacote está na pasta resourcepacks do jogo
		if filepath.Clean(filepath.Dir(cfg.Pack.Out)) == filepath.Clean(filepath.Join(in.GameDir, "resourcepacks")) {
			file = filepath.Base(cfg.Pack.Out)
		} else {
			fmt.Fprintf(w, "  aviso: pack.out (%s) não está em %s; ativando o nome padrão %s\n", cfg.Pack.Out, filepath.Join(in.GameDir, "resourcepacks"), file)
		}
	}
	r, err := activate.Run(activate.Options{GameDir: in.GameDir, PackFormat: in.PackFormat, PackFile: file, SetLang: setLang, DryRun: dry, Force: force})
	if err != nil {
		return err
	}
	for _, n := range r.Notes {
		fmt.Fprintln(w, "  aviso:", n)
	}
	switch {
	case !r.Changed:
		fmt.Fprintf(w, "activate: já estava tudo ligado em %s\n", r.Path)
	case dry:
		fmt.Fprintf(w, "activate: (simulação) ligaria o pacote %s", r.PackEntry)
		if r.LangChanged {
			fmt.Fprintf(w, " e o idioma %s", r.Lang)
		}
		fmt.Fprintf(w, " em %s\n", r.Path)
	default:
		fmt.Fprintf(w, "activate: pacote %s", r.PackEntry)
		if r.LangChanged {
			fmt.Fprintf(w, " e idioma %s", r.Lang)
		}
		fmt.Fprintf(w, " ligados em %s", r.Path)
		if r.Backup != "" {
			fmt.Fprintf(w, " (cópia do original em %s)", r.Backup)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func stageDoctor(ctx context.Context, work, config string) error {
	in := doctor.Input{Work: work, Deps: doctor.DefaultDeps()}
	if config != "" {
		cfg, err := loadConfig(config)
		if err != nil {
			return err
		}
		in.Route, in.Translate = &cfg.Route, &cfg.Translate
	}
	checks := doctor.Run(ctx, in)
	for _, c := range checks {
		fmt.Printf("[%s] %s: %s\n", c.Level, c.Name, c.Msg)
	}
	switch doctor.Worst(checks) {
	case doctor.Fail:
		return exitErr{schema.ExitError, "doctor: há erros que vão impedir a execução"}
	case doctor.Warn:
		fmt.Println("doctor: há avisos; leia as linhas [aviso]")
	default:
		fmt.Println("doctor: tudo certo")
	}
	return nil
}

func runStage(name, work string, cfg Config) error {
	return runStageCtx(context.Background(), name, work, cfg)
}

func runStageCtx(ctx context.Context, name, work string, cfg Config) error {
	errs, err := schema.OpenErrLog(work, name)
	if err != nil {
		return err
	}
	defer errs.Close()
	defer func() {
		if n := errs.Count(); n > 0 {
			fmt.Printf("  %d avisos/erros em %s\n", n, filepath.Join(work, "errors.jsonl"))
		}
	}()
	switch name {
	case "scan":
		inv, err := scan.Run(work, errs)
		if err != nil {
			return err
		}
		langs := 0
		for _, j := range inv.Jars {
			langs += len(j.Langs)
		}
		fmt.Printf("scan: %d jars (contando os aninhados), %d arquivos de idioma\n", len(inv.Jars), langs)
	case "extract":
		c, err := extract.Run(work, errs)
		if err != nil {
			return err
		}
		fmt.Printf("extract: %d textos em %d arquivos de %d jars; %d já com pt_br; %d duplicados ignorados\n", c.Keys, c.Files, c.Jars, c.ExistingPt, c.Duplicates)
		if c.FileErrors > 0 {
			return partial("extract: %d arquivos não puderam ser lidos (veja errors.jsonl)", c.FileErrors)
		}
	case "patchouli":
		c, err := patchouli.Run(work, errs)
		if err != nil {
			return err
		}
		fmt.Printf("patchouli: %d textos em %d documentos de %d livros (%d jars); %d já com pt_br; %d pulados\n", c.Keys, c.Docs, c.Books, c.Jars, c.ExistingPt, c.Skipped)
		if c.FileErrors > 0 {
			return partial("patchouli: %d arquivos não puderam ser lidos (veja errors.jsonl)", c.FileErrors)
		}
	case "ftbq":
		c, err := ftbq.Run(work, errs)
		if err != nil {
			return err
		}
		if c.Era == "" {
			fmt.Println("ftbq: não há FTB Quests nesta instância")
			break
		}
		fmt.Printf("ftbq: formato %s; %d textos em %d arquivos; %d já com pt_br; %d pulados\n", c.Era, c.Keys, c.Files, c.ExistingPt, c.Skipped)
		if c.FileErrors > 0 {
			return partial("ftbq: %d arquivos não puderam ser lidos (veja errors.jsonl)", c.FileErrors)
		}
	case "diff":
		c, err := diff.Run(work, diff.Filter{Only: cfg.OnlyMods, Skip: cfg.SkipMods})
		if err != nil {
			return err
		}
		fmt.Printf("diff: %d textos; %d reaproveitados; %d a traduzir (%d sem tradução, %d iguais ao inglês)\n", c.Total, c.Reused, c.Missing+c.Identical, c.Missing, c.Identical)
		if c.Filtered > 0 {
			fmt.Printf("diff: %d textos ficam em inglês por causa de only_mods/skip_mods\n", c.Filtered)
		}
	case "protect":
		style := cfg.ProtectStyle
		if style == "" {
			style = protect.StyleBrackets
		}
		c, err := protect.Run(work, style)
		if err != nil {
			return err
		}
		fmt.Printf("protect: %d textos, %d com marcas (%d marcas no total), estilo %s\n", c.Texts, c.WithTokens, c.Tokens, style)
	case "route":
		counts, err := route.Run(work, cfg.Route)
		if err != nil {
			return err
		}
		fmt.Printf("route: %v\n", counts)
	case "translate":
		last := 0
		cfg.Translate.RequestLog = runlog.RequestsPath()
		cfg.Translate.Notice = func(m string) { fmt.Println(m) }
		report := cfg.progress
		if report == nil {
			report = func(done, total int) {
				if done-last >= 200 || done == total {
					last = done
					fmt.Fprintf(os.Stderr, "\r  traduzidos %d de %d", done, total)
				}
			}
		}
		st, err := translate.Run(ctx, work, cfg.Translate, errs, report)
		if cfg.progress == nil {
			fmt.Fprintln(os.Stderr)
		}
		if errors.Is(err, context.Canceled) {
			fmt.Printf("translate: interrompido; %d traduzidos nesta execução estão salvos (rode de novo para continuar)\n", st.Translated+st.CacheHits)
			return partial("translate: interrompido")
		}
		if err != nil {
			return err
		}
		fmt.Printf("translate: %d traduzidos, %d do cache, %d falharam\n", st.Translated, st.CacheHits, st.Failed)
		for from, n := range st.Fallback {
			fmt.Printf("  %d textos do motor %s foram para o motor reserva\n", n, from)
		}
		if st.Retried > 0 || st.Stale > 0 {
			fmt.Printf("  %d refeitos por reprovação na validação, %d porque o texto de origem mudou\n", st.Retried, st.Stale)
		}
		if st.CacheFail > 0 {
			fmt.Printf("  aviso: %d traduções não foram gravadas no cache (veja errors.jsonl); numa nova execução elas serão pedidas de novo\n", st.CacheFail)
			return partial("translate: falha ao gravar o cache")
		}
		for e, why := range st.Stopped {
			fmt.Printf("  motor %s parou: %s (rode de novo para continuar)\n", e, why)
		}
		if st.Failed > 0 || len(st.Stopped) > 0 {
			return partial("translate: terminou incompleto")
		}
	case "validate":
		var g *glossary.Glossary
		if gl, err := glossary.Load(filepath.Join(work, "glossary.json")); err == nil {
			g = gl
		}
		c, err := validate.Run(work, validate.Options{Gloss: g})
		if err != nil {
			return err
		}
		fmt.Printf("validate: %d aprovados, %d reprovados, %d sem tradução, %d com aviso (relatório em %s)\n", c.OK, c.Failed, c.Pending, c.Warnings, filepath.Join(work, "report.md"))
		if c.Failed > 0 || c.Pending > 0 {
			return partial("validate: há textos reprovados ou sem tradução")
		}
	case "pack":
		st, p, err := pack.Run(work, cfg.Pack.Out, cfg.Pack.Description)
		if err != nil {
			return err
		}
		if p != "" {
			fmt.Printf("pack: %s com %d textos de idioma em %d namespaces e %d documentos do Patchouli (%d textos)\n", p, st.Keys, st.Namespaces, st.PatchouliDocs, st.PatchouliKeys)
		}
		if st.FTBFiles > 0 {
			fmt.Printf("pack: %d arquivos do FTB Quests (%d textos) em %s; rode `mtrad install` para copiar para o jogo\n", st.FTBFiles, st.FTBKeys, pack.FTBOutDir(work))
		}
		fmt.Printf("pack: %d textos reaproveitados e %d traduzidos agora\n", st.FromReused, st.FromOK)
		for _, w := range st.Warnings {
			fmt.Println("  aviso:", w)
		}
	}
	return nil
}

// runOpts são os passos extras depois do pack.
type runOpts struct {
	Install, Overwrite, Activate bool
}

// partialKeeper devolve a função que deixa passar os resultados "terminou com
// itens falhos" (código 3), guardando o primeiro, e a que devolve esse primeiro.
func partialKeeper() (keep func(error) error, worst func() error) {
	var w error
	keep = func(err error) error {
		var ee exitErr
		if errors.As(err, &ee) && ee.code == schema.ExitPartial {
			if w == nil {
				w = err
			}
			return nil
		}
		return err
	}
	return keep, func() error { return w }
}

// readStages roda version, scan, as fontes ligadas e diff: tudo o que lê os mods,
// sem traduzir. Fontes desligadas não deixam arquivos de uma execução anterior
// para o diff e o pack pegarem.
//
// stop é chamada entre as etapas e pode devolver um erro para parar (a interface
// usa para cancelar a leitura; o run passa nil: uma execução interrompida ainda
// lê os mods e monta o pacote parcial).
func readStages(stop func() error, work, inst, mc string, cfg Config, keep func(error) error) error {
	srcs, err := enabledSources(cfg.Sources)
	if err != nil {
		return err
	}
	if err := stageVersion(work, inst, mc, "", ""); err != nil {
		return err
	}
	on := map[string]bool{}
	for _, n := range srcs {
		on[n] = true
	}
	for _, n := range allSources {
		if on[n] {
			continue
		}
		ss := sourceStage[n]
		_ = os.Remove(filepath.Join(work, ss.stage+".done.json"))
		for _, f := range ss.files {
			_ = os.Remove(filepath.Join(work, f))
		}
		if n != "lang" {
			_ = os.RemoveAll(filepath.Join(work, "docs", n))
		}
	}
	if err := keep(runStage("scan", work, cfg)); err != nil {
		return err
	}
	for _, n := range srcs {
		if stop != nil {
			if err := stop(); err != nil {
				return err
			}
		}
		if err := keep(runStage(sourceStage[n].stage, work, cfg)); err != nil {
			return err
		}
	}
	return keep(runStage("diff", work, cfg))
}

func runAll(ctx context.Context, work, inst, mc string, cfg Config, ro runOpts) error {
	keep, worst := partialKeeper()
	if err := readStages(nil, work, inst, mc, cfg, keep); err != nil {
		return err
	}
	if len(cfg.Glossary.TSV) > 0 || cfg.Glossary.En != "" || cfg.Glossary.Vanilla {
		if err := stageGlossary(work, glossaryArgs{TSV: cfg.Glossary.TSV, En: cfg.Glossary.En, Pt: cfg.Glossary.Pt, Vanilla: cfg.Glossary.Vanilla, Roots: cfg.Glossary.PrismData}); err != nil {
			return err
		}
	}
	for _, s := range []string{"protect", "route", "translate", "validate", "pack"} {
		if s == "validate" && ctx.Err() != nil {
			fmt.Println("interrompido: montando o pacote com o que já foi traduzido (Ctrl+C de novo cancela)")
		}
		if err := keep(runStageCtx(ctx, s, work, cfg)); err != nil {
			return err
		}
	}
	if ro.Install {
		if err := keep(stageInstall(work, ro.Overwrite, false)); err != nil {
			return err
		}
	}
	if ro.Activate {
		if err := stageActivate(os.Stdout, work, cfg, true, false, false); err != nil {
			return err
		}
	}
	return worst()
}
