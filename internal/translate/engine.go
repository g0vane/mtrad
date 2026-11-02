// Package translate manda os textos protegidos aos motores de tradução, com
// fila, limite de pedidos, cache e retomada. Os motores ficam neste pacote
// atrás da interface Engine.
package translate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mtrad/internal/glossary"
)

// Item é um texto para traduzir. Terms são os termos do glossário que
// aparecem nele.
type Item struct {
	ID    string
	Text  string
	Terms []glossary.Term
	// Retry: a resposta anterior para este texto foi reprovada; motores que
	// aceitam temperatura usam uma maior, senão a resposta repete igual.
	Retry bool
	// XML: o texto já veio com as marcas no formato <xn/> (retry_style: xml).
	XML bool
}

// Result é a resposta para um item; Err preenchido significa que falhou.
type Result struct {
	ID  string
	Out string
	Err error
}

type Engine interface {
	Name() string
	Model() string
	BatchSize() int
	// Translate devolve um Result por item, na mesma ordem. Um erro geral
	// quer dizer que o pedido inteiro falhou e pode ser repetido.
	Translate(ctx context.Context, items []Item) ([]Result, error)
}

// EngineConfig descreve um motor no arquivo de configuração.
type EngineConfig struct {
	Type       string  `json:"type"` // echo, openai, libretranslate, worker
	BaseURL    string  `json:"base_url,omitempty"`
	APIKeyEnv  string  `json:"api_key_env,omitempty"`
	Model      string  `json:"model,omitempty"`
	PromptMode string  `json:"prompt_mode,omitempty"` // chat (padrão) ou plain
	Temp       float64 `json:"temperature,omitempty"`
	MaxTokens  int     `json:"max_tokens,omitempty"`
	TimeoutSec int     `json:"timeout_sec,omitempty"`
	Batch      int     `json:"batch,omitempty"`
	Workers    int     `json:"workers,omitempty"`
	// BatchChars: limite de caracteres por lote nos motores que mandam vários
	// textos de uma vez (0 = só pela quantidade, campo batch).
	BatchChars int `json:"batch_chars,omitempty"`
	// Fallback: nome de outro motor da configuração que recebe os textos que
	// este não conseguiu traduzir (ou todos os que faltam, se este parar).
	Fallback string `json:"fallback,omitempty"`
	// MaxWaitSec: maior espera aceita quando o servidor manda Retry-After
	// (padrão 120); se ele pedir mais, o motor para.
	MaxWaitSec int      `json:"max_wait_sec,omitempty"`
	RPM        int      `json:"rpm,omitempty"`
	RPD        int      `json:"rpd,omitempty"`
	Source     string   `json:"source,omitempty"`
	Target     string   `json:"target,omitempty"`
	Command    []string `json:"command,omitempty"` // type=worker
}

func (c EngineConfig) timeout() time.Duration {
	if c.TimeoutSec > 0 {
		return time.Duration(c.TimeoutSec) * time.Second
	}
	return 120 * time.Second
}

// NewEngine cria o motor descrito por cfg.
func NewEngine(name string, cfg EngineConfig, env func(string) string) (Engine, error) {
	switch cfg.Type {
	case "echo":
		return &echoEngine{name: name}, nil
	case "openai":
		return newOpenAI(name, cfg, env)
	case "libretranslate":
		return newLibre(name, cfg, env)
	case "worker":
		return newWorker(name, cfg)
	default:
		return nil, fmt.Errorf("motor %q: tipo desconhecido %q", name, cfg.Type)
	}
}

// echoEngine devolve o texto com um prefixo; serve para testar o fluxo sem motor.
type echoEngine struct{ name string }

func (e *echoEngine) Name() string   { return e.name }
func (e *echoEngine) Model() string  { return "echo" }
func (e *echoEngine) BatchSize() int { return 8 }
func (e *echoEngine) Translate(ctx context.Context, items []Item) ([]Result, error) {
	out := make([]Result, len(items))
	for i, it := range items {
		out[i] = Result{ID: it.ID, Out: "[pt] " + it.Text}
	}
	return out, nil
}

// matchEdges copia para out os espaços e tabs do começo e do fim de src e tira
// as quebras de linha que o modelo põe nas pontas (o texto vem sem quebras
// reais: o protect as troca por marcas).
func matchEdges(src, out string) string {
	t := strings.Trim(out, " \t\r\n")
	lead := src[:len(src)-len(strings.TrimLeft(src, " \t"))]
	trail := src[len(strings.TrimRight(src, " \t")):]
	if strings.Trim(src, " \t") == "" {
		return src
	}
	return lead + t + trail
}
