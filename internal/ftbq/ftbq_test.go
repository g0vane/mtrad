package ftbq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

type sink struct{ msgs []string }

func (s *sink) Add(src, id, msg string) { s.msgs = append(s.msgs, msg) }

func write(t *testing.T, game, rel, body string) {
	t.Helper()
	p := filepath.Join(game, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, game string) (map[string]schema.StringRec, []schema.DocRec, Counts, *sink) {
	t.Helper()
	s := &sink{}
	strs := map[string]schema.StringRec{}
	var docs []schema.DocRec
	c, err := Extract(game, s, Out{
		Str: func(r schema.StringRec) error { strs[r.Key] = r; return nil },
		Doc: func(d schema.DocRec, b []byte) error { docs = append(docs, d); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return strs, docs, c, s
}

const snbtEn = `{
	"chapter.AAAA.title": "Getting Started"
	"quest.BBBB.title": "First &aquest&r"
	"quest.BBBB.quest_desc": [
		"Line one"
		""
		"{image:ftbquests:textures/a.png}"
		"Line &6two"
	]
	"quest.CCCC.title": ""
}`

func TestEra2Snbt(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/lang/en_us.snbt", snbtEn)
	write(t, game, QuestsDir+"/lang/pt_br.snbt", `{"quest.BBBB.title": "Primeira missão"
"quest.BBBB.quest_desc": ["Linha um"]}`)
	strs, docs, c, _ := run(t, game)
	// title, title, desc 0, desc 2 (a imagem tem texto, a vazia não), desc 3
	if c.Era != "snbt" || c.Keys != 5 || c.Files != 1 || c.ExistingPt != 2 {
		t.Fatalf("%+v %v", c, strs)
	}
	if len(docs) != 1 || docs[0].Out != QuestsDir+"/lang/pt_br.snbt" || docs[0].Format != "snbt" {
		t.Fatalf("%+v", docs)
	}
	r := strs[QuestsDir+"/lang/en_us.snbt#/quest.BBBB.quest_desc/0"]
	if r.Pt == nil || *r.Pt != "Linha um" || r.Loc.Ptr != "/quest.BBBB.quest_desc/0" || r.Kind != "ftbq" {
		t.Fatalf("%+v", r)
	}
	if r := strs[QuestsDir+"/lang/en_us.snbt#/quest.BBBB.quest_desc/3"]; r.Pt != nil || r.En != "Line &6two" {
		t.Fatalf("%+v", r)
	}
	if _, bad := strs[QuestsDir+"/lang/en_us.snbt#/quest.CCCC.title"]; bad {
		t.Fatal("texto vazio não devia ser extraído")
	}
}

func TestEra3Json5Split(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/lang/en_us/chapters.json5", `// c
{ "chapter.A.title": "One", }`)
	write(t, game, QuestsDir+"/lang/en_us/sub/quests.json5", `{"quest.B.title":"Two"}`)
	write(t, game, QuestsDir+"/lang/en_us/readme.txt", `ignorado`)
	strs, docs, c, _ := run(t, game)
	if c.Era != "json5" || c.Keys != 2 || len(docs) != 2 {
		t.Fatalf("%+v %v", c, strs)
	}
	outs := []string{docs[0].Out, docs[1].Out}
	if outs[0] != QuestsDir+"/lang/pt_br/chapters.json5" || outs[1] != QuestsDir+"/lang/pt_br/sub/quests.json5" || docs[0].Format != "json5" {
		t.Fatalf("%v", outs)
	}
}

// Capítulo no formato que o FTB Quests 1.20.1 grava (SNBT com números
// tipados, textos em campos "title", "subtitle", "description" e "hover").
const chapter1201 = `{
	id: "5DDF8D6C7D2BA17D"
	group: ""
	order_index: 0
	filename: "getting_started"
	title: "Getting Started"
	icon: {
		id: "minecraft:grass_block"
	}
	subtitle: [
		"Your first steps"
	]
	images: [{
		x: 1.0d
		y: 2.0d
		image: "ftbquests:textures/logo.png"
		hover: ["Click me"]
		click: "https://example.com"
	}]
	quests: [{
		title: "Welcome"
		x: 0.0d
		y: 0.0d
		subtitle: "Be &6careful&r"
		description: [
			"First line with &#FF8800color&r and 100% fun"
			""
			"{@pagebreak}"
			"{image:ftbquests:textures/a.png width:100 height:100 align:center fit:true}"
			"{already.a.key}"
			"{\"text\":\"rich\",\"color\":\"red\"}"
			"Second page"
		]
		id: "0000000000000001"
		tasks: [{
			id: "0000000000000002"
			type: "item"
			title: "Collect wood"
			item: {
				id: "minecraft:oak_log"
				Count: 1b
				tag: {
					title: "Do not touch"
				}
			}
		}]
		rewards: [{
			id: "0000000000000003"
			type: "xp"
			xp: 50
			title: "Free XP"
		}]
	}]
}
`

func TestEra1Inline(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/chapters/getting_started.snbt", chapter1201)
	write(t, game, QuestsDir+"/data.snbt", "{\n\tversion: 13\n}\n")
	strs, docs, c, s := run(t, game)
	p := KeyPrefix
	want := map[string]string{
		p + "5ddf8d6c7d2ba17d.title":                   "Getting Started",
		p + "5ddf8d6c7d2ba17d.subtitle.0":              "Your first steps",
		p + "5ddf8d6c7d2ba17d.images.0.hover.0":        "Click me",
		p + "0000000000000001.title":                   "Welcome",
		p + "0000000000000001.subtitle":                "Be &6careful&r",
		p + "0000000000000001.description.0":           "First line with &#FF8800color&r and 100% fun",
		p + "0000000000000001.description.6":           "Second page",
		p + "0000000000000002.title":                   "Collect wood",
		p + "0000000000000003.title":                   "Free XP",
		p + "0000000000000001.description.5.rich.text": "rich",
	}
	if c.Era != "inline" || c.Keys != len(want) || c.Files != 1 || c.Skipped != 0 || len(strs) != len(want) || len(docs) != 1 {
		t.Fatalf("%+v keys=%d docs=%d msgs=%v", c, len(strs), len(docs), s.msgs)
	}
	for k, en := range want {
		r, ok := strs[k]
		kind := "ftbqi"
		if strings.Contains(k, ".rich.") {
			kind = "ftbqr"
		}
		if !ok || r.En != en || r.Kind != kind || r.Loc == nil || r.Loc.Doc != QuestsDir+"/chapters/getting_started.snbt" {
			t.Errorf("%s: %+v", k, r)
		}
	}
	// a NBT do item (livro com título) e as linhas que não são texto ficam de fora
	for k := range strs {
		if strings.Contains(k, "oak") || strings.Contains(k, "pagebreak") {
			t.Errorf("chave indevida %s", k)
		}
	}
	if strings.Contains(strings.Join(s.msgs, ""), "JSON de texto rico") {
		t.Errorf("a linha de JSON rico com texto agora é traduzida, não pulada: %v", s.msgs)
	}
	if docs[0].Kind != "ftbqi" || docs[0].Format != "snbt" || docs[0].Out != docs[0].Src {
		t.Errorf("%+v", docs[0])
	}
	// o ponteiro de cada texto acha o mesmo texto no documento
	root, err := relaxed.Parse([]byte(chapter1201))
	if err != nil {
		t.Fatal(err)
	}
	for k, r := range strs {
		if strings.Contains(r.Loc.Ptr, "#") {
			continue // texto de dentro de JSON rico: conferido em TestRichJSON
		}
		if n := root.Get(r.Loc.Ptr); n == nil || n.Kind != relaxed.Str || n.Str != r.En {
			t.Errorf("%s: ponteiro %s não acha o texto", k, r.Loc.Ptr)
		}
	}
}

func TestEra1InlineAlreadyKeyedIsSkipped(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/chapters/a.snbt", `{id: "AA", title: "{some.key}", quests: [{id: "BB", title: "{image:a:b.png}"}]}`)
	strs, docs, c, _ := run(t, game)
	if len(strs) != 0 || len(docs) != 0 || c.Era != "inline" {
		t.Fatalf("%v %v %+v", strs, docs, c)
	}
}

func TestNoFTBQuests(t *testing.T) {
	_, _, c, s := run(t, t.TempDir())
	if c.Era != "" || c.Keys != 0 || len(s.msgs) != 0 {
		t.Fatalf("%+v %v", c, s.msgs)
	}
}

func TestBrokenFileCounted(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/lang/en_us.snbt", `{"a": `)
	_, _, c, s := run(t, game)
	if c.FileErrors != 1 || len(s.msgs) != 1 {
		t.Fatalf("%+v %v", c, s.msgs)
	}
}

func TestRun(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/lang/en_us.snbt", snbtEn)
	work := t.TempDir()
	schema.WriteJSON(filepath.Join(work, "inventory.json"), schema.Inventory{Schema: schema.VInventory, GameDir: game})
	c, err := Run(work, &sink{})
	if err != nil || c.Keys != 5 {
		t.Fatalf("%+v %v", c, err)
	}
	docs, _ := schema.ReadAll[schema.DocRec](filepath.Join(work, "docs.ftbq.jsonl"), schema.VDocs)
	if len(docs) != 1 || !strings.HasSuffix(docs[0].Orig, ".snbt") {
		t.Fatalf("%+v", docs)
	}
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(docs[0].Orig))); err != nil {
		t.Fatal(err)
	}
}

// O mesmo ID de missão em dois capítulos (capítulo copiado) não pode dar dois
// textos com o mesmo ID: um sobrescreveria o outro e uma das duas cópias
// ficaria sem tradução.
func TestEra1InlineMesmoIDEmDoisArquivos(t *testing.T) {
	game := t.TempDir()
	body := func(title string) string {
		return `{id: "AA", title: "` + title + `", quests: [{id: "BB", title: "Same quest"}]}`
	}
	write(t, game, QuestsDir+"/chapters/a.snbt", body("Chapter A"))
	write(t, game, QuestsDir+"/chapters/b.snbt", body("Chapter B"))
	s := &sink{}
	var all []schema.StringRec
	_, err := Extract(game, s, Out{
		Str: func(r schema.StringRec) error { all = append(all, r); return nil },
		Doc: func(d schema.DocRec, b []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, r := range all {
		if prev, dup := ids[r.ID]; dup {
			t.Fatalf("o ID %s repete: %q e %q", r.ID, prev, r.Key)
		}
		ids[r.ID] = r.Key
	}
	// 2 títulos de capítulo + 2 títulos "Same quest" (um por arquivo), todos presentes
	if len(all) != 4 {
		t.Fatalf("%d textos: %+v", len(all), all)
	}
	docs := map[string]int{}
	for _, r := range all {
		docs[r.Loc.Doc]++
	}
	if docs[QuestsDir+"/chapters/a.snbt"] != 2 || docs[QuestsDir+"/chapters/b.snbt"] != 2 {
		t.Fatalf("cada arquivo devia ficar com os seus 2 textos: %v", docs)
	}
	if !strings.Contains(strings.Join(s.msgs, "|"), "mesmo ID de outro arquivo") {
		t.Fatalf("faltou o aviso: %v", s.msgs)
	}
}

const richChapter = `{
	id: "AAAA"
	quests: [{
		id: "BBBB"
		title: "Rich"
		description: [
			"[\"\",{\"text\":\"Hello \",\"color\":\"gold\"},{\"text\":\"world\",\"bold\":true,\"hoverEvent\":{\"action\":\"show_text\",\"contents\":{\"text\":\"Tooltip here\"}},\"clickEvent\":{\"action\":\"open_url\",\"value\":\"https://example.com\"}},\"plain tail\"]"
			"{\"text\":\"\",\"extra\":[{\"text\":\"Nested &6gold\"},{\"text\":\"  \"}]}"
			"{\"text\":\"\"}"
			"{\"broken\": "
		]
	}]
}
`

func TestRichJSON(t *testing.T) {
	game := t.TempDir()
	write(t, game, QuestsDir+"/chapters/rich.snbt", richChapter)
	strs, _, c, s := run(t, game)
	got := map[string]string{}
	for k, r := range strs {
		if r.Kind == "ftbqr" {
			got[r.Loc.Ptr] = r.En
			if !strings.Contains(k, ".rich.") {
				t.Errorf("chave sem .rich.: %s", k)
			}
		}
	}
	base := "/quests/0/description/"
	want := map[string]string{
		base + "0#/1/text":                     "Hello ",
		base + "0#/2/text":                     "world",
		base + "0#/2/hoverEvent/contents/text": "Tooltip here",
		base + "0#/3":                          "plain tail",
		base + "1#/extra/0/text":               "Nested &6gold",
	}
	if len(got) != len(want) {
		t.Fatalf("%d textos ricos: %v", len(got), got)
	}
	for p, en := range want {
		if got[p] != en {
			t.Errorf("%s: %q, queria %q", p, got[p], en)
		}
	}
	// a linha quebrada e a vazia contam como puladas, e nada de clickEvent/color vira texto
	if c.Skipped != 2 {
		t.Errorf("puladas: %d (%v)", c.Skipped, s.msgs)
	}
	for _, en := range got {
		if strings.Contains(en, "example.com") || en == "gold" {
			t.Errorf("texto indevido: %q", en)
		}
	}
	// o ponteiro de dentro do JSON acha o mesmo texto
	root, _ := relaxed.Parse([]byte(richChapter))
	for p := range want {
		outer, inner, _ := strings.Cut(p, "#")
		line := root.Get(outer)
		jn, err := relaxed.Parse([]byte(line.Str))
		if err != nil {
			t.Fatal(err)
		}
		if n := jn.Get(inner); n == nil || n.Kind != relaxed.Str {
			t.Errorf("%s não acha o texto", p)
		}
	}
}
