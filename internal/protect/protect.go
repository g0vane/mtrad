// Package protect troca %s, %1$s, §a, tags, macros do Patchouli, endereços e
// quebras de linha por marcas numeradas que o motor de tradução não deve
// mexer, e guarda o mapa para desfazer.
package protect

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mtrad/internal/schema"
)

// Estilos de marca. O melhor para cada motor sai dos testes do módulo eval.
const (
	StyleBrackets = "brackets" // ⟦1⟧
	StyleXML      = "xml"      // <x1/>
	StyleCurly    = "curly"    // {{1}}
)

// Marcas só de algumas fontes. ftbq: códigos de cor "&a" e comandos
// "{image:...}" do FTB Quests (formato NÃO conferido no código; ver doc §22).
var ftbqPatterns = []string{
	`[&§]#[0-9a-fA-F]{6}`, // cor hexadecimal: &#RRGGBB (vem antes de "&a")
	`&[0-9a-fk-or]`,       // cores e estilos
	`\{@[a-z_]+\}`,        // {@pagebreak}
	`\{[a-z_]+:[^}\n]*\}`, // {image:...} {open_url:...}
}

var extraPatterns = map[string][]string{
	schema.KindFTBQ:     ftbqPatterns,
	schema.KindFTBQInl:  ftbqPatterns,
	schema.KindFTBQLang: ftbqPatterns,
	schema.KindFTBQRich: ftbqPatterns,
}

var kindRe = map[string]*regexp.Regexp{}

func init() {
	for k, extra := range extraPatterns {
		kindRe[k] = regexp.MustCompile(strings.Join(append(append([]string{}, extra...), basePatterns...), "|"))
	}
}

// Ordem importa: o primeiro que casar na posição vence.
var basePatterns = []string{
	`\$\([^)]*\)`, // macros do Patchouli: $(l:...) $(br)
	`%(?:\d+\$|<)?[-#+0,(]*\d*(?:\.\d+)?(?:[tT][A-Za-z]|[bBhHsScCdoxXeEfgGaAn%])`, // formato Java
	`§[0-9a-fk-orxA-FK-ORX]`, // códigos de cor e estilo
	`</?[A-Za-z][^<>\n]*>`,   // tags
	`\{[A-Za-z0-9_.]*\}`,     // {0} {nome}
	`https?://[^\s]+`,        // endereços
	`\\n`,                    // \n escrito como dois caracteres
	`\r?\n`,                  // quebra de linha de verdade
}

var tokenRe = regexp.MustCompile(strings.Join(basePatterns, "|"))

func reFor(kind string) *regexp.Regexp {
	if re, ok := kindRe[kind]; ok {
		return re
	}
	return tokenRe
}

func marker(style string, n int) string {
	switch style {
	case StyleXML:
		return "<x" + strconv.Itoa(n) + "/>"
	case StyleCurly:
		return "{{" + strconv.Itoa(n) + "}}"
	default:
		return "⟦" + strconv.Itoa(n) + "⟧"
	}
}

func markerRe(style string) *regexp.Regexp {
	switch style {
	case StyleXML:
		return regexp.MustCompile(`<x\d+/>`)
	case StyleCurly:
		return regexp.MustCompile(`\{\{\d+\}\}`)
	default:
		return regexp.MustCompile(`⟦\d+⟧`)
	}
}

func ValidStyle(s string) bool {
	return s == StyleBrackets || s == StyleXML || s == StyleCurly
}

// Mask devolve o texto com marcas e o mapa de volta.
func Mask(text, style string) (string, []schema.Token) { return MaskKind(text, style, "") }

// MaskKind é Mask com as marcas extras da fonte (kind "" = arquivo de idioma).
func MaskKind(text, style, kind string) (string, []schema.Token) {
	locs := reFor(kind).FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return text, nil
	}
	var b strings.Builder
	var toks []schema.Token
	last := 0
	for i, l := range locs {
		b.WriteString(text[last:l[0]])
		m := marker(style, i+1)
		b.WriteString(m)
		toks = append(toks, schema.Token{T: m, Raw: text[l[0]:l[1]]})
		last = l[1]
	}
	b.WriteString(text[last:])
	return b.String(), toks
}

// Unmask desfaz Mask. Cada marca do mapa precisa aparecer exatamente uma vez e
// não pode haver marca desconhecida; os problemas vêm na lista (o texto sai
// mesmo assim, no melhor esforço).
func Unmask(masked string, toks []schema.Token, style string) (string, []string) {
	var problems []string
	known := map[string]bool{}
	for _, t := range toks {
		known[t.T] = true
	}
	for _, m := range markerRe(style).FindAllString(masked, -1) {
		if !known[m] {
			problems = append(problems, "marca desconhecida "+m)
		}
	}
	out := masked
	for _, t := range toks {
		switch n := strings.Count(masked, t.T); {
		case n == 0:
			problems = append(problems, "marca perdida "+t.T+" ("+t.Raw+")")
		case n > 1:
			problems = append(problems, fmt.Sprintf("marca repetida %s (%d vezes)", t.T, n))
		}
		out = strings.Replace(out, t.T, t.Raw, 1)
	}
	return out, problems
}

type Counts struct{ Texts, WithTokens, Tokens int }

// Run lê todo.jsonl e grava protected.jsonl.
func Run(workDir, style string) (Counts, error) {
	var c Counts
	if !ValidStyle(style) {
		return c, fmt.Errorf("estilo de marca desconhecido: %q", style)
	}
	if _, err := schema.RequireFresh(workDir, "diff", "todo.jsonl"); err != nil {
		return c, err
	}
	w, err := schema.Create(filepath.Join(workDir, "protected.jsonl"), schema.VProtected)
	if err != nil {
		return c, err
	}
	err = schema.Each(filepath.Join(workDir, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		masked, toks := MaskKind(r.En, style, r.Kind)
		c.Texts++
		if len(toks) > 0 {
			c.WithTokens++
			c.Tokens += len(toks)
		}
		return w.Write(schema.ProtectedRec{ID: r.ID, Masked: masked, Tokens: toks, Style: style})
	})
	if err != nil {
		w.Abort()
		return c, err
	}
	if err := w.Close(); err != nil {
		return c, err
	}
	err = schema.WriteDone(workDir, "protect", []string{"todo.jsonl"}, []string{"protected.jsonl"},
		map[string]int{"texts": c.Texts, "with_tokens": c.WithTokens, "tokens": c.Tokens})
	return c, err
}
