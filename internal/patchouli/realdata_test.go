package patchouli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Teste opcional com livros de verdade. Defina MTRAD_REAL_PATCHOULI com uma
// pasta de código-fonte de um mod que tenha .../patchouli_books/... (por
// exemplo um clone do Botania); sem a variável o teste é pulado.
//
//	MTRAD_REAL_PATCHOULI=/caminho/do/clone go test ./internal/patchouli -run Real -v
func TestRealBooks(t *testing.T) {
	root := os.Getenv("MTRAD_REAL_PATCHOULI")
	if root == "" {
		t.Skip("MTRAD_REAL_PATCHOULI não definida")
	}
	files := map[string]string{"fabric.mod.json": `{"id":"real","version":"1"}`}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		slash := filepath.ToSlash(p)
		i := strings.Index(slash, "/assets/")
		if j := strings.Index(slash, "/data/"); i < 0 || (j >= 0 && j < i) {
			i = j
		}
		if i < 0 || !strings.Contains(slash, "/patchouli_books/") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		body := string(b)
		if os.Getenv("MTRAD_REAL_NO_I18N") != "" && strings.HasSuffix(slash, "/book.json") {
			// força a leitura dos JSON mesmo em livros i18n (só para exercitar o parser)
			body = strings.ReplaceAll(strings.ReplaceAll(body, `"i18n": true`, `"i18n": false`), `"i18n":true`, `"i18n":false`)
		}
		files[slash[i+1:]] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	inv := build(t, files)
	strs, docs, _, c, s := run(t, inv)
	t.Logf("arquivos no jar: %d; resultado: %+v; documentos: %d; textos: %d", len(files), c, len(docs), len(strs))
	for _, m := range s.msgs {
		t.Logf("aviso: %s", m)
	}
	n := 0
	for k, r := range strs {
		if n < 8 {
			t.Logf("%s => %q", k, r.En)
			n++
		}
	}
}
