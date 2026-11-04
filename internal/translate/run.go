package translate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mtrad/internal/glossary"
	"mtrad/internal/review"
	"mtrad/internal/schema"
)

type Config struct {
	Engines   map[string]EngineConfig `json:"engines"`
	Retries   int                     `json:"retries,omitempty"`
	BackoffMs int                     `json:"backoff_ms,omitempty"`
	CachePath string                  `json:"cache_path,omitempty"` // padrão: <work>/cache/cache.jsonl
	// RetryEngine: motor que refaz os textos reprovados na validação (por
	// exemplo um modelo maior). Vazio = o mesmo motor da rota.
	RetryEngine string `json:"retry_engine,omitempty"`
	// RetryStyle: "xml" refaz os reprovados com as marcas no formato <xn/>.
	RetryStyle string `json:"retry_style,omitempty"`
	// Notice recebe os avisos e o resumo do que será feito (vazio = ignora).
	Notice func(string) `json:"-"`
	Limit  int          `json:"-"` // só traduz os N primeiros (teste)
	// RequestLog: arquivo onde cada pedido ao motor vira uma linha (vazio = não registra)
	RequestLog string `json:"-"`
	// Meter mede palavras por segundo e o tempo que falta (nil = não mede).
	Meter *Meter `json:"-"`
}

// Validate confere as referências entre motores: reserva e motor de nova
// tentativa precisam existir, e as reservas não podem formar um ciclo.
func (c Config) Validate() error {
	for name, ec := range c.Engines {
		if ec.Fallback == "" {
			continue
		}
		if _, ok := c.Engines[ec.Fallback]; !ok {
			return fmt.Errorf("motor %q: fallback %q não existe em translate.engines", name, ec.Fallback)
		}
		seen := map[string]bool{name: true}
		for n := ec.Fallback; n != ""; n = c.Engines[n].Fallback {
			if seen[n] {
				return fmt.Errorf("motor %q: os fallbacks formam um ciclo (passam de novo por %q)", name, n)
			}
			seen[n] = true
		}
	}
	if c.RetryEngine != "" {
		if _, ok := c.Engines[c.RetryEngine]; !ok {
			return fmt.Errorf("retry_engine %q não existe em translate.engines", c.RetryEngine)
		}
	}
	switch c.RetryStyle {
	case "", "xml":
	default:
		return fmt.Errorf("retry_style %q desconhecido (use \"xml\" ou deixe vazio)", c.RetryStyle)
	}
	return nil
}

// Run lê protected.jsonl e routed.jsonl e acrescenta em translated.jsonl.
// Textos que já estão em translated.jsonl são pulados.
func Run(ctx context.Context, workDir string, cfg Config, errs Sink, progress func(done, total int)) (Stats, error) {
	var st Stats
	if err := cfg.Validate(); err != nil {
		return st, err
	}
	notice := func(m string) {
		if cfg.Notice != nil {
			cfg.Notice(m)
		}
	}
	if _, err := schema.RequireFresh(workDir, "protect", "protected.jsonl"); err != nil {
		return st, err
	}
	if _, err := schema.RequireFresh(workDir, "route", "routed.jsonl"); err != nil {
		return st, err
	}
	routed := map[string]string{}
	if err := schema.Each(filepath.Join(workDir, "routed.jsonl"), schema.VRouted, func(r schema.RoutedRec) error {
		routed[r.ID] = r.Engine
		return nil
	}); err != nil {
		return st, err
	}
	// o que já está feito: a última tradução de cada ID e o hash do texto que a
	// gerou (vazio nos arquivos antigos)
	type done struct{ out, msha string }
	have := map[string]done{}
	tpath := filepath.Join(workDir, "translated.jsonl")
	if err := schema.Each(tpath, schema.VTranslated, func(r schema.TranslatedRec) error {
		have[r.ID] = done{r.Out, r.MSha} // a última vale
		return nil
	}); err != nil && !isNotExist(err) {
		return st, err
	}
	// respostas que a validação reprovou: pedem nova tentativa (só se ainda
	// forem a resposta registrada; uma reprovação antiga de um texto já
	// refeito não conta)
	failedOut := map[string]string{}
	if err := schema.Each(filepath.Join(workDir, "failed.jsonl"), schema.VFailed, func(r schema.FailedRec) error {
		failedOut[r.ID] = r.Out
		return nil
	}); err != nil && !isNotExist(err) {
		return st, err
	}
	// textos corrigidos à mão não são refeitos pelo motor
	reviewed, err := review.Load(workDir)
	if err != nil {
		return st, err
	}
	// correção manual que ainda vale (o original não mudou desde que foi feita):
	// a validação usa o texto do jogador e o motor não é chamado para ele
	keepReviewed := map[string]bool{}
	if len(reviewed) > 0 {
		if err := schema.Each(filepath.Join(workDir, "todo.jsonl"), schema.VTodo, func(t schema.TodoRec) error {
			if e, ok := reviewed[t.ID]; ok && strings.TrimSpace(e.Pt) != "" && (e.EnSha == "" || e.EnSha == t.EnSha) {
				keepReviewed[t.ID] = true
			}
			return nil
		}); err != nil && !isNotExist(err) {
			return st, err
		}
	}
	var jobs []Job
	style := ""
	skipped, stale, retried, fresh := 0, 0, 0, 0
	if err := schema.Each(filepath.Join(workDir, "protected.jsonl"), schema.VProtected, func(p schema.ProtectedRec) error {
		style = p.Style
		if keepReviewed[p.ID] {
			skipped++
			return nil
		}
		retry := false
		if h, ok := have[p.ID]; !ok {
			fresh++
		} else {
			switch {
			case h.msha != "" && h.msha != schema.Sha(p.Masked):
				stale++ // o texto de origem mudou desde a tradução
			case failedOut[p.ID] != "" && failedOut[p.ID] == h.out && strings.TrimSpace(reviewed[p.ID].Pt) == "":
				retry = true
				retried++
			default:
				skipped++
				return nil
			}
		}
		eng, ok := routed[p.ID]
		if !ok {
			errs.Add("translate", p.ID, "texto sem motor em routed.jsonl")
			return nil
		}
		j := Job{ID: p.ID, Engine: eng, Masked: p.Masked, Retry: retry}
		if retry {
			if cfg.RetryEngine != "" {
				j.Engine = cfg.RetryEngine
			}
			j.XML = cfg.RetryStyle == "xml" && p.Style == "brackets"
		}
		jobs = append(jobs, j)
		return nil
	}); err != nil {
		return st, err
	}
	if cfg.Limit > 0 && len(jobs) > cfg.Limit {
		jobs = jobs[:cfg.Limit]
	}
	var gl *glossary.Glossary
	if g, err := glossary.Load(filepath.Join(workDir, "glossary.json")); err == nil {
		gl = g
	}
	engines := map[string]EngineRun{}
	var addEngine func(name string) error
	addEngine = func(name string) error {
		if _, ok := engines[name]; ok {
			return nil
		}
		ec, ok := cfg.Engines[name]
		if !ok {
			return nil // Execute registra o erro
		}
		e, err := NewEngine(name, ec, os.Getenv)
		if err != nil {
			return err
		}
		var lim *Limiter
		if ec.RPM > 0 || ec.RPD > 0 {
			lim = NewLimiter(ec.RPM, ec.RPD, filepath.Join(workDir, "ratelimit-"+name+".json"))
		}
		engines[name] = EngineRun{Engine: e, Limiter: lim, Workers: ec.Workers, BatchChars: ec.BatchChars,
			Fallback: ec.Fallback, MaxWait: time.Duration(ec.MaxWaitSec) * time.Second}
		if ec.Fallback != "" {
			return addEngine(ec.Fallback)
		}
		return nil
	}
	for _, j := range jobs {
		if err := addEngine(j.Engine); err != nil {
			return st, err
		}
	}
	cpath, err := resolveCachePath(workDir, cfg.CachePath)
	if err != nil {
		return st, err
	}
	cache, err := OpenCache(cpath)
	if err != nil {
		return st, err
	}
	defer cache.Close()
	if len(jobs) > 0 {
		notice(planNotice(len(have)+fresh, len(have), len(jobs), fresh, stale, retried, cache.Len()))
	}
	out, err := schema.OpenAppend(tpath, schema.VTranslated)
	if err != nil {
		return st, err
	}
	defer out.Close()
	var reqLog func(ReqRec)
	if cfg.RequestLog != "" {
		if rl, err := schema.OpenAppend(cfg.RequestLog, VRequests); err != nil {
			errs.Add("translate", "", "registro de pedidos: "+err.Error())
		} else {
			defer rl.Close()
			reqLog = func(r ReqRec) {
				if err := rl.Write(r); err != nil {
					errs.Add("translate", r.ID, "registro de pedidos: "+err.Error())
				}
			}
		}
	}
	st, err = Execute(ctx, jobs, Options{
		Engines: engines, Cache: cache, Gloss: gl, Style: style, Out: out, Errs: errs,
		Retries: cfg.Retries, Backoff: backoffOf(cfg), Progress: progress, ReqLog: reqLog, Meter: cfg.Meter,
	})
	st.Skipped, st.Stale, st.Retried = skipped, stale, retried
	for _, er := range engines {
		if c, ok := er.Engine.(interface{ Close() }); ok {
			c.Close()
		}
	}
	// uma interrupção (Ctrl+C) não é erro do trabalho: o que foi traduzido está
	// salvo, e o registro da etapa precisa acompanhar o arquivo para a validação
	// e o pacote parcial funcionarem.
	if err != nil && !errors.Is(err, context.Canceled) {
		return st, err
	}
	cancelErr := err
	if werr := schema.WriteDone(workDir, "translate", []string{"protected.jsonl", "routed.jsonl"}, []string{"translated.jsonl"},
		map[string]int{"translated": st.Translated, "cache_hits": st.CacheHits, "failed": st.Failed, "skipped": st.Skipped, "stale": st.Stale, "retried": st.Retried, "cache_write_failed": st.CacheFail}); werr != nil {
		return st, werr
	}
	return st, cancelErr
}

// planNotice descreve o que a execução vai fazer e avisa de duas situações que
// custam horas de GPU sem o usuário perceber: o texto de origem de muitos
// textos ter mudado (outra versão do protect ou do glossário) e haver
// traduções em work mas nenhum cache (pasta restaurada pela metade).
func planNotice(total, have, jobs, fresh, stale, retried, cacheLen int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "translate: %d textos; %d já traduzidos; %d a pedir ao motor (%d novos, %d com o texto de origem mudado, %d reprovados na validação)", total, have-stale-retried, jobs, fresh, stale, retried)
	if stale >= 50 && stale*10 >= total {
		fmt.Fprintf(&b, "\n  aviso: %d textos mudaram de origem desde a tradução anterior. A etapa protect ou o glossário mudou (outra versão do mtrad, outro estilo de marca)? O cache só vale com o mesmo texto, motor, modelo e glossário", stale)
	}
	if have > 0 && cacheLen == 0 && jobs > 0 {
		fmt.Fprintf(&b, "\n  aviso: há %d traduções em work, mas o cache está vazio. Se você restaurou só parte da pasta, o work/cache/cache.jsonl se perdeu: o que for refeito será pedido de novo ao motor", have)
	}
	return b.String()
}

func backoffOf(c Config) time.Duration {
	if c.BackoffMs > 0 {
		return time.Duration(c.BackoffMs) * time.Millisecond
	}
	return 2 * time.Second
}

// resolveCachePath devolve o arquivo do cache. O padrão fica dentro da pasta de
// trabalho, para o cache andar junto com o resto (antes ficava numa pasta irmã
// e se perdia quando só o work era copiado). Se só existir o cache do lugar
// antigo, ele é copiado para o novo, sem apagar o antigo.
func resolveCachePath(workDir, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	path := filepath.Join(workDir, "cache", "cache.jsonl")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	old := filepath.Join(filepath.Dir(workDir), "cache", "cache.jsonl")
	b, err := os.ReadFile(old)
	if err != nil {
		return path, nil // não havia cache antigo
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
