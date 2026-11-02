// Package testutil reúne ajudantes só para testes.
package testutil

import (
	"archive/zip"
	"bytes"
	"sort"
	"testing"
)

// MakeJar monta um zip em memória com os arquivos dados.
func MakeJar(t testing.TB, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
