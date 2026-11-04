// Package hardware descobre a placa de vídeo, a memória e os núcleos do
// computador, para o mtrad sugerir um modelo que caiba. Só biblioteca padrão:
// no Linux lê /proc e /sys, e chama `nvidia-smi`, `sysctl` e `llama-server
// --list-devices` quando existem.
// Tudo o que toca o sistema passa por Deps, para os testes. Não altera nada.
package hardware

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const GiB = 1 << 30

// FormatGiB escreve um tamanho em GiB com uma casa e vírgula decimal ("8,1").
func FormatGiB(g float64) string { return strings.Replace(fmt.Sprintf("%.1f", g), ".", ",", 1) }

// Buffer é a folga que sempre fica de fora da memória de vídeo: a tela do
// sistema e o contexto do modelo (1,2 GiB).
const Buffer uint64 = 12 * GiB / 10

// GPU é uma placa de vídeo (ou a memória unificada de um Mac com Apple Silicon).
type GPU struct {
	Vendor string // nvidia, amd, intel, apple, manual (informada por você) ou unknown
	Name   string
	Total  uint64 // bytes de memória de vídeo
	Used   uint64 // bytes em uso agora (0 se UsedKnown for falso)
	// UsedKnown é falso quando o sistema não diz quanto já está em uso.
	UsedKnown bool
	// Unified: a memória é a mesma do sistema (Apple Silicon).
	Unified bool
}

// Usable é a memória de vídeo que sobra para um modelo: total menos o que já
// está em uso menos a folga (Buffer). Em memória unificada só se conta uma
// parte do total (2/3), uma estimativa prudente: o sistema também a usa.
func (g GPU) Usable() uint64 {
	total := g.Total
	if g.Unified {
		total = total / 3 * 2
	}
	reserved := Buffer
	if g.UsedKnown {
		reserved += g.Used
	}
	if total <= reserved {
		return 0
	}
	return total - reserved
}

// Info é o que foi encontrado no computador.
type Info struct {
	OS, Arch string
	CPUCores int
	// RAMTotal e RAMAvail em bytes; 0 quando o sistema não permite medir.
	RAMTotal, RAMAvail uint64
	GPUs               []GPU
	// UnknownGPUs são placas que o sistema mostra mas cuja memória não deu para
	// medir (por exemplo, uma Intel Arc sem o llama.cpp instalado). Ficam fora
	// de GPUs: o mtrad não chuta o tamanho delas.
	UnknownGPUs []string
	// Notes são avisos do que não deu para medir (em português).
	Notes []string
	// Warnings são coisas que pedem atenção do usuário (por exemplo, uma
	// MTRAD_VRAM_GIB inválida). O doctor mostra como aviso, não como "ok".
	Warnings []string
}

// BestGPU devolve a placa com mais memória utilizável. Com várias placas, o
// mtrad só conta a melhor: é o número prudente, sem supor divisão do modelo.
func (i Info) BestGPU() (GPU, bool) {
	var best GPU
	found := false
	for _, g := range i.GPUs {
		if !found || g.Usable() > best.Usable() {
			best, found = g, true
		}
	}
	return best, found
}

// Deps isola o que toca o mundo de fora.
type Deps struct {
	GOOS, GOARCH string
	NumCPU       int
	ReadFile     func(string) ([]byte, error)
	Glob         func(string) ([]string, error)
	// Run executa um comando e devolve a saída padrão; erro se não existir ou falhar.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunAll é como Run, mas devolve a saída padrão e a de erro juntas (o
	// llama.cpp escreve a lista de dispositivos numa delas e o mtrad não sabe qual).
	RunAll func(ctx context.Context, name string, args ...string) ([]byte, error)
	Getenv func(string) string
}

// DefaultDeps usa o sistema de verdade.
func DefaultDeps() Deps {
	return Deps{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, NumCPU: runtime.NumCPU(),
		ReadFile: os.ReadFile,
		Glob:     filepath.Glob,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Output()
		},
		RunAll: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second) // iniciar o Vulkan demora mais
			defer cancel()
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		Getenv: os.Getenv,
	}
}

// Detect mede o computador. Nunca falha: o que não deu para medir vira zero e
// um aviso em Notes.
func Detect(ctx context.Context, d Deps) Info {
	info := Info{OS: d.GOOS, Arch: d.GOARCH, CPUCores: d.NumCPU}
	switch d.GOOS {
	case "linux":
		info.memLinux(d)
		info.gpusSysfs(d)
	case "darwin":
		info.darwin(ctx, d)
	default:
		info.Notes = append(info.Notes, "não sei medir a memória do computador neste sistema")
	}
	if d.GOOS != "darwin" { // no Mac a memória unificada já foi contada acima
		info.gpusNvidia(ctx, d)
		info.gpusLlama(ctx, d)
	}
	if d.GOOS == "linux" {
		info.unknownCards(d)
	}
	info.applyOverride(d)
	sort.SliceStable(info.GPUs, func(a, b int) bool { return info.GPUs[a].Total > info.GPUs[b].Total })
	return info
}

func (i *Info) memLinux(d Deps) {
	b, err := d.ReadFile("/proc/meminfo")
	if err != nil {
		i.Notes = append(i.Notes, "não consegui ler /proc/meminfo")
		return
	}
	i.RAMTotal, i.RAMAvail = parseMeminfo(b)
}

// parseMeminfo lê MemTotal e MemAvailable (em kB) e devolve bytes.
func parseMeminfo(b []byte) (total, avail uint64) {
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			avail = kb * 1024
		}
	}
	return total, avail
}

// gpusSysfs acha placas AMD pelo driver amdgpu, que publica a memória de vídeo
// em /sys/class/drm/cardN/device/mem_info_vram_*. Não precisa de ferramenta.
func (i *Info) gpusSysfs(d Deps) {
	files, err := d.Glob("/sys/class/drm/card*/device/mem_info_vram_total")
	if err != nil {
		return
	}
	sort.Strings(files)
	for _, f := range files {
		dir := filepath.Dir(f)
		total, ok := readUint(d, f)
		if !ok || total == 0 {
			continue
		}
		g := GPU{Vendor: "amd", Total: total, Name: "AMD " + filepath.Base(filepath.Dir(dir))}
		if n, err := d.ReadFile(filepath.Join(dir, "product_name")); err == nil {
			if s := strings.TrimSpace(string(n)); s != "" {
				g.Name = s
			}
		}
		if used, ok := readUint(d, filepath.Join(dir, "mem_info_vram_used")); ok {
			g.Used, g.UsedKnown = used, true
		}
		if total < 2*GiB {
			i.Notes = append(i.Notes, fmt.Sprintf("%s tem só %s GiB de memória de vídeo: parece uma GPU integrada", g.Name, FormatGiB(float64(total)/GiB)))
		}
		i.GPUs = append(i.GPUs, g)
	}
}

func readUint(d Deps, path string) (uint64, bool) {
	b, err := d.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

// gpusNvidia pergunta ao `nvidia-smi`, se estiver instalado.
func (i *Info) gpusNvidia(ctx context.Context, d Deps) {
	out, err := d.Run(ctx, "nvidia-smi", "--query-gpu=name,memory.total,memory.used", "--format=csv,noheader,nounits")
	if err != nil {
		return // sem placa NVIDIA (ou sem a ferramenta): não é problema
	}
	i.GPUs = append(i.GPUs, parseNvidiaSMI(out)...)
}

// parseNvidiaSMI lê linhas "nome, total MiB, usado MiB". O nome pode ter
// vírgula, por isso os dois números são lidos do fim da linha.
func parseNvidiaSMI(out []byte) []GPU {
	var gs []GPU
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) < 3 {
			continue
		}
		totMiB, err := strconv.ParseUint(strings.TrimSpace(parts[len(parts)-2]), 10, 64)
		if err != nil || totMiB == 0 {
			continue // "[N/A]" e afins: sem memória dedicada para contar
		}
		g := GPU{Vendor: "nvidia", Name: strings.TrimSpace(strings.Join(parts[:len(parts)-2], ",")), Total: totMiB << 20}
		if usedMiB, err := strconv.ParseUint(strings.TrimSpace(parts[len(parts)-1]), 10, 64); err == nil {
			g.Used, g.UsedKnown = usedMiB<<20, true
		}
		gs = append(gs, g)
	}
	return gs
}

// llamaDevice lê uma linha da lista que `llama-server --list-devices` imprime:
// "  Vulkan0: AMD Radeon RX 7900 XT (20464 MiB, 20464 MiB free)". O formato foi
// conferido em saídas reais com Vulkan e placas AMD; outros backends (SYCL, CUDA)
// usam a mesma função de listagem do llama.cpp, mas a linha deles não foi vista.
var llamaDevice = regexp.MustCompile(`^\s*([A-Za-z]+)(\d+):\s+(.+?)\s+\((\d+) MiB,\s*(\d+) MiB free\)\s*$`)

// parseLlamaDevices lê a saída de `llama-server --list-devices`. Linhas que não
// têm esse formato (avisos do Vulkan, "Available devices:") são ignoradas.
func parseLlamaDevices(out []byte) []GPU {
	var gs []GPU
	for _, line := range strings.Split(string(out), "\n") {
		m := llamaDevice.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		total, err1 := strconv.ParseUint(m[4], 10, 64)
		free, err2 := strconv.ParseUint(m[5], 10, 64)
		if err1 != nil || err2 != nil || total == 0 {
			continue
		}
		g := GPU{Vendor: guessVendor(m[3]), Name: m[3] + " [" + m[1] + "]", Total: total << 20}
		if free <= total {
			g.Used, g.UsedKnown = (total-free)<<20, true
		}
		gs = append(gs, g)
	}
	return gs
}

// guessVendor adivinha o fabricante pelo nome que o driver informa.
func guessVendor(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia") || strings.Contains(n, "geforce") || strings.Contains(n, "quadro"):
		return "nvidia"
	case strings.Contains(n, "amd") || strings.Contains(n, "radeon"):
		return "amd"
	case strings.Contains(n, "intel") || strings.Contains(n, "arc(tm)"):
		return "intel"
	case strings.Contains(n, "apple"):
		return "apple"
	}
	return "unknown"
}

// sameGPU diz se duas leituras parecem ser a mesma placa: fabricante compatível
// e memória total até 5% diferente.
func sameGPU(a, b GPU) bool {
	if a.Vendor != b.Vendor && a.Vendor != "unknown" && b.Vendor != "unknown" {
		return false
	}
	lo, hi := a.Total, b.Total
	if lo > hi {
		lo, hi = hi, lo
	}
	return hi-lo <= hi/20
}

// gpusLlama pergunta ao llama.cpp, se o `llama-server` estiver instalado, quais
// placas ele enxerga e quanta memória elas têm. É o caminho para medir uma Intel
// Arc no Linux: o kernel não publica o tamanho da memória dela em /sys.
func (i *Info) gpusLlama(ctx context.Context, d Deps) {
	if d.RunAll == nil {
		return
	}
	out, _ := d.RunAll(ctx, "llama-server", "--list-devices") // sem o programa, a saída vem vazia
	for _, g := range parseLlamaDevices(out) {
		dup := false
		for _, e := range i.GPUs {
			if sameGPU(e, g) {
				dup = true
				break
			}
		}
		if !dup {
			i.GPUs = append(i.GPUs, g)
		}
	}
}

// unknownCards acha placas de vídeo pelo fabricante em /sys (Intel, AMD, NVIDIA)
// que nenhuma leitura acima conseguiu medir.
func (i *Info) unknownCards(d Deps) {
	var files []string
	for _, pat := range []string{"/sys/class/drm/card[0-9]/device/vendor", "/sys/class/drm/card[0-9][0-9]/device/vendor"} {
		if f, err := d.Glob(pat); err == nil {
			files = append(files, f...)
		}
	}
	sort.Strings(files)
	vendors := map[string]struct{ name, key string }{
		"0x8086": {"Intel", "intel"}, "0x1002": {"AMD", "amd"}, "0x10de": {"NVIDIA", "nvidia"},
	}
	for _, f := range files {
		raw, err := d.ReadFile(f)
		if err != nil {
			continue
		}
		v, ok := vendors[strings.ToLower(strings.TrimSpace(string(raw)))]
		if !ok || i.measuredVendor(v.key) {
			continue
		}
		i.UnknownGPUs = append(i.UnknownGPUs, v.name+" ("+filepath.Base(filepath.Dir(filepath.Dir(f)))+")")
	}
}

func (i *Info) measuredVendor(key string) bool {
	for _, g := range i.GPUs {
		if g.Vendor == key || g.Vendor == "unknown" {
			return true
		}
	}
	return false
}

// applyOverride deixa o usuário informar a memória da placa quando o mtrad não
// consegue medir: MTRAD_VRAM_GIB=12 (em GiB, aceita vírgula). Vale no lugar de
// qualquer medição e deve ser a memória TOTAL da placa; o mtrad ainda desconta a folga.
func (i *Info) applyOverride(d Deps) {
	if d.Getenv == nil {
		return
	}
	raw := strings.TrimSpace(d.Getenv("MTRAD_VRAM_GIB"))
	if raw == "" {
		return
	}
	v, err := strconv.ParseFloat(strings.Replace(raw, ",", ".", 1), 64)
	if err != nil || math.IsNaN(v) || v <= 0 || v > 1024 {
		i.Warnings = append(i.Warnings, fmt.Sprintf("MTRAD_VRAM_GIB=%q não é um número de GiB entre 0 e 1024: ignorei", raw))
		return
	}
	i.GPUs = []GPU{{Vendor: "manual", Name: "memória informada por você (MTRAD_VRAM_GIB)", Total: uint64(v * GiB)}}
	i.UnknownGPUs = nil
	i.Notes = append(i.Notes, "usando MTRAD_VRAM_GIB="+FormatGiB(v)+" no lugar da medição")
}

func (i *Info) darwin(ctx context.Context, d Deps) {
	out, err := d.Run(ctx, "sysctl", "-n", "hw.memsize")
	if err != nil {
		i.Notes = append(i.Notes, "não consegui ler a memória com sysctl")
		return
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		i.Notes = append(i.Notes, "sysctl hw.memsize devolveu um valor estranho")
		return
	}
	i.RAMTotal = n
	if d.GOARCH == "arm64" {
		i.GPUs = append(i.GPUs, GPU{Vendor: "apple", Name: "Apple Silicon (memória unificada)", Total: n, Unified: true})
		i.Notes = append(i.Notes, "Apple Silicon: conto só 2/3 da memória para o modelo (estimativa prudente)")
	} else {
		i.Notes = append(i.Notes, "Mac com Intel: não sei medir a placa de vídeo; vou supor só CPU")
	}
}

// Lines descreve o que foi encontrado, uma linha por item, para o doctor e o setup.
func (i Info) Lines() []string {
	var out []string
	gib := func(b uint64) string { return FormatGiB(float64(b)/GiB) + " GiB" }
	if i.RAMTotal > 0 {
		s := "memória do sistema: " + gib(i.RAMTotal)
		if i.RAMAvail > 0 {
			s += " (livre " + gib(i.RAMAvail) + ")"
		}
		out = append(out, s)
	}
	out = append(out, fmt.Sprintf("processador: %d núcleos", i.CPUCores))
	if len(i.GPUs) == 0 && len(i.UnknownGPUs) == 0 {
		out = append(out, "placa de vídeo: nenhuma encontrada (o modelo roda na CPU, mais devagar)")
	}
	if len(i.UnknownGPUs) > 0 {
		out = append(out, "placa de vídeo: encontrei "+strings.Join(i.UnknownGPUs, ", ")+", mas não consegui medir a memória (rode `llama-server --list-devices` ou informe MTRAD_VRAM_GIB)")
	}
	for _, g := range i.GPUs {
		s := fmt.Sprintf("placa de vídeo: %s, %s", g.Name, gib(g.Total))
		if g.UsedKnown {
			s += ", em uso " + gib(g.Used)
		}
		s += ", sobram " + gib(g.Usable()) + " para o modelo"
		out = append(out, s)
	}
	out = append(out, i.Notes...)
	return append(out, i.Warnings...)
}
