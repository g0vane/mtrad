package patchouli

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

const entry = `{
  "name": "Intro",
  "icon": "minecraft:book",
  "category": "mod:basics",
  "pages": [
    {"type": "patchouli:text", "title": "Olá", "text": "Hello $(l:foo)world$(/l).$(br)Second"},
    {"type": "patchouli:spotlight", "item": "minecraft:stone", "text": "#text"},
    {"type": "patchouli:link", "url": "https://x.y", "link_text": "Click here", "text": "item.mod.thing"}
  ]
}`

func build(t *testing.T, files map[string]string) schema.Inventory {
	t.Helper()
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	os.WriteFile(filepath.Join(mods, "m.jar"), testutil.MakeJar(t, files), 0o644)
	inv, err := scan.Scan(game, mods, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func run(t *testing.T, inv schema.Inventory) (map[string]schema.StringRec, []schema.DocRec, map[string]string, Counts, *sink) {
	t.Helper()
	s := &sink{}
	strs := map[string]schema.StringRec{}
	var docs []schema.DocRec
	origs := map[string]string{}
	c, err := Extract(inv, s, Out{
		Str: func(r schema.StringRec) error { strs[r.Key] = r; return nil },
		Doc: func(d schema.DocRec, b []byte) error { docs = append(docs, d); origs[d.ID] = string(b); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return strs, docs, origs, c, s
}

func TestExtractEntry(t *testing.T) {
	inv := build(t, map[string]string{
		"fabric.mod.json":                                               `{"id":"mod","version":"1"}`,
		"data/mod/patchouli_books/guide/book.json":                      `{"name":"Guide","use_resource_pack":true}`,
		"assets/mod/patchouli_books/guide/en_us/entries/intro.json":     entry,
		"assets/mod/patchouli_books/guide/en_us/categories/basics.json": `{"name":"Basics","description":"All basics","icon":"minecraft:stone"}`,
		"assets/mod/patchouli_books/guide/pt_br/entries/intro.json":     `{"name":"Introdução","pages":[{"title":"Oi","text":"Olá $(l:foo)mundo$(/l)"}]}`,
	})
	strs, docs, origs, c, _ := run(t, inv)
	// intro: name, pages/0 title+text, pages/2 link_text (text é chave de idioma e pages/1 é variável) = 4; basics: name, description = 2
	if c.Keys != 6 || c.Docs != 2 || c.Books != 1 || c.ExistingPt != 3 {
		t.Fatalf("%+v", c)
	}
	k := "entries/intro.json#/pages/0/text"
	r, ok := strs[k]
	if !ok {
		t.Fatalf("falta %s em %v", k, strs)
	}
	if r.Kind != "patchouli" || r.Loc.Ptr != "/pages/0/text" || r.Loc.Doc != "assets/mod/patchouli_books/guide/pt_br/entries/intro.json" {
		t.Fatalf("%+v", r)
	}
	if r.Pt == nil || *r.Pt != "Olá $(l:foo)mundo$(/l)" || r.ID != schema.ID("mod", "patchouli:mod/guide", k) {
		t.Fatalf("%+v", r)
	}
	if _, bad := strs["entries/intro.json#/pages/1/text"]; bad {
		t.Fatal("variável #text não devia ser extraída")
	}
	if _, bad := strs["entries/intro.json#/pages/2/text"]; bad {
		t.Fatal("chave de idioma não devia ser extraída")
	}
	if _, ok := strs["entries/intro.json#/pages/2/link_text"]; !ok {
		t.Fatal("link_text devia ser extraído")
	}
	if _, bad := strs["entries/intro.json#/icon"]; bad {
		t.Fatal("icon não devia ser extraído")
	}
	if len(docs) != 2 || !strings.Contains(origs[docs[0].ID], `"patchouli:text"`) && !strings.Contains(origs[docs[1].ID], `"patchouli:text"`) {
		t.Fatalf("%+v", docs)
	}
	for _, d := range docs {
		if d.Format != "json" || d.Kind != "patchouli" || !strings.Contains(d.Out, "/pt_br/") {
			t.Fatalf("%+v", d)
		}
	}
}

func TestTranslatableRule(t *testing.T) {
	no := []string{"", "  ", "#text", "#text#", "#a.b", "item.mod.thing", "botania.page.spellCloth0",
		"hexcasting.page.circle_patterns.circle/bounds/max", "hexcasting.action.hexcasting:create_lava", "v1.2", "$(n)#input#/$ → $(n)#output#/$"}
	yes := []string{"Hello", "Hello world.", "Gem.", "See the $(l:x)guide$(/l).", "A.", "Mana pool", "e.g. this", "Use item.mod.thing here", "Rank #1 and #2"}
	for _, s := range no {
		if translatable(s) {
			t.Errorf("%q não devia ser traduzível", s)
		}
	}
	for _, s := range yes {
		if !translatable(s) {
			t.Errorf("%q devia ser traduzível", s)
		}
	}
}

func TestShortcutStringPagesAndComponentText(t *testing.T) {
	inv := build(t, map[string]string{
		"fabric.mod.json": `{"id":"mod","version":"1"}`,
		"assets/mod/patchouli_books/g/en_us/entries/a.json": `{"name":"A","pages":["Plain text page","#var",{"type":"patchouli:text","text":{"text":"Rich","color":"red"}}]}`,
	})
	strs, _, _, c, _ := run(t, inv)
	for _, k := range []string{"entries/a.json#/name", "entries/a.json#/pages/0", "entries/a.json#/pages/2/text/text"} {
		if _, ok := strs[k]; !ok {
			t.Errorf("falta %s em %v", k, strs)
		}
	}
	if _, bad := strs["entries/a.json#/pages/1"]; bad || c.Keys != 3 {
		t.Fatalf("%+v %v", c, strs)
	}
}

func TestI18nBookSkippedAndOldBookWarned(t *testing.T) {
	inv := build(t, map[string]string{
		"fabric.mod.json":                                      `{"id":"mod","version":"1"}`,
		"data/mod/patchouli_books/lang/book.json":              `{"name":"x","i18n":true}`,
		"assets/mod/patchouli_books/lang/en_us/entries/a.json": `{"name":"mod.entry.a"}`,
		"data/mod/patchouli_books/old/en_us/entries/b.json":    `{"name":"Old"}`,
	})
	strs, docs, _, c, s := run(t, inv)
	if len(strs) != 0 || len(docs) != 0 || c.Skipped != 2 {
		t.Fatalf("%+v %v", c, strs)
	}
	joined := strings.Join(s.msgs, "\n")
	if !strings.Contains(joined, "i18n") || !strings.Contains(joined, "antes do 1.20") {
		t.Fatal(joined)
	}
}

func TestBadJSONCounted(t *testing.T) {
	inv := build(t, map[string]string{
		"fabric.mod.json": `{"id":"mod","version":"1"}`,
		"assets/mod/patchouli_books/g/en_us/entries/a.json": `{"name": `,
		"assets/mod/patchouli_books/g/en_us/entries/b.json": `{"name":"Ok"}`,
	})
	strs, _, _, c, s := run(t, inv)
	if c.FileErrors != 1 || len(strs) != 1 || len(s.msgs) != 1 {
		t.Fatalf("%+v %v", c, s.msgs)
	}
}

func TestRunWritesFiles(t *testing.T) {
	inv := build(t, map[string]string{
		"fabric.mod.json": `{"id":"mod","version":"1"}`,
		"assets/mod/patchouli_books/g/en_us/entries/b.json": `{"name":"Ok"}`,
	})
	work := t.TempDir()
	if err := schema.WriteJSON(filepath.Join(work, "inventory.json"), inv); err != nil {
		t.Fatal(err)
	}
	c, err := Run(work, &sink{})
	if err != nil || c.Keys != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	docs, err := schema.ReadAll[schema.DocRec](filepath.Join(work, "docs.patchouli.jsonl"), schema.VDocs)
	if err != nil || len(docs) != 1 {
		t.Fatal(err, docs)
	}
	if b, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(docs[0].Orig))); err != nil || !strings.Contains(string(b), "Ok") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "patchouli.done.json")); err != nil {
		t.Fatal(err)
	}
}
