package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedWorkerAndWriteIfMissing(t *testing.T) {
	if !strings.Contains(string(CT2Worker), "ct2worker") {
		t.Fatal("o ct2worker.py não foi embutido")
	}
	p := filepath.Join(t.TempDir(), "tools", "ct2worker.py")
	ok, err := WriteIfMissing(p, CT2Worker)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	os.WriteFile(p, []byte("editado"), 0o755)
	ok, err = WriteIfMissing(p, CT2Worker)
	if err != nil || ok {
		t.Fatalf("não deveria sobrescrever: ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "editado" {
		t.Fatalf("%q", b)
	}
}
