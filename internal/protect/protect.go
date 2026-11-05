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

// Classes de marca. Dizem o que a marca faz no texto, para quem precisar
// decidir diferente por tipo (hoje ninguém decide: MaskKind trata todas igual).
const (
	ClassPlaceholder = "placeholder" // %s %d %1$s {0} {nome}: carrega um valor
	ClassStyle       = "style"       // §a §l &6 &#RRGGBB: abre cor ou estilo
	ClassReset       = "reset"       // §r &r: volta ao estilo padrão
	ClassTagOpen     = "tag-open"    // <b>
	ClassTagClose    = "tag-close"   // </b>
	ClassTagVoid     = "tag-void"    // <br/> <lang:key/>
	ClassMacro       = "macro"       // $(l:...) e outras macros do Patchouli
	ClassBreak       = "break"       // quebra de linha, \n escrito, $(br), {@pagebreak}
	ClassURL         = "url"         // https://...
	ClassCommand     = "command"     // {image:...} {open_url:...} do FTB Quests
)

// Mark é uma marca achada no texto: onde fica, o trecho original e a classe.
type Mark struct {
	Start, End int // posição em bytes: text[Start:End] == Raw
	Raw        string
	Class      string
}

// Marks acha as marcas de um texto, na ordem em que aparecem. É a mesma busca
// que MaskKind sempre fez (kind "" = arquivo de idioma); só acrescenta a classe.
func Marks(text, kind string) []Mark {
	locs := reFor(kind).FindAllStringIndex(text, -1)
	out := make([]Mark, 0, len(locs))
	for _, l := range locs {
		raw := text[l[0]:l[1]]
		out = append(out, Mark{Start: l[0], End: l[1], Raw: raw, Class: classify(raw)})
	}
	return out
}

// classify põe uma marca já reconhecida numa classe. Só olha o próprio trecho.
// Das macros do Patchouli, só $(br) e $(br2) têm classe própria; as demais
// ($(l:...), $(/l), $()) ficam como macro, porque não conferi o que cada uma
// faz na documentação do Patchouli.
func classify(raw string) string {
	switch {
	case strings.HasPrefix(raw, "$("):
		if raw == "$(br)" || raw == "$(br2)" {
			return ClassBreak
		}
		return ClassMacro
	case strings.HasPrefix(raw, "%"):
		return ClassPlaceholder
	case strings.HasPrefix(raw, "§"), strings.HasPrefix(raw, "&"):
		if strings.HasSuffix(raw, "r") || strings.HasSuffix(raw, "R") {
			if !strings.Contains(raw, "#") {
				return ClassReset
			}
		}
		return ClassStyle
	case strings.HasPrefix(raw, "</"):
		return ClassTagClose
	case strings.HasPrefix(raw, "<"):
		if strings.HasSuffix(raw, "/>") {
			return ClassTagVoid
		}
		return ClassTagOpen
	case strings.HasPrefix(raw, "{@"):
		return ClassBreak
	case strings.HasPrefix(raw, "{"):
		if strings.Contains(raw, ":") {
			return ClassCommand
		}
		return ClassPlaceholder
	case strings.HasPrefix(raw, "http"):
		return ClassURL
	case raw == `\n`, strings.HasPrefix(raw, "\n"), strings.HasPrefix(raw, "\r"):
		return ClassBreak
	}
	return ClassMacro
}

// MaskKind é Mask com as marcas extras da fonte (kind "" = arquivo de idioma).
func MaskKind(text, style, kind string) (string, []schema.Token) {
	marks := Marks(text, kind)
	if len(marks) == 0 {
		return text, nil
	}
	var b strings.Builder
	var toks []schema.Token
	last := 0
	for i, m := range marks {
		b.WriteString(text[last:m.Start])
		t := marker(style, i+1)
		b.WriteString(t)
		toks = append(toks, schema.Token{T: t, Raw: m.Raw, Class: m.Class})
		last = m.End
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
