package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/scan"
	"mtrad/internal/schema"
	"mtrad/internal/testutil"
)

type sink struct{ msgs []string }

func (s *sink) Add(src, id, msg string) { s.msgs = append(s.msgs, msg) }

func TestExtractEndToEnd(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	inner := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":              `{"id":"inner","version":"2"}`,
		"assets/inner/lang/en_us.json": `{"i.a":"Inner A"}`,
	})
	outer := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":              `{"id":"outer","version":"1","jars":[{"file":"libs/in.jar"}]}`,
		"libs/in.jar":                  string(inner),
		"assets/outer/lang/en_us.json": "{\n // c\n \"a\":\"Hello %s\",\n \"b\":\"Same\",\n \"c\":\"Only en\",\n \"n\":3,\n}",
		"assets/outer/lang/pt_br.json": `{"a":"Olá %s","b":"Same"}`,
		"assets/other/lang/en_us.json": `{"o":"Other"}`,
	})
	os.WriteFile(filepath.Join(mods, "outer.jar"), outer, 0o644)
	// mesmo mod de novo (duplicado)
	os.WriteFile(filepath.Join(mods, "outer2.jar"), outer, 0o644)
	// jar sem fabric.mod.json: o mod vira o nome do arquivo
	os.WriteFile(filepath.Join(mods, "legacy-lib.jar"), testutil.MakeJar(t, map[string]string{"assets/leg/lang/en_us.json": `{"l":"L"}`}), 0o644)

	inv, err := scan.Scan(game, mods, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	var recs []schema.StringRec
	c, err := Extract(inv, s, func(r schema.StringRec) error { recs = append(recs, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	// outer: a,b,c (ns outer) + o (ns other); inner: i.a; legacy: l; outer2 é duplicado
	if c.Keys != 6 || c.ExistingPt != 2 || c.Duplicates != 5 {
		t.Fatalf("%+v", c)
	}
	by := map[string]schema.StringRec{}
	for _, r := range recs {
		by[r.Mod+"/"+r.NS+"/"+r.Key] = r
	}
	a := by["outer/outer/a"]
	if a.Pt == nil || *a.Pt != "Olá %s" || a.En != "Hello %s" || a.ID != schema.ID("outer", "outer", "a") || a.EnSha != schema.Sha("Hello %s") {
		t.Fatalf("%+v", a)
	}
	if by["outer/outer/c"].Pt != nil {
		t.Fatal("c não tem pt")
	}
	if _, ok := by["legacy-lib/leg/l"]; !ok {
		t.Fatalf("mod sem fabric.mod.json deveria usar o nome do jar: %v", by)
	}
	if by["inner/inner/i.a"].Src != "mods/outer.jar > libs/in.jar" {
		t.Fatalf("%+v", by["inner/inner/i.a"])
	}
	found := false
	for _, m := range s.msgs {
		if strings.Contains(m, "não são texto") {
			found = true
		}
	}
	if !found {
		t.Fatalf("deveria avisar do valor que não é texto: %v", s.msgs)
	}
}

func TestRunWritesStringsAndDone(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	os.WriteFile(filepath.Join(mods, "m.jar"), testutil.MakeJar(t, map[string]string{
		"fabric.mod.json": `{"id":"m","version":"1"}`, "assets/m/lang/en_us.json": `{"k":"v"}`}), 0o644)
	work := t.TempDir()
	inv, _ := scan.Scan(game, mods, nil)
	if err := schema.WriteJSON(filepath.Join(work, "inventory.json"), inv); err != nil {
		t.Fatal(err)
	}
	c, err := Run(work, &sink{})
	if err != nil || c.Keys != 1 {
		t.Fatalf("%v %+v", err, c)
	}
	got, err := schema.ReadAll[schema.StringRec](filepath.Join(work, "strings.jsonl"), schema.VStrings)
	if err != nil || len(got) != 1 || got[0].Key != "k" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestKubeJSLang(t *testing.T) {
	game := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(game, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("kubejs/assets/ftbquestlocalizer/lang/en_us.json", `{"q.a":"Hello &6friend","q.b":"Done"}`)
	w("kubejs/assets/ftbquestlocalizer/lang/pt_br.json", `{"q.b":"Pronto"}`)
	w("kubejs/assets/ftbquestlocalizer/lang/ru_ru.json", `{"q.a":"Привет"}`)
	w("kubejs/assets/semingles/lang/de_de.json", `{"x":"y"}`)
	w("kubejs/assets/quebrado/lang/en_us.json", `{`)
	w("FTBLang/kubejs/assets/ftbquestlocalizer/lang/en_us.json", `{"backup":"não ler"}`)
	var recs []schema.StringRec
	s := &sinkT{}
	c, err := Extract(schema.Inventory{GameDir: game}, s, func(r schema.StringRec) error { recs = append(recs, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.Keys != 2 || c.KubeJS != 1 || c.ExistingPt != 1 || c.FileErrors != 1 || len(recs) != 2 {
		t.Fatalf("%+v %v %v", c, recs, s.msgs)
	}
	if recs[0].Kind != "ftbql" || recs[1].Kind != "ftbql" {
		t.Fatalf("o texto de missões devia ter o tipo ftbql: %+v", recs)
	}
	if recs[0].Mod != "kubejs" || recs[0].NS != "ftbquestlocalizer" || recs[0].Key != "q.a" || recs[0].Pt != nil || recs[1].Pt == nil || *recs[1].Pt != "Pronto" {
		t.Fatalf("%+v", recs)
	}
}

type sinkT struct{ msgs []string }

func (s *sinkT) Add(src, id, msg string) { s.msgs = append(s.msgs, src+": "+msg) }
