// Package runlog guarda um registro de cada execução em work/logs: tudo o que o
// programa escreve no terminal (menos as linhas de progresso, de que só a última
// fica) e, na tradução, uma linha por pedido ao motor. Serve para achar depois
// um erro que não chamou a atenção na tela.
//
// O registro nunca guarda o conteúdo do arquivo de configuração (só o caminho),
// para não levar chaves de API para o disco.
package runlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Keep é quantas execuções ficam guardadas; as mais antigas são apagadas.
const Keep = 10

// Log é o registro de uma execução.
type Log struct {
	Dir   string // work/logs
	Stamp string // AAAAMMDD-HHMMSS
	Path  string // run-<stamp>.log

	start time.Time
	f     *os.File
	mu    sync.Mutex
	wg    sync.WaitGroup

	origOut, origErr *os.File
	pipes            []*os.File
	quiet            bool // só grava no arquivo: a interface em tela cheia não pode receber texto solto
}

var current *Log

// Start abre o registro, copia a saída do terminal para ele e apaga os mais
// antigos. Quem chama deve chamar Close no fim.
func Start(work string, args []string, version string) (*Log, error) {
	return start(work, args, version, false)
}

// StartQuiet é como Start, mas a saída do programa vai só para o arquivo, não
// para o terminal. A interface em tela cheia usa esta: as etapas escrevem com
// fmt.Println, e isso estragaria o desenho.
func StartQuiet(work string, args []string, version string) (*Log, error) {
	return start(work, args, version, true)
}

func start(work string, args []string, version string, quiet bool) (*Log, error) {
	dir := filepath.Join(work, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	base := now.Format("20060102-150405")
	stamp, path := base, filepath.Join(dir, "run-"+base+".log")
	var f *os.File
	var err error
	for i := 2; ; i++ { // duas execuções no mesmo segundo: -2, -3...
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) || i > 99 {
			return nil, err
		}
		stamp = fmt.Sprintf("%s-%d", base, i)
		path = filepath.Join(dir, "run-"+stamp+".log")
	}
	l := &Log{Dir: dir, Stamp: stamp, Path: path, start: now, f: f, origOut: os.Stdout, origErr: os.Stderr, quiet: quiet}
	host, _ := os.Hostname()
	fmt.Fprintf(f, "# mtrad %s\n# início: %s\n# comando: %s\n# pasta de trabalho: %s\n# máquina: %s\n", version, now.Format(time.RFC3339), strings.Join(args, " "), work, host)
	for _, p := range []**os.File{&os.Stdout, &os.Stderr} {
		r, w, err := os.Pipe()
		if err != nil {
			l.restore()
			f.Close()
			return nil, err
		}
		orig := *p
		*p = w
		l.pipes = append(l.pipes, w)
		l.wg.Add(1)
		go l.copy(r, orig)
	}
	prune(dir, "run-", ".log", Keep)
	prune(dir, "requests-", ".jsonl", Keep)
	current = l
	return l, nil
}

// copy repassa tudo ao terminal e ao arquivo. No arquivo, uma linha que foi
// reescrita com \r (o progresso) conta só pela última versão.
func (l *Log) copy(r *os.File, orig *os.File) {
	defer l.wg.Done()
	defer r.Close()
	buf := make([]byte, 32<<10)
	var line []byte
	flush := func() {
		if len(line) > 0 {
			l.write(string(line))
			line = line[:0]
		}
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if !l.quiet {
				orig.Write(buf[:n])
			}
			for _, b := range buf[:n] {
				switch b {
				case '\r':
					line = line[:0]
				case '\n':
					line = append(line, '\n')
					flush()
				default:
					line = append(line, b)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				l.write(fmt.Sprintf("# erro ao copiar a saída: %v\n", err))
			}
			if len(line) > 0 {
				line = append(line, '\n')
				flush()
			}
			return
		}
	}
}

func (l *Log) write(s string) {
	l.mu.Lock()
	l.f.WriteString(s)
	l.mu.Unlock()
}

func (l *Log) restore() {
	os.Stdout, os.Stderr = l.origOut, l.origErr
}

// Close devolve a saída ao terminal, espera a cópia terminar e escreve o
// resultado da execução no fim do registro.
func (l *Log) Close(runErr error) {
	if l == nil {
		return
	}
	l.restore()
	for _, w := range l.pipes {
		w.Close()
	}
	l.wg.Wait()
	status := "ok"
	if runErr != nil {
		status = "erro: " + runErr.Error()
		// terminar com itens falhos (código de saída 3) não é erro do programa
		var p interface{ Partial() bool }
		if errors.As(runErr, &p) && p.Partial() {
			status = "incompleto (itens que precisam de atenção): " + runErr.Error()
		}
	}
	l.write(fmt.Sprintf("# fim: %s, duração %s, resultado: %s\n", time.Now().Format(time.RFC3339), time.Since(l.start).Round(time.Second), status))
	l.f.Close()
	current = nil
}

// RequestsPath é o arquivo onde a tradução registra um pedido por linha, ou
// vazio se não há registro aberto.
func RequestsPath() string {
	if current == nil {
		return ""
	}
	return filepath.Join(current.Dir, "requests-"+current.Stamp+".jsonl")
}

// prune apaga os arquivos mais antigos de dir com esse prefixo e sufixo,
// deixando os keep mais novos (o nome tem a data, então a ordem do nome é a
// ordem do tempo).
func prune(dir, prefix, suffix string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, suffix) {
			names = append(names, n)
		}
	}
	sortByStamp(names, suffix)
	for len(names) > keep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// Latest devolve o caminho do registro mais recente (n=0) ou do n-ésimo mais
// recente, e o dos pedidos da mesma execução (pode não existir).
func Latest(work string, n int) (runPath, reqPath string, err error) {
	dir := filepath.Join(work, "logs")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("sem registros em %s (rode uma etapa antes): %w", dir, err)
	}
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "run-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sortByStamp(names, ".log")
	if n < 0 || n >= len(names) {
		return "", "", fmt.Errorf("há %d registros em %s; não existe o número %d", len(names), dir, n)
	}
	name := names[len(names)-1-n]
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "run-"), ".log")
	return filepath.Join(dir, name), filepath.Join(dir, "requests-"+stamp+".jsonl"), nil
}

// sortByStamp ordena pelo carimbo de data sem a extensão: "…-2" vem depois de
// "…" (com a extensão, o "." ordenaria o sem sufixo por último).
func sortByStamp(names []string, ext string) {
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.TrimSuffix(names[i], ext), strings.TrimSuffix(names[j], ext)
		if len(a) != len(b) && strings.HasPrefix(b, a) || strings.HasPrefix(a, b) {
			return len(a) < len(b)
		}
		return a < b
	})
}
