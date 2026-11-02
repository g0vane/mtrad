package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseCfg = `{
  "sources": ["lang", "ftbq"],
  "protect_style": "brackets",
  "route": {"default": "llm"},
  "translate": {"engines": {"llm": {"type": "openai", "base_url": "http://127.0.0.1:11434/v1", "model": "translategemma:4b", "workers": 2}}, "retries": 2}
}`

func cfgFile(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "mtrad.json")
	if err := os.WriteFile(p, []byte(baseCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCfg(t *testing.T, p string, args ...string) (string, error) {
	var out bytes.Buffer
	err := stageConfig(&out, p, args)
	return out.String(), err
}

func TestConfigAddRemoveSkipMods(t *testing.T) {
	p := cfgFile(t)
	if _, err := runCfg(t, p, "add", "skip_mods", "chipped", "rechiseled", "chipped"); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(p)
	if err != nil || strings.Join(c.SkipMods, ",") != "chipped,rechiseled" {
		t.Fatalf("%v %v", c.SkipMods, err)
	}
	if _, err := runCfg(t, p, "remove", "skip_mods", "chipped"); err != nil {
		t.Fatal(err)
	}
	c, _ = loadConfig(p)
	if strings.Join(c.SkipMods, ",") != "rechiseled" {
		t.Fatalf("%v", c.SkipMods)
	}
	if _, err := runCfg(t, p, "remove", "skip_mods", "rechiseled"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "skip_mods") {
		t.Fatalf("lista vazia devia sumir do arquivo:\n%s", b)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatal("o arquivo anterior devia ficar em .bak")
	}
}

func TestConfigSetNested(t *testing.T) {
	p := cfgFile(t)
	for _, a := range [][]string{
		{"set", "translate.engines.llm.model", "outro:1b"},
		{"set", "translate.engines.llm.workers", "4"},
		{"set", "translate.retry_style", "xml"},
		{"set", "translate.engines.reserva", `{"type":"echo"}`},
		{"set", "translate.engines.llm.fallback", "reserva"},
	} {
		if _, err := runCfg(t, p, a...); err != nil {
			t.Fatalf("%v: %v", a, err)
		}
	}
	c, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	llm := c.Translate.Engines["llm"]
	if llm.Model != "outro:1b" || llm.Workers != 4 || llm.Fallback != "reserva" || c.Translate.RetryStyle != "xml" {
		t.Fatalf("%+v %+v", llm, c.Translate)
	}
	out, err := runCfg(t, p, "get", "translate.engines.llm.model")
	if err != nil || strings.TrimSpace(out) != `"outro:1b"` {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := runCfg(t, p, "unset", "translate.retry_style"); err != nil {
		t.Fatal(err)
	}
	if c, _ = loadConfig(p); c.Translate.RetryStyle != "" {
		t.Fatal("unset não apagou")
	}
}

func TestConfigRecusaAlteracaoInvalida(t *testing.T) {
	p := cfgFile(t)
	before, _ := os.ReadFile(p)
	for name, a := range map[string][]string{
		"campo desconhecido":    {"set", "translate.engines.llm.modelo", "x"},
		"tipo errado":           {"set", "translate.engines.llm.workers", `"muitos"`},
		"fonte desconhecida":    {"add", "sources", "bogus"},
		"fallback inexistente":  {"set", "translate.engines.llm.fallback", "nada"},
		"lista que não é lista": {"add", "protect_style", "x"},
	} {
		if _, err := runCfg(t, p, a...); err == nil {
			t.Errorf("%s: devia recusar", name)
		}
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("o arquivo não pode mudar quando a alteração é recusada")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Fatal("não devia criar .bak quando nada foi gravado")
	}
}

func TestConfigUsoErrado(t *testing.T) {
	p := cfgFile(t)
	for _, a := range [][]string{{}, {"set"}, {"set", "x"}, {"add", "skip_mods"}, {"get"}, {"dance"}} {
		if _, err := runCfg(t, p, a...); err == nil {
			t.Errorf("%v devia dar erro", a)
		}
	}
	if _, err := runCfg(t, p, "get", "nao.existe"); err == nil {
		t.Error("get de chave ausente devia dar erro")
	}
	if _, err := runCfg(t, filepath.Join(t.TempDir(), "nada.json"), "show"); err == nil {
		t.Error("arquivo ausente devia dar erro")
	}
}

func TestParseValue(t *testing.T) {
	if parseValue("12").(interface{ String() string }).String() != "12" {
		t.Fatal("número")
	}
	if parseValue("true") != true {
		t.Fatal("bool")
	}
	if parseValue("translategemma:4b") != "translategemma:4b" {
		t.Fatal("texto")
	}
	if l, ok := parseValue(`["a","b"]`).([]any); !ok || len(l) != 2 {
		t.Fatal("lista")
	}
}
