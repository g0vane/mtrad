package review

import (
	"bytes"
	"strings"
	"testing"
)

func TestAppendLoadUltimaVale(t *testing.T) {
	w := t.TempDir()
	if m, err := Load(w); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	if err := Append(w, Entry{ID: "a", Pt: "um", EnSha: "x"}, Entry{ID: "b", Pt: "dois", EnSha: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(w, Entry{ID: "a", Pt: "um corrigido", EnSha: "x"}); err != nil {
		t.Fatal(err)
	}
	m, err := Load(w)
	if err != nil || len(m) != 2 || m["a"].Pt != "um corrigido" || m["a"].Time == "" {
		t.Fatalf("%v %v", m, err)
	}
}

func TestCSVIdaEVolta(t *testing.T) {
	items := []Item{
		{ID: "1", Status: "reprovado", Mod: "m", Key: "k.a", Reasons: []string{"marca perdida ⟦1⟧", "outra"}, En: "Hello, \"world\"\nline2", Current: "Olá"},
		{ID: "2", Status: "aviso", En: "x", Current: "y"},
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, items); err != nil {
		t.Fatal(err)
	}
	// o usuário preenche new_pt da primeira linha
	txt := strings.Replace(buf.String(), `,Olá,`, `,Olá,"Olá, ""mundo""`+"\n"+`linha2"`, 1)
	rows, err := ReadCSV(strings.NewReader(txt))
	if err != nil || len(rows) != 1 || rows[0].ID != "1" || rows[0].NewPt != "Olá, \"mundo\"\nlinha2" {
		t.Fatalf("%+v %v\n%s", rows, err, txt)
	}
}

func TestReadCSVPontoEVirgulaEBOM(t *testing.T) {
	in := "\ufeffid;status;new_pt\r\nabc;reprovado;Olá mundo\r\ndef;aviso;\r\n"
	rows, err := ReadCSV(strings.NewReader(in))
	if err != nil || len(rows) != 1 || rows[0].ID != "abc" || rows[0].NewPt != "Olá mundo" {
		t.Fatalf("%+v %v", rows, err)
	}
}

func TestReadCSVSemColunas(t *testing.T) {
	if _, err := ReadCSV(strings.NewReader("a,b\n1,2\n")); err == nil {
		t.Fatal("devia exigir id e new_pt")
	}
	if _, err := ReadCSV(strings.NewReader("")); err == nil {
		t.Fatal("arquivo vazio devia dar erro")
	}
}
