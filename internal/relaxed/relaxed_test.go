package relaxed

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Node {
	t.Helper()
	n, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, s)
	}
	return n
}

func TestSNBTNewlineSeparators(t *testing.T) {
	n := mustParse(t, "{\n\t\"chapter.AB.title\": \"Olá\"\n\tquest.CD.quest_desc: [\n\t\t\"a\"\n\t\t\"b\"\n\t]\n}")
	if g := n.Get("/chapter.AB.title"); g == nil || g.Str != "Olá" {
		t.Fatalf("title: %+v", g)
	}
	if g := n.Get("/quest.CD.quest_desc/1"); g == nil || g.Str != "b" {
		t.Fatalf("desc/1: %+v", g)
	}
}

func TestCommasComments(t *testing.T) {
	n := mustParse(t, "// c\n{ a: 'x', /* y */ \"b\": [1, 2,], c = true, }")
	if n.Get("/a").Str != "x" || n.Get("/b/1").Raw != "2" || n.Get("/c").Raw != "true" {
		t.Fatalf("%+v", n)
	}
}

func TestEscapes(t *testing.T) {
	n := mustParse(t, `["a\nb", "é", "😀", "q\"q", 'it\'s', "x\
y"]`)
	want := []string{"a\nb", "é", "😀", `q"q`, "it's", "xy"}
	for i, w := range want {
		if got := n.Items[i].Str; got != w {
			t.Errorf("%d: %q != %q", i, got, w)
		}
	}
}

func TestBOMAndErrors(t *testing.T) {
	if _, err := Parse([]byte("\xef\xbb\xbf{}")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{`, `{"a"}`, `{"a": }`, `[1`, `"abc`, `{} x`, `{"a": "\q"}`, strings.Repeat("[", 300)} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("devia falhar: %.20q", bad)
		}
	}
}

func TestDupKeys(t *testing.T) {
	n := mustParse(t, `{"a":"1","a":"2"}`)
	if n.Dups != 1 || len(n.Pairs) != 1 || n.Get("/a").Str != "2" {
		t.Fatalf("%+v", n)
	}
}

func TestPointerEscaping(t *testing.T) {
	n := mustParse(t, `{"a/b": {"c~d": "x"}}`)
	var got []string
	n.Walk(func(p string, _ *Node) { got = append(got, p) })
	if len(got) != 1 || got[0] != "/a~1b/c~0d" {
		t.Fatalf("%v", got)
	}
	if n.Get(got[0]).Str != "x" {
		t.Fatal("Get")
	}
}

func TestSetStringErrors(t *testing.T) {
	n := mustParse(t, `{"a":"x","n":1,"l":["y"]}`)
	if err := n.SetString("/a", "z"); err != nil || n.Get("/a").Str != "z" {
		t.Fatal(err)
	}
	if n.SetString("/n", "z") == nil || n.SetString("/nope", "z") == nil || n.SetString("/l/01", "z") == nil {
		t.Fatal("devia falhar")
	}
	if err := n.SetString("/l/0", "w"); err != nil {
		t.Fatal(err)
	}
}

func TestWriteJSONValid(t *testing.T) {
	n := mustParse(t, `{"a":"é\n\"x\"","n":1.5,"b":true,"z":null,"o":{},"l":[],"s":3b}`)
	out, err := Write(n, JSON)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, out)
	}
	if v["a"] != "é\n\"x\"" || v["n"] != 1.5 || v["s"] != "3b" {
		t.Fatalf("%v", v)
	}
	if !strings.Contains(string(out), "é") {
		t.Fatal("acento não devia virar \\u")
	}
}

func TestSNBTRoundTrip(t *testing.T) {
	src := "{\n\t\"k.1\": \"a\\\"b\",\n\t\"k.2\": [\n\t\t\"x\",\n\t\t\"\"\n\t],\n\tcolors: [I; 1, 2, 3],\n\tn: 5L\n}\n"
	n := mustParse(t, src)
	out, err := Write(n, SNBT)
	if err != nil {
		t.Fatal(err)
	}
	n2 := mustParse(t, string(out))
	out2, _ := Write(n2, SNBT)
	if string(out) != string(out2) {
		t.Fatalf("não é estável:\n%s\n%s", out, out2)
	}
	if n2.Get("/colors").Prefix != "I" || n2.Get("/n").Raw != "5L" || n2.Get("/k.1").Str != `a"b` {
		t.Fatalf("%s", out)
	}
	if _, err := Write(n, JSON); err == nil {
		t.Fatal("lista tipada em JSON devia falhar")
	}
}

func TestCloneIndependent(t *testing.T) {
	n := mustParse(t, `{"a":["x"]}`)
	c := n.Clone()
	c.SetString("/a/0", "y")
	if n.Get("/a/0").Str != "x" {
		t.Fatal("Clone compartilha estado")
	}
}

// Entradas aleatórias feitas só de caracteres de sintaxe: nunca pode dar pânico
// nem travar, e o que for aceito tem de sobreviver a gravar e ler de novo.
func TestRandomInputsNeverPanic(t *testing.T) {
	alphabet := []string{"{", "}", "[", "]", ",", ":", "=", "\"", "'", "\\", "\\u00e9", "\\ud83d", "/", "//", "/*", "*/", "\n", " ", "a", "1", "-", "true", "I;", "é", "\x00"}
	seed := uint32(12345)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	accepted := 0
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for j, n := 0, next(24); j < n; j++ {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		n, err := Parse([]byte(b.String()))
		if err != nil {
			continue
		}
		accepted++
		for _, f := range []Format{JSON, SNBT} {
			out, err := Write(n, f)
			if err != nil {
				continue // lista tipada em JSON, por exemplo
			}
			if f == SNBT {
				n2, err := Parse(out)
				if err != nil {
					t.Fatalf("saída SNBT não volta a ler: %q -> %q: %v", b.String(), out, err)
				}
				out2, _ := Write(n2, SNBT)
				if string(out) != string(out2) {
					t.Fatalf("SNBT instável: %q", b.String())
				}
			}
		}
	}
	if accepted < 100 {
		t.Fatalf("poucas entradas aceitas (%d): o teste não está exercitando nada", accepted)
	}
}
