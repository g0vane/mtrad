package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// openaiEngine fala com qualquer servidor compatível com a API de chat da
// OpenAI: llama-server, Ollama, Gemini (rota beta compatível), etc.
type openaiEngine struct {
	name, model, url, key, mode string
	temp                        float64
	maxTokens                   int
	hc                          *http.Client
}

// promptRev é a versão dos prompts abaixo. Quem mudar defaultSystem ou
// plainPrompt DEVE subir este número: ele entra na chave do cache, e sem isso
// as traduções feitas com o prompt antigo seriam reaproveitadas como se fossem
// do novo. O teste TestPromptRevSobeComOPrompt avisa se alguém esquecer.
const promptRev = 1

const defaultSystem = "Você traduz textos de interface de jogos (Minecraft e seus mods) do inglês para o português do Brasil. " +
	"Regras: mantenha intactas as marcas numeradas do texto (cada uma exatamente uma vez); " +
	"use os termos do glossário quando eles aparecerem; não explique nada; responda somente com a tradução, sem aspas."

const plainPrompt = "Translate the following English text to Brazilian Portuguese (pt-BR). " +
	"Keep numbered markers unchanged. Output only the translation.\n\n"

// PromptTag identifica a versão do prompt na chave do cache. A versão 1 (a
// original) devolve vazio, para o cache antigo continuar valendo.
func (e *openaiEngine) PromptTag() string {
	if promptRev <= 1 {
		return ""
	}
	return "p" + strconv.Itoa(promptRev)
}

func newOpenAI(name string, c EngineConfig, env func(string) string) (Engine, error) {
	if c.BaseURL == "" || c.Model == "" {
		return nil, fmt.Errorf("motor %q: base_url e model são obrigatórios", name)
	}
	e := &openaiEngine{name: name, model: c.Model, url: strings.TrimRight(c.BaseURL, "/") + "/chat/completions",
		mode: c.PromptMode, temp: c.Temp, maxTokens: c.MaxTokens, hc: &http.Client{Timeout: c.timeout()}}
	if c.APIKeyEnv != "" {
		e.key = env(c.APIKeyEnv)
		if e.key == "" {
			return nil, fmt.Errorf("motor %q: a variável %s está vazia", name, c.APIKeyEnv)
		}
	}
	if e.maxTokens == 0 {
		e.maxTokens = 1024
	}
	return e, nil
}

func (e *openaiEngine) Name() string   { return e.name }
func (e *openaiEngine) Model() string  { return e.model }
func (e *openaiEngine) BatchSize() int { return 1 }

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (e *openaiEngine) messages(it Item) []chatMsg {
	if e.mode == "plain" {
		// para modelos com formato fixo de tradução (sem campo de sistema)
		return []chatMsg{{"user", plainPrompt + it.Text}}
	}
	user := it.Text
	if len(it.Terms) > 0 {
		var b strings.Builder
		b.WriteString("Glossário:\n")
		for _, t := range it.Terms {
			b.WriteString("- " + t.En + " → " + t.Pt + "\n")
		}
		b.WriteString("\nTexto:\n" + it.Text)
		user = b.String()
	}
	return []chatMsg{{"system", defaultSystem}, {"user", user}}
}

// retryTemp é a temperatura usada ao refazer um texto reprovado.
const retryTemp = 0.5

func (e *openaiEngine) Translate(ctx context.Context, items []Item) ([]Result, error) {
	out := make([]Result, len(items))
	for i, it := range items {
		temp := e.temp
		if it.Retry && temp < retryTemp {
			temp = retryTemp
		}
		body, _ := json.Marshal(map[string]any{
			"model": e.model, "messages": e.messages(it), "temperature": temp,
			"max_tokens": e.maxTokens, "stream": false,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if e.key != "" {
			req.Header.Set("Authorization", "Bearer "+e.key)
		}
		resp, err := e.hc.Do(req)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, httpError(resp, b)
		}
		if resp.StatusCode != http.StatusOK {
			out[i] = Result{ID: it.ID, Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(b))}
			continue
		}
		var r struct {
			Choices []struct {
				Message      chatMsg `json:"message"`
				FinishReason string  `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(b, &r); err != nil || len(r.Choices) == 0 {
			out[i] = Result{ID: it.ID, Err: fmt.Errorf("resposta sem choices: %s", snippet(b))}
			continue
		}
		if r.Choices[0].FinishReason == "length" {
			out[i] = Result{ID: it.ID, Err: fmt.Errorf("resposta cortada por max_tokens")}
			continue
		}
		out[i] = Result{ID: it.ID, Out: matchEdges(it.Text, r.Choices[0].Message.Content)}
	}
	return out, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
