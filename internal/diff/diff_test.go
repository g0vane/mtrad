package diff

import (
	"path/filepath"
	"strconv"
	"testing"

	"mtrad/internal/schema"
)

func sp(s string) *string { return &s }

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		rec  schema.StringRec
		kind Kind
		pt   string
	}{
		{"traduzido", schema.StringRec{En: "Hello", Pt: sp("Olá")}, Reuse, "Olá"},
		{"faltando", schema.StringRec{En: "Hello"}, TodoMissing, ""},
		{"pt vazio", schema.StringRec{En: "Hello", Pt: sp("  ")}, TodoMissing, ""},
		{"igual ao inglês", schema.StringRec{En: "Hello", Pt: sp("Hello")}, TodoIdentical, ""},
		{"só marcas, sem pt", schema.StringRec{En: "%s/%s §a"}, Reuse, "%s/%s §a"},
		{"só números", schema.StringRec{En: "100%%"}, Reuse, "100%%"},
		{"só marcas, com pt igual", schema.StringRec{En: "%d", Pt: sp("%d")}, Reuse, "%d"},
		{"tag e url", schema.StringRec{En: "<b>https://x.y/z</b>"}, Reuse, "<b>https://x.y/z</b>"},
		{"texto com marca", schema.StringRec{En: "Press %s"}, TodoMissing, ""},
	}
	for _, c := range cases {
		k, pt := Classify(c.rec)
		if k != c.kind || pt != c.pt {
			t.Errorf("%s: %v %q", c.name, k, pt)
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	w, _ := schema.Create(filepath.Join(dir, "strings.jsonl"), schema.VStrings)
	_ = w.Write(schema.StringRec{ID: "1", Mod: "m", NS: "m", Key: "a", En: "Hello", Pt: sp("Olá")})
	_ = w.Write(schema.StringRec{ID: "2", Mod: "m", NS: "m", Key: "b", En: "Bye"})
	_ = w.Write(schema.StringRec{ID: "3", Mod: "m", NS: "m", Key: "c", En: "Same", Pt: sp("Same")})
	_ = w.Write(schema.StringRec{ID: "4", Mod: "m", NS: "m", Key: "d", En: "%s"})
	w.Close()
	c, err := Run(dir, Filter{})
	if err != nil || c.Total != 4 || c.Reused != 2 || c.Missing != 1 || c.Identical != 1 {
		t.Fatalf("%v %+v", err, c)
	}
	todo, err := schema.ReadAll[schema.TodoRec](filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	if err != nil || len(todo) != 2 || todo[0].Reason != "missing" || todo[1].Reason != "identical" {
		t.Fatalf("%v %+v", err, todo)
	}
	re, err := schema.ReadAll[schema.ReusedRec](filepath.Join(dir, "reused.jsonl"), schema.VReused)
	if err != nil || len(re) != 2 || re[0].Pt != "Olá" || re[1].Pt != "%s" {
		t.Fatalf("%v %+v", err, re)
	}
}

func TestRunSeveralSources(t *testing.T) {
	dir := t.TempDir()
	w, _ := schema.Create(filepath.Join(dir, "strings.jsonl"), schema.VStrings)
	_ = w.Write(schema.StringRec{ID: "1", Mod: "m", NS: "m", Key: "a", En: "Hello"})
	w.Close()
	p, _ := schema.Create(filepath.Join(dir, "strings.patchouli.jsonl"), schema.VStrings)
	loc := &schema.Loc{Doc: "assets/m/patchouli_books/g/pt_br/entries/a.json", Ptr: "/name"}
	_ = p.Write(schema.StringRec{ID: "2", Mod: "m", NS: "patchouli:m/g", Key: "entries/a.json#/name", En: "Intro", Pt: sp("Introdução"), Kind: "patchouli", Loc: loc})
	_ = p.Write(schema.StringRec{ID: "3", Mod: "m", NS: "patchouli:m/g", Key: "entries/a.json#/pages/0/text", En: "Body", Kind: "patchouli", Loc: &schema.Loc{Doc: loc.Doc, Ptr: "/pages/0/text"}})
	p.Close()
	f, _ := schema.Create(filepath.Join(dir, "strings.ftbq.jsonl"), schema.VStrings)
	_ = f.Write(schema.StringRec{ID: "4", Mod: "ftbquests", NS: "ftbquests", Key: "k#/x", En: "&a{image:foo:bar.png}", Kind: "ftbq", Loc: &schema.Loc{Doc: "d", Ptr: "/x"}})
	_ = f.Write(schema.StringRec{ID: "5", Mod: "ftbquests", NS: "ftbquests", Key: "k#/y", En: "&6Gold quest", Kind: "ftbq", Loc: &schema.Loc{Doc: "d", Ptr: "/y"}})
	f.Close()
	c, err := Run(dir, Filter{})
	if err != nil || c.Total != 5 || c.Reused != 2 || c.Missing != 3 {
		t.Fatalf("%v %+v", err, c)
	}
	todo, _ := schema.ReadAll[schema.TodoRec](filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	// ordem: strings.jsonl, depois as outras fontes em ordem alfabética (ftbq, patchouli)
	if len(todo) != 3 || todo[2].Kind != "patchouli" || todo[2].Loc == nil || todo[2].Loc.Ptr != "/pages/0/text" || todo[1].Kind != "ftbq" {
		t.Fatalf("%+v", todo)
	}
	re, _ := schema.ReadAll[schema.ReusedRec](filepath.Join(dir, "reused.jsonl"), schema.VReused)
	// Patchouli com tradução e o FTB só com comandos (sem letras)
	if len(re) != 2 || re[1].Pt != "Introdução" || re[1].Kind != "patchouli" || re[0].Kind != "ftbq" || re[0].Pt != "&a{image:foo:bar.png}" {
		t.Fatalf("%+v", re)
	}
	var d schema.Done
	if err := schema.ReadJSON(filepath.Join(dir, "diff.done.json"), &d, schema.VDone); err != nil || len(d.Inputs) != 3 {
		t.Fatalf("%v %+v", err, d)
	}
}

func TestRunRejectsDuplicateIDAcrossSources(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"strings.jsonl", "strings.ftbq.jsonl"} {
		w, _ := schema.Create(filepath.Join(dir, n), schema.VStrings)
		_ = w.Write(schema.StringRec{ID: "same", En: "Hello"})
		w.Close()
	}
	if _, err := Run(dir, Filter{}); err == nil {
		t.Fatal("devia recusar id repetido entre fontes")
	}
}

func TestRunWithoutSources(t *testing.T) {
	if _, err := Run(t.TempDir(), Filter{}); err == nil {
		t.Fatal("devia falhar sem nenhum strings*.jsonl")
	}
}

func TestClassifyFTBQ(t *testing.T) {
	if k, _ := Classify(schema.StringRec{En: "&a&l{image:x:y}", Kind: "ftbq"}); k != Reuse {
		t.Fatal("só marcas do FTB não precisa de tradução")
	}
	// no arquivo de idioma "&a" não é marca: tem letra
	if k, _ := Classify(schema.StringRec{En: "&a"}); k != TodoMissing {
		t.Fatalf("%v", k)
	}
}

func TestRunFilter(t *testing.T) {
	mk := func() string {
		dir := t.TempDir()
		w, _ := schema.Create(filepath.Join(dir, "strings.jsonl"), schema.VStrings)
		_ = w.Write(schema.StringRec{ID: "1", Mod: "Quests", NS: "q", Key: "a", En: "Hello"})
		_ = w.Write(schema.StringRec{ID: "2", Mod: "big", NS: "b", Key: "a", En: "Bye"})
		_ = w.Write(schema.StringRec{ID: "3", Mod: "big", NS: "b", Key: "b", En: "Ola", Pt: sp("Olá")})
		_ = w.Write(schema.StringRec{ID: "4", Mod: "other", NS: "o", Key: "a", En: "Again", Pt: sp("Again")})
		w.Close()
		return dir
	}
	todoIDs := func(dir string) string {
		todo, _ := schema.ReadAll[schema.TodoRec](filepath.Join(dir, "todo.jsonl"), schema.VTodo)
		s := ""
		for _, r := range todo {
			s += r.ID
		}
		return s
	}
	d := mk()
	c, err := Run(d, Filter{Only: []string{"quests"}})
	if err != nil || todoIDs(d) != "1" || c.Filtered != 2 || c.Reused != 1 {
		t.Fatalf("only: %v %+v %s", err, c, todoIDs(d))
	}
	d = mk()
	c, err = Run(d, Filter{Skip: []string{"BIG"}})
	if err != nil || todoIDs(d) != "14" || c.Filtered != 1 {
		t.Fatalf("skip: %v %+v %s", err, c, todoIDs(d))
	}
}

func TestFormatOnlyTextsNeedNoTranslation(t *testing.T) {
	for _, s := range []string{"%s%s/t", "%s / %s B", "%s%s/10m", "%d%%", "%1$s/%2$s"} {
		if k, pt := Classify(schema.StringRec{En: s}); k != Reuse || pt != s {
			t.Errorf("%q: %v %q", s, k, pt)
		}
	}
	for _, s := range []string{"%s items", "On", "%s Ok to go", "§aOn"} {
		if k, _ := Classify(schema.StringRec{En: s}); k == Reuse {
			t.Errorf("%q devia ir para tradução", s)
		}
	}
}

func TestModStatsGuessesRepetitiveMods(t *testing.T) {
	dir := t.TempDir()
	w, _ := schema.Create(filepath.Join(dir, "strings.jsonl"), schema.VStrings)
	mats := []string{"Oak", "Birch", "Spruce", "Stone", "Granite", "Marble", "Brick", "Sand"}
	shapes := []string{"Door", "Slab", "Stairs", "Pillar", "Wall"}
	n := 0
	for i := 0; i < 400; i++ { // nomes "material forma número": vocabulário minúsculo
		n++
		_ = w.Write(schema.StringRec{ID: "c" + strconv.Itoa(n), Mod: "chipped", NS: "chipped", Key: "block." + strconv.Itoa(n), En: mats[i%8] + " " + shapes[(i/8)%5] + " " + strconv.Itoa(i%7)})
	}
	for i := 0; i < 400; i++ { // frases longas e variadas
		n++
		_ = w.Write(schema.StringRec{ID: "m" + strconv.Itoa(n), Mod: "create", NS: "create", Key: "tooltip." + strconv.Itoa(n), En: "Rotates the shaft number " + strconv.Itoa(n) + " with speed and stress that depends on how many machines are attached to the network " + strconv.Itoa(n*7)})
	}
	_ = w.Write(schema.StringRec{ID: "x", Mod: "create", NS: "create", Key: "ja.traduzido", En: "Hello", Pt: sp("Olá")}) // não conta
	w.Close()
	st, err := ModStats(dir)
	if err != nil || len(st) != 2 {
		t.Fatalf("%v %+v", err, st)
	}
	got := map[string]ModStat{}
	for _, s := range st {
		got[s.Mod] = s
	}
	if !got["chipped"].Repetitive || got["chipped"].Texts != 400 {
		t.Errorf("chipped devia ser repetitivo: %+v", got["chipped"])
	}
	if got["create"].Repetitive || got["create"].Texts != 400 {
		t.Errorf("create não devia ser repetitivo: %+v", got["create"])
	}
	if _, err := ModStats(t.TempDir()); err == nil {
		t.Error("sem strings*.jsonl devia dar erro")
	}
}
