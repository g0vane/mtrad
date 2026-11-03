package ui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func runDemo(t *testing.T, d *Demo, sc *Scripted, rows, cols int) string {
	t.Helper()
	var out bytes.Buffer
	u := New(sc, &out, d, "teste", false, false, func() (int, int) { return rows, cols })
	done := make(chan struct{})
	go func() { u.Run(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("a interface travou; saída até aqui:\n%s", out.String())
	}
	return out.String()
}

func keys(spec string) *Scripted { return &Scripted{Keys: Script(spec)} }

func want(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("faltou %q na saída:\n%s", s, out)
		}
	}
}

func TestWelcomeHidesInstanceUntilSetup(t *testing.T) {
	out := runDemo(t, &Demo{}, keys("esc q"), 30, 100) // Esc no menu inicial não faz nada
	want(t, out, "m t r a d", "Bem-vindo!", "Configurar (setup)", "Primeira vez")
	if strings.Contains(out, "Selecionar sua instância") {
		t.Fatal("a opção de instância não deveria aparecer antes da configuração")
	}
}

func TestSetupThenInstanceAppears(t *testing.T) {
	out := runDemo(t, &Demo{}, keys("enter enter enter enter down down down enter enter enter enter q"), 30, 100)
	want(t, out, "Passo 1 de 6", "Passo 6 de 6", "configuração gravada", "Selecionar sua instância")
}

func TestSetupBackAndEscLeaves(t *testing.T) {
	out := runDemo(t, &Demo{}, keys("enter enter enter esc esc esc q"), 30, 100)
	want(t, out, "Passo 3 de 6", "Passo 2 de 6", "Passo 1 de 6")
	if strings.Contains(out, "configuração gravada") || strings.Contains(out, "Selecionar sua instância") {
		t.Fatal("nada deveria ter sido gravado")
	}
}

func TestSetupChangeEngineResetsModel(t *testing.T) {
	// passo 2: gemini (duas setas); passo 3: Enter; passo 4: Continuar; passo 5; passo 6: Cancelar
	out := runDemo(t, &Demo{}, keys("enter enter down down enter enter down down down enter enter down down enter q"), 30, 100)
	want(t, out, "GEMINI_API_KEY", "Motor      gemini · gemini-2.0-flash")
}

func TestSetupWithoutOllamaOffersInstall(t *testing.T) {
	// Instalar agora > Sim > aviso; depois Continuar assim mesmo; Ctrl+C sai
	out := runDemo(t, &Demo{Broken: true}, keys("enter enter up enter enter down down enter ctrlc"), 30, 100)
	want(t, out, "✗", "sudo pacman -S ollama ollama-vulkan", "Instalar o Ollama e o modelo?", "simulado", "Passo 2 de 6")
}

func TestInstanceFlowModsAndTranslate(t *testing.T) {
	// Selecionar > 1ª instância > Escolher mods > r > Esc > Traduzir > Teste > Começar > fim > Esc > q
	out := runDemo(t, &Demo{Done: true}, keys("enter enter down enter r esc up enter enter enter enter esc q"), 30, 140)
	want(t, out,
		`"skip_mods": ["chipped", "rechiseled", "fancymenu", "mcwfurnitures", "mcwroofs"]`,
		"Mods em inglês: 5", "etapa concluída", "Reclamation - Reclaim the World-")
}

func TestTranslatePause(t *testing.T) {
	// 5 Enters chegam a "Começar agora"; o Esc do Poll pausa; Ctrl+C sai do menu de pausa
	sc := keys("enter enter enter enter enter ctrlc")
	sc.PollKeys = Script("esc")
	out := runDemo(t, &Demo{Done: true, Delay: 20 * time.Millisecond}, sc, 30, 100)
	want(t, out, "Traduzindo…", "pausado", "Continuar de onde parou")
}

// Depois de pausar, "Continuar de onde parou" volta a traduzir na hora, sem
// passar de novo pelos menus de Traduzir.
func TestTranslatePauseThenResume(t *testing.T) {
	// 5 Enters começam; o 6º é "Continuar de onde parou"; o 7º fecha a tela de fim
	sc := keys("enter enter enter enter enter enter enter ctrlc")
	sc.PollKeys = Script("esc")
	out := runDemo(t, &Demo{Done: true, Delay: 20 * time.Millisecond}, sc, 30, 100)
	want(t, out, "pausado", "Continuar de onde parou", "etapa concluída")
}

// "Voltar ao menu" depois de pausar não recomeça a tradução.
func TestTranslatePauseThenBack(t *testing.T) {
	sc := keys("enter enter enter enter enter down enter ctrlc")
	sc.PollKeys = Script("esc")
	out := runDemo(t, &Demo{Done: true, Delay: 20 * time.Millisecond}, sc, 30, 100)
	want(t, out, "pausado", "Voltar ao menu")
	if strings.Contains(out, "etapa concluída") {
		t.Fatal("não devia ter recomeçado a tradução")
	}
}

func TestFailedEditWarnsAboutMissingMarks(t *testing.T) {
	// Reprovados > 1º > Editar > Enter (avisa) > Enter (aceita assim mesmo)
	out := runDemo(t, &Demo{Done: true}, keys("enter enter down down enter enter enter enter enter esc ctrlc"), 30, 100)
	want(t, out, "faltam as marcas do original: &6 &r", "Enter de novo para salvar", "[corrigido]")
}

func TestFailedLeaveInEnglish(t *testing.T) {
	out := runDemo(t, &Demo{Done: true}, keys("enter enter down down enter enter down enter esc ctrlc"), 30, 100)
	want(t, out, "[fica em inglês]")
}

func TestStyleSwitch(t *testing.T) {
	out := runDemo(t, &Demo{Done: true}, keys("enter enter i i esc q"), 30, 100)
	want(t, out, "i estilo: Painéis", "Sobre o item", "┌ Instância", "┌ Última execução")
}

func TestInputEditing(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("bs t:ab left left t:Z home del t:ção enter"), &out, &Demo{}, "t", false, false, nil)
	got, ok := u.input(nil, "xyz", nil)
	// xyz → xy → xyab → cursor em 2 → xyZab → home → del tira o x → yZab → ção na frente
	if !ok || got != "çãoyZab" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	u = New(keys("t:abc esc"), &out, &Demo{}, "t", false, false, nil)
	if _, ok := u.input(nil, "", nil); ok {
		t.Fatal("Esc deveria cancelar")
	}
}

func TestLongListScrolls(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("pgdn down down enter"), &out, &Demo{}, "t", false, false, func() (int, int) { return 12, 80 })
	i, ok := u.pick(Pick{Build: func() Layout {
		var its []Item
		for n := 0; n < 100; n++ {
			its = append(its, Item{Label: "item" + strings.Repeat("x", n%3)})
		}
		return Layout{Head: []string{"cabeça"}, Items: its}
	}})
	if !ok || i <= 5 || i >= 100 {
		t.Fatalf("i=%d ok=%v", i, ok)
	}
	want(t, out.String(), "↓ mais", "↑ mais")
}

func TestMissingTokens(t *testing.T) {
	if m := missingTokens("Hold [Shift] to show %s", "Segure [Shift]"); len(m) != 1 || m[0] != "%s" {
		t.Fatalf("%v", m)
	}
	if m := missingTokens("&6A&r e &6B&r", "&6A&r e B"); len(m) != 2 {
		t.Fatalf("%v", m)
	}
	if m := missingTokens("x ⟦1⟧", "y ⟦1⟧"); len(m) != 0 {
		t.Fatalf("%v", m)
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("abcdefghij", 5); vis(got) != 5 {
		t.Fatalf("%q", got)
	}
	if got := trunc("\x1b[1mabcdefghij\x1b[0m", 5); vis(got) != 5 {
		t.Fatalf("%q", got)
	}
	if got := trunc("abc", 5); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func TestTerminalDecodesKeys(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	w.Write([]byte("\x1b[A\x1b[B\x1b[C\x1b[D\x1b[5~\x1b[6~\x1b[3~\x1bOH\xc3\xa7a\r\x7f\x03"))
	w.Close()
	term := &Terminal{in: r}
	want := []KeyEvent{{K: KUp}, {K: KDown}, {K: KRight}, {K: KLeft}, {K: KPgUp}, {K: KPgDn}, {K: KDelete}, {K: KHome},
		{K: KRune, R: 'ç'}, {K: KRune, R: 'a'}, {K: KEnter}, {K: KBackspace}, {K: KCtrlC}}
	for i, w := range want {
		got, ok := term.Poll(0)
		if !ok || got != w {
			t.Fatalf("tecla %d: got %+v ok=%v, want %+v", i, got, ok, w)
		}
	}
}

// ---------- filtro, janela pequena, Esc + tecla, ligação ao backend ----------

func filterItems() Pick {
	return Pick{Filterable: true, Tail: 1, Build: func() Layout {
		var its []Item
		for n := 0; n < 100; n++ {
			its = append(its, Item{Label: "item" + strings.Repeat("x", n%3), Hint: "dica"})
		}
		its = append(its, Item{Label: "Voltar"})
		return Layout{Head: []string{"cabeça"}, Items: its}
	}}
}

func TestFilterNarrowsAndKeepsOriginalIndex(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("/ t:xx enter enter"), &out, &Demo{}, "t", false, false, func() (int, int) { return 20, 80 })
	i, ok := u.pick(filterItems())
	if !ok || i != 2 { // o primeiro "itemxx" é o terceiro da lista inteira
		t.Fatalf("i=%d ok=%v", i, ok)
	}
	want(t, out.String(), "/ xx", "(33 de 100)")
}

func TestFilterEscClearsThenLeaves(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("/ t:zzz enter esc up enter"), &out, &Demo{}, "t", false, false, func() (int, int) { return 20, 80 })
	// "zzz" não casa com nada: só o item fixo (Voltar) fica; Esc limpa o filtro, a lista volta inteira
	// com o cursor em "Voltar" (índice 100) e a seta para cima chega ao 99
	i, ok := u.pick(filterItems())
	if !ok || i != 99 {
		t.Fatalf("i=%d ok=%v", i, ok)
	}
	want(t, out.String(), "(0 de 100)")
	u = New(keys("/ t:zzz esc esc"), &out, &Demo{}, "t", false, false, func() (int, int) { return 20, 80 })
	if _, ok := u.pick(filterItems()); ok {
		t.Fatal("o segundo Esc deveria sair da lista")
	}
}

func TestFilterTailAlwaysReachable(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("/ t:zzz enter enter"), &out, &Demo{}, "t", false, false, func() (int, int) { return 20, 80 })
	i, ok := u.pick(filterItems())
	if !ok || i != 100 {
		t.Fatalf("o item fixo devia continuar à mão: i=%d ok=%v", i, ok)
	}
	want(t, out.String(), "(0 de 100)")
}

type frameCheck struct {
	Input
	u    **UI
	rows int
	t    *testing.T
}

func (f *frameCheck) Next() KeyEvent {
	if n := len((*f.u).last); n > f.rows-1 {
		f.t.Errorf("quadro com %d linhas numa janela de %d:\n%s", n, f.rows, strings.Join((*f.u).last, "\n"))
	}
	return f.Input.Next()
}

func TestSmallWindowFramesFit(t *testing.T) {
	for _, sz := range [][2]int{{14, 50}, {10, 44}, {18, 60}} {
		var out bytes.Buffer
		var u *UI
		sc := &frameCheck{Input: keys("enter enter i i i down down enter esc enter enter esc esc q"), u: &u, rows: sz[0], t: t}
		u = New(sc, &out, &Demo{Done: true}, "t", false, false, func() (int, int) { return sz[0], sz[1] })
		u.Run()
		want(t, out.String(), "janela pequena")
	}
}

func TestEscThenKeyIsNotLost(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	w.Write([]byte("\x1bq\x1b\x1b[A"))
	w.Close()
	term := &Terminal{in: r}
	for i, wnt := range []KeyEvent{{K: KEsc}, {K: KRune, R: 'q'}, {K: KEsc}, {K: KUp}} {
		got, ok := term.Poll(0)
		if !ok || got != wnt {
			t.Fatalf("tecla %d: got %+v ok=%v, want %+v", i, got, ok, wnt)
		}
	}
}

func TestSetupSavesThroughBackend(t *testing.T) {
	d := &Demo{}
	runDemo(t, d, keys("enter enter down down enter enter down down down enter enter enter enter q"), 30, 100)
	got := d.Defaults()
	if got.Engine != "gemini" || got.Model != "gemini-2.0-flash" || len(got.Sources) != 3 || !d.SetupDone() {
		t.Fatalf("o setup não chegou ao backend: %+v", got)
	}
}

type failSelect struct{ *Demo }

func (failSelect) SelectInstance(in Instance) (Instance, error) {
	return in, errTest("não achei minecraft/ nem .minecraft/")
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestInstanceErrorShowsReasonAndReturnsToList(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("enter enter enter esc q"), &out, failSelect{&Demo{Done: true}}, "t", false, false, func() (int, int) { return 30, 100 })
	u.Run()
	want(t, out.String(), "não consegui usar esta instância", "não achei minecraft/ nem .minecraft/", "Selecione sua instância")
	if strings.Contains(out.String(), "Traduzir") {
		t.Fatal("com a instância inválida o menu da instância não devia abrir")
	}
}

func TestFailedEditAndLeavePersistAndApply(t *testing.T) {
	d := &Demo{Done: true}
	// editar o 1º (Enter avisa, Enter de novo aceita), deixar o 2º em inglês, aplicar as correções
	out := runDemo(t, d, keys("enter enter down down enter enter enter enter enter down enter down enter down down down down down enter enter esc esc ctrlc"), 30, 100)
	if got := d.Status().Fail; got != 194 {
		t.Fatalf("Status.Fail = %d, esperava 194 depois de 2 resolvidos\n%s", got, out)
	}
	want(t, out, "[corrigido]", "[fica em inglês]", "correções aplicadas")
}

func TestModsSavedOnConclude(t *testing.T) {
	d := &Demo{Done: true}
	runDemo(t, d, keys("enter enter down enter r esc q"), 30, 140)
	if got := strings.Join(d.Skipped(), ","); got != "chipped,fancymenu,mcwfurnitures,mcwroofs,rechiseled" {
		t.Fatalf("skip_mods não foi gravado: %q", got)
	}
}

// partialBackend termina a tradução com falhas do motor e sem nada reprovado.
type partialBackend struct{ *Demo }

func (partialBackend) Translate(ctx context.Context, limit int, install bool, progress func(done, total int)) (Outcome, error) {
	progress(10, 10)
	return Outcome{Lines: []string{"translate: 0 traduzidos, 0 do cache, 10 falharam"}, Partial: true,
		Problems: []string{`10 × translate: Post "http://127.0.0.1:8765/v1/chat/completions": connection refused`}}, nil
}

func (partialBackend) Failed() []Failed { return nil }

func TestTranslateEngineFailureShowsWhy(t *testing.T) {
	var out bytes.Buffer
	u := New(keys("enter enter enter enter enter enter ctrlc"), &out, partialBackend{&Demo{Done: true}}, "t", false, false, func() (int, int) { return 30, 120 })
	u.Run()
	want(t, out.String(), "concluída com avisos (nada para revisar em Reprovados)", "10 × translate:", "connection refused")
	if strings.Contains(out.String(), "veja Reprovados") {
		t.Fatal("sem reprovados não pode mandar olhar a lista de Reprovados")
	}
}

func TestFailedTagStaysVisibleInNarrowWindow(t *testing.T) {
	d := &Demo{Done: true}
	out := runDemo(t, d, keys("enter enter down down enter enter enter enter enter esc ctrlc"), 30, 60)
	want(t, out, "[corrigido]")
}

func TestModsShowTimeCostPerMod(t *testing.T) {
	// o Demo mede 100 textos por minuto: o chipped (7258 textos) leva ~73 min
	out := runDemo(t, &Demo{Done: true}, keys("enter enter down enter esc q"), 30, 140)
	want(t, out, "~73 min", "repetitivo")
}

func TestMinutesText(t *testing.T) {
	for _, c := range []struct {
		n, per int
		want   string
	}{{50, 100, "~1 min"}, {250, 100, "~3 min"}, {8900, 100, "~89 min"}, {9000, 100, "~1 h 30 min"}, {10, 0, ""}} {
		if got := minutesText(c.n, c.per); got != c.want {
			t.Errorf("minutesText(%d,%d) = %q, queria %q", c.n, c.per, got, c.want)
		}
	}
}

func TestMaintenanceVerifyPack(t *testing.T) {
	// Manutenção (7º item) > Conferir o pacote montado (3º) > Enter volta
	out := runDemo(t, &Demo{Done: true}, keys("enter enter down down down down down down enter down down enter enter esc esc ctrlc"), 30, 140)
	want(t, out, "Conferir o pacote montado", "nenhum problema", "Erro de carregamento", "mtrad verify")
}
