package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/route"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
)

func fakeDeps() Deps {
	return Deps{
		LookPath:  func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Exec:      func(context.Context, string, ...string) error { return nil },
		Get:       func(context.Context, string) (int, error) { return 200, nil },
		Getenv:    func(string) string { return "" },
		FreeBytes: func(string) (uint64, error) { return 50 << 30, nil },
		Writable:  func(string) error { return nil },
	}
}

func find(cs []Check, name string) []Check {
	var out []Check
	for _, c := range cs {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func levelOf(t *testing.T, cs []Check, name string) Level {
	t.Helper()
	f := find(cs, name)
	if len(f) == 0 {
		t.Fatalf("sem verificação %q em %+v", name, cs)
	}
	return Worst(f)
}

func TestEmptyWorkWarnsButDoesNotFail(t *testing.T) {
	cs := Run(context.Background(), Input{Work: filepath.Join(t.TempDir(), "work"), Deps: fakeDeps()})
	if Worst(cs) != Warn {
		t.Fatalf("%+v", cs)
	}
	if levelOf(t, cs, "instância") != Warn || levelOf(t, cs, "motores") != Warn {
		t.Fatalf("%+v", cs)
	}
}

func TestInstanceGameAndOptions(t *testing.T) {
	work, game := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(game, "mods"), 0o755)
	os.WriteFile(filepath.Join(game, "mods", "a.jar"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(game, "resourcepacks"), 0o755)
	os.WriteFile(filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(game, "options.txt"), []byte("lang:en_us\r\nresourcePacks:[\"vanilla\"]\r\n"), 0o644)
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game, McVersion: "1.20.1", Loader: "fabric", Warnings: []string{"aviso do version"}})
	cs := Run(context.Background(), Input{Work: work, Deps: fakeDeps()})
	if find(cs, "instância")[0].Level != OK || levelOf(t, cs, "mods") != OK || levelOf(t, cs, "idioma do jogo") != Warn || levelOf(t, cs, "pacote") != Warn {
		t.Fatalf("%+v", cs)
	}
	if len(find(cs, "instância")) != 2 {
		t.Fatal("o aviso do version devia aparecer")
	}
	// ativa o pacote e o idioma
	os.WriteFile(filepath.Join(game, "options.txt"), []byte("lang:pt_br\nresourcePacks:[\"vanilla\",\"file/mtrad-pt_br.zip\"]\n"), 0o644)
	cs = Run(context.Background(), Input{Work: work, Deps: fakeDeps()})
	if levelOf(t, cs, "idioma do jogo") != OK || levelOf(t, cs, "pacote") != OK {
		t.Fatalf("%+v", cs)
	}
	// pasta do jogo sumiu
	os.RemoveAll(game)
	cs = Run(context.Background(), Input{Work: work, Deps: fakeDeps()})
	if levelOf(t, cs, "pasta do jogo") != Fail || Worst(cs) != Fail {
		t.Fatalf("%+v", cs)
	}
}

func TestStaleStageFiles(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "todo.jsonl"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(work, "reused.jsonl"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(work, "strings.jsonl"), []byte("c"), 0o644)
	if err := schema.WriteDone(work, "diff", []string{"strings.jsonl"}, []string{"todo.jsonl", "reused.jsonl"}, nil); err != nil {
		t.Fatal(err)
	}
	cs := Run(context.Background(), Input{Work: work, Deps: fakeDeps()})
	if levelOf(t, cs, "etapas") != OK {
		t.Fatalf("%+v", cs)
	}
	os.WriteFile(filepath.Join(work, "todo.jsonl"), []byte("alterado"), 0o644)
	os.Remove(filepath.Join(work, "reused.jsonl"))
	cs = Run(context.Background(), Input{Work: work, Deps: fakeDeps()})
	f := find(cs, "etapas")
	if len(f) != 2 || f[0].Level != Warn || !strings.Contains(f[0].Msg+f[1].Msg, "mudou") || !strings.Contains(f[0].Msg+f[1].Msg, "sumiu") {
		t.Fatalf("%+v", f)
	}
}

func TestErrorsLogAndGlossaryAndDisk(t *testing.T) {
	work := t.TempDir()
	l, _ := schema.OpenErrLog(work, "extract")
	l.Add("x.jar", "", "ruim")
	l.Add("y.jar", "", "ruim")
	l.Close()
	d := fakeDeps()
	d.FreeBytes = func(string) (uint64, error) { return 10 << 20, nil }
	cs := Run(context.Background(), Input{Work: filepath.Join(work, "novo"), Deps: d})
	if levelOf(t, cs, "disco") != Warn || levelOf(t, cs, "glossário") != Warn {
		t.Fatalf("%+v", cs)
	}
	cs = Run(context.Background(), Input{Work: work, Deps: d})
	if f := find(cs, "errors.jsonl"); len(f) != 1 || f[0].Level != Warn || !strings.Contains(f[0].Msg, "extract 2") {
		t.Fatalf("%+v", f)
	}
}

func cfg() (*route.Config, *translate.Config) {
	return &route.Config{Default: "llm"}, &translate.Config{Engines: map[string]translate.EngineConfig{
		"llm": {Type: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "m"},
	}}
}

func TestEngines(t *testing.T) {
	r, tc := cfg()
	tc.Engines["libre"] = translate.EngineConfig{Type: "libretranslate", BaseURL: "http://127.0.0.1:5000"}
	tc.Engines["w"] = translate.EngineConfig{Type: "worker", Command: []string{"python3", "tools/w.py", "--backend", "ct2"}}
	tc.Engines["bad"] = translate.EngineConfig{Type: "xyz"}
	tc.Engines["gem"] = translate.EngineConfig{Type: "openai", BaseURL: "https://x/v1", APIKeyEnv: "GEMINI_KEY"}
	d := fakeDeps()
	var urls []string
	d.Get = func(_ context.Context, u string) (int, error) {
		urls = append(urls, u)
		if strings.Contains(u, "5000") {
			return 0, errors.New("connection refused")
		}
		return 200, nil
	}
	d.Exec = func(context.Context, string, ...string) error { return errors.New("no module") }
	cs := Run(context.Background(), Input{Work: t.TempDir(), Route: r, Translate: tc, Deps: d})
	if levelOf(t, cs, "motor llm") != OK || levelOf(t, cs, "motor libre") != Warn || levelOf(t, cs, "motor bad") != Fail || levelOf(t, cs, "motor gem") != Fail {
		t.Fatalf("%+v", cs)
	}
	// o script do worker não existe a partir da pasta atual do teste
	if levelOf(t, cs, "motor w") != Fail {
		t.Fatalf("%+v", find(cs, "motor w"))
	}
	if !contains(urls, "http://127.0.0.1:8080/v1/models") || !contains(urls, "http://127.0.0.1:5000/languages") {
		t.Fatalf("%v", urls)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestWorkerCT2ImportCheck(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "w.py")
	os.WriteFile(script, []byte("#"), 0o644)
	r, tc := cfg()
	tc.Engines["w"] = translate.EngineConfig{Type: "worker", Command: []string{"python3", script, "--backend", "ct2"}}
	d := fakeDeps()
	d.Exec = func(context.Context, string, ...string) error { return errors.New("no module") }
	cs := Run(context.Background(), Input{Work: t.TempDir(), Route: r, Translate: tc, Deps: d})
	if levelOf(t, cs, "motor w") != Warn {
		t.Fatalf("%+v", find(cs, "motor w"))
	}
	d.Exec = func(context.Context, string, ...string) error { return nil }
	cs = Run(context.Background(), Input{Work: t.TempDir(), Route: r, Translate: tc, Deps: d})
	if levelOf(t, cs, "motor w") != OK {
		t.Fatalf("%+v", find(cs, "motor w"))
	}
}

func TestRouteMustPointToExistingEngines(t *testing.T) {
	r, tc := cfg()
	r.Rules = []route.Rule{{MaxChars: 10, Engine: "fantasma"}}
	cs := Run(context.Background(), Input{Work: t.TempDir(), Route: r, Translate: tc, Deps: fakeDeps()})
	if f := find(cs, "rota"); len(f) != 1 || f[0].Level != Fail || !strings.Contains(f[0].Msg, "fantasma") {
		t.Fatalf("%+v", cs)
	}
}

func TestAPIKeyPresent(t *testing.T) {
	r, tc := cfg()
	tc.Engines["llm"] = translate.EngineConfig{Type: "openai", BaseURL: "https://x/v1", APIKeyEnv: "K", Model: "m"}
	d := fakeDeps()
	d.Getenv = func(k string) string {
		if k == "K" {
			return "segredo"
		}
		return ""
	}
	d.Get = func(context.Context, string) (int, error) { return 401, nil }
	cs := Run(context.Background(), Input{Work: t.TempDir(), Route: r, Translate: tc, Deps: d})
	if f := find(cs, "motor llm"); len(f) != 1 || f[0].Level != Warn || !strings.Contains(f[0].Msg, "chave") {
		t.Fatalf("%+v", f)
	}
	for _, c := range cs {
		if strings.Contains(c.Msg, "segredo") {
			t.Fatal("a chave não pode aparecer na saída")
		}
	}
}

func TestWritableChecks(t *testing.T) {
	work, game := t.TempDir(), t.TempDir()
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game, McVersion: "1.21.1"})
	d := fakeDeps()
	cs := Run(context.Background(), Input{Work: work, Deps: d})
	if levelOf(t, cs, "pasta de trabalho") != OK || levelOf(t, cs, "pasta resourcepacks") != OK {
		t.Fatalf("%+v", cs)
	}
	var tried []string
	d.Writable = func(dir string) error {
		tried = append(tried, dir)
		if strings.HasPrefix(dir, game) {
			return errors.New("permission denied")
		}
		return nil
	}
	cs = Run(context.Background(), Input{Work: work, Deps: d})
	f := find(cs, "pasta resourcepacks")
	if len(f) != 1 || f[0].Level != Fail || !strings.Contains(f[0].Msg, "permission denied") {
		t.Fatalf("%+v", cs)
	}
	// resourcepacks ainda não existe: a verificação sobe até a pasta do jogo
	if len(tried) != 2 || tried[1] != game {
		t.Fatalf("%v", tried)
	}
}

func TestDefaultWritableLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := DefaultDeps().Writable(dir); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("sobrou %v", ents)
	}
	if err := DefaultDeps().Writable(filepath.Join(dir, "nao-existe")); err == nil {
		t.Fatal("pasta inexistente devia dar erro")
	}
}
