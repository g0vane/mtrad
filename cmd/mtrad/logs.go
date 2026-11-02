package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"mtrad/internal/runlog"
	"mtrad/internal/schema"
	"mtrad/internal/translate"
)

// toolVersion pode ser trocada na compilação:
// go build -ldflags "-X main.toolVersion=0.9.1".
var toolVersion = "0.9 (28/10/2026)"

// registrado diz se a etapa grava registro em work/logs.
func registrado(cmd string) bool {
	switch cmd {
	case "version", "scan", "extract", "patchouli", "ftbq", "diff", "glossary", "protect", "route", "translate", "validate", "pack", "install", "run":
		return true
	}
	return false
}

// workDir acha o valor de -w nos argumentos (o padrão é "work").
func workDir(args []string) string {
	for i, a := range args {
		a = strings.TrimLeft(a, "-")
		if a == "w" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "w=") {
			return strings.TrimPrefix(a, "w=")
		}
	}
	return "work"
}

// stageLogs resume o registro de uma execução: etapas, avisos, pedidos ao motor.
func stageLogs(work string, n int) error { return stageLogsTo(os.Stdout, work, n) }

// stageLogsTo escreve o resumo em w (a interface mostra o mesmo texto).
func stageLogsTo(w io.Writer, work string, n int) error {
	runPath, reqPath, err := runlog.Latest(work, n)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "registro: %s\n", runPath)
	f, err := os.Open(runPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var header, stages []string
	var errLines, warnLines int
	end := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "# fim:"):
			end = l
		case strings.HasPrefix(l, "#"):
			header = append(header, l)
		default:
			if i := strings.Index(l, ":"); i > 0 && i < 12 && !strings.Contains(l[:i], " ") {
				stages = append(stages, l)
			}
			if strings.HasPrefix(l, "erro") {
				errLines++
			}
			if strings.Contains(l, "aviso:") {
				warnLines++
			}
		}
	}
	for _, h := range header {
		fmt.Fprintln(w, h)
	}
	if end == "" {
		fmt.Fprintln(w, "# fim: não registrado (o programa foi interrompido sem fechar o registro)")
	} else {
		fmt.Fprintln(w, end)
	}
	fmt.Fprintf(w, "linhas de etapa: %d; linhas de erro: %d; linhas de aviso: %d\n", len(stages), errLines, warnLines)
	for _, s := range stages {
		if len(s) > 160 {
			s = s[:160] + "…"
		}
		fmt.Fprintln(w, "  "+s)
	}

	// errors.jsonl da pasta de trabalho (é o da última etapa que rodou)
	byKey := map[string]int{}
	total := 0
	_ = schema.Each(filepath.Join(work, "errors.jsonl"), schema.VErrors, func(r schema.ErrRec) error {
		byKey[r.Stage+": "+firstWords(r.Msg, 90)]++
		total++
		return nil
	})
	if total > 0 {
		fmt.Fprintf(w, "\nerrors.jsonl: %d registros\n", total)
		printTop(w, byKey, 8)
	}

	// pedidos ao motor
	var recs []translate.ReqRec
	if err := schema.Each(reqPath, translate.VRequests, func(r translate.ReqRec) error {
		recs = append(recs, r)
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "\nesta execução não fez pedidos ao motor")
			return nil
		}
		return err
	}
	var hit, retry, fail, again int
	var ms []int64
	errMsg := map[string]int{}
	for _, r := range recs {
		switch {
		case r.Cache:
			hit++
			continue
		case r.Err != "":
			fail++
			errMsg[firstWords(r.Err, 90)]++
		}
		if r.Retry {
			retry++
		}
		if r.Attempt > 1 {
			again++
		}
		if r.Ms > 0 {
			ms = append(ms, r.Ms)
		}
	}
	fmt.Fprintf(w, "\npedidos: %d linhas, %d vieram do cache, %d ao motor (%d com erro, %d refeitos por reprovação, %d na 2ª tentativa ou mais)\n", len(recs), hit, len(recs)-hit, fail, retry, again)
	if len(ms) > 0 {
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		var sum int64
		for _, v := range ms {
			sum += v
		}
		fmt.Fprintf(w, "tempo por texto: média %d ms, mediana %d ms, 95%% abaixo de %d ms, mais lento %d ms\n", sum/int64(len(ms)), ms[len(ms)/2], ms[len(ms)*95/100], ms[len(ms)-1])
		type slow struct {
			id string
			ms int64
		}
		var s []slow
		for _, r := range recs {
			if !r.Cache && r.Ms > 0 {
				s = append(s, slow{r.ID, r.Ms})
			}
		}
		sort.Slice(s, func(i, j int) bool { return s[i].ms > s[j].ms })
		for i := 0; i < len(s) && i < 5; i++ {
			fmt.Fprintf(w, "  lento: %6d ms  %s\n", s[i].ms, s[i].id)
		}
	}
	if len(errMsg) > 0 {
		fmt.Fprintln(w, "erros do motor:")
		printTop(w, errMsg, 5)
	}
	return nil
}

func firstWords(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

func printTop(w io.Writer, m map[string]int, k int) {
	type kv struct {
		k string
		v int
	}
	var l []kv
	for a, b := range m {
		l = append(l, kv{a, b})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].v != l[j].v {
			return l[i].v > l[j].v
		}
		return l[i].k < l[j].k
	})
	for i := 0; i < len(l) && i < k; i++ {
		fmt.Fprintf(w, "  %5d  %s\n", l[i].v, l[i].k)
	}
}

// versionLine descreve o binário: versão, commit (se compilado dentro de um
// repositório git), compilador e sistema.
func versionLine() string {
	s := "mtrad " + toolVersion
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range bi.Settings {
			if kv.Key == "vcs.revision" && len(kv.Value) >= 7 {
				s += " commit " + kv.Value[:7]
			}
		}
		s += " " + bi.GoVersion
	}
	return s + " " + runtime.GOOS + "/" + runtime.GOARCH
}
