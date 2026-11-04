package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mtrad/internal/assets"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mtrad/internal/route"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
	"mtrad/internal/version"
)

// initOpts reúne o que o init aceita.
type initOpts struct {
	Engine, Model string
	// BaseURL e APIKeyEnv só valem no motor "custom" (qualquer servidor compatível
	// com a API de chat da OpenAI). APIKeyEnv é o NOME da variável de ambiente.
	BaseURL, APIKeyEnv string
	// Fallback é um motor de nuvem (gemini, openai ou openrouter) que recebe os
	// textos que o motor principal não conseguiu traduzir. Vazio = nada sai do
	// computador.
	Fallback, FallbackModel string
}

// cloudPresets são os provedores de nuvem com API compatível com a da OpenAI.
// O mtrad só guarda o NOME da variável de ambiente da chave, nunca a chave.
var cloudPresets = map[string]struct{ BaseURL, KeyEnv, Host string }{
	"openai":     {"https://api.openai.com/v1", "OPENAI_API_KEY", "api.openai.com"},
	"openrouter": {"https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", "openrouter.ai"},
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// validEnvName confere que o texto é um NOME de variável de ambiente. Quem cola
// a chave da API no lugar do nome recebe um erro em vez de ter a chave gravada
// no arquivo de configuração.
func validEnvName(s string) error {
	if !envNameRe.MatchString(s) {
		return fmt.Errorf("%q não parece o nome de uma variável de ambiente (exemplo: OPENAI_API_KEY); informe só o NOME, nunca a chave", redact(s))
	}
	return nil
}

// redact corta um texto que pode ser uma chave colada por engano, para o erro
// não repetir a chave inteira na tela nem no registro.
func redact(s string) string {
	if len(s) > 6 {
		return s[:3] + "…"
	}
	return s
}

// initConfig monta a configuração inicial para um tipo de motor. Os nomes de
// modelo que dependem do que o usuário instalou não têm padrão: ou vêm de
// --model ou dá erro, em vez de chutar um nome.
func initConfig(engine, model string) (Config, []string, error) {
	return initConfigOpts(initOpts{Engine: engine, Model: model})
}

func initConfigOpts(o initOpts) (Config, []string, error) {
	var c Config
	c.ProtectStyle = "brackets"
	c.Glossary.Vanilla = true
	c.Translate.Retries = 2
	c.Translate.BackoffMs = 2000
	name, ec, notes, err := engineFor(o)
	if err != nil {
		return c, nil, err
	}
	c.Route = route.Config{Default: name}
	c.Translate.Engines = map[string]translate.EngineConfig{name: ec}
	if o.Fallback != "" {
		if o.Fallback == "custom" || o.Fallback == "llama" || o.Fallback == "ollama" || o.Fallback == "libre" || o.Fallback == "ct2" {
			return c, nil, fmt.Errorf("--fallback aceita gemini, openai ou openrouter (um motor de nuvem), não %q", o.Fallback)
		}
		if o.Fallback == o.Engine {
			return c, nil, fmt.Errorf("o motor reserva não pode ser o mesmo motor principal (%s)", o.Engine)
		}
		if o.FallbackModel == "" {
			return c, nil, errors.New("informe --fallback-model com o nome do modelo do motor reserva")
		}
		fname, fec, fnotes, err := engineFor(initOpts{Engine: o.Fallback, Model: o.FallbackModel})
		if err != nil {
			return c, nil, fmt.Errorf("motor reserva: %w", err)
		}
		if fname == name {
			return c, nil, fmt.Errorf("o motor reserva não pode ter o mesmo nome do principal (%s)", name)
		}
		ec.Fallback = fname
		c.Translate.Engines[name] = ec
		c.Translate.Engines[fname] = fec
		notes = append(notes, "motor reserva "+fname+": "+strings.Join(fnotes, "; "),
			"ATENÇÃO: os textos que o motor principal não traduzir serão enviados para a nuvem ("+fname+"). Sem --fallback, nada sai do seu computador")
	}
	return c, notes, nil
}

// engineFor devolve o nome, a configuração e as notas de um motor.
func engineFor(o initOpts) (string, translate.EngineConfig, []string, error) {
	engine, model := o.Engine, o.Model
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
			return "", ec, nil, err
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: model, PromptMode: "chat", MaxTokens: 512, Workers: 2, TimeoutSec: 120}
	case "gemini":
		name = "gemini"
		if err := need("o nome do modelo na sua conta do Google AI Studio"); err != nil {
			return "", ec, nil, err
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", APIKeyEnv: "GEMINI_API_KEY", Model: model, MaxTokens: 512, Workers: 1, RPM: 5, RPD: 200, TimeoutSec: 120}
		notes = append(notes, "exporte GEMINI_API_KEY antes de rodar", "rpm 5 e rpd 200 são valores prudentes, não os limites da sua conta: confira e ajuste no arquivo")
	case "openai", "openrouter":
		p := cloudPresets[engine]
		name = engine
		if err := need("o nome do modelo na sua conta de " + p.Host); err != nil {
			return "", ec, nil, err
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: p.BaseURL, APIKeyEnv: p.KeyEnv, Model: model, MaxTokens: 512, Workers: 2, TimeoutSec: 120}
		notes = append(notes,
			"exporte "+p.KeyEnv+" antes de rodar (o mtrad só guarda o nome da variável)",
			"os textos dos mods são enviados para "+p.Host+" e o uso é cobrado pelo provedor; não pus rpm/rpd porque não conheço os limites da sua conta (ajuste no arquivo se precisar)")
	case "custom":
		name = "custom"
		if err := need("o nome do modelo do servidor"); err != nil {
			return "", ec, nil, err
		}
		u, err := url.Parse(o.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", ec, nil, errors.New("informe --base-url com o endereço do servidor (exemplo: http://127.0.0.1:1234/v1)")
		}
		if o.APIKeyEnv != "" {
			if err := validEnvName(o.APIKeyEnv); err != nil {
				return "", ec, nil, err
			}
		}
		ec = translate.EngineConfig{Type: "openai", BaseURL: strings.TrimRight(o.BaseURL, "/"), APIKeyEnv: o.APIKeyEnv, Model: model, MaxTokens: 512, Workers: 2, TimeoutSec: 120}
		notes = append(notes, "o servidor precisa aceitar POST em <base-url>/chat/completions (API de chat da OpenAI)")
		if o.APIKeyEnv != "" {
			notes = append(notes, "exporte "+o.APIKeyEnv+" antes de rodar")
		}
		if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
			notes = append(notes, "os textos dos mods serão enviados para "+u.Host)
		}
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
		return "", ec, nil, fmt.Errorf("motor desconhecido %q (use llama, ollama, gemini, openai, openrouter, custom, libre ou ct2)", engine)
	}
	return name, ec, notes, nil
}

func stageInit(inst, out, engine, model string, overwrite bool) error {
	return stageInitOpts(inst, out, initOpts{Engine: engine, Model: model}, overwrite)
}

func stageInitOpts(inst, out string, o initOpts, overwrite bool) error {
	engine := o.Engine
	c, notes, err := initConfigOpts(o)
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
