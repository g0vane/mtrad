package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mtrad/internal/ftbq"
	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

const inlineChapter = `{
	id: "5DDF8D6C7D2BA17D"
	filename: "getting_started"
	title: "Getting Started"
	images: [{
		x: 1.0d
		image: "ftbquests:textures/logo.png"
		hover: ["Click me"]
	}]
	quests: [{
		title: "Welcome"
		description: [
			"First line with &#FF8800color&r and 100% fun"
			""
			"{@pagebreak}"
			"{\"text\":\"rich\",\"color\":\"red\"}"
		]
		id: "0000000000000001"
		tasks: [{
			id: "0000000000000002"
			type: "item"
			item: {
				id: "minecraft:oak_log"
				Count: 1b
				tag: {
					title: "Do not touch"
				}
			}
		}]
	}]
}
`

// Do capítulo inline ao pack: extrai, "traduz", monta e confere que o capítulo
// novo só troca os textos por {chave}, que o lang do pack tem as mesmas chaves
// e que o "%" sai dobrado.
func TestInlineFTBRoundTrip(t *testing.T) {
	game := t.TempDir()
	rel := ftbq.QuestsDir + "/chapters/getting_started.snbt"
	p := filepath.Join(game, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(inlineChapter), 0o644); err != nil {
		t.Fatal(err)
	}
	var ok []schema.OKRec
	var docs []DocInput
	var errs nopSink
	c, err := ftbq.Extract(game, errs, ftbq.Out{
		Str: func(r schema.StringRec) error {
			ok = append(ok, schema.OKRec{ID: r.ID, Mod: r.Mod, NS: r.NS, Key: r.Key, Pt: "PT " + r.En, Kind: r.Kind, Loc: r.Loc})
			return nil
		},
		Doc: func(d schema.DocRec, b []byte) error { docs = append(docs, DocInput{Rec: d, Orig: b}); return nil },
	})
	if err != nil || c.Era != "inline" || len(ok) != 5 || len(docs) != 1 {
		t.Fatalf("%v %+v %d %d", err, c, len(ok), len(docs))
	}
	pl, err := Assemble(Input{OK: ok, Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	if pl.FTBFiles != 1 || pl.FTBKeys != 5 || len(pl.Warnings) != 0 {
		t.Fatalf("%+v", pl.Stats)
	}
	zip := map[string]string{}
	for _, f := range pl.Zip {
		zip[f.Name] = string(f.Data)
	}
	var pt, en map[string]string
	if err := json.Unmarshal([]byte(zip["assets/mtrad_ftbq/lang/pt_br.json"]), &pt); err != nil {
		t.Fatalf("pt_br.json: %v (%v)", err, zip)
	}
	if err := json.Unmarshal([]byte(zip["assets/mtrad_ftbq/lang/en_us.json"]), &en); err != nil {
		t.Fatalf("en_us.json: %v", err)
	}
	k := "mtrad.ftbq.0000000000000001.description.0"
	if pt[k] != "PT First line with &#FF8800color&r and 100%% fun" || en[k] != "First line with &#FF8800color&r and 100%% fun" {
		t.Fatalf("pt=%q en=%q", pt[k], en[k])
	}
	if len(pt) != 4 || len(en) != 4 {
		t.Fatalf("pt=%v en=%v", pt, en)
	}
	// o capítulo novo
	var chap []byte
	for _, f := range pl.FTB {
		if f.Name == rel {
			chap = f.Data
		}
	}
	if chap == nil {
		t.Fatalf("capítulo não saiu: %v", pl.FTB)
	}
	n, err := relaxed.Parse(chap)
	if err != nil {
		t.Fatalf("o capítulo novo não abre: %v\n%s", err, chap)
	}
	get := func(ptr string) string {
		x := n.Get(ptr)
		if x == nil {
			t.Fatalf("sem %s\n%s", ptr, chap)
		}
		if x.Kind == relaxed.Raw {
			return x.Raw
		}
		return x.Str
	}
	if got := get("/title"); got != "{mtrad.ftbq.5ddf8d6c7d2ba17d.title}" {
		t.Errorf("title: %q", got)
	}
	if got := get("/quests/0/description/0"); got != "{"+k+"}" {
		t.Errorf("desc0: %q", got)
	}
	// o que não é texto traduzível fica como estava
	for ptr, want := range map[string]string{
		"/quests/0/description/1":          "",
		"/quests/0/description/2":          "{@pagebreak}",
		"/quests/0/description/3":          `{"text":"PT rich","color":"red"}`, // JSON rico: o texto volta para dentro da linha, sem passar pelo lang
		"/quests/0/tasks/0/item/tag/title": "Do not touch",
		"/quests/0/tasks/0/item/Count":     "1b",
		"/images/0/x":                      "1.0d",
		"/images/0/image":                  "ftbquests:textures/logo.png",
	} {
		if got := get(ptr); got != want {
			t.Errorf("%s: %q, queria %q", ptr, got, want)
		}
	}
	// toda {chave} do capítulo existe no lang do pack, e vice-versa
	refs := regexp.MustCompile(`\{(mtrad\.ftbq\.[a-z0-9_.]+)\}`).FindAllStringSubmatch(string(chap), -1)
	if len(refs) != 4 {
		t.Fatalf("refs: %v", refs)
	}
	for _, r := range refs {
		if _, ok := pt[r[1]]; !ok || !strings.HasPrefix(r[1], ftbq.KeyPrefix) {
			t.Errorf("chave %s sem tradução no pack", r[1])
		}
		if _, ok := en[r[1]]; !ok {
			t.Errorf("chave %s sem inglês no pack", r[1])
		}
	}
}

type nopSink struct{}

func (nopSink) Add(string, string, string) {}

func TestWriteFTBFailureKeepsPreviousOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out", "ftbquests")
	if err := WriteFTB(dir, []File{{Name: "config/a.snbt", Data: []byte("v1")}}); err != nil {
		t.Fatal(err)
	}
	// segunda geração com um caminho inválido no meio: erro e a saída antiga fica
	err := WriteFTB(dir, []File{{Name: "config/a.snbt", Data: []byte("v2")}, {Name: "../fora.snbt", Data: []byte("x")}})
	if err == nil {
		t.Fatal("deveria recusar o caminho fora da pasta")
	}
	b, rerr := os.ReadFile(filepath.Join(dir, "config", "a.snbt"))
	if rerr != nil || string(b) != "v1" {
		t.Fatalf("a saída anterior foi perdida: %q %v", b, rerr)
	}
	if _, serr := os.Stat(dir + ".new"); serr == nil {
		t.Fatal("a pasta temporária deveria ser removida")
	}
	// terceira geração boa troca a saída e remove arquivos que não existem mais
	if err := WriteFTB(dir, []File{{Name: "config/b.snbt", Data: []byte("v3")}}); err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "config", "a.snbt")); serr == nil {
		t.Fatal("arquivo obsoleto deveria sumir")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config", "b.snbt")); string(b) != "v3" {
		t.Fatalf("%q", b)
	}
	if _, serr := os.Stat(dir + ".old"); serr == nil {
		t.Fatal("a pasta .old deveria ser removida")
	}
}
