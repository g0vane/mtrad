package relaxed

import (
	"os"
	"testing"
)

// Arquivo de teste escrito para o mtrad: vírgulas a mais e a menos, "=" no
// lugar de ":", aspas simples, comentários com "#" e "//", arrays tipados.
func TestLenientSNBTFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/snbt_lenient.snbt")
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != Obj {
		t.Fatalf("raiz: %v", n.Kind)
	}
	if got := n.Get("/test_string"); got == nil || got.Str != "value" {
		t.Fatalf("test_string: %+v", got)
	}
	if got := n.Get("/test string key"); got == nil || got.Str != `value "2"` {
		t.Fatalf("chave com espaço: %+v", got)
	}
	out, err := Write(n, SNBT)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := Parse(out)
	if err != nil {
		t.Fatalf("o que gravamos não abre: %v\n%s", err, out)
	}
	out2, _ := Write(n2, SNBT)
	if string(out) != string(out2) {
		t.Fatalf("gravar duas vezes difere:\n%s\n---\n%s", out, out2)
	}
}
