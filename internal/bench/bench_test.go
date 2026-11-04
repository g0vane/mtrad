package bench

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mtrad/internal/translate"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// fakeEng gasta tempo de mentira: cold na 1ª chamada, depois per por chamada.
type fakeEng struct {
	c     *clock
	batch int
	cold  time.Duration
	per   time.Duration
	calls int
	fail  func(call int, items []translate.Item) ([]translate.Result, error)
	seen  [][]string
}

func (f *fakeEng) Name() string   { return "fake" }
func (f *fakeEng) Model() string  { return "m:1" }
func (f *fakeEng) BatchSize() int { return f.batch }
func (f *fakeEng) Translate(_ context.Context, items []translate.Item) ([]translate.Result, error) {
	f.calls++
	var texts []string
	for _, it := range items {
		texts = append(texts, it.Text)
	}
	f.seen = append(f.seen, texts)
	if f.calls == 1 {
		f.c.t = f.c.t.Add(f.cold)
	} else {
		f.c.t = f.c.t.Add(f.per)
	}
	if f.fail != nil {
		if r, err := f.fail(f.calls, items); r != nil || err != nil {
			return r, err
		}
	}
	out := make([]translate.Result, len(items))
	for i, it := range items {
		out[i] = translate.Result{ID: it.ID, Out: "[pt] " + it.Text}
	}
	return out, nil
}

func newFake(batch int) (*fakeEng, Options) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	return &fakeEng{c: c, batch: batch, cold: 8 * time.Second, per: 2 * time.Second}, Options{Now: c.now,
		Samples: []string{"one two three", "four five", "six seven eight nine", "ten ⟦1⟧ eleven"}}
}

func TestPrimeiraRespostaFicaForaDaMedia(t *testing.T) {
	f, o := newFake(1)
	r := Run(context.Background(), f, o)
	if r.Err != nil || !r.OK() {
		t.Fatalf("%+v", r)
	}
	if r.Cold != 8*time.Second {
		t.Fatalf("Cold = %v", r.Cold)
	}
	// 3 frases medidas (2+4+2 palavras... "four five"=2, "six seven eight nine"=4, "ten ⟦1⟧ eleven"=2) em 3 chamadas de 2 s
	if r.Calls != 3 || r.Texts != 3 || r.Words != 8 || r.Elapsed != 6*time.Second {
		t.Fatalf("%+v", r)
	}
	if got := r.WordsPerSec(); got < 1.33 || got > 1.34 {
		t.Fatalf("WordsPerSec = %v, queria 8/6", got)
	}
	if got := r.TextsPerSec(); got != 0.5 {
		t.Fatalf("TextsPerSec = %v", got)
	}
}

func TestMotorEmLoteRecebeAPrimeiraSozinha(t *testing.T) {
	f, o := newFake(2)
	o.Samples = append(o.Samples, "twelve thirteen") // 5 frases: 1 de aquecimento + 4 em 2 lotes
	r := Run(context.Background(), f, o)
	if r.Err != nil {
		t.Fatal(err2(r))
	}
	if len(f.seen[0]) != 1 {
		t.Fatalf("a 1ª chamada (aquecimento) devia levar 1 frase: %v", f.seen[0])
	}
	if len(f.seen[1]) != 2 || len(f.seen[2]) != 2 {
		t.Fatalf("o resto devia ir em lotes de 2: %v", f.seen)
	}
	if r.Calls != 2 || r.Texts != 4 {
		t.Fatalf("%+v", r)
	}
}

func err2(r Result) string { return r.Err.Error() }

func TestFalhaDeUmTextoContaEmFailed(t *testing.T) {
	f, o := newFake(1)
	f.fail = func(call int, items []translate.Item) ([]translate.Result, error) {
		if call == 3 {
			return []translate.Result{{ID: items[0].ID, Err: errors.New("recusou")}}, nil
		}
		return nil, nil
	}
	r := Run(context.Background(), f, o)
	if r.Err != nil || r.Failed != 1 || r.Texts != 2 {
		t.Fatalf("%+v", r)
	}
	if f.calls != 4 {
		t.Fatalf("o teste não deve repetir pedido que falhou: %d chamadas", f.calls)
	}
}

func TestErroDoMotorPara(t *testing.T) {
	f, o := newFake(1)
	f.fail = func(call int, _ []translate.Item) ([]translate.Result, error) {
		if call == 2 {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}
	r := Run(context.Background(), f, o)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "connection refused") || r.OK() {
		t.Fatalf("%+v", r)
	}
	// motor fora do ar já na 1ª frase
	f, o = newFake(1)
	f.fail = func(int, []translate.Item) ([]translate.Result, error) { return nil, errors.New("sem rede") }
	if r := Run(context.Background(), f, o); r.Err == nil || r.Cold != 0 {
		t.Fatalf("%+v", r)
	}
	// a 1ª frase volta com erro de item
	f, o = newFake(1)
	f.fail = func(call int, items []translate.Item) ([]translate.Result, error) {
		return []translate.Result{{ID: items[0].ID, Err: errors.New("chave recusada")}}, nil
	}
	if r := Run(context.Background(), f, o); r.Err == nil || !strings.Contains(r.Err.Error(), "primeira frase") {
		t.Fatalf("%+v", r)
	}
}

func TestRespostaComTamanhoErradoEErro(t *testing.T) {
	f, o := newFake(1)
	f.fail = func(call int, items []translate.Item) ([]translate.Result, error) {
		if call == 2 {
			return []translate.Result{}, nil // vazio, mas não nil
		}
		return nil, nil
	}
	if r := Run(context.Background(), f, o); r.Err == nil || !strings.Contains(r.Err.Error(), "respostas") {
		t.Fatalf("%+v", r)
	}
}

func TestSemFrasesECancelamento(t *testing.T) {
	f, o := newFake(1)
	o.Samples = nil
	if r := Run(context.Background(), f, o); r.Err == nil {
		t.Fatal("sem frases devia dar erro")
	}
	f, o = newFake(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := Run(ctx, f, o); r.Err == nil {
		t.Fatalf("contexto cancelado devia parar: %+v", r)
	}
}

func TestWaitNaoEntraNaMedida(t *testing.T) {
	f, o := newFake(1)
	o.Wait = func(context.Context) error { f.c.t = f.c.t.Add(30 * time.Second); return nil } // limite de pedidos
	r := Run(context.Background(), f, o)
	if r.Elapsed != 6*time.Second || r.Cold != 8*time.Second {
		t.Fatalf("a espera do limite não pode entrar na medida: %+v", r)
	}
	// um Wait que falha (limite diário) para o teste
	f, o = newFake(1)
	o.Wait = func(context.Context) error { return errors.New("limite diário") }
	if r := Run(context.Background(), f, o); r.Err == nil {
		t.Fatalf("%+v", r)
	}
}

func TestPercentilEEstimativa(t *testing.T) {
	r := Result{Calls: 5, Elapsed: 15 * time.Second, Words: 30,
		Latencies: []time.Duration{5 * time.Second, time.Second, 2 * time.Second, 4 * time.Second, 3 * time.Second}}
	if got := r.Percentile(50); got != 3*time.Second {
		t.Fatalf("mediana = %v", got)
	}
	if got := r.Percentile(95); got != 5*time.Second {
		t.Fatalf("p95 = %v", got)
	}
	if got := r.Percentile(0); got != time.Second {
		t.Fatalf("p0 = %v", got)
	}
	if (Result{}).Percentile(50) != 0 {
		t.Fatal("sem medidas = 0")
	}
	// 30 palavras em 15 s = 2/s; 7200 palavras = 3600 s
	d, ok := r.Estimate(7200)
	if !ok || d != time.Hour {
		t.Fatalf("Estimate = %v %v", d, ok)
	}
	if _, ok := r.Estimate(0); ok {
		t.Fatal("sem palavras não há estimativa")
	}
	if _, ok := (Result{Err: errors.New("x"), Calls: 1, Elapsed: time.Second, Words: 5}).Estimate(100); ok {
		t.Fatal("teste com erro não deve estimar")
	}
	if _, ok := (Result{}).Estimate(100); ok {
		t.Fatal("sem medida não deve inventar número")
	}
}

func TestAmostrasValidas(t *testing.T) {
	if len(Samples) < 24 {
		t.Fatalf("poucas frases: %d", len(Samples))
	}
	seen := map[string]bool{}
	for _, s := range Samples {
		if strings.TrimSpace(s) == "" || seen[s] || translate.CountWords(s) < 2 {
			t.Fatalf("frase inválida ou repetida: %q", s)
		}
		seen[s] = true
	}
	// as marcas têm que estar no formato do mtrad (⟦n⟧), nunca %s ou §
	for _, s := range Samples {
		if strings.ContainsAny(s, "%§&") {
			t.Fatalf("a frase %q usa marca que não é ⟦n⟧", s)
		}
	}
}
