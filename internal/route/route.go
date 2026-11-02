// Package route escolhe o motor de cada texto com regras simples: a primeira
// regra que casar vence; sem regra, vale o motor padrão.
package route

import (
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"mtrad/internal/schema"
)

// Rule casa um texto quando todos os limites informados (maiores que zero)
// são respeitados.
type Rule struct {
	MinChars  int    `json:"min_chars,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
	MinTokens int    `json:"min_tokens,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Engine    string `json:"engine"`
}

type Config struct {
	Default string `json:"default"`
	Rules   []Rule `json:"rules,omitempty"`
}

func (c Config) Validate() error {
	if c.Default == "" {
		return fmt.Errorf("route: falta o motor padrão")
	}
	for i, r := range c.Rules {
		if r.Engine == "" {
			return fmt.Errorf("route: regra %d sem motor", i+1)
		}
	}
	return nil
}

func (r Rule) matches(chars, tokens int) bool {
	if r.MinChars > 0 && chars < r.MinChars {
		return false
	}
	if r.MaxChars > 0 && chars > r.MaxChars {
		return false
	}
	if r.MinTokens > 0 && tokens < r.MinTokens {
		return false
	}
	if r.MaxTokens > 0 && tokens > r.MaxTokens {
		return false
	}
	return true
}

// Choose devolve o nome do motor para um texto protegido.
func Choose(c Config, p schema.ProtectedRec) string {
	chars := utf8.RuneCountInString(p.Masked)
	for _, r := range c.Rules {
		if r.matches(chars, len(p.Tokens)) {
			return r.Engine
		}
	}
	return c.Default
}

// Run lê protected.jsonl e grava routed.jsonl. Devolve a contagem por motor.
func Run(workDir string, c Config) (map[string]int, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if _, err := schema.RequireFresh(workDir, "protect", "protected.jsonl"); err != nil {
		return nil, err
	}
	w, err := schema.Create(filepath.Join(workDir, "routed.jsonl"), schema.VRouted)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	err = schema.Each(filepath.Join(workDir, "protected.jsonl"), schema.VProtected, func(p schema.ProtectedRec) error {
		e := Choose(c, p)
		counts[e]++
		return w.Write(schema.RoutedRec{ID: p.ID, Engine: e})
	})
	if err != nil {
		w.Abort()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	err = schema.WriteDone(workDir, "route", []string{"protected.jsonl"}, []string{"routed.jsonl"}, counts)
	return counts, err
}
