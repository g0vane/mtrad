package version

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func mkInstance(t *testing.T, gameDirName, mmc string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "PrismLauncher", "instances", "inst")
	if err := os.MkdirAll(filepath.Join(root, gameDirName, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if mmc != "" {
		os.WriteFile(filepath.Join(root, "mmc-pack.json"), []byte(mmc), 0o644)
	}
	return root
}

const mmc121 = `{"components":[{"uid":"net.minecraft","version":"1.21.1"},{"uid":"net.fabricmc.fabric-loader","version":"0.16.5"}],"formatVersion":1}`

func TestDetectBothGameDirNames(t *testing.T) {
	for _, n := range []string{"minecraft", ".minecraft"} {
		in, err := Detect(Options{InstanceDir: mkInstance(t, n, mmc121)})
		if err != nil {
			t.Fatal(err)
		}
		if in.McVersion != "1.21.1" || in.Loader != "fabric" || in.LoaderVersion != "0.16.5" || in.PackFormat.Major != 34 || filepath.Base(in.GameDir) != n {
			t.Fatalf("%+v", in)
		}
	}
}

func TestPathsAreAbsolute(t *testing.T) {
	dir := mkInstance(t, "minecraft", mmc121)
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(filepath.Dir(dir))
	in, err := Detect(Options{InstanceDir: filepath.Base(dir)})
	if err != nil || !filepath.IsAbs(in.GameDir) || !filepath.IsAbs(in.InstanceDir) {
		t.Fatalf("%v %+v", err, in)
	}
}

func TestTableSamples(t *testing.T) {
	cases := map[string][2]int{"1.6.1": {1, 0}, "1.8.9": {1, 0}, "1.9": {2, 0}, "1.10.2": {2, 0}, "1.11": {3, 0}, "1.12.2": {3, 0}, "1.13.2": {4, 0}, "1.14.4": {4, 0}, "1.16.5": {6, 0}, "1.19.3": {12, 0}, "1.20.1": {15, 0}, "1.21.8": {64, 0}, "1.21.9": {69, 0}, "26.2": {88, 0}, "26.3": {97, 1}}
	for v, want := range cases {
		pf, ok := PackFormatFor(v)
		if !ok || pf.Major != want[0] || pf.Minor != want[1] {
			t.Errorf("%s: %+v %v", v, pf, ok)
		}
	}
	if len(releases) != 48+38 { // 48 de 1.14 a 26.3, 38 de 1.6.1 a 1.13.2
		t.Errorf("tabela com %d versões", len(releases))
	}
}

func TestOverridesAndErrors(t *testing.T) {
	dir := mkInstance(t, "minecraft", "")
	if _, err := Detect(Options{InstanceDir: dir}); err == nil {
		t.Fatal("sem mmc-pack e sem --mc deveria falhar")
	}
	in, err := Detect(Options{InstanceDir: dir, McVersion: "1.20.1", Loader: "fabric"})
	if err != nil || in.PackFormat.Major != 15 || len(in.Warnings) == 0 {
		t.Fatalf("%v %+v", err, in)
	}
	if _, err := Detect(Options{InstanceDir: dir, McVersion: "99.9"}); err == nil {
		t.Fatal("versão desconhecida deveria pedir --pack-format")
	}
	in, err = Detect(Options{InstanceDir: dir, McVersion: "99.9", PackMajor: 120, PackMinor: 2})
	if err != nil || in.PackFormat.Major != 120 || in.PackFormat.Minor != 2 {
		t.Fatalf("%v %+v", err, in)
	}
	if _, err := Detect(Options{InstanceDir: filepath.Join(dir, "nao")}); err == nil {
		t.Fatal("pasta inexistente")
	}
	empty := t.TempDir()
	if _, err := Detect(Options{InstanceDir: empty}); err == nil {
		t.Fatal("sem pasta do jogo")
	}
}

func TestClientJarFallback(t *testing.T) {
	dir := mkInstance(t, "minecraft", `{"components":[{"uid":"net.minecraft","version":"26.4-snapshot-4"},{"uid":"net.fabricmc.fabric-loader","version":"0.20"}]}`)
	data := filepath.Dir(filepath.Dir(dir))
	jarDir := filepath.Join(data, "libraries", "com", "mojang", "minecraft", "26.4-snapshot-4")
	os.MkdirAll(jarDir, 0o755)
	f, _ := os.Create(filepath.Join(jarDir, "minecraft-26.4-snapshot-4-client.jar"))
	zw := zip.NewWriter(f)
	w, _ := zw.Create("version.json")
	w.Write([]byte(`{"pack_version":{"resource_major":100,"resource_minor":1,"data_major":124}}`))
	zw.Close()
	f.Close()
	in, err := Detect(Options{InstanceDir: dir})
	if err != nil || in.PackFormat.Major != 100 || in.PackFormat.Minor != 1 {
		t.Fatalf("%v %+v", err, in)
	}
	if pf, ok := parsePackVersion([]byte(`{"pack_version":34}`)); !ok || pf.Major != 34 {
		t.Fatal("número simples")
	}
	if _, ok := parsePackVersion([]byte(`{}`)); ok {
		t.Fatal("sem campo")
	}
}

func TestRunWritesFiles(t *testing.T) {
	dir := mkInstance(t, "minecraft", mmc121)
	work := t.TempDir()
	if _, err := Run(work, Options{InstanceDir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"instance.json", "version.done.json"} {
		if _, err := os.Stat(filepath.Join(work, n)); err != nil {
			t.Fatal(err)
		}
	}
}
