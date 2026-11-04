package ui

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Demo é um Backend falso, com dados do caso do Reclamation, para refinar a
// interface e para os testes, sem tocar em nada de verdade. Use &Demo{...}.
type Demo struct {
	Done   bool          // a configuração inicial já foi feita
	Broken bool          // o Ollama não está instalado (para ver a tela de aviso)
	Delay  time.Duration // pausa por passo da barra de progresso (0 nos testes)

	mu    sync.Mutex
	skip  map[string]bool
	edits map[string]string
	left  map[string]bool
	saved *Setup
	rate  Rate // andamento simulado da tradução
}

func (d *Demo) IsDemo() bool { return true }

func (d *Demo) Defaults() Setup {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.saved != nil {
		return *d.saved
	}
	return Setup{Engine: "ollama", Model: "translategemma:4b", Sources: []string{"lang", "patchouli", "ftbq"}, Work: "work-real", Config: "mtrad.json"}
}

func (d *Demo) SetupDone() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Done || d.saved != nil
}

func (d *Demo) SaveSetup(s Setup) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.saved = &s
	return nil
}

func (*Demo) Instances() []Instance {
	return []Instance{
		{"Reclamation - Reclaim the World-", "~/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/instances/Reclamation - Reclaim the World-", "1.20.1", "forge", "47.3.0", "15"},
		{"Fabric Vanilla+", "~/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/instances/Fabric Vanilla+", "1.21.1", "fabric", "0.16.9", "34"},
	}
}

func (*Demo) SelectInstance(in Instance) (Instance, error) { return in, nil }

func (d *Demo) Status() Status {
	return Status{OllamaUp: true, Model: "translategemma:4b", OnGPU: true, OK: 19228 + d.resolved(), Fail: 196 - d.resolved(), Miss: 0, LastRun: "28/10 02:53", Known: true}
}

func (d *Demo) resolved() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, f := range demoFailed() {
		if d.edits[f.ID] != "" || d.left[f.ID] {
			n++
		}
	}
	return n
}

func (*Demo) Models() []string {
	return []string{"translategemma:4b", "gemma3:12b", "qwen3:8b"}
}

// Hardware na demonstração é o computador do autor: RX 580 de 8 GiB.
func (*Demo) Hardware() Hardware {
	return Hardware{
		Lines: []string{
			"memória do sistema: 15,6 GiB (livre 8,6 GiB)",
			"processador: 8 núcleos",
			"placa de vídeo: Radeon RX 580, 8,0 GiB, em uso 0,5 GiB, sobram 6,3 GiB para o modelo",
		},
		Options: []ModelOption{
			{Name: "translategemma:4b", SizeGiB: 3.3, Fit: "cabe na placa de vídeo", Runs: true, Installed: true, Recommended: true},
			{Name: "translategemma:12b", SizeGiB: 8.1, Fit: "parte na CPU (mais lento)", Runs: true},
			{Name: "translategemma:27b", SizeGiB: 17, Fit: "não cabe"},
			{Name: "gemma3:4b", SizeGiB: 3.4, Fit: "cabe na placa de vídeo", Runs: true},
			{Name: "gemma3:12b", SizeGiB: 8.2, Fit: "parte na CPU (mais lento)", Runs: true, Installed: true},
		},
		Why:     "translategemma:4b é o maior modelo desta lista que cabe inteiro na sua placa de vídeo",
		Checked: "2026-11-03",
	}
}

func (*Demo) Prepare(context.Context) error { return nil }

func (*Demo) Mods() []Mod {
	return []Mod{
		{"kubejs", 3120, false}, {"ftbquests", 2315, false}, {"theurgy", 1480, false},
		{"quark", 1890, false}, {"create", 2210, false}, {"chipped", 7258, true},
		{"rechiseled", 3656, true}, {"fancymenu", 3364, true}, {"mcwfurnitures", 2100, true},
		{"mcwroofs", 1590, true}, {"patchouli", 2298, false},
	}
}

func (d *Demo) Skipped() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for id := range d.skip {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (d *Demo) SaveSkip(ids []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.skip = map[string]bool{}
	for _, id := range ids {
		d.skip[id] = true
	}
	return nil
}

func (*Demo) Estimate(limit int) Estimate {
	if limit > 0 {
		return Estimate{Texts: limit, PerMin: 100}
	}
	return Estimate{Texts: 17031, PerMin: 100}
}

func (d *Demo) Doctor() []Check {
	if d.Broken {
		return []Check{
			{Name: "Ollama responde em 127.0.0.1:11434", OK: false, Detail: "não respondeu"},
			{Name: "Modelo translategemma:4b instalado", OK: false, Detail: "Ollama parado"},
			{Name: "Pasta de trabalho gravável", OK: true, Detail: "work-real"},
			{Name: "Espaço livre em disco", OK: true, Detail: "41 GB"},
			{Name: "ollama-vulkan instalado", OK: false, Detail: "não encontrado"},
		}
	}
	return []Check{
		{Name: "Ollama responde em 127.0.0.1:11434", OK: true, Detail: "versão 0.12"},
		{Name: "Modelo translategemma:4b instalado", OK: true, Detail: "3,3 GB"},
		{Name: "Modelo está na GPU", OK: true, Detail: "100% GPU, 4,1 de 8 GB de VRAM"},
		{Name: "Pasta de trabalho gravável", OK: true, Detail: "work-real"},
		{Name: "Espaço livre em disco", OK: true, Detail: "41 GB"},
		{Name: "Instância do Prism encontrada", OK: true, Detail: "Reclamation - Reclaim the World-"},
		{Name: "ollama-vulkan instalado", OK: true, Detail: "pacote do Arch"},
	}
}

func demoFailed() []Failed {
	return []Failed{
		{ID: "quest.4F2A.description[2]", Source: "ftbquests", Reason: "perdeu a marca de cor ⟦3⟧", Orig: "Collect &6Dried Earth&r from the desert.", Out: "Colete Terra Seca do deserto."},
		{ID: "theurgy:item.sal_ammoniac", Source: "lang", Reason: "texto igual ao inglês", Orig: "Sal Ammoniac", Out: "Sal Ammoniac"},
		{ID: "kubejs:tooltip.shift", Source: "kubejs", Reason: "perdeu %s", Orig: "Hold [Shift] to show %s", Out: "Segure [Shift] para mostrar"},
		{ID: "quark:config.tab.tweaks", Source: "lang", Reason: "resposta com explicação extra", Orig: "Tweaks", Out: "Ajustes (nota: \"Tweaks\" significa ajustes)"},
		{ID: "create:tooltip.stress", Source: "lang", Reason: "perdeu a marca ⟦1⟧", Orig: "Stress impact: %s", Out: "Impacto de tensão"},
		{ID: "ftbquests:chapter.7.title", Source: "ftbquests", Reason: "perdeu a marca de cor ⟦1⟧", Orig: "&bThe Beginning", Out: "O Começo"},
	}
}

func (d *Demo) Failed() []Failed {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := demoFailed()
	for i := range l {
		l[i].Edit = d.edits[l[i].ID]
		l[i].Left = d.left[l[i].ID]
	}
	return l
}

func (d *Demo) SaveEdit(id, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.edits == nil {
		d.edits = map[string]string{}
	}
	if d.left == nil {
		d.left = map[string]bool{}
	}
	d.edits[id] = text
	delete(d.left, id)
	return nil
}

func (d *Demo) Leave(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.edits == nil {
		d.edits = map[string]string{}
	}
	if d.left == nil {
		d.left = map[string]bool{}
	}
	d.left[id] = true
	delete(d.edits, id)
	return nil
}

func (d *Demo) Translate(ctx context.Context, limit int, install bool, progress func(done, total int)) (Outcome, error) {
	total := 17031
	if limit > 0 {
		total = limit
	}
	steps := 40
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		case <-time.After(d.Delay):
		}
		d.setRate(total*i/steps, total)
		progress(total*i/steps, total)
	}
	lines := []string{fmt.Sprintf("translate: %d traduzidos, 0 do cache, 0 falharam", total), "validate: simulado", "pack: simulado"}
	if install {
		lines = append(lines, "install: simulado")
	}
	return Outcome{Lines: lines}, nil
}

// Na demonstração cada texto tem 7 palavras e o motor faz 7,9 palavras por segundo.
func (d *Demo) setRate(done, total int) {
	const wordsPerText, wps = 7, 7.9
	r := Rate{TotalWords: int64(total * wordsPerText), DoneWords: int64(done * wordsPerText), WordsPerSec: wps}
	if left := r.TotalWords - r.DoneWords; left > 0 {
		r.Remaining = time.Duration(float64(left) / wps * float64(time.Second))
	}
	r.RemainingKnown = true
	d.mu.Lock()
	d.rate = r
	d.mu.Unlock()
}

func (d *Demo) Rate() Rate {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rate
}

func (*Demo) Apply(context.Context) (Outcome, error) {
	return Outcome{Lines: []string{"validate: simulado", "pack: simulado"}}, nil
}

func (*Demo) Install(dry bool) ([]string, error) {
	return []string{
		"config/ftbquests/quests/chapters/inicio.snbt (novo)",
		"config/ftbquests/quests/chapters/deserto.snbt (novo)",
		"config/ftbquests/quests/lang/pt_br.snbt (novo)",
	}, nil
}

func (*Demo) Verify() (Outcome, error) {
	return Outcome{Lines: []string{
		"verify: ~/.var/app/org.prismlauncher.PrismLauncher/.../resourcepacks/mtrad-pt_br.zip",
		"verify: 96 documentos do Patchouli iguais aos originais fora dos textos traduzidos (0 não estão no pacote e ficam em inglês), 12 arquivos de idioma com 17031 chaves",
		"verify: nenhum problema: o pacote só mudou os textos que o mtrad traduziu"}}, nil
}

func (*Demo) Activate(dry bool) ([]string, error) {
	return []string{"options.txt: ligaria o pacote file/mtrad-pt_br.zip e o idioma pt_br (simulado)"}, nil
}

func (*Demo) Logs(n int) []string {
	return []string{
		fmt.Sprintf("execução %d: run-20261028-025317.log", n),
		"  início 02:53, duração 41m12s, resultado ok",
		"  pedidos: 17031 (cache 0, refeitos 238, erros 3)",
		"  tempo por texto: média 312 ms, mediana 270 ms, p95 640 ms",
		"  erro mais comum: 3 × \"context deadline exceeded\"",
	}
}

func (*Demo) Unlock() string { return "nenhuma trava encontrada em work-real" }
