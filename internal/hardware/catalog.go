package hardware

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogChecked e CatalogSource dizem de onde vieram os tamanhos abaixo e
// quando foram conferidos. Os tamanhos mudam quando o Ollama republica um
// modelo: confira de novo antes de acrescentar linhas.
const (
	CatalogChecked = "2026-11-03"
	CatalogSource  = "https://ollama.com/library/translategemma/tags e https://ollama.com/library/gemma3"
)

// Model é um modelo local do Ollama que o mtrad sabe sugerir.
type Model struct {
	ID string // nome como o `ollama pull` espera
	// SizeGiB é o tamanho do download como o Ollama mostra. Contado como GiB
	// (um pouco a mais que o real), para a conta errar para o lado seguro.
	SizeGiB float64
	// Translator marca os modelos feitos para tradução; entram na frente na
	// recomendação.
	Translator bool
	Note       string
}

// Catalog é a lista conferida em CatalogChecked. Só entram modelos cujo
// tamanho foi lido na página do Ollama; não há número copiado de memória.
var Catalog = []Model{
	{ID: "translategemma:4b", SizeGiB: 3.3, Translator: true, Note: "feito para tradução; a página do modelo lista pt-BR"},
	{ID: "translategemma:12b", SizeGiB: 8.1, Translator: true, Note: "feito para tradução; mais fiel, pede mais memória"},
	{ID: "translategemma:27b", SizeGiB: 17, Translator: true, Note: "feito para tradução; o maior da família"},
	{ID: "gemma3:4b", SizeGiB: 3.4, Note: "modelo geral, alternativa se o translategemma não servir"},
	{ID: "gemma3:12b", SizeGiB: 8.2, Note: "modelo geral"},
}

// Fit diz onde um modelo cabe.
type Fit int

const (
	FitNone    Fit = iota // não cabe nem somando a memória do sistema
	FitCPU                // sem placa de vídeo: roda na CPU (devagar)
	FitPartial            // parte na placa, parte na CPU (devagar)
	FitGPU                // cabe inteiro na placa
	FitUnknown            // não deu para medir a memória do sistema
)

func (f Fit) String() string {
	return [...]string{"não cabe", "só na CPU (lento)", "parte na CPU (mais lento)", "cabe na placa de vídeo", "não consegui medir"}[f]
}

// Option é um modelo do catálogo avaliado para este computador.
type Option struct {
	Model     Model
	Fit       Fit
	Installed bool
}

// Recommendation é o resultado de Recommend.
type Recommendation struct {
	Options []Option // na ordem do catálogo
	// Best é a sugestão; nil quando nenhum modelo do catálogo serve (o caminho
	// então é um motor na nuvem ou um modelo menor escolhido por você).
	Best *Option
	// Why explica a sugestão em uma frase.
	Why string
}

const cpuHeadroom = 2 * GiB // sobra de RAM para o sistema ao rodar na CPU

// Recommend avalia cada modelo contra o hardware. installed são os nomes que
// `ollama list` mostra (pode ser nil).
func Recommend(info Info, models []Model, installed []string) Recommendation {
	var rec Recommendation
	gpu, hasGPU := info.BestGPU()
	usable := uint64(0)
	if hasGPU {
		usable = gpu.Usable()
	}
	// há placa de vídeo, mas o mtrad não conseguiu medir a memória dela: nesse caso
	// não dá para dizer que o modelo roda "só na CPU"
	unknown := ""
	if !hasGPU && len(info.UnknownGPUs) > 0 {
		unknown = strings.Join(info.UnknownGPUs, ", ")
	}
	ram := info.RAMAvail
	if ram == 0 {
		ram = info.RAMTotal
	}
	for _, m := range models {
		size := uint64(m.SizeGiB * GiB)
		o := Option{Model: m, Installed: isInstalled(m.ID, installed)}
		switch {
		case unknown != "":
			o.Fit = FitUnknown
		case hasGPU && usable >= size:
			o.Fit = FitGPU
		case ram == 0:
			o.Fit = FitUnknown
		case hasGPU && usable > 0 && ram >= size-usable+cpuHeadroom:
			o.Fit = FitPartial
		case !hasGPU && ram >= size+cpuHeadroom:
			o.Fit = FitCPU
		case hasGPU && usable == 0 && ram >= size+cpuHeadroom:
			o.Fit = FitCPU
		default:
			o.Fit = FitNone
		}
		rec.Options = append(rec.Options, o)
	}
	rec.Best, rec.Why = pickBest(rec.Options, usable > 0, unknown)
	return rec
}

// pickBest escolhe o maior tradutor que cabe inteiro na placa; se nenhum cabe,
// o menor tradutor que ao menos roda (parte na CPU, ou só CPU). Sem tradutor
// que sirva, tenta o mesmo com os outros modelos.
func pickBest(opts []Option, hasUsableGPU bool, unknownGPU string) (*Option, string) {
	pick := func(translator bool) (*Option, string) {
		var gpu, slow *Option
		for i := range opts {
			o := &opts[i]
			if o.Model.Translator != translator {
				continue
			}
			switch o.Fit {
			case FitGPU:
				if gpu == nil || o.Model.SizeGiB > gpu.Model.SizeGiB {
					gpu = o
				}
			case FitPartial, FitCPU, FitUnknown:
				if slow == nil || o.Model.SizeGiB < slow.Model.SizeGiB {
					slow = o
				}
			}
		}
		switch {
		case gpu != nil:
			return gpu, fmt.Sprintf("%s é o maior modelo desta lista que cabe inteiro na sua placa de vídeo", gpu.Model.ID)
		case slow != nil && slow.Fit == FitUnknown && unknownGPU != "":
			return slow, fmt.Sprintf("encontrei %s, mas não consegui medir a memória dela; %s é o menor desta lista. Rode `llama-server --list-devices` (llama.cpp) ou informe MTRAD_VRAM_GIB para eu saber se cabe um modelo maior", unknownGPU, slow.Model.ID)
		case slow != nil && slow.Fit == FitUnknown:
			return slow, fmt.Sprintf("%s é o menor desta lista; não consegui medir a memória, confira com `mtrad doctor`", slow.Model.ID)
		case slow != nil && !hasUsableGPU:
			return slow, fmt.Sprintf("não há placa de vídeo com memória livre para o modelo; %s é o menor e vai rodar %s", slow.Model.ID, slow.Fit)
		case slow != nil:
			return slow, fmt.Sprintf("nada cabe inteiro na placa de vídeo; %s é o menor e vai rodar %s", slow.Model.ID, slow.Fit)
		}
		return nil, ""
	}
	if o, why := pick(true); o != nil {
		return o, why
	}
	if o, why := pick(false); o != nil {
		return o, why
	}
	return nil, "nenhum modelo da lista cabe neste computador: use um motor na nuvem ou escolha um modelo menor"
}

// isInstalled compara com os nomes do `ollama list`: o mesmo nome ou o nome
// com sufixo de quantização (translategemma:4b-it-q4_K_M).
func isInstalled(id string, installed []string) bool {
	for _, n := range installed {
		if n == id || strings.HasPrefix(n, id+"-") {
			return true
		}
	}
	return false
}

// SortedBySize devolve uma cópia da lista do menor para o maior.
func SortedBySize(models []Model) []Model {
	out := append([]Model(nil), models...)
	sort.SliceStable(out, func(a, b int) bool { return out[a].SizeGiB < out[b].SizeGiB })
	return out
}
