package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/review"
	"mtrad/internal/schema"
	"mtrad/internal/validate"
)

func reviewWork(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	os.MkdirAll(dir, 0o755)
	tw, _ := schema.Create(filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	pw, _ := schema.Create(filepath.Join(dir, "protected.jsonl"), schema.VProtected)
	trw, _ := schema.OpenAppend(filepath.Join(dir, "translated.jsonl"), schema.VTranslated)
	tw.Write(schema.TodoRec{ID: "b", Mod: "m", NS: "m", Key: "k.b", En: "Press %s", EnSha: schema.Sha("Press %s"), Reason: "missing"})
	pw.Write(schema.ProtectedRec{ID: "b", Masked: "Press ⟦1⟧", Style: "brackets", Tokens: []schema.Token{{T: "⟦1⟧", Raw: "%s"}}})
	trw.Write(schema.TranslatedRec{ID: "b", Out: "Pressione"})
	tw.Close()
	pw.Close()
	trw.Close()
	return dir
}

func TestReviewExportImportFluxoCompleto(t *testing.T) {
	work := reviewWork(t)
	var out bytes.Buffer
	csvPath := filepath.Join(t.TempDir(), "r.csv")
	if err := stageReview(&out, work, []string{"export", "-o", csvPath}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(csvPath)
	if !strings.Contains(string(b), "Press %s") || !strings.Contains(string(b), "Pressione") {
		t.Fatalf("csv sem o conteúdo esperado:\n%s", b)
	}
	// o usuário preenche new_pt (última coluna) e acrescenta um id que não existe
	filled := strings.Replace(string(b), "Pressione,\n", "Pressione,Pressione %s\n", 1) + "zzzz,reprovado,m,k.z,,x,y,Texto\n"
	os.WriteFile(csvPath, []byte(filled), 0o644)
	out.Reset()
	if err := stageReview(&out, work, []string{"import", csvPath}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 correções guardadas, 1 ignoradas") {
		t.Fatalf("%s", out.String())
	}
	c, err := validate.Run(work, validate.Options{})
	if err != nil || c.Failed != 0 || c.Reviewed != 1 || c.OK != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	out.Reset()
	stageReview(&out, work, []string{"status"})
	if !strings.Contains(out.String(), "1 correções manuais (0 desatualizadas") {
		t.Fatal(out.String())
	}
	// drop desfaz
	if err := stageReview(&out, work, []string{"drop", "b"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := validate.Run(work, validate.Options{}); c.Failed != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestReviewSetEUsoErrado(t *testing.T) {
	work := reviewWork(t)
	var out bytes.Buffer
	if err := stageReview(&out, work, []string{"set", "b", "Pressione", "%s"}); err != nil {
		t.Fatal(err)
	}
	m, _ := review.Load(work)
	if m["b"].Pt != "Pressione %s" || m["b"].EnSha != schema.Sha("Press %s") {
		t.Fatalf("%+v", m["b"])
	}
	for _, a := range [][]string{{}, {"import"}, {"set", "b"}, {"drop"}, {"export", "--scope", "x"}, {"dance"}, {"set", "nao-existe", "x"}} {
		if err := stageReview(&out, work, a); err == nil {
			t.Errorf("%v devia dar erro", a)
		}
	}
}
