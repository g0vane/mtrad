package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mtrad/internal/glossary"
	"mtrad/internal/schema"
)

type errSink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *errSink) Add(src, id, msg string) {
	s.mu.Lock()
	s.msgs = append(s.msgs, id+": "+msg)
	s.mu.Unlock()
}

func afterTexto(user string) string {
	if i := strings.LastIndex(user, "Texto:\n"); i >= 0 {
		return user[i+len("Texto:\n"):]
	}
	return user
}

func newOut(t *testing.T) (*schema.Appender, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "translated.jsonl")
	a, err := schema.OpenAppend(p, schema.VTranslated)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, p
}

func jobs(n int, engine string) []Job {
	var j []Job
	for i := 0; i < n; i++ {
		j = append(j, Job{ID: fmt.Sprintf("id%03d", i), Engine: engine, Masked: fmt.Sprintf("Text %d ⟦1⟧", i)})
	}
	return j
}

func TestExecuteEchoAndCache(t *testing.T) {
	out, p := newOut(t)
	cache, err := OpenCache(filepath.Join(t.TempDir(), "c.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	o := Options{Engines: map[string]EngineRun{"e": {Engine: &echoEngine{name: "e"}, Workers: 3}}, Cache: cache, Out: out, Errs: &errSink{}, Style: "brackets", Backoff: time.Millisecond}
	st, err := Execute(context.Background(), jobs(20, "e"), o)
	if err != nil || st.Translated != 20 || st.CacheHits != 0 || st.Failed != 0 {
		t.Fatalf("%v %+v", err, st)
	}
	// segunda vez: tudo vem do cache
	st, err = Execute(context.Background(), jobs(20, "e"), o)
	if err != nil || st.Translated != 0 || st.CacheHits != 20 {
		t.Fatalf("%v %+v", err, st)
	}
	got, err := schema.ReadAll[schema.TranslatedRec](p, schema.VTranslated)
	if err != nil || len(got) != 40 {
		t.Fatalf("%v %d", err, len(got))
	}
	for _, r := range got {
		if !strings.HasPrefix(r.Out, "[pt] Text ") || r.Engine != "e" {
			t.Fatalf("%+v", r)
		}
	}
	if !strings.HasSuffix(got[len(got)-1].Model, "(cache)") {
		t.Fatalf("a segunda rodada deveria vir do cache: %+v", got[len(got)-1])
	}
	// glossário diferente invalida o cache
	g := glossary.New([]glossary.Term{{En: "Text", Pt: "Texto"}})
	o.Gloss = g
	st, _ = Execute(context.Background(), jobs(3, "e"), o)
	if st.CacheHits != 0 || st.Translated != 3 {
		t.Fatalf("%+v", st)
	}
}

func TestCachePersistsAcrossOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonl")
	c, _ := OpenCache(p)
	k := CacheKey("e", "m", "", "g", "s", "txt")
	_ = c.Put(k, "saída")
	c.Close()
	c2, err := OpenCache(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if v, ok := c2.Get(k); !ok || v != "saída" || c2.Len() != 1 {
		t.Fatalf("%q %v", v, ok)
	}
	if CacheKey("e", "m", "", "g", "s", "txt") == CacheKey("e", "m2", "", "g", "s", "txt") {
		t.Fatal("o modelo deve entrar na chave")
	}
}

func TestOpenAIEngine(t *testing.T) {
	var calls int32
	var lastGlossaryUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer segredo" {
			http.Error(w, "caminho ou chave errada", 404)
			return
		}
		n := atomic.AddInt32(&calls, 1)
		b := map[string]any{}
		json.NewDecoder(r.Body).Decode(&b)
		msgs := b["messages"].([]any)
		user := msgs[len(msgs)-1].(map[string]any)["content"].(string)
		if strings.Contains(user, "Glossário") {
			lastGlossaryUser = user
		}
		switch {
		case strings.Contains(user, "RATE"):
			if n < 3 {
				http.Error(w, "muitos pedidos", 429)
				return
			}
		case strings.Contains(user, "LONG"):
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "cortado"}, "finish_reason": "length"}}})
			return
		case strings.Contains(user, "BAD"):
			http.Error(w, "ruim", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "  PT:" + strings.TrimSpace(afterTexto(user)) + "  "}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	t.Setenv("TESTKEY", "segredo")
	eng, err := NewEngine("llm", EngineConfig{Type: "openai", BaseURL: srv.URL + "/v1", Model: "m", APIKeyEnv: "TESTKEY"}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	out, p := newOut(t)
	es := &errSink{}
	gl := glossary.New([]glossary.Term{{En: "Iron", Pt: "Ferro"}})
	js := []Job{
		{ID: "a", Engine: "llm", Masked: " Iron ⟦1⟧ "},
		{ID: "b", Engine: "llm", Masked: "RATE x"},
		{ID: "c", Engine: "llm", Masked: "LONG y"},
		{ID: "d", Engine: "llm", Masked: "BAD z"},
	}
	st, err := Execute(context.Background(), js, Options{Engines: map[string]EngineRun{"llm": {Engine: eng, Workers: 1}}, Gloss: gl, Out: out, Errs: es, Retries: 3, Backoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if st.Translated != 2 || st.Failed != 2 {
		t.Fatalf("%+v erros=%v", st, es.msgs)
	}
	got, _ := schema.ReadAll[schema.TranslatedRec](p, schema.VTranslated)
	by := map[string]schema.TranslatedRec{}
	for _, r := range got {
		by[r.ID] = r
	}
	if by["a"].Out != " PT:Iron ⟦1⟧ " {
		t.Fatalf("espaços das pontas devem ser mantidos: %q", by["a"].Out)
	}
	if !strings.Contains(lastGlossaryUser, "Iron → Ferro") {
		t.Fatalf("o glossário deveria ir no pedido: %q", lastGlossaryUser)
	}
	if by["b"].Attempt < 2 {
		t.Fatalf("429 deveria ter feito nova tentativa: %+v", by["b"])
	}
}

func TestOpenAIPromptHasGlossary(t *testing.T) {
	e := &openaiEngine{}
	m := e.messages(Item{Text: "Iron Sword", Terms: []glossary.Term{{En: "Iron", Pt: "Ferro"}}})
	if len(m) != 2 || m[0].Role != "system" || !strings.Contains(m[1].Content, "Iron → Ferro") || !strings.HasSuffix(m[1].Content, "Iron Sword") {
		t.Fatalf("%+v", m)
	}
	e.mode = "plain"
	m = e.messages(Item{Text: "Iron Sword"})
	if len(m) != 1 || m[0].Role != "user" || !strings.Contains(m[0].Content, "pt-BR") {
		t.Fatalf("%+v", m)
	}
	if _, err := NewEngine("x", EngineConfig{Type: "openai"}, os.Getenv); err == nil {
		t.Fatal("sem base_url deveria falhar")
	}
	if _, err := NewEngine("x", EngineConfig{Type: "openai", BaseURL: "u", Model: "m", APIKeyEnv: "VAZIA_XYZ"}, os.Getenv); err == nil {
		t.Fatal("chave vazia deveria falhar")
	}
	if _, err := NewEngine("x", EngineConfig{Type: "nada"}, os.Getenv); err == nil {
		t.Fatal("tipo desconhecido")
	}
}

func TestLibreEngineBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Q      []string `json:"q"`
			Target string   `json:"target"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/translate" || b.Target != "pt-BR" {
			http.Error(w, "x", 400)
			return
		}
		res := make([]string, len(b.Q))
		for i, q := range b.Q {
			res[i] = strings.ToUpper(q)
		}
		if len(b.Q) == 3 { // simula resposta com tamanho errado
			res = res[:2]
		}
		json.NewEncoder(w).Encode(map[string]any{"translatedText": res})
	}))
	defer srv.Close()
	eng, err := NewEngine("lt", EngineConfig{Type: "libretranslate", BaseURL: srv.URL, Batch: 4}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Translate(context.Background(), []Item{{ID: "1", Text: "a b"}, {ID: "2", Text: " c"}})
	if err != nil || res[0].Out != "A B" || res[1].Out != " C" {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := eng.Translate(context.Background(), []Item{{ID: "1", Text: "a"}, {ID: "2", Text: "b"}, {ID: "3", Text: "c"}}); err == nil {
		t.Fatal("tamanho errado deveria dar erro")
	}
	if eng.BatchSize() != 4 {
		t.Fatal(eng.BatchSize())
	}
}

const fakeWorker = `import sys, json
def send(o):
    sys.stdout.write(json.dumps(o, ensure_ascii=False) + "\n"); sys.stdout.flush()
first = sys.stdin.readline()
send({"hello": 1})
crash_marker = sys.argv[1] if len(sys.argv) > 1 else ""
for line in sys.stdin:
    r = json.loads(line)
    if r["text"] == "CRASH" and crash_marker:
        import os
        if not os.path.exists(crash_marker):
            open(crash_marker, "w").write("x")
            sys.exit(3)
    if r["text"] == "ERR":
        send({"id": r["id"], "error": "falhou de propósito"})
    else:
        send({"id": r["id"], "out": r["text"].upper()})
`

func TestWorkerEngine(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("sem python3")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "w.py")
	os.WriteFile(script, []byte(fakeWorker), 0o644)
	marker := filepath.Join(dir, "crashed")
	eng, err := NewEngine("w", EngineConfig{Type: "worker", Command: []string{"python3", script, marker}, Batch: 8, TimeoutSec: 10}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.(*workerEngine).Close()
	res, err := eng.Translate(context.Background(), []Item{{ID: "1", Text: "olá"}, {ID: "2", Text: "ERR"}})
	if err != nil || res[0].Out != "OLÁ" || res[1].Err == nil {
		t.Fatalf("%v %+v", err, res)
	}
	// o worker morre uma vez no meio do lote; o Go reinicia e reenvia
	res, err = eng.Translate(context.Background(), []Item{{ID: "3", Text: "x"}, {ID: "4", Text: "CRASH"}, {ID: "5", Text: "z"}})
	if err != nil || res[0].Out != "X" || res[1].Out != "CRASH" || res[2].Out != "Z" {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := NewEngine("w", EngineConfig{Type: "worker"}, os.Getenv); err == nil {
		t.Fatal("sem command deveria falhar")
	}
	bad, _ := NewEngine("b", EngineConfig{Type: "worker", Command: []string{"python3", "-c", "print('oi')"}, TimeoutSec: 2}, os.Getenv)
	if _, err := bad.Translate(context.Background(), []Item{{ID: "1", Text: "a"}}); err == nil {
		t.Fatal("hello inválido deveria falhar")
	}
}

func TestLimiterPacingAndDaily(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	var slept []time.Duration
	l := NewLimiter(60, 3, filepath.Join(t.TempDir(), "s.json"))
	l.now = func() time.Time { return now }
	l.sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Wait(context.Background()); err != ErrDailyLimit {
		t.Fatalf("esperava limite diário, veio %v", err)
	}
	// 60 por minuto = 1 s entre pedidos: 0, 1 s, 2 s
	if len(slept) != 3 || slept[0] != 0 || slept[1] != time.Second || slept[2] != 2*time.Second {
		t.Fatalf("%v", slept)
	}
	// o contador do dia sobrevive a um novo Limiter
	l2 := NewLimiter(0, 3, l.state)
	l2.now = func() time.Time { return now }
	if err := l2.Wait(context.Background()); err != ErrDailyLimit {
		t.Fatalf("o estado deveria ter sido lido: %v", err)
	}
	// no dia seguinte zera
	l2.now = func() time.Time { return now.Add(24 * time.Hour) }
	if err := l2.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteStopsOnDailyLimit(t *testing.T) {
	out, _ := newOut(t)
	lim := NewLimiter(0, 2, "")
	es := &errSink{}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: &echoEngine{name: "e"}, Limiter: lim, Workers: 1}}, Out: out, Errs: es, Backoff: time.Millisecond}
	// lotes de 8: 40 textos = 5 lotes; só 2 pedidos permitidos
	st, err := Execute(context.Background(), jobs(40, "e"), o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Translated != 16 || st.Stopped["e"] == "" {
		t.Fatalf("%+v", st)
	}
}

func TestRunResumeFromFiles(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	pw, _ := schema.Create(filepath.Join(work, "protected.jsonl"), schema.VProtected)
	rw, _ := schema.Create(filepath.Join(work, "routed.jsonl"), schema.VRouted)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id%d", i)
		pw.Write(schema.ProtectedRec{ID: id, Masked: "Hello " + id, Style: "brackets"})
		rw.Write(schema.RoutedRec{ID: id, Engine: "echo"})
	}
	pw.Close()
	rw.Close()
	// duas já traduzidas numa execução anterior
	a, _ := schema.OpenAppend(filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "id0", Out: "x"})
	a.Write(schema.TranslatedRec{ID: "id1", Out: "y"})
	a.Close()
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo"}}, BackoffMs: 1}
	es := &errSink{}
	st, err := Run(context.Background(), work, cfg, es, nil)
	if err != nil || st.Translated != 3 {
		t.Fatalf("%v %+v %v", err, st, es.msgs)
	}
	got, _ := schema.ReadAll[schema.TranslatedRec](filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	if len(got) != 5 {
		t.Fatalf("%d", len(got))
	}
	// rodar de novo não traduz nada
	st, err = Run(context.Background(), work, cfg, es, nil)
	if err != nil || st.Translated != 0 {
		t.Fatalf("%v %+v", err, st)
	}
	// cache dentro do work
	if _, err := os.Stat(filepath.Join(work, "cache", "cache.jsonl")); err != nil {
		t.Fatal(err)
	}
}

// mantém o texto da tradução antiga quando a origem não mudou; refaz quando
// mudou; refaz também o que a validação reprovou (sem usar o cache)
func TestRunStaleAndFailedAreRedone(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	pw, _ := schema.Create(filepath.Join(work, "protected.jsonl"), schema.VProtected)
	rw, _ := schema.Create(filepath.Join(work, "routed.jsonl"), schema.VRouted)
	for _, id := range []string{"same", "changed", "bad", "old"} {
		pw.Write(schema.ProtectedRec{ID: id, Masked: "Hello " + id + " v2", Style: "brackets"})
		rw.Write(schema.RoutedRec{ID: id, Engine: "echo"})
	}
	pw.Close()
	rw.Close()
	a, _ := schema.OpenAppend(filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "same", Out: "ok", MSha: schema.Sha("Hello same v2")})
	a.Write(schema.TranslatedRec{ID: "changed", Out: "velho", MSha: schema.Sha("Hello changed v1")})
	a.Write(schema.TranslatedRec{ID: "bad", Out: "ruim", MSha: schema.Sha("Hello bad v2")})
	a.Write(schema.TranslatedRec{ID: "old", Out: "sem hash"}) // arquivo antigo: vale
	a.Close()
	// a validação reprovou "bad" (e uma reprovação de "same" que já foi refeita)
	fw, _ := schema.Create(filepath.Join(work, "failed.jsonl"), schema.VFailed)
	fw.Write(schema.FailedRec{ID: "bad", Out: "ruim", Reason: []string{"marca perdida"}})
	fw.Write(schema.FailedRec{ID: "same", Out: "antiga", Reason: []string{"marca perdida"}})
	fw.Close()
	// o cache tem a resposta ruim de "bad"; o retry não pode usá-la
	cache, _ := OpenCache(filepath.Join(root, "cache", "cache.jsonl"))
	cache.Put(CacheKey("echo", "echo", "", "", "brackets", "Hello bad v2"), "ruim")
	cache.Close()
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo"}}, BackoffMs: 1}
	st, err := Run(context.Background(), work, cfg, &errSink{}, nil)
	if err != nil || st.Translated != 2 || st.Stale != 1 || st.Retried != 1 || st.Skipped != 2 || st.CacheHits != 0 {
		t.Fatalf("%v %+v", err, st)
	}
	all, _ := schema.ReadAll[schema.TranslatedRec](filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	last := map[string]string{}
	for _, r := range all {
		last[r.ID] = r.Out
	}
	if last["bad"] != "[pt] Hello bad v2" || last["changed"] != "[pt] Hello changed v2" || last["same"] != "ok" || last["old"] != "sem hash" {
		t.Fatalf("%v", last)
	}
}

func TestOpenAIRetryRaisesTemperature(t *testing.T) {
	var temps []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := map[string]any{}
		json.NewDecoder(r.Body).Decode(&b)
		temps = append(temps, b["temperature"].(float64))
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	eng, err := NewEngine("llm", EngineConfig{Type: "openai", BaseURL: srv.URL + "/v1", Model: "m"}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Translate(context.Background(), []Item{{ID: "a", Text: "x"}, {ID: "b", Text: "y", Retry: true}}); err != nil {
		t.Fatal(err)
	}
	if len(temps) != 2 || temps[0] != 0 || temps[1] != retryTemp {
		t.Fatalf("%v", temps)
	}
}

func TestCacheMovesIntoWorkAndCopiesOldOne(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	// sem cache nenhum: o caminho novo, dentro do work
	p, err := resolveCachePath(work, "")
	if err != nil || p != filepath.Join(work, "cache", "cache.jsonl") {
		t.Fatalf("%q %v", p, err)
	}
	// só existe o cache do lugar antigo: é copiado e o antigo continua lá
	old := filepath.Join(root, "cache", "cache.jsonl")
	c, _ := OpenCache(old)
	c.Put("k", "valor")
	c.Close()
	p, err = resolveCachePath(work, "")
	if err != nil {
		t.Fatal(err)
	}
	n, err := OpenCache(p)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if v, ok := n.Get("k"); !ok || v != "valor" {
		t.Fatalf("cache copiado sem a entrada: %q %v", v, ok)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("o cache antigo não deveria sumir")
	}
	// caminho configurado manda
	if p, _ := resolveCachePath(work, "/x/y.jsonl"); p != "/x/y.jsonl" {
		t.Fatal(p)
	}
}

// Ctrl+C (ctx cancelado) não espera o tempo limite do motor nem os 10 minutos
// do hello: volta logo, mesmo com o worker parado e sem ler a entrada.
func TestWorkerEngineCancelReturnsPromptly(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("sem python3")
	}
	dir := t.TempDir()
	hang := filepath.Join(dir, "hang.py")
	os.WriteFile(hang, []byte("import sys, json, time\nsys.stdin.readline()\nsys.stdout.write('{\"hello\": 1}\\n'); sys.stdout.flush()\ntime.sleep(600)\n"), 0o644)
	silent := filepath.Join(dir, "silent.py")
	os.WriteFile(silent, []byte("import time\ntime.sleep(600)\n"), 0o644)
	for name, script := range map[string]string{"parado depois do hello": hang, "sem hello": silent} {
		eng, err := NewEngine("w", EngineConfig{Type: "worker", Command: []string{"python3", script}, Batch: 8, TimeoutSec: 300}, os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(300 * time.Millisecond); cancel() }()
		start := time.Now()
		_, err = eng.Translate(ctx, []Item{{ID: "1", Text: "a"}})
		if err == nil || time.Since(start) > 10*time.Second {
			t.Errorf("%s: err=%v depois de %v", name, err, time.Since(start))
		}
		eng.(*workerEngine).Close()
	}
}

// O aviso de progresso é chamado um de cada vez e com contagem crescente, mesmo
// com vários trabalhadores: quem o recebe pode guardar estado sem trava (-race).
func TestProgressCalledSeriallyAndIncreasing(t *testing.T) {
	out, _ := newOut(t)
	last, calls := 0, 0
	o := Options{
		Engines: map[string]EngineRun{"e": {Engine: &echoEngine{name: "e"}, Workers: 4}},
		Out:     out,
		Errs:    &errSink{},
		Backoff: time.Millisecond,
		Progress: func(done, total int) {
			if done < last || total != 400 {
				t.Errorf("progresso fora de ordem: %d depois de %d (total %d)", done, last, total)
			}
			last = done
			calls++
		},
	}
	if _, err := Execute(context.Background(), jobs(400, "e"), o); err != nil {
		t.Fatal(err)
	}
	if last != 400 || calls == 0 {
		t.Fatalf("last=%d calls=%d", last, calls)
	}
}
