package translate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// workerEngine conversa com um programa externo (por exemplo, o ct2worker em
// Python) por linhas JSON em stdin/stdout.
//
// Protocolo: cada lado manda primeiro {"hello":1}. Depois o Go manda uma linha
// por pedido {"id","text","src","tgt"} e lê uma resposta por pedido
// {"id","out"} ou {"id","error"}, na mesma ordem.
type workerEngine struct {
	name, model, src, tgt string
	cmdline               []string
	batch                 int
	timeout               time.Duration

	mu   sync.Mutex
	cmd  *exec.Cmd
	in   *json.Encoder
	out  *bufio.Reader
	stdi interface{ Close() error }
}

func newWorker(name string, c EngineConfig) (Engine, error) {
	if len(c.Command) == 0 {
		return nil, fmt.Errorf("motor %q: command é obrigatório", name)
	}
	e := &workerEngine{name: name, model: c.Model, src: c.Source, tgt: c.Target, cmdline: c.Command, batch: c.Batch, timeout: c.timeout()}
	if e.src == "" {
		e.src = "en"
	}
	if e.tgt == "" {
		e.tgt = "pt_br"
	}
	if e.batch <= 0 {
		e.batch = 16
	}
	if e.model == "" {
		e.model = "worker"
	}
	return e, nil
}

func (e *workerEngine) Name() string   { return e.name }
func (e *workerEngine) Model() string  { return e.model }
func (e *workerEngine) BatchSize() int { return e.batch }

func (e *workerEngine) start(ctx context.Context) error {
	cmd := exec.Command(e.cmdline[0], e.cmdline[1:]...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	e.cmd, e.stdi = cmd, stdin
	e.in = json.NewEncoder(stdin)
	e.out = bufio.NewReaderSize(stdout, 1<<20)
	if err := e.in.Encode(map[string]int{"hello": 1}); err != nil {
		e.stop()
		return err
	}
	line, err := e.readLine(ctx, 10*time.Minute) // o modelo pode demorar para carregar
	if err != nil {
		e.stop()
		return fmt.Errorf("sem hello do worker: %w", err)
	}
	var h struct {
		Hello int `json:"hello"`
	}
	if json.Unmarshal(line, &h) != nil || h.Hello != 1 {
		e.stop()
		return fmt.Errorf("hello inválido do worker: %s", snippet(line))
	}
	return nil
}

func (e *workerEngine) stop() {
	if e.cmd == nil {
		return
	}
	if e.stdi != nil {
		e.stdi.Close()
	}
	_ = e.cmd.Process.Kill()
	_ = e.cmd.Wait()
	e.cmd = nil
}

// readLine espera uma linha do worker por no máximo d e volta na hora se ctx
// for cancelado (Ctrl+C não espera o tempo limite do motor).
func (e *workerEngine) readLine(ctx context.Context, d time.Duration) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() { b, err := e.out.ReadBytes('\n'); ch <- res{b, err} }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-t.C:
		return nil, fmt.Errorf("tempo esgotado (%s)", d)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *workerEngine) Translate(ctx context.Context, items []Item) ([]Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for attempt := 0; ; attempt++ {
		res, err := e.try(ctx, items)
		if err == nil {
			return res, nil
		}
		e.stop() // reinicia e reenvia o lote uma vez
		if attempt >= 1 || ctx.Err() != nil {
			return nil, err
		}
	}
}

func (e *workerEngine) try(ctx context.Context, items []Item) ([]Result, error) {
	if e.cmd == nil {
		if err := e.start(ctx); err != nil {
			return nil, err
		}
	}
	// Se ctx for cancelado, matar o processo destrava também uma escrita presa
	// (worker que não lê a entrada); a leitura já olha para ctx.
	cmd, stdi := e.cmd, e.stdi
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stdi.Close()
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	for _, it := range items {
		if err := e.in.Encode(map[string]string{"id": it.ID, "text": it.Text, "src": e.src, "tgt": e.tgt}); err != nil {
			return nil, err
		}
	}
	out := make([]Result, len(items))
	for i, it := range items {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line, err := e.readLine(ctx, e.timeout)
		if err != nil {
			return nil, err
		}
		var r struct {
			ID    string `json:"id"`
			Out   string `json:"out"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("resposta inválida do worker: %s", snippet(line))
		}
		if r.ID != it.ID {
			return nil, fmt.Errorf("resposta fora de ordem: esperava %s, veio %s", it.ID, r.ID)
		}
		if r.Error != "" {
			out[i] = Result{ID: it.ID, Err: fmt.Errorf("%s", r.Error)}
			continue
		}
		out[i] = Result{ID: it.ID, Out: matchEdges(it.Text, r.Out)}
	}
	return out, nil
}

// Close encerra o processo do worker.
func (e *workerEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stop()
}
