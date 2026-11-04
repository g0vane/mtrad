package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mtrad/internal/install"
	"mtrad/internal/pack"
	"mtrad/internal/route"
	"mtrad/internal/schema"
	"mtrad/internal/testutil"
	"mtrad/internal/translate"
)

func TestInitConfigsLoadBack(t *testing.T) {
	for _, tc := range []struct{ engine, model string }{
		{"llama", ""}, {"llama", "x"}, {"ollama", "m:1"}, {"gemini", "g"}, {"libre", ""}, {"ct2", ""},
	} {
		c, _, err := initConfig(tc.engine, tc.model)
		if err != nil {
			t.Fatalf("%s: %v", tc.engine, err)
		}
		b, _ := json.Marshal(c)
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, b, 0o644)
		back, err := loadConfig(p) // recusa campos desconhecidos
		if err != nil {
			t.Fatalf("%s: %v\n%s", tc.engine, err, b)
		}
		if _, ok := back.Translate.Engines[back.Route.Default]; !ok {
			t.Fatalf("%s: rota aponta para motor que não existe", tc.engine)
		}
		if err := back.Route.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []string{"ollama", "gemini"} {
		if _, _, err := initConfig(e, ""); err == nil {
			t.Errorf("%s sem --model devia dar erro", e)
		}
	}
	if _, _, err := initConfig("nada", ""); err == nil {
		t.Error("motor desconhecido devia dar erro")
	}
}

func TestLoadConfigRejectsUnknownSource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"sources":["lang","bogus"],"route":{"default":"e"},"translate":{"engines":{"e":{"type":"echo"}}}}`), 0o644)
	if _, err := loadConfig(p); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("%v", err)
	}
	srcs, err := enabledSources([]string{"ftbq", "lang"})
	if err != nil || strings.Join(srcs, ",") != "lang,ftbq" {
		t.Fatalf("%v %v", srcs, err)
	}
}

func TestInitWritesFileAndRefusesOverwrite(t *testing.T) {
	inst := t.TempDir()
	os.WriteFile(filepath.Join(inst, "mmc-pack.json"), []byte(`{"components":[{"uid":"net.minecraft","version":"1.21.1"},{"uid":"net.fabricmc.fabric-loader","version":"0.16.5"}],"formatVersion":1}`), 0o644)
	os.MkdirAll(filepath.Join(inst, "minecraft"), 0o755)
	out := filepath.Join(t.TempDir(), "sub", "mtrad.json")
	if err := stageInit(inst, out, "llama", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(out); err != nil {
		t.Fatal(err)
	}
	if err := stageInit(inst, out, "llama", "", false); err == nil {
		t.Fatal("devia recusar sobrescrever")
	}
	if err := stageInit(inst, out, "llama", "outro", true); err != nil {
		t.Fatal(err)
	}
	if err := stageInit(filepath.Join(inst, "nao-existe"), filepath.Join(t.TempDir(), "x.json"), "llama", "", false); err == nil {
		t.Fatal("instância inexistente devia dar erro")
	}
}

// ---- ponta a ponta ----

func fakeInstance(t *testing.T) (root, inst, game string) {
	t.Helper()
	root = t.TempDir()
	inst = filepath.Join(root, "PrismLauncher", "instances", "teste")
	game = filepath.Join(inst, "minecraft")
	os.MkdirAll(filepath.Join(game, "mods"), 0o755)
	os.WriteFile(filepath.Join(inst, "mmc-pack.json"), []byte(`{"components":[{"uid":"net.minecraft","version":"1.21.1"},{"uid":"net.fabricmc.fabric-loader","version":"0.16.5"}],"formatVersion":1}`), 0o644)
	jar := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":                                                 `{"id":"mymod","version":"1.0"}`,
		"assets/mymod/lang/en_us.json":                                    `{"item.mymod.gem":"Shiny Gem","msg.hello":"Hello %s, welcome","msg.done":"Already translated"}`,
		"assets/mymod/lang/pt_br.json":                                    `{"msg.done":"Já traduzido"}`,
		"data/mymod/patchouli_books/guide/book.json":                      `{"name":"Guide","use_resource_pack":true}`,
		"assets/mymod/patchouli_books/guide/en_us/entries/intro.json":     `{"name":"Introduction","category":"mymod:basics","pages":[{"type":"patchouli:text","text":"Welcome to the $(l:mymod:intro)guide$(/l).$(br)Enjoy!"}]}`,
		"assets/mymod/patchouli_books/guide/en_us/categories/basics.json": `{"name":"Basics","description":"The basic things."}`,
	})
	os.WriteFile(filepath.Join(game, "mods", "mymod.jar"), jar, 0o644)
	q := filepath.Join(game, "config", "ftbquests", "quests", "lang")
	os.MkdirAll(q, 0o755)
	os.WriteFile(filepath.Join(q, "en_us.snbt"), []byte("{\n\t\"chapter.AAAA.title\": \"Getting Started\"\n\t\"quest.BBBB.title\": \"&aFirst quest\"\n\t\"quest.BBBB.quest_desc\": [\n\t\t\"Collect a gem.\"\n\t\t\"\"\n\t\t\"{image:ftbquests:textures/a.png}\"\n\t]\n}"), 0o644)
	return root, inst, game
}

func echoConfig() Config {
	var c Config
	c.Route = route.Config{Default: "eco"}
	c.Translate = translate.Config{Engines: map[string]translate.EngineConfig{"eco": {Type: "echo"}}, BackoffMs: 1}
	return c
}

func readZipFile(t *testing.T, zp, name string) (string, bool) {
	t.Helper()
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b), true
		}
	}
	return "", false
}

func TestEndToEndAllSources(t *testing.T) {
	root, inst, game := fakeInstance(t)
	work := filepath.Join(root, "work")
	// o motor echo devolve "[pt] " + o texto: serve para conferir o caminho dos arquivos
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{Install: true}); err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	lang, ok := readZipFile(t, zp, "assets/mymod/lang/pt_br.json")
	if !ok {
		t.Fatal("falta o idioma no zip")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(lang), &m); err != nil || m["msg.done"] != "Já traduzido" || m["item.mymod.gem"] != "[pt] Shiny Gem" || m["msg.hello"] != "[pt] Hello %s, welcome" || len(m) != 3 {
		t.Fatalf("%v %v", err, m)
	}
	entry, ok := readZipFile(t, zp, "assets/mymod/patchouli_books/guide/pt_br/entries/intro.json")
	if !ok || !strings.Contains(entry, `[pt] Welcome to the $(l:mymod:intro)guide$(/l).$(br)Enjoy!`) || !strings.Contains(entry, `"category": "mymod:basics"`) {
		t.Fatalf("%v\n%s", ok, entry)
	}
	if _, ok := readZipFile(t, zp, "assets/mymod/patchouli_books/guide/pt_br/categories/basics.json"); !ok {
		t.Fatal("falta a categoria")
	}
	// FTB Quests instalado na pasta do jogo (--install)
	got, err := os.ReadFile(filepath.Join(game, "config", "ftbquests", "quests", "lang", "pt_br.snbt"))
	if err != nil || !strings.Contains(string(got), `"quest.BBBB.title": "[pt] &aFirst quest"`) || !strings.Contains(string(got), "{image:ftbquests:textures/a.png}") {
		t.Fatalf("%v\n%s", err, got)
	}
	// o original do FTB Quests não foi tocado
	if b, _ := os.ReadFile(filepath.Join(game, "config", "ftbquests", "quests", "lang", "en_us.snbt")); !strings.Contains(string(b), "\"Collect a gem.\"\n") {
		t.Fatal("en_us.snbt foi alterado")
	}
	// todos os textos têm marcas conferidas: nenhum reprovado
	ok2, _ := schema.ReadAll[schema.OKRec](filepath.Join(work, "ok.jsonl"), schema.VOK)
	kinds := map[string]int{}
	for _, r := range ok2 {
		kinds[r.Kind]++
	}
	if kinds[""] != 2 || kinds["patchouli"] != 4 || kinds["ftbq"] != 3 {
		t.Fatalf("%v", kinds)
	}
	// segunda execução: nada muda e o install não sobrescreve (arquivo igual)
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{Install: true}); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEndSourcesOffRemovesStaleFiles(t *testing.T) {
	root, inst, game := fakeInstance(t)
	work := filepath.Join(root, "work")
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "docs.patchouli.jsonl")); err != nil {
		t.Fatal(err)
	}
	cfg := echoConfig()
	cfg.Sources = []string{"lang"}
	if err := runAll(context.Background(), work, inst, "", cfg, runOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"strings.patchouli.jsonl", "docs.patchouli.jsonl", "patchouli.done.json", "strings.ftbq.jsonl", "docs.ftbq.jsonl", "ftbq.done.json"} {
		if _, err := os.Stat(filepath.Join(work, f)); err == nil {
			t.Errorf("%s devia ter sido removido", f)
		}
	}
	zp := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	if _, ok := readZipFile(t, zp, "assets/mymod/patchouli_books/guide/pt_br/entries/intro.json"); ok {
		t.Fatal("o zip ainda tem o Patchouli")
	}
	if _, err := os.Stat(filepath.Join(work, "out", "ftbquests")); err == nil {
		if ents, _ := os.ReadDir(filepath.Join(work, "out", "ftbquests")); len(ents) != 0 {
			t.Fatal("work/out/ftbquests devia estar vazio")
		}
	}
}

func TestEndToEndOnlyFTB(t *testing.T) {
	// instância sem nenhum arquivo de idioma de mod: só FTB Quests
	root, inst, game := fakeInstance(t)
	os.Remove(filepath.Join(game, "mods", "mymod.jar"))
	work := filepath.Join(root, "work")
	cfg := echoConfig()
	if err := runAll(context.Background(), work, inst, "", cfg, runOpts{Install: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")); err == nil {
		t.Fatal("sem textos para o zip, ele não devia existir")
	}
	if _, err := os.Stat(filepath.Join(game, "config", "ftbquests", "quests", "lang", "pt_br.snbt")); err != nil {
		t.Fatal(err)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := loadConfig("../../mtrad.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Route.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Translate.Engines[c.Route.Default]; !ok {
		t.Fatal("a rota padrão aponta para motor que não existe")
	}
}

func TestGlossaryVanilla(t *testing.T) {
	// isola do Prism real da máquina (o localizador também olha em $HOME)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root, inst, _ := fakeInstance(t)
	work := filepath.Join(root, "work")
	// dados falsos do Prism ao lado de instances/
	data := filepath.Join(root, "PrismLauncher")
	h := strings.Repeat("ab", 20)
	os.MkdirAll(filepath.Join(data, "assets", "indexes"), 0o755)
	os.MkdirAll(filepath.Join(data, "assets", "objects", h[:2]), 0o755)
	os.WriteFile(filepath.Join(data, "assets", "indexes", "17.json"), []byte(`{"objects":{"minecraft/lang/pt_br.json":{"hash":"`+h+`"}}}`), 0o644)
	os.WriteFile(filepath.Join(data, "assets", "objects", h[:2], h), []byte(`{"item.minecraft.diamond":"Diamante"}`), 0o644)
	jar := filepath.Join(data, "libraries", "com", "mojang", "minecraft", "1.21.1", "minecraft-1.21.1-client.jar")
	os.MkdirAll(filepath.Dir(jar), 0o755)
	os.WriteFile(jar, testutil.MakeJar(t, map[string]string{"assets/minecraft/lang/en_us.json": `{"item.minecraft.diamond":"Diamond"}`}), 0o644)

	if err := stageVersion(work, inst, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// o glossário manual ganha do vanilla
	tsv := filepath.Join(root, "g.tsv")
	os.WriteFile(tsv, []byte("Diamond\tDiamante raro\n"), 0o644)
	if err := stageGlossary(work, glossaryArgs{Vanilla: true, TSV: []string{tsv}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(work, "glossary.json"))
	if err != nil || !strings.Contains(string(b), "Diamante raro") {
		t.Fatalf("%v\n%s", err, b)
	}
	// sem os arquivos do Minecraft e sem TSV: erro claro em vez de glossário vazio
	os.RemoveAll(data)
	if err := stageGlossary(work, glossaryArgs{Vanilla: true}); err == nil {
		t.Fatal("devia falhar sem achar nada")
	}
	// sem os arquivos, mas com TSV: segue só com o TSV
	if err := stageGlossary(work, glossaryArgs{Vanilla: true, TSV: []string{tsv}}); err != nil {
		t.Fatal(err)
	}
}

// Modpack Forge 1.20.1 (como o Reclamation): mods.toml, mod dentro de mod
// (JarJar) e missões do FTB Quests com o texto dentro do capítulo.
func TestEndToEndForgeInlineQuests(t *testing.T) {
	root := t.TempDir()
	inst := filepath.Join(root, "PrismLauncher", "instances", "forge")
	game := filepath.Join(inst, "minecraft")
	os.MkdirAll(filepath.Join(game, "mods"), 0o755)
	os.WriteFile(filepath.Join(inst, "mmc-pack.json"), []byte(`{"components":[{"uid":"net.minecraft","version":"1.20.1"},{"uid":"net.minecraftforge","version":"47.3.0"}],"formatVersion":1}`), 0o644)
	inner := testutil.MakeJar(t, map[string]string{
		"META-INF/mods.toml":              "[[mods]]\nmodId=\"innerlib\"\nversion=\"1\"\n",
		"assets/innerlib/lang/en_us.json": `{"innerlib.hello":"Hello inner"}`,
	})
	outer := testutil.MakeJar(t, map[string]string{
		"META-INF/mods.toml":            "modLoader=\"javafml\"\n[[mods]]\nmodId=\"bigmod\"\nversion=\"1.0\"\n",
		"META-INF/jarjar/metadata.json": `{"jars":[{"path":"META-INF/jarjar/inner.jar"}]}`,
		"META-INF/jarjar/inner.jar":     string(inner),
		"assets/bigmod/lang/en_us.json": `{"item.bigmod.gem":"Shiny Gem"}`,
	})
	os.WriteFile(filepath.Join(game, "mods", "bigmod.jar"), outer, 0o644)
	qdir := filepath.Join(game, "config", "ftbquests", "quests", "chapters")
	os.MkdirAll(qdir, 0o755)
	chapter := "{\n\tid: \"5DDF8D6C7D2BA17D\"\n\ttitle: \"Getting Started\"\n\tquests: [{\n\t\tid: \"0000000000000001\"\n\t\ttitle: \"&#FF8800Welcome&r\"\n\t\tdescription: [\"50% done\", \"{@pagebreak}\"]\n\t\ttasks: [{id: \"0000000000000002\", type: \"item\", item: {id: \"minecraft:oak_log\", Count: 1b}}]\n\t}]\n}\n"
	os.WriteFile(filepath.Join(qdir, "start.snbt"), []byte(chapter), 0o644)

	work := filepath.Join(root, "work")
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{}); err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	meta, ok := readZipFile(t, zp, "pack.mcmeta")
	if !ok || !strings.Contains(meta, `"pack_format": 15`) {
		t.Fatalf("pack.mcmeta: %v %s", ok, meta)
	}
	for _, n := range []string{"assets/bigmod/lang/pt_br.json", "assets/innerlib/lang/pt_br.json"} {
		if _, ok := readZipFile(t, zp, n); !ok {
			t.Errorf("falta %s (o mod embutido tem de ser lido)", n)
		}
	}
	pt, ok := readZipFile(t, zp, "assets/mtrad_ftbq/lang/pt_br.json")
	if !ok || !strings.Contains(pt, `"mtrad.ftbq.0000000000000001.description.0": "[pt] 50%% done"`) || !strings.Contains(pt, "&#FF8800Welcome&r") {
		t.Fatalf("%v\n%s", ok, pt)
	}
	if _, ok := readZipFile(t, zp, "assets/mtrad_ftbq/lang/en_us.json"); !ok {
		t.Fatal("falta o inglês das chaves das missões")
	}
	// o capítulo original continua igual até o install
	if b, _ := os.ReadFile(filepath.Join(qdir, "start.snbt")); string(b) != chapter {
		t.Fatal("o capítulo foi alterado antes do install")
	}
	res, err := install.Install(pack.FTBOutDir(work), game, false, false)
	if err != nil || res.Conflicts != 1 {
		t.Fatalf("sem --overwrite devia recusar: %v %+v", err, res)
	}
	if _, err := install.Install(pack.FTBOutDir(work), game, true, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(qdir, "start.snbt"))
	bak, berr := os.ReadFile(filepath.Join(qdir, "start.snbt.bak"))
	if berr != nil || string(bak) != chapter {
		t.Fatalf("a cópia .bak devia ser o original: %v", berr)
	}
	if !strings.Contains(string(got), `"{mtrad.ftbq.5ddf8d6c7d2ba17d.title}"`) || !strings.Contains(string(got), `"{@pagebreak}"`) || !strings.Contains(string(got), "Count\": 1b") {
		t.Fatalf("%s", got)
	}
}

func TestEndToEndActivateEOverwrite(t *testing.T) {
	root, inst, game := fakeInstance(t)
	work := filepath.Join(root, "work")
	os.WriteFile(filepath.Join(game, "options.txt"), []byte("lang:en_us\nresourcePacks:[\"vanilla\"]\n"), 0o644)
	// um pt_br.snbt diferente já existe no jogo: sem --overwrite o install recusa
	ptSnbt := filepath.Join(game, "config", "ftbquests", "quests", "lang", "pt_br.snbt")
	os.MkdirAll(filepath.Dir(ptSnbt), 0o755)
	os.WriteFile(ptSnbt, []byte(`{"quest.BBBB.title": "feito à mão"}`), 0o644)
	err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{Install: true})
	var ee exitErr
	if !errors.As(err, &ee) || ee.code != schema.ExitPartial {
		t.Fatalf("sem --overwrite devia terminar incompleto: %v", err)
	}
	if b, _ := os.ReadFile(ptSnbt); !strings.Contains(string(b), "feito à mão") {
		t.Fatal("o arquivo do usuário foi sobrescrito sem --overwrite")
	}
	// agora com overwrite e activate
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{Install: true, Overwrite: true, Activate: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ptSnbt); !strings.Contains(string(b), "[pt]") {
		t.Fatalf("o arquivo devia ter sido substituído:\n%s", b)
	}
	if b, err := os.ReadFile(ptSnbt + ".bak"); err != nil || !strings.Contains(string(b), "feito à mão") {
		t.Fatalf("o original devia ficar em .bak: %v", err)
	}
	opt, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if !strings.Contains(string(opt), `"file/mtrad-pt_br.zip"`) || !strings.Contains(string(opt), "lang:pt_br") {
		t.Fatalf("options.txt:\n%s", opt)
	}
}

// Ctrl+C no meio do translate: o que já existe vai para um pacote parcial e o
// código de saída diz que ficou incompleto.
func TestInterrupcaoMontaPacoteParcial(t *testing.T) {
	root, inst, game := fakeInstance(t)
	work := filepath.Join(root, "work")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // como se o Ctrl+C viesse antes de qualquer pedido
	err := runAll(ctx, work, inst, "", echoConfig(), runOpts{})
	var ee exitErr
	if !errors.As(err, &ee) || ee.code != schema.ExitPartial {
		t.Fatalf("devia terminar com saída parcial: %v", err)
	}
	zp := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	lang, ok := readZipFile(t, zp, "assets/mymod/lang/pt_br.json")
	if !ok || !strings.Contains(lang, "Já traduzido") {
		t.Fatalf("o pacote parcial devia levar o que já estava traduzido: %v %s", ok, lang)
	}
	if _, err := os.Stat(filepath.Join(work, "translate.done.json")); err != nil {
		t.Fatal("o registro da etapa translate devia existir mesmo com a interrupção")
	}
	// retomar com o contexto normal termina o serviço
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{}); err != nil {
		t.Fatal(err)
	}
	lang, _ = readZipFile(t, zp, "assets/mymod/lang/pt_br.json")
	if !strings.Contains(lang, "[pt] Shiny Gem") {
		t.Fatalf("depois de retomar o pacote devia ficar completo: %s", lang)
	}
}

func TestInitSemInstancia(t *testing.T) {
	out := filepath.Join(t.TempDir(), "mtrad.json")
	if err := stageInit("", out, "ollama", "translategemma:4b", false); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(out)
	if err != nil || c.Translate.Engines["llm"].Model != "translategemma:4b" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := stageInit("", out, "nada", "", true); err == nil {
		t.Fatal("motor desconhecido devia dar erro mesmo sem instância")
	}
}

// Mod de Minecraft 1.12.2 (Forge, arquivos .lang): lê en_US.lang, aproveita o
// pt_BR.lang, traduz e grava assets/<ns>/lang/pt_br.lang (formato de pacote 3).
func TestEndToEndLangAntigo(t *testing.T) {
	root := t.TempDir()
	inst := filepath.Join(root, "PrismLauncher", "instances", "velha")
	game := filepath.Join(inst, "minecraft")
	os.MkdirAll(filepath.Join(game, "mods"), 0o755)
	os.WriteFile(filepath.Join(inst, "mmc-pack.json"), []byte(`{"components":[{"uid":"net.minecraft","version":"1.12.2"},{"uid":"net.minecraftforge","version":"14.23.5.2860"}],"formatVersion":1}`), 0o644)
	jar := testutil.MakeJar(t, map[string]string{
		"mcmod.info":                    `[{"modid":"oldmod","name":"Old Mod","version":"1.0"}]`,
		"assets/oldmod/lang/en_US.lang": "# comentário\nitem.gem.name=Shiny Gem\ntile.rock.name=Rock\nmsg.hello=Hello %s, welcome\n",
		"assets/oldmod/lang/pt_BR.lang": "tile.rock.name=Rocha\n",
	})
	os.WriteFile(filepath.Join(game, "mods", "oldmod.jar"), jar, 0o644)
	work := filepath.Join(root, "work")
	if err := runAll(context.Background(), work, inst, "", echoConfig(), runOpts{}); err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip")
	lang, ok := readZipFile(t, zp, "assets/oldmod/lang/pt_br.lang")
	if !ok {
		t.Fatal("falta o pt_br.lang no zip")
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(lang), "\n") {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	if got["tile.rock.name"] != "Rocha" || got["item.gem.name"] != "[pt] Shiny Gem" || got["msg.hello"] != "[pt] Hello %s, welcome" || len(got) != 3 {
		t.Fatalf("%v\n%s", got, lang)
	}
	if _, has := readZipFile(t, zp, "assets/oldmod/lang/pt_br.json"); has {
		t.Fatal("não pode haver .json no formato antigo")
	}
	if meta, _ := readZipFile(t, zp, "pack.mcmeta"); !strings.Contains(meta, `"pack_format": 3`) {
		t.Fatal(meta)
	}
}

// Cada etapa precisa abrir as próprias opções sem entrar em pânico (o activate
// registrava --force duas vezes e caía em qualquer chamada).
func TestEveryStageParsesItsFlags(t *testing.T) {
	for _, cmd := range []string{"version", "scan", "extract", "patchouli", "ftbq", "diff", "glossary", "protect", "route", "translate", "validate", "pack", "install", "run", "activate", "verify", "init", "doctor", "evalset", "logs", "unlock"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s -h: pânico: %v", cmd, r)
				}
			}()
			dispatch(context.Background(), cmd, []string{"-h"})
		}()
	}
}

func TestActivateAcceptsItsOwnForceAndDryRun(t *testing.T) {
	work := t.TempDir() // sem instance.json: tem que falhar com erro normal, não com pânico
	for _, args := range [][]string{{"-w", work, "--dry-run"}, {"-w", work, "--force", "--no-lang"}} {
		err := dispatch(context.Background(), "activate", args)
		if err == nil {
			t.Fatalf("activate %v sem instância devia falhar", args)
		}
	}
}

func TestProgressLine(t *testing.T) {
	r := translate.Rate{Elapsed: 12*time.Minute + 4*time.Second, WordsPerSec: 7.94, Remaining: 28 * time.Minute, RemainingKnown: true}
	got := progressLine(5100, 17031, r)
	for _, want := range []string{"traduzidos 5100 de 17031", "12 min 04 s decorridos", "7,9 palavras/s", "faltam ~28 min 00 s"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em %q", want, got)
		}
	}
	// no começo, sem velocidade nem previsão: só o que se sabe
	got = progressLine(0, 100, translate.Rate{Elapsed: time.Second})
	if strings.Contains(got, "palavras/s") || strings.Contains(got, "faltam") {
		t.Errorf("não devia inventar velocidade nem previsão: %q", got)
	}
}
