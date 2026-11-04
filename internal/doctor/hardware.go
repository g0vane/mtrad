package doctor

import (
	"sort"
	"strings"

	"mtrad/internal/hardware"
)

// hardware conta o que foi medido no computador, sugere um modelo e confere se
// o modelo local que a configuração usa cabe. Sem Input.Hardware não faz nada.
func (r *runner) hardware(in Input) {
	if in.Hardware == nil {
		return
	}
	info := *in.Hardware
	_, hasGPU := info.BestGPU()
	unmeasured := !hasGPU && len(info.UnknownGPUs) > 0
	for _, l := range info.Lines() {
		if !hasGPU && (strings.HasPrefix(l, "placa de vídeo: nenhuma") || strings.HasPrefix(l, "placa de vídeo: encontrei")) {
			continue // o aviso abaixo diz o mesmo, com nível de aviso
		}
		lvl := OK
		for _, w := range info.Warnings {
			if l == w {
				lvl = Warn
			}
		}
		r.add(lvl, "computador", "%s", l)
	}
	switch {
	case unmeasured:
		r.add(Warn, "placa de vídeo", "encontrei %s, mas não consegui medir a memória: rode `llama-server --list-devices` (llama.cpp) ou informe a memória TOTAL da placa com MTRAD_VRAM_GIB=<GiB>", strings.Join(info.UnknownGPUs, ", "))
	case !hasGPU:
		r.add(Warn, "placa de vídeo", "nenhuma encontrada: o modelo local roda na CPU, bem mais devagar")
	}
	rec := hardware.Recommend(info, hardware.Catalog, nil)
	if rec.Best == nil || unmeasured {
		r.add(Warn, "modelo sugerido", "%s", rec.Why)
	} else {
		r.add(OK, "modelo sugerido", "%s (tamanhos conferidos em %s)", rec.Why, hardware.CatalogChecked)
	}
	if in.Translate == nil {
		return
	}
	names := make([]string, 0, len(in.Translate.Engines))
	for n := range in.Translate.Engines {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ec := in.Translate.Engines[n]
		if ec.Type != "openai" || !strings.Contains(ec.BaseURL, ":11434") {
			continue // só o Ollama é local e tem modelo no catálogo
		}
		for _, o := range rec.Options {
			if ec.Model != o.Model.ID && !strings.HasPrefix(ec.Model, o.Model.ID+"-") {
				continue
			}
			label := "modelo do motor " + n
			switch o.Fit {
			case hardware.FitGPU:
				r.add(OK, label, "o modelo %s cabe inteiro na placa de vídeo", ec.Model)
			case hardware.FitNone:
				r.add(Warn, label, "o modelo %s (%s GB) não cabe neste computador; escolha um menor ou um motor na nuvem", ec.Model, hardware.FormatGiB(o.Model.SizeGiB))
			case hardware.FitUnknown:
				r.add(Warn, label, "não consegui medir a memória para saber se %s cabe", ec.Model)
			default:
				r.add(Warn, label, "o modelo %s vai rodar %s", ec.Model, o.Fit)
			}
		}
	}
}
