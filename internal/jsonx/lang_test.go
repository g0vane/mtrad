package jsonx

import "testing"

func TestParseLang(t *testing.T) {
	src := "\uFEFF# comentário\r\nitem.gem.name=Shiny Gem\r\n\r\ntile.rock.name = Rock = hard\nbroken line\nitem.gem.name=Shinier Gem\nmsg=Linha um\\nlinha dois\n  # outro\n"
	d := ParseLang([]byte(src))
	m := d.Map()
	if len(d.Pairs) != 3 || m["item.gem.name"] != "Shinier Gem" || d.Dups != 1 || d.NonString != 1 {
		t.Fatalf("%+v", d)
	}
	// só o primeiro "=" separa; o valor guarda o espaço depois do "="
	if m["tile.rock.name"] != " Rock = hard" {
		t.Fatalf("%q", m["tile.rock.name"])
	}
	if m["msg"] != `Linha um\nlinha dois` {
		t.Fatalf("o \\n escrito com barra fica como está: %q", m["msg"])
	}
	if d.Pairs[0].Key != "item.gem.name" {
		t.Fatal("a ordem do arquivo deve ser mantida")
	}
}

func TestMarshalLangIdaEVolta(t *testing.T) {
	pairs := []Pair{{"a.b", "Olá"}, {"c", "duas\nlinhas"}, {"d", "com = igual"}}
	out := MarshalLang(pairs)
	if string(out) != "a.b=Olá\nc=duas\\nlinhas\nd=com = igual\n" {
		t.Fatalf("%q", out)
	}
	back := ParseLang(out).Map()
	if back["d"] != "com = igual" || back["a.b"] != "Olá" {
		t.Fatalf("%v", back)
	}
}
