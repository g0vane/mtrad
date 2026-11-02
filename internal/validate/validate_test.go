package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtrad/internal/glossary"
	"mtrad/internal/review"
	"mtrad/internal/schema"
)

func prot(masked string, toks ...[2]string) schema.ProtectedRec {
	p := schema.ProtectedRec{ID: "x", Masked: masked, Style: "brackets"}
	for _, t := range toks {
		p.Tokens = append(p.Tokens, schema.Token{T: t[0], Raw: t[1]})
	}
	return p
}

func TestCheck(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "Hello %s, §atake this§r\nbye", Reason: "missing"}
	p := prot("Hello ⟦1⟧, ⟦2⟧take this⟦3⟧⟦4⟧bye", [2]string{"⟦1⟧", "%s"}, [2]string{"⟦2⟧", "§a"}, [2]string{"⟦3⟧", "§r"}, [2]string{"⟦4⟧", "\n"})

	pt, fails, _ := Check(todo, p, "Olá ⟦1⟧, ⟦2⟧pegue isto⟦3⟧⟦4⟧tchau", Options{})
	if len(fails) != 0 || pt != "Olá %s, §apegue isto§r\ntchau" {
		t.Fatalf("%q %v", pt, fails)
	}
	// ordem diferente é aceita
	if _, fails, _ := Check(todo, p, "⟦2⟧Pegue isto⟦3⟧⟦4⟧tchau, ⟦1⟧", Options{}); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
	cases := map[string]string{
		"marca perdida":      "Olá, ⟦2⟧pegue isto⟦3⟧⟦4⟧tchau",
		"marca repetida":     "Olá ⟦1⟧ ⟦1⟧, ⟦2⟧pegue⟦3⟧⟦4⟧tchau",
		"marca desconhecida": "Olá ⟦1⟧, ⟦2⟧pegue⟦3⟧⟦4⟧tchau ⟦9⟧",
		"texto vazio":        "   ",
		"explicação":         "Aqui está a tradução: Olá ⟦1⟧, ⟦2⟧pegue⟦3⟧⟦4⟧tchau",
		// aspas em volta de tudo: o repair() tira, ver TestStripAddedQuotes
		"tamanho":   "Olá ⟦1⟧, ⟦2⟧" + strings.Repeat("blá ", 100) + "⟦3⟧⟦4⟧tchau",
		"caractere": "Olá ⟦1⟧, ⟦2⟧pegue\x01⟦3⟧⟦4⟧tchau",
	}
	for name, out := range cases {
		if _, fails, _ := Check(todo, p, out, Options{}); len(fails) == 0 {
			t.Errorf("%s: deveria reprovar", name)
		}
	}
	// o motor trocou o %s por texto escrito à mão: as marcas do original somem
	// e a conferência independente pega mesmo que o mapa do protect esteja vazio
	empty := prot("Hello ⟦1⟧", [2]string{"⟦1⟧", "%s"})
	todo2 := schema.TodoRec{En: "Hello %s %d"}
	if _, fails, _ := Check(todo2, empty, "Olá ⟦1⟧", Options{}); len(fails) == 0 || !strings.Contains(strings.Join(fails, " "), "%d") {
		t.Fatalf("protect esqueceu o %%d; validate deveria notar: %v", fails)
	}
}

func TestWarnings(t *testing.T) {
	g := glossary.New([]glossary.Term{{En: "Iron", Pt: "Ferro"}})
	// igual ao inglês: um aviso só (o do glossário seria o mesmo problema dito duas vezes)
	_, fails, w := Check(schema.TodoRec{En: "Iron sword", Reason: "missing"}, prot("Iron sword"), "Iron sword", Options{Gloss: g})
	if len(fails) != 0 || len(w) != 1 || w[0] != "igual ao inglês" {
		t.Fatalf("%v %v", fails, w)
	}
	// traduzido, mas sem o termo do glossário: o aviso do glossário continua
	_, fails, w = Check(schema.TodoRec{En: "Iron sword", Reason: "missing"}, prot("Iron sword"), "Espada metálica", Options{Gloss: g})
	if len(fails) != 0 || len(w) != 1 || !strings.Contains(w[0], "glossário") {
		t.Fatalf("%v %v", fails, w)
	}
	// texto de poucas letras: sem aviso de glossário
	g2 := glossary.New([]glossary.Term{{En: "Ore", Pt: "Minério"}})
	_, _, w = Check(schema.TodoRec{En: "Ore", Reason: "missing"}, prot("Ore"), "Xyz", Options{Gloss: g2})
	if len(w) != 0 {
		t.Fatalf("%v", w)
	}
	// igual ao inglês é normal quando o diff já tinha marcado como idêntico
	_, _, w = Check(schema.TodoRec{En: "Waystone", Reason: "identical"}, prot("Waystone"), "Waystone", Options{})
	if len(w) != 0 {
		t.Fatalf("%v", w)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	tw, _ := schema.Create(filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	pw, _ := schema.Create(filepath.Join(dir, "protected.jsonl"), schema.VProtected)
	for _, r := range []struct{ id, en, masked string }{{"a", "Hello", "Hello"}, {"b", "Press %s", "Press ⟦1⟧"}, {"c", "Missing", "Missing"}} {
		tw.Write(schema.TodoRec{ID: r.id, Mod: "m", NS: "m", Key: r.id, En: r.en, Reason: "missing"})
		p := schema.ProtectedRec{ID: r.id, Masked: r.masked, Style: "brackets"}
		if r.id == "b" {
			p.Tokens = []schema.Token{{T: "⟦1⟧", Raw: "%s"}}
		}
		pw.Write(p)
	}
	tw.Close()
	pw.Close()
	a, _ := schema.OpenAppend(filepath.Join(dir, "translated.jsonl"), schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "a", Engine: "e1", Out: "Olá"})
	a.Write(schema.TranslatedRec{ID: "b", Engine: "e1", Out: "Pressione"}) // perdeu a marca
	a.Close()
	c, err := Run(dir, Options{})
	if err != nil || c.OK != 1 || c.Failed != 1 || c.Pending != 1 {
		t.Fatalf("%v %+v", err, c)
	}
	ok, _ := schema.ReadAll[schema.OKRec](filepath.Join(dir, "ok.jsonl"), schema.VOK)
	fl, _ := schema.ReadAll[schema.FailedRec](filepath.Join(dir, "failed.jsonl"), schema.VFailed)
	if len(ok) != 1 || ok[0].Pt != "Olá" || len(fl) != 1 || fl[0].ID != "b" {
		t.Fatalf("%+v %+v", ok, fl)
	}
	rep, _ := os.ReadFile(filepath.Join(dir, "report.md"))
	for _, want := range []string{"Aprovados: 1", "Reprovados: 1", "Ainda sem tradução: 1", "marca perdida", "| e1 | 1 | 1 |"} {
		if !strings.Contains(string(rep), want) {
			t.Errorf("relatório sem %q:\n%s", want, rep)
		}
	}
}

func TestCheckFTBQMarks(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "&aFirst quest {image:a:b.png}", Reason: "missing", Kind: "ftbq"}
	p := prot("⟦1⟧First quest ⟦2⟧", [2]string{"⟦1⟧", "&a"}, [2]string{"⟦2⟧", "{image:a:b.png}"})
	if pt, fails, _ := Check(todo, p, "⟦1⟧Primeira missão ⟦2⟧", Options{}); len(fails) != 0 || pt != "&aPrimeira missão {image:a:b.png}" {
		t.Fatalf("%q %v", pt, fails)
	}
	// o motor escreveu a cor à mão e esqueceu a imagem: a conferência independente pega
	empty := prot("x")
	if _, fails, _ := Check(todo, empty, "&aPrimeira missão", Options{}); len(fails) == 0 {
		t.Fatal("devia reprovar: falta {image:...}")
	}
	// fora do FTB, & não é marca
	todo.Kind = ""
	if _, fails, _ := Check(todo, empty, "Primeira missão &b {image:a:b.png}", Options{}); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
}

func TestSignatureFTBQHexColor(t *testing.T) {
	a := signature("&#FF8800Gold {@pagebreak}", "ftbqi")
	b := signature("Ouro", "ftbqi")
	if len(diffSig(a, b)) < 2 {
		t.Fatalf("tradução sem a cor e sem o pagebreak deveria falhar: %v", diffSig(a, b))
	}
	c := signature("&#FF8800Ouro {@pagebreak}", "ftbqi")
	if d := diffSig(a, c); len(d) != 0 {
		t.Fatalf("mesmas marcas deveriam passar: %v", d)
	}
}

func TestRepairBracketsAndNewline(t *testing.T) {
	p := schema.ProtectedRec{ID: "x", Masked: "⟦1⟧ hidden", Style: "brackets", Tokens: []schema.Token{{T: "⟦1⟧", Raw: "%d"}}}
	todo := schema.TodoRec{ID: "x", En: "%d hidden"}
	pt, fails, _ := Check(todo, p, "[1] oculto\n", Options{})
	if len(fails) != 0 || pt != "%d oculto" {
		t.Fatalf("pt=%q fails=%v", pt, fails)
	}
}

func TestStripAddedQuotes(t *testing.T) {
	p := schema.ProtectedRec{ID: "x", Masked: "Hello there", Style: "brackets"}
	pt, fails, _ := Check(schema.TodoRec{ID: "x", En: "Hello there"}, p, "\"Olá, tudo bem\"", Options{})
	if len(fails) != 0 || pt != "Olá, tudo bem" {
		t.Fatalf("pt=%q fails=%v", pt, fails)
	}
	// o original já vem entre aspas simples: as aspas da tradução são do texto
	p2 := schema.ProtectedRec{ID: "y", Masked: "'You Won't Get Anything Done Hoeing Like That'", Style: "brackets"}
	pt, fails, _ = Check(schema.TodoRec{ID: "y", En: "'You Won't Get Anything Done Hoeing Like That'"}, p2, "\"Você não vai conseguir fazer nada trabalhando assim\"", Options{})
	if len(fails) != 0 {
		t.Fatalf("pt=%q fails=%v", pt, fails)
	}
}

func TestCorAbertaOuFechadaComoNoOriginal(t *testing.T) {
	// original: &6Fertility&r is ... (cor fecha no meio, termina fechada)
	todo := schema.TodoRec{ID: "x", En: "&6Fertility&r is high", Kind: schema.KindFTBQ}
	p := prot("⟦1⟧Fertility⟦2⟧ is high", [2]string{"⟦1⟧", "&6"}, [2]string{"⟦2⟧", "&r"})
	if _, fails, _ := Check(todo, p, "⟦1⟧Fertilidade⟦2⟧ é alta", Options{}); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
	// o modelo trocou a ordem: o reset veio antes da cor e a cor cobre o resto
	_, fails, _ := Check(todo, p, "⟦2⟧Fertilidade⟦1⟧ é alta", Options{})
	if len(fails) == 0 {
		t.Fatal("cor aberta no fim devia reprovar")
	}
	// original que termina aberto: tradução que termina aberta é aceita
	todo2 := schema.TodoRec{ID: "x", En: "&cWarning", Kind: schema.KindFTBQ}
	p2 := prot("⟦1⟧Warning", [2]string{"⟦1⟧", "&c"})
	if _, fails, _ := Check(todo2, p2, "⟦1⟧Aviso", Options{}); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
	// § em arquivo de idioma comum
	todo3 := schema.TodoRec{ID: "x", En: "§aOK§r now"}
	p3 := prot("⟦1⟧OK⟦2⟧ now", [2]string{"⟦1⟧", "§a"}, [2]string{"⟦2⟧", "§r"})
	if _, fails, _ := Check(todo3, p3, "⟦2⟧Agora⟦1⟧ ok", Options{}); len(fails) == 0 {
		t.Fatal("reset antes da cor e cor aberta no fim devia reprovar")
	}
	// & em arquivo de idioma comum não é código de cor
	todo4 := schema.TodoRec{ID: "x", En: "Fire &r Ice"}
	if _, fails, _ := Check(todo4, prot("Fire &r Ice"), "Fogo &r Gelo", Options{}); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
}

func TestColorStateHexEReset(t *testing.T) {
	if eo, cf := colorState("&#ff8800Hot&r cold", schema.KindFTBQ); eo || cf {
		t.Fatal(eo, cf)
	}
	if eo, cf := colorState("&rplain &6gold", schema.KindFTBQ); !eo || !cf {
		t.Fatal(eo, cf)
	}
	if eo, _ := colorState("no codes", ""); eo {
		t.Fatal("sem códigos não está aberto")
	}
}

func TestEspacoDuploSoQuandoOOriginalNaoTem(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "Fertility is high"}
	pt, fails, _ := Check(todo, prot("Fertility is high"), "Fertilidade  é   alta", Options{})
	if len(fails) != 0 || pt != "Fertilidade é alta" {
		t.Fatalf("%q %v", pt, fails)
	}
	todo = schema.TodoRec{ID: "x", En: "A  B"}
	pt, _, _ = Check(todo, prot("A  B"), "A  B", Options{})
	if pt != "A  B" {
		t.Fatalf("o original tem espaço duplo: %q", pt)
	}
}

func writeTodo(t *testing.T, dir string, items map[string]string) {
	t.Helper()
	tw, _ := schema.Create(filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	pw, _ := schema.Create(filepath.Join(dir, "protected.jsonl"), schema.VProtected)
	trw, _ := schema.OpenAppend(filepath.Join(dir, "translated.jsonl"), schema.VTranslated)
	for _, id := range []string{"a", "b", "c"} {
		en, ok := items[id]
		if !ok {
			continue
		}
		tw.Write(schema.TodoRec{ID: id, Mod: "m", NS: "m", Key: "k." + id, En: en, EnSha: schema.Sha(en), Reason: "missing"})
		p := schema.ProtectedRec{ID: id, Masked: en, Style: "brackets"}
		if strings.Contains(en, "%s") {
			p.Masked = strings.Replace(en, "%s", "⟦1⟧", 1)
			p.Tokens = []schema.Token{{T: "⟦1⟧", Raw: "%s"}}
		}
		pw.Write(p)
	}
	tw.Close()
	pw.Close()
	trw.Write(schema.TranslatedRec{ID: "a", Out: "Olá"})
	trw.Write(schema.TranslatedRec{ID: "b", Out: "Pressione"}) // perdeu o %s: reprova
	trw.Close()
}

func TestRevisaoManualEntraComoOK(t *testing.T) {
	dir := t.TempDir()
	writeTodo(t, dir, map[string]string{"a": "Hello", "b": "Press %s", "c": "Pending"})
	c, err := Run(dir, Options{})
	if err != nil || c.Failed != 1 || c.Pending != 1 || c.OK != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	items, err := ReviewItems(dir, Options{}, "failed")
	if err != nil || len(items) != 1 || items[0].ID != "b" || items[0].Status != "reprovado" || items[0].Current != "Pressione" {
		t.Fatalf("%+v %v", items, err)
	}
	if all, _ := ReviewItems(dir, Options{}, "all"); len(all) != 2 {
		t.Fatalf("scope all devia incluir o pendente: %+v", all)
	}
	// corrige o reprovado à mão e traduz o pendente à mão
	if err := review.Append(dir,
		review.Entry{ID: "b", Pt: "Pressione %s", EnSha: schema.Sha("Press %s")},
		review.Entry{ID: "c", Pt: "Pendente", EnSha: schema.Sha("Pending")}); err != nil {
		t.Fatal(err)
	}
	c, err = Run(dir, Options{})
	if err != nil || c.Failed != 0 || c.Pending != 0 || c.OK != 3 || c.Reviewed != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	oks, _ := schema.ReadAll[schema.OKRec](filepath.Join(dir, "ok.jsonl"), schema.VOK)
	got := map[string]string{}
	for _, o := range oks {
		got[o.ID] = o.Pt + "|" + o.Engine
	}
	if got["b"] != "Pressione %s|manual" || got["a"] != "Olá|echo" && got["a"] != "Olá|" {
		t.Fatalf("%v", got)
	}
	if items, _ := ReviewItems(dir, Options{}, "all"); len(items) != 0 {
		t.Fatalf("nada mais para revisar: %+v", items)
	}
}

func TestRevisaoManualDesatualizadaEhIgnorada(t *testing.T) {
	dir := t.TempDir()
	writeTodo(t, dir, map[string]string{"a": "Hello", "b": "Press %s"})
	review.Append(dir, review.Entry{ID: "b", Pt: "Pressione %s", EnSha: schema.Sha("Press the %s")}) // original era outro
	c, err := Run(dir, Options{})
	if err != nil || c.Failed != 1 || c.ReviewStale != 1 || c.Reviewed != 0 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRevisaoManualComMarcasDiferentesSoAvisa(t *testing.T) {
	dir := t.TempDir()
	writeTodo(t, dir, map[string]string{"b": "Press %s"})
	review.Append(dir, review.Entry{ID: "b", Pt: "Pressione", EnSha: schema.Sha("Press %s")})
	c, err := Run(dir, Options{})
	if err != nil || c.OK != 1 || c.Failed != 0 || c.Warnings != 1 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRevisaoManualDropDesfaz(t *testing.T) {
	dir := t.TempDir()
	writeTodo(t, dir, map[string]string{"b": "Press %s"})
	review.Append(dir, review.Entry{ID: "b", Pt: "Pressione %s", EnSha: schema.Sha("Press %s")})
	review.Append(dir, review.Entry{ID: "b", Pt: ""})
	if c, _ := Run(dir, Options{}); c.Failed != 1 || c.Reviewed != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestNotaDoModeloReprova(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "Right click to open"}
	p := prot("Right click to open")
	for _, out := range []string{"Clique direito para abrir (Nota: 'open' pode ser 'abrir')", "Clique direito para abrir\n(Note: literal)", "Clique direito. Nota do tradutor: ok"} {
		if _, fails, _ := Check(todo, p, out, Options{}); len(fails) == 0 {
			t.Errorf("devia reprovar: %q", out)
		}
	}
	// o original que já tem "(Note:" não reprova
	todo2 := schema.TodoRec{ID: "x", En: "Hot (Note: very hot)"}
	if _, fails, _ := Check(todo2, prot("Hot (Note: very hot)"), "Quente (Note: muito quente)", Options{}); len(fails) != 0 {
		t.Errorf("%v", fails)
	}
}

// Depois de atualizar o mod, a tradução guardada é de outro texto em inglês: o
// validate não pode aprová-la (o pacote levaria "Shiny Gem" traduzido para um
// item que agora se chama "Steel Pickaxe").
func TestStaleTranslationIsPendingNotOK(t *testing.T) {
	dir := t.TempDir()
	tw, _ := schema.Create(filepath.Join(dir, "todo.jsonl"), schema.VTodo)
	tw.Write(schema.TodoRec{ID: "g", Mod: "m", NS: "m", Key: "item.gem", En: "Steel Pickaxe", Reason: "missing"})
	tw.Write(schema.TodoRec{ID: "o", Mod: "m", NS: "m", Key: "item.old", En: "Old record", Reason: "missing"})
	tw.Write(schema.TodoRec{ID: "f", Mod: "m", NS: "m", Key: "item.fresh", En: "Fresh text", Reason: "missing"})
	tw.Close()
	pw, _ := schema.Create(filepath.Join(dir, "protected.jsonl"), schema.VProtected)
	for _, r := range [][2]string{{"g", "Steel Pickaxe"}, {"o", "Old record"}, {"f", "Fresh text"}} {
		pw.Write(schema.ProtectedRec{ID: r[0], Masked: r[1], Style: "brackets"})
	}
	pw.Close()
	a, _ := schema.OpenAppend(filepath.Join(dir, "translated.jsonl"), schema.VTranslated)
	a.Write(schema.TranslatedRec{ID: "g", Engine: "e", Out: "Gema Brilhante", MSha: schema.Sha("Shiny Gem")}) // feita para o texto antigo
	a.Write(schema.TranslatedRec{ID: "o", Engine: "e", Out: "Registro antigo"})                               // sem hash (arquivo de versão antiga): vale
	a.Write(schema.TranslatedRec{ID: "f", Engine: "e", Out: "Texto novo", MSha: schema.Sha("Fresh text")})    // hash bate
	a.Close()
	c, err := Run(dir, Options{})
	if err != nil || c.OK != 2 || c.Pending != 1 || c.Stale != 1 || c.Failed != 0 {
		t.Fatalf("%v %+v", err, c)
	}
	ok, _ := schema.ReadAll[schema.OKRec](filepath.Join(dir, "ok.jsonl"), schema.VOK)
	for _, r := range ok {
		if r.ID == "g" {
			t.Fatalf("a tradução velha entrou no pacote: %+v", r)
		}
	}
	items, err := ReviewItems(dir, Options{}, "all")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.ID == "g" {
			found = it.Status == "pendente"
		}
	}
	if !found {
		t.Fatalf("a revisão devia mostrar o texto como pendente: %+v", items)
	}
}

func fmtProt(masked string, raws ...string) schema.ProtectedRec {
	p := schema.ProtectedRec{ID: "x", Masked: masked, Style: "brackets"}
	for i, r := range raws {
		p.Tokens = append(p.Tokens, schema.Token{T: fmt.Sprintf("⟦%d⟧", i+1), Raw: r})
	}
	return p
}

func TestReorderedFormatSpecsBecomePositional(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "Deals %s damage to %d entities", Reason: "missing"}
	p := fmtProt("Deals ⟦1⟧ damage to ⟦2⟧ entities", "%s", "%d")
	// ordem trocada: o jogo entregaria o nome onde o texto espera o número
	pt, fails, warns := Check(todo, p, "Causa ⟦2⟧ de dano a ⟦1⟧ entidades", Options{})
	if len(fails) != 0 || pt != "Causa %2$d de dano a %1$s entidades" {
		t.Fatalf("%q %v", pt, fails)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "outra ordem") {
		t.Fatalf("devia avisar da troca: %v", warns)
	}
	// ordem igual: nada muda
	pt, fails, warns = Check(todo, p, "Causa ⟦1⟧ de dano a ⟦2⟧ entidades", Options{})
	if len(fails) != 0 || len(warns) != 0 || pt != "Causa %s de dano a %d entidades" {
		t.Fatalf("%q %v %v", pt, fails, warns)
	}
	// com flags e três especificadores: cada um leva a posição que tinha no original
	todo3 := schema.TodoRec{ID: "x", En: "%s has %.1f of %d", Reason: "missing"}
	p3 := fmtProt("⟦1⟧ has ⟦2⟧ of ⟦3⟧", "%s", "%.1f", "%d")
	pt, fails, _ = Check(todo3, p3, "⟦3⟧ de ⟦2⟧ para ⟦1⟧", Options{})
	if len(fails) != 0 || pt != "%3$d de %2$.1f para %1$s" {
		t.Fatalf("%q %v", pt, fails)
	}
	// especificadores que já têm posição podem ser trocados à vontade: nada a fazer
	todoP := schema.TodoRec{ID: "x", En: "%1$s hit %2$s", Reason: "missing"}
	pP := fmtProt("⟦1⟧ hit ⟦2⟧", "%1$s", "%2$s")
	pt, fails, warns = Check(todoP, pP, "⟦2⟧ foi atingido por ⟦1⟧", Options{})
	if len(fails) != 0 || len(warns) != 0 || pt != "%2$s foi atingido por %1$s" {
		t.Fatalf("%q %v %v", pt, fails, warns)
	}
	// marca perdida continua reprovando, sem mexer no texto
	_, fails, _ = Check(todo, p, "Causa ⟦2⟧ de dano", Options{})
	if len(fails) == 0 {
		t.Fatal("marca perdida devia reprovar")
	}
	// %n e %% não contam como argumentos
	todoN := schema.TodoRec{ID: "x", En: "%s%n%d%%", Reason: "missing"}
	pN := fmtProt("⟦1⟧⟦2⟧⟦3⟧⟦4⟧", "%s", "%n", "%d", "%%")
	pt, fails, _ = Check(todoN, pN, "⟦3⟧⟦2⟧⟦1⟧⟦4⟧", Options{})
	if len(fails) != 0 || pt != "%2$d%n%1$s%%" {
		t.Fatalf("%q %v", pt, fails)
	}
}

func TestCheckWarnsOnLostAmpCodeInLang(t *testing.T) {
	todo := schema.TodoRec{ID: "x", En: "&6Gold &rText", Reason: "missing"}
	p := schema.ProtectedRec{ID: "x", Masked: "&6Gold &rText", Style: "brackets"}
	_, fails, warns := Check(todo, p, "Ouro Texto", Options{})
	if len(fails) != 0 || len(warns) != 2 || !strings.Contains(warns[0], "&6: original 1, tradução 0") {
		t.Fatalf("fails=%v warns=%v", fails, warns)
	}
	// códigos mantidos (mesmo em outra posição): sem aviso
	_, fails, warns = Check(todo, p, "&6Ouro &rTexto", Options{})
	if len(fails) != 0 || len(warns) != 0 {
		t.Fatalf("fails=%v warns=%v", fails, warns)
	}
	// um "&" comum não vira aviso
	todo2 := schema.TodoRec{ID: "y", En: "Fire & Ice", Reason: "missing"}
	p2 := schema.ProtectedRec{ID: "y", Masked: "Fire & Ice", Style: "brackets"}
	_, _, warns = Check(todo2, p2, "Fogo & Gelo", Options{})
	if len(warns) != 0 {
		t.Fatalf("%v", warns)
	}
}
