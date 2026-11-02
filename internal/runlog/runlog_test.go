package runlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartCopiesOutputAndKeepsLastProgress(t *testing.T) {
	work := t.TempDir()
	l, err := Start(work, []string{"mtrad", "run", "-c", "mtrad.json"}, "teste")
	if err != nil {
		t.Fatal(err)
	}
	if RequestsPath() == "" || !strings.HasSuffix(RequestsPath(), ".jsonl") {
		t.Fatalf("RequestsPath: %q", RequestsPath())
	}
	fmt.Println("scan: 3 jars")
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(os.Stderr, "\r  traduzidos %d de 3", i)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Println("validate: ok")
	l.Close(nil)
	if RequestsPath() != "" {
		t.Fatal("depois do Close não há registro aberto")
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"# mtrad teste", "# comando: mtrad run -c mtrad.json", "scan: 3 jars", "traduzidos 3 de 3", "validate: ok", "# fim:", "resultado: ok"} {
		if !strings.Contains(s, want) {
			t.Errorf("falta %q em:\n%s", want, s)
		}
	}
	if strings.Contains(s, "traduzidos 1 de 3") || strings.Contains(s, "traduzidos 2 de 3") {
		t.Errorf("só o último progresso deveria ficar:\n%s", s)
	}
}

func TestCloseRecordsError(t *testing.T) {
	work := t.TempDir()
	l, err := Start(work, []string{"mtrad", "scan"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	l.Close(fmt.Errorf("deu ruim"))
	b, _ := os.ReadFile(l.Path)
	if !strings.Contains(string(b), "resultado: erro: deu ruim") {
		t.Fatalf("%s", b)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 14; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("run-20260101-0000%02d.log", i)), nil, 0o644)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("requests-20260101-0000%02d.jsonl", i)), nil, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "outro.txt"), nil, 0o644)
	prune(dir, "run-", ".log", Keep)
	prune(dir, "requests-", ".jsonl", Keep)
	ents, _ := os.ReadDir(dir)
	runs, reqs, other := 0, 0, 0
	for _, e := range ents {
		switch {
		case strings.HasPrefix(e.Name(), "run-"):
			runs++
		case strings.HasPrefix(e.Name(), "requests-"):
			reqs++
		default:
			other++
		}
	}
	if runs != Keep || reqs != Keep || other != 1 {
		t.Fatalf("runs=%d reqs=%d other=%d", runs, reqs, other)
	}
	if _, err := os.Stat(filepath.Join(dir, "run-20260101-000013.log")); err != nil {
		t.Fatal("o mais novo deveria ficar")
	}
	if _, err := os.Stat(filepath.Join(dir, "run-20260101-000000.log")); err == nil {
		t.Fatal("o mais antigo deveria sumir")
	}
}

func TestLatest(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "logs")
	os.MkdirAll(dir, 0o755)
	for _, n := range []string{"20260101-000001", "20260101-000002"} {
		os.WriteFile(filepath.Join(dir, "run-"+n+".log"), nil, 0o644)
	}
	run, req, err := Latest(work, 0)
	if err != nil || !strings.HasSuffix(run, "run-20260101-000002.log") || !strings.HasSuffix(req, "requests-20260101-000002.jsonl") {
		t.Fatalf("%q %q %v", run, req, err)
	}
	run, _, _ = Latest(work, 1)
	if !strings.HasSuffix(run, "000001.log") {
		t.Fatal(run)
	}
	if _, _, err := Latest(work, 2); err == nil {
		t.Fatal("deveria dizer que não existe o terceiro")
	}
	_ = time.Now
}

func TestTwoRunsInTheSameSecondGetDifferentFiles(t *testing.T) {
	work := t.TempDir()
	a, err := Start(work, []string{"mtrad", "scan"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	a.Close(nil)
	b, err := Start(work, []string{"mtrad", "scan"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	b.Close(nil)
	if a.Path == b.Path {
		t.Fatalf("mesmo arquivo: %s", a.Path)
	}
	if _, _, err := Latest(work, 1); err != nil {
		t.Fatal(err)
	}
}

func TestLatestOrdersSuffixAfterPlainName(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "logs")
	os.MkdirAll(dir, 0o755)
	for _, n := range []string{"run-20260101-000001.log", "run-20260101-000001-2.log", "run-20260101-000001-3.log"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	run, _, err := Latest(work, 0)
	if err != nil || !strings.HasSuffix(run, "000001-3.log") {
		t.Fatalf("%q %v", run, err)
	}
	run, _, _ = Latest(work, 2)
	if !strings.HasSuffix(run, "000001.log") {
		t.Fatalf("%q", run)
	}
}

func TestStartQuietDoesNotReachTheTerminal(t *testing.T) {
	dir := t.TempDir()
	origOut := os.Stdout
	term, err := os.CreateTemp(dir, "term")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = term // o "terminal" deste teste
	l, err := StartQuiet(dir, []string{"mtrad", "ui"}, "teste")
	if err != nil {
		os.Stdout = origOut
		t.Fatal(err)
	}
	fmt.Println("linha só para o registro")
	l.Close(nil)
	os.Stdout = origOut
	term.Close()
	if b, _ := os.ReadFile(term.Name()); len(b) != 0 {
		t.Fatalf("o terminal recebeu texto: %q", b)
	}
	b, _ := os.ReadFile(l.Path)
	if !strings.Contains(string(b), "linha só para o registro") {
		t.Fatalf("o registro não tem a linha: %s", b)
	}
}

type partialErr struct{}

func (partialErr) Error() string { return "validate: há textos reprovados" }
func (partialErr) Partial() bool { return true }

func TestClosePartialIsNotCalledError(t *testing.T) {
	dir := t.TempDir()
	l, err := start(dir, []string{"mtrad", "run"}, "t", true)
	if err != nil {
		t.Fatal(err)
	}
	l.Close(partialErr{})
	raw, _ := os.ReadFile(l.Path)
	got := string(raw)
	if !strings.Contains(got, "resultado: incompleto") || strings.Contains(got, "resultado: erro") {
		t.Fatalf("registro:\n%s", got)
	}
}
