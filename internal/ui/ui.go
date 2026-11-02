// Package ui é o menu interativo do mtrad no terminal, controlado por setas.
// Só usa a biblioteca padrão. Não desenha cor de fundo: o terminal usa o
// próprio fundo (e a transparência que o usuário configurou).
package ui

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// State é o que o usuário escolheu até agora.
type State struct {
	SetupDone bool
	Inst      *Instance
	Engine    string
	Model     string
	Work      string
	Config    string
	Sources   map[string]bool // lang, patchouli, ftbq
	Skip      map[string]bool // mods que ficam em inglês
}

// Estilos do menu da instância (tecla i alterna).
var styleNames = []string{"Clássica", "Painéis", "Dashboard"}

// UI é uma sessão do menu.
type UI struct {
	in    Input
	out   io.Writer
	live  bool // terminal de verdade: limpa a tela a cada quadro
	size  func() (rows, cols int)
	color bool
	be    Backend
	st    State
	ver   string
	style int
	quit  bool
	last  []string // último quadro desenhado (para os testes)
}

// New cria a interface. live liga o desenho em tela cheia (limpar e redesenhar);
// size devolve o tamanho do terminal (nil = 24 x 80); color liga as cores ANSI.
func New(in Input, out io.Writer, be Backend, version string, live, color bool, size func() (int, int)) *UI {
	if size == nil {
		size = func() (int, int) { return 24, 80 }
	}
	u := &UI{in: in, out: out, be: be, ver: version, live: live, color: color, size: size}
	d := be.Defaults()
	u.st = State{Engine: d.Engine, Model: d.Model, Work: d.Work, Config: d.Config, Sources: map[string]bool{}, Skip: map[string]bool{}}
	for _, s := range d.Sources {
		u.st.Sources[s] = true
	}
	if len(u.st.Sources) == 0 {
		u.st.Sources = map[string]bool{"lang": true, "patchouli": true, "ftbq": true}
	}
	u.st.SetupDone = be.SetupDone()
	return u
}

// ---------- estilo do texto (só cor de letra, nunca de fundo) ----------

func (u *UI) c(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (u *UI) bold(s string) string { return u.c("1", s) }
func (u *UI) dim(s string) string  { return u.c("2", s) }
func (u *UI) ok(s string) string   { return u.c("32", s) }
func (u *UI) bad(s string) string  { return u.c("31", s) }
func (u *UI) warn(s string) string { return u.c("33", s) }
func (u *UI) acc(s string) string  { return u.c("36", s) }

// ---------- medidas ----------

// vis conta as colunas de s, sem as sequências de cor.
func vis(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc:
			if r == 'm' {
				esc = false
			}
		case r == 0x1b:
			esc = true
		default:
			n++
		}
	}
	return n
}

// trunc corta s em w colunas visíveis (mantendo as cores) e põe … se cortou.
func trunc(s string, w int) string {
	if vis(s) <= w {
		return s
	}
	var b strings.Builder
	n, esc := 0, false
	for _, r := range s {
		if esc {
			b.WriteRune(r)
			if r == 'm' {
				esc = false
			}
			continue
		}
		if r == 0x1b {
			esc = true
			b.WriteRune(r)
			continue
		}
		if n >= w-1 {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString("…")
	if strings.Contains(s, "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func pad(s string, w int) string {
	if n := vis(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func (u *UI) width() int {
	_, c := u.size()
	if c > 100 {
		c = 100
	}
	if c < 40 {
		c = 40
	}
	return c
}

func (u *UI) rule() string { return u.dim(strings.Repeat("─", u.width()-2)) }

// boxLines desenha uma caixa com título; w é a largura total.
func (u *UI) boxLines(title string, lines []string, w int) []string {
	inner := w - 4
	top := "┌ " + title + " " + strings.Repeat("─", maxInt(0, w-4-utf8.RuneCountInString(title))) + "┐"
	out := []string{u.dim(top)}
	for _, l := range lines {
		out = append(out, u.dim("│")+" "+pad(trunc(l, inner), inner)+" "+u.dim("│"))
	}
	out = append(out, u.dim("└"+strings.Repeat("─", w-2)+"┘"))
	return out
}

// beside junta dois blocos de linhas lado a lado.
func beside(a, b []string, wa, gap int) []string {
	n := maxInt(len(a), len(b))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(a) {
			l = a[i]
		}
		if i < len(b) {
			r = b[i]
		}
		out = append(out, pad(l, wa)+strings.Repeat(" ", gap)+r)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// centered desenha uma caixa dupla com as linhas centralizadas.
func (u *UI) banner(lines ...string) []string {
	w := 0
	for _, l := range lines {
		w = maxInt(w, utf8.RuneCountInString(l))
	}
	w += 4
	out := []string{"", "  " + u.acc("╔"+strings.Repeat("═", w)+"╗")}
	for _, l := range lines {
		p := w - utf8.RuneCountInString(l)
		left := p / 2
		out = append(out, "  "+u.acc("║")+strings.Repeat(" ", left)+u.bold(l)+strings.Repeat(" ", p-left)+u.acc("║"))
	}
	return append(out, "  "+u.acc("╚"+strings.Repeat("═", w)+"╝"))
}

// ---------- desenho ----------

// draw mostra um quadro. No terminal de verdade, limpa e redesenha; nos testes,
// só acrescenta as linhas. Se o quadro for mais alto que a janela, corta o
// meio (o fim, com a linha de teclas, é o que mais importa) e avisa.
func (u *UI) draw(lines []string) {
	rows, cols := u.size()
	if rows >= 6 && len(lines) > rows-1 {
		keep := rows - 1
		cut := make([]string, 0, keep)
		cut = append(cut, lines[:keep-2]...)
		cut = append(cut, u.dim("  … (janela baixa: aumente a janela para ver tudo)"), lines[len(lines)-1])
		lines = cut
	}
	u.last = lines
	var b strings.Builder
	if u.live {
		b.WriteString("\x1b[H")
	}
	for _, l := range lines {
		b.WriteString(trunc(l, cols-1))
		if u.live {
			b.WriteString("\x1b[K")
		}
		b.WriteString("\n")
	}
	if u.live {
		b.WriteString("\x1b[J")
	}
	io.WriteString(u.out, b.String())
}

// compact diz se a janela é pequena demais para o desenho completo (menos de
// 24 linhas ou 66 colunas).
func (u *UI) compact() bool {
	rows, cols := u.size()
	return rows < 24 || cols < 66
}

// squash tira de um bloco de linhas as vazias e as réguas (só traços).
func squash(lines []string) []string {
	var out []string
	for _, l := range lines {
		p := strings.TrimSpace(stripANSI(l))
		if p == "" || strings.Trim(p, "─═ ") == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// stripANSI tira as sequências de cor de um texto.
func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case esc:
			if r == 'm' {
				esc = false
			}
		case r == 0x1b:
			esc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Item é uma linha de uma lista.
type Item struct {
	Label string
	Hint  string // texto cinza à direita
	Mark  string // "[x]", "◉"...
}

// Layout é o que uma lista mostra, reconstruído a cada tecla.
type Layout struct {
	Head  []string
	Items []Item
	Foot  []string
	Side  func(i int) []string // se não for nil, o item escolhido aparece numa área ao lado
}

// Pick descreve uma lista de escolha.
type Pick struct {
	Build   func() Layout
	Start   int
	OnSpace func(i int)                  // Espaço marca/desmarca
	OnKey   func(k KeyEvent, i int) bool // outras teclas; true = tratei
	Keys    string                       // texto de ajuda das teclas extras
	KeysFn  func() string                // o mesmo, quando o texto muda (por exemplo, o estilo)
	// Filterable liga o filtro com "/": os itens são reduzidos aos que contêm o
	// texto digitado. Tail é quantos itens do fim (Concluir, Voltar...) nunca
	// são filtrados. Os índices que a lista devolve e passa a OnSpace e OnKey
	// são sempre os da lista completa.
	Filterable bool
	Tail       int
}

// matches diz se um item contém o texto do filtro (sem diferenciar maiúsculas
// nem contar as cores).
func matches(it Item, f string) bool {
	f = strings.ToLower(f)
	return strings.Contains(strings.ToLower(stripANSI(it.Label)), f) || strings.Contains(strings.ToLower(stripANSI(it.Hint)), f)
}

// pick mostra a lista e devolve o item escolhido com Enter. ok é falso com Esc,
// Backspace, Ctrl+C ou fim da entrada (nestes dois, u.quit fica verdadeiro).
func (u *UI) pick(p Pick) (idx int, ok bool) {
	if u.quit {
		return -1, false
	}
	sel := p.Start
	top := 0
	filter, typing := "", false
	for {
		l := p.Build()
		n := len(l.Items)
		if sel >= n {
			sel = n - 1
		}
		if sel < 0 {
			sel = 0
		}
		// itens à mostra (índices da lista completa)
		var shown []int
		for i, it := range l.Items {
			if filter == "" || i >= n-p.Tail || matches(it, filter) {
				shown = append(shown, i)
			}
		}
		pos := -1
		for k, i := range shown {
			if i == sel {
				pos = k
			}
		}
		if pos < 0 && len(shown) > 0 { // o item escolhido saiu do filtro: vai ao primeiro que sobrou
			pos = 0
			sel = shown[0]
		}
		vl := l
		vl.Items = make([]Item, len(shown))
		for k, i := range shown {
			vl.Items[k] = l.Items[i]
		}
		var extra []string
		if p.Filterable && (typing || filter != "") {
			cursor := ""
			if typing {
				cursor = u.acc("▏")
			}
			count := len(shown) - minInt(p.Tail, len(shown))
			extra = []string{"  " + u.acc("/") + " " + filter + cursor + "  " + u.dim(fmt.Sprintf("(%d de %d)", count, n-minInt(p.Tail, n)))}
		}
		u.drawList(vl, maxInt(pos, 0), &top, p, extra)
		ev := u.in.Next()
		if typing {
			switch ev.K {
			case KEsc:
				filter, typing = "", false
				continue
			case KEnter:
				typing = false
				continue
			case KBackspace:
				if r := []rune(filter); len(r) > 0 {
					filter = string(r[:len(r)-1])
				} else {
					typing = false
				}
				continue
			case KRune:
				filter += string(ev.R)
				continue
			}
		}
		if p.Filterable && ev.K == KRune && ev.R == '/' {
			typing = true
			continue
		}
		if p.OnKey != nil && ev.K != KCtrlC && ev.K != KEOF && p.OnKey(ev, sel) {
			continue
		}
		page := maxInt(3, u.listRows(vl)-1)
		m := len(shown)
		move := func(to int) {
			if m > 0 {
				pos = to
				sel = shown[pos]
			}
		}
		switch ev.K {
		case KUp:
			if m > 0 {
				move((pos + m - 1) % m)
			}
		case KDown:
			if m > 0 {
				move((pos + 1) % m)
			}
		case KPgUp:
			move(maxInt(0, pos-page))
		case KPgDn:
			move(minInt(m-1, pos+page))
		case KHome:
			move(0)
		case KEnd:
			move(maxInt(0, m-1))
		case KEnter:
			if m > 0 {
				return shown[pos], true
			}
		case KEsc, KBackspace:
			if filter != "" { // o primeiro Esc só limpa o filtro
				filter = ""
				continue
			}
			return -1, false
		case KCtrlC, KEOF:
			u.quit = true
			return -1, false
		case KRune:
			switch ev.R {
			case ' ':
				if p.OnSpace != nil && m > 0 {
					p.OnSpace(sel)
				}
			case '?':
				u.help()
			}
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (u *UI) listRows(l Layout) int {
	rows, _ := u.size()
	r := rows - len(l.Head) - len(l.Foot) - 5
	if r < 3 {
		r = 3
	}
	return r
}

func (u *UI) hintLine(p Pick) string {
	var h string
	if u.compact() {
		h = "janela pequena (ideal 66×24) · ↑↓ Enter Esc"
		if p.OnSpace != nil {
			h += " Espaço"
		}
		if p.Filterable {
			h += " /"
		}
		return u.dim("  " + h + " ?")
	}
	h = "↑↓ mover · Enter escolher · Esc voltar"
	if p.OnSpace != nil {
		h = "↑↓ mover · Espaço marcar · Enter escolher · Esc voltar"
	}
	if p.Filterable {
		h += " · / filtrar"
	}
	k := p.Keys
	if p.KeysFn != nil {
		k = p.KeysFn()
	}
	if k != "" {
		h += " · " + k
	}
	return u.dim("  " + h + " · ? ajuda")
}

func (u *UI) drawList(l Layout, idx int, top *int, p Pick, extra []string) {
	rows, _ := u.size()
	head, foot := l.Head, l.Foot
	n := len(l.Items)
	// janela baixa: tira primeiro o rodapé, depois o enfeite do topo, para sobrar
	// espaço para ao menos 3 itens
	fixed := func() int { return len(head) + len(foot) + len(extra) + 2 } // mais a linha em branco e a das teclas
	room := func() int { return rows - 1 - fixed() }                      // linhas para a lista (a última da janela fica livre)
	need := minInt(n, 3)
	if room() < need {
		foot = squash(foot)
	}
	if room() < need {
		foot = nil
	}
	if room() < need {
		head = squash(head)
	}
	if room() < need && len(head) > 2 {
		head = head[len(head)-2:]
	}
	avail := room()
	if n > avail { // vai rolar: guarda duas linhas para "↑ mais" e "↓ mais"
		avail -= 2
	}
	if avail < 1 {
		avail = 1
	}
	var f []string
	f = append(f, head...)
	if *top > idx {
		*top = idx
	}
	if idx >= *top+avail {
		*top = idx - avail + 1
	}
	if *top < 0 {
		*top = 0
	}
	end := minInt(n, *top+avail)
	lw := 0
	for _, it := range l.Items[*top:end] {
		lw = maxInt(lw, vis(it.Label))
	}
	lw = minInt(lw, 36)
	var out []string
	if *top > 0 {
		out = append(out, u.dim(fmt.Sprintf("    ↑ mais %d acima", *top)))
	}
	for i := *top; i < end; i++ {
		it := l.Items[i]
		mark := ""
		if it.Mark != "" {
			mark = it.Mark + " "
		}
		label := pad(it.Label, lw)
		hint := ""
		if it.Hint != "" {
			hint = "  " + u.dim(it.Hint)
		}
		if i == idx {
			out = append(out, "  "+u.acc("❯")+" "+mark+u.bold(label)+hint)
		} else {
			out = append(out, "    "+mark+label+hint)
		}
	}
	if end < n {
		out = append(out, u.dim(fmt.Sprintf("    ↓ mais %d abaixo", n-end)))
	}
	if n == 0 && len(extra) > 0 {
		out = append(out, u.dim("    nenhum item combina com o filtro"))
	}
	if l.Side != nil && u.width() >= 72 && !u.compact() {
		lw := 0
		for _, r := range out {
			lw = maxInt(lw, vis(r))
		}
		lw = minInt(lw+4, u.width()/2)
		f = append(f, beside(u.boxLines("Menu", out, lw), u.boxLines("Sobre o item", l.Side(idx), u.width()-lw-3), lw, 1)...)
	} else {
		f = append(f, out...)
	}
	f = append(f, foot...)
	f = append(f, extra...)
	f = append(f, "", u.hintLine(p))
	u.draw(f)
}

// ---------- caixa de texto ----------

// input pede um texto. check devolve uma mensagem de problema; se soft for
// verdadeiro, enviar o mesmo texto de novo aceita mesmo assim.
func (u *UI) input(head []string, initial string, check func(string) (msg string, soft bool)) (string, bool) {
	if u.quit {
		return "", false
	}
	text := []rune(initial)
	cur := len(text)
	msg, warned := "", ""
	for {
		w := u.width() - 8
		start := 0
		if cur > w-1 {
			start = cur - w + 1
		}
		before := string(text[start:cur])
		after := string(text[cur:minInt(len(text), start+w)])
		line := before + u.acc("▏") + after
		f := append([]string{}, head...)
		f = append(f, "", "  "+u.dim("┌"+strings.Repeat("─", w+2)+"┐"), "  "+u.dim("│")+" "+pad(line, w+1)+u.dim("│"), "  "+u.dim("└"+strings.Repeat("─", w+2)+"┘"))
		if msg != "" {
			f = append(f, "  "+u.warn(msg))
		}
		f = append(f, "", u.dim("  Enter confirma · Esc cancela · ←→ move o cursor"))
		u.draw(f)
		ev := u.in.Next()
		switch ev.K {
		case KEsc:
			return "", false
		case KCtrlC, KEOF:
			u.quit = true
			return "", false
		case KEnter:
			s := string(text)
			if check != nil {
				m, soft := check(s)
				if m != "" && !(soft && warned == s) {
					msg = m
					if soft {
						msg += " (Enter de novo para salvar assim mesmo)"
						warned = s
					}
					continue
				}
			}
			return s, true
		case KLeft:
			cur = maxInt(0, cur-1)
		case KRight:
			cur = minInt(len(text), cur+1)
		case KHome:
			cur = 0
		case KEnd:
			cur = len(text)
		case KBackspace:
			if cur > 0 {
				text = append(text[:cur-1], text[cur:]...)
				cur--
			}
		case KDelete:
			if cur < len(text) {
				text = append(text[:cur], text[cur+1:]...)
			}
		case KRune:
			text = append(text[:cur], append([]rune{ev.R}, text[cur:]...)...)
			cur++
		}
		msg = ""
	}
}

// ---------- ajuda e avisos ----------

func (u *UI) help() {
	u.draw([]string{
		"", " " + u.bold("Ajuda: teclas"), "",
		"  ↑ ↓            mover a seleção (dá a volta no fim da lista)",
		"  Enter          escolher o item",
		"  Espaço         marcar e desmarcar (onde houver [ ] ou [x])",
		"  Esc            voltar uma tela (na tradução, pausar)",
		"  PgUp PgDn      rolar listas longas; Home e End vão ao começo e ao fim",
		"  /              filtrar listas longas (Enter fixa o filtro, Esc limpa)",
		"  ?              esta ajuda",
		"  Ctrl+C         sair (na tradução, pausa e espera o lote atual)",
		"",
		"  " + u.dim("Nada aqui apaga a pasta de trabalho nem o cache."),
		"", u.dim("  (qualquer tecla volta)"),
	})
	if ev := u.in.Next(); ev.K == KCtrlC || ev.K == KEOF {
		u.quit = true
	}
}

// notice mostra uma tela curta e espera uma tecla.
func (u *UI) notice(lines ...string) {
	if u.quit {
		return
	}
	u.draw(append(append([]string{""}, lines...), "", u.dim("  (Enter volta)")))
	if ev := u.in.Next(); ev.K == KCtrlC || ev.K == KEOF {
		u.quit = true
	}
}

// confirm mostra uma pergunta com Sim e Não (Não vem escolhido, a menos que def).
func (u *UI) confirm(head []string, yes string, def bool) bool {
	start := 1
	if def {
		start = 0
	}
	i, ok := u.pick(Pick{Start: start, Build: func() Layout {
		return Layout{Head: append([]string{""}, head...), Items: []Item{{Label: yes}, {Label: "Não, voltar"}}}
	}})
	return ok && i == 0
}

func (u *UI) cmd(s string) string { return "  " + u.dim("equivale a: ") + u.acc(s) }
