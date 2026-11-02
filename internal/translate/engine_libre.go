package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// libreEngine usa a rota POST /translate do LibreTranslate (próprio ou
// hospedado). O glossário não é usado por este motor.
type libreEngine struct {
	name, url, key, src, tgt string
	batch                    int
	hc                       *http.Client
}

func newLibre(name string, c EngineConfig, env func(string) string) (Engine, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("motor %q: base_url é obrigatório", name)
	}
	e := &libreEngine{name: name, url: strings.TrimRight(c.BaseURL, "/") + "/translate", src: c.Source, tgt: c.Target,
		batch: c.Batch, hc: &http.Client{Timeout: c.timeout()}}
	if e.src == "" {
		e.src = "en"
	}
	if e.tgt == "" {
		e.tgt = "pt-BR"
	}
	if e.batch <= 0 {
		e.batch = 16
	}
	if c.APIKeyEnv != "" {
		e.key = env(c.APIKeyEnv)
		if e.key == "" {
			return nil, fmt.Errorf("motor %q: a variável %s está vazia", name, c.APIKeyEnv)
		}
	}
	return e, nil
}

func (e *libreEngine) Name() string   { return e.name }
func (e *libreEngine) Model() string  { return "libretranslate:" + e.tgt }
func (e *libreEngine) BatchSize() int { return e.batch }

func (e *libreEngine) Translate(ctx context.Context, items []Item) ([]Result, error) {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = it.Text
	}
	payload := map[string]any{"q": q, "source": e.src, "target": e.tgt, "format": "text"}
	if e.key != "" {
		payload["api_key"] = e.key
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp, b)
	}
	var r struct {
		Translated json.RawMessage `json:"translatedText"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("resposta inválida: %w", err)
	}
	var list []string
	if err := json.Unmarshal(r.Translated, &list); err != nil {
		var one string
		if err2 := json.Unmarshal(r.Translated, &one); err2 != nil || len(items) != 1 {
			return nil, fmt.Errorf("translatedText inesperado: %s", snippet(r.Translated))
		}
		list = []string{one}
	}
	if len(list) != len(items) {
		return nil, fmt.Errorf("pedi %d textos e vieram %d: %w", len(items), len(list), errCount)
	}
	out := make([]Result, len(items))
	for i, it := range items {
		out[i] = Result{ID: it.ID, Out: matchEdges(it.Text, list[i])}
	}
	return out, nil
}
