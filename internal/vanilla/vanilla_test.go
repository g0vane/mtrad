package vanilla

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mtrad/internal/schema"
	"mtrad/internal/testutil"
)

const hash = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

func mkRoot(t *testing.T, mc string, withJar, withAssets bool) (root string, in schema.Instance) {
	t.Helper()
	root = t.TempDir()
	inst := filepath.Join(root, "instances", "teste")
	os.MkdirAll(inst, 0o755)
	if withJar {
		jar := filepath.Join(root, "libraries", "com", "mojang", "minecraft", mc, "minecraft-"+mc+"-client.jar")
		os.MkdirAll(filepath.Dir(jar), 0o755)
		os.WriteFile(jar, testutil.MakeJar(t, map[string]string{"assets/minecraft/lang/en_us.json": `{"block.minecraft.stone":"Stone"}`}), 0o644)
	}
	if withAssets {
		os.MkdirAll(filepath.Join(root, "assets", "indexes"), 0o755)
		os.MkdirAll(filepath.Join(root, "assets", "objects", hash[:2]), 0o755)
		os.WriteFile(filepath.Join(root, "assets", "indexes", "12.json"), []byte(`{"objects":{"minecraft/lang/pt_br.json":{"hash":"`+hash+`","size":3},"minecraft/lang/../../x":{"hash":"zz"}}}`), 0o644)
		os.WriteFile(filepath.Join(root, "assets", "objects", hash[:2], hash), []byte(`{"block.minecraft.stone":"Pedra"}`), 0o644)
	}
	return root, schema.Instance{InstanceDir: inst, McVersion: mc}
}

func TestFindBoth(t *testing.T) {
	_, in := mkRoot(t, "1.20.1", true, true)
	f := Find(in, Roots(in, nil, ""))
	if !strings.Contains(string(f.En), "Stone") || !strings.Contains(string(f.Pt), "Pedra") || len(f.Notes) != 0 {
		t.Fatalf("%+v", f)
	}
}

func TestFindMissingExplains(t *testing.T) {
	_, in := mkRoot(t, "1.20.1", false, false)
	f := Find(in, Roots(in, nil, ""))
	if f.En != nil || f.Pt != nil || len(f.Notes) != 2 {
		t.Fatalf("%+v", f)
	}
}

func TestIndexNamedAsVersionWinsAndBadHashIgnored(t *testing.T) {
	root, in := mkRoot(t, "1.20.1", false, true)
	// um índice mais novo, mas com outro nome e hash que não existe como arquivo
	other := "cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34"
	os.WriteFile(filepath.Join(root, "assets", "indexes", "99.json"), []byte(`{"objects":{"minecraft/lang/pt_br.json":{"hash":"`+other+`"}}}`), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(root, "assets", "indexes", "99.json"), future, future)
	f := Find(in, Roots(in, nil, ""))
	// 99.json é mais novo, mas o objeto não existe: cai para o seguinte
	if !strings.Contains(string(f.Pt), "Pedra") {
		t.Fatalf("%+v", f)
	}
	// com um índice chamado 1.20.1.json ele vence
	os.MkdirAll(filepath.Join(root, "assets", "objects", other[:2]), 0o755)
	os.WriteFile(filepath.Join(root, "assets", "objects", other[:2], other), []byte(`{"x":"Outro"}`), 0o644)
	os.WriteFile(filepath.Join(root, "assets", "indexes", "1.20.1.json"), []byte(`{"objects":{"minecraft/lang/pt_br.json":{"hash":"`+hash+`"}}}`), 0o644)
	f = Find(in, Roots(in, nil, ""))
	if !strings.Contains(string(f.Pt), "Pedra") {
		t.Fatalf("%s", f.Pt)
	}
}

func TestRootsOrderAndDedup(t *testing.T) {
	in := schema.Instance{InstanceDir: "/d/PrismLauncher/instances/x"}
	r := Roots(in, []string{"/custom", "/custom"}, "/home/u")
	want := []string{"/custom", "/d/PrismLauncher", "/home/u/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher", "/home/u/.local/share/PrismLauncher"}
	if strings.Join(r, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", r)
	}
}
