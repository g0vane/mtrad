package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"mtrad/internal/fmtx"
)

// Run abre o menu inicial e fica nele até o usuário sair.
func (u *UI) Run() error {
	for !u.quit {
		items := []string{"Configurar (setup)"}
		if u.st.SetupDone {
			items = append(items, "Selecionar sua instância")
		}
		items = append(items, "Sair")
		start := 0
		if u.st.SetupDone {
			start = 1
		}
		i, ok := u.pick(Pick{Start: start, Keys: "q sair", OnKey: func(k KeyEvent, _ int) bool {
			if k.K == KRune && (k.R == 'q' || k.R == 'Q') {
				u.quit = true
				return true
			}
			return false
		}, Build: func() Layout {
			head := u.banner("m t r a d", "", "Tradutor de mods de Minecraft para português do Brasil", "versão "+u.ver)
			note := "Configuração: " + u.st.Config
			if u.be.IsDemo() {
				note = "Modo demonstração: nada é executado de verdade."
			}
			head = append(head, "", "  "+u.bold("Bem-vindo!"), "  "+u.dim(note), "")
			var its []Item
			for _, t := range items {
				its = append(its, Item{Label: t})
			}
			var foot []string
			if !u.st.SetupDone {
				foot = []string{"", "  " + u.dim("Primeira vez por aqui? Comece pela configuração: leva poucos passos.")}
			}
			return Layout{Head: head, Items: its, Foot: foot}
		}})
		if !ok {
			if u.quit {
				break
			}
			continue // Esc no menu inicial não faz nada
		}
		switch items[i] {
		case "Configurar (setup)":
			u.setup()
		case "Selecionar sua instância":
			u.chooseInstance()
		case "Sair":
			u.quit = true
		}
	}
	return nil
}

// ---------- setup em passos ----------

type nav int

const (
	next nav = iota
	back
	cancel
)

func (u *UI) stepHead(n int, title string) []string {
	return []string{"", u.rule(), " " + u.dim(fmt.Sprintf("Passo %d de 6", n)) + "  " + u.bold(title), u.rule(), ""}
}

func (u *UI) setup() {
	steps := []func() nav{u.stepEnv, u.stepEngine, u.stepModel, u.stepSources, u.stepWork, u.stepSummary}
	for i := 0; i < len(steps) && !u.quit; {
		switch steps[i]() {
		case next:
			i++
		case back:
			if i == 0 {
				return
			}
			i--
		case cancel:
			return
		}
	}
}

func (u *UI) stepEnv() nav {
	for {
		checks := u.be.Doctor()
		bad := 0
		for _, c := range checks {
			if !c.OK && !c.Warn {
				bad++
			}
		}
		hw := u.be.Hardware()
		items := []Item{{Label: "Continuar"}}
		if bad > 0 {
			inst := Item{Label: "Instalar agora", Hint: "pede confirmação; a senha é do sudo"}
			if !u.be.IsDemo() {
				inst = Item{Label: "Como instalar", Hint: "mostra os comandos para rodar num terminal"}
			}
			items = []Item{inst, {Label: "Verificar de novo"}, {Label: "Continuar assim mesmo"}}
		}
		i, ok := u.pick(Pick{Build: func() Layout {
			head := u.stepHead(1, "Conferir o computador")
			for _, c := range checks {
				head = append(head, fmt.Sprintf("  %s %s  %s", u.checkMark(c), pad(c.Name, 40), u.dim(c.Detail)))
			}
			head = append(head, "")
			for _, l := range hw.Lines {
				head = append(head, "  "+u.dim("·")+" "+l)
			}
			if hw.Why != "" {
				head = append(head, "  "+u.ok("→")+" "+hw.Why)
			}
			if len(hw.Lines) > 0 {
				head = append(head, "")
			}
			if bad == 0 {
				head = append(head, "  "+u.ok("✓")+" tudo certo com o Ollama, o modelo e a placa de vídeo.")
			} else {
				lead := " falta algo. No Arch, rode num terminal (o mtrad não executa sudo por você):"
				if u.be.IsDemo() {
					lead = " falta algo. No Arch, o mtrad rodaria (só com a sua confirmação):"
				}
				head = append(head, "  "+u.warn("!")+lead,
					"    "+u.acc("sudo pacman -S ollama ollama-vulkan"),
					"    "+u.acc("sudo systemctl enable --now ollama"),
					"    "+u.acc("ollama pull "+u.st.Model))
			}
			head = append(head, u.cmd("mtrad doctor"), "")
			return Layout{Head: head, Items: items}
		}})
		if !ok {
			return u.escNav()
		}
		switch items[i].Label {
		case "Como instalar":
			u.notice(" "+u.bold("Instalar o Ollama e o modelo"), "",
				"  Abra outro terminal e rode, um por vez:", "",
				"    "+u.acc("sudo pacman -S ollama ollama-vulkan"), "    "+u.acc("sudo systemctl enable --now ollama"), "    "+u.acc("ollama pull "+u.st.Model)+"   (cerca de 3,3 GB)", "",
				"  "+u.dim("Em outras distribuições, veja ollama.com/download. A senha é pedida pelo sudo; o mtrad não a vê."),
				"  "+u.dim("Depois volte aqui e escolha \"Verificar de novo\"."))
		case "Instalar agora":
			if u.confirm([]string{" " + u.bold("Instalar o Ollama e o modelo?"), "", "  sudo pacman -S ollama ollama-vulkan", "  sudo systemctl enable --now ollama", "  ollama pull " + u.st.Model + "   (cerca de 3,3 GB)", "", "  " + u.dim("Quem pede a senha é o sudo; o mtrad não a guarda."), "  " + u.dim("Na demonstração, nada é executado.")}, "Sim, instalar", false) {
				u.notice("  " + u.ok("✓") + " simulado: na versão de verdade, os três comandos rodariam aqui.")
			}
		case "Verificar de novo":
		default:
			return next
		}
	}
}

func (u *UI) checkMark(c Check) string {
	switch {
	case c.OK:
		return u.ok("✓")
	case c.Warn:
		return u.warn("!")
	}
	return u.bad("✗")
}

// escNav diz o que fazer quando o usuário aperta Esc num passo.
func (u *UI) escNav() nav {
	if u.quit {
		return cancel
	}
	return back
}

var engineInfo = []struct{ name, desc string }{
	{"ollama", "modelo local pelo Ollama (grátis, na sua placa) · recomendado"},
	{"llama", "modelo local pelo llama.cpp (mais manual)"},
	{"gemini", "API do Google (grátis com limite; precisa de chave)"},
	{"libre", "LibreTranslate local (qualidade menor)"},
	{"ct2", "tradutor neural em CPU (lento, precisa de Python)"},
	{"openai", "API da OpenAI (paga; precisa de chave e do nome do modelo)"},
	{"openrouter", "um endereço para vários modelos (precisa de chave e do nome do modelo)"},
}

func (u *UI) stepEngine() nav {
	start := 0
	for i, e := range engineInfo {
		if e.name == u.st.Engine {
			start = i
		}
	}
	i, ok := u.pick(Pick{Start: start, Build: func() Layout {
		var its []Item
		for _, e := range engineInfo {
			m := "○"
			if e.name == u.st.Engine {
				m = "◉"
			}
			its = append(its, Item{Mark: m, Label: e.name, Hint: e.desc})
		}
		return Layout{Head: u.stepHead(2, "Escolher o motor de tradução"), Items: its}
	}})
	if !ok {
		return u.escNav()
	}
	if u.st.Engine != engineInfo[i].name {
		u.st.Engine = engineInfo[i].name
		u.st.Model = defaultModel(u.st.Engine)
	}
	return next
}

func defaultModel(engine string) string {
	switch engine {
	case "ollama":
		return "translategemma:4b"
	case "gemini":
		return "gemini-2.0-flash"
	case "openai", "openrouter":
		return "" // nomes de modelo de nuvem mudam: quem escolhe é o usuário
	}
	return "padrão do motor"
}

func (u *UI) stepModel() nav {
	if _, ok := cloudHosts[u.st.Engine]; ok {
		return u.stepCloudModel()
	}
	if u.st.Engine != "ollama" && u.st.Engine != "gemini" {
		_, ok := u.pick(Pick{Build: func() Layout {
			head := u.stepHead(3, "Escolher o modelo")
			head = append(head, "  O motor "+u.st.Engine+" usa o modelo do próprio serviço.", "")
			if u.st.Engine == "llama" {
				// o llama-server usa o arquivo GGUF que você carregou; a sugestão de tamanho vale igual
				if hw := u.be.Hardware(); hw.Why != "" {
					head = append(head, "  "+u.ok("→")+" "+hw.Why,
						"  "+u.dim("Escolha um arquivo GGUF que caiba na sua placa. Os tamanhos da lista são os do Ollama: confira o tamanho do arquivo."), "")
				}
			}
			return Layout{Head: head, Items: []Item{{Label: "Continuar"}}}
		}})
		if !ok {
			return u.escNav()
		}
		return next
	}
	type m = modelChoice
	var ms []m
	var hw Hardware
	custom := false // a última linha da lista deixa digitar outro nome
	if u.st.Engine == "ollama" {
		custom = true
		hw = u.be.Hardware()
		ms = ollamaChoices(u.be.Models(), hw, u.st.Model)
	} else {
		custom = true
		ms = []m{{"gemini-2.0-flash", "precisa de GEMINI_API_KEY"}, {"gemini-2.5-flash-lite", "precisa de GEMINI_API_KEY"}}
		if cur := u.st.Model; cur != "" && cur != "padrão do motor" && cur != ms[0].name && cur != ms[1].name {
			ms = append([]m{{cur, "o que já está na configuração"}}, ms...)
		}
	}
	start := 0
	for k, x := range ms {
		if x.name == u.st.Model {
			start = k
		}
	}
	i, ok := u.pick(Pick{Start: start, Build: func() Layout {
		head := u.stepHead(3, "Escolher o modelo")
		if u.st.Engine == "gemini" {
			head = append(head, "  "+u.warn("!")+" exporte GEMINI_API_KEY antes de traduzir; o limite grátis é do Google.", "")
		}
		if hw.Why != "" {
			head = append(head, "  "+u.ok("→")+" "+hw.Why)
			if hw.Checked != "" {
				head = append(head, "  "+u.dim("tamanhos conferidos em "+hw.Checked+"; a lista não é completa: use a última linha para outro modelo"))
			}
			head = append(head, "")
		}
		var its []Item
		for _, x := range ms {
			mk := "○"
			if x.name == u.st.Model {
				mk = "◉"
			}
			its = append(its, Item{Mark: mk, Label: x.name, Hint: x.hint})
		}
		if custom {
			its = append(its, Item{Mark: "○", Label: "Digitar outro nome…", Hint: otherHint(u.st.Engine)})
		}
		return Layout{Head: head, Items: its}
	}})
	if !ok {
		return u.escNav()
	}
	if i >= len(ms) { // "Digitar outro nome…"
		name, ok := u.input(append(u.stepHead(3, "Escolher o modelo"), "  "+otherPrompt(u.st.Engine)), "", func(s string) (string, bool) {
			s = strings.TrimSpace(s)
			switch {
			case s == "":
				return "digite o nome do modelo", false
			case strings.ContainsAny(s, " \t"):
				return "o nome não pode ter espaço", false
			}
			return "", false
		})
		if !ok {
			return u.escNav()
		}
		u.st.Model = strings.TrimSpace(name)
		return next
	}
	u.st.Model = ms[i].name
	return next
}

// cloudHosts são os provedores de nuvem do setup: para onde os textos vão e
// o nome da variável de ambiente da chave. Espelha cloudPresets em cmd/mtrad.
var cloudHosts = map[string]struct{ host, env string }{
	"openai":     {"api.openai.com", "OPENAI_API_KEY"},
	"openrouter": {"openrouter.ai", "OPENROUTER_API_KEY"},
}

func otherHint(engine string) string {
	if engine == "ollama" {
		return "qualquer modelo que o `ollama list` mostre"
	}
	return "o nome do modelo na sua conta"
}

func otherPrompt(engine string) string {
	if engine == "ollama" {
		return "Nome do modelo, como o `ollama list` mostra (exemplo: gemma3:12b):"
	}
	return "Nome do modelo na sua conta:"
}

// stepCloudModel pede o nome do modelo de um provedor de nuvem. Não há lista:
// os nomes mudam e o mtrad não os conhece de antemão.
func (u *UI) stepCloudModel() nav {
	c := cloudHosts[u.st.Engine]
	head := u.stepHead(3, "Escolher o modelo")
	head = append(head,
		"  "+u.warn("!")+" exporte "+c.env+" antes de traduzir; o mtrad só guarda o NOME da variável.",
		"  "+u.warn("!")+" os textos dos mods serão enviados para "+c.host+" e o provedor pode cobrar.",
		"", "  Nome do modelo na sua conta (a lista está no site do provedor):")
	name, ok := u.input(head, u.st.Model, func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		switch {
		case s == "":
			return "digite o nome do modelo", false
		case strings.ContainsAny(s, " \t"):
			return "o nome não pode ter espaço", false
		}
		return "", false
	})
	if !ok {
		return u.escNav()
	}
	u.st.Model = strings.TrimSpace(name)
	return next
}

// modelChoice é uma linha da lista de modelos.
type modelChoice struct{ name, hint string }

// ollamaChoices junta os modelos instalados com os do catálogo que rodam neste
// computador (com a dica de onde cada um cabe) e, se a configuração tem um
// modelo fora das duas listas, o mantém no topo para não ser perdido.
func ollamaChoices(installed []string, hw Hardware, current string) []modelChoice {
	type m = modelChoice
	opt := func(name string) (ModelOption, bool) {
		for _, o := range hw.Options {
			if o.Name == name || strings.HasPrefix(name, o.Name+"-") {
				return o, true
			}
		}
		return ModelOption{}, false
	}
	var out []m
	seen := map[string]bool{}
	for _, name := range installed {
		h := "instalado"
		if o, ok := opt(name); ok {
			h += " · " + o.Fit
			if o.Recommended {
				h += " · recomendado"
			}
			seen[o.Name] = true
		}
		seen[name] = true
		out = append(out, m{name, h})
	}
	for _, o := range hw.Options {
		if seen[o.Name] || !o.Runs {
			continue
		}
		h := fmt.Sprintf("ollama pull %s · %s GB · %s", o.Name, strings.Replace(fmt.Sprintf("%.1f", o.SizeGiB), ".", ",", 1), o.Fit)
		if len(installed) > 0 {
			h = "não instalado · " + h
		}
		if o.Recommended {
			h += " · recomendado"
		}
		out = append(out, m{o.Name, h})
		seen[o.Name] = true
	}
	if current != "" && current != "padrão do motor" && !seen[current] {
		out = append([]m{{current, "o que já está na configuração"}}, out...)
	}
	if len(out) == 0 {
		out = append(out, m{defaultModel("ollama"), "ollama pull " + defaultModel("ollama")})
	}
	return out
}

func (u *UI) stepSources() nav {
	keys := []string{"lang", "patchouli", "ftbq"}
	info := map[string]string{"lang": "nomes, itens e dicas dos mods", "patchouli": "livros de guia dentro do jogo", "ftbq": "missões do FTB Quests"}
	toggle := func(i int) {
		if i >= len(keys) {
			return
		}
		k := keys[i]
		if u.st.Sources[k] {
			delete(u.st.Sources, k)
		} else {
			u.st.Sources[k] = true
		}
		if len(u.st.Sources) == 0 { // ao menos uma
			u.st.Sources[k] = true
		}
	}
	for {
		i, ok := u.pick(Pick{OnSpace: toggle, Build: func() Layout {
			head := u.stepHead(4, "O que traduzir")
			head = append(head, "  "+u.dim("Espaço marca e desmarca; escolha Continuar para seguir. Ao menos uma fonte fica marcada."), "")
			var its []Item
			for _, k := range keys {
				mk := "[ ]"
				if u.st.Sources[k] {
					mk = u.ok("[x]")
				}
				its = append(its, Item{Mark: mk, Label: k, Hint: info[k]})
			}
			its = append(its, Item{Label: "Continuar"})
			return Layout{Head: head, Items: its}
		}})
		if !ok {
			return u.escNav()
		}
		if i == len(keys) {
			return next
		}
		toggle(i)
	}
}

func (u *UI) stepWork() nav {
	i, ok := u.pick(Pick{Build: func() Layout {
		head := u.stepHead(5, "Onde guardar o trabalho")
		head = append(head, "  Pasta de trabalho: "+u.bold(u.st.Work)+"  "+u.dim("(tradução, cache e registros; nunca apague)"), "  Arquivo de configuração: "+u.bold(u.st.Config), "")
		return Layout{Head: head, Items: []Item{{Label: "Usar " + u.st.Work}, {Label: "Usar outra pasta…", Hint: "digitar o caminho"}}}
	}})
	if !ok {
		return u.escNav()
	}
	if i == 1 {
		if s, ok := u.input(u.stepHead(5, "Onde guardar o trabalho"), u.st.Work, func(s string) (string, bool) {
			if strings.TrimSpace(s) == "" {
				return "digite um nome de pasta", false
			}
			return "", false
		}); ok {
			u.st.Work = strings.TrimSpace(s)
		}
		if u.quit {
			return cancel
		}
		return u.stepWork()
	}
	return next
}

func (u *UI) stepSummary() nav {
	i, ok := u.pick(Pick{Build: func() Layout {
		head := u.stepHead(6, "Resumo")
		head = append(head,
			"  Motor      "+u.st.Engine+" · "+u.st.Model,
			"  Fontes     "+u.sourcesText(),
			"  Trabalho   "+u.st.Work,
			"  Arquivo    "+u.st.Config,
			u.cmd(fmt.Sprintf("mtrad init -o %s --engine %s --model '%s'", u.st.Config, u.st.Engine, u.st.Model)), "")
		return Layout{Head: head, Items: []Item{{Label: "Gravar a configuração"}, {Label: "Voltar"}, {Label: "Cancelar", Hint: "nada é gravado"}}}
	}})
	if !ok {
		return u.escNav()
	}
	switch i {
	case 0:
		var srcs []string
		for _, k := range []string{"lang", "patchouli", "ftbq"} {
			if u.st.Sources[k] {
				srcs = append(srcs, k)
			}
		}
		if err := u.be.SaveSetup(Setup{Engine: u.st.Engine, Model: u.st.Model, Sources: srcs, Work: u.st.Work, Config: u.st.Config}); err != nil {
			u.notice("  "+u.bad("✗")+" não consegui gravar a configuração:", "  "+err.Error(), "  "+u.dim("Nada foi alterado. Volte e escolha outro arquivo ou pasta."))
			return back
		}
		u.st.SetupDone = true
		what := "gravada em " + u.st.Config
		if u.be.IsDemo() {
			what = "gravada (simulado)"
		}
		u.notice("  "+u.ok("✓")+" configuração "+what+".", "  Agora a opção "+u.bold("Selecionar sua instância")+" aparece no menu inicial.")
		return cancel
	case 1:
		return back
	}
	return cancel
}

func (u *UI) sourcesText() string {
	var l []string
	for _, k := range []string{"lang", "patchouli", "ftbq"} {
		if u.st.Sources[k] {
			l = append(l, k)
		}
	}
	return strings.Join(l, ", ")
}

// ---------- instância ----------

func (u *UI) chooseInstance() {
	list := u.be.Instances()
	for !u.quit {
		i, ok := u.pick(Pick{Filterable: true, Tail: 2, Build: func() Layout {
			head := []string{"", " " + u.bold("Selecione sua instância"), "  " + u.dim("Instâncias do Prism encontradas (Flatpak e instalação normal)"), ""}
			if len(list) == 0 {
				head = append(head, "  "+u.warn("!")+" não achei nenhuma instância nas pastas de costume do Prism.", "  "+u.dim("Digite o caminho da pasta da instância abaixo."), "")
			}
			var its []Item
			for _, in := range list {
				its = append(its, Item{Label: in.Name, Hint: fmt.Sprintf("Minecraft %s · %s %s · pacote %s", in.MC, in.Loader, in.LoaderVer, in.PackFormat)})
			}
			its = append(its, Item{Label: "Digitar o caminho de outra pasta…"}, Item{Label: "Voltar"})
			return Layout{Head: head, Items: its}
		}})
		if !ok || i == len(list)+1 {
			return
		}
		var in Instance
		if i == len(list) {
			path, ok := u.input([]string{"", " " + u.bold("Caminho da instância")}, "", func(s string) (string, bool) {
				if strings.TrimSpace(s) == "" {
					return "digite o caminho da pasta da instância", false
				}
				return "", false
			})
			if !ok {
				continue
			}
			path = strings.TrimSpace(path)
			in = Instance{Name: pathBase(path), Path: path, MC: "?", Loader: "?", LoaderVer: "?", PackFormat: "?"}
		} else {
			in = list[i]
		}
		full, err := u.be.SelectInstance(in)
		if err != nil {
			u.notice("  "+u.bad("✗")+" não consegui usar esta instância:", "  "+err.Error(), "  "+u.dim("Escolha outra, ou confira se a pasta tem minecraft/ ou .minecraft/."))
			continue
		}
		u.st.Inst = &full
		u.st.Skip = map[string]bool{}
		for _, id := range u.be.Skipped() {
			u.st.Skip[id] = true
		}
		u.instanceMenu()
		return
	}
}

func pathBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (u *UI) pending() int {
	if n := u.be.Status().Fail; n > 0 {
		return n
	}
	return 0
}

func (u *UI) gpuText() string {
	s := u.be.Status()
	if u.st.Engine != "ollama" {
		return u.dim("motor externo")
	}
	if !s.OllamaUp {
		return u.bad("Ollama parado")
	}
	if s.Idle {
		return u.ok("Ollama ok") + u.dim(" · modelo ainda não carregado")
	}
	if s.OnGPU {
		return u.ok("Ollama ok · na GPU")
	}
	return u.warn("Ollama ok · na CPU (lento)")
}

// workDir é a pasta de trabalho em uso: a da instância, se o backend disser, ou
// a escolhida no setup.
func (u *UI) workDir() string {
	if w := u.be.Status().Work; w != "" {
		return w
	}
	return u.st.Work
}

// lastRun é o texto da última execução ("nenhuma ainda" se não houve).
func (u *UI) lastRun(s Status) string {
	if !s.Known || s.LastRun == "" {
		return "nenhuma ainda"
	}
	return s.LastRun
}

// statusHead monta o painel de estado no estilo escolhido (Clássica ou Dashboard).
func (u *UI) statusHead() []string {
	i, s := u.st.Inst, u.be.Status()
	det := fmt.Sprintf("Minecraft %s · %s %s · pacote %s", i.MC, i.Loader, i.LoaderVer, i.PackFormat)
	mods := ""
	if n := len(u.st.Skip); n > 0 {
		mods = fmt.Sprintf("Mods em inglês: %d", n)
	}
	if u.compact() { // janela pequena: duas linhas só
		l1 := " " + u.bold(i.Name) + "  " + u.dim(det)
		l2 := fmt.Sprintf(" %s · %s traduzidos · %s reprovados · %s", u.st.Engine, u.ok(fmt.Sprint(s.OK)), u.warnN(u.pending()), u.gpuText())
		return []string{l1, l2}
	}
	if u.style == 2 {
		w := (u.width() - 3) / 2
		a := u.boxLines("Instância", []string{u.bold(i.Name), u.dim(det)}, w)
		b := u.boxLines("Traduções", []string{u.ok(fmt.Sprint(s.OK)) + " concluídas", fmt.Sprintf("%d reprovados · %d faltando", u.pending(), s.Miss)}, w)
		c := u.boxLines("Motor", []string{u.st.Engine + " · " + u.st.Model, u.gpuText()}, w)
		d := u.boxLines("Última execução", []string{u.lastRun(s), u.dim(mods)}, w)
		out := []string{""}
		out = append(out, beside(a, b, w, 1)...)
		out = append(out, beside(c, d, w, 1)...)
		return append(out, "")
	}
	out := []string{"", u.rule(), " " + u.bold(i.Name) + "  " + u.dim(det)}
	out = append(out, fmt.Sprintf(" Motor %s · %s   [%s]    Trabalho %s", u.st.Engine, u.st.Model, u.gpuText(), u.workDir()))
	out = append(out, fmt.Sprintf(" Textos %s traduzidos · %s reprovados · %d faltando   %s", u.ok(fmt.Sprint(s.OK)), u.warnN(u.pending()), s.Miss, u.dim("última execução "+u.lastRun(s))))
	if mods != "" {
		out = append(out, " "+mods)
	}
	return append(out, u.rule(), "")
}

func (u *UI) warnN(n int) string {
	if n > 0 {
		return u.warn(fmt.Sprint(n))
	}
	return fmt.Sprint(n)
}

type menuEntry struct {
	label string
	hint  func(u *UI) string
	about func(u *UI) []string
	run   func(u *UI)
}

func instanceEntries() []menuEntry {
	return []menuEntry{
		{"Traduzir", nil, func(u *UI) []string {
			return []string{"Teste pequeno ou tradução completa.", "Retoma de onde parou: o que já está", "traduzido não é refeito.", "", u.dim("mtrad run -i ... -c mtrad.json")}
		}, (*UI).translate},
		{"Escolher mods", func(u *UI) string {
			if n := len(u.st.Skip); n > 0 {
				return fmt.Sprintf("%d ficam em inglês", n)
			}
			return "ficam em inglês"
		}, func(u *UI) []string {
			return []string{"Marque os mods que ficam em inglês", "(por exemplo, os repetitivos).", "Mostra quanto tempo isso economiza."}
		}, (*UI).pickMods},
		{"Reprovados", func(u *UI) string { return fmt.Sprintf("%d para revisar", u.pending()) }, func(u *UI) []string {
			return []string{fmt.Sprintf("%d textos não passaram na validação.", u.pending()), "Ver o motivo, editar à mão ou", "deixar em inglês."}
		}, (*UI).failed},
		{"Resultado", nil, func(u *UI) []string {
			return []string{"Relatório, caminho do pacote .zip e", "como ativar no Prism Launcher."}
		}, (*UI).result},
		{"Instalar missões do FTB Quests", nil, func(u *UI) []string {
			return []string{"Mostra o que seria copiado e só copia", "depois de você confirmar.", "Nada é sobrescrito sem pedir."}
		}, (*UI).install},
		{"Registros", nil, func(u *UI) []string {
			return []string{"Resumo das últimas execuções:", "erros, tempos e reprovados."}
		}, (*UI).logs},
		{"Manutenção", nil, func(u *UI) []string {
			return []string{"Remover a trava, verificar o ambiente,", "ver a versão."}
		}, (*UI).maintenance},
	}
}

func (u *UI) suggestion() string {
	s := u.be.Status()
	switch {
	case !s.Known:
		return "Traduzir (ainda não rodou nesta instância)"
	case s.Miss > 0:
		return "Traduzir (faltam " + fmt.Sprint(s.Miss) + ")"
	case u.pending() > 0:
		return fmt.Sprintf("Reprovados (revisar os %d)", u.pending())
	}
	return "Resultado"
}

func (u *UI) instanceMenu() {
	entries := instanceEntries()
	start := 0
	for !u.quit {
		i, ok := u.pick(Pick{Start: start, KeysFn: func() string { return "i estilo: " + styleNames[u.style] },
			OnKey: func(k KeyEvent, _ int) bool {
				if k.K == KRune && (k.R == 'i' || k.R == 'I') {
					u.style = (u.style + 1) % len(styleNames)
					return true
				}
				return false
			},
			Build: func() Layout {
				var its []Item
				for _, e := range entries {
					h := ""
					if e.hint != nil && u.style != 1 {
						h = e.hint(u)
					}
					its = append(its, Item{Label: e.label, Hint: h})
				}
				its = append(its, Item{Label: "Voltar ao início"})
				l := Layout{Head: u.statusHead(), Items: its, Foot: []string{"", "  " + u.dim("sugestão: ") + u.suggestion()}}
				if u.style == 1 {
					l.Side = func(i int) []string {
						if i >= len(entries) {
							return []string{"Volta ao menu inicial."}
						}
						return entries[i].about(u)
					}
				}
				return l
			}})
		if !ok || i == len(entries) {
			return
		}
		start = i
		entries[i].run(u)
	}
}

// ---------- traduzir ----------

func (u *UI) runCmd() string {
	return "mtrad run -i <instância> -c " + u.st.Config + " -w " + u.workDir()
}

func (u *UI) translate() {
	i, ok := u.pick(Pick{Build: func() Layout {
		head := []string{"", " " + u.bold("Traduzir"), ""}
		foot := []string{"", u.cmd(u.runCmd())}
		if n := len(u.st.Skip); n > 0 {
			foot = append(foot, fmt.Sprintf("  %s %d mod(s) ficam em inglês", u.dim("filtro:"), n))
		}
		return Layout{Head: head, Items: []Item{{Label: "Teste pequeno", Hint: "50 textos"}, {Label: "Completo", Hint: "retoma de onde parou; o que já está traduzido não é refeito"}, {Label: "Voltar"}}, Foot: foot}
	}})
	if !ok || i == 2 {
		return
	}
	limit := 0
	if i == 0 {
		limit = 50
	}
	est := u.be.Estimate(limit)
	var head string
	switch {
	case est.Texts > 0 && est.PerMin > 0:
		head = fmt.Sprintf("%d textos a traduzir · cerca de %d min (%d por minuto, medido na última execução)", est.Texts, maxInt(1, est.Texts/est.PerMin), est.PerMin)
	case est.Texts > 0:
		head = fmt.Sprintf("%d textos a traduzir · sem estimativa de tempo ainda (nenhuma execução anterior)", est.Texts)
	case limit > 0:
		head = fmt.Sprintf("Teste com até %d textos. Os mods são lidos primeiro; isso leva um tempo.", limit)
	default:
		head = "Os mods são lidos primeiro; o número de textos aparece quando a leitura terminar."
	}
	j, ok := u.pick(Pick{Build: func() Layout {
		its := []Item{{Label: "Começar agora"}, {Label: "Começar e copiar os arquivos do FTB Quests no fim", Hint: "mtrad run --install"}, {Label: "Voltar"}}
		return Layout{Head: []string{"", "  " + head, ""}, Items: its}
	}})
	if !ok || j == 2 {
		return
	}
	u.runTranslate(limit, j == 1)
}

// runTranslate roda a tradução. Se o usuário pausar com Esc, oferece logo
// "Continuar de onde parou" (a mesma execução, sem passar de novo pelos menus de
// Traduzir); o que já foi traduzido está guardado, então continuar só pede o que falta.
func (u *UI) runTranslate(limit int, install bool) {
	for !u.quit {
		if !u.translateOnce(limit, install) {
			return
		}
	}
}

// translateOnce faz uma rodada e diz se o usuário escolheu continuar depois de pausar.
func (u *UI) translateOnce(limit int, install bool) (resume bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var done, total int64
	type result struct {
		out Outcome
		err error
	}
	finished := make(chan result, 1)
	start := time.Now()
	go func() {
		o, err := u.be.Translate(ctx, limit, install, func(d, t int) { atomic.StoreInt64(&done, int64(d)); atomic.StoreInt64(&total, int64(t)) })
		finished <- result{o, err}
	}()
	paused := false
	var res result
loop:
	for {
		d, t := atomic.LoadInt64(&done), atomic.LoadInt64(&total)
		u.draw(u.progressFrame(d, t, time.Since(start), u.be.Rate(), paused, false))
		select {
		case res = <-finished:
			break loop
		default:
		}
		if ev, ok := u.in.Poll(100 * time.Millisecond); ok && !paused {
			if ev.K == KEsc || ev.K == KCtrlC || ev.K == KEOF {
				paused = true
				cancel()
			}
		}
	}
	d, t := atomic.LoadInt64(&done), atomic.LoadInt64(&total)
	lines := u.progressFrame(d, t, time.Since(start), u.be.Rate(), false, true)
	switch {
	case paused:
		lines = append(lines, "", "  "+u.warn("!")+" pausado. O que já foi traduzido está guardado.")
		for _, l := range res.out.Lines {
			lines = append(lines, "  "+u.dim(l))
		}
		i, ok := u.pick(Pick{Build: func() Layout {
			return Layout{Head: lines, Items: []Item{{Label: "Continuar de onde parou", Hint: "só pede o que falta"}, {Label: "Voltar ao menu"}}}
		}})
		return ok && i == 0
	case res.err != nil:
		lines = append(lines, "", "  "+u.bad("✗")+" parou: "+res.err.Error(), "  "+u.dim("O que já foi traduzido está guardado; confira o ambiente em Manutenção e tente de novo."))
	case res.out.Partial:
		lines = append(lines, "", u.attention(res.out))
		for _, p := range res.out.Problems {
			lines = append(lines, "    "+u.dim(p))
		}
	default:
		lines = append(lines, "", "  "+u.ok("✓")+" etapa concluída.")
	}
	for _, l := range res.out.Lines {
		lines = append(lines, "  "+u.dim(l))
	}
	u.draw(append(lines, "", u.dim("  (Enter volta)")))
	if ev := u.in.Next(); ev.K == KCtrlC || ev.K == KEOF {
		u.quit = true
	}
	return false
}

// attention é a linha de aviso do fim de uma execução que terminou com itens
// que pedem atenção: diz quantos textos reprovaram (se houver) em vez de mandar
// o usuário olhar uma lista que pode estar vazia.
func (u *UI) attention(o Outcome) string {
	n := len(u.be.Failed())
	switch {
	case n > 0 && len(o.Problems) > 0:
		return fmt.Sprintf("  %s concluída com %d texto(s) reprovado(s) (veja Reprovados) e avisos do motor ou da leitura:", u.warn("!"), n)
	case n > 0:
		return fmt.Sprintf("  %s concluída com %d texto(s) reprovado(s): veja Reprovados.", u.warn("!"), n)
	case len(o.Problems) > 0:
		return "  " + u.warn("!") + " concluída com avisos (nada para revisar em Reprovados). O que a etapa registrou:"
	}
	return "  " + u.warn("!") + " concluída com itens que precisam de atenção (Registros mostra os detalhes)."
}

// runBusy roda uma tarefa sem barra de progresso (ler os mods, aplicar as
// correções) mostrando o tempo; Esc cancela. Devolve o erro da tarefa.
func (u *UI) runBusy(title string, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	start := time.Now()
	go func() { finished <- fn(ctx) }()
	canceled := false
	for {
		el := time.Since(start).Round(time.Second)
		lines := []string{"", " " + u.bold(title), "", fmt.Sprintf("  %s decorridos", el)}
		if canceled {
			lines = append(lines, "", "  "+u.dim("cancelando: esperando a etapa atual terminar…"))
		} else {
			lines = append(lines, "", "  "+u.dim("Esc cancela."))
		}
		u.draw(lines)
		select {
		case err := <-finished:
			if canceled && err == nil {
				err = context.Canceled
			}
			return err
		default:
		}
		if ev, ok := u.in.Poll(100 * time.Millisecond); ok && !canceled {
			if ev.K == KEsc || ev.K == KCtrlC || ev.K == KEOF {
				canceled = true
				cancel()
			}
		}
	}
}

func (u *UI) progressFrame(done, total int64, el time.Duration, rate Rate, paused, final bool) []string {
	const w = 30
	var pct int64
	if total > 0 {
		pct = done * 100 / total
	}
	f := int(pct) * w / 100
	lines := []string{"", " " + u.bold("Traduzindo…"), "", fmt.Sprintf("  [%s%s] %d/%d  %d%%", strings.Repeat("█", f), strings.Repeat("░", w-f), done, total, pct)}
	// o tempo que passou e, no ritmo atual, o que falta
	timing := "  " + fmtx.Duration(el) + " decorridos"
	if final {
		timing = "  levou " + fmtx.Duration(el)
	} else if rate.RemainingKnown && rate.Remaining > 0 {
		timing += " · faltam ~" + fmtx.Duration(rounded(rate.Remaining))
	}
	lines = append(lines, u.dim(timing))
	var parts []string
	if rate.WordsPerSec > 0 {
		parts = append(parts, fmtx.Decimal1(rate.WordsPerSec)+" palavras/s")
	}
	if el > 2*time.Second && done > 0 {
		parts = append(parts, fmt.Sprintf("%.0f textos/min", float64(done)/el.Minutes()))
	}
	if len(parts) > 0 {
		lines = append(lines, "  "+u.dim(strings.Join(parts, " · ")+" · "+u.gpuTextPlain()))
	}
	if final {
		return lines
	}
	if paused {
		lines = append(lines, "", "  "+u.dim("pausando: esperando o lote atual terminar…"))
	} else {
		lines = append(lines, "", "  "+u.dim("Esc pausa; rodar de novo retoma."))
	}
	return lines
}

// rounded arredonda uma previsão: acima de 10 min para o minuto, para a
// previsão não ficar tremendo a cada quadro.
func rounded(d time.Duration) time.Duration {
	if d >= 10*time.Minute {
		return d.Round(time.Minute)
	}
	return d
}

func (u *UI) gpuTextPlain() string {
	s := u.be.Status()
	switch {
	case u.st.Engine != "ollama":
		return "motor externo"
	case !s.OllamaUp:
		return "Ollama parado"
	case s.Idle:
		return "Ollama no ar"
	case s.OnGPU:
		return "Ollama na GPU"
	}
	return "Ollama na CPU"
}

// ---------- mods ----------

// readMods oferece ler os mods (scan, extract, diff) quando ainda não há lista.
func (u *UI) readMods() bool {
	if !u.confirm([]string{" " + u.bold("Ler os mods agora?"), "", "  Ainda não li os mods desta instância. Vou rodar scan, extract e diff,", "  sem traduzir nada. Num modpack grande leva alguns minutos.", u.cmd("mtrad scan && mtrad extract && mtrad diff")}, "Sim, ler os mods", true) {
		return false
	}
	err := u.runBusy("Lendo os mods…", func(ctx context.Context) error { return u.be.Prepare(ctx) })
	if u.quit {
		return false
	}
	if err != nil {
		u.notice("  "+u.bad("✗")+" não consegui ler os mods:", "  "+err.Error())
		return false
	}
	return true
}

func (u *UI) pickMods() {
	mods := u.be.Mods()
	if len(mods) == 0 {
		if !u.readMods() {
			return
		}
		mods = u.be.Mods()
		if len(mods) == 0 {
			u.notice("  Nenhum mod tem textos a traduzir nesta instância.")
			return
		}
	}
	sort.SliceStable(mods, func(i, j int) bool { return mods[i].Texts > mods[j].Texts })
	changed := false
	save := func() {
		if !changed {
			return
		}
		var ids []string
		for id := range u.st.Skip {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if err := u.be.SaveSkip(ids); err != nil {
			u.notice("  "+u.bad("✗")+" não consegui gravar a lista de mods em "+u.st.Config+":", "  "+err.Error())
		}
		changed = false
	}
	toggle := func(i int) {
		if i < len(mods) {
			id := mods[i].ID
			if u.st.Skip[id] {
				delete(u.st.Skip, id)
			} else {
				u.st.Skip[id] = true
			}
			changed = true
		}
	}
	perMin := u.be.Estimate(0).PerMin
	start := 0
	for !u.quit {
		i, ok := u.pick(Pick{Start: start, OnSpace: toggle, Keys: "r repetitivos · l limpar", Filterable: true, Tail: 1,
			OnKey: func(k KeyEvent, _ int) bool {
				if k.K != KRune {
					return false
				}
				switch k.R {
				case 'r', 'R':
					for _, m := range mods {
						if m.Repetitive {
							u.st.Skip[m.ID] = true
						}
					}
					changed = true
					return true
				case 'l', 'L':
					u.st.Skip = map[string]bool{}
					changed = true
					return true
				}
				return false
			},
			Build: func() Layout {
				skipped := 0
				var its []Item
				var ids []string
				for _, m := range mods {
					mk := "[ ]"
					if u.st.Skip[m.ID] {
						mk = u.warn("[x]")
						skipped += m.Texts
						ids = append(ids, `"`+m.ID+`"`)
					}
					h := fmt.Sprintf("%d textos", m.Texts)
					if perMin > 0 {
						// quanto custa traduzir este mod, no ritmo da última execução: é o que
						// ajuda a decidir sobre os repetitivos (milhares de nomes curtos)
						h += " · " + minutesText(m.Texts, perMin)
					}
					if m.Repetitive {
						h += " · repetitivo"
					}
					its = append(its, Item{Mark: mk, Label: m.ID, Hint: h})
				}
				its = append(its, Item{Label: "Concluir"})
				head := []string{"", " " + u.bold("Quais mods ficam em inglês?") + "  " + u.dim("[x] = fica em inglês"), ""}
				sum := fmt.Sprintf("  %s %d textos ficam em inglês", u.dim("resumo:"), skipped)
				if perMin > 0 {
					sum += fmt.Sprintf(", cerca de %d min a menos", skipped/perMin)
				}
				foot := []string{"", sum, u.cmd(`"skip_mods": [` + strings.Join(ids, ", ") + `]`)}
				return Layout{Head: head, Items: its, Foot: foot}
			}})
		if !ok || i == len(mods) {
			save()
			return
		}
		toggle(i)
		start = i
	}
	save()
}

// minutesText diz em minutos (ou horas) quanto leva traduzir n textos no ritmo
// de perMin textos por minuto.
func minutesText(n, perMin int) string {
	if perMin <= 0 {
		return ""
	}
	min := (n + perMin - 1) / perMin
	if min < 1 {
		min = 1
	}
	if min >= 90 {
		return fmt.Sprintf("~%d h %02d min", min/60, min%60)
	}
	return fmt.Sprintf("~%d min", min)
}

// ---------- reprovados ----------

func (u *UI) failed() {
	start := 0
	for !u.quit {
		list := u.be.Failed()
		i, ok := u.pick(Pick{Start: start, Filterable: true, Tail: 3, Build: func() Layout {
			list = u.be.Failed()
			head := []string{"", " " + u.bold("Reprovados") + "  " + u.dim(fmt.Sprintf("(%d na lista)", len(list))), ""}
			if len(list) == 0 {
				head = append(head, "  "+u.ok("✓")+" nenhum texto reprovado.", "")
			}
			var its []Item
			for _, f := range list {
				// a etiqueta vem antes do motivo: o motivo é longo e é ele que a
				// largura da janela corta
				h := f.Reason
				switch {
				case f.Edit != "":
					h = u.ok("[corrigido]") + " " + h
				case f.Left:
					h = u.warn("[fica em inglês]") + " " + h
				}
				its = append(its, Item{Label: f.Title(), Hint: h})
			}
			its = append(its, Item{Label: "Aplicar as correções", Hint: "valida e monta o pacote de novo, sem traduzir"}, Item{Label: "Tentar todos de novo", Hint: "os reprovados são refeitos sozinhos, com temperatura 0,5"}, Item{Label: "Voltar"})
			return Layout{Head: head, Items: its}
		}})
		if !ok || i == len(list)+2 {
			return
		}
		start = i
		switch i - len(list) {
		case 0:
			u.applyFixes()
			continue
		case 1:
			if u.confirm([]string{" " + u.bold("Tentar os reprovados de novo?"), "", "  Roda a tradução inteira: só os reprovados e o que falta são pedidos de novo.", u.cmd(u.runCmd())}, "Sim, tentar", false) {
				u.runTranslate(0, false)
			}
			continue
		}
		u.failedDetail(list[i])
	}
}

// applyFixes valida e monta o pacote de novo, para as correções à mão valerem.
func (u *UI) applyFixes() {
	var out Outcome
	err := u.runBusy("Validando e montando o pacote…", func(ctx context.Context) error {
		var e error
		out, e = u.be.Apply(ctx)
		return e
	})
	if u.quit {
		return
	}
	if err != nil {
		u.notice("  "+u.bad("✗")+" não consegui aplicar as correções:", "  "+err.Error())
		return
	}
	lines := []string{"  " + u.ok("✓") + " correções aplicadas."}
	if out.Partial {
		lines = []string{u.attention(out)}
		for _, p := range out.Problems {
			lines = append(lines, "    "+u.dim(p))
		}
	}
	for _, l := range out.Lines {
		lines = append(lines, "  "+u.dim(l))
	}
	u.notice(append(lines, u.cmd("mtrad validate && mtrad pack"))...)
}

var reTokens = regexp.MustCompile(`[&§][0-9a-fk-or]|%(?:\d+\$)?[sdf]|⟦\d+⟧`)

// missingTokens devolve as marcas do original que faltam no texto.
func missingTokens(orig, text string) []string {
	have := map[string]int{}
	for _, t := range reTokens.FindAllString(text, -1) {
		have[t]++
	}
	var miss []string
	for _, t := range reTokens.FindAllString(orig, -1) {
		if have[t] > 0 {
			have[t]--
		} else {
			miss = append(miss, t)
		}
	}
	return miss
}

func (u *UI) failedDetail(f Failed) {
	for !u.quit {
		i, ok := u.pick(Pick{Build: func() Layout {
			out := f.Out
			if f.Edit != "" {
				out = f.Edit
			}
			head := []string{"", " " + u.bold(f.Title()) + "  " + u.dim("("+f.Source+")"), "  motivo:    " + u.warn(f.Reason), "  original:  " + f.Orig, "  tradução:  " + out}
			if f.Edit != "" {
				head = append(head, "  "+u.ok("corrigido à mão"))
			}
			if f.Left {
				head = append(head, "  "+u.warn("vai ficar em inglês"))
			}
			head = append(head, "")
			return Layout{Head: head, Items: []Item{{Label: "Editar à mão"}, {Label: "Deixar em inglês"}, {Label: "Voltar"}}}
		}})
		if !ok || i == 2 {
			return
		}
		if i == 1 {
			if err := u.be.Leave(f.ID); err != nil {
				u.notice("  "+u.bad("✗")+" não consegui guardar:", "  "+err.Error())
				continue
			}
			return
		}
		cur := f.Out
		if f.Edit != "" {
			cur = f.Edit
		}
		s, ok := u.input([]string{"", " " + u.bold("Editar a tradução") + "  " + u.dim(f.Title()), "  original: " + f.Orig, "  " + u.dim("Mantenha as marcas (&6, %s, ⟦1⟧) como no original.")}, cur, func(s string) (string, bool) {
			if m := missingTokens(f.Orig, s); len(m) > 0 {
				return "faltam as marcas do original: " + strings.Join(m, " "), true
			}
			return "", false
		})
		if ok {
			if err := u.be.SaveEdit(f.ID, s); err != nil {
				u.notice("  "+u.bad("✗")+" não consegui guardar a correção:", "  "+err.Error())
				continue
			}
			return
		}
	}
}

// ---------- resultado, instalar, registros, manutenção ----------

func (u *UI) result() {
	for !u.quit {
		s := u.be.Status()
		pack := s.Pack
		if pack == "" {
			pack = u.workDir() + "/out/mtrad-pt_br.zip"
		}
		step1 := "  1. Copie o zip para a pasta resourcepacks da instância."
		if strings.Contains(pack, "resourcepacks") {
			step1 = "  1. O pacote já está na pasta resourcepacks da instância."
		}
		i, ok := u.pick(Pick{Build: func() Layout {
			head := []string{"", " " + u.bold("Resultado"),
				"  Pacote:  " + u.acc(pack),
				fmt.Sprintf("  Textos:  %d traduzidos, %d reprovados (ficam em inglês)", s.OK, u.pending()),
				"", " " + u.bold("Como ativar no Prism"), step1,
				"  2. No jogo: Opções > Pacotes de recursos, mova o pacote para a direita.",
				"  3. Em Opções > Idioma, escolha Português (Brasil).",
				"  " + u.dim("Textos fixos no código dos mods ficam em inglês: pacote de recursos não alcança."),
				"  " + u.dim("Livro do Patchouli com \"Erro de carregamento\"? Manutenção > Conferir o pacote montado diz se foi o pacote."),
				"  " + u.dim("Ou deixe o mtrad ligar no options.txt (com o jogo fechado):"),
				u.cmd("mtrad activate -w " + u.workDir() + " -c " + u.st.Config), ""}
			return Layout{Head: head, Items: []Item{{Label: "Ligar o pacote e o idioma no jogo", Hint: "mexe no options.txt; guarda uma cópia"}, {Label: "Voltar"}}}
		}})
		if !ok || i == 1 {
			return
		}
		u.activate()
	}
}

// activate mostra o que mudaria no options.txt e só liga depois de confirmar.
func (u *UI) activate() {
	plan, err := u.be.Activate(true)
	if err != nil {
		u.notice("  "+u.bad("✗")+" não deu para preparar:", "  "+err.Error())
		return
	}
	head := []string{" " + u.bold("Ligar o pacote e o idioma no jogo?"), ""}
	for _, l := range plan {
		head = append(head, "  "+l)
	}
	head = append(head, "", "  "+u.dim("O jogo precisa estar fechado: ele regrava o options.txt ao sair."), "  "+u.dim("O options.txt atual é guardado em options.txt.mtrad.bak."))
	if !u.confirm(head, "Sim, ligar", false) {
		return
	}
	done, err := u.be.Activate(false)
	if err != nil {
		u.notice("  "+u.bad("✗")+" não consegui ligar:", "  "+err.Error())
		return
	}
	lines := []string{"  " + u.ok("✓") + " pronto."}
	for _, l := range done {
		lines = append(lines, "  "+l)
	}
	u.notice(lines...)
}

func (u *UI) install() {
	files, err := u.be.Install(true)
	if err != nil {
		u.notice("  "+u.bad("✗")+" não deu para ver o que seria copiado:", "  "+err.Error())
		return
	}
	if len(files) == 0 {
		u.notice(" "+u.bold("Instalar arquivos do FTB Quests"), "", "  Nada para copiar: o pacote não gerou arquivos do FTB Quests.", u.cmd("mtrad install -w "+u.workDir()+" --dry-run"))
		return
	}
	i, ok := u.pick(Pick{Build: func() Layout {
		head := []string{"", " " + u.bold("Instalar arquivos do FTB Quests"), "  " + u.dim("O que seria copiado:")}
		for _, f := range files {
			head = append(head, "  • "+f)
		}
		head = append(head, u.cmd("mtrad install -w "+u.workDir()+" --dry-run"), "")
		return Layout{Head: head, Items: []Item{{Label: "Copiar para a pasta do jogo", Hint: "pede confirmação; nada é sobrescrito"}, {Label: "Voltar"}}}
	}})
	if !ok || i == 1 {
		return
	}
	if u.confirm([]string{" " + u.bold("Copiar os arquivos para a pasta do jogo?"), "  Arquivos que já existem e são diferentes não serão substituídos."}, "Sim, copiar", false) {
		done, err := u.be.Install(false)
		if err != nil {
			u.notice("  "+u.bad("✗")+" a cópia terminou com problema:", "  "+err.Error())
			return
		}
		lines := []string{"  " + u.ok("✓") + " copiado."}
		for _, l := range done {
			lines = append(lines, "  • "+l)
		}
		u.notice(lines...)
	}
}

func (u *UI) logs() {
	n := 0
	for !u.quit {
		lines := u.be.Logs(n)
		_, ok := u.pick(Pick{Keys: "←→ outra execução", OnKey: func(k KeyEvent, _ int) bool {
			switch k.K {
			case KLeft:
				n = minInt(n+1, 9)
				return true
			case KRight:
				n = maxInt(0, n-1)
				return true
			}
			return false
		}, Build: func() Layout {
			lines = u.be.Logs(n)
			head := []string{"", " " + u.bold("Registros das execuções")}
			for _, l := range lines {
				head = append(head, "  "+l)
			}
			head = append(head, u.cmd(fmt.Sprintf("mtrad logs -w %s -n %d", u.workDir(), n)), "")
			return Layout{Head: head, Items: []Item{{Label: "Voltar"}}}
		}})
		if !ok {
			return
		}
		return
	}
}

// verifyPack confere o pacote montado e explica o que o resultado quer dizer.
func (u *UI) verifyPack() {
	var out Outcome
	err := u.runBusy("Conferindo o pacote…", func(ctx context.Context) error {
		var e error
		out, e = u.be.Verify()
		return e
	})
	if u.quit {
		return
	}
	if err != nil {
		u.notice("  "+u.bad("✗")+" não consegui conferir o pacote:", "  "+err.Error(), "  Monte o pacote antes (Traduzir) e tente de novo.")
		return
	}
	l := []string{u.bold(" Conferir o pacote montado"), ""}
	for _, x := range out.Lines {
		l = append(l, "  "+x)
	}
	if out.Partial {
		l = append(l, "", "  "+u.warn("!")+" o pacote tem diferenças que o mtrad não devia ter feito; monte de novo (Reprovados > Aplicar as correções) e, se continuar, guarde este relatório.")
	} else {
		l = append(l, "", "  "+u.ok("✓")+" se um livro do Patchouli mostra \"Erro de carregamento\", o problema não está nos documentos do pacote.",
			"  "+u.dim("Para saber se é o idioma: no jogo, troque para English (US) e abra o livro de novo; se o erro continuar, não é a tradução."),
			"  "+u.dim("O motivo verdadeiro está em logs/latest.log da instância (procure o nome do livro)."))
	}
	u.notice(append(l, u.cmd("mtrad verify -w "+u.workDir()))...)
}

func (u *UI) maintenance() {
	start := 0
	for !u.quit {
		i, ok := u.pick(Pick{Start: start, Build: func() Layout {
			return Layout{Head: []string{"", " " + u.bold("Manutenção"), ""}, Items: []Item{
				{Label: "Remover a trava de uma execução que caiu", Hint: "pede confirmação"},
				{Label: "Verificar o ambiente", Hint: "o mesmo do passo 1 do setup"},
				{Label: "Conferir o pacote montado", Hint: "o pacote só mudou o que foi traduzido?"},
				{Label: "Ver a versão"},
				{Label: "Criar conjunto de teste (evalset)"},
				{Label: "Voltar"}}}
		}})
		if !ok || i == 5 {
			return
		}
		start = i
		switch i {
		case 0:
			if u.confirm([]string{" " + u.bold("Remover a trava?"), "  Só faça isso se nenhuma execução estiver rodando."}, "Sim, remover", false) {
				u.notice("  "+u.be.Unlock(), u.cmd("mtrad unlock -w "+u.workDir()))
			}
		case 1:
			var l []string
			l = append(l, u.bold(" Verificar o ambiente"), "")
			for _, c := range u.be.Doctor() {
				l = append(l, fmt.Sprintf("  %s %s  %s", u.checkMark(c), pad(c.Name, 40), u.dim(c.Detail)))
			}
			u.notice(append(l, u.cmd("mtrad doctor -c "+u.st.Config))...)
		case 2:
			u.verifyPack()
		case 3:
			u.notice("  mtrad "+u.ver, u.cmd("mtrad --version"))
		case 4:
			u.notice("  Cria um conjunto de 500 textos com traduções humanas, para medir a qualidade.", u.cmd("mtrad evalset -o evalwork -n 500"))
		}
	}
}
