package translate

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

// CountWords conta as palavras de um texto protegido: pedaços separados por
// espaço que têm ao menos uma letra ou um número. As marcas ⟦n⟧ e <xn/> não
// contam (são do mtrad, não do texto).
func CountWords(masked string) int {
	s := reMarkBrackets.ReplaceAllString(masked, " ")
	s = reMarkXML.ReplaceAllString(s, " ")
	n := 0
	for _, f := range strings.Fields(s) {
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			n++
		}
	}
	return n
}

// Meter mede o andamento da tradução em palavras. Pode ser nil (não mede) e é
// seguro para uso de várias goroutines.
type Meter struct {
	mu      sync.Mutex
	now     func() time.Time
	start   time.Time
	end     time.Time
	total   int64 // palavras a traduzir nesta execução
	cached  int64 // palavras que vieram do cache
	engine  int64 // palavras traduzidas pelo motor (só estas medem a velocidade)
	started bool
}

// NewMeter cria um medidor; o relógio só começa a contar quando a tradução começa.
func NewMeter() *Meter { return &Meter{now: time.Now} }

func (m *Meter) begin(total int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.now == nil {
		m.now = time.Now
	}
	m.start, m.end, m.total, m.cached, m.engine, m.started = m.now(), time.Time{}, total, 0, 0, true
}

func (m *Meter) stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started && m.end.IsZero() {
		m.end = m.now()
	}
}

func (m *Meter) addCached(w int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.cached += int64(w)
	m.mu.Unlock()
}

func (m *Meter) addTranslated(w int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.engine += int64(w)
	m.mu.Unlock()
}

// Rate é uma fotografia do andamento.
type Rate struct {
	Elapsed time.Duration
	// TotalWords são as palavras desta execução; DoneWords as já resolvidas
	// (cache + motor); EngineWords só as que o motor traduziu.
	TotalWords, DoneWords, EngineWords int64
	// WordsPerSec é a velocidade do motor: palavras traduzidas por ele dividido
	// pelo tempo decorrido. O cache não entra, para não inflar o número.
	WordsPerSec float64
	// Remaining é o tempo que falta no ritmo atual; só vale se RemainingKnown.
	Remaining      time.Duration
	RemainingKnown bool
}

// minEstimate: antes disto o ritmo ainda oscila demais para prever o fim.
const minEstimate = 3 * time.Second

// Snapshot lê o andamento agora. Num medidor nil ou que ainda não começou
// devolve tudo zero.
func (m *Meter) Snapshot() Rate {
	if m == nil {
		return Rate{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return Rate{}
	}
	end := m.end
	if end.IsZero() {
		end = m.now()
	}
	r := Rate{Elapsed: end.Sub(m.start), TotalWords: m.total, DoneWords: m.cached + m.engine, EngineWords: m.engine}
	if r.Elapsed <= 0 {
		return r
	}
	r.WordsPerSec = float64(m.engine) / r.Elapsed.Seconds()
	left := m.total - r.DoneWords
	switch {
	case left <= 0:
		r.RemainingKnown = true
	case r.WordsPerSec > 0 && r.Elapsed >= minEstimate:
		r.Remaining, r.RemainingKnown = time.Duration(float64(left)/r.WordsPerSec*float64(time.Second)), true
	}
	return r
}
