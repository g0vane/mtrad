// Package diff separa o que já tem tradução do que ainda precisa de uma.
package diff

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"mtrad/internal/schema"
)

type Kind int

const (
	Reuse Kind = iota
	TodoMissing
	TodoIdentical
)

// Texto sem letras (só marcas, números, símbolos) não precisa de tradução.
var noise = regexp.MustCompile(`%(?:\d+\$)?[-#+0,(]*\d*(?:\.\d+)?[a-zA-Z%]|§.|\$\([^)]*\)|</?[A-Za-z][^<>\n]*>|\{[A-Za-z0-9_.]*\}|https?://\S+`)

// Marcas só do FTB Quests: "&a" e "{image:...}".
var noiseFTBQ = regexp.MustCompile(`[&§]#[0-9a-fA-F]{6}|&[0-9a-fk-or]|\{@[a-z_]+\}|\{[a-z_]+:[^}\n]*\}`)

// Especificador de formato (%s, %d, %1$s): textos como "%s / %s B" ou "%s%s/t"
// só têm unidades e não precisam de modelo (ele estraga as marcas).
var reSpec = regexp.MustCompile(`%(?:\d+\$)?[-#+0,(]*\d*(?:\.\d+)?[a-zA-Z]`)

func hasLetters(s, kind string) bool {
	hasSpec := reSpec.MatchString(s)
	s = noise.ReplaceAllString(s, "")
	if schema.IsFTBQ(kind) {
		s = noiseFTBQ.ReplaceAllString(s, "")
	}
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	if hasSpec && n <= 2 {
		return false // só unidade ao lado de %s: "/t", "B", "m"
	}
	return n > 0
}

// Classify devolve o destino de um texto e, para Reuse, o texto em pt_br a usar.
func Classify(r schema.StringRec) (Kind, string) {
	have := r.Pt != nil && strings.TrimSpace(*r.Pt) != ""
	if have && *r.Pt != r.En {
		return Reuse, *r.Pt
	}
	if !hasLetters(r.En, r.Kind) {
		if have {
			return Reuse, *r.Pt
		}
		return Reuse, r.En
	}
	if have {
		return TodoIdentical, ""
	}
	return TodoMissing, ""
}

// Filter limita o que vai para a tradução. Only e Skip comparam o nome do mod
// (o id do mod; "kubejs" para os textos de kubejs/assets), sem diferenciar
// maiúsculas. Só afeta os textos a traduzir: o que já tem tradução continua
// sendo reaproveitado. Only vazio = todos.
type Filter struct {
	Only, Skip []string
}

func (f Filter) allows(mod string) bool {
	m := strings.ToLower(mod)
	for _, s := range f.Skip {
		if strings.ToLower(s) == m {
			return false
		}
	}
	if len(f.Only) == 0 {
		return true
	}
	for _, s := range f.Only {
		if strings.ToLower(s) == m {
			return true
		}
	}
	return false
}

type Counts struct {
	Filtered                          int // a traduzir, mas fora do filtro (ficam em inglês)
	Total, Reused, Missing, Identical int
}

// Fontes de texto: strings.jsonl (arquivos de idioma, etapa extract) e
// strings.<fonte>.jsonl (patchouli, ftbq...), cada uma com a etapa que a gerou.
func sources(workDir string) ([][2]string, error) {
	var out [][2]string // arquivo, etapa
	if _, err := os.Stat(filepath.Join(workDir, "strings.jsonl")); err == nil {
		out = append(out, [2]string{"strings.jsonl", "extract"})
	}
	m, err := filepath.Glob(filepath.Join(workDir, "strings.*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(m)
	for _, f := range m {
		b := filepath.Base(f)
		stage := strings.TrimSuffix(strings.TrimPrefix(b, "strings."), ".jsonl")
		out = append(out, [2]string{b, stage})
	}
	return out, nil
}

// Run lê strings.jsonl e strings.<fonte>.jsonl e grava todo.jsonl e reused.jsonl.
func Run(workDir string, f Filter) (Counts, error) {
	var c Counts
	srcs, err := sources(workDir)
	if err != nil {
		return c, err
	}
	if len(srcs) == 0 {
		return c, fmt.Errorf("nenhum strings*.jsonl em %s; rode extract (ou patchouli, ftbq)", workDir)
	}
	var inputs []string
	for _, s := range srcs {
		if _, err := schema.RequireFresh(workDir, s[1], s[0]); err != nil {
			return c, err
		}
		inputs = append(inputs, s[0])
	}
	todo, err := schema.Create(filepath.Join(workDir, "todo.jsonl"), schema.VTodo)
	if err != nil {
		return c, err
	}
	reused, err := schema.Create(filepath.Join(workDir, "reused.jsonl"), schema.VReused)
	if err != nil {
		todo.Abort()
		return c, err
	}
	seen := map[string]bool{}
	for _, s := range srcs {
		err = schema.Each(filepath.Join(workDir, s[0]), schema.VStrings, func(r schema.StringRec) error {
			if seen[r.ID] {
				return fmt.Errorf("%s: id %s (%s %s) já veio de outra fonte", s[0], r.ID, r.NS, r.Key)
			}
			seen[r.ID] = true
			c.Total++
			k, pt := Classify(r)
			switch k {
			case Reuse:
				c.Reused++
				return reused.Write(schema.ReusedRec{ID: r.ID, Mod: r.Mod, NS: r.NS, Key: r.Key, Pt: pt, Kind: r.Kind, Loc: r.Loc})
			case TodoMissing, TodoIdentical:
				if !f.allows(r.Mod) {
					c.Filtered++
					return nil
				}
				if k == TodoIdentical {
					c.Identical++
					return todo.Write(schema.TodoRec{ID: r.ID, Mod: r.Mod, NS: r.NS, Key: r.Key, Reason: "identical", En: r.En, EnSha: r.EnSha, Kind: r.Kind, Loc: r.Loc})
				}
				c.Missing++
				return todo.Write(schema.TodoRec{ID: r.ID, Mod: r.Mod, NS: r.NS, Key: r.Key, Reason: "missing", En: r.En, EnSha: r.EnSha, Kind: r.Kind, Loc: r.Loc})
			}
			return nil
		})
		if err != nil {
			todo.Abort()
			reused.Abort()
			return c, err
		}
	}
	if err := todo.Close(); err != nil {
		return c, err
	}
	if err := reused.Close(); err != nil {
		return c, err
	}
	err = schema.WriteDone(workDir, "diff", inputs, []string{"todo.jsonl", "reused.jsonl"},
		map[string]int{"total": c.Total, "reused": c.Reused, "missing": c.Missing, "identical": c.Identical, "filtered": c.Filtered})
	return c, err
}

// ModStat resume os textos de um mod que precisam de tradução (com ou sem
// filtro: serve para a lista "quais mods ficam em inglês").
type ModStat struct {
	Mod   string
	Texts int
	// Repetitive: quase todos os textos são nomes curtos feitos de poucas
	// palavras repetidas ("<material> <forma>", como chipped e rechiseled).
	// É um palpite pelo conteúdo; quem decide é o usuário.
	Repetitive bool
}

type modAcc struct {
	texts, short, words int
	distinct            map[string]struct{}
}

// ModStats lê os strings*.jsonl e conta, por mod, os textos que ainda não têm
// tradução (os mesmos que o diff manda traduzir, sem aplicar only_mods e
// skip_mods). Maior primeiro.
func ModStats(workDir string) ([]ModStat, error) {
	srcs, err := sources(workDir)
	if err != nil {
		return nil, err
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("nenhum strings*.jsonl em %s; rode extract (ou patchouli, ftbq)", workDir)
	}
	acc := map[string]*modAcc{}
	for _, s := range srcs {
		err := schema.Each(filepath.Join(workDir, s[0]), schema.VStrings, func(r schema.StringRec) error {
			if k, _ := Classify(r); k == Reuse {
				return nil
			}
			a := acc[r.Mod]
			if a == nil {
				a = &modAcc{distinct: map[string]struct{}{}}
				acc[r.Mod] = a
			}
			a.texts++
			w := strings.Fields(noise.ReplaceAllString(r.En, " "))
			a.words += len(w)
			if len(w) <= 4 {
				a.short++
			}
			if len(a.distinct) < 20000 {
				for _, x := range w {
					a.distinct[strings.ToLower(strings.Trim(x, ".,:;!?()[]\"'"))] = struct{}{}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var out []ModStat
	for m, a := range acc {
		rep := a.texts >= 300 && a.short*100/a.texts >= 80 && a.words > 0 && len(a.distinct)*100/a.words <= 12
		out = append(out, ModStat{Mod: m, Texts: a.texts, Repetitive: rep})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Texts != out[j].Texts {
			return out[i].Texts > out[j].Texts
		}
		return out[i].Mod < out[j].Mod
	})
	return out, nil
}
