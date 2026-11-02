package translate

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"mtrad/internal/glossary"
	"mtrad/internal/schema"
)

type Job struct {
	ID     string
	Engine string
	Masked string
	// Retry: a resposta anterior foi reprovada na validação; pede de novo ao
	// motor sem olhar o cache (que guardaria a mesma resposta ruim).
	Retry bool
	// XML: o pedido troca as marcas ⟦n⟧ por <xn/> (modelos pequenos costumam
	// estragar menos esta forma) e a resposta volta para ⟦n⟧.
	XML bool
}

// VRequests é a versão do arquivo requests-*.jsonl (um pedido por linha).
const VRequests = "requests/1"

// ReqRec é uma linha do registro de pedidos. O texto não entra: só o ID.
type ReqRec struct {
	Time    string `json:"time"`
	ID      string `json:"id"`
	Engine  string `json:"engine"`
	Model   string `json:"model"`
	Attempt int    `json:"attempt,omitempty"`
	Ms      int64  `json:"ms,omitempty"`
	Cache   bool   `json:"cache,omitempty"`
	Retry   bool   `json:"retry,omitempty"` // refeito depois de reprovado na validação
	Err     string `json:"err,omitempty"`
}

type Sink interface {
	Add(src, id, msg string)
}

type EngineRun struct {
	Engine  Engine
	Limiter *Limiter // pode ser nil
	Workers int
	// BatchChars limita os caracteres de um lote (0 = só pela quantidade de
	// textos do motor). Um texto maior que o limite vai sozinho.
	BatchChars int
	// Fallback: motor que recebe os textos que este não conseguiu traduzir.
	Fallback string
	// MaxWait: maior espera aceita quando o servidor manda Retry-After; se ele
	// pedir mais, o motor para (zero = 2 minutos).
	MaxWait time.Duration
}

type Options struct {
	Engines  map[string]EngineRun
	Cache    *Cache // pode ser nil
	Gloss    *glossary.Glossary
	Style    string
	Out      *schema.Appender
	Errs     Sink
	Retries  int
	Backoff  time.Duration
	Progress func(done, total int)
	ReqLog   func(ReqRec) // pode ser nil

	cacheFail *int64 // gravações do cache que falharam (preenchido em Execute)
}

type Stats struct {
	Translated int
	CacheHits  int
	Failed     int
	CacheFail  int               // traduções feitas, mas não gravadas no cache
	Skipped    int               // já traduzidos e com o mesmo texto de origem
	Stale      int               // texto de origem mudou: traduzidos de novo
	Retried    int               // reprovados na validação: pedidos de novo
	Fallback   map[string]int    // motor -> textos que passaram para o motor reserva
	Stopped    map[string]string // motor -> motivo
}

const (
	defaultMaxWait   = 2 * time.Minute
	maxFallbackDepth = 4
)

// Execute traduz os jobs. Itens que falham ficam de fora do arquivo de saída
// (uma nova execução tenta de novo). Os que falham num motor com Fallback
// vão para o motor reserva.
func Execute(ctx context.Context, jobs []Job, o Options) (Stats, error) {
	st := Stats{Stopped: map[string]string{}, Fallback: map[string]int{}}
	o.cacheFail = new(int64)
	var done int64
	total := len(jobs)

	failed, left := o.pass(ctx, jobs, &st, &done, total, true)
	for depth := 0; depth < maxFallbackDepth && ctx.Err() == nil; depth++ {
		var next []Job
		moved := 0
		for i, list := range [][]Job{failed, left} {
			for _, j := range list {
				fb := o.Engines[j.Engine].Fallback
				if _, ok := o.Engines[fb]; fb == "" || !ok || fb == j.Engine {
					continue
				}
				st.Fallback[j.Engine]++
				j.Engine = fb
				next = append(next, j)
				if i == 0 {
					moved++ // já tinha sido contado como falha
				}
			}
		}
		if len(next) == 0 {
			break
		}
		st.Failed -= moved
		failed, left = o.pass(ctx, next, &st, &done, total, false)
	}
	st.CacheFail = int(atomic.LoadInt64(o.cacheFail))
	return st, ctx.Err()
}

// pass processa os jobs, cada um no motor do seu campo Engine. Devolve os que
// falharam (já somados em st.Failed) e os que nem chegaram a ser enviados
// porque o motor parou.
func (o Options) pass(ctx context.Context, jobs []Job, st *Stats, done *int64, total int, report bool) (failed, left []Job) {
	var mu, pmu sync.Mutex // pmu: só os avisos de progresso
	byEngine := map[string][]Job{}
	for _, j := range jobs {
		if _, ok := o.Engines[j.Engine]; !ok {
			o.Errs.Add("translate", j.ID, "motor desconhecido: "+j.Engine)
			st.Failed++
			continue
		}
		byEngine[j.Engine] = append(byEngine[j.Engine], j)
	}
	var wg sync.WaitGroup
	for name, list := range byEngine {
		name, er := name, o.Engines[name]
		bs := er.Engine.BatchSize()
		if bs < 1 {
			bs = 1
		}
		workers := er.Workers
		if workers < 1 {
			workers = 1
		}
		batches := makeBatches(list, bs, er.BatchChars)
		ectx, cancel := context.WithCancel(ctx)
		ch := make(chan []Job)
		var unsent []Job // só o produtor escreve; lido depois de ch fechar
		go func() {
			defer close(ch)
			for i, b := range batches {
				select {
				case ch <- b:
				case <-ectx.Done():
					for _, rest := range batches[i:] {
						unsent = append(unsent, rest...)
					}
					return
				}
			}
		}()
		var ewg sync.WaitGroup
		for w := 0; w < workers; w++ {
			ewg.Add(1)
			go func() {
				defer ewg.Done()
				for batch := range ch {
					if ectx.Err() != nil { // o motor parou enquanto este lote já estava a caminho
						mu.Lock()
						left = append(left, batch...)
						mu.Unlock()
						continue
					}
					r := o.runBatch(ectx, er, batch)
					mu.Lock()
					st.Translated += r.translated
					st.CacheHits += r.hits
					st.Failed += len(r.failed)
					failed = append(failed, r.failed...)
					if r.stop != "" {
						if _, has := st.Stopped[name]; !has {
							st.Stopped[name] = r.stop
						}
					}
					mu.Unlock()
					if r.stop != "" {
						cancel()
					}
					if report && o.Progress != nil {
						// um de cada vez e em ordem crescente: quem recebe o aviso
						// não precisa de trava própria
						pmu.Lock()
						n := atomic.AddInt64(done, int64(len(batch)))
						o.Progress(int(n), total)
						pmu.Unlock()
					} else {
						atomic.AddInt64(done, int64(len(batch)))
					}
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ewg.Wait()
			cancel()
			mu.Lock()
			left = append(left, unsent...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return failed, left
}

// makeBatches agrupa os jobs em lotes de no máximo n textos e, se maxChars > 0,
// de no máximo maxChars caracteres (contando o texto protegido).
func makeBatches(list []Job, n, maxChars int) [][]Job {
	var out [][]Job
	var cur []Job
	chars := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
			cur, chars = nil, 0
		}
	}
	for _, j := range list {
		c := utf8.RuneCountInString(j.Masked)
		if len(cur) > 0 && (len(cur) >= n || (maxChars > 0 && chars+c > maxChars)) {
			flush()
		}
		cur = append(cur, j)
		chars += c
	}
	flush()
	return out
}

func (o Options) logReq(r ReqRec) {
	if o.ReqLog != nil {
		r.Time = time.Now().Format(time.RFC3339)
		o.ReqLog(r)
	}
}

type batchRes struct {
	translated, hits int
	failed           []Job
	stop             string
}

var (
	reMarkBrackets = regexp.MustCompile(`⟦(\d+)⟧`)
	reMarkXML      = regexp.MustCompile(`<\s*x\s*(\d+)\s*/?\s*>`)
)

// toXMLMarks troca ⟦n⟧ por <xn/>; fromXMLMarks faz o caminho de volta, aceitando
// pequenas variações (<x1>, < x1 />).
func toXMLMarks(s string) string   { return reMarkBrackets.ReplaceAllString(s, "<x$1/>") }
func fromXMLMarks(s string) string { return reMarkXML.ReplaceAllString(s, "⟦$1⟧") }

func (o Options) runBatch(ctx context.Context, er EngineRun, batch []Job) batchRes {
	var res batchRes
	eng := er.Engine
	tag := promptTagOf(eng)
	var pending []Item
	jobOf := map[string]Job{}
	keys := map[string]string{}
	msha := map[string]string{}
	for _, j := range batch {
		k := CacheKey(eng.Name(), eng.Model(), tag, glossVersion(o.Gloss), o.Style, j.Masked)
		keys[j.ID] = k
		msha[j.ID] = schema.Sha(j.Masked)
		jobOf[j.ID] = j
		if o.Cache != nil && !j.Retry {
			if out, ok := o.Cache.Get(k); ok {
				if err := o.Out.Write(schema.TranslatedRec{ID: j.ID, Engine: eng.Name(), Model: eng.Model() + " (cache)", Out: out, MSha: msha[j.ID]}); err != nil {
					o.Errs.Add("translate", j.ID, "gravar: "+err.Error())
					res.failed = append(res.failed, j)
					continue
				}
				o.logReq(ReqRec{ID: j.ID, Engine: eng.Name(), Model: eng.Model(), Cache: true})
				res.hits++
				continue
			}
		}
		text := j.Masked
		if j.XML {
			text = toXMLMarks(text)
		}
		it := Item{ID: j.ID, Text: text, Retry: j.Retry, XML: j.XML}
		if o.Gloss != nil {
			it.Terms = o.Gloss.Find(j.Masked)
		}
		pending = append(pending, it)
	}
	tr, failedItems, stop := o.runPending(ctx, er, pending, o.Retries+1, keys, msha)
	res.translated = tr
	for _, it := range failedItems {
		res.failed = append(res.failed, jobOf[it.ID])
	}
	res.stop = stop
	return res
}

// runPending pede a tradução dos itens ao motor, com até attempts tentativas.
// Se o pedido inteiro continua falhando e tem mais de um texto, divide o lote
// ao meio para isolar o texto que o servidor não aceita.
func (o Options) runPending(ctx context.Context, er EngineRun, pending []Item, attempts int, keys, msha map[string]string) (translated int, failed []Item, stop string) {
	eng := er.Engine
	maxWait := er.MaxWait
	if maxWait <= 0 {
		maxWait = defaultMaxWait
	}
	for attempt := 1; len(pending) > 0 && attempt <= attempts; attempt++ {
		if er.Limiter != nil {
			if err := er.Limiter.Wait(ctx); err != nil {
				if errors.Is(err, ErrDailyLimit) {
					return translated, append(failed, pending...), "limite diário atingido"
				}
				return translated, append(failed, pending...), ""
			}
		}
		start := time.Now()
		res, err := eng.Translate(ctx, pending)
		ms := time.Since(start).Milliseconds() / int64(len(pending))
		if err == nil && len(res) != len(pending) {
			err = errCount
		}
		if err != nil {
			if ctx.Err() != nil {
				return translated, append(failed, pending...), ""
			}
			for _, it := range pending {
				o.logReq(ReqRec{ID: it.ID, Engine: eng.Name(), Model: eng.Model(), Attempt: attempt, Ms: ms, Retry: it.Retry, Err: err.Error()})
			}
			wait := o.Backoff << (attempt - 1)
			if ra := retryAfterOf(err); ra > 0 {
				if ra > maxWait {
					for _, it := range pending {
						o.Errs.Add("translate", it.ID, eng.Name()+": "+err.Error())
					}
					return translated, append(failed, pending...), "o servidor pediu para esperar " + ra.Round(time.Second).String() + " (mais que o limite de " + maxWait.String() + ")"
				}
				wait = ra
			}
			if attempt == attempts {
				if len(pending) > 1 && splittable(err) {
					h := len(pending) / 2
					t1, f1, s1 := o.runPending(ctx, er, pending[:h], 1, keys, msha)
					t2, f2, s2 := o.runPending(ctx, er, pending[h:], 1, keys, msha)
					translated += t1 + t2
					failed = append(append(failed, f1...), f2...)
					if s1 == "" {
						s1 = s2
					}
					return translated, failed, s1
				}
				for _, it := range pending {
					o.Errs.Add("translate", it.ID, eng.Name()+": "+err.Error())
				}
				return translated, append(failed, pending...), ""
			}
			sleep(ctx, wait)
			continue
		}
		var retry []Item
		for i, r := range res {
			it := pending[i]
			rq := ReqRec{ID: it.ID, Engine: eng.Name(), Model: eng.Model(), Attempt: attempt, Ms: ms, Retry: it.Retry}
			if r.Err != nil {
				rq.Err = r.Err.Error()
			} else if r.Out == "" {
				rq.Err = "saída vazia"
			}
			o.logReq(rq)
			if r.Err != nil || r.Out == "" {
				if attempt == attempts {
					msg := "saída vazia"
					if r.Err != nil {
						msg = r.Err.Error()
					}
					o.Errs.Add("translate", it.ID, eng.Name()+": "+msg)
					failed = append(failed, it)
				} else {
					retry = append(retry, it)
				}
				continue
			}
			out := r.Out
			if it.XML {
				out = fromXMLMarks(out)
			}
			if err := o.Out.Write(schema.TranslatedRec{ID: r.ID, Engine: eng.Name(), Model: eng.Model(), Out: out, Attempt: attempt, Ms: ms, MSha: msha[r.ID]}); err != nil {
				o.Errs.Add("translate", r.ID, "gravar: "+err.Error())
				failed = append(failed, it)
				continue
			}
			if o.Cache != nil {
				if err := o.Cache.Put(keys[r.ID], out); err != nil {
					atomic.AddInt64(o.cacheFail, 1)
					o.Errs.Add("translate", r.ID, "cache: não gravou a tradução ("+err.Error()+")")
				}
			}
			translated++
		}
		pending = retry
		if len(pending) > 0 {
			sleep(ctx, o.Backoff<<(attempt-1))
		}
	}
	return translated, failed, ""
}

func glossVersion(g *glossary.Glossary) string {
	if g == nil {
		return ""
	}
	return g.Version
}

func sleep(ctx context.Context, d time.Duration) { _ = sleepCtx(ctx, d) }

// promptTagOf devolve a marca de versão do prompt do motor (vazia se o motor
// não usa prompt ou usa o prompt original).
func promptTagOf(e Engine) string {
	if p, ok := e.(interface{ PromptTag() string }); ok {
		return p.PromptTag()
	}
	return ""
}
