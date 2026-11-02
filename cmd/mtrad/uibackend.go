package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mtrad/internal/diff"
	"mtrad/internal/doctor"
	"mtrad/internal/install"
	"mtrad/internal/pack"
	"mtrad/internal/review"
	"mtrad/internal/runlog"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
	"mtrad/internal/ui"
	"mtrad/internal/validate"
	"mtrad/internal/version"
)

// uiBackend liga a interface (internal/ui) às etapas do mtrad. Cada método
// chama o mesmo código que o comando equivalente, e as etapas que escrevem no
// terminal têm a saída desviada para o registro em work/logs (a tela cheia não
// pode receber texto solto).
type uiBackend struct {
	mu      sync.Mutex
	version string
	home    string
	cfgPath string
	root    string // pasta de trabalho escolhida (cada instância usa ela ou uma subpasta)
	cfg     Config
	cfgOK   bool
	cfgErr  error

	inst    *ui.Instance
	instDir string
	gameDir string
	work    string

	// caches, descartados quando algo muda (invalidate)
	failed    []ui.Failed
	failedOK  bool
	stats     []diff.ModStat
	statsOK   bool
	statusAt  time.Time
	statusVal ui.Status

	pmu      sync.Mutex // sondagem do Ollama
	probeAt  time.Time
	probeVal ollamaState

	// ollamaBase troca a URL do Ollama nos testes ("" = a da configuração).
	ollamaBase string
}

type ollamaState struct {
	up, idle, gpu bool
	models        []string
}

// newUIBackend lê o mtrad.json (se existir) e prepara o backend. root vazio
// usa a pasta escolhida no arquivo ou ~/mtrad-trabalho.
func newUIBackend(toolVersion, cfgPath, root, home string) *uiBackend {
	b := &uiBackend{version: toolVersion, home: home, cfgPath: cfgPath, root: root}
	b.reload()
	return b
}

// defaultConfigPath escolhe onde está (ou ficará) o mtrad.json: ./mtrad.json se
// existir, senão ~/.config/mtrad/mtrad.json.
func defaultConfigPath(home string) string {
	if _, err := os.Stat("mtrad.json"); err == nil {
		abs, _ := filepath.Abs("mtrad.json")
		return abs
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "mtrad", "mtrad.json")
}

func expandHome(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func (b *uiBackend) reload() {
	b.cfg, b.cfgOK, b.cfgErr = Config{}, false, nil
	if _, err := os.Stat(b.cfgPath); err == nil {
		c, err := loadConfig(b.cfgPath)
		if err != nil {
			b.cfgErr = err
		} else {
			b.cfg, b.cfgOK = c, true
		}
	}
	if b.root == "" {
		b.root = b.cfg.Work
	}
	if b.root == "" {
		b.root = filepath.Join(b.home, "mtrad-trabalho")
	}
	b.root = expandHome(b.root, b.home)
	if b.instDir != "" {
		b.work = workFor(b.root, b.instDir)
	}
	b.invalidate()
}

func (b *uiBackend) invalidate() {
	b.failedOK, b.statsOK = false, false
	b.statusAt = time.Time{}
}

// ---------- configuração ----------

func (b *uiBackend) IsDemo() bool { return false }

func (b *uiBackend) SetupDone() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfgOK
}

// engineKind diz qual das opções do setup uma configuração de motor é.
func engineKind(ec translate.EngineConfig) string {
	switch ec.Type {
	case "libretranslate":
		return "libre"
	case "worker":
		return "ct2"
	}
	switch {
	case strings.Contains(ec.BaseURL, "generativelanguage"):
		return "gemini"
	case strings.Contains(ec.BaseURL, ":11434"):
		return "ollama"
	}
	return "llama"
}

func (b *uiBackend) Defaults() ui.Setup {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := ui.Setup{Engine: "ollama", Model: "translategemma:4b", Sources: allSources, Work: b.root, Config: b.cfgPath}
	if b.cfgOK {
		if ec, ok := b.cfg.Translate.Engines[b.cfg.Route.Default]; ok {
			s.Engine, s.Model = engineKind(ec), ec.Model
		}
		if len(b.cfg.Sources) > 0 {
			s.Sources = b.cfg.Sources
		}
	}
	return s
}

// SaveSetup grava o mtrad.json. Se já existe um válido, só troca o motor, as
// fontes e a pasta de trabalho (o resto fica como o usuário deixou) e guarda o
// anterior em .bak; se existe mas está quebrado, vai para .invalido.bak.
func (b *uiBackend) SaveSetup(s ui.Setup) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	path := expandHome(s.Config, b.home)
	if path == "" {
		path = b.cfgPath
	}
	engine, model := s.Engine, s.Model
	if model == "padrão do motor" {
		model = ""
	}
	c, _, err := initConfig(engine, model)
	if err != nil {
		return err
	}
	name := c.Route.Default
	var ecMap map[string]any
	raw, _ := json.Marshal(c.Translate.Engines[name])
	if err := json.Unmarshal(raw, &ecMap); err != nil {
		return err
	}
	work := expandHome(s.Work, b.home)
	srcs := s.Sources
	allOn := len(srcs) == 0 || len(srcs) == len(allSources)
	if _, statErr := os.Stat(path); statErr == nil {
		if _, err := loadConfig(path); err != nil { // arquivo quebrado: guarda e recomeça
			old, _ := os.ReadFile(path)
			if err := os.WriteFile(path+".invalido.bak", old, 0o644); err != nil {
				return err
			}
			os.Remove(path)
		}
	}
	if _, statErr := os.Stat(path); statErr != nil { // não existe: cria completo
		c.Work = work
		if !allOn {
			c.Sources = srcs
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(c); err != nil {
			return err
		}
		if err := writeFileAtomic(path, buf.Bytes()); err != nil {
			return err
		}
	} else {
		err := editConfigFile(path, func(m map[string]any) error {
			if err := setKey(m, "route.default", name); err != nil {
				return err
			}
			if err := setKey(m, "translate.engines."+name, ecMap); err != nil {
				return err
			}
			if allOn {
				_ = unsetKey(m, "sources")
			} else {
				items := make([]any, len(srcs))
				for i, x := range srcs {
					items[i] = x
				}
				m["sources"] = items
			}
			return setKey(m, "work", work)
		})
		if err != nil {
			return err
		}
	}
	b.cfgPath, b.root = path, work
	b.reload()
	if !b.cfgOK {
		return fmt.Errorf("gravei %s, mas ele não passou na conferência: %v", path, b.cfgErr)
	}
	return nil
}

// editCfg aplica uma alteração ao mtrad.json e o relê.
func (b *uiBackend) editCfg(fn func(m map[string]any) error) error {
	if !b.cfgOK {
		return errors.New("ainda não há configuração: faça o setup primeiro")
	}
	if err := editConfigFile(b.cfgPath, fn); err != nil {
		return err
	}
	b.reload()
	return nil
}

// runtimeCfg é a configuração com o que depende da instância: o cache fica na
// pasta de trabalho escolhida, para todas as instâncias o dividirem.
func (b *uiBackend) runtimeCfg() Config {
	c := b.cfg
	if c.Translate.CachePath == "" && b.work != b.root {
		c.Translate.CachePath = filepath.Join(b.root, "cache", "cache.jsonl")
	}
	c.progress = nil
	return c
}

// ---------- instâncias ----------

func prismRoots(home string, extra []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range extra {
		add(e)
	}
	add(filepath.Join(home, ".var", "app", "org.prismlauncher.PrismLauncher", "data", "PrismLauncher"))
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	add(filepath.Join(data, "PrismLauncher"))
	add(filepath.Join(home, "Library", "Application Support", "PrismLauncher"))
	return out
}

func instName(dir string) string {
	if f, err := os.Open(filepath.Join(dir, "instance.cfg")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if n, ok := strings.CutPrefix(sc.Text(), "name="); ok && strings.TrimSpace(n) != "" {
				return strings.TrimSpace(n)
			}
		}
	}
	return filepath.Base(dir)
}

func packFormatText(pf schema.PackFormat) string {
	if pf.Minor > 0 {
		return fmt.Sprintf("%d.%d", pf.Major, pf.Minor)
	}
	return fmt.Sprint(pf.Major)
}

func toUIInstance(dir string, in schema.Instance) ui.Instance {
	u := ui.Instance{Name: instName(dir), Path: dir, MC: "?", Loader: "?", LoaderVer: "?", PackFormat: "?"}
	if in.McVersion != "" {
		u.MC = in.McVersion
	}
	if in.Loader != "" {
		u.Loader = in.Loader
	}
	if in.LoaderVersion != "" {
		u.LoaderVer = in.LoaderVersion
	}
	if in.PackFormat.Major > 0 {
		u.PackFormat = packFormatText(in.PackFormat)
	}
	return u
}

// Instances procura as instâncias nas pastas de costume do Prism (Flatpak,
// instalação normal, macOS); a última usada vem primeiro.
func (b *uiBackend) Instances() []ui.Instance {
	b.mu.Lock()
	defer b.mu.Unlock()
	var list []ui.Instance
	for _, root := range prismRoots(b.home, b.cfg.Glossary.PrismData) {
		ents, err := os.ReadDir(filepath.Join(root, "instances"))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(root, "instances", e.Name())
			if _, err := os.Stat(filepath.Join(dir, "mmc-pack.json")); err != nil {
				if _, err := os.Stat(filepath.Join(dir, "instance.cfg")); err != nil {
					continue
				}
			}
			in, _ := version.Detect(version.Options{InstanceDir: dir})
			list = append(list, toUIInstance(dir, in))
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		li, lj := list[i].Path == b.cfg.LastInstance, list[j].Path == b.cfg.LastInstance
		if li != lj {
			return li
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list
}

var reSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(name string) string {
	s := strings.Trim(reSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "instancia"
	}
	return s
}

// workFor escolhe a pasta de trabalho de uma instância: a própria raiz, se ela
// já guarda o trabalho dessa instância (é o caso de quem já usava
// ~/mtrad-trabalho), senão uma subpasta com o nome da instância. Assim duas
// instâncias nunca misturam os arquivos de etapa.
func workFor(root, instDir string) string {
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(root, "instance.json"), &in, schema.VInstance); err == nil && in.InstanceDir != "" {
		if filepath.Clean(in.InstanceDir) == filepath.Clean(instDir) {
			return root
		}
		return filepath.Join(root, slug(instName(instDir)))
	}
	return filepath.Join(root, slug(instName(instDir)))
}

func (b *uiBackend) SelectInstance(in ui.Instance) (ui.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := expandHome(in.Path, b.home)
	det, err := version.Detect(version.Options{InstanceDir: dir})
	if err != nil {
		return in, err
	}
	abs := det.InstanceDir
	full := toUIInstance(abs, det)
	b.inst, b.instDir, b.gameDir = &full, abs, det.GameDir
	b.work = workFor(b.root, abs)
	b.invalidate()
	if b.cfgOK && b.cfg.LastInstance != abs {
		_ = b.editCfg(func(m map[string]any) error { m["last_instance"] = abs; return nil }) // melhor esforço
	}
	return full, nil
}

func (b *uiBackend) needInstance() error {
	if b.inst == nil {
		return errors.New("nenhuma instância escolhida")
	}
	if !b.cfgOK {
		return errors.New("ainda não há configuração: faça o setup primeiro")
	}
	return nil
}

// ---------- estado ----------

func (b *uiBackend) ollamaURL() string {
	if b.ollamaBase != "" {
		return b.ollamaBase
	}
	if ec, ok := b.cfg.Translate.Engines[b.cfg.Route.Default]; ok && engineKind(ec) == "ollama" {
		return strings.TrimSuffix(strings.TrimRight(ec.BaseURL, "/"), "/v1")
	}
	return "http://127.0.0.1:11434"
}

func httpJSON(url string, v any) error {
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: código %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// probeOllama pergunta ao Ollama se está no ar, quais modelos tem e se o
// modelo está carregado na GPU. O resultado vale por 10 s (a tela pede o
// estado a cada tecla).
func (b *uiBackend) probeOllama(model string, force bool) ollamaState {
	b.pmu.Lock()
	defer b.pmu.Unlock()
	if !force && time.Since(b.probeAt) < 10*time.Second {
		return b.probeVal
	}
	base := b.ollamaURL()
	var st ollamaState
	var ver struct {
		Version string `json:"version"`
	}
	if httpJSON(base+"/api/version", &ver) == nil {
		st.up = true
		var tags struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if httpJSON(base+"/api/tags", &tags) == nil {
			for _, m := range tags.Models {
				st.models = append(st.models, m.Name)
			}
		}
		var ps struct {
			Models []struct {
				Name     string `json:"name"`
				Size     int64  `json:"size"`
				SizeVRAM int64  `json:"size_vram"`
			} `json:"models"`
		}
		st.idle = true
		if httpJSON(base+"/api/ps", &ps) == nil {
			for _, m := range ps.Models {
				if model == "" || m.Name == model || strings.HasPrefix(m.Name, model) {
					st.idle = false
					st.gpu = m.SizeVRAM > 0 && m.SizeVRAM >= m.Size/2
				}
			}
		}
	}
	b.probeAt, b.probeVal = time.Now(), st
	return st
}

func (b *uiBackend) cfgModel() string {
	if ec, ok := b.cfg.Translate.Engines[b.cfg.Route.Default]; ok {
		return ec.Model
	}
	return ""
}

func (b *uiBackend) packPath() string {
	if b.cfg.Pack.Out != "" {
		return b.cfg.Pack.Out
	}
	if b.gameDir != "" {
		return filepath.Join(b.gameDir, "resourcepacks", "mtrad-pt_br.zip")
	}
	return ""
}

func (b *uiBackend) Status() ui.Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statusLocked()
}

func (b *uiBackend) statusLocked() ui.Status {
	s := ui.Status{Model: b.cfgModel(), Work: b.work, Pack: b.packPath()}
	if b.cfgOK {
		if ec, ok := b.cfg.Translate.Engines[b.cfg.Route.Default]; ok && engineKind(ec) == "ollama" {
			p := b.probeOllama(s.Model, false)
			s.OllamaUp, s.Idle, s.OnGPU = p.up, p.up && p.idle, p.gpu
		}
	}
	if b.work == "" {
		return s
	}
	var dn schema.Done
	if err := schema.ReadJSON(filepath.Join(b.work, "validate.done.json"), &dn, schema.VDone); err == nil {
		s.Known = true
		s.OK, s.Miss = dn.Counts["ok"], dn.Counts["pending"]
		if t, err := time.Parse(time.RFC3339, dn.Time); err == nil {
			s.LastRun = t.Local().Format("02/01 15:04")
		}
		failed := b.failedLocked()
		open := 0
		for _, f := range failed {
			if f.Edit == "" && !f.Left {
				open++
			}
		}
		s.Fail = open
		s.OK += len(failed) - open
	}
	return s
}

func (b *uiBackend) Models() []string {
	b.mu.Lock()
	model := b.cfgModel()
	b.mu.Unlock()
	return b.probeOllama(model, true).models
}

func (b *uiBackend) Doctor() []ui.Check {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []ui.Check
	model := b.cfgModel()
	if model == "" {
		model = "translategemma:4b"
	}
	p := b.probeOllama(model, true)
	base := b.ollamaURL()
	out = append(out, ui.Check{Name: "Ollama responde em " + strings.TrimPrefix(base, "http://"), OK: p.up, Detail: map[bool]string{true: "no ar", false: "não respondeu (o serviço está ligado?)"}[p.up]})
	if p.up {
		has := false
		for _, m := range p.models {
			if m == model || strings.HasPrefix(m, model) {
				has = true
			}
		}
		out = append(out, ui.Check{Name: "Modelo " + model + " instalado", OK: has, Detail: map[bool]string{true: "instalado", false: "rode: ollama pull " + model}[has]})
		switch {
		case p.idle:
			out = append(out, ui.Check{Name: "Modelo está na GPU", OK: false, Warn: true, Detail: "o modelo ainda não está carregado; depois do primeiro texto, confira de novo"})
		default:
			out = append(out, ui.Check{Name: "Modelo está na GPU", OK: p.gpu, Warn: !p.gpu, Detail: map[bool]string{true: "carregado na placa de vídeo", false: "rodando na CPU (lento); no Arch, instale ollama-vulkan"}[p.gpu]})
		}
	} else {
		out = append(out, ui.Check{Name: "Modelo " + model + " instalado", OK: false, Detail: "Ollama parado"})
	}
	work := b.work
	if work == "" {
		work = b.root
	}
	d := doctor.DefaultDeps()
	if err := os.MkdirAll(work, 0o755); err == nil {
		if err := d.Writable(work); err != nil {
			out = append(out, ui.Check{Name: "Pasta de trabalho gravável", OK: false, Detail: err.Error()})
		} else {
			out = append(out, ui.Check{Name: "Pasta de trabalho gravável", OK: true, Detail: work})
		}
		if free, err := d.FreeBytes(work); err == nil {
			out = append(out, ui.Check{Name: "Espaço livre em disco", OK: free > 1<<30, Warn: free <= 1<<30, Detail: fmt.Sprintf("%.1f GB", float64(free)/(1<<30))})
		}
	}
	if b.inst != nil && b.cfgOK {
		in := doctor.Input{Work: work, Route: &b.cfg.Route, Translate: &b.cfg.Translate, Deps: d}
		for _, c := range doctor.Run(context.Background(), in) {
			if strings.HasPrefix(c.Name, "motor ") && engineKind(b.cfg.Translate.Engines[strings.TrimPrefix(c.Name, "motor ")]) == "ollama" {
				continue // já mostrado acima
			}
			out = append(out, ui.Check{Name: c.Name, OK: c.Level == doctor.OK, Warn: c.Level == doctor.Warn, Detail: c.Msg})
		}
	}
	return out
}

// ---------- mods ----------

func (b *uiBackend) modStatsLocked() []diff.ModStat {
	if !b.statsOK {
		b.stats, _ = diff.ModStats(b.work)
		b.statsOK = true
	}
	return b.stats
}

func (b *uiBackend) Mods() []ui.Mod {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.work == "" {
		return nil
	}
	var out []ui.Mod
	for _, s := range b.modStatsLocked() {
		out = append(out, ui.Mod{ID: s.Mod, Texts: s.Texts, Repetitive: s.Repetitive})
	}
	return out
}

func (b *uiBackend) Skipped() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.cfg.SkipMods...)
}

func (b *uiBackend) SaveSkip(ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.editCfg(func(m map[string]any) error {
		if len(ids) == 0 {
			_ = unsetKey(m, "skip_mods")
			return nil
		}
		items := make([]any, len(ids))
		for i, id := range ids {
			items[i] = id
		}
		m["skip_mods"] = items
		return nil
	})
}

// Prepare lê os mods (version, scan, as fontes e diff) sem traduzir.
func (b *uiBackend) Prepare(ctx context.Context) error {
	b.mu.Lock()
	if err := b.needInstance(); err != nil {
		b.mu.Unlock()
		return err
	}
	work, inst, cfg := b.work, b.instDir, b.runtimeCfg()
	b.mu.Unlock()
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	_, err := b.logged(work, "ler os mods", func() error {
		keep, _ := partialKeeper()
		return withLock(work, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return readStages(ctx.Err, work, inst, "", cfg, keep)
		})
	})
	b.mu.Lock()
	b.invalidate()
	b.mu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	return err
}

// requestRate mede quantos textos por minuto a última execução fez (0 = sem medida).
func requestRate(work string) int {
	_, req, err := runlog.Latest(work, 0)
	if err != nil {
		return 0
	}
	var first, last time.Time
	n := 0
	_ = schema.Each(req, translate.VRequests, func(r translate.ReqRec) error {
		if r.Cache || r.Err != "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, r.Time)
		if err != nil {
			return nil
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
		if t.After(last) {
			last = t
		}
		n++
		return nil
	})
	if n < 20 || last.Sub(first) < 30*time.Second {
		return 0
	}
	return int(float64(n) / last.Sub(first).Minutes())
}

func (b *uiBackend) Estimate(limit int) ui.Estimate {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.work == "" {
		return ui.Estimate{}
	}
	skip := map[string]bool{}
	for _, s := range b.cfg.SkipMods {
		skip[strings.ToLower(s)] = true
	}
	total := 0
	for _, s := range b.modStatsLocked() {
		if !skip[strings.ToLower(s.Mod)] {
			total += s.Texts
		}
	}
	if limit > 0 && total > limit {
		total = limit
	}
	return ui.Estimate{Texts: total, PerMin: requestRate(b.work)}
}

// ---------- executar ----------

var reStageLine = regexp.MustCompile(`^(version|scan|extract|patchouli|ftbq|diff|glossary|protect|route|translate|validate|pack|install|activate|interrompido)\b`)

// outcomeLines pega do registro as linhas que resumem o que cada etapa fez.
func outcomeLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " ")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if reStageLine.MatchString(l) || strings.HasPrefix(l, "  aviso") || strings.HasPrefix(l, "  motor ") || strings.HasPrefix(l, "  ") && strings.Contains(l, "refeitos") {
			out = append(out, l)
		}
	}
	if len(out) > 14 {
		out = append(out[:13], fmt.Sprintf("… (mais %d linhas no registro, em Registros)", len(out)-13))
	}
	return out
}

// logged roda fn com a saída do programa desviada para um registro novo em
// work/logs e devolve as linhas de resumo do que ele escreveu.
func (b *uiBackend) logged(work, what string, fn func() error) ([]string, error) {
	rl, lerr := runlog.StartQuiet(work, []string{"mtrad", "ui", what}, b.version)
	err := fn()
	rl.Close(err)
	if lerr != nil || rl == nil {
		return nil, err
	}
	return outcomeLines(rl.Path), err
}

// topProblems resume o errors.jsonl da pasta de trabalho: os k motivos mais
// comuns, cada um com a contagem. Vazio quando não há nada.
func topProblems(work string, k int) []string {
	by := map[string]int{}
	_ = schema.Each(filepath.Join(work, "errors.jsonl"), schema.VErrors, func(r schema.ErrRec) error {
		by[r.Stage+": "+firstWords(r.Msg, 110)]++
		return nil
	})
	type kv struct {
		k string
		n int
	}
	var l []kv
	for a, n := range by {
		l = append(l, kv{a, n})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].n != l[j].n {
			return l[i].n > l[j].n
		}
		return l[i].k < l[j].k
	})
	var out []string
	for i := 0; i < len(l) && i < k; i++ {
		out = append(out, fmt.Sprintf("%d × %s", l[i].n, l[i].k))
	}
	if len(l) > k {
		out = append(out, fmt.Sprintf("… e mais %d tipos de aviso (mtrad logs mostra todos)", len(l)-k))
	}
	return out
}

// outcome transforma o fim de uma execução em resultado para a tela: terminar
// com itens falhos (código 3) não é erro. work é a pasta de trabalho, de onde
// vêm os motivos (errors.jsonl).
func outcome(work string, lines []string, err error) (ui.Outcome, error) {
	o := ui.Outcome{Lines: lines}
	var ee exitErr
	if errors.As(err, &ee) && ee.code == schema.ExitPartial {
		o.Partial = true
		o.Problems = topProblems(work, 3)
		return o, nil
	}
	return o, err
}

func (b *uiBackend) Translate(ctx context.Context, limit int, installFiles bool, progress func(done, total int)) (ui.Outcome, error) {
	b.mu.Lock()
	if err := b.needInstance(); err != nil {
		b.mu.Unlock()
		return ui.Outcome{}, err
	}
	work, inst, cfg := b.work, b.instDir, b.runtimeCfg()
	if ec, ok := cfg.Translate.Engines[cfg.Route.Default]; ok && engineKind(ec) == "ollama" {
		if !b.probeOllama(ec.Model, true).up {
			b.mu.Unlock()
			return ui.Outcome{}, fmt.Errorf("o Ollama não respondeu em %s: inicie o serviço (systemctl start ollama) e confira em Manutenção > Verificar o ambiente", b.ollamaURL())
		}
	}
	b.mu.Unlock()
	cfg.Translate.Limit = limit
	cfg.progress = progress
	if err := os.MkdirAll(work, 0o755); err != nil {
		return ui.Outcome{}, err
	}
	lines, err := b.logged(work, "run", func() error {
		return withLock(work, func() error {
			return runAll(ctx, work, inst, "", cfg, runOpts{Install: installFiles})
		})
	})
	b.mu.Lock()
	b.invalidate()
	b.mu.Unlock()
	return outcome(work, lines, err)
}

// Apply valida e monta o pacote de novo, sem traduzir: é o que faz as
// correções à mão entrarem.
func (b *uiBackend) Apply(ctx context.Context) (ui.Outcome, error) {
	b.mu.Lock()
	if err := b.needInstance(); err != nil {
		b.mu.Unlock()
		return ui.Outcome{}, err
	}
	work, cfg := b.work, b.runtimeCfg()
	b.mu.Unlock()
	lines, err := b.logged(work, "aplicar correções", func() error {
		return withLock(work, func() error {
			keep, worst := partialKeeper()
			for _, s := range []string{"validate", "pack"} {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := keep(runStageCtx(ctx, s, work, cfg)); err != nil {
					return err
				}
			}
			return worst()
		})
	})
	b.mu.Lock()
	b.invalidate()
	b.mu.Unlock()
	return outcome(work, lines, err)
}

// Verify confere o pacote já montado (mtrad verify).
func (b *uiBackend) Verify() (ui.Outcome, error) {
	b.mu.Lock()
	if err := b.needInstance(); err != nil {
		b.mu.Unlock()
		return ui.Outcome{}, err
	}
	work, zp := b.work, b.packPath()
	b.mu.Unlock()
	rep, err := pack.Verify(work, zp)
	if err != nil {
		return ui.Outcome{}, err
	}
	o := ui.Outcome{Lines: verifyLines(rep), Partial: len(rep.Problems) > 0}
	if !o.Partial {
		o.Lines = append(o.Lines, "verify: nenhum problema: o pacote só mudou os textos que o mtrad traduziu")
	}
	return o, nil
}

// ---------- reprovados ----------

// failedLocked monta a lista a partir do que a validação gravou: os reprovados,
// com o original, o que o modelo respondeu e a correção à mão, se houver.
func (b *uiBackend) failedLocked() []ui.Failed {
	if b.failedOK {
		return b.failed
	}
	b.failed, b.failedOK = nil, true
	work := b.work
	if work == "" {
		return nil
	}
	fails, err := schema.ReadAll[schema.FailedRec](filepath.Join(work, "failed.jsonl"), schema.VFailed)
	if err != nil || len(fails) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, f := range fails {
		want[f.ID] = true
	}
	todo := map[string]schema.TodoRec{}
	_ = schema.Each(filepath.Join(work, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		if want[r.ID] {
			todo[r.ID] = r
		}
		return nil
	})
	prot := map[string]schema.ProtectedRec{}
	_ = schema.Each(filepath.Join(work, "protected.jsonl"), schema.VProtected, func(r schema.ProtectedRec) error {
		if want[r.ID] {
			prot[r.ID] = r
		}
		return nil
	})
	rv, _ := review.Load(work)
	for _, f := range fails {
		t := todo[f.ID]
		p, ok := prot[f.ID]
		if !ok {
			p = schema.ProtectedRec{ID: f.ID, Masked: t.En}
		}
		source := t.Mod
		if f.Kind != "" {
			source += " · " + f.Kind
		}
		uf := ui.Failed{ID: f.ID, Name: f.NS + ":" + f.Key, Source: source, Reason: strings.Join(f.Reason, "; "), Orig: t.En, Out: validate.Render(p, f.Out)}
		if e, ok := rv[f.ID]; ok && strings.TrimSpace(e.Pt) != "" && (e.EnSha == "" || e.EnSha == t.EnSha) {
			if e.Pt == t.En {
				uf.Left = true
			} else {
				uf.Edit = e.Pt
			}
		}
		b.failed = append(b.failed, uf)
	}
	return b.failed
}

func (b *uiBackend) Failed() []ui.Failed {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failedLocked()
}

func (b *uiBackend) saveReview(id string, text func(t schema.TodoRec) string, note string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.work == "" {
		return errors.New("nenhuma instância escolhida")
	}
	var t schema.TodoRec
	found := false
	err := schema.Each(filepath.Join(b.work, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		if r.ID == id {
			t, found = r, true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("não consegui ler todo.jsonl: %w", err)
	}
	if !found {
		return fmt.Errorf("o texto %s não está em todo.jsonl (a lista mudou?)", id)
	}
	if err := review.Append(b.work, review.Entry{ID: id, Pt: text(t), EnSha: t.EnSha, Note: note}); err != nil {
		return err
	}
	b.invalidate()
	return nil
}

func (b *uiBackend) SaveEdit(id, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("a correção está vazia (para voltar atrás, deixe o texto em inglês)")
	}
	return b.saveReview(id, func(schema.TodoRec) string { return text }, "ui")
}

// Leave deixa o texto em inglês: a correção é o próprio original, e a
// validação a aceita como está.
func (b *uiBackend) Leave(id string) error {
	return b.saveReview(id, func(t schema.TodoRec) string { return t.En }, "ui:inglês")
}

// ---------- instalar, ativar, registros, trava ----------

func (b *uiBackend) Install(dry bool) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.work == "" {
		return nil, errors.New("nenhuma instância escolhida")
	}
	var res install.Result
	var err error
	if dry {
		res, err = install.Run(b.work, false, true)
	} else {
		err = withLock(b.work, func() error {
			var e error
			res, e = install.Run(b.work, false, false)
			return e
		})
	}
	if err != nil {
		return nil, err
	}
	var lines []string
	refused := 0
	for _, it := range res.Items {
		l := fmt.Sprintf("%s (%s)", it.Rel, it.Action)
		if it.Note != "" {
			l += " " + it.Note
		}
		lines = append(lines, l)
		if it.Action == install.Refused {
			refused++
		}
	}
	if !dry && (res.Conflicts > 0 || refused > 0) {
		return lines, fmt.Errorf("%d arquivos já existem e são diferentes e %d foram recusados; nada disso foi substituído (para substituir, rode `mtrad install --overwrite` num terminal)", res.Conflicts, refused)
	}
	return lines, nil
}

func (b *uiBackend) Activate(dry bool) ([]string, error) {
	b.mu.Lock()
	if b.work == "" {
		b.mu.Unlock()
		return nil, errors.New("nenhuma instância escolhida")
	}
	work, cfg := b.work, b.runtimeCfg()
	b.mu.Unlock()
	var buf bytes.Buffer
	err := withLock(work, func() error { return stageActivate(&buf, work, cfg, true, dry, false) })
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if err != nil {
		return lines, err
	}
	return lines, nil
}

func (b *uiBackend) Logs(n int) []string {
	b.mu.Lock()
	work := b.work
	b.mu.Unlock()
	if work == "" {
		return []string{"nenhuma instância escolhida"}
	}
	var buf bytes.Buffer
	if err := stageLogsTo(&buf, work, n); err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		out = append(out, l)
	}
	if len(out) > 18 {
		out = append(out[:17], fmt.Sprintf("… (mais %d linhas: rode `mtrad logs -w %s -n %d`)", len(out)-17, work, n))
	}
	return out
}

func (b *uiBackend) Unlock() string {
	b.mu.Lock()
	work := b.work
	b.mu.Unlock()
	if work == "" {
		return "nenhuma instância escolhida"
	}
	exists, pid, alive := schema.LockInfo(work)
	switch {
	case !exists:
		return "nenhuma trava encontrada em " + work
	case alive && pid != os.Getpid():
		return fmt.Sprintf("a trava é do processo %d, que ainda está rodando; não removi. Feche a execução primeiro.", pid)
	}
	if err := os.Remove(filepath.Join(work, ".lock")); err != nil {
		return "não consegui remover a trava: " + err.Error()
	}
	return "trava removida de " + work
}

var _ ui.Backend = (*uiBackend)(nil)
