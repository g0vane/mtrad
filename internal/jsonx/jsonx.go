// Package jsonx lê JSON "tolerante" (BOM, comentários // e /* */, vírgula
// final) e grava JSON comum. Para arquivos de idioma do Minecraft.
// Não é JSON5 completo: aspas simples e chaves sem aspas não são aceitas.
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Pair struct {
	Key  string
	Text string
}

// Doc é um objeto de nível único, com a ordem das chaves preservada.
type Doc struct {
	Pairs     []Pair
	NonString int  // valores que não são texto (ignorados)
	Dups      int  // chaves repetidas (a última vale)
	Lenient   bool // só foi lido depois de limpar comentários etc.
}

func (d *Doc) Map() map[string]string {
	m := make(map[string]string, len(d.Pairs))
	for _, p := range d.Pairs {
		m[p.Key] = p.Text
	}
	return m
}

// Parse lê um arquivo de idioma.
func Parse(b []byte) (*Doc, error) {
	d, err := parseStrict(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
	if err == nil {
		return d, nil
	}
	d2, err2 := parseStrict(Clean(b))
	if err2 != nil {
		return nil, err2
	}
	d2.Lenient = true
	return d2, nil
}

// Unmarshal lê qualquer JSON tolerante (por exemplo fabric.mod.json).
func Unmarshal(b []byte, v any) error {
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), v); err == nil {
		return nil
	}
	return json.Unmarshal(Clean(b), v)
}

func parseStrict(data []byte) (*Doc, error) {
	d := &Doc{}
	dec := json.NewDecoder(bytes.NewReader(data))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if dl, ok := t.(json.Delim); !ok || dl != '{' {
		return nil, fmt.Errorf("a raiz não é um objeto")
	}
	idx := map[string]int{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("chave inválida")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			d.NonString++
			continue
		}
		if i, seen := idx[key]; seen {
			d.Dups++
			d.Pairs[i].Text = s
			continue
		}
		idx[key] = len(d.Pairs)
		d.Pairs = append(d.Pairs, Pair{key, s})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("texto depois do objeto")
	}
	return d, nil
}

// Clean remove BOM, comentários e vírgulas finais, sem mexer dentro de textos.
func Clean(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var out bytes.Buffer
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(b) {
				i++
				out.WriteByte(b[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	s := out.Bytes()
	var fin bytes.Buffer
	inStr = false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			fin.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				fin.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		fin.WriteByte(c)
	}
	return fin.Bytes()
}

// Marshal grava um objeto de textos como JSON comum, uma chave por linha,
// sem escapar <, > e &, mantendo os acentos.
func Marshal(pairs []Pair) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, p := range pairs {
		b.WriteString("  ")
		b.WriteString(quote(p.Key))
		b.WriteString(": ")
		b.WriteString(quote(p.Text))
		if i < len(pairs)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}
