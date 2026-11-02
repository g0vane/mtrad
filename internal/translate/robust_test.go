package translate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"mtrad/internal/review"
	"mtrad/internal/schema"
)

// fakeEngine deixa o teste decidir o que cada pedido devolve.
type fakeEngine struct {
	name  string
	batch int
	fn    func(items []Item) ([]Result, error)
	mu    sync.Mutex
	calls [][]string // IDs de cada pedido
}

func (f *fakeEngine) Name() string   { return f.name }
func (f *fakeEngine) Model() string  { return "fake" }
func (f *fakeEngine) BatchSize() int { return f.batch }
func (f *fakeEngine) Translate(ctx context.Context, items []Item) ([]Result, error) {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	f.mu.Lock()
	f.calls = append(f.calls, ids)
	f.mu.Unlock()
	return f.fn(items)
}

func okAll(prefix string) func([]Item) ([]Result, error) {
	return func(items []Item) ([]Result, error) {
		out := make([]Result, len(items))
		for i, it := range items {
			out[i] = Result{ID: it.ID, Out: prefix + it.Text}
		}
		return out, nil
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0}, {"7", 7 * time.Second}, {" 30 ", 30 * time.Second}, {"-5", 0}, {"abc", 0},
		{now.Add(90 * time.Second).UTC().Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in, now); got != c.want {
			t.Errorf("%q: %v, queria %v", c.in, got, c.want)
		}
	}
}

func TestOpenAI429UsaRetryAfter(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	eng, err := NewEngine("o", EngineConfig{Type: "openai", BaseURL: srv.URL, Model: "m"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Translate(context.Background(), []Item{{ID: "a", Text: "x"}})
	if d := retryAfterOf(err); d != 3*time.Second {
		t.Fatalf("Retry-After lido: %v (erro %v)", d, err)
	}
}

func TestRetryAfterLongoParaOMotor(t *testing.T) {
	out, _ := newOut(t)
	fe := &fakeEngine{name: "e", batch: 1, fn: func([]Item) ([]Result, error) {
		return nil, &statusError{Code: 429, Msg: "cota", RetryAfter: 3 * time.Hour}
	}}
	es := &errSink{}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1, MaxWait: time.Minute}}, Out: out, Errs: es, Retries: 3, Backoff: time.Millisecond}
	start := time.Now()
	st, _ := Execute(context.Background(), jobs(5, "e"), o)
	if time.Since(start) > 2*time.Second {
		t.Fatal("não devia ter esperado")
	}
	if st.Stopped["e"] == "" || !strings.Contains(st.Stopped["e"], "esperar") {
		t.Fatalf("o motor devia parar com o motivo: %+v", st.Stopped)
	}
	if len(fe.calls) != 1 {
		t.Fatalf("depois do pedido de espera longa não pode haver mais pedidos: %d", len(fe.calls))
	}
}

func TestRetryAfterCurtoEhRespeitado(t *testing.T) {
	out, _ := newOut(t)
	n := 0
	fe := &fakeEngine{name: "e", batch: 1, fn: func(items []Item) ([]Result, error) {
		n++
		if n == 1 {
			return nil, &statusError{Code: 429, Msg: "devagar", RetryAfter: 150 * time.Millisecond}
		}
		return okAll("[pt] ")(items)
	}}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Out: out, Errs: &errSink{}, Retries: 2, Backoff: time.Millisecond}
	start := time.Now()
	st, _ := Execute(context.Background(), jobs(1, "e"), o)
	if st.Translated != 1 {
		t.Fatalf("%+v", st)
	}
	if time.Since(start) < 140*time.Millisecond {
		t.Fatalf("devia esperar o Retry-After (150 ms), esperou %v", time.Since(start))
	}
}

func TestLotesPorCaracteres(t *testing.T) {
	js := []Job{{ID: "1", Masked: strings.Repeat("a", 40)}, {ID: "2", Masked: strings.Repeat("b", 40)}, {ID: "3", Masked: strings.Repeat("c", 40)},
		{ID: "4", Masked: strings.Repeat("d", 500)}, {ID: "5", Masked: "e"}}
	b := makeBatches(js, 10, 100)
	var shape []string
	for _, x := range b {
		var ids []string
		for _, j := range x {
			ids = append(ids, j.ID)
		}
		shape = append(shape, strings.Join(ids, ","))
	}
	if got := strings.Join(shape, " | "); got != "1,2 | 3 | 4 | 5" {
		t.Fatalf("lotes: %s", got)
	}
	// sem limite de caracteres só vale a quantidade
	if got := len(makeBatches(js, 2, 0)); got != 3 {
		t.Fatalf("lotes por quantidade: %d", got)
	}
}

func TestDivideLoteQueFalha(t *testing.T) {
	out, p := newOut(t)
	// o servidor recusa qualquer pedido que contenha o texto "VENENO"
	fe := &fakeEngine{name: "e", batch: 8, fn: func(items []Item) ([]Result, error) {
		for _, it := range items {
			if strings.Contains(it.Text, "VENENO") {
				return nil, &statusError{Code: 400, Msg: "texto inválido"}
			}
		}
		return okAll("[pt] ")(items)
	}}
	js := jobs(8, "e")
	js[5].Masked = "VENENO ⟦1⟧"
	es := &errSink{}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Out: out, Errs: es, Retries: 1, Backoff: time.Millisecond}
	st, err := Execute(context.Background(), js, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Translated != 7 || st.Failed != 1 {
		t.Fatalf("só o texto ruim devia falhar: %+v", st)
	}
	_ = p
	if len(es.msgs) != 1 || !strings.HasPrefix(es.msgs[0], "id005") {
		t.Fatalf("erros: %v", es.msgs)
	}
}

func TestNaoDivideQuandoOServidorEstaForaDoAr(t *testing.T) {
	out, _ := newOut(t)
	fe := &fakeEngine{name: "e", batch: 8, fn: func([]Item) ([]Result, error) { return nil, errors.New("connection refused") }}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Out: out, Errs: &errSink{}, Retries: 2, Backoff: time.Millisecond}
	st, _ := Execute(context.Background(), jobs(8, "e"), o)
	if st.Failed != 8 {
		t.Fatalf("%+v", st)
	}
	if len(fe.calls) != 3 { // 3 tentativas do lote inteiro, sem dividir
		t.Fatalf("pedidos: %d", len(fe.calls))
	}
}

func TestFallbackRecebeOQueFalhou(t *testing.T) {
	out, _ := newOut(t)
	main := &fakeEngine{name: "main", batch: 1, fn: func(items []Item) ([]Result, error) {
		if strings.Contains(items[0].Text, "RUIM") {
			return []Result{{ID: items[0].ID, Err: errors.New("não sei")}}, nil
		}
		return okAll("[main] ")(items)
	}}
	reserva := &fakeEngine{name: "reserva", batch: 1, fn: okAll("[reserva] ")}
	js := jobs(4, "main")
	js[2].Masked = "RUIM ⟦1⟧"
	o := Options{Engines: map[string]EngineRun{
		"main":    {Engine: main, Workers: 1, Fallback: "reserva"},
		"reserva": {Engine: reserva, Workers: 1},
	}, Out: out, Errs: &errSink{}, Retries: 0, Backoff: time.Millisecond}
	st, err := Execute(context.Background(), js, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Translated != 4 || st.Failed != 0 || st.Fallback["main"] != 1 {
		t.Fatalf("%+v", st)
	}
	if len(reserva.calls) != 1 || reserva.calls[0][0] != "id002" {
		t.Fatalf("a reserva devia receber só o id002: %v", reserva.calls)
	}
}

func TestFallbackQuandoOMotorParaPorLimiteDiario(t *testing.T) {
	out, _ := newOut(t)
	main := &fakeEngine{name: "main", batch: 1, fn: okAll("[main] ")}
	reserva := &fakeEngine{name: "reserva", batch: 1, fn: okAll("[reserva] ")}
	o := Options{Engines: map[string]EngineRun{
		"main":    {Engine: main, Workers: 1, Limiter: NewLimiter(0, 2, ""), Fallback: "reserva"},
		"reserva": {Engine: reserva, Workers: 1},
	}, Out: out, Errs: &errSink{}, Backoff: time.Millisecond}
	st, _ := Execute(context.Background(), jobs(6, "main"), o)
	if st.Translated != 6 || st.Failed != 0 {
		t.Fatalf("a reserva devia terminar o serviço: %+v", st)
	}
	if st.Stopped["main"] == "" {
		t.Fatal("o motivo da parada do motor principal devia continuar registrado")
	}
}

func TestFallbackSemReservaContinuaFalhando(t *testing.T) {
	out, _ := newOut(t)
	main := &fakeEngine{name: "main", batch: 1, fn: func(items []Item) ([]Result, error) {
		return []Result{{ID: items[0].ID, Err: errors.New("x")}}, nil
	}}
	o := Options{Engines: map[string]EngineRun{"main": {Engine: main, Workers: 1}}, Out: out, Errs: &errSink{}, Backoff: time.Millisecond}
	st, _ := Execute(context.Background(), jobs(3, "main"), o)
	if st.Failed != 3 || st.Translated != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestValidateDeConfiguracao(t *testing.T) {
	ok := Config{Engines: map[string]EngineConfig{"a": {Fallback: "b"}, "b": {}}, RetryEngine: "b", RetryStyle: "xml"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Config{
		"fallback inexistente": {Engines: map[string]EngineConfig{"a": {Fallback: "zzz"}}},
		"ciclo":                {Engines: map[string]EngineConfig{"a": {Fallback: "b"}, "b": {Fallback: "a"}}},
		"auto-ciclo":           {Engines: map[string]EngineConfig{"a": {Fallback: "a"}}},
		"retry_engine":         {Engines: map[string]EngineConfig{"a": {}}, RetryEngine: "zzz"},
		"retry_style":          {Engines: map[string]EngineConfig{"a": {}}, RetryStyle: "json"},
	} {
		if c.Validate() == nil {
			t.Errorf("%s: devia dar erro", name)
		}
	}
}

func TestMarcasXMLIdaEVolta(t *testing.T) {
	src := "Click ⟦1⟧here⟦2⟧ to ⟦10⟧go"
	x := toXMLMarks(src)
	if x != "Click <x1/>here<x2/> to <x10/>go" {
		t.Fatal(x)
	}
	if fromXMLMarks(x) != src {
		t.Fatal(fromXMLMarks(x))
	}
	if got := fromXMLMarks("a < x 3 /> b <x4> c"); got != "a ⟦3⟧ b ⟦4⟧ c" {
		t.Fatal(got)
	}
}

func TestRetryComXMLManda_xml_e_grava_colchetes(t *testing.T) {
	out, p := newOut(t)
	var seen string
	fe := &fakeEngine{name: "e", batch: 1, fn: func(items []Item) ([]Result, error) {
		seen = items[0].Text
		return []Result{{ID: items[0].ID, Out: "Clique <x1/>aqui<x2/>"}}, nil
	}}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Out: out, Errs: &errSink{}, Backoff: time.Millisecond}
	st, _ := Execute(context.Background(), []Job{{ID: "a", Engine: "e", Masked: "Click ⟦1⟧here⟦2⟧", Retry: true, XML: true}}, o)
	if st.Translated != 1 || seen != "Click <x1/>here<x2/>" {
		t.Fatalf("%+v visto %q", st, seen)
	}
	out.Close()
	var got string
	if err := readTranslated(p, func(id, o string) { got = o }); err != nil || got != "Clique ⟦1⟧aqui⟦2⟧" {
		t.Fatalf("gravado %q (%v)", got, err)
	}
}

// Golden: a chave sem prompt tem de continuar a MESMA que o mtrad 0.9 calculava,
// senão as 19 mil traduções do cache de quem atualiza são jogadas fora.
func TestCacheKeyCompativelComVersaoAnterior(t *testing.T) {
	h := sha256.New()
	for _, s := range []string{"llm", "translategemma:4b", "gl1", "brackets", "Hello ⟦1⟧"} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	want := hex.EncodeToString(h.Sum(nil))[:32]
	if got := CacheKey("llm", "translategemma:4b", "", "gl1", "brackets", "Hello ⟦1⟧"); got != want {
		t.Fatalf("a chave mudou: %s != %s", got, want)
	}
	if CacheKey("llm", "m", "p2", "g", "s", "t") == CacheKey("llm", "m", "", "g", "s", "t") {
		t.Fatal("a versão do prompt devia mudar a chave")
	}
}

// Se este teste falhar, alguém mudou o texto do prompt: suba promptRev (para o
// cache antigo não ser reaproveitado) e atualize o hash abaixo.
func TestPromptRevSobeComOPrompt(t *testing.T) {
	sum := sha256.Sum256([]byte(defaultSystem + "\x00" + plainPrompt))
	const goldenRev, goldenHash = 1, "8d0e2d505d0360aa"
	got := hex.EncodeToString(sum[:])[:16]
	if got != goldenHash || promptRev != goldenRev {
		t.Fatalf("o prompt mudou (hash %s) ou promptRev mudou (%d): suba promptRev e atualize o hash", got, promptRev)
	}
}

func TestPromptTagDoOpenAI(t *testing.T) {
	e := &openaiEngine{}
	if promptRev == 1 && e.PromptTag() != "" {
		t.Fatal("a revisão 1 não pode mudar a chave do cache")
	}
}

func TestPlanNotice(t *testing.T) {
	m := planNotice(1000, 900, 150, 100, 40, 10, 5000)
	if strings.Contains(m, "aviso") {
		t.Fatalf("sem motivo para avisar: %s", m)
	}
	m = planNotice(1000, 1000, 600, 0, 600, 0, 5000)
	if !strings.Contains(m, "mudaram de origem") {
		t.Fatalf("devia avisar de origem mudada: %s", m)
	}
	m = planNotice(1000, 1000, 10, 0, 0, 10, 0)
	if !strings.Contains(m, "cache está vazio") {
		t.Fatalf("devia avisar de cache vazio: %s", m)
	}
	m = planNotice(1000, 0, 1000, 1000, 0, 0, 0) // primeira execução: cache vazio é normal
	if strings.Contains(m, "aviso") {
		t.Fatalf("primeira execução não avisa: %s", m)
	}
}

func readTranslated(path string, fn func(id, out string)) error {
	return schema.Each(path, schema.VTranslated, func(r schema.TranslatedRec) error { fn(r.ID, r.Out); return nil })
}

func writeWork(t *testing.T, texts map[string]string, engine string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	os.MkdirAll(work, 0o755)
	pw, _ := schema.Create(filepath.Join(work, "protected.jsonl"), schema.VProtected)
	rw, _ := schema.Create(filepath.Join(work, "routed.jsonl"), schema.VRouted)
	ids := make([]string, 0, len(texts))
	for id := range texts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pw.Write(schema.ProtectedRec{ID: id, Masked: texts[id], Style: "brackets"})
		rw.Write(schema.RoutedRec{ID: id, Engine: engine})
	}
	pw.Close()
	rw.Close()
	return work
}

func TestRunRetryEngineEXMLeAvisoDePlano(t *testing.T) {
	work := writeWork(t, map[string]string{"ok": "Hello", "bad": "Click ⟦1⟧here⟦2⟧"}, "echo")
	a, _ := schema.OpenAppend(filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "ok", Out: "Olá", MSha: schema.Sha("Hello")})
	a.Write(schema.TranslatedRec{ID: "bad", Out: "estragado", MSha: schema.Sha("Click ⟦1⟧here⟦2⟧")})
	a.Close()
	fw, _ := schema.Create(filepath.Join(work, "failed.jsonl"), schema.VFailed)
	fw.Write(schema.FailedRec{ID: "bad", Out: "estragado", Reason: []string{"marca perdida"}})
	fw.Close()
	var notices []string
	cfg := Config{
		Engines:     map[string]EngineConfig{"echo": {Type: "echo"}, "grande": {Type: "echo"}},
		RetryEngine: "grande", RetryStyle: "xml", BackoffMs: 1,
		Notice: func(m string) { notices = append(notices, m) },
	}
	st, err := Run(context.Background(), work, cfg, &errSink{}, nil)
	if err != nil || st.Translated != 1 || st.Retried != 1 {
		t.Fatalf("%v %+v", err, st)
	}
	recs, _ := schema.ReadAll[schema.TranslatedRec](filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	last := recs[len(recs)-1]
	if last.ID != "bad" || last.Engine != "grande" || last.Out != "[pt] Click ⟦1⟧here⟦2⟧" {
		t.Fatalf("o retry devia ir ao motor 'grande' com marcas convertidas de volta: %+v", last)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "1 reprovados") {
		t.Fatalf("avisos: %q", notices)
	}
}

func TestRunConfigInvalidaNaoComecaATraduzir(t *testing.T) {
	work := writeWork(t, map[string]string{"a": "Hello"}, "echo")
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo", Fallback: "nao-existe"}}}
	if _, err := Run(context.Background(), work, cfg, &errSink{}, nil); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "translated.jsonl")); err == nil {
		t.Fatal("não devia ter criado translated.jsonl")
	}
}

func TestCacheHitGravaMSha(t *testing.T) {
	work := writeWork(t, map[string]string{"a": "Hello"}, "echo")
	cache, _ := OpenCache(filepath.Join(work, "cache", "cache.jsonl"))
	cache.Put(CacheKey("echo", "echo", "", "", "brackets", "Hello"), "Olá do cache")
	cache.Close()
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo"}}, BackoffMs: 1}
	st, err := Run(context.Background(), work, cfg, &errSink{}, nil)
	if err != nil || st.CacheHits != 1 {
		t.Fatalf("%v %+v", err, st)
	}
	recs, _ := schema.ReadAll[schema.TranslatedRec](filepath.Join(work, "translated.jsonl"), schema.VTranslated)
	if len(recs) != 1 || recs[0].MSha != schema.Sha("Hello") {
		t.Fatalf("o registro vindo do cache devia levar o hash do texto de origem: %+v", recs)
	}
}

// A retomada depois de uma queda: a última linha de translated.jsonl ficou
// pela metade; nada pode ser refeito além dela e o arquivo continua íntegro.
func TestRunRetomaDepoisDeLinhaCortada(t *testing.T) {
	work := writeWork(t, map[string]string{"a": "Hello a", "b": "Hello b", "c": "Hello c"}, "echo")
	tp := filepath.Join(work, "translated.jsonl")
	a, _ := schema.OpenAppend(tp, schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "a", Out: "A", MSha: schema.Sha("Hello a")})
	a.Write(schema.TranslatedRec{ID: "b", Out: "B", MSha: schema.Sha("Hello b")})
	a.Close()
	f, _ := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"c","engine":"echo","out":"C inco`) // queda no meio da gravação
	f.Close()
	old := schema.OnRepair
	schema.OnRepair = func(string, int64) {}
	defer func() { schema.OnRepair = old }()
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo"}}, BackoffMs: 1}
	st, err := Run(context.Background(), work, cfg, &errSink{}, nil)
	if err != nil || st.Translated != 1 || st.Skipped != 2 {
		t.Fatalf("%v %+v", err, st)
	}
	recs, err := schema.ReadAll[schema.TranslatedRec](tp, schema.VTranslated)
	if err != nil || len(recs) != 3 {
		t.Fatalf("%v %d", err, len(recs))
	}
}

// Texto com correção manual que ainda vale não vai ao motor, nem sem tradução
// anterior; correção velha (o original mudou) não protege.
func TestRunSkipsReviewedTexts(t *testing.T) {
	work := writeWork(t, map[string]string{"rev": "Hello rev", "revold": "Hello revold", "plain": "Hello plain"}, "echo")
	tw, _ := schema.Create(filepath.Join(work, "todo.jsonl"), schema.VTodo)
	for _, id := range []string{"plain", "rev", "revold"} {
		tw.Write(schema.TodoRec{ID: id, En: "Hello " + id, EnSha: schema.Sha("Hello " + id), Reason: "missing"})
	}
	tw.Close()
	if err := review.Append(work,
		review.Entry{ID: "rev", Pt: "Olá rev", EnSha: schema.Sha("Hello rev")},
		review.Entry{ID: "revold", Pt: "Olá velho", EnSha: schema.Sha("Hello outro texto")},
	); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Engines: map[string]EngineConfig{"echo": {Type: "echo"}}, BackoffMs: 1}
	st, err := Run(context.Background(), work, cfg, &errSink{}, nil)
	if err != nil || st.Translated != 2 || st.Skipped != 1 {
		t.Fatalf("%v %+v", err, st)
	}
	got := map[string]bool{}
	readTranslated(filepath.Join(work, "translated.jsonl"), func(id, out string) { got[id] = true })
	if got["rev"] || !got["revold"] || !got["plain"] {
		t.Fatalf("traduzidos: %v", got)
	}
}
