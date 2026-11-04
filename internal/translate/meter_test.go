package translate

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCountWords(t *testing.T) {
	for in, want := range map[string]int{
		"":                                 0,
		"   ":                              0,
		"Right-click to open ⟦1⟧ the menu": 5, // right-click, to, open, the, menu
		"⟦1⟧⟦2⟧":                           0,
		"<x1/> Hello <x2/>world":           2, // as marcas XML também não contam
		"— ... !!":                         0, // só pontuação
		"Iron Ingot x3":                    3,
		"Ação rápida":                      2, // letras acentuadas contam
		"Hold [Shift] to show ⟦1⟧":         4,
	} {
		if got := CountWords(in); got != want {
			t.Errorf("CountWords(%q) = %d, queria %d", in, got, want)
		}
	}
}

// fakeClock avança só quando o teste manda.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestMeter() (*Meter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return &Meter{now: c.now}, c
}

func TestMeterNilEAntesDeComecar(t *testing.T) {
	var m *Meter
	m.begin(10) // não pode entrar em pânico
	m.addCached(1)
	m.addTranslated(1)
	m.stop()
	if r := m.Snapshot(); r != (Rate{}) {
		t.Fatalf("medidor nil: %+v", r)
	}
	m2, _ := newTestMeter()
	if r := m2.Snapshot(); r != (Rate{}) {
		t.Fatalf("antes de começar: %+v", r)
	}
}

func TestMeterVelocidadeNaoContaOCache(t *testing.T) {
	m, c := newTestMeter()
	m.begin(1000)
	m.addCached(400) // veio do cache, instantâneo
	m.addTranslated(100)
	c.advance(10 * time.Second)
	r := m.Snapshot()
	if r.WordsPerSec != 10 {
		t.Fatalf("100 palavras do motor em 10 s = 10/s, veio %v (o cache não pode inflar)", r.WordsPerSec)
	}
	if r.DoneWords != 500 || r.EngineWords != 100 || r.TotalWords != 1000 {
		t.Fatalf("%+v", r)
	}
	// faltam 500 palavras a 10/s = 50 s
	if !r.RemainingKnown || r.Remaining != 50*time.Second {
		t.Fatalf("faltam: %+v", r)
	}
}

func TestMeterNaoPrevêOFimCedoDemais(t *testing.T) {
	m, c := newTestMeter()
	m.begin(1000)
	m.addTranslated(50)
	c.advance(time.Second) // menos de 3 s: o ritmo ainda oscila
	if r := m.Snapshot(); r.RemainingKnown {
		t.Fatalf("não devia prever com 1 s de dados: %+v", r)
	}
	// só cache até agora: sem ritmo do motor, sem previsão
	m2, c2 := newTestMeter()
	m2.begin(1000)
	m2.addCached(10)
	c2.advance(30 * time.Second)
	if r := m2.Snapshot(); r.RemainingKnown || r.WordsPerSec != 0 {
		t.Fatalf("sem palavras do motor não há como prever: %+v", r)
	}
}

func TestMeterTerminouFicaCongelado(t *testing.T) {
	m, c := newTestMeter()
	m.begin(100)
	m.addTranslated(100)
	c.advance(20 * time.Second)
	m.stop()
	c.advance(time.Hour) // o relógio continua, o medidor não
	r := m.Snapshot()
	if r.Elapsed != 20*time.Second || r.WordsPerSec != 5 || !r.RemainingKnown || r.Remaining != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestMeterBeginZeraAExecucaoAnterior(t *testing.T) {
	m, c := newTestMeter()
	m.begin(10)
	m.addTranslated(10)
	c.advance(time.Second)
	m.stop()
	m.begin(50)
	if r := m.Snapshot(); r.EngineWords != 0 || r.TotalWords != 50 || r.Elapsed != 0 {
		t.Fatalf("a nova execução devia começar do zero: %+v", r)
	}
}

// Ligado ao Execute: o total vem dos jobs, as palavras do cache e do motor ficam
// separadas e o medidor para quando a tradução acaba.
func TestExecuteAlimentaOMedidor(t *testing.T) {
	out, _ := newOut(t)
	cache, err := OpenCache(filepath.Join(t.TempDir(), "c.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	fe := &fakeEngine{name: "e", batch: 2, fn: okAll("[pt] ")}
	js := jobs(6, "e") // "Text N ⟦1⟧" = 2 palavras cada
	m, c := newTestMeter()
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Cache: cache, Out: out, Errs: &errSink{}, Retries: 0, Backoff: time.Millisecond, Meter: m}
	if _, err := Execute(context.Background(), js, o); err != nil {
		t.Fatal(err)
	}
	r := m.Snapshot()
	if r.TotalWords != 12 || r.EngineWords != 12 || r.DoneWords != 12 {
		t.Fatalf("1ª rodada: %+v", r)
	}
	c.advance(time.Minute)
	// 2ª rodada, mesmos textos: tudo vem do cache, o motor não trabalha
	out2, _ := newOut(t)
	o.Out = out2
	if _, err := Execute(context.Background(), js, o); err != nil {
		t.Fatal(err)
	}
	r = m.Snapshot()
	if r.TotalWords != 12 || r.DoneWords != 12 || r.EngineWords != 0 || r.WordsPerSec != 0 {
		t.Fatalf("2ª rodada (cache): %+v", r)
	}
}

func TestExecuteSemMedidorContinuaFuncionando(t *testing.T) {
	out, _ := newOut(t)
	fe := &fakeEngine{name: "e", batch: 1, fn: okAll("[pt] ")}
	o := Options{Engines: map[string]EngineRun{"e": {Engine: fe, Workers: 1}}, Out: out, Errs: &errSink{}, Backoff: time.Millisecond}
	if st, err := Execute(context.Background(), jobs(3, "e"), o); err != nil || st.Translated != 3 {
		t.Fatalf("%+v %v", st, err)
	}
}
