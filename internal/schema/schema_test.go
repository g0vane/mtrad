package schema

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestID(t *testing.T) {
	a := ID("jade", "jade", "k")
	if len(a) != 16 {
		t.Fatalf("id com %d caracteres", len(a))
	}
	if a != ID("jade", "jade", "k") || a == ID("jade", "jade", "k2") || a == ID("jad", "ejade", "k") {
		t.Fatal("id deve ser estável e separar os campos")
	}
}

func TestJSONLRoundTripAndVersion(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	w, err := Create(p, VStrings)
	if err != nil {
		t.Fatal(err)
	}
	pt := "Olá <b>"
	_ = w.Write(StringRec{ID: "1", En: "a\nb & <x>", Pt: &pt})
	_ = w.Write(StringRec{ID: "2", En: "é"})
	if _, err := os.Stat(p); err == nil {
		t.Fatal("arquivo final não deve existir antes do Close")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAll[StringRec](p, VStrings)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %d", err, len(got))
	}
	if got[0].En != "a\nb & <x>" || got[0].Pt == nil || *got[0].Pt != "Olá <b>" || got[1].Pt != nil {
		t.Fatalf("%+v", got)
	}
	// esquema mais novo é recusado
	if _, err := ReadAll[StringRec](p, "strings/0"); err == nil {
		t.Fatal("esperava recusar versão maior")
	}
	if _, err := ReadAll[StringRec](p, "outro/1"); err == nil {
		t.Fatal("esperava recusar nome diferente")
	}
	raw, _ := os.ReadFile(p)
	if string(raw[:len(`{"_schema":"strings/1"}`)]) != `{"_schema":"strings/1"}` {
		t.Fatalf("cabeçalho: %q", raw[:40])
	}
}

func TestAppendResume(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	a, _ := OpenAppend(p, VTranslated)
	_ = a.Write(TranslatedRec{ID: "a"})
	a.Close()
	a, err := OpenAppend(p, VTranslated)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Write(TranslatedRec{ID: "b"})
	a.Close()
	got, err := ReadAll[TranslatedRec](p, VTranslated)
	if err != nil || len(got) != 2 || got[1].ID != "b" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestDoneAndFresh(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "in.txt"), []byte("1"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("2"), 0o644)
	if err := WriteDone(dir, "x", []string{"in.txt"}, []string{"out.txt"}, map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if ok, err := RequireFresh(dir, "x", "out.txt"); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("3"), 0o644)
	if _, err := RequireFresh(dir, "x", "out.txt"); err == nil {
		t.Fatal("esperava erro: arquivo mudou")
	}
	SkipFreshness = true
	if _, err := RequireFresh(dir, "x", "out.txt"); err != nil {
		t.Fatal("com --force a conferência é ignorada")
	}
	SkipFreshness = false
	if ok, err := RequireFresh(dir, "nunca", "out.txt"); ok || err != nil {
		t.Fatal("sem .done.json não confere nem falha")
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	un, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir); err == nil {
		t.Fatal("segunda trava deveria falhar")
	}
	un()
	if un2, err := Lock(dir); err != nil {
		t.Fatal(err)
	} else {
		un2()
	}
}

func TestSwapDir(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "docs", "x")
	// sem pasta antiga
	tmp := final + ".new"
	os.MkdirAll(tmp, 0o755)
	os.WriteFile(filepath.Join(tmp, "a"), []byte("1"), 0o644)
	if err := SwapDir(tmp, final); err != nil {
		t.Fatal(err)
	}
	// com pasta antiga
	os.MkdirAll(tmp, 0o755)
	os.WriteFile(filepath.Join(tmp, "b"), []byte("2"), 0o644)
	if err := SwapDir(tmp, final); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(final, "a")); err == nil {
		t.Fatal("o arquivo da pasta antiga deveria sumir")
	}
	if b, _ := os.ReadFile(filepath.Join(final, "b")); string(b) != "2" {
		t.Fatalf("%q", b)
	}
	for _, p := range []string{tmp, final + ".old"} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s deveria ter sumido", p)
		}
	}
}

type tRec struct {
	N int `json:"n"`
}

func TestEachIgnoraUltimaLinhaCortada(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	os.WriteFile(p, []byte("{\"_schema\":\"translated/1\"}\n{\"n\":1}\n{\"n\":2}\n{\"n\":3,\"x\":\"cor"), 0o644)
	var got []int
	if err := Each(p, "translated/1", func(r tRec) error { got = append(got, r.N); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
	// linha estragada no MEIO continua sendo erro
	os.WriteFile(p, []byte("{\"_schema\":\"translated/1\"}\n{\"n\":1}\n{lixo\n{\"n\":3}\n"), 0o644)
	if err := Each(p, "translated/1", func(r tRec) error { return nil }); err == nil {
		t.Fatal("linha estragada no meio devia dar erro")
	}
	// cabeçalho cortado: arquivo sem registros, sem erro
	os.WriteFile(p, []byte("{\"_sche"), 0o644)
	if err := Each(p, "translated/1", func(r tRec) error { t.Fatal("não devia ter registros"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAppendCortaLinhaIncompleta(t *testing.T) {
	var avisos []int64
	old := OnRepair
	OnRepair = func(path string, dropped int64) { avisos = append(avisos, dropped) }
	defer func() { OnRepair = old }()
	p := filepath.Join(t.TempDir(), "a.jsonl")
	os.WriteFile(p, []byte("{\"_schema\":\"translated/1\"}\n{\"n\":1}\n{\"n\":2,\"x\":\"cor"), 0o644)
	a, err := OpenAppend(p, "translated/1")
	if err != nil {
		t.Fatal(err)
	}
	a.Write(tRec{N: 9})
	a.Close()
	var got []int
	if err := Each(p, "translated/1", func(r tRec) error { got = append(got, r.N); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 9 {
		t.Fatalf("got %v (a linha cortada devia sumir e a nova ficar íntegra)", got)
	}
	if len(avisos) != 1 || avisos[0] == 0 {
		t.Fatalf("avisos %v", avisos)
	}
	// JSON completo sem quebra de linha: mantém o registro
	os.WriteFile(p, []byte("{\"_schema\":\"translated/1\"}\n{\"n\":1}\n{\"n\":2}"), 0o644)
	avisos = nil
	a, _ = OpenAppend(p, "translated/1")
	a.Write(tRec{N: 3})
	a.Close()
	got = nil
	Each(p, "translated/1", func(r tRec) error { got = append(got, r.N); return nil })
	if len(got) != 3 || got[1] != 2 || len(avisos) != 0 {
		t.Fatalf("got %v avisos %v", got, avisos)
	}
	// só um cabeçalho cortado: recomeça o arquivo
	os.WriteFile(p, []byte("{\"_sche"), 0o644)
	a, err = OpenAppend(p, "translated/1")
	if err != nil {
		t.Fatal(err)
	}
	a.Write(tRec{N: 5})
	a.Close()
	got = nil
	if err := Each(p, "translated/1", func(r tRec) error { got = append(got, r.N); return nil }); err != nil || len(got) != 1 || got[0] != 5 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestLockRemoveTravaDeProcessoMorto(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".lock"), []byte("2000000000\n"), 0o644) // pid que não existe
	un, err := Lock(dir)
	if err != nil {
		t.Fatalf("devia ter tomado a trava velha: %v", err)
	}
	// com a trava tomada agora (pid vivo = o nosso), outra tentativa recusa
	if _, err := Lock(dir); err == nil {
		t.Fatal("trava viva devia ser recusada")
	}
	un()
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !os.IsNotExist(err) {
		t.Fatal("a trava devia sumir no unlock")
	}
	// trava sem pid legível e recém-criada: não se mexe
	os.WriteFile(filepath.Join(dir, ".lock"), []byte(""), 0o644)
	if _, err := Lock(dir); err == nil {
		t.Fatal("trava vazia recém-criada devia ser respeitada")
	}
}

// Uma gravação que falha pela metade não pode deixar uma linha quebrada no meio
// do arquivo: o pedaço é tirado e os registros seguintes continuam legíveis.
func TestAppenderPartialWriteIsRolledBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	a, err := OpenAppend(path, "teste/1")
	if err != nil {
		t.Fatal(err)
	}
	type rec struct {
		ID string `json:"id"`
	}
	if err := a.Write(rec{"a"}); err != nil {
		t.Fatal(err)
	}
	real := a.write
	a.write = func(b []byte) (int, error) {
		n, _ := real(b[:len(b)/2])
		return n, errors.New("disco cheio")
	}
	if err := a.Write(rec{"quebrado"}); err == nil {
		t.Fatal("devia dar erro")
	}
	a.write = real
	if err := a.Write(rec{"b"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	var ids []string
	if err := Each(path, "teste/1", func(r rec) error { ids = append(ids, r.ID); return nil }); err != nil {
		t.Fatalf("o arquivo ficou ilegível: %v", err)
	}
	if strings.Join(ids, ",") != "a,b" || a.N != 2 {
		t.Fatalf("%v N=%d", ids, a.N)
	}
}

// Depois de `mtrad unlock` e de outra execução tomar a trava, o fim da primeira
// não pode apagar a trava da segunda.
func TestUnlockDoesNotRemoveForeignLock(t *testing.T) {
	dir := t.TempDir()
	un, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".lock")
	os.WriteFile(p, []byte("2000000001\n"), 0o644) // trava de outra execução
	un()
	if b, err := os.ReadFile(p); err != nil || strings.TrimSpace(string(b)) != "2000000001" {
		t.Fatalf("a trava alheia foi apagada: %q %v", b, err)
	}
}

// Se, entre ler e tomar, a trava velha foi trocada por uma nova, a nova é
// devolvida em vez de apagada.
func TestTakeOverStaleGivesBackALiveLock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".lock")
	os.WriteFile(p, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644) // trava nova e viva
	if takeOverStale(p, "2000000000") {
		t.Fatal("não devia tomar uma trava diferente da que foi lida")
	}
	if b, err := os.ReadFile(p); err != nil || strings.TrimSpace(string(b)) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("a trava nova não foi devolvida: %q %v", b, err)
	}
	if m, _ := filepath.Glob(p + ".stale-*"); len(m) != 0 {
		t.Fatalf("sobrou arquivo temporário: %v", m)
	}
	// e a trava velha de verdade é tomada
	os.WriteFile(p, []byte("2000000000\n"), 0o644)
	if !takeOverStale(p, "2000000000") {
		t.Fatal("devia tomar a trava velha")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("a trava velha devia ter sido removida")
	}
}
