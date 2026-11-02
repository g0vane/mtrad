package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mtrad/internal/assets"
	"os"
	"path/filepath"

	"mtrad/internal/route"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
	"mtrad/internal/version"
)

// initConfig monta a configuração inicial para um tipo de motor. Os nomes de
// modelo que dependem do que o usuário instalou não têm padrão: ou vêm de
// --model ou dá erro, em vez de chutar um nome.
func initConfig(engine, model string) (Config, []string, error) {
	var c Config
	c.ProtectStyle = "brackets"
	c.Glossary.Vanilla = true
	c.Translate.Retries = 2
	c.Translate.BackoffMs = 2000
	var name string
	var ec translate.EngineConfig
	var notes []string
	need := func(what string) error {
		if model == "" {
			return fmt.Errorf("informe --model com %s", what)
		}
		return nil
	}
	switch engine {
	case "llama":
		name = "llm"
		if model == "" {
			model = "translategemma-4b"
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: model, PromptMode: "chat", MaxTokens: 512, Workers: 2, TimeoutSec: 120}
		notes = append(notes, "inicie o llama-server com o modelo antes de rodar; o llama-server ignora o nome do modelo, ele usa o que foi carregado")
	case "ollama":
		name = "llm"
		if err := need("o nome que `ollama list` mostra"); err != nil {
			return c, nil, err
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: model, PromptMode: "chat", MaxTokens: 512, Workers: 2, TimeoutSec: 120}
	case "gemini":
		name = "gemini"
		if err := need("o nome do modelo na sua conta do Google AI Studio"); err != nil {
			return c, nil, err
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", APIKeyEnv: "GEMINI_API_KEY", Model: model, MaxTokens: 512, Workers: 1, RPM: 5, RPD: 200, TimeoutSec: 120}
		notes = append(notes, "exporte GEMINI_API_KEY antes de rodar", "rpm 5 e rpd 200 são valores prudentes, não os limites da sua conta: confira e ajuste no arquivo")
	case "libre":
		name = "libre"
		ec = translate.EngineConfig{Type: "libretranslate", BaseURL: "http://127.0.0.1:5000", Target: "pt-BR", Batch: 16}
		notes = append(notes, "suba o LibreTranslate local antes de rodar")
	case "ct2":
		name = "ct2"
		if model == "" {
			model = "opus-mt-tc-big-en-pt"
		}
		ec = translate.EngineConfig{Type: "worker", Model: model, Batch: 16,
			Command: []string{"python3", "tools/ct2worker.py", "--backend", "ct2", "--model", "opus-en-pt", "--tokenizer", "Helsinki-NLP/opus-mt-tc-big-en-pt", "--text-prefix", ">>pob<< "}}
		notes = append(notes, "precisa de `pip install ctranslate2 sentencepiece transformers` e do modelo convertido em ./opus-en-pt; rode o mtrad na pasta que tem tools/ct2worker.py")
	default:
		return c, nil, fmt.Errorf("motor desconhecido %q (use llama, ollama, gemini, libre ou ct2)", engine)
	}
	c.Route = route.Config{Default: name}
	c.Translate.Engines = map[string]translate.EngineConfig{name: ec}
	return c, notes, nil
}

func stageInit(inst, out, engine, model string, overwrite bool) error {
	c, notes, err := initConfig(engine, model)
	if err != nil {
		return exitErr{2, err.Error()}
	}
	// -i é opcional: o motor, o modelo e as fontes não dependem da instância (a
	// interface faz o setup antes de escolher uma). Sem -i a instância não é lida.
	var in schema.Instance
	if inst != "" {
		if in, err = version.Detect(version.Options{InstanceDir: inst}); err != nil {
			return fmt.Errorf("não consegui ler a instância: %w", err)
		}
	}
	if _, err := os.Stat(out); err == nil && !overwrite {
		return exitErr{2, out + " já existe; use --overwrite para substituir"}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if dir := filepath.Dir(out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if inst != "" {
		fmt.Printf("init: instância com Minecraft %s, %s %s, formato de pacote %d.%d\n", in.McVersion, in.Loader, in.LoaderVersion, in.PackFormat.Major, in.PackFormat.Minor)
		for _, w := range in.Warnings {
			fmt.Println("  aviso:", w)
		}
	}
	fmt.Printf("init: configuração gravada em %s (motor %s)\n", out, engine)
	if engine == "ct2" {
		if wrote, err := assets.WriteIfMissing(filepath.Join("tools", "ct2worker.py"), assets.CT2Worker); err != nil {
			notes = append(notes, "não consegui gravar tools/ct2worker.py: "+err.Error())
		} else if wrote {
			notes = append(notes, "gravei tools/ct2worker.py (vem dentro do binário) na pasta atual")
		}
	}
	for _, n := range notes {
		fmt.Println("  nota:", n)
	}
	if inst == "" {
		inst = "PASTA_DA_INSTANCIA"
	}
	fmt.Printf("próximos passos:\n  mtrad doctor -c %s\n  mtrad run -i %s -c %s --limit 50    # teste pequeno\n  mtrad run -i %s -c %s\n", out, inst, out, inst, out)
	return nil
}
