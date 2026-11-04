package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mtrad/internal/hardware"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
	"mtrad/internal/ui"
)

// testBackend monta um Prism falso (instância "teste") e um backend sem
// configuração, com a pasta de configuração e a de trabalho dentro de um
// diretório temporário.
func testBackend(t *testing.T) (b *uiBackend, root, inst, game string) {
	t.Helper()
	root, inst, game = fakeInstance(t)
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	b = newUIBackend("teste", filepath.Join(t.TempDir(), "cfg", "mtrad.json"), filepath.Join(root, "trabalho"), home)
	b.ollamaBase = "http://127.0.0.1:1" // ninguém responde: não usa o Ollama de quem roda o teste
	return
}

// writeEchoConfig grava um mtrad.json com o motor de teste (echo) e o carrega.
func writeEchoConfig(t *testing.T, b *uiBackend) {
	t.Helper()
	c := echoConfig()
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(b.cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	b.reload()
	if !b.cfgOK {
		t.Fatalf("configuração de teste recusada: %v", b.cfgErr)
	}
}

func TestBackendSetupCreatesThenPreserves(t *testing.T) {
	b, root, _, _ := testBackend(t)
	if b.SetupDone() {
		t.Fatal("sem arquivo, o setup não está feito")
	}
	d := b.Defaults()
	if d.Engine != "ollama" || d.Config != b.cfgPath || d.Work != filepath.Join(root, "trabalho") {
		t.Fatalf("padrões: %+v", d)
	}
	if err := b.SaveSetup(ui.Setup{Engine: "ollama", Model: "m:1", Sources: []string{"lang", "ftbq"}, Work: filepath.Join(root, "w2"), Config: b.cfgPath}); err != nil {
		t.Fatal(err)
	}
	if !b.SetupDone() {
		t.Fatal("o arquivo gravado devia valer")
	}
	got := b.Defaults()
	if got.Engine != "ollama" || got.Model != "m:1" || strings.Join(got.Sources, ",") != "lang,ftbq" || got.Work != filepath.Join(root, "w2") {
		t.Fatalf("depois de gravar: %+v", got)
	}
	// o usuário marca mods em inglês e personaliza algo à mão
	if err := b.SaveSkip([]string{"chipped", "rechiseled"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(b.cfgPath)
	raw = []byte(strings.Replace(string(raw), `"retries": 2`, `"retries": 7`, 1))
	os.WriteFile(b.cfgPath, raw, 0o644)
	b.reload()
	// novo setup: troca o motor e todas as fontes; o resto fica
	if err := b.SaveSetup(ui.Setup{Engine: "gemini", Model: "gemini-2.0-flash", Sources: []string{"lang", "patchouli", "ftbq"}, Work: filepath.Join(root, "w2"), Config: b.cfgPath}); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(b.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.SkipMods, ",") != "chipped,rechiseled" || c.Translate.Retries != 7 || len(c.Sources) != 0 {
		t.Fatalf("o setup apagou o que o usuário tinha: %+v", c)
	}
	if ec := c.Translate.Engines[c.Route.Default]; ec.Model != "gemini-2.0-flash" || engineKind(ec) != "gemini" {
		t.Fatalf("motor não trocou: %+v", ec)
	}
	if _, err := os.Stat(b.cfgPath + ".bak"); err != nil {
		t.Fatal("faltou a cópia .bak")
	}
	// arquivo quebrado: vai para .invalido.bak e recomeça
	os.WriteFile(b.cfgPath, []byte("{isto não é json"), 0o644)
	b.reload()
	if b.SetupDone() || b.cfgErr == nil {
		t.Fatal("arquivo quebrado não pode contar como setup feito")
	}
	if err := b.SaveSetup(ui.Setup{Engine: "libre", Model: "padrão do motor", Sources: []string{"lang"}, Work: filepath.Join(root, "w3"), Config: b.cfgPath}); err != nil {
		t.Fatal(err)
	}
	if old, err := os.ReadFile(b.cfgPath + ".invalido.bak"); err != nil || !strings.Contains(string(old), "isto não é json") {
		t.Fatalf("o arquivo quebrado devia ser guardado: %v", err)
	}
	if !b.SetupDone() || b.Defaults().Engine != "libre" {
		t.Fatalf("%+v", b.Defaults())
	}
	// erro de motor sem modelo não grava nada
	if err := b.SaveSetup(ui.Setup{Engine: "gemini", Model: "", Work: "x", Config: b.cfgPath}); err == nil {
		t.Fatal("gemini sem modelo devia dar erro")
	}
}

func TestBackendInstancesAndSelect(t *testing.T) {
	b, root, inst, game := testBackend(t)
	os.WriteFile(filepath.Join(inst, "instance.cfg"), []byte("InstanceType=OneSix\nname=Meu Modpack\n"), 0o644)
	// pastas que o Prism usa por dentro e que não são instâncias
	os.MkdirAll(filepath.Join(root, "PrismLauncher", "instances", "_LAUNCHER_TEMP"), 0o755)
	os.MkdirAll(filepath.Join(root, "PrismLauncher", "instances", "vazia"), 0o755)
	list := b.Instances()
	if len(list) != 1 || list[0].Name != "Meu Modpack" || list[0].MC != "1.21.1" || list[0].Loader != "fabric" || list[0].PackFormat != "34" {
		t.Fatalf("%+v", list)
	}
	full, err := b.SelectInstance(list[0])
	if err != nil || full.MC != "1.21.1" || b.gameDir != game {
		t.Fatalf("%v %+v", err, full)
	}
	if want := filepath.Join(root, "trabalho", "meu-modpack"); b.work != want {
		t.Fatalf("pasta de trabalho %q, queria %q", b.work, want)
	}
	if _, err := b.SelectInstance(ui.Instance{Path: filepath.Join(root, "nao-existe")}); err == nil {
		t.Fatal("pasta inexistente devia dar erro")
	}
	if _, err := b.SelectInstance(ui.Instance{Path: root}); err == nil || !strings.Contains(err.Error(), "minecraft") {
		t.Fatalf("pasta sem minecraft/ devia explicar: %v", err)
	}
	// a última instância usada fica no mtrad.json e vem primeiro
	writeEchoConfig(t, b)
	if _, err := b.SelectInstance(list[0]); err != nil {
		t.Fatal(err)
	}
	c, _ := loadConfig(b.cfgPath)
	if c.LastInstance != inst {
		t.Fatalf("last_instance = %q", c.LastInstance)
	}
}

func TestWorkForKeepsExistingWorkOfTheSameInstance(t *testing.T) {
	root := t.TempDir()
	inst := filepath.Join(t.TempDir(), "instances", "Reclamation")
	other := filepath.Join(t.TempDir(), "instances", "Outra")
	for _, d := range []string{inst, other} {
		os.MkdirAll(d, 0o755)
	}
	if got := workFor(root, inst); got != filepath.Join(root, "reclamation") {
		t.Fatalf("pasta nova: %q", got)
	}
	// quem já usava a pasta como trabalho dessa instância continua nela
	schema.WriteJSON(filepath.Join(root, "instance.json"), schema.Instance{Schema: schema.VInstance, InstanceDir: inst})
	if got := workFor(root, inst); got != root {
		t.Fatalf("o trabalho que já existe na raiz devia continuar: %q", got)
	}
	if got := workFor(root, other); got != filepath.Join(root, "outra") {
		t.Fatalf("outra instância não pode usar a raiz: %q", got)
	}
}

func TestBackendFullFlow(t *testing.T) {
	b, _, inst, game := testBackend(t)
	writeEchoConfig(t, b)
	if _, err := b.SelectInstance(ui.Instance{Path: inst}); err != nil {
		t.Fatal(err)
	}
	if len(b.Mods()) != 0 || b.Estimate(0).Texts != 0 {
		t.Fatal("antes de ler os mods não há lista nem estimativa")
	}
	if st := b.Status(); st.Known || st.Work != b.work || !strings.HasSuffix(st.Pack, filepath.Join("resourcepacks", "mtrad-pt_br.zip")) {
		t.Fatalf("status inicial: %+v", st)
	}
	// nada pode vazar para o terminal enquanto as etapas rodam
	tmp, _ := os.CreateTemp(t.TempDir(), "saida")
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = tmp, tmp
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	if err := b.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	mods := b.Mods()
	if len(mods) == 0 {
		t.Fatalf("depois de Prepare devia haver mods")
	}
	if est := b.Estimate(0); est.Texts < 5 {
		t.Fatalf("estimativa: %+v", est)
	}
	if err := b.SaveSkip([]string{"mymod"}); err != nil {
		t.Fatal(err)
	}
	if got := b.Skipped(); len(got) != 1 || got[0] != "mymod" {
		t.Fatalf("skipped: %v", got)
	}
	if est := b.Estimate(0); est.Texts >= 5 {
		t.Fatalf("com o mod em inglês a estimativa devia cair: %+v", est)
	}
	b.SaveSkip(nil)

	var last int64
	out, err := b.Translate(context.Background(), 0, true, func(d, total int) { atomic.StoreInt64(&last, int64(d)) })
	if err != nil {
		t.Fatalf("translate: %v\n%v", err, out.Lines)
	}
	if atomic.LoadInt64(&last) == 0 {
		t.Error("o progresso nunca foi chamado")
	}
	os.Stdout, os.Stderr = oldOut, oldErr
	if fi, _ := tmp.Stat(); fi.Size() != 0 {
		b, _ := os.ReadFile(tmp.Name())
		t.Fatalf("saída vazou para o terminal: %q", b)
	}
	joined := strings.Join(out.Lines, "\n")
	if !strings.Contains(joined, "translate:") || !strings.Contains(joined, "pack:") {
		t.Fatalf("o resumo devia ter as etapas:\n%s", joined)
	}
	if _, err := os.Stat(filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")); err != nil {
		t.Fatal("o pacote não foi montado")
	}
	if _, err := os.Stat(filepath.Join(game, "config", "ftbquests", "quests", "lang", "pt_br.snbt")); err != nil {
		t.Fatal("install=true devia copiar o FTB Quests")
	}
	st := b.Status()
	if !st.Known || st.OK < 5 || st.Fail != 0 || st.LastRun == "" {
		t.Fatalf("status depois: %+v", st)
	}
	if lines := b.Logs(0); len(lines) < 3 || !strings.Contains(strings.Join(lines, "\n"), "registro:") {
		t.Fatalf("logs: %v", lines)
	}
	// instalar de novo: os arquivos são iguais, nada a fazer nem a recusar
	files, err := b.Install(true)
	if err != nil || len(files) == 0 {
		t.Fatalf("%v %v", err, files)
	}
	// ativar: simulação mostra o plano; o jogo não está aberto
	plan, err := b.Activate(true)
	if err != nil || !strings.Contains(strings.Join(plan, "\n"), "file/mtrad-pt_br.zip") {
		t.Fatalf("%v %v", err, plan)
	}
	if _, err := b.Activate(false); err != nil {
		t.Fatal(err)
	}
	opts, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if !strings.Contains(string(opts), "file/mtrad-pt_br.zip") || !strings.Contains(string(opts), "lang:pt_br") {
		t.Fatalf("options.txt: %s", opts)
	}
	// trava: sem trava, e uma trava de processo morto é removida
	if got := b.Unlock(); !strings.Contains(got, "nenhuma trava") {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(b.work, ".lock"), []byte("999999\n"), 0o644)
	if got := b.Unlock(); !strings.Contains(got, "removida") {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(b.work, ".lock"), []byte("1\n"), 0o644) // pid 1 existe
	if got := b.Unlock(); !strings.Contains(got, "ainda está rodando") {
		t.Fatalf("não pode remover a trava de quem está vivo: %s", got)
	}
	os.Remove(filepath.Join(b.work, ".lock"))
}

// rewriteTranslated troca o que o modelo respondeu para um texto, para forçar uma reprovação.
func rewriteTranslated(t *testing.T, work, idSub string, out func(old string) string) string {
	t.Helper()
	p := filepath.Join(work, "translated.jsonl")
	recs, err := schema.ReadAll[schema.TranslatedRec](p, schema.VTranslated)
	if err != nil {
		t.Fatal(err)
	}
	todo, _ := schema.ReadAll[schema.TodoRec](filepath.Join(work, "todo.jsonl"), schema.VTodo)
	key := map[string]string{}
	for _, r := range todo {
		key[r.ID] = r.Key
	}
	w, err := schema.Create(p, schema.VTranslated)
	if err != nil {
		t.Fatal(err)
	}
	hit := ""
	for _, r := range recs {
		if key[r.ID] == idSub {
			r.Out = out(r.Out)
			hit = r.ID
		}
		w.Write(r)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if hit == "" {
		t.Fatalf("texto %q não achado", idSub)
	}
	return hit
}

func TestBackendFailedEditLeaveApply(t *testing.T) {
	b, _, inst, game := testBackend(t)
	writeEchoConfig(t, b)
	b.SelectInstance(ui.Instance{Path: inst})
	if _, err := b.Translate(context.Background(), 0, false, func(int, int) {}); err != nil {
		t.Fatal(err)
	}
	schema.SkipFreshness = true // o teste mexe em translated.jsonl por fora, de propósito
	defer func() { schema.SkipFreshness = false }()
	idHello := rewriteTranslated(t, b.work, "msg.hello", func(string) string { return "Olá, bem-vindo" }) // perde a marca do %s
	idGem := rewriteTranslated(t, b.work, "item.mymod.gem", func(string) string { return "" })            // vazio
	if _, err := b.Apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	fl := b.Failed()
	if len(fl) != 2 {
		t.Fatalf("esperava 2 reprovados: %+v", fl)
	}
	byID := map[string]ui.Failed{}
	for _, f := range fl {
		byID[f.ID] = f
	}
	h := byID[idHello]
	if h.Name != "mymod:msg.hello" || h.Orig != "Hello %s, welcome" || !strings.Contains(h.Out, "bem-vindo") || h.Reason == "" || h.Edit != "" {
		t.Fatalf("reprovado mal montado: %+v", h)
	}
	if st := b.Status(); st.Fail != 2 {
		t.Fatalf("status.Fail = %d", st.Fail)
	}
	if err := b.SaveEdit(idHello, "Olá %s, bem-vindo"); err != nil {
		t.Fatal(err)
	}
	if err := b.Leave(idGem); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveEdit(idGem, "  "); err == nil {
		t.Fatal("correção vazia devia dar erro")
	}
	byID = map[string]ui.Failed{}
	for _, f := range b.Failed() {
		byID[f.ID] = f
	}
	if byID[idHello].Edit != "Olá %s, bem-vindo" || !byID[idGem].Left {
		t.Fatalf("a lista não refletiu as correções: %+v", byID)
	}
	if st := b.Status(); st.Fail != 0 {
		t.Fatalf("depois de resolver os dois, Fail = %d", st.Fail)
	}
	if _, err := b.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	lang, ok := readZipFile(t, filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip"), "assets/mymod/lang/pt_br.json")
	if !ok {
		t.Fatal("sem idioma no pacote")
	}
	var m map[string]string
	json.Unmarshal([]byte(lang), &m)
	if m["msg.hello"] != "Olá %s, bem-vindo" || m["item.mymod.gem"] != "Shiny Gem" {
		t.Fatalf("o pacote devia ter a correção e o inglês: %v", m)
	}
	// depois de aplicar, a validação já aceita os dois e a lista de reprovados esvazia
	if len(b.Failed()) != 0 {
		t.Fatalf("lista: %+v", b.Failed())
	}
	if st := b.Status(); st.Fail != 0 || st.OK < 5 {
		t.Fatalf("status: %+v", st)
	}
}

func TestBackendRefusesWithoutInstanceOrConfig(t *testing.T) {
	b, _, inst, _ := testBackend(t)
	if _, err := b.Translate(context.Background(), 0, false, func(int, int) {}); err == nil {
		t.Fatal("sem instância devia dar erro")
	}
	if err := b.SaveSkip([]string{"x"}); err == nil {
		t.Fatal("sem configuração devia dar erro")
	}
	b.SelectInstance(ui.Instance{Path: inst})
	if _, err := b.Translate(context.Background(), 0, false, func(int, int) {}); err == nil || !strings.Contains(err.Error(), "configuração") {
		t.Fatalf("sem configuração: %v", err)
	}
	if err := b.SaveEdit("x", "y"); err == nil {
		t.Fatal("sem todo.jsonl devia dar erro")
	}
}

func TestBackendOllamaDownBlocksTranslateWithAdvice(t *testing.T) {
	b, _, inst, _ := testBackend(t)
	if err := b.SaveSetup(ui.Setup{Engine: "ollama", Model: "m:1", Work: b.root, Config: b.cfgPath}); err != nil {
		t.Fatal(err)
	}
	b.ollamaBase = "" // volta a usar a URL do arquivo
	b.cfg.Translate.Engines[b.cfg.Route.Default] = withURL(b.cfg.Translate.Engines[b.cfg.Route.Default], "http://127.0.0.1:1/v1")
	b.SelectInstance(ui.Instance{Path: inst})
	_, err := b.Translate(context.Background(), 0, false, func(int, int) {})
	if err == nil || !strings.Contains(err.Error(), "Ollama não respondeu") {
		t.Fatalf("%v", err)
	}
	checks := b.Doctor()
	if checks[0].OK || !strings.Contains(checks[0].Name, "Ollama") {
		t.Fatalf("doctor devia acusar o Ollama parado: %+v", checks)
	}
}

func withURL(ec translate.EngineConfig, url string) translate.EngineConfig {
	ec.BaseURL = url
	return ec
}

// Quando o motor não responde, o resultado precisa dizer o motivo (vindo do
// errors.jsonl) em vez de mandar o usuário olhar uma lista de reprovados vazia.
func TestBackendEngineFailureExplainsWhy(t *testing.T) {
	b, _, inst, _ := testBackend(t)
	c := echoConfig()
	c.Translate.Engines = map[string]translate.EngineConfig{"morto": {Type: "openai", BaseURL: "http://127.0.0.1:1/v1", Model: "m", TimeoutSec: 2, Workers: 1}}
	c.Route.Default = "morto"
	c.Translate.Retries = 0
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(b.cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	b.reload()
	if !b.cfgOK {
		t.Fatalf("configuração recusada: %v", b.cfgErr)
	}
	if _, err := b.SelectInstance(ui.Instance{Path: inst}); err != nil {
		t.Fatal(err)
	}
	tmp, _ := os.CreateTemp(t.TempDir(), "saida")
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = tmp, tmp
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	out, err := b.Translate(context.Background(), 0, false, func(int, int) {})
	if err != nil {
		t.Fatalf("falha do motor não é erro de execução, é resultado parcial: %v", err)
	}
	if !out.Partial {
		t.Fatalf("devia terminar parcial: %+v", out)
	}
	if len(out.Problems) == 0 || !strings.Contains(strings.Join(out.Problems, "\n"), "translate:") {
		t.Fatalf("faltou o motivo da falha do motor: %+v", out.Problems)
	}
}

func TestBackendVerifyAfterTranslateAndAfterDamage(t *testing.T) {
	b, _, inst, _ := testBackend(t)
	writeEchoConfig(t, b)
	if _, err := b.SelectInstance(ui.Instance{Path: inst}); err != nil {
		t.Fatal(err)
	}
	tmp, _ := os.CreateTemp(t.TempDir(), "saida")
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = tmp, tmp
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	if _, err := b.Verify(); err == nil {
		t.Fatal("sem pacote montado, conferir tem que dizer que não achou o pacote")
	}
	if _, err := b.Translate(context.Background(), 0, false, func(int, int) {}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Verify()
	if err != nil || out.Partial || !strings.Contains(strings.Join(out.Lines, "\n"), "nenhum problema") {
		t.Fatalf("pacote recém-montado devia estar certo: %v %+v", err, out)
	}
	// um arquivo de idioma com uma chave que o original não tem
	zp := b.packPath()
	files := map[string][]byte{}
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if strings.HasSuffix(f.Name, "/lang/pt_br.json") {
			data = []byte(`{"chave.que.nao.existe":"x"}`)
		}
		files[f.Name] = data
	}
	zr.Close()
	zf, _ := os.Create(zp)
	zw := zip.NewWriter(zf)
	for n, d := range files {
		w, _ := zw.Create(n)
		w.Write(d)
	}
	zw.Close()
	zf.Close()
	out, err = b.Verify()
	if err != nil || !out.Partial || !strings.Contains(strings.Join(out.Lines, "\n"), "não existe no original") {
		t.Fatalf("o pacote adulterado devia ser apontado: %v %+v", err, out)
	}
}

func TestBackendHardwareRecomendaEUsaCache(t *testing.T) {
	b, _, _, _ := testBackend(t)
	b.ollamaBase = "http://127.0.0.1:1" // Ollama parado: nada instalado conhecido
	calls := 0
	b.hwDetect = func() hardware.Info {
		calls++
		return hardware.Info{CPUCores: 8, RAMTotal: 16 << 30, RAMAvail: 9 << 30,
			GPUs: []hardware.GPU{{Vendor: "amd", Name: "RX 580", Total: 8 << 30, Used: 512 << 20, UsedKnown: true}}}
	}
	h := b.Hardware()
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	var rec string
	for _, o := range h.Options {
		if o.Recommended {
			rec = o.Name
		}
	}
	if rec != "translategemma:4b" {
		t.Fatalf("recomendado = %q (%s)", rec, h.Why)
	}
	if !strings.Contains(strings.Join(h.Lines, "\n"), "RX 580") || h.Checked == "" {
		t.Fatalf("faltou a linha da placa ou a data do catálogo: %+v", h)
	}
	b.Hardware()
	if calls != 1 {
		t.Fatalf("a segunda chamada devia usar o cache: calls = %d", calls)
	}
	b.Doctor() // "Verificar de novo" mede outra vez
	b.Hardware()
	if calls != 2 {
		t.Fatalf("depois do Doctor devia medir de novo: calls = %d", calls)
	}
}

// O setup do menu troca o modelo, mas não pode apagar o que o usuário ajustou
// à mão no mesmo motor (fallback, workers, limites).
func TestBackendSetupPreservaAjustesDoMotor(t *testing.T) {
	b, root, _, _ := testBackend(t)
	setup := func(model string) error {
		return b.SaveSetup(ui.Setup{Engine: "ollama", Model: model, Work: filepath.Join(root, "w"), Config: b.cfgPath})
	}
	if err := setup("m:1"); err != nil {
		t.Fatal(err)
	}
	if err := editConfigFile(b.cfgPath, func(m map[string]any) error {
		if err := setKey(m, "translate.engines.reserva", map[string]any{"type": "echo"}); err != nil {
			return err
		}
		if err := setKey(m, "translate.engines.llm.fallback", "reserva"); err != nil {
			return err
		}
		if err := setKey(m, "translate.engines.llm.workers", json.Number("4")); err != nil {
			return err
		}
		return setKey(m, "translate.engines.llm.max_wait_sec", json.Number("30"))
	}); err != nil {
		t.Fatal(err)
	}
	b.reload()
	if err := setup("m:2"); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(b.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ec := c.Translate.Engines["llm"]
	if ec.Model != "m:2" {
		t.Fatalf("o modelo devia ter trocado: %+v", ec)
	}
	if ec.Fallback != "reserva" || ec.Workers != 4 || ec.MaxWaitSec != 30 {
		t.Fatalf("o setup apagou o que o usuário ajustou no motor: %+v", ec)
	}
	if _, ok := c.Translate.Engines["reserva"]; !ok {
		t.Fatal("o motor reserva sumiu")
	}
}

// Trocar para outro motor substitui a entrada, mas o fallback continua valendo
// quando o motor reserva ainda existe e não forma um ciclo.
func TestBackendSetupTrocaDeMotorMantemFallbackValido(t *testing.T) {
	b, root, _, _ := testBackend(t)
	save := func(engine, model string) error {
		return b.SaveSetup(ui.Setup{Engine: engine, Model: model, Work: filepath.Join(root, "w"), Config: b.cfgPath})
	}
	if err := save("ollama", "m:1"); err != nil {
		t.Fatal(err)
	}
	// o motor "llm" (llama-server, outro endereço) vai trocar de lugar com o Ollama
	if err := editConfigFile(b.cfgPath, func(m map[string]any) error {
		if err := setKey(m, "translate.engines.reserva", map[string]any{"type": "echo"}); err != nil {
			return err
		}
		return setKey(m, "translate.engines.llm.fallback", "reserva")
	}); err != nil {
		t.Fatal(err)
	}
	b.reload()
	if err := save("llama", ""); err != nil { // llama usa o mesmo nome "llm", mas outra URL
		t.Fatal(err)
	}
	c, err := loadConfig(b.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ec := c.Translate.Engines["llm"]
	if !strings.Contains(ec.BaseURL, ":8080") || ec.Fallback != "reserva" {
		t.Fatalf("a troca de motor devia manter o fallback válido: %+v", ec)
	}
	// reserva que deixou de existir: não pode impedir o setup
	if err := editConfigFile(b.cfgPath, func(m map[string]any) error {
		// remove o reserva e o fallback juntos, como o usuário faria
		if err := unsetKey(m, "translate.engines.llm.fallback"); err != nil {
			return err
		}
		return unsetKey(m, "translate.engines.reserva")
	}); err != nil {
		t.Fatal(err)
	}
	b.reload()
	if err := save("ollama", "m:3"); err != nil {
		t.Fatalf("sem motor reserva o setup tem que funcionar: %v", err)
	}
}

func TestEngineKindReconheceNuvem(t *testing.T) {
	for url, want := range map[string]string{
		"https://api.openai.com/v1":                               "openai",
		"https://openrouter.ai/api/v1":                            "openrouter",
		"https://generativelanguage.googleapis.com/v1beta/openai": "gemini",
		"http://127.0.0.1:11434/v1":                               "ollama",
		"http://127.0.0.1:8080/v1":                                "llama",
	} {
		if got := engineKind(translate.EngineConfig{Type: "openai", BaseURL: url}); got != want {
			t.Errorf("%s: engineKind = %q, queria %q", url, got, want)
		}
	}
}

func TestBackendSetupComMotorDeNuvem(t *testing.T) {
	b, root, _, _ := testBackend(t)
	save := func(engine, model string) error {
		return b.SaveSetup(ui.Setup{Engine: engine, Model: model, Work: filepath.Join(root, "w"), Config: b.cfgPath})
	}
	if err := save("openai", ""); err == nil {
		t.Fatal("nuvem sem nome de modelo devia dar erro, sem chutar um nome")
	}
	if err := save("openai", "modelo-x"); err != nil {
		t.Fatal(err)
	}
	if d := b.Defaults(); d.Engine != "openai" || d.Model != "modelo-x" {
		t.Fatalf("%+v", d)
	}
	raw, _ := os.ReadFile(b.cfgPath)
	if !strings.Contains(string(raw), `"api_key_env": "OPENAI_API_KEY"`) {
		t.Fatalf("o arquivo devia guardar o nome da variável da chave:\n%s", raw)
	}
	// o status do painel não confunde a nuvem com o Ollama
	if st := b.Status(); st.OllamaUp {
		t.Fatalf("motor de nuvem não é Ollama: %+v", st)
	}
}
