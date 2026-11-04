package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mtrad/internal/bench"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
)

// fakeChatServer responde como um servidor compatível com a API de chat da OpenAI.
func fakeChatServer(t *testing.T, calls *int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "tradução de teste"}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeBenchConfig(t *testing.T, engines map[string]translate.EngineConfig, def string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"route":     map[string]any{"default": def},
		"translate": map[string]any{"engines": engines},
	})
	p := filepath.Join(t.TempDir(), "mtrad.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBenchMedeMotorLocal(t *testing.T) {
	var calls int64
	srv := fakeChatServer(t, &calls)
	cfg := writeBenchConfig(t, map[string]translate.EngineConfig{
		"llm": {Type: "openai", BaseURL: srv.URL + "/v1", Model: "m:1", TimeoutSec: 5},
	}, "llm")
	var out bytes.Buffer
	if err := stageBench(context.Background(), &out, cfg, "", 6, 5000, true, false, t.TempDir()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"motor llm (m:1)", "primeira resposta", "palavras/s", "mediana", "p95", "5000 palavras levariam", "não medido"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	if n := atomic.LoadInt64(&calls); n != 6 { // 1 de aquecimento + 5 medidas, uma frase por pedido
		t.Fatalf("pedidos ao servidor = %d, queria 6", n)
	}
}

func TestBenchPulaMotorDeNuvemSemPermissao(t *testing.T) {
	cfg := writeBenchConfig(t, map[string]translate.EngineConfig{
		"nuvem": {Type: "openai", BaseURL: "https://api.exemplo.invalid/v1", Model: "x", APIKeyEnv: "BENCH_TESTE_KEY"},
		"eco":   {Type: "echo"},
	}, "nuvem")
	var out bytes.Buffer
	err := stageBench(context.Background(), &out, cfg, "", 4, 0, false, false, t.TempDir())
	var ee exitErr
	if !errors.As(err, &ee) || ee.code != schema.ExitError {
		t.Fatalf("sem nenhum motor medido devia dar erro de saída 1: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "fora do seu computador") || !strings.Contains(got, "--allow-cloud") || !strings.Contains(got, "api.exemplo.invalid") {
		t.Errorf("devia explicar por que pulou a nuvem:\n%s", got)
	}
	if !strings.Contains(got, "motor de teste") {
		t.Errorf("devia dizer que pulou o echo:\n%s", got)
	}
}

func TestBenchNuvemComPermissaoSemChaveMostraOErro(t *testing.T) {
	t.Setenv("BENCH_TESTE_KEY", "") // a variável existe mas está vazia: NewEngine recusa
	cfg := writeBenchConfig(t, map[string]translate.EngineConfig{
		"nuvem": {Type: "openai", BaseURL: "https://api.exemplo.invalid/v1", Model: "x", APIKeyEnv: "BENCH_TESTE_KEY"},
	}, "nuvem")
	var out bytes.Buffer
	err := stageBench(context.Background(), &out, cfg, "", 4, 0, false, true, t.TempDir())
	if err == nil {
		t.Fatalf("sem chave nada foi medido, devia dar erro:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "BENCH_TESTE_KEY") || !strings.Contains(out.String(), "não consegui medir") {
		t.Errorf("devia dizer qual variável está vazia:\n%s", out.String())
	}
}

func TestBenchArgumentosInvalidos(t *testing.T) {
	cfg := writeBenchConfig(t, map[string]translate.EngineConfig{"e": {Type: "echo"}}, "e")
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"sem -c":    func() error { return stageBench(ctx, &bytes.Buffer{}, "", "", 8, 0, false, false, ".") },
		"n pequeno": func() error { return stageBench(ctx, &bytes.Buffer{}, cfg, "", 1, 0, false, false, ".") },
		"n grande": func() error {
			return stageBench(ctx, &bytes.Buffer{}, cfg, "", len(bench.Samples)+1, 0, false, false, ".")
		},
		"motor inexist": func() error { return stageBench(ctx, &bytes.Buffer{}, cfg, "nada", 8, 0, false, false, ".") },
	} {
		var ee exitErr
		if err := call(); !errors.As(err, &ee) || ee.code != schema.ExitUsage {
			t.Errorf("%s: devia ser erro de uso (2): %v", name, err)
		}
	}
	if err := stageBench(ctx, &bytes.Buffer{}, filepath.Join(t.TempDir(), "nao-existe.json"), "", 8, 0, false, false, "."); err == nil {
		t.Error("config inexistente devia dar erro")
	}
}

func TestIsLocalEngine(t *testing.T) {
	for _, tc := range []struct {
		ec   translate.EngineConfig
		want bool
	}{
		{translate.EngineConfig{Type: "openai", BaseURL: "http://127.0.0.1:11434/v1"}, true},
		{translate.EngineConfig{Type: "openai", BaseURL: "http://localhost:8080/v1"}, true},
		{translate.EngineConfig{Type: "libretranslate", BaseURL: "http://127.0.0.1:5000"}, true},
		{translate.EngineConfig{Type: "worker"}, true},
		{translate.EngineConfig{Type: "openai", BaseURL: "https://api.openai.com/v1"}, false},
		{translate.EngineConfig{Type: "openai", BaseURL: "http://192.168.0.10:11434/v1"}, false}, // rede de casa não é loopback
		{translate.EngineConfig{Type: "openai", BaseURL: "http://127.0.0.1.evil.com/v1"}, false},
		{translate.EngineConfig{Type: "openai", BaseURL: "::lixo::"}, false},
	} {
		if got := isLocalEngine(tc.ec); got != tc.want {
			t.Errorf("%+v: %v, queria %v", tc.ec, got, tc.want)
		}
	}
}

func TestPendingWords(t *testing.T) {
	work := t.TempDir()
	if _, ok := pendingWords(work); ok {
		t.Fatal("sem protected.jsonl não há como saber")
	}
	pa, err := schema.OpenAppend(filepath.Join(work, "protected.jsonl"), schema.VProtected)
	if err != nil {
		t.Fatal(err)
	}
	for id, m := range map[string]string{"a": "one two ⟦1⟧", "b": "three four five", "c": "six"} {
		if err := pa.Write(schema.ProtectedRec{ID: id, Masked: m}); err != nil {
			t.Fatal(err)
		}
	}
	pa.Close()
	if w, ok := pendingWords(work); !ok || w != 6 {
		t.Fatalf("sem nada traduzido: %d %v", w, ok)
	}
	ta, err := schema.OpenAppend(filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	if err != nil {
		t.Fatal(err)
	}
	ta.Write(schema.TranslatedRec{ID: "b", Out: "x"})
	ta.Close()
	if w, ok := pendingWords(work); !ok || w != 3 { // sobram a (2) e c (1)
		t.Fatalf("depois de traduzir o b: %d %v", w, ok)
	}
}

func TestBenchReportNaoInventaNumeros(t *testing.T) {
	var out bytes.Buffer
	benchReport(&out, bench.Result{Name: "x", Err: errors.New("connection refused")}, 1000, 0)
	if strings.Contains(out.String(), "palavras/s") || strings.Contains(out.String(), "estimativa") {
		t.Errorf("com erro não pode mostrar velocidade:\n%s", out.String())
	}
	out.Reset()
	benchReport(&out, bench.Result{Name: "x", Cold: time.Second}, 1000, 0) // 1 frase só: nada medido
	if !strings.Contains(out.String(), "poucas frases") || strings.Contains(out.String(), "estimativa") {
		t.Errorf("sem medida devia pedir -n maior:\n%s", out.String())
	}
	out.Reset()
	r := bench.Result{Name: "x", Model: "m", Cold: 3 * time.Second, Calls: 4, Texts: 4, Failed: 1, Words: 40, Elapsed: 10 * time.Second,
		Latencies: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}}
	benchReport(&out, r, 7200, 5)
	for _, want := range []string{"4,0 palavras/s", "falhas             1 de 5", "5 pedidos por minuto", "7200 palavras levariam cerca de 30 min 00 s"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("faltou %q:\n%s", want, out.String())
		}
	}
}
