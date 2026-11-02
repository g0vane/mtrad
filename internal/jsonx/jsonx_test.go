package jsonx

import (
	"encoding/json"
	"testing"
)

func TestParseStrictAndTolerant(t *testing.T) {
	d, err := Parse([]byte(`{"a":"1","b":"2"}`))
	if err != nil || d.Lenient || len(d.Pairs) != 2 {
		t.Fatalf("%v %+v", err, d)
	}
	src := "\xef\xbb\xbf{\n // c\n \"a.b\":\"Hello %s\",\n \"a.b\":\"Dup\",\n \"n\":5,\n \"l\":[\"x\"],\n \"u\":\"caf\\u00e9 \\\"q\\\" // nao\",\n /* bloco */ \"z\":\"fim\",\n}"
	d, err = Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Lenient || d.Dups != 1 || d.NonString != 2 || len(d.Pairs) != 3 {
		t.Fatalf("%+v", d)
	}
	if d.Pairs[0].Key != "a.b" || d.Pairs[0].Text != "Dup" {
		t.Fatalf("a última chave repetida deve valer: %+v", d.Pairs)
	}
	if d.Pairs[1].Text != "café \"q\" // nao" {
		t.Fatalf("%q", d.Pairs[1].Text)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"", "[]", `{"a":`, `{"a":"b"} x`, "nada"} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%q deveria falhar", s)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	in := []Pair{{"k.1", "Olá <b>&\n\"x\"\\"}, {"k.2", "§a%s§r"}}
	b := Marshal(in)
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if m["k.1"] != in[0].Text || m["k.2"] != in[1].Text {
		t.Fatalf("%v", m)
	}
	if !json.Valid(b) {
		t.Fatal("inválido")
	}
	d, err := Parse(b)
	if err != nil || d.Lenient || d.Pairs[0].Key != "k.1" {
		t.Fatalf("%v %+v", err, d)
	}
}

func TestUnmarshalTolerant(t *testing.T) {
	var v struct {
		ID   string `json:"id"`
		Jars []struct {
			File string `json:"file"`
		} `json:"jars"`
	}
	err := Unmarshal([]byte("\xef\xbb\xbf{\"id\":\"x\", // c\n \"jars\":[{\"file\":\"a.jar\"},],}"), &v)
	if err != nil || v.ID != "x" || len(v.Jars) != 1 {
		t.Fatalf("%v %+v", err, v)
	}
}
