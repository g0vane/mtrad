// Package validate confere cada tradução contra o original e separa o que
// pode ir para o pacote (ok.jsonl) do que não pode (failed.jsonl).
//
// A conferência das marcas é refeita aqui com expressões próprias, sem usar o
// código do módulo protect: se um dos dois errar, a diferença aparece.
package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"mtrad/internal/glossary"
	"mtrad/internal/review"
	"mtrad/internal/schema"
)

var (
	reFormat = regexp.MustCompile(`%(?:\d+\$|<)?[-#+0,(]*\d*(?:\.\d+)?(?:[tT][A-Za-z]|[bBhHsScCdoxXeEfgGaAn%])`)
	reSect   = regexp.MustCompile(`§[0-9a-fk-orxA-FK-ORX]`)
	reBrace  = regexp.MustCompile(`\{[A-Za-z0-9_.]*\}`)
	rePatch  = regexp.MustCompile(`\$\([^)]*\)`)
	reTag    = regexp.MustCompile(`</?[A-Za-z][^<>\n]*>`)
	reURL    = regexp.MustCompile(`https?://[^\s]+`)
	reAmp    = regexp.MustCompile(`[&§]#[0-9a-fA-F]{6}|&[0-9a-fk-or]`)
	reCmd    = regexp.MustCompile(`\{@[a-z_]+\}|\{[a-z_]+:[^}\n]*\}`)
	reMarker = map[string]*regexp.Regexp{
		"brackets": regexp.MustCompile(`⟦\d+⟧`),
		"xml":      regexp.MustCompile(`<x\d+/>`),
		"curly":    regexp.MustCompile(`\{\{\d+\}\}`),
	}
)

var (
	reColorAny      = regexp.MustCompile(`[&§]#[0-9a-fA-F]{6}|[&§][0-9a-fk-orA-FK-OR]`)
	reColorSectOnly = regexp.MustCompile(`§#[0-9a-fA-F]{6}|§[0-9a-fk-orA-FK-OR]`)
	reManySpaces    = regexp.MustCompile(` {2,}`)
)

// notas que o modelo costuma acrescentar no fim da tradução
var modelNotes = []string{"(nota:", "(note:", "nota do tradutor", "translator's note", "(tradução:", "(translation:", "[nota:", "[note:"}

var chatter = []string{"tradução:", "translation:", "aqui está", "here is", "here's", "claro,", "claro!", "certamente", "sure,", "sure!", "com certeza"}

// signature conta as marcas de um texto por categoria.
func signature(s, kind string) map[string]int {
	m := map[string]int{}
	add := func(cat string, list []string) {
		for _, x := range list {
			m[cat+"\x00"+x]++
		}
	}
	add("fmt", reFormat.FindAllString(s, -1))
	add("sec", reSect.FindAllString(s, -1))
	add("brace", reBrace.FindAllString(s, -1))
	add("pat", rePatch.FindAllString(s, -1))
	add("tag", reTag.FindAllString(s, -1))
	add("url", reURL.FindAllString(s, -1))
	if schema.IsFTBQ(kind) {
		add("amp", reAmp.FindAllString(s, -1))
		add("cmd", reCmd.FindAllString(s, -1))
	}
	m["nl\x00"] = strings.Count(s, "\n")
	m["lit-nl\x00"] = strings.Count(s, `\n`)
	return m
}

func diffSig(a, b map[string]int) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if a[k] != b[k] {
			name := strings.ReplaceAll(k, "\x00", " ")
			out = append(out, fmt.Sprintf("%s: original %d, tradução %d", strings.TrimSpace(name), a[k], b[k]))
		}
	}
	sort.Strings(out)
	return out
}

type Options struct {
	Gloss *glossary.Glossary
	// Review: correções feitas à mão (work/review.jsonl); vazio = nenhuma.
	Review map[string]review.Entry
}

// colorState percorre os códigos de cor e formato de um texto e diz se a cor
// continua aberta no fim e se um "reset" (&r, §r) aparece antes de qualquer cor.
// A tradução pode mudar a posição dos códigos, mas não esse comportamento: um
// texto que no original termina com a cor fechada e na tradução termina aberta
// pinta o resto do tooltip inteiro.
func colorState(s, kind string) (endsOpen, closerFirst bool) {
	re := reColorSectOnly
	if schema.IsFTBQ(kind) {
		re = reColorAny
	}
	seen := false
	for _, m := range re.FindAllString(s, -1) {
		closer := strings.EqualFold(m[len(m)-1:], "r") && !strings.Contains(m, "#")
		if closer {
			if !seen {
				closerFirst = true
			}
			endsOpen = false
		} else {
			endsOpen = true
		}
		seen = true
	}
	return endsOpen, closerFirst
}

// repair conserta estragos comuns e sem ambiguidade na saída do modelo:
// a marca ⟦1⟧ escrita como [1], ⟦ 1 ⟧ ou ⟦1] (só quando a marca certa não
// aparece e a forma torta aparece uma única vez) e quebras de linha que o
// modelo inventou (o texto protegido não tem quebras reais: o protect as troca
// por marcas).
func repair(prot schema.ProtectedRec, out string) string {
	if prot.Style == "brackets" {
		for _, t := range prot.Tokens {
			if strings.Contains(out, t.T) {
				continue
			}
			n := strings.TrimSuffix(strings.TrimPrefix(t.T, "⟦"), "⟧")
			re := regexp.MustCompile(`[⟦\[]\s*` + regexp.QuoteMeta(n) + `\s*[⟧\]]`)
			if loc := re.FindAllStringIndex(out, -1); len(loc) == 1 {
				out = out[:loc[0][0]] + t.T + out[loc[0][1]:]
			}
		}
	}
	out = stripAddedQuotes(prot.Masked, out)
	if !strings.Contains(prot.Masked, "\n") {
		out = strings.Trim(out, "\r\n")
		out = strings.ReplaceAll(strings.ReplaceAll(out, "\r\n", " "), "\n", " ")
	}
	return out
}

// Check devolve o texto final (sem marcas), os motivos de falha e os avisos.
func Check(todo schema.TodoRec, prot schema.ProtectedRec, out string, o Options) (pt string, fails, warns []string) {
	out = repair(prot, out)
	// 1. desfaz as marcas, exigindo cada uma exatamente uma vez
	pt = out
	known := map[string]bool{}
	for _, t := range prot.Tokens {
		known[t.T] = true
	}
	if re := reMarker[prot.Style]; re != nil {
		for _, m := range re.FindAllString(out, -1) {
			if !known[m] {
				fails = append(fails, "marca desconhecida "+m)
			}
		}
	}
	for _, t := range prot.Tokens {
		switch n := strings.Count(out, t.T); {
		case n == 0:
			fails = append(fails, "marca perdida "+t.T+" ("+strings.ReplaceAll(t.Raw, "\n", `\n`)+")")
		case n > 1:
			fails = append(fails, fmt.Sprintf("marca repetida %s (%d vezes)", t.T, n))
		}
		pt = strings.Replace(pt, t.T, t.Raw, 1)
	}
	// espaço duplo que o modelo (ou a marca) deixou, só quando o original não os tem
	if !strings.Contains(todo.En, "  ") {
		pt = reManySpaces.ReplaceAllString(pt, " ")
	}
	// 2. vazio e caracteres estranhos
	if strings.TrimSpace(pt) == "" {
		return pt, append(fails, "texto vazio"), warns
	}
	for _, r := range pt {
		if r == utf8.RuneError || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			fails = append(fails, "caractere inválido")
			break
		}
	}
	// 3. mesmas marcas do original
	for _, d := range diffSig(signature(todo.En, todo.Kind), signature(pt, todo.Kind)) {
		fails = append(fails, "marcas diferentes do original — "+d)
	}
	// 3b. a cor fica aberta/fechada como no original
	if len(fails) == 0 {
		eo, ef := colorState(todo.En, todo.Kind)
		po, pf := colorState(pt, todo.Kind)
		if eo != po {
			if po {
				fails = append(fails, "a cor ficou aberta no fim e no original está fechada (pintaria o resto do texto)")
			} else {
				fails = append(fails, "a cor está fechada no fim e no original continua aberta")
			}
		} else if ef != pf {
			fails = append(fails, "um reset de cor (&r ou §r) aparece antes de qualquer cor, diferente do original")
		}
	}
	// 4. tamanho
	en, pr := utf8.RuneCountInString(todo.En), utf8.RuneCountInString(pt)
	if en >= 12 && (pr > 3*en+20 || pr*5 < en) {
		fails = append(fails, fmt.Sprintf("tamanho suspeito (%d caracteres contra %d no original)", pr, en))
	}
	// 5. conversa do modelo
	low := strings.ToLower(strings.TrimSpace(pt))
	lowEn := strings.ToLower(strings.TrimSpace(todo.En))
	for _, c := range chatter {
		if strings.HasPrefix(low, c) && !strings.HasPrefix(lowEn, c) {
			fails = append(fails, "parece explicação do modelo: começa com “"+c+"”")
			break
		}
	}
	for _, n := range modelNotes {
		if strings.Contains(low, n) && !strings.Contains(lowEn, n) {
			fails = append(fails, "parece nota do modelo: contém “"+n+"”")
			break
		}
	}
	if strings.Contains(pt, "```") && !strings.Contains(todo.En, "```") {
		fails = append(fails, "bloco de código na resposta")
	}
	t := strings.TrimSpace(pt)
	te := strings.TrimSpace(todo.En)
	if len([]rune(t)) > 1 && isQuote(firstRune(t)) && isQuote(lastRune(t)) && !(len(te) > 0 && isQuote(firstRune(te))) {
		fails = append(fails, "texto entre aspas que o original não tem")
	}
	// avisos
	// códigos de cor com & em arquivo de idioma (alguns mods usam &6, &r): o
	// FTB Quests já os confere como falha; aqui só avisa, porque em texto comum
	// o & pode ser só um "e" comercial seguido de letra
	if !schema.IsFTBQ(todo.Kind) {
		for _, d := range diffSig(ampCodes(todo.En), ampCodes(pt)) {
			warns = append(warns, "códigos de cor com &: "+strings.TrimPrefix(d, "amp "))
		}
	}
	if pt == todo.En && todo.Reason != "identical" && letters(todo.En) > 2 {
		warns = append(warns, "igual ao inglês")
	}
	// o glossário só avisa quando há o que dizer: texto igual ao inglês já foi
	// avisado acima, e textos de poucas letras são quase sempre nomes
	if o.Gloss != nil && pt != todo.En && letters(todo.En) > 3 {
		for _, m := range o.Gloss.Missing(todo.En, pt) {
			warns = append(warns, "glossário: “"+m.En+"” → “"+m.Pt+"” não aparece")
		}
	}
	// 6. especificadores % sem posição que o modelo trocou de ordem (normal na
	// ordem das palavras do português): o jogo entrega os argumentos na ordem
	// do original, então "Causa %d de dano a %s" mostraria o número no lugar do
	// nome. Troca por especificadores com posição (%2$d ... %1$s), que mantêm o
	// sentido que o modelo quis.
	if len(fails) == 0 {
		if pos := reorderedSpecs(prot, out); pos != nil {
			fixed := out
			for _, t := range prot.Tokens {
				raw := t.Raw
				if p, ok := pos[t.T]; ok {
					raw = p
				}
				fixed = strings.Replace(fixed, t.T, raw, 1)
			}
			if !strings.Contains(todo.En, "  ") {
				fixed = reManySpaces.ReplaceAllString(fixed, " ")
			}
			pt = fixed
			warns = append(warns, "especificadores % em outra ordem: trocados por especificadores com posição")
		}
	}
	return pt, fails, warns
}

var reAmpOnly = regexp.MustCompile(`&#[0-9a-fA-F]{6}|&[0-9a-fk-or]`)

// ampCodes conta os códigos de cor com & de um texto (no formato de signature).
func ampCodes(s string) map[string]int {
	m := map[string]int{}
	for _, c := range reAmpOnly.FindAllString(s, -1) {
		m["amp\x00"+c]++
	}
	return m
}

// rePlainSpec é um especificador % comum, sem posição (nem %n, %%, %<s ou %tY).
var rePlainSpec = regexp.MustCompile(`^%[-#+0,(]*\d*(?:\.\d+)?[bBhHsScCdoxXeEfgGaA]$`)

// reorderedSpecs devolve, para cada marca de especificador % sem posição, o
// especificador com a posição que ela tinha no original ("%d" que era o
// segundo vira "%2$d"), quando o modelo mudou a ordem deles. Devolve nil se
// a ordem não mudou, se há menos de dois, ou se o original tem algum
// especificador de outro tipo (posicional, %<s, %tY...), caso em que não dá
// para numerar com segurança.
func reorderedSpecs(prot schema.ProtectedRec, out string) map[string]string {
	var specs []schema.Token
	for _, t := range prot.Tokens {
		switch {
		case rePlainSpec.MatchString(t.Raw):
			specs = append(specs, t)
		case reFormat.MatchString(t.Raw) && t.Raw != "%%" && t.Raw != "%n":
			return nil
		}
	}
	if len(specs) < 2 {
		return nil
	}
	rank := map[string]int{}
	sort.SliceStable(specs, func(i, j int) bool {
		return strings.Index(prot.Masked, specs[i].T) < strings.Index(prot.Masked, specs[j].T)
	})
	for i, t := range specs {
		if strings.Index(prot.Masked, t.T) < 0 {
			return nil
		}
		rank[t.T] = i + 1
	}
	byOut := append([]schema.Token(nil), specs...)
	for _, t := range byOut {
		if strings.Count(out, t.T) != 1 {
			return nil // perdida ou repetida: outra conferência já reprova
		}
	}
	sort.SliceStable(byOut, func(i, j int) bool { return strings.Index(out, byOut[i].T) < strings.Index(out, byOut[j].T) })
	moved := false
	for i, t := range byOut {
		if rank[t.T] != i+1 {
			moved = true
		}
	}
	if !moved {
		return nil
	}
	res := map[string]string{}
	for _, t := range specs {
		res[t.T] = fmt.Sprintf("%%%d$%s", rank[t.T], t.Raw[1:])
	}
	return res
}

func isQuote(r rune) bool {
	return r == '"' || r == '“' || r == '”' || r == '«' || r == '»' || r == '\'' || r == '‘' || r == '’'
}

// stripAddedQuotes tira as aspas que o modelo põe em volta da tradução inteira
// quando o original não começa nem termina com aspas.
func stripAddedQuotes(src, out string) string {
	t := strings.TrimSpace(out)
	rs := []rune(t)
	if len(rs) < 3 || !isQuote(rs[0]) || !isQuote(rs[len(rs)-1]) {
		return out
	}
	ts := strings.TrimSpace(src)
	if f := firstRune(ts); ts != "" && (isQuote(f) || isQuote(lastRune(ts))) {
		return out
	}
	return string(rs[1 : len(rs)-1])
}

func firstRune(s string) rune { r, _ := utf8.DecodeRuneInString(s); return r }
func lastRune(s string) rune  { r, _ := utf8.DecodeLastRuneInString(s); return r }

func letters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

type Counts struct {
	Todo, OK, Failed, Pending, Warnings int
	// Reviewed: textos que ficaram com a correção feita à mão; ReviewStale:
	// correções ignoradas porque o texto original mudou depois delas.
	Reviewed, ReviewStale int
	// Stale: traduções feitas para uma versão antiga do texto em inglês (o hash
	// guardado na tradução não bate com o do texto protegido de hoje); contam
	// como pendentes até o translate refazê-las.
	Stale int
}

// staleTranslation diz que a tradução foi feita para outro texto de origem.
// Registros antigos sem hash (MSha vazio) valem como estavam.
func staleTranslation(r schema.TranslatedRec, p schema.ProtectedRec) bool {
	return r.MSha != "" && r.MSha != schema.Sha(p.Masked)
}

// applyReview devolve o texto da correção manual, se houver uma que ainda vale.
// A correção é final: só gera avisos (marcas diferentes do original), não
// reprova.
func applyReview(t schema.TodoRec, o Options) (pt string, warns []string, used, stale bool) {
	e, ok := o.Review[t.ID]
	if !ok || strings.TrimSpace(e.Pt) == "" {
		return "", nil, false, false
	}
	if e.EnSha != "" && e.EnSha != t.EnSha {
		return "", nil, false, true
	}
	for _, d := range diffSig(signature(t.En, t.Kind), signature(e.Pt, t.Kind)) {
		warns = append(warns, "revisão manual: marcas diferentes do original — "+d)
	}
	return e.Pt, warns, true, false
}

// Run lê todo, protected e translated e grava ok.jsonl, failed.jsonl e report.md.
func Run(workDir string, o Options) (Counts, error) {
	var c Counts
	for _, f := range [][2]string{{"diff", "todo.jsonl"}, {"protect", "protected.jsonl"}, {"translate", "translated.jsonl"}} {
		if _, err := schema.RequireFresh(workDir, f[0], f[1]); err != nil {
			return c, err
		}
	}
	todo := map[string]schema.TodoRec{}
	var order []string
	if err := schema.Each(filepath.Join(workDir, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		todo[r.ID] = r
		order = append(order, r.ID)
		return nil
	}); err != nil {
		return c, err
	}
	prot := map[string]schema.ProtectedRec{}
	if err := schema.Each(filepath.Join(workDir, "protected.jsonl"), schema.VProtected, func(r schema.ProtectedRec) error {
		prot[r.ID] = r
		return nil
	}); err != nil {
		return c, err
	}
	tr := map[string]schema.TranslatedRec{}
	if err := schema.Each(filepath.Join(workDir, "translated.jsonl"), schema.VTranslated, func(r schema.TranslatedRec) error {
		tr[r.ID] = r // a última vale
		return nil
	}); err != nil {
		return c, err
	}
	if o.Review == nil {
		rv, err := review.Load(workDir)
		if err != nil {
			return c, err
		}
		o.Review = rv
	}
	okw, err := schema.Create(filepath.Join(workDir, "ok.jsonl"), schema.VOK)
	if err != nil {
		return c, err
	}
	fw, err := schema.Create(filepath.Join(workDir, "failed.jsonl"), schema.VFailed)
	if err != nil {
		okw.Abort()
		return c, err
	}
	rep := newReport()
	c.Todo = len(order)
	for _, id := range order {
		t := todo[id]
		r, translated := tr[id]
		if pt, warns, used, stale := applyReview(t, o); used {
			c.Reviewed++
			c.OK++
			if len(warns) > 0 {
				c.Warnings++
			}
			rep.add("manual", t, pt, pt, nil, warns)
			if err := okw.Write(schema.OKRec{ID: id, Mod: t.Mod, NS: t.NS, Key: t.Key, Pt: pt, Engine: "manual", Kind: t.Kind, Loc: t.Loc}); err != nil {
				okw.Abort()
				fw.Abort()
				return c, err
			}
			continue
		} else if stale {
			c.ReviewStale++
		}
		if !translated {
			c.Pending++
			continue
		}
		p, hasProt := prot[id]
		if !hasProt {
			p = schema.ProtectedRec{ID: id, Masked: t.En}
		}
		if staleTranslation(r, p) {
			// o texto em inglês mudou depois desta tradução (mod atualizado): ela
			// não vale para o texto de hoje, nem aprovada nem reprovada
			c.Pending++
			c.Stale++
			continue
		}
		pt, fails, warns := Check(t, p, r.Out, o)
		rep.add(r.Engine, t, r.Out, pt, fails, warns)
		if len(warns) > 0 {
			c.Warnings++
		}
		if len(fails) > 0 {
			c.Failed++
			if err := fw.Write(schema.FailedRec{ID: id, NS: t.NS, Key: t.Key, Engine: r.Engine, Out: r.Out, Reason: fails, Kind: t.Kind, Loc: t.Loc}); err != nil {
				okw.Abort()
				fw.Abort()
				return c, err
			}
			continue
		}
		c.OK++
		if err := okw.Write(schema.OKRec{ID: id, Mod: t.Mod, NS: t.NS, Key: t.Key, Pt: pt, Engine: r.Engine, Kind: t.Kind, Loc: t.Loc}); err != nil {
			okw.Abort()
			fw.Abort()
			return c, err
		}
	}
	if err := okw.Close(); err != nil {
		return c, err
	}
	if err := fw.Close(); err != nil {
		return c, err
	}
	if err := writeFile(filepath.Join(workDir, "report.md"), rep.render(c)); err != nil {
		return c, err
	}
	err = schema.WriteDone(workDir, "validate", []string{"todo.jsonl", "protected.jsonl", "translated.jsonl"}, []string{"ok.jsonl", "failed.jsonl"},
		map[string]int{"todo": c.Todo, "ok": c.OK, "failed": c.Failed, "pending": c.Pending, "warnings": c.Warnings, "reviewed": c.Reviewed, "review_stale": c.ReviewStale, "stale": c.Stale})
	return c, err
}

// Render devolve a tradução como o jogador a verá (marcas ⟦n⟧ trocadas pelo
// trecho original), sem conferir nada: serve para mostrar ao revisor o que o
// modelo respondeu, mesmo quando foi reprovado.
func Render(prot schema.ProtectedRec, out string) string {
	out = repair(prot, out)
	for _, t := range prot.Tokens {
		out = strings.Replace(out, t.T, t.Raw, 1)
	}
	return out
}

// ReviewItems lista o que precisa de revisão: os reprovados, e conforme scope
// também os com aviso ("warnings") e os sem tradução ("all"). Textos que já
// têm uma correção manual válida ficam de fora.
func ReviewItems(workDir string, o Options, scope string) ([]review.Item, error) {
	todo := map[string]schema.TodoRec{}
	var order []string
	if err := schema.Each(filepath.Join(workDir, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		todo[r.ID] = r
		order = append(order, r.ID)
		return nil
	}); err != nil {
		return nil, err
	}
	prot := map[string]schema.ProtectedRec{}
	if err := schema.Each(filepath.Join(workDir, "protected.jsonl"), schema.VProtected, func(r schema.ProtectedRec) error {
		prot[r.ID] = r
		return nil
	}); err != nil {
		return nil, err
	}
	tr := map[string]schema.TranslatedRec{}
	if err := schema.Each(filepath.Join(workDir, "translated.jsonl"), schema.VTranslated, func(r schema.TranslatedRec) error {
		tr[r.ID] = r
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if o.Review == nil {
		rv, err := review.Load(workDir)
		if err != nil {
			return nil, err
		}
		o.Review = rv
	}
	var items []review.Item
	for _, id := range order {
		t := todo[id]
		if _, _, used, _ := applyReview(t, o); used {
			continue
		}
		it := review.Item{ID: id, Mod: t.Mod, Key: t.Key, En: t.En}
		r, translated := tr[id]
		if !translated {
			if scope == "all" {
				it.Status = "pendente"
				items = append(items, it)
			}
			continue
		}
		p, ok := prot[id]
		if !ok {
			p = schema.ProtectedRec{ID: id, Masked: t.En}
		}
		if staleTranslation(r, p) {
			if scope == "all" {
				it.Status = "pendente"
				items = append(items, it)
			}
			continue
		}
		pt, fails, warns := Check(t, p, r.Out, o)
		switch {
		case len(fails) > 0:
			it.Status, it.Reasons, it.Current = "reprovado", fails, Render(p, r.Out)
		case len(warns) > 0 && (scope == "warnings" || scope == "all"):
			it.Status, it.Reasons, it.Current = "aviso", warns, pt
		default:
			continue
		}
		items = append(items, it)
	}
	return items, nil
}
