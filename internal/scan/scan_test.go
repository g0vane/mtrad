package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/testutil"
)

type sink struct{ msgs []string }

func (s *sink) Add(src, id, msg string) { s.msgs = append(s.msgs, src+": "+msg) }

func TestScanNestedOrphanGhostCase(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(filepath.Join(mods, "sub"), 0o755)
	inner := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":              `{"id":"inner","version":"2"}`,
		"assets/inner/lang/en_us.json": `{"a":"b"}`,
	})
	outer := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":              `{"id":"outer","version":"1","jars":[{"file":"libs/in.jar"},{"file":"META-INF/jars/ghost.jar"}]}`,
		"libs/in.jar":                  string(inner),
		"META-INF/jars/orph.jar":       string(inner),
		"assets/outer/lang/en_us.json": `{"x":"y"}`,
		"assets/outer/lang/pt_br.json": `{"x":"z"}`,
		"assets/outer/lang/en_US.json": `{"x":"y"}`,
		"assets/outer/lang/de_de.json": `{"x":"y"}`,
	})
	os.WriteFile(filepath.Join(mods, "outer.jar"), outer, 0o644)
	os.WriteFile(filepath.Join(mods, "sub", "plain.JAR"), testutil.MakeJar(t, map[string]string{"assets/p/lang/en_us.json": `{}`}), 0o644)
	os.WriteFile(filepath.Join(mods, "broken.jar"), []byte("não é zip"), 0o644)
	os.WriteFile(filepath.Join(mods, "off.jar.disabled"), outer, 0o644)

	s := &sink{}
	inv, err := Scan(game, mods, s)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, j := range inv.Jars {
		by[strings.Join(j.Chain, " > ")] = i
	}
	if len(inv.Jars) != 4 {
		t.Fatalf("esperava 4 jars (broken, outer, outer>in, plain), veio %d: %v", len(inv.Jars), by)
	}
	o := inv.Jars[by["mods/outer.jar"]]
	if o.ModID != "outer" || len(o.Langs) != 2 {
		t.Fatalf("%+v", o)
	}
	joined := strings.Join(o.Notes, "|")
	for _, want := range []string{"ghost.jar", "orph.jar", "en_US.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("faltou nota sobre %s: %v", want, o.Notes)
		}
	}
	n := inv.Jars[by["mods/outer.jar > libs/in.jar"]]
	if n.ModID != "inner" || len(n.Langs) != 1 || len(n.SHA256) != 64 {
		t.Fatalf("%+v", n)
	}
	p := inv.Jars[by["mods/sub/plain.JAR"]]
	if p.ModID != "" || len(p.Notes) == 0 || len(p.Langs) != 1 {
		t.Fatalf("%+v", p)
	}
	if len(s.msgs) == 0 {
		t.Fatal("jar quebrado deveria gerar erro")
	}
}

func TestScanMissingModsDir(t *testing.T) {
	if _, err := Scan(t.TempDir(), filepath.Join(t.TempDir(), "x"), nil); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestScanForgeJarJar(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	inner := testutil.MakeJar(t, map[string]string{
		"META-INF/mods.toml":               "[[mods]]\nmodId=\"inner_lib\"\nversion=\"3\"\n",
		"assets/inner_lib/lang/en_us.json": `{"a":"b"}`,
	})
	outer := testutil.MakeJar(t, map[string]string{
		"META-INF/mods.toml":             "modLoader=\"javafml\"\n[[mods]]\nmodId=\"big_mod\"\nversion=\"1.0\"\ndisplayName=\"Big Mod\"\n",
		"META-INF/jarjar/metadata.json":  `{"jars":[{"path":"META-INF/jarjar/inner-3.jar"}]}`,
		"META-INF/jarjar/inner-3.jar":    string(inner),
		"assets/big_mod/lang/en_us.json": `{"x":"y"}`,
	})
	os.WriteFile(filepath.Join(mods, "big.jar"), outer, 0o644)
	// mod Forge sem id legível: vira nota, não erro
	os.WriteFile(filepath.Join(mods, "noid.jar"), testutil.MakeJar(t, map[string]string{"META-INF/mods.toml": "modLoader=\"javafml\"\n"}), 0o644)
	s := &sink{}
	inv, err := Scan(game, mods, s)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]string{}
	for _, j := range inv.Jars {
		by[j.ModID] = j.Loader
	}
	if len(inv.Jars) != 3 || by["big_mod"] != "forge" || by["inner_lib"] != "forge" {
		t.Fatalf("%+v %v", inv.Jars, s.msgs)
	}
	for _, j := range inv.Jars {
		if j.ModID == "" && (len(j.Notes) == 0 || !strings.Contains(strings.Join(j.Notes, ""), "sem o id")) {
			t.Fatalf("faltou a nota do mod sem id: %+v", j)
		}
	}
}

func nestedFixture(t *testing.T, listed int) (game, mods string) {
	t.Helper()
	game = t.TempDir()
	mods = filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	inner := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":              `{"id":"inner","version":"2"}`,
		"assets/inner/lang/en_us.json": `{"a":"b"}`,
	})
	var jars []string
	for i := 0; i < listed; i++ {
		jars = append(jars, `{"file":"libs/in.jar"}`)
	}
	outer := testutil.MakeJar(t, map[string]string{
		"fabric.mod.json": `{"id":"outer","version":"1","jars":[` + strings.Join(jars, ",") + `]}`,
		"libs/in.jar":     string(inner),
	})
	os.WriteFile(filepath.Join(mods, "outer.jar"), outer, 0o644)
	return game, mods
}

// O mesmo jar aninhado listado várias vezes é lido uma vez só.
func TestScanDuplicateNestedJarsListedOnce(t *testing.T) {
	game, mods := nestedFixture(t, 50)
	inv, err := Scan(game, mods, &sink{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Jars) != 2 {
		t.Fatalf("esperava outer e outer>in, veio %d jars", len(inv.Jars))
	}
}

// Um jar que lista jars demais bate no teto, avisa e para.
func TestScanNestedBudget(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	files := map[string]string{}
	var jars []string
	for i := 0; i < 6; i++ {
		name := "libs/in" + string(rune('a'+i)) + ".jar"
		files[name] = string(testutil.MakeJar(t, map[string]string{"assets/n/lang/en_us.json": `{"a":"` + name + `"}`}))
		jars = append(jars, `{"file":"`+name+`"}`)
	}
	files["fabric.mod.json"] = `{"id":"outer","version":"1","jars":[` + strings.Join(jars, ",") + `]}`
	os.WriteFile(filepath.Join(mods, "outer.jar"), testutil.MakeJar(t, files), 0o644)

	old := MaxNestedJars
	MaxNestedJars = 3
	defer func() { MaxNestedJars = old }()
	s := &sink{}
	inv, err := Scan(game, mods, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Jars) != 4 { // outer + 3 aninhados
		t.Fatalf("esperava 4 jars, veio %d", len(inv.Jars))
	}
	found := false
	for _, n := range inv.Jars[0].Notes {
		if strings.Contains(n, "limite de jars aninhados") {
			found = true
		}
	}
	if !found || len(s.msgs) == 0 {
		t.Fatalf("faltou aviso do limite: %v %v", inv.Jars[0].Notes, s.msgs)
	}
}

// Arquivo de idioma com ".." no caminho (jar hostil) não entra no inventário.
func TestScanIgnoresTraversalLangNames(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "mods")
	os.MkdirAll(mods, 0o755)
	os.WriteFile(filepath.Join(mods, "evil.jar"), testutil.MakeJar(t, map[string]string{
		"fabric.mod.json":             `{"id":"evil","version":"1"}`,
		"assets/../lang/en_us.json":   `{"a":"b"}`,
		"assets/evil/lang/en_us.json": `{"a":"b"}`,
	}), 0o644)
	inv, err := Scan(game, mods, &sink{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Jars) != 1 || len(inv.Jars[0].Langs) != 1 || inv.Jars[0].Langs[0].NS != "evil" {
		t.Fatalf("%+v", inv.Jars)
	}
	if !strings.Contains(strings.Join(inv.Jars[0].Notes, "|"), "suspeito") {
		t.Fatalf("faltou a nota: %v", inv.Jars[0].Notes)
	}
}
