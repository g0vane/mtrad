// mtrad-prototype é uma demonstração visual isolada. Não chama o backend,
// não traduz, não instala, não grava configuração e não apaga arquivos.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type screen int

const (
	chooseStyle screen = iota
	home
	instances
	translate
	review
	results
	history
	settings
	help
)

var styleNames = []string{"Clássica", "Painéis", "Dashboard"}
var menu = []string{"Visão geral", "Instâncias", "Traduzir", "Revisar falhas", "Resultados", "Histórico", "Configurações", "Trocar interface", "Ajuda", "Sair"}

type app struct {
	style, styleCursor, cursor, itemCursor int
	page                                   screen
	notice                                 string
}

func main() {
	if !terminal() {
		fmt.Println("Execute em um terminal interativo: go run ./cmd/mtrad-prototype")
		os.Exit(1)
	}
	old, err := stty("-g")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Não consegui configurar o terminal:", err)
		os.Exit(1)
	}
	defer func() { _, _ = stty(old); fmt.Print("\x1b[0m\x1b[?25h\n") }()
	_, _ = stty("-icanon -echo -isig min 0 time 1") // não usar "raw": ele desliga o \n -> \r\n e a tela sai em escada
	fmt.Print("\x1b[?25l")
	a := app{page: chooseStyle}
	for {
		a.draw()
		key := readKey()
		if a.handle(key) {
			return
		}
	}
}
func terminal() bool { fi, e := os.Stdin.Stat(); return e == nil && fi.Mode()&os.ModeCharDevice != 0 }
func stty(s string) (string, error) {
	c := exec.Command("stty", strings.Fields(s)...)
	c.Stdin = os.Stdin
	b, e := c.Output()
	return strings.TrimSpace(string(b)), e
}
func readByte() byte {
	var b [1]byte
	n, _ := os.Stdin.Read(b[:])
	if n == 0 {
		return 0
	}
	return b[0]
}
func readKey() string {
	b := readByte()
	if b == 0 {
		return ""
	}
	switch b {
	case 3:
		return "CTRL_C"
	case 13, 10:
		return "ENTER"
	case 127, 8:
		return "BACK"
	case 32:
		return "SPACE"
	case 27:
		b2 := readByte()
		if b2 == 0 {
			return "ESC"
		}
		if b2 != '[' {
			return "ESC"
		}
		b3 := readByte()
		switch b3 {
		case 'A':
			return "UP"
		case 'B':
			return "DOWN"
		case 'C':
			return "RIGHT"
		case 'D':
			return "LEFT"
		}
		return "ESC"
	default:
		if b >= 32 && b < 127 {
			return strings.ToLower(string(b))
		}
		return ""
	}
}
func (a *app) handle(k string) bool {
	if k == "CTRL_C" {
		return true
	}
	if k == "?" {
		a.page = help
		return false
	}
	if k == "i" && a.page != chooseStyle {
		a.page = chooseStyle
		a.styleCursor = a.style
		return false
	}
	if a.page == chooseStyle {
		switch k {
		case "UP":
			a.styleCursor = (a.styleCursor + len(styleNames) - 1) % len(styleNames)
		case "DOWN":
			a.styleCursor = (a.styleCursor + 1) % len(styleNames)
		case "ENTER":
			a.style = a.styleCursor
			a.page = home
			a.cursor = 0
		case "ESC", "q":
			a.page = home
		}
		return false
	}
	if a.page == home {
		switch k {
		case "UP":
			a.cursor = (a.cursor + len(menu) - 1) % len(menu)
		case "DOWN":
			a.cursor = (a.cursor + 1) % len(menu)
		case "ESC":
			a.page = chooseStyle
			a.styleCursor = a.style
		case "q":
			return true
		case "ENTER":
			switch a.cursor {
			case 0:
				a.page = home
			case 1:
				a.page = instances
			case 2:
				a.page = translate
			case 3:
				a.page = review
			case 4:
				a.page = results
			case 5:
				a.page = history
			case 6:
				a.page = settings
			case 7:
				a.page = chooseStyle
				a.styleCursor = a.style
			case 8:
				a.page = help
			case 9:
				return true
			}
			a.itemCursor = 0
		}
		return false
	}
	switch k {
	case "ESC", "BACK", "LEFT":
		a.page = home
		a.cursor = 0
	case "UP":
		if a.itemCursor > 0 {
			a.itemCursor--
		}
	case "DOWN":
		a.itemCursor++
	case "ENTER", "SPACE":
		a.notice = "Demonstração visual: nenhuma ação real foi executada."
	case "r":
		a.notice = "Prévia atualizada (dados fictícios)."
	}
	return false
}
func (a *app) draw() {
	fmt.Print("\x1b[H\x1b[2J")
	switch a.page {
	case chooseStyle:
		a.drawStyle()
	case home:
		a.drawHome()
	case instances:
		a.drawPage("Instâncias", []string{"Reclamation - Reclaim the World-", "Fabric Vanilla+"}, []string{"Minecraft 1.20.1 · Forge 47.3.0", "Minecraft 1.21.1 · Fabric 0.16.9"}, "Escolha uma instância para ver suas ações.")
	case translate:
		a.drawPage("Traduzir", []string{"Teste pequeno · 50 textos", "Tradução completa · retoma o progresso", "Selecionar mods incluídos", "Ver estado do mecanismo"}, []string{"Simulação; nenhum pedido será enviado", "17.031 textos pendentes (fictício)", "11 mods detectados (fictício)", "Ollama disponível (fictício)"}, "Escolha uma opção para visualizar o fluxo.")
	case review:
		a.drawPage("Revisar falhas", []string{"quest.4F2A.description[2]", "theurgy:item.sal_ammoniac", "kubejs:tooltip.shift", "quark:config.tab.tweaks", "create:tooltip.stress"}, []string{"Marcador de cor ausente", "Texto igual ao original", "Placeholder %s ausente", "Explicação extra na resposta", "Marcador de formatação ausente"}, "Dados ilustrativos; a revisão real não está conectada.")
	case results:
		a.drawPage("Resultados", []string{"Validar traduções", "Pré-visualizar arquivos", "Gerar pacote de tradução", "Instalar arquivos FTB Quests (simulação)"}, []string{"3 problemas ilustrativos", "lang/pt_br.json · Patchouli · FTB Quests", "Somente prévia nesta demonstração", "Nenhuma cópia será feita"}, "Nenhuma instalação ou gravação é executada.")
	case history:
		a.drawPage("Histórico", []string{"Execução de demonstração · 28/10 02:53", "Execução anterior · 27/10 19:14", "Abrir diagnóstico"}, []string{"17.031 textos · 41m12s (fictício)", "8.200 textos · 22m08s (fictício)", "Logs ilustrativos"}, "Os registros mostrados são exemplos.")
	case settings:
		a.drawPage("Configurações", []string{"Motor de tradução", "Modelo", "Pasta de trabalho", "Fontes: lang / Patchouli / FTB Quests", "Estilo visual: " + styleNames[a.style]}, []string{"Ollama (fictício)", "translategemma:4b (fictício)", "work-real (fictício)", "Ativadas (fictício)", "Pressione i para abrir o seletor"}, "As configurações não são lidas nem salvas.")
	case help:
		a.drawHelp()
	}
	fmt.Print("\n\n  ↑/↓ navegar   Enter selecionar   Esc voltar   i trocar interface   ? ajuda   q sair")
}
func (a *app) header(title, sub string) {
	fmt.Println(cyan("mtrad") + "  " + dim("protótipo visual · sem backend"))
	fmt.Println(strings.Repeat("─", 72))
	fmt.Println(bold(title))
	if sub != "" {
		fmt.Println(dim(sub))
	}
	fmt.Println()
}
func (a *app) drawStyle() {
	a.header("Escolha o estilo da interface", "Use as setas para comparar. Enter abre a interface selecionada.")
	desc := []string{"Menu vertical, simples e direto", "Navegação lateral com conteúdo ao lado", "Resumo com blocos de status"}
	for i, s := range styleNames {
		p := "  "
		n := s
		if i == a.styleCursor {
			p = cyan("❯ ")
			n = bold(n)
		}
		fmt.Printf("%s%-14s %s\n", p, n, desc[i])
	}
	fmt.Println("\n" + dim("Prévia"))
	switch a.styleCursor {
	case 0:
		fmt.Println("  Início\n  Instâncias\n  Traduzir\n  Revisar falhas\n  Resultados\n  Histórico\n  Configurações")
	case 1:
		fmt.Println("  ┌ Menu ──────────┐   Conteúdo da área selecionada\n  │ ❯ Início       │   Instância: Reclamation (exemplo)\n  │   Instâncias   │   Estado: pronto para demonstração\n  │   Traduzir     │   Falhas: 196 (dado fictício)\n  └────────────────┘")
	case 2:
		fmt.Println("  ┌ Instância atual ───────┐  ┌ Traduções ───────────┐\n  │ Reclamation (exemplo)   │  │ 19.228 concluídas    │\n  └────────────────────────┘  └──────────────────────┘\n  ┌ Falhas ────────────────┐  ┌ Última execução ─────┐\n  │ 196 para revisar       │  │ 28/10 · fictício     │\n  └────────────────────────┘  └──────────────────────┘")
	}
	fmt.Println("\n" + dim("Transparência global: nenhum fundo colorido é desenhado."))
}
func (a *app) drawHome() {
	a.header("Visão geral", "Demonstração com dados fictícios. Nada será traduzido, instalado ou salvo.")
	switch a.style {
	case 0:
		for i, x := range menu {
			p := "  "
			if i == a.cursor {
				p = cyan("❯ ")
				x = bold(x)
			}
			fmt.Println(p + x)
		}
	case 1:
		fmt.Println("  ÁREAS                                      VISÃO GERAL")
		fmt.Println("  ┌ Menu ──────────────────┐                 Instância: Reclamation (exemplo)")
		for i, x := range menu {
			p := " "
			if i == a.cursor {
				p = cyan("❯")
			}
			fmt.Printf("  │ %s %-20s │                 Traduções: 19.228 concluídas\n", p, x)
		}
		fmt.Println("  └────────────────────────┘                 Falhas: 196 para revisar")
	case 2:
		fmt.Println("  ┌ Instância atual ─────────────────┐   ┌ Traduções ──────────────┐")
		fmt.Println("  │ Reclamation - Reclaim the World- │   │ 19.228 concluídas       │")
		fmt.Println("  │ Minecraft 1.20.1 · Forge         │   │ 196 falhas para revisar │")
		fmt.Println("  └──────────────────────────────────┘   └────────────────────────┘\n\n  Ações")
		for i, x := range menu {
			p := "  "
			if i == a.cursor {
				p = cyan("❯ ")
				x = bold(x)
			}
			fmt.Println(p + x)
		}
		fmt.Println("\n" + dim("Dica: pressione i a qualquer momento para trocar o estilo."))
	}
	if a.notice != "" {
		fmt.Println("\n" + yellow(a.notice))
	}
}
func (a *app) drawPage(title string, items, details []string, sub string) {
	a.header(title, sub)
	for i, x := range items {
		p := "  "
		if i == a.itemCursor {
			p = cyan("❯ ")
			x = bold(x)
		}
		d := ""
		if i < len(details) {
			d = "  " + dim("— "+details[i])
		}
		fmt.Println(p + x + d)
	}
	if a.notice != "" {
		fmt.Println("\n" + yellow(a.notice))
	}
}
func (a *app) drawHelp() {
	a.header("Ajuda e navegação", "Todas as telas são demonstração visual.")
	fmt.Println("  ↑ / ↓     Mover a seleção\n  Enter     Abrir / selecionar item\n  Esc       Voltar ao menu principal\n  i         Abrir seletor de estilo em qualquer tela\n  ?         Abrir esta ajuda\n  q         Sair do protótipo\n\n  Transparência: nenhum estilo aplica cor de fundo ANSI.\n  Segurança: não há integração com tradutor, Prism Launcher ou arquivos.\n\n" + dim("A transparência real depende de como o terminal trata seu fundo padrão."))
}
func cyan(s string) string   { return "\x1b[36m" + s + "\x1b[0m" }
func bold(s string) string   { return "\x1b[1m" + s + "\x1b[0m" }
func dim(s string) string    { return "\x1b[2m" + s + "\x1b[0m" }
func yellow(s string) string { return "\x1b[33m" + s + "\x1b[0m" }
