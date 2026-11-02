// Package glossary guarda termos fixos (inglês -> português do Brasil) para
// orientar o motor e conferir a saída.
package glossary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"mtrad/internal/jsonx"
	"mtrad/internal/schema"
)

type Term struct {
	En string `json:"en"`
	Pt string `json:"pt"`
}

type Glossary struct {
	Schema  string `json:"schema"`
	Version string `json:"version"` // hash dos termos; entra na chave do cache
	Terms   []Term `json:"terms"`
}

// New ordena os termos (mais longos primeiro) e calcula a versão.
func New(terms []Term) *Glossary {
	m := map[string]string{}
	for _, t := range terms {
		en := strings.TrimSpace(t.En)
		pt := strings.TrimSpace(t.Pt)
		if en != "" && pt != "" {
			m[en] = pt
		}
	}
	g := &Glossary{Schema: schema.VGlossary}
	for en, pt := range m {
		g.Terms = append(g.Terms, Term{en, pt})
	}
	sort.Slice(g.Terms, func(i, j int) bool {
		if len(g.Terms[i].En) != len(g.Terms[j].En) {
			return len(g.Terms[i].En) > len(g.Terms[j].En)
		}
		return g.Terms[i].En < g.Terms[j].En
	})
	h := sha256.New()
	for _, t := range g.Terms {
		h.Write([]byte(t.En + "\x00" + t.Pt + "\x00"))
	}
	g.Version = hex.EncodeToString(h.Sum(nil))[:12]
	return g
}

func Load(path string) (*Glossary, error) {
	var g Glossary
	if err := schema.ReadJSON(path, &g, schema.VGlossary); err != nil {
		return nil, err
	}
	return &g, nil
}

func (g *Glossary) Save(path string) error { return schema.WriteJSON(path, g) }

// ParseTSV lê linhas "inglês<TAB>português"; linhas vazias e as que começam
// com # são ignoradas.
func ParseTSV(b []byte) ([]Term, error) {
	var out []Term
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 {
			return nil, fmt.Errorf("linha %d: esperava inglês<TAB>português", i+1)
		}
		out = append(out, Term{f[0], f[1]})
	}
	return out, nil
}

// FromLangFiles cria termos casando en_us e pt_br do Minecraft: só chaves de
// nomes curtos (itens, blocos, entidades, efeitos...) e de uma a quatro
// palavras, para não virar frase.
func FromLangFiles(enJSON, ptJSON []byte) ([]Term, error) {
	en, err := jsonx.Parse(enJSON)
	if err != nil {
		return nil, fmt.Errorf("en_us: %w", err)
	}
	pt, err := jsonx.Parse(ptJSON)
	if err != nil {
		return nil, fmt.Errorf("pt_br: %w", err)
	}
	ptm := pt.Map()
	prefixes := []string{"item.", "block.", "entity.", "effect.", "enchantment.", "biome."}
	var out []Term
	for _, p := range en.Pairs {
		ok := false
		for _, pre := range prefixes {
			if strings.HasPrefix(p.Key, pre) {
				ok = true
			}
		}
		if !ok || strings.Contains(p.Text, "%") || strings.HasSuffix(p.Key, ".desc") || strings.Contains(p.Key, ".tooltip") {
			continue
		}
		if n := len(strings.Fields(p.Text)); n < 1 || n > 4 {
			continue
		}
		if q, has := ptm[p.Key]; has && q != p.Text {
			out = append(out, Term{p.Text, q})
		}
	}
	return out, nil
}

func isWordChar(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// Find devolve os termos que aparecem em text (palavra inteira, sem
// diferenciar maiúsculas).
func (g *Glossary) Find(text string) []Term {
	low := strings.ToLower(text)
	var out []Term
	for _, t := range g.Terms {
		en := strings.ToLower(t.En)
		from := 0
		for {
			i := strings.Index(low[from:], en)
			if i < 0 {
				break
			}
			i += from
			end := i + len(en)
			before := i == 0 || !isWordChar(lastRune(low[:i]))
			after := end >= len(low) || !isWordChar(firstRune(low[end:]))
			if before && after {
				out = append(out, t)
				break
			}
			from = i + 1
		}
	}
	return out
}

func lastRune(s string) rune {
	r := []rune(s)
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// fold põe em minúsculas e tira os acentos do português, para "Ferro" e "ferro",
// "Pá" e "pa" contarem como o mesmo termo.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'á', 'à', 'â', 'ã', 'ä':
			r = 'a'
		case 'é', 'è', 'ê', 'ë':
			r = 'e'
		case 'í', 'ì', 'î', 'ï':
			r = 'i'
		case 'ó', 'ò', 'ô', 'õ', 'ö':
			r = 'o'
		case 'ú', 'ù', 'û', 'ü':
			r = 'u'
		case 'ç':
			r = 'c'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Missing devolve os termos do inglês cuja tradução não aparece no português.
// Para não encher o relatório de falsos avisos:
//   - um termo curto contido num termo mais longo já respeitado ("Iron" dentro
//     de "Iron Ingot") não conta;
//   - uma ocorrência que só aparece como parte de um nome próprio (duas ou mais
//     palavras seguidas começando com maiúscula, no meio da frase) não conta;
//   - acento e maiúscula não diferenciam.
func (g *Glossary) Missing(en, pt string) []Term {
	low := fold(pt)
	found := g.Find(en) // do mais longo para o mais curto
	var satisfied []Term
	var miss []Term
	for _, t := range found {
		if strings.Contains(low, fold(t.Pt)) {
			satisfied = append(satisfied, t)
			continue
		}
		covered := false
		for _, s := range satisfied {
			if len(s.En) > len(t.En) && strings.Contains(strings.ToLower(s.En), strings.ToLower(t.En)) {
				covered = true
				break
			}
		}
		if covered || onlyInProperName(en, t.En) {
			continue
		}
		miss = append(miss, t)
	}
	return miss
}

var capStop = map[string]bool{"the": true, "a": true, "an": true, "this": true, "that": true, "these": true, "those": true,
	"your": true, "you": true, "when": true, "if": true, "to": true, "and": true, "or": true, "of": true, "in": true,
	"on": true, "for": true, "with": true, "from": true, "by": true, "it": true, "can": true, "must": true}

// onlyInProperName diz se todas as ocorrências do termo no texto estão dentro
// de uma sequência de palavras capitalizadas maior que o próprio termo
// ("Create Mechanical Press" para o termo "Press"; "Applied Energistics" para
// "Applied"): nomes de mods e de itens próprios, que o jogador reconhece em
// inglês. Palavras comuns capitalizadas no começo da frase ("The", "This") não
// contam como parte do nome.
func onlyInProperName(text, term string) bool {
	words := strings.Fields(text)
	termWords := strings.Fields(term)
	if len(termWords) == 0 {
		return false
	}
	clean := func(w string) string { return strings.Trim(w, ".,;:!?()[]{}\"'“”‘’") }
	cap := func(w string) bool {
		c := clean(w)
		if c == "" || capStop[strings.ToLower(c)] {
			return false
		}
		r, _ := utf8.DecodeRuneInString(c)
		return unicode.IsUpper(r)
	}
	found := false
	for i := 0; i+len(termWords) <= len(words); i++ {
		match := true
		for j, tw := range termWords {
			if strings.ToLower(clean(words[i+j])) != strings.ToLower(tw) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		found = true
		lo, hi := i, i+len(termWords)-1
		for lo > 0 && cap(words[lo-1]) {
			lo--
		}
		for hi+1 < len(words) && cap(words[hi+1]) {
			hi++
		}
		allCap := true
		for k := i; k < i+len(termWords); k++ {
			if !cap(words[k]) {
				allCap = false
			}
		}
		if !allCap || hi-lo+1 <= len(termWords) {
			return false // esta ocorrência está solta: o aviso vale
		}
	}
	return found
}
