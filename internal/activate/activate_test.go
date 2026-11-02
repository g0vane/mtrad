package activate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/schema"
)

func never(string) bool { return false }

func setup(t *testing.T, options string) string {
	game := t.TempDir()
	os.MkdirAll(filepath.Join(game, "resourcepacks"), 0o755)
	os.WriteFile(filepath.Join(game, "resourcepacks", "mtrad-pt_br.zip"), []byte("zip"), 0o644)
	if options != "" {
		os.WriteFile(filepath.Join(game, "options.txt"), []byte(options), 0o644)
	}
	return game
}

const opts = "version:3465\nlang:en_us\nresourcePacks:[\"vanilla\",\"file/outro.zip\"]\nincompatibleResourcePacks:[]\nfov:0.5\n"

func TestAtivaPacoteEIdioma(t *testing.T) {
	game := setup(t, opts)
	r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, SetLang: true, Running: never})
	if err != nil || !r.PackAdded || !r.LangChanged || r.Backup == "" {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	want := "version:3465\nlang:pt_br\nresourcePacks:[\"vanilla\",\"file/outro.zip\",\"file/mtrad-pt_br.zip\"]\nincompatibleResourcePacks:[]\nfov:0.5\n"
	if string(b) != want {
		t.Fatalf("\n%s\nqueria\n%s", b, want)
	}
	bak, _ := os.ReadFile(r.Backup)
	if string(bak) != opts {
		t.Fatal("o backup devia ser o arquivo original")
	}
	// de novo: nada a fazer, nada regravado
	r, err = Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, SetLang: true, Running: never})
	if err != nil || r.Changed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMantemFimDeLinhaCRLF(t *testing.T) {
	game := setup(t, strings.ReplaceAll(opts, "\n", "\r\n"))
	if _, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 34}, Running: never}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if strings.Count(string(b), "\r\n") != 5 || strings.Contains(strings.ReplaceAll(string(b), "\r\n", ""), "\n") {
		t.Fatalf("%q", b)
	}
	if !strings.Contains(string(b), "lang:en_us") {
		t.Fatal("sem SetLang o idioma não pode mudar")
	}
}

func TestSemOptionsTxtCria(t *testing.T) {
	game := setup(t, "")
	r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 34}, SetLang: true, Running: never})
	if err != nil || !r.Created || r.Backup != "" {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if string(b) != "resourcePacks:[\"vanilla\",\"file/mtrad-pt_br.zip\"]\nlang:pt_br\n" {
		t.Fatalf("%q", b)
	}
}

func TestVersoesAntigas(t *testing.T) {
	game := setup(t, "lang:en_US\nresourcePacks:[]\n")
	r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 2}, SetLang: true, Running: never})
	if err != nil || r.PackEntry != "mtrad-pt_br.zip" || r.Lang != "pt_BR" {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if string(b) != "lang:pt_BR\nresourcePacks:[\"mtrad-pt_br.zip\"]\n" {
		t.Fatalf("%q", b)
	}
}

func TestRecusaComJogoAbertoELinhaEstranha(t *testing.T) {
	game := setup(t, opts)
	if _, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, Running: func(string) bool { return true }}); err == nil || !strings.Contains(err.Error(), "aberto") {
		t.Fatalf("%v", err)
	}
	if _, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, Running: func(string) bool { return true }, Force: true}); err != nil {
		t.Fatalf("--force devia passar: %v", err)
	}
	bad := setup(t, "resourcePacks:[nao é json\n")
	before, _ := os.ReadFile(filepath.Join(bad, "options.txt"))
	if _, err := Run(Options{GameDir: bad, PackFormat: schema.PackFormat{Major: 15}, Running: never}); err == nil {
		t.Fatal("linha ilegível devia dar erro")
	}
	after, _ := os.ReadFile(filepath.Join(bad, "options.txt"))
	if string(before) != string(after) {
		t.Fatal("o arquivo não pode mudar quando dá erro")
	}
}

func TestDryRunNaoGrava(t *testing.T) {
	game := setup(t, opts)
	r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, SetLang: true, DryRun: true, Running: func(string) bool { return true }})
	if err != nil || !r.Changed {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(game, "options.txt"))
	if string(b) != opts {
		t.Fatal("dry-run gravou")
	}
}

func TestAvisaQuandoOPacoteNaoExiste(t *testing.T) {
	game := t.TempDir()
	r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, DryRun: true, Running: never})
	if err != nil || len(r.Notes) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

// A primeira cópia (o options.txt original) não pode ser trocada por uma
// ativação seguinte: ela já seria o arquivo mexido pelo mtrad.
func TestActivateKeepsFirstBackup(t *testing.T) {
	game := setup(t, opts)
	run := func() Result {
		r, err := Run(Options{GameDir: game, PackFormat: schema.PackFormat{Major: 15}, SetLang: true, Running: never})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := run()
	if !r1.Changed || filepath.Base(r1.Backup) != "options.txt.mtrad.bak" {
		t.Fatalf("%+v", r1)
	}
	// o jogador volta o idioma e tira o pacote; ativar de novo muda o arquivo outra vez
	os.WriteFile(filepath.Join(game, "options.txt"), []byte("version:3465\nlang:en_us\nresourcePacks:[\"vanilla\"]\n"), 0o644)
	r2 := run()
	if !r2.Changed || r2.Backup == r1.Backup {
		t.Fatalf("a segunda ativação sobrescreveu a primeira cópia: %+v", r2)
	}
	first, _ := os.ReadFile(r1.Backup)
	if string(first) != opts {
		t.Fatalf("a cópia do original foi trocada:\n%s", first)
	}
	second, _ := os.ReadFile(r2.Backup)
	if !strings.Contains(string(second), "lang:en_us") || strings.Contains(string(second), "mtrad-pt_br") {
		t.Fatalf("segunda cópia errada:\n%s", second)
	}
}
