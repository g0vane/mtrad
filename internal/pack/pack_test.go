package pack

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/schema"
)

func readZip(t *testing.T, p string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	m := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		m[f.Name] = string(b)
	}
	return m
}

func TestMcMetaVariants(t *testing.T) {
	check := func(pf schema.PackFormat, want map[string]any) {
		b, err := McMeta(pf, "d")
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Pack map[string]any `json:"pack"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		if len(v.Pack) != len(want) {
			t.Fatalf("%+v: %v", pf, v.Pack)
		}
		for k, w := range want {
			g, _ := json.Marshal(v.Pack[k])
			e, _ := json.Marshal(w)
			if string(g) != string(e) {
				t.Errorf("%+v %s: %s != %s", pf, k, g, e)
			}
		}
	}
	check(schema.PackFormat{Major: 34}, map[string]any{"pack_format": 34, "description": "d"})
	check(schema.PackFormat{Major: 64}, map[string]any{"pack_format": 64, "description": "d"})
	check(schema.PackFormat{Major: 69}, map[string]any{"min_format": 69, "max_format": 69, "description": "d"})
	check(schema.PackFormat{Major: 88}, map[string]any{"min_format": 88, "max_format": 88, "description": "d"})
	check(schema.PackFormat{Major: 97, Minor: 1}, map[string]any{"min_format": []int{97, 1}, "max_format": []int{97, 1}, "description": "d"})
	if _, err := McMeta(schema.PackFormat{}, "d"); err == nil {
		t.Fatal("formato zero deveria falhar")
	}
}

func TestBuild(t *testing.T) {
	out := filepath.Join(t.TempDir(), "rp", "p.zip")
	reused := []schema.ReusedRec{{NS: "b", Key: "k1", Pt: "Um <b>"}, {NS: "a", Key: "k2", Pt: "Dois"}}
	ok := []schema.OKRec{{NS: "a", Key: "k3", Pt: "Três \"aspas\"\n§a"}, {NS: "b", Key: "k1", Pt: "Um novo"}}
	st, err := Build(out, schema.PackFormat{Major: 34}, "", reused, ok)
	if err != nil {
		t.Fatal(err)
	}
	if st.Namespaces != 2 || st.Keys != 3 || st.FromReused != 2 || st.FromOK != 2 {
		t.Fatalf("%+v", st)
	}
	files := readZip(t, out)
	if len(files) != 3 {
		t.Fatalf("%v", files)
	}
	var a map[string]string
	if err := json.Unmarshal([]byte(files["assets/a/lang/pt_br.json"]), &a); err != nil || a["k3"] != "Três \"aspas\"\n§a" || a["k2"] != "Dois" {
		t.Fatalf("%v %v", err, a)
	}
	var b map[string]string
	json.Unmarshal([]byte(files["assets/b/lang/pt_br.json"]), &b)
	if b["k1"] != "Um novo" {
		t.Fatalf("o texto novo substitui o reaproveitado: %v", b)
	}
	// mesma entrada, mesmos bytes
	out2 := filepath.Join(t.TempDir(), "p2.zip")
	if _, err := Build(out2, schema.PackFormat{Major: 34}, "", reused, ok); err != nil {
		t.Fatal(err)
	}
	h1, _ := schema.FileSHA256(out)
	h2, _ := schema.FileSHA256(out2)
	if h1 != h2 {
		t.Fatal("o pacote deveria ser reproduzível")
	}
	if _, err := Build(filepath.Join(t.TempDir(), "x.zip"), schema.PackFormat{Major: 34}, "", nil, nil); err == nil {
		t.Fatal("vazio deveria falhar")
	}
	if _, err := os.Stat(out + ".tmp"); err == nil {
		t.Fatal("sobrou arquivo temporário")
	}
}

func TestRun(t *testing.T) {
	work := t.TempDir()
	game := filepath.Join(t.TempDir(), "minecraft")
	os.MkdirAll(game, 0o755)
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game, PackFormat: schema.PackFormat{Major: 69}})
	rw, _ := schema.Create(filepath.Join(work, "reused.jsonl"), schema.VReused)
	rw.Write(schema.ReusedRec{NS: "m", Key: "k", Pt: "v"})
	rw.Close()
	ow, _ := schema.Create(filepath.Join(work, "ok.jsonl"), schema.VOK)
	ow.Write(schema.OKRec{NS: "m", Key: "k2", Pt: "v2"})
	ow.Close()
	st, p, err := Run(work, "", "")
	if err != nil || st.Keys != 2 || p != filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip") {
		t.Fatalf("%v %+v %s", err, st, p)
	}
	files := readZip(t, p)
	if !json.Valid([]byte(files["pack.mcmeta"])) || files["assets/m/lang/pt_br.json"] == "" {
		t.Fatalf("%v", files)
	}
}

const patchDoc = `{"name":"Intro","icon":"minecraft:book","pages":[{"type":"patchouli:text","text":"Hello $(br)world","title":"Hi"},{"type":"patchouli:text","text":"Second"}]}`
const patchOut = "assets/m/patchouli_books/g/pt_br/entries/a.json"
const ftbOut = "config/ftbquests/quests/lang/pt_br.snbt"
const ftbDoc = "{\n\t\"quest.A.title\": \"First\"\n\t\"quest.A.quest_desc\": [\n\t\t\"One\"\n\t\t\"Two\"\n\t]\n\tcount: 3\n}"

func docsFixture() []DocInput {
	return []DocInput{
		{Rec: schema.DocRec{ID: "d1", Kind: "patchouli", Mod: "m", Out: patchOut, Format: "json"}, Orig: []byte(patchDoc)},
		{Rec: schema.DocRec{ID: "d2", Kind: "ftbq", Mod: "ftbquests", Out: ftbOut, Format: "snbt"}, Orig: []byte(ftbDoc)},
	}
}

func TestAssembleDocs(t *testing.T) {
	pl, err := Assemble(Input{
		Reused: []schema.ReusedRec{
			{ID: "1", NS: "m", Key: "a", Pt: "Olá"},
			{ID: "2", NS: "patchouli:m/g", Key: "entries/a.json#/name", Pt: "Introdução", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/name"}},
		},
		OK: []schema.OKRec{
			{ID: "3", Pt: "Olá $(br)mundo", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/pages/0/text"}},
			{ID: "4", Pt: "Primeira", Kind: "ftbq", Loc: &schema.Loc{Doc: ftbOut, Ptr: "/quest.A.title"}},
			{ID: "5", Pt: "Dois", Kind: "ftbq", Loc: &schema.Loc{Doc: ftbOut, Ptr: "/quest.A.quest_desc/1"}},
		},
		Docs: docsFixture(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if pl.PatchouliDocs != 1 || pl.PatchouliKeys != 2 || pl.FTBFiles != 1 || pl.FTBKeys != 2 || pl.Keys != 1 || len(pl.Warnings) != 0 {
		t.Fatalf("%+v", pl.Stats)
	}
	zip := map[string]string{}
	for _, f := range pl.Zip {
		zip[f.Name] = string(f.Data)
	}
	var doc struct {
		Name  string `json:"name"`
		Icon  string `json:"icon"`
		Pages []struct {
			Type, Text, Title string
		} `json:"pages"`
	}
	if err := json.Unmarshal([]byte(zip[patchOut]), &doc); err != nil {
		t.Fatalf("%v\n%s", err, zip[patchOut])
	}
	if doc.Name != "Introdução" || doc.Icon != "minecraft:book" || doc.Pages[0].Text != "Olá $(br)mundo" || doc.Pages[0].Title != "Hi" || doc.Pages[1].Text != "Second" || doc.Pages[0].Type != "patchouli:text" {
		t.Fatalf("%+v", doc)
	}
	if zip["assets/m/lang/pt_br.json"] == "" {
		t.Fatal("falta o arquivo de idioma")
	}
	if len(pl.FTB) != 1 || pl.FTB[0].Name != ftbOut {
		t.Fatalf("%+v", pl.FTB)
	}
	out := string(pl.FTB[0].Data)
	for _, want := range []string{`"quest.A.title": "Primeira"`, `"One"`, `"Dois"`, `"count": 3`} {
		if !contains(out, want) {
			t.Errorf("falta %q em:\n%s", want, out)
		}
	}
	if contains(out, `"Two"`) || contains(out, `"First"`) {
		t.Errorf("sobrou inglês traduzido:\n%s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAssembleSkipsUntranslatedDocAndWarnsOrphans(t *testing.T) {
	pl, err := Assemble(Input{
		Reused: []schema.ReusedRec{
			{ID: "1", NS: "m", Key: "a", Pt: "Olá"},
			{ID: "2", Kind: "patchouli", Loc: &schema.Loc{Doc: "assets/x/patchouli_books/z/pt_br/entries/o.json", Ptr: "/name"}, Pt: "X"},
			{ID: "3", Kind: "weird", Key: "k", Pt: "Y"},
			{ID: "4", Kind: "ftbq", Key: "k"},
		},
		Docs: docsFixture(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// os dois documentos não têm nenhum texto traduzido: nada de cópia em inglês
	if pl.PatchouliDocs != 0 || pl.FTBFiles != 0 || len(pl.FTB) != 0 {
		t.Fatalf("%+v", pl.Stats)
	}
	if len(pl.Warnings) != 3 {
		t.Fatalf("%v", pl.Warnings)
	}
}

func TestAssembleBadPointerIsError(t *testing.T) {
	_, err := Assemble(Input{
		OK:   []schema.OKRec{{ID: "1", Pt: "x", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/pages/9/text"}}},
		Docs: docsFixture(),
	})
	if err == nil {
		t.Fatal("caminho que sumiu do original devia dar erro")
	}
}

func TestAssembleEmpty(t *testing.T) {
	if _, err := Assemble(Input{}); err == nil {
		t.Fatal("devia falhar sem nada")
	}
}

func TestAssembleDeterministic(t *testing.T) {
	in := Input{
		OK: []schema.OKRec{
			{ID: "3", Pt: "A", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/pages/0/text"}},
			{ID: "1", Pt: "B", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/name"}},
		},
		Docs: docsFixture(),
	}
	a, _ := Assemble(in)
	b, _ := Assemble(in)
	if string(a.Zip[0].Data) != string(b.Zip[0].Data) {
		t.Fatal("saída muda de uma vez para outra")
	}
}

func TestRunWithDocs(t *testing.T) {
	work := t.TempDir()
	game := t.TempDir()
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game, PackFormat: schema.PackFormat{Major: 34}})
	// documentos
	os.MkdirAll(filepath.Join(work, "docs", "patchouli"), 0o755)
	os.MkdirAll(filepath.Join(work, "docs", "ftbq"), 0o755)
	os.WriteFile(filepath.Join(work, "docs", "patchouli", "d1.json"), []byte(patchDoc), 0o644)
	os.WriteFile(filepath.Join(work, "docs", "ftbq", "d2.snbt"), []byte(ftbDoc), 0o644)
	dp, _ := schema.Create(filepath.Join(work, "docs.patchouli.jsonl"), schema.VDocs)
	dp.Write(schema.DocRec{ID: "d1", Kind: "patchouli", Mod: "m", Orig: "docs/patchouli/d1.json", Out: patchOut, Format: "json"})
	dp.Close()
	df, _ := schema.Create(filepath.Join(work, "docs.ftbq.jsonl"), schema.VDocs)
	df.Write(schema.DocRec{ID: "d2", Kind: "ftbq", Mod: "ftbquests", Orig: "docs/ftbq/d2.snbt", Out: ftbOut, Format: "snbt"})
	df.Close()
	rw, _ := schema.Create(filepath.Join(work, "reused.jsonl"), schema.VReused)
	rw.Write(schema.ReusedRec{ID: "2", NS: "patchouli:m/g", Key: "k", Pt: "Introdução", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/name"}})
	rw.Close()
	ow, _ := schema.Create(filepath.Join(work, "ok.jsonl"), schema.VOK)
	ow.Write(schema.OKRec{ID: "4", Pt: "Primeira", Kind: "ftbq", Loc: &schema.Loc{Doc: ftbOut, Ptr: "/quest.A.title"}})
	ow.Close()

	st, p, err := Run(work, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip") || st.PatchouliDocs != 1 || st.FTBFiles != 1 {
		t.Fatalf("%s %+v", p, st)
	}
	z := readZip(t, p)
	if z["pack.mcmeta"] == "" || z[patchOut] == "" || len(z) != 2 {
		t.Fatalf("%v", z)
	}
	b, err := os.ReadFile(filepath.Join(FTBOutDir(work), filepath.FromSlash(ftbOut)))
	if err != nil || !contains(string(b), "Primeira") {
		t.Fatalf("%v %s", err, b)
	}
	// só FTB: não monta zip
	os.Remove(filepath.Join(work, "docs.patchouli.jsonl"))
	rw, _ = schema.Create(filepath.Join(work, "reused.jsonl"), schema.VReused)
	rw.Close()
	os.Remove(p)
	st, p, err = Run(work, "", "")
	if err != nil || p != "" || st.FTBFiles != 1 {
		t.Fatalf("%v %q %+v", err, p, st)
	}
	if _, err := os.Stat(filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")); err == nil {
		t.Fatal("zip não devia existir")
	}
}

func TestFormatoAntigoGravaLang(t *testing.T) {
	for _, tc := range []struct {
		major int
		file  string
	}{{1, "pt_BR.lang"}, {2, "pt_BR.lang"}, {3, "pt_br.lang"}, {4, ""}, {34, ""}} {
		if got := LegacyLangName(schema.PackFormat{Major: tc.major}); got != tc.file {
			t.Errorf("formato %d: %q, queria %q", tc.major, got, tc.file)
		}
	}
	out := filepath.Join(t.TempDir(), "p.zip")
	reused := []schema.ReusedRec{{NS: "oldmod", Key: "tile.rock.name", Pt: "Rocha"}}
	ok := []schema.OKRec{{NS: "oldmod", Key: "item.gem.name", Pt: "Gema\nbrilhante"}}
	if _, err := Build(out, schema.PackFormat{Major: 3}, "", reused, ok); err != nil {
		t.Fatal(err)
	}
	files := readZip(t, out)
	if got := files["assets/oldmod/lang/pt_br.lang"]; got != "tile.rock.name=Rocha\nitem.gem.name=Gema\\nbrilhante\n" {
		t.Fatalf("%q (%v)", got, files)
	}
	if _, has := files["assets/oldmod/lang/pt_br.json"]; has {
		t.Fatal("no formato antigo não pode haver .json")
	}
	if !strings.Contains(files["pack.mcmeta"], `"pack_format": 3`) {
		t.Fatal(files["pack.mcmeta"])
	}
}

// verifyFixture monta uma pasta de trabalho com um documento do Patchouli e um
// arquivo de idioma, roda o pack e devolve a pasta, o zip e as strings.
func verifyFixture(t *testing.T) (work, zipPath string) {
	t.Helper()
	work = t.TempDir()
	game := t.TempDir()
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game, PackFormat: schema.PackFormat{Major: 34}})
	os.MkdirAll(filepath.Join(work, "docs", "patchouli"), 0o755)
	os.WriteFile(filepath.Join(work, "docs", "patchouli", "d1.json"), []byte(patchDoc), 0o644)
	dp, _ := schema.Create(filepath.Join(work, "docs.patchouli.jsonl"), schema.VDocs)
	dp.Write(schema.DocRec{ID: "d1", Kind: "patchouli", Mod: "m", Orig: "docs/patchouli/d1.json", Out: patchOut, Format: "json"})
	dp.Close()
	// as strings extraídas dizem quais caminhos do documento são texto
	sp, _ := schema.Create(filepath.Join(work, "strings.patchouli.jsonl"), schema.VStrings)
	for _, ptr := range []string{"/name", "/pages/0/text", "/pages/0/title", "/pages/1/text"} {
		sp.Write(schema.StringRec{ID: ptr, NS: "patchouli:m/g", Key: ptr, En: "x", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: ptr}})
	}
	sp.Close()
	sl, _ := schema.Create(filepath.Join(work, "strings.jsonl"), schema.VStrings)
	sl.Write(schema.StringRec{ID: "a", NS: "m", Key: "k", En: "x"})
	sl.Close()
	rw, _ := schema.Create(filepath.Join(work, "reused.jsonl"), schema.VReused)
	rw.Write(schema.ReusedRec{ID: "1", NS: "m", Key: "k", Pt: "v"})
	rw.Write(schema.ReusedRec{ID: "2", NS: "patchouli:m/g", Key: "k", Pt: "Introdução", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/name"}})
	rw.Close()
	ow, _ := schema.Create(filepath.Join(work, "ok.jsonl"), schema.VOK)
	ow.Write(schema.OKRec{ID: "3", Pt: "Olá $(br)mundo", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/pages/0/text"}})
	ow.Close()
	for _, s := range []struct{ stage, f string }{{"extract", "strings.jsonl"}, {"patchouli", "docs.patchouli.jsonl"}, {"diff", "reused.jsonl"}, {"validate", "ok.jsonl"}} {
		if err := schema.WriteDone(work, s.stage, nil, []string{s.f}, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, p, err := Run(work, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return work, p
}

func TestVerifyAcceptsAFreshPack(t *testing.T) {
	work, p := verifyFixture(t)
	rep, err := Verify(work, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 0 || rep.Docs != 1 || rep.LangFiles != 1 || rep.Keys != 1 || rep.Zip != p {
		t.Fatalf("%+v", rep)
	}
}

// rewriteZip troca o conteúdo de uma entrada do zip (ou acrescenta uma).
func rewriteZip(t *testing.T, p, name, content string) {
	t.Helper()
	files := readZip(t, p)
	files[name] = content
	var out []File
	for n, c := range files {
		if n != "pack.mcmeta" {
			out = append(out, File{n, []byte(c)})
		}
	}
	if err := WriteZip(p, schema.PackFormat{Major: 34}, "", out); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCatchesDamage(t *testing.T) {
	cases := []struct {
		name, file, content, want string
	}{
		{"campo que não é texto mudou", patchOut, `{"name":"Introdução","icon":"minecraft:stick","pages":[{"type":"patchouli:text","text":"Olá","title":"Hi"},{"type":"patchouli:text","text":"Second"}]}`, "/icon"},
		{"página a menos", patchOut, `{"name":"Introdução","icon":"minecraft:book","pages":[{"type":"patchouli:text","text":"Olá","title":"Hi"}]}`, "devia ter 2"},
		{"tipo de página trocado", patchOut, `{"name":"Introdução","icon":"minecraft:book","pages":[{"type":"patchouli:entity","text":"Olá","title":"Hi"},{"type":"patchouli:text","text":"Second"}]}`, "/pages/0/type"},
		{"texto virou objeto", patchOut, `{"name":{"a":1},"icon":"minecraft:book","pages":[{"type":"patchouli:text","text":"Olá","title":"Hi"},{"type":"patchouli:text","text":"Second"}]}`, "o tipo mudou"},
		{"JSON quebrado", patchOut, `{"name":`, "não abre como JSON"},
		{"chave de idioma inventada", "assets/m/lang/pt_br.json", `{"k":"v","inventada":"x"}`, "não existe no original"},
		{"valor de idioma que não é texto", "assets/m/lang/pt_br.json", `{"k":3}`, "não tem texto"},
		{"caminho perigoso", "assets/../../fora.txt", "x", "caminho suspeito"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, p := verifyFixture(t)
			rewriteZip(t, p, c.file, c.content)
			rep, err := Verify(work, "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(rep.Problems, "\n"), c.want) {
				t.Fatalf("queria um problema com %q, veio %q", c.want, rep.Problems)
			}
		})
	}
}

func TestVerifyWrongPackFormat(t *testing.T) {
	work, p := verifyFixture(t)
	files := readZip(t, p)
	var out []File
	for n, c := range files {
		if n != "pack.mcmeta" {
			out = append(out, File{n, []byte(c)})
		}
	}
	if err := WriteZip(p, schema.PackFormat{Major: 15}, "", out); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(work, "")
	if err != nil || !strings.Contains(strings.Join(rep.Problems, "\n"), "pack.mcmeta com formato") {
		t.Fatalf("%v %q", err, rep.Problems)
	}
}

func TestVerifyMissingZip(t *testing.T) {
	work, p := verifyFixture(t)
	os.Remove(p)
	if _, err := Verify(work, ""); err == nil || !strings.Contains(err.Error(), "não consegui abrir o pacote") {
		t.Fatalf("%v", err)
	}
}

// Nomes tirados de jars não são de confiança: um namespace ".." ou um caminho
// de documento com ".." não pode virar entrada do zip.
func TestAssembleRejectsTraversalNames(t *testing.T) {
	evilDoc := "assets/m/patchouli_books/g/pt_br/entries/../../../../../evil.json"
	docs := append(docsFixture(), DocInput{
		Rec:  schema.DocRec{ID: "d3", Kind: "patchouli", Mod: "m", Out: evilDoc, Format: "json"},
		Orig: []byte(patchDoc),
	})
	pl, err := Assemble(Input{
		Reused: []schema.ReusedRec{
			{ID: "1", NS: "m", Key: "a", Pt: "Olá"},
			{ID: "2", NS: "..", Key: "b", Pt: "Mal"},
			{ID: "3", NS: "a/../../b", Key: "c", Pt: "Mal"},
		},
		OK: []schema.OKRec{
			{ID: "4", Pt: "Olá $(br)mundo", Kind: "patchouli", Loc: &schema.Loc{Doc: patchOut, Ptr: "/pages/0/text"}},
			{ID: "5", Pt: "Mal", Kind: "patchouli", Loc: &schema.Loc{Doc: evilDoc, Ptr: "/pages/0/text"}},
		},
		Docs: docs,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range pl.Zip {
		names[f.Name] = true
		if !schema.SafeEntryName(f.Name) {
			t.Errorf("entrada perigosa no pacote: %q", f.Name)
		}
	}
	if !names["assets/m/lang/pt_br.json"] || !names[patchOut] {
		t.Fatalf("os arquivos bons sumiram: %v", names)
	}
	if len(pl.Warnings) < 2 {
		t.Errorf("devia avisar dos nomes suspeitos: %v", pl.Warnings)
	}
}

func TestWriteFTBRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out", "ftbquests")
	for _, name := range []string{"../x.snbt", "a/../../x.snbt", ".."} {
		if err := WriteFTB(dir, []File{{Name: name, Data: []byte("x")}}); err == nil {
			t.Errorf("%q devia ser recusado", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "out", "x.snbt")); err == nil {
		t.Fatal("arquivo escapou da pasta de saída")
	}
	// nomes que apenas começam com ".." são legítimos
	if err := WriteFTB(dir, []File{{Name: "..hidden/q.snbt", Data: []byte("x")}}); err != nil {
		t.Fatalf("nome válido recusado: %v", err)
	}
}
