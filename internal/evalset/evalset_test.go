package evalset

import (
	"fmt"
	"path/filepath"
	"testing"

	"mtrad/internal/schema"
)

func TestBuild(t *testing.T) {
	src := t.TempDir()
	w, _ := schema.Create(filepath.Join(src, "strings.jsonl"), schema.VStrings)
	for i := 0; i < 50; i++ {
		pt := fmt.Sprintf("Texto %d", i)
		en := fmt.Sprintf("Text %d", i)
		rec := schema.StringRec{ID: fmt.Sprintf("id%02d", i), Mod: "m", NS: "m", Key: fmt.Sprintf("k%d", i), En: en, Pt: &pt}
		switch i {
		case 0:
			rec.Pt = nil // sem tradução humana
		case 1:
			same := en
			rec.Pt = &same // igual ao inglês
		case 2:
			rec.En = "%s"
			x := "%s"
			rec.Pt = &x
		}
		w.Write(rec)
	}
	w.Close()
	out := filepath.Join(t.TempDir(), "ev")
	n, err := Build(src, out, 10, "s1")
	if err != nil || n != 10 {
		t.Fatalf("%v %d", err, n)
	}
	todo, _ := schema.ReadAll[schema.TodoRec](filepath.Join(out, "todo.jsonl"), schema.VTodo)
	ref, err := schema.ReadAll[ReferenceRec](filepath.Join(out, "reference.jsonl"), VReference)
	if err != nil || len(todo) != 10 || len(ref) != 10 {
		t.Fatalf("%v %d %d", err, len(todo), len(ref))
	}
	for i := range todo {
		if todo[i].ID != ref[i].ID || todo[i].En == ref[i].Pt || todo[i].ID == "id00" || todo[i].ID == "id01" || todo[i].ID == "id02" {
			t.Fatalf("%+v %+v", todo[i], ref[i])
		}
	}
	// mesmo sorteio com a mesma semente, outro com semente diferente
	out2 := filepath.Join(t.TempDir(), "ev2")
	Build(src, out2, 10, "s1")
	ref2, _ := schema.ReadAll[ReferenceRec](filepath.Join(out2, "reference.jsonl"), VReference)
	out3 := filepath.Join(t.TempDir(), "ev3")
	Build(src, out3, 10, "outra")
	ref3, _ := schema.ReadAll[ReferenceRec](filepath.Join(out3, "reference.jsonl"), VReference)
	same2, same3 := true, true
	for i := range ref {
		same2 = same2 && ref[i].ID == ref2[i].ID
		same3 = same3 && ref[i].ID == ref3[i].ID
	}
	if !same2 || same3 {
		t.Fatalf("sorteio: mesma semente igual=%v, outra semente igual=%v", same2, same3)
	}
	if _, err := Build(t.TempDir(), out, 5, "x"); err == nil {
		t.Fatal("sem strings.jsonl deveria falhar")
	}
}
