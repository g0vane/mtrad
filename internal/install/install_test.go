package install

import (
	"os"
	"path/filepath"
	"testing"

	"mtrad/internal/pack"
	"mtrad/internal/schema"
)

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const rel = "config/ftbquests/quests/lang/pt_br.snbt"

func TestInstallFlow(t *testing.T) {
	src, game := t.TempDir(), t.TempDir()
	put(t, src, rel, "novo")
	put(t, src, "config/ftbquests/quests/lang/pt_br/a.json5", "{}")

	// simulação não grava nada
	r, err := Install(src, game, false, true)
	if err != nil || len(r.Items) != 2 || r.Items[0].Action != Planned {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := os.Stat(filepath.Join(game, "config")); err == nil {
		t.Fatal("dry-run gravou")
	}

	r, err = Install(src, game, false, false)
	if err != nil || r.Items[0].Action != Copied || read(t, game, rel) != "novo" {
		t.Fatalf("%v %+v", err, r)
	}
	// segunda vez: igual
	r, _ = Install(src, game, false, false)
	if r.Items[0].Action != Same || r.Items[1].Action != Same {
		t.Fatalf("%+v", r)
	}
	// arquivo do usuário diferente: não sobrescreve
	put(t, game, rel, "do usuario")
	r, _ = Install(src, game, false, false)
	if r.Conflicts != 1 || read(t, game, rel) != "do usuario" {
		t.Fatalf("%+v", r)
	}
	// com --overwrite: guarda .bak
	r, err = Install(src, game, true, false)
	if err != nil || read(t, game, rel) != "novo" || read(t, game, rel+".bak") != "do usuario" {
		t.Fatalf("%v %+v", err, r)
	}
	// de novo diferente: .bak.1 e o .bak antigo fica
	put(t, game, rel, "outra edicao")
	if _, err := Install(src, game, true, false); err != nil {
		t.Fatal(err)
	}
	if read(t, game, rel+".bak") != "do usuario" || read(t, game, rel+".bak.1") != "outra edicao" {
		t.Fatal("cópias .bak erradas")
	}
	if _, err := os.Stat(filepath.Join(game, filepath.FromSlash(rel)) + ".mtrad-tmp"); err == nil {
		t.Fatal("sobrou arquivo temporário")
	}
}

func TestInstallRefusesSymlinks(t *testing.T) {
	src, game, outside := t.TempDir(), t.TempDir(), t.TempDir()
	put(t, src, rel, "x")
	// a pasta config é um link para fora do jogo
	if err := os.Symlink(outside, filepath.Join(game, "config")); err != nil {
		t.Skip("sem link simbólico:", err)
	}
	r, err := Install(src, game, true, false)
	if err != nil || r.Items[0].Action != Refused {
		t.Fatalf("%v %+v", err, r)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("escreveu através do link")
	}
}

func TestInstallNothingGenerated(t *testing.T) {
	r, err := Install(filepath.Join(t.TempDir(), "nao-existe"), t.TempDir(), false, false)
	if err != nil || len(r.Items) != 0 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := Install(t.TempDir(), filepath.Join(t.TempDir(), "nao-existe"), false, false); err == nil {
		t.Fatal("pasta do jogo inexistente devia falhar")
	}
}

func TestRunUsesInstanceJSON(t *testing.T) {
	work, game := t.TempDir(), t.TempDir()
	schema.WriteJSON(filepath.Join(work, "instance.json"), schema.Instance{Schema: schema.VInstance, GameDir: game})
	put(t, pack.FTBOutDir(work), rel, "ok")
	r, err := Run(work, false, false)
	if err != nil || len(r.Items) != 1 || read(t, game, rel) != "ok" {
		t.Fatalf("%v %+v", err, r)
	}
}
