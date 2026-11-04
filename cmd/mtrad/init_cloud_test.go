package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, c Config) Config {
	t.Helper()
	b, _ := json.Marshal(c)
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, b, 0o644)
	back, err := loadConfig(p) // recusa campos desconhecidos e confere o fallback
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return back
}

func TestInitProvedoresDeNuvem(t *testing.T) {
	for _, tc := range []struct{ engine, url, env string }{
		{"openai", "https://api.openai.com/v1", "OPENAI_API_KEY"},
		{"openrouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
	} {
		if _, _, err := initConfig(tc.engine, ""); err == nil || !strings.Contains(err.Error(), "--model") {
			t.Errorf("%s sem --model devia pedir o modelo: %v", tc.engine, err)
		}
		c, notes, err := initConfig(tc.engine, "algum-modelo")
		if err != nil {
			t.Fatal(err)
		}
		back := roundTrip(t, c)
		ec := back.Translate.Engines[tc.engine]
		if back.Route.Default != tc.engine || ec.Type != "openai" || ec.BaseURL != tc.url || ec.APIKeyEnv != tc.env || ec.Model != "algum-modelo" {
			t.Fatalf("%s: %+v", tc.engine, ec)
		}
		if ec.RPM != 0 || ec.RPD != 0 {
			t.Errorf("%s: não devia inventar limites de pedidos: %+v", tc.engine, ec)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "enviados para") {
			t.Errorf("%s: as notas devem avisar que os textos saem do computador: %v", tc.engine, notes)
		}
	}
}

func TestInitCustom(t *testing.T) {
	if _, _, err := initConfigOpts(initOpts{Engine: "custom", Model: "m"}); err == nil || !strings.Contains(err.Error(), "--base-url") {
		t.Fatalf("custom sem --base-url devia pedir o endereço: %v", err)
	}
	for _, bad := range []string{"ftp://x/v1", "127.0.0.1:1234/v1", "http://", "///"} {
		if _, _, err := initConfigOpts(initOpts{Engine: "custom", Model: "m", BaseURL: bad}); err == nil {
			t.Errorf("--base-url %q devia ser recusado", bad)
		}
	}
	if _, _, err := initConfigOpts(initOpts{Engine: "custom", BaseURL: "http://127.0.0.1:1234/v1"}); err == nil {
		t.Error("custom sem --model devia dar erro")
	}
	// servidor local sem chave: sem aviso de envio para fora
	c, notes, err := initConfigOpts(initOpts{Engine: "custom", Model: "m", BaseURL: "http://127.0.0.1:1234/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	ec := roundTrip(t, c).Translate.Engines["custom"]
	if ec.BaseURL != "http://127.0.0.1:1234/v1" || ec.APIKeyEnv != "" {
		t.Fatalf("%+v", ec)
	}
	if strings.Contains(strings.Join(notes, "\n"), "serão enviados") {
		t.Errorf("servidor local não devia avisar envio para fora: %v", notes)
	}
	// servidor remoto: avisa
	_, notes, err = initConfigOpts(initOpts{Engine: "custom", Model: "m", BaseURL: "https://api.exemplo.com/v1", APIKeyEnv: "EXEMPLO_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "serão enviados para api.exemplo.com") {
		t.Errorf("servidor remoto devia avisar: %v", notes)
	}
}

// Quem cola a chave da API no lugar do nome da variável não pode ter a chave
// gravada no arquivo nem repetida inteira na mensagem de erro.
func TestInitRecusaChaveNoLugarDoNomeDaVariavel(t *testing.T) {
	key := "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	_, _, err := initConfigOpts(initOpts{Engine: "custom", Model: "m", BaseURL: "https://api.exemplo.com/v1", APIKeyEnv: key})
	if err == nil {
		t.Fatal("uma chave no lugar do nome devia ser recusada")
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), key[:20]) {
		t.Fatalf("o erro repete a chave: %v", err)
	}
	if !strings.Contains(err.Error(), "NOME") {
		t.Fatalf("o erro devia explicar que é só o nome: %v", err)
	}
	for _, ok := range []string{"MY_KEY", "_x", "OPENAI_API_KEY"} {
		if err := validEnvName(ok); err != nil {
			t.Errorf("%q devia ser aceito: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1ABC", "a-b", "a b", "a.b", strings.Repeat("A", 65), "chave=segredo"} {
		if err := validEnvName(bad); err == nil {
			t.Errorf("%q devia ser recusado", bad)
		}
	}
}

func TestInitFallbackNaNuvem(t *testing.T) {
	c, notes, err := initConfigOpts(initOpts{Engine: "ollama", Model: "translategemma:4b", Fallback: "openai", FallbackModel: "um-modelo"})
	if err != nil {
		t.Fatal(err)
	}
	back := roundTrip(t, c) // loadConfig também confere que o fallback existe e não há ciclo
	if back.Route.Default != "llm" || back.Translate.Engines["llm"].Fallback != "openai" {
		t.Fatalf("o motor local devia apontar para o reserva: %+v", back.Translate.Engines["llm"])
	}
	fb := back.Translate.Engines["openai"]
	if fb.Model != "um-modelo" || fb.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("%+v", fb)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "ATENÇÃO") {
		t.Errorf("com reserva na nuvem as notas devem avisar: %v", notes)
	}
	// sem --fallback, nenhum aviso e nenhum campo fallback
	c, notes, _ = initConfigOpts(initOpts{Engine: "ollama", Model: "m:1"})
	if c.Translate.Engines["llm"].Fallback != "" || strings.Contains(strings.Join(notes, "\n"), "ATENÇÃO") {
		t.Errorf("sem --fallback nada devia sair do computador: %+v %v", c.Translate.Engines["llm"], notes)
	}
}

func TestInitFallbackRecusasClaras(t *testing.T) {
	for name, o := range map[string]initOpts{
		"sem modelo":        {Engine: "ollama", Model: "m:1", Fallback: "openai"},
		"mesmo motor":       {Engine: "openai", Model: "a", Fallback: "openai", FallbackModel: "b"},
		"reserva local":     {Engine: "ollama", Model: "m:1", Fallback: "llama", FallbackModel: "x"},
		"reserva custom":    {Engine: "ollama", Model: "m:1", Fallback: "custom", FallbackModel: "x"},
		"reserva ct2":       {Engine: "ollama", Model: "m:1", Fallback: "ct2", FallbackModel: "x"},
		"reserva inventada": {Engine: "ollama", Model: "m:1", Fallback: "nada", FallbackModel: "x"},
	} {
		if _, _, err := initConfigOpts(o); err == nil {
			t.Errorf("%s: devia dar erro", name)
		}
	}
}

// A chave só existe no ambiente: o arquivo gravado guarda o NOME da variável.
func TestStageInitNaoGravaAChave(t *testing.T) {
	const secret = "SEGREDO-NAO-GRAVAR-123"
	t.Setenv("OPENAI_API_KEY", secret)
	out := filepath.Join(t.TempDir(), "mtrad.json")
	if err := stageInitOpts("", out, initOpts{Engine: "ollama", Model: "m:1", Fallback: "openai", FallbackModel: "x"}, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), secret) {
		t.Fatalf("a chave foi parar no arquivo:\n%s", b)
	}
	if !strings.Contains(string(b), `"api_key_env": "OPENAI_API_KEY"`) {
		t.Fatalf("devia guardar o nome da variável:\n%s", b)
	}
}
