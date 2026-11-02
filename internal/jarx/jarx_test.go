package jarx

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeJar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(c)
	}
	zw.Close()
	return b.Bytes()
}

func TestOpenNestedAndMeta(t *testing.T) {
	inner := makeJar(t, map[string][]byte{"fabric.mod.json": []byte(`{"id":"inner","version":"2"}`), "assets/inner/lang/en_us.json": []byte(`{"a":"b"}`)})
	outer := makeJar(t, map[string][]byte{
		"fabric.mod.json":        []byte("{\"id\":\"outer\",\"version\":\"1\",\"jars\":[{\"file\":\"libs/in.jar\"},{\"file\":\"META-INF/jars/ghost.jar\"}],}"),
		"libs/in.jar":            inner,
		"META-INF/jars/orph.jar": inner,
	})
	p := filepath.Join(t.TempDir(), "o.jar")
	os.WriteFile(p, outer, 0o644)
	j, err := Open([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	m, err := j.Meta()
	if err != nil || m.ID != "outer" || len(m.Jars) != 2 {
		t.Fatalf("%v %+v", err, m)
	}
	o := j.Orphans(m)
	if len(o) != 1 || o[0] != "META-INF/jars/orph.jar" {
		t.Fatalf("órfãos: %v", o)
	}
	j2, err := Open([]string{p, "libs/in.jar"})
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	m2, _ := j2.Meta()
	if m2.ID != "inner" {
		t.Fatalf("%+v", m2)
	}
	b, err := j2.Read("assets/inner/lang/en_us.json", MaxEntry)
	if err != nil || string(b) != `{"a":"b"}` {
		t.Fatalf("%v %q", err, b)
	}
	if _, err := Open([]string{p, "nao/existe.jar"}); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestMetaMissing(t *testing.T) {
	j, _ := FromBytes(makeJar(t, map[string][]byte{"x": []byte("1")}))
	m, err := j.Meta()
	if err != nil || m.Found {
		t.Fatalf("%v %+v", err, m)
	}
}

func openJar(t *testing.T, files map[string][]byte) *Jar {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.jar")
	os.WriteFile(p, makeJar(t, files), 0o644)
	j, err := Open([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(j.Close)
	return j
}

func TestMetaForgeModsTomlAndJarJar(t *testing.T) {
	toml := "# comentário\nmodLoader=\"javafml\"\nloaderVersion=\"[47,)\"\nlicense=\"MIT\"\n\n" +
		"[[mods]] #cada mod\nmodId=\"cool_mod\" #o id\nversion=\"${file.jarVersion}\"\ndisplayName='Cool Mod'\n" +
		"description='''\nvárias linhas\n'''\n\n" +
		"[[dependencies.cool_mod]]\n    modId=\"forge\"\n    mandatory=true\n" +
		"[[mods]]\nmodId=\"outro\"\n"
	inner := makeJar(t, map[string][]byte{"META-INF/mods.toml": []byte("[[mods]]\nmodId=\"lib\"\nversion=\"2\"\n")})
	j := openJar(t, map[string][]byte{
		"META-INF/mods.toml":              []byte(toml),
		"META-INF/jarjar/metadata.json":   []byte(`{"jars":[{"identifier":{"group":"g","artifact":"a"},"version":{"range":"[1,)","artifactVersion":"2"},"path":"META-INF/jarjar/lib-2.jar","isObfuscated":false},{"path":"META-INF/jarjar/ghost.jar"}]}`),
		"META-INF/jarjar/lib-2.jar":       inner,
		"META-INF/jarjar/semmetadata.jar": inner,
	})
	m, err := j.Meta()
	if err != nil || !m.Found || m.Loader != "forge" || m.ID != "cool_mod" || m.Name != "Cool Mod" || m.Version != "${file.jarVersion}" {
		t.Fatalf("%v %+v", err, m)
	}
	// lib-2.jar vem do metadata.json; semmetadata.jar só da pasta; ghost.jar não existe
	if len(m.Jars) != 2 || m.Jars[0] != "META-INF/jarjar/lib-2.jar" || m.Jars[1] != "META-INF/jarjar/semmetadata.jar" {
		t.Fatalf("jars: %v", m.Jars)
	}
}

func TestMetaNeoForgeAndLegacy(t *testing.T) {
	j := openJar(t, map[string][]byte{"META-INF/neoforge.mods.toml": []byte("[[mods]]\nmodId = \"nmod\"\nversion = \"1.2\"\n")})
	if m, _ := j.Meta(); m.Loader != "neoforge" || m.ID != "nmod" || m.Version != "1.2" {
		t.Fatalf("%+v", m)
	}
	j = openJar(t, map[string][]byte{"mcmod.info": []byte(`[{"modid":"old","name":"Old Mod","version":"1.0"}]`)})
	if m, _ := j.Meta(); m.Loader != "forge-legacy" || m.ID != "old" || m.Name != "Old Mod" {
		t.Fatalf("%+v", m)
	}
	j = openJar(t, map[string][]byte{"mcmod.info": []byte(`{"modListVersion":2,"modList":[{"modid":"old2","name":"N","version":"2"}]}`)})
	if m, _ := j.Meta(); m.ID != "old2" {
		t.Fatalf("%+v", m)
	}
	j = openJar(t, map[string][]byte{"readme.txt": []byte("x")})
	if m, err := j.Meta(); err != nil || m.Found {
		t.Fatalf("%v %+v", err, m)
	}
}

func TestLimiteDeEntradasELeitura(t *testing.T) {
	b := makeJar(t, map[string][]byte{"a.txt": []byte("12345"), "b.txt": []byte("x"), "c.txt": []byte("y")})
	old := MaxEntries
	defer func() { MaxEntries = old }()
	MaxEntries = 2
	if _, err := FromBytes(b); err == nil || !strings.Contains(err.Error(), "limite") {
		t.Fatalf("devia recusar jar com entradas demais: %v", err)
	}
	MaxEntries = 10
	j, err := FromBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := j.Read("a.txt", 5); err != nil || string(got) != "12345" {
		t.Fatalf("no limite exato devia ler: %q %v", got, err)
	}
	if _, err := j.Read("a.txt", 4); err == nil {
		t.Fatal("acima do limite devia dar erro, não texto cortado")
	}
}
