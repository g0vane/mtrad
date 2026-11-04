package hardware

import (
	"strings"
	"testing"
)

func gpuInfo(totalGiB float64, usedGiB float64, ramGiB uint64) Info {
	return Info{
		RAMTotal: ramGiB * GiB, RAMAvail: ramGiB * GiB,
		GPUs: []GPU{{Vendor: "amd", Name: "teste", Total: uint64(totalGiB * GiB), Used: uint64(usedGiB * GiB), UsedKnown: true}},
	}
}

func fitOf(rec Recommendation, id string) Fit {
	for _, o := range rec.Options {
		if o.Model.ID == id {
			return o.Fit
		}
	}
	return -1
}

func TestRX580De8GBFicaComOModelo4b(t *testing.T) {
	// 8 GiB, 0,5 em uso: sobram 6,3 GiB. O 4b (3,3) cabe; o 12b (8,1) não.
	rec := Recommend(gpuInfo(8, 0.5, 16), Catalog, nil)
	if rec.Best == nil || rec.Best.Model.ID != "translategemma:4b" {
		t.Fatalf("Best = %+v (%s)", rec.Best, rec.Why)
	}
	if fitOf(rec, "translategemma:12b") != FitPartial {
		t.Fatalf("o 12b devia caber só em parte: %v", fitOf(rec, "translategemma:12b"))
	}
	// O 27b (17 GiB) só roda com mais de 10 GiB na CPU: cabe "em parte", mas a
	// sugestão continua sendo o 4b.
	if fitOf(rec, "translategemma:27b") != FitPartial {
		t.Fatalf("o 27b devia caber só em parte: %v", fitOf(rec, "translategemma:27b"))
	}
	// Com pouca RAM livre o mesmo modelo já não cabe de jeito nenhum.
	rec = Recommend(gpuInfo(8, 0.5, 8), Catalog, nil)
	if fitOf(rec, "translategemma:27b") != FitNone {
		t.Fatalf("com 8 GiB de RAM o 27b não cabe: %v", fitOf(rec, "translategemma:27b"))
	}
}

func TestPlacaGrandeEscolheOMaiorTradutor(t *testing.T) {
	rec := Recommend(gpuInfo(24, 0, 32), Catalog, nil)
	if rec.Best == nil || rec.Best.Model.ID != "translategemma:27b" {
		t.Fatalf("Best = %+v", rec.Best)
	}
	if !rec.Best.Model.Translator {
		t.Fatal("a sugestão deve ser um tradutor quando um cabe")
	}
}

func TestSemPlacaUsaCPUComAviso(t *testing.T) {
	rec := Recommend(Info{RAMTotal: 16 * GiB, RAMAvail: 12 * GiB}, Catalog, nil)
	if rec.Best == nil || rec.Best.Model.ID != "translategemma:4b" || rec.Best.Fit != FitCPU {
		t.Fatalf("Best = %+v", rec.Best)
	}
	if !strings.Contains(rec.Why, "CPU") || !strings.Contains(rec.Why, "não há placa") {
		t.Fatalf("a explicação deve falar da CPU e da falta de placa: %q", rec.Why)
	}
}

func TestPoucaRAMSemPlacaNaoSugereNada(t *testing.T) {
	rec := Recommend(Info{RAMTotal: 4 * GiB, RAMAvail: 3 * GiB}, Catalog, nil)
	if rec.Best != nil {
		t.Fatalf("com 3 GiB livres nada deveria ser sugerido: %+v", rec.Best)
	}
	if !strings.Contains(rec.Why, "nuvem") {
		t.Fatalf("deve apontar a nuvem: %q", rec.Why)
	}
}

func TestMemoriaNaoMedidaNaoChutaCabimento(t *testing.T) {
	rec := Recommend(Info{CPUCores: 4}, Catalog, nil) // sem GPU e sem RAM medida (ex.: Windows)
	if fitOf(rec, "translategemma:4b") != FitUnknown {
		t.Fatalf("devia ser FitUnknown: %v", fitOf(rec, "translategemma:4b"))
	}
	if rec.Best == nil || !strings.Contains(rec.Why, "não consegui medir") {
		t.Fatalf("a sugestão deve avisar que não mediu: %+v %q", rec.Best, rec.Why)
	}
}

func TestInstaladoReconheceSufixoDeQuantizacao(t *testing.T) {
	rec := Recommend(gpuInfo(8, 0, 16), Catalog, []string{"translategemma:4b-it-q4_K_M", "outro:1b"})
	for _, o := range rec.Options {
		want := o.Model.ID == "translategemma:4b"
		if o.Installed != want {
			t.Fatalf("%s: Installed = %v", o.Model.ID, o.Installed)
		}
	}
	// translategemma:4b não deve ser confundido com um nome que só começa igual
	if isInstalled("gemma3:4b", []string{"gemma3:40b"}) {
		t.Fatal("gemma3:40b não é gemma3:4b")
	}
}

func TestSemTradutorQueSirvaCaiNosModelosGerais(t *testing.T) {
	models := []Model{{ID: "t:big", SizeGiB: 30, Translator: true}, {ID: "g:small", SizeGiB: 2}}
	rec := Recommend(gpuInfo(8, 0, 16), models, nil)
	if rec.Best == nil || rec.Best.Model.ID != "g:small" {
		t.Fatalf("Best = %+v", rec.Best)
	}
}

func TestCatalogoSemDuplicatasENomesValidos(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Catalog {
		if m.ID == "" || !strings.Contains(m.ID, ":") || m.SizeGiB <= 0 {
			t.Fatalf("linha inválida: %+v", m)
		}
		if seen[m.ID] {
			t.Fatalf("modelo repetido: %s", m.ID)
		}
		seen[m.ID] = true
	}
	if CatalogChecked == "" || CatalogSource == "" {
		t.Fatal("o catálogo precisa dizer de onde veio e quando foi conferido")
	}
}

func TestSortedBySizeNaoMexeNaOriginal(t *testing.T) {
	in := []Model{{ID: "b", SizeGiB: 9}, {ID: "a", SizeGiB: 1}}
	out := SortedBySize(in)
	if out[0].ID != "a" || in[0].ID != "b" {
		t.Fatalf("out=%v in=%v", out, in)
	}
}
