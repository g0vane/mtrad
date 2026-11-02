// Package relaxed lê JSON "frouxo" (comentários, aspas simples, chaves sem
// aspas, vírgulas opcionais) e SNBT (o formato de texto do Minecraft usado
// pelo FTB Quests) para uma árvore em ordem, deixa trocar textos por caminho e
// grava de volta em JSON ou em SNBT.
//
// Só textos são interpretados. Números, true, false, null e palavras soltas
// ficam como Raw, exatamente como estavam (1b, 2.0f, 5L continuam iguais).
package relaxed

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Kind int

const (
	Obj Kind = iota
	Arr
	Str
	Raw
)

type Pair struct {
	Key string
	Val *Node
}

type Node struct {
	Kind   Kind
	Str    string  // Kind Str
	Raw    string  // Kind Raw
	Pairs  []Pair  // Kind Obj
	Items  []*Node // Kind Arr
	Prefix string  // Kind Arr: "I", "B" ou "L" em [I; 1, 2]
	Dups   int     // só na raiz: chaves repetidas (a última vale)
}

const maxDepth = 200

type parser struct {
	b    []byte
	i    int
	dups int
}

// Parse lê um documento inteiro. Texto depois do valor é erro.
func Parse(b []byte) (*Node, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	p := &parser{b: b}
	p.skip()
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.i < len(p.b) {
		return nil, p.errf("texto depois do fim do documento")
	}
	n.Dups = p.dups
	return n, nil
}

func (p *parser) errf(format string, a ...any) error {
	line, col := 1, 1
	for _, c := range p.b[:min(p.i, len(p.b))] {
		if c == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return fmt.Errorf("linha %d, coluna %d: %s", line, col, fmt.Sprintf(format, a...))
}

// skip pula espaços, vírgulas e comentários.
func (p *parser) skip() {
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.i++
		case c == '/' && p.i+1 < len(p.b) && p.b[p.i+1] == '/', c == '#' && p.lineStart():
			// "//" em qualquer lugar; "#" só no começo da linha (o leitor do
			// FTB Library descarta as linhas que começam com "//" ou "#")
			for p.i < len(p.b) && p.b[p.i] != '\n' {
				p.i++
			}
		case c == '/' && p.i+1 < len(p.b) && p.b[p.i+1] == '*':
			end := bytes.Index(p.b[p.i+2:], []byte("*/"))
			if end < 0 {
				p.i = len(p.b)
			} else {
				p.i += end + 4
			}
		default:
			return
		}
	}
}

// lineStart diz se só há espaços entre o começo da linha e a posição atual.
func (p *parser) lineStart() bool {
	for j := p.i - 1; j >= 0; j-- {
		switch p.b[j] {
		case ' ', '\t', '\r':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

func (p *parser) skipSep() {
	for {
		p.skip()
		if p.i < len(p.b) && p.b[p.i] == ',' {
			p.i++
			continue
		}
		return
	}
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ':', '=', '[', ']', '{', '}', '"', '\'':
		return true
	}
	return false
}

func (p *parser) bare() string {
	st := p.i
	for p.i < len(p.b) && !isDelim(p.b[p.i]) {
		p.i++
	}
	return string(p.b[st:p.i])
}

func (p *parser) value(depth int) (*Node, error) {
	if depth > maxDepth {
		return nil, p.errf("aninhamento profundo demais")
	}
	if p.i >= len(p.b) {
		return nil, p.errf("fim inesperado do documento")
	}
	switch c := p.b[p.i]; c {
	case '{':
		return p.object(depth)
	case '[':
		return p.array(depth)
	case '"', '\'':
		s, err := p.quoted()
		if err != nil {
			return nil, err
		}
		return &Node{Kind: Str, Str: s}, nil
	case ']', '}', ',', ':', '=':
		return nil, p.errf("%q onde devia haver um valor", c)
	}
	w := p.bare()
	if w == "" {
		return nil, p.errf("valor inválido")
	}
	return &Node{Kind: Raw, Raw: w}, nil
}

func (p *parser) object(depth int) (*Node, error) {
	p.i++ // {
	n := &Node{Kind: Obj}
	idx := map[string]int{}
	for {
		p.skipSep()
		if p.i >= len(p.b) {
			return nil, p.errf("objeto sem }")
		}
		if p.b[p.i] == '}' {
			p.i++
			return n, nil
		}
		var key string
		if c := p.b[p.i]; c == '"' || c == '\'' {
			k, err := p.quoted()
			if err != nil {
				return nil, err
			}
			key = k
		} else {
			key = p.bare()
			if key == "" {
				return nil, p.errf("chave inválida")
			}
		}
		p.skip()
		if p.i >= len(p.b) || (p.b[p.i] != ':' && p.b[p.i] != '=') {
			return nil, p.errf("falta : depois da chave %q", key)
		}
		p.i++
		p.skip()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if j, dup := idx[key]; dup {
			n.Pairs[j].Val = v
			p.dups++
			continue
		}
		idx[key] = len(n.Pairs)
		n.Pairs = append(n.Pairs, Pair{key, v})
	}
}

func (p *parser) array(depth int) (*Node, error) {
	p.i++ // [
	n := &Node{Kind: Arr}
	// SNBT: [I; 1, 2, 3]
	save := p.i
	p.skip()
	if p.i+1 < len(p.b) && (p.b[p.i] == 'I' || p.b[p.i] == 'B' || p.b[p.i] == 'L') {
		j := p.i + 1
		for j < len(p.b) && (p.b[j] == ' ' || p.b[j] == '\t') {
			j++
		}
		if j < len(p.b) && p.b[j] == ';' {
			n.Prefix = string(p.b[p.i])
			p.i = j + 1
		} else {
			p.i = save
		}
	} else {
		p.i = save
	}
	for {
		p.skipSep()
		if p.i >= len(p.b) {
			return nil, p.errf("lista sem ]")
		}
		if p.b[p.i] == ']' {
			p.i++
			return n, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.Items = append(n.Items, v)
	}
}

func (p *parser) quoted() (string, error) {
	q := p.b[p.i]
	p.i++
	var sb strings.Builder
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case c == q:
			p.i++
			return sb.String(), nil
		case c == '\\':
			p.i++
			if p.i >= len(p.b) {
				return "", p.errf("texto termina em \\")
			}
			e := p.b[p.i]
			p.i++
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 's':
				sb.WriteByte(' ')
			case '0':
				sb.WriteByte(0)
			case '\\', '"', '\'', '/':
				sb.WriteByte(e)
			case '\n':
				// continuação de linha (JSON5)
			case '\r':
				if p.i < len(p.b) && p.b[p.i] == '\n' {
					p.i++
				}
			case 'u', 'U', 'x':
				n := map[byte]int{'u': 4, 'U': 8, 'x': 2}[e]
				if p.i+n > len(p.b) {
					return "", p.errf("escape \\%c incompleto", e)
				}
				v, err := strconv.ParseUint(string(p.b[p.i:p.i+n]), 16, 32)
				if err != nil {
					return "", p.errf("escape \\%c inválido", e)
				}
				p.i += n
				r := rune(v)
				// par substituto 😀
				if e == 'u' && r >= 0xD800 && r < 0xDC00 && p.i+6 <= len(p.b) && p.b[p.i] == '\\' && p.b[p.i+1] == 'u' {
					if lo, err := strconv.ParseUint(string(p.b[p.i+2:p.i+6]), 16, 32); err == nil && lo >= 0xDC00 && lo < 0xE000 {
						r = 0x10000 + (r-0xD800)<<10 + (rune(lo) - 0xDC00)
						p.i += 6
					}
				}
				if !utf8.ValidRune(r) {
					r = utf8.RuneError
				}
				sb.WriteRune(r)
			default:
				return "", p.errf("escape desconhecido \\%c", e)
			}
		default:
			sb.WriteByte(c)
			p.i++
		}
	}
	return "", p.errf("texto sem aspas de fechamento")
}

// ---- caminhos ----

func escSeg(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescSeg(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

// Child devolve o caminho de um filho: Child("/a", "b") = "/a/b".
func Child(ptr, seg string) string { return ptr + "/" + escSeg(seg) }

func (n *Node) step(seg string) *Node {
	switch n.Kind {
	case Obj:
		for _, p := range n.Pairs {
			if p.Key == seg {
				return p.Val
			}
		}
	case Arr:
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(n.Items) && strconv.Itoa(i) == seg {
			return n.Items[i]
		}
	}
	return nil
}

// Get acha o nó de um caminho ("" é a raiz). Devolve nil se não existir.
func (n *Node) Get(ptr string) *Node {
	if ptr == "" {
		return n
	}
	if ptr[0] != '/' {
		return nil
	}
	cur := n
	for _, seg := range strings.Split(ptr[1:], "/") {
		if cur = cur.step(unescSeg(seg)); cur == nil {
			return nil
		}
	}
	return cur
}

// SetString troca o texto no caminho. Falha se o nó não existe ou não é texto.
func (n *Node) SetString(ptr, s string) error {
	t := n.Get(ptr)
	if t == nil {
		return fmt.Errorf("caminho %s não existe", ptr)
	}
	if t.Kind != Str {
		return fmt.Errorf("caminho %s não é um texto", ptr)
	}
	t.Str = s
	return nil
}

// Walk chama fn para cada texto, em ordem, com o caminho dele.
func (n *Node) Walk(fn func(ptr string, s *Node)) { n.walk("", fn) }

func (n *Node) walk(ptr string, fn func(string, *Node)) {
	switch n.Kind {
	case Str:
		fn(ptr, n)
	case Obj:
		for _, p := range n.Pairs {
			p.Val.walk(Child(ptr, p.Key), fn)
		}
	case Arr:
		for i, it := range n.Items {
			it.walk(Child(ptr, strconv.Itoa(i)), fn)
		}
	}
}

// Clone copia a árvore.
func (n *Node) Clone() *Node {
	c := *n
	if n.Pairs != nil {
		c.Pairs = make([]Pair, len(n.Pairs))
		for i, p := range n.Pairs {
			c.Pairs[i] = Pair{p.Key, p.Val.Clone()}
		}
	}
	if n.Items != nil {
		c.Items = make([]*Node, len(n.Items))
		for i, it := range n.Items {
			c.Items[i] = it.Clone()
		}
	}
	return &c
}

// ---- gravação ----

type Format int

const (
	JSON Format = iota
	SNBT
)

// Write grava a árvore. JSON: recuo de 2 espaços; textos com acentos saem
// como estão (UTF-8). Raw que não é número, true, false ou null em JSON sai
// como texto entre aspas. SNBT: tabulação, itens separados por vírgula e
// quebra de linha, chaves sempre entre aspas.
func Write(n *Node, f Format) ([]byte, error) {
	var b bytes.Buffer
	if err := write(&b, n, f, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func indent(b *bytes.Buffer, f Format, d int) {
	if f == SNBT {
		b.WriteString(strings.Repeat("\t", d))
	} else {
		b.WriteString(strings.Repeat("  ", d))
	}
}

func write(b *bytes.Buffer, n *Node, f Format, d int) error {
	switch n.Kind {
	case Str:
		b.WriteString(quote(n.Str, f))
	case Raw:
		if f == JSON && !jsonRaw(n.Raw) {
			b.WriteString(quote(n.Raw, f))
		} else {
			b.WriteString(n.Raw)
		}
	case Obj:
		if len(n.Pairs) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, p := range n.Pairs {
			indent(b, f, d+1)
			b.WriteString(quote(p.Key, f))
			b.WriteString(": ")
			if err := write(b, p.Val, f, d+1); err != nil {
				return err
			}
			if i < len(n.Pairs)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, f, d)
		b.WriteByte('}')
	case Arr:
		if f == JSON && n.Prefix != "" {
			return errors.New("lista tipada de SNBT não cabe em JSON")
		}
		head := "["
		if n.Prefix != "" {
			head = "[" + n.Prefix + "; "
		}
		if len(n.Items) == 0 {
			b.WriteString(head + "]")
			return nil
		}
		if n.Prefix != "" {
			b.WriteString(head)
			for i, it := range n.Items {
				if i > 0 {
					b.WriteString(", ")
				}
				if err := write(b, it, f, d+1); err != nil {
					return err
				}
			}
			b.WriteByte(']')
			return nil
		}
		b.WriteString("[\n")
		for i, it := range n.Items {
			indent(b, f, d+1)
			if err := write(b, it, f, d+1); err != nil {
				return err
			}
			if i < len(n.Items)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, f, d)
		b.WriteByte(']')
	}
	return nil
}

func jsonRaw(s string) bool {
	if s == "true" || s == "false" || s == "null" {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	// ParseFloat aceita "0x1p-2", "inf", "1_0"; JSON não
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E') {
			return false
		}
	}
	return true
}

func quote(s string, f Format) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// WriteCompact grava a árvore como JSON numa linha só, sem espaços (para
// guardar um JSON dentro de um texto, como as linhas de texto rico do FTB
// Quests). Lista tipada de SNBT não cabe em JSON.
func WriteCompact(n *Node) ([]byte, error) {
	var b bytes.Buffer
	if err := writeCompact(&b, n); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeCompact(b *bytes.Buffer, n *Node) error {
	switch n.Kind {
	case Str:
		b.WriteString(quote(n.Str, JSON))
	case Raw:
		if !jsonRaw(n.Raw) {
			b.WriteString(quote(n.Raw, JSON))
		} else {
			b.WriteString(n.Raw)
		}
	case Obj:
		b.WriteByte('{')
		for i, p := range n.Pairs {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(quote(p.Key, JSON))
			b.WriteByte(':')
			if err := writeCompact(b, p.Val); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case Arr:
		if n.Prefix != "" {
			return errors.New("lista tipada de SNBT não cabe em JSON")
		}
		b.WriteByte('[')
		for i, it := range n.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCompact(b, it); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	}
	return nil
}
