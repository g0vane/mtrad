package hardware

import (
	"context"
	"strings"
	"testing"
)

// Saídas reais de `llama-server --list-devices` com Vulkan e placas AMD, tiradas de
// um guia de uso e de uma discussão pública do llama.cpp (ver docs/HARDWARE_E_MOTORES.md).
const llamaGuia = `ggml_vulkan: Found 1 Vulkan devices:
ggml_vulkan: 0 = AMD Radeon RX 7900 XT (AMD open-source driver) | uma: 0 | fp16: 1 | warp size: 64
Available devices:
  Vulkan0: AMD Radeon RX 7900 XT (20464 MiB, 20464 MiB free)
`

const llamaDiscussao = `Available devices:
  Vulkan0: AMD Radeon Graphics (RADV RENOIR) (98330 MiB, 97438 MiB free)
  Vulkan1: AMD Radeon Pro V620 (RADV NAVI21) (30704 MiB, 30669 MiB free)
  Vulkan2: AMD Radeon Pro V620 (RADV NAVI21) (30704 MiB, 30669 MiB free)
`

// ATENÇÃO: o formato da linha do SYCL (Intel Arc) NÃO foi visto em saída real; este
// texto supõe que o llama.cpp usa a mesma função de listagem de todos os backends.
const llamaArcPresumido = "Available devices:\n  SYCL0: Intel(R) Arc(TM) A770 Graphics (16225 MiB, 15999 MiB free)\n"

func withLlama(d Deps) Deps { d.RunAll = d.Run; return d }

func withEnv(d Deps, env map[string]string) Deps {
	d.Getenv = func(k string) string { return env[k] }
	return d
}

func TestParseLlamaDevicesSaidasReais(t *testing.T) {
	gs := parseLlamaDevices([]byte(llamaGuia))
	if len(gs) != 1 {
		t.Fatalf("esperava 1 placa, veio %+v", gs)
	}
	g := gs[0]
	if g.Vendor != "amd" || g.Name != "AMD Radeon RX 7900 XT [Vulkan]" || g.Total != 20464<<20 || !g.UsedKnown || g.Used != 0 {
		t.Fatalf("%+v", g)
	}
	gs = parseLlamaDevices([]byte(llamaDiscussao))
	if len(gs) != 3 {
		t.Fatalf("esperava 3 placas, veio %+v", gs)
	}
	// o nome tem parênteses: tudo antes do "(memória" é o nome
	if gs[0].Name != "AMD Radeon Graphics (RADV RENOIR) [Vulkan]" || gs[0].Total != 98330<<20 || gs[0].Used != (98330-97438)<<20 {
		t.Fatalf("%+v", gs[0])
	}
	if gs[1].Name != "AMD Radeon Pro V620 (RADV NAVI21) [Vulkan]" || gs[1].Total != 30704<<20 {
		t.Fatalf("%+v", gs[1])
	}
}

func TestParseLlamaDevicesCasosRuins(t *testing.T) {
	for name, in := range map[string]string{
		"lista vazia (sem backend de GPU)": "Available devices:\n",
		"só avisos do Vulkan":              "ggml_vulkan: No devices found.\n",
		"vazio":                            "",
		"memória zero":                     "Available devices:\n  Vulkan0: Algo (0 MiB, 0 MiB free)\n",
		"sem o 'free'":                     "Available devices:\n  Vulkan0: Algo (1024 MiB)\n",
		"texto qualquer":                   "error: invalid argument: --list-devices\n",
	} {
		if gs := parseLlamaDevices([]byte(in)); len(gs) != 0 {
			t.Errorf("%s: não devia achar placa: %+v", name, gs)
		}
	}
	// linhas com fim CRLF (Windows) e livre maior que o total (leitura estranha)
	gs := parseLlamaDevices([]byte("Available devices:\r\n  CUDA0: NVIDIA GeForce RTX 3060 (12288 MiB, 11000 MiB free)\r\n  Vulkan1: Bicho (1000 MiB, 2000 MiB free)\r\n"))
	if len(gs) != 2 || gs[0].Vendor != "nvidia" || gs[0].Used != (12288-11000)<<20 {
		t.Fatalf("CRLF: %+v", gs)
	}
	if gs[1].UsedKnown || gs[1].Vendor != "unknown" {
		t.Fatalf("livre > total não pode virar uso negativo: %+v", gs[1])
	}
}

func TestGuessVendor(t *testing.T) {
	for in, want := range map[string]string{
		"NVIDIA GeForce RTX 3060": "nvidia", "Quadro P2000": "nvidia",
		"AMD Radeon RX 580": "amd", "Radeon Pro V620": "amd",
		"Intel(R) Arc(TM) A770 Graphics": "intel", "Intel(R) UHD Graphics": "intel",
		"Apple M2": "apple", "Placa Misteriosa": "unknown",
	} {
		if got := guessVendor(in); got != want {
			t.Errorf("guessVendor(%q) = %q, queria %q", in, got, want)
		}
	}
}

func TestSameGPU(t *testing.T) {
	a := GPU{Vendor: "amd", Total: 8 << 30}
	for name, tc := range map[string]struct {
		b    GPU
		want bool
	}{
		"mesma placa, tamanho igual":         {GPU{Vendor: "amd", Total: 8 << 30}, true},
		"mesma placa, 3% de diferença":       {GPU{Vendor: "amd", Total: 8<<30 - 8<<30/33}, true},
		"fabricante desconhecido, igual":     {GPU{Vendor: "unknown", Total: 8 << 30}, true},
		"outro fabricante, mesmo tamanho":    {GPU{Vendor: "nvidia", Total: 8 << 30}, false},
		"mesma marca, memória bem diferente": {GPU{Vendor: "amd", Total: 512 << 20}, false},
	} {
		if got := sameGPU(a, tc.b); got != tc.want {
			t.Errorf("%s: sameGPU = %v", name, got)
		}
	}
}

func TestArcMedidaPeloLlamaServer(t *testing.T) {
	d := withLlama(fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo":                      meminfo,
		"/sys/class/drm/card1/device/vendor": "0x8086\n",
	}, map[string]string{"llama-server": llamaArcPresumido}))
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 1 || info.GPUs[0].Vendor != "intel" || info.GPUs[0].Total != 16225<<20 {
		t.Fatalf("a Arc devia ser medida pelo llama.cpp: %+v", info.GPUs)
	}
	if len(info.UnknownGPUs) != 0 {
		t.Fatalf("placa medida não pode aparecer como 'não medida': %v", info.UnknownGPUs)
	}
	rec := Recommend(info, Catalog, nil)
	// 16225 MiB − 226 MiB em uso − 1,2 GiB de folga ≈ 14,4 GiB: cabe o 12b (8,1), não o 27b (17)
	if rec.Best == nil || rec.Best.Model.ID != "translategemma:12b" || rec.Best.Fit != FitGPU {
		t.Fatalf("Best = %+v (%s)", rec.Best, rec.Why)
	}
}

func TestIntelSemLlamaFicaSemMedidaESemChute(t *testing.T) {
	d := withLlama(fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo":                      meminfo,
		"/sys/class/drm/card1/device/vendor": "0x8086\n",
	}, nil)) // llama-server não instalado
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 0 || len(info.UnknownGPUs) != 1 || info.UnknownGPUs[0] != "Intel (card1)" {
		t.Fatalf("%+v", info)
	}
	lines := strings.Join(info.Lines(), "\n")
	if !strings.Contains(lines, "encontrei Intel (card1)") || strings.Contains(lines, "nenhuma encontrada") {
		t.Fatalf("o resumo não pode dizer 'nenhuma placa' quando há uma: %s", lines)
	}
	rec := Recommend(info, Catalog, nil)
	for _, o := range rec.Options {
		if o.Fit != FitUnknown {
			t.Fatalf("sem medida, %s não pode ser dado como CPU ou GPU: %v", o.Model.ID, o.Fit)
		}
	}
	if rec.Best == nil || rec.Best.Model.ID != "translategemma:4b" {
		t.Fatalf("o palpite seguro é o menor tradutor: %+v", rec.Best)
	}
	for _, want := range []string{"Intel (card1)", "llama-server --list-devices", "MTRAD_VRAM_GIB"} {
		if !strings.Contains(rec.Why, want) {
			t.Errorf("a explicação devia citar %q: %s", want, rec.Why)
		}
	}
	if strings.Contains(rec.Why, "CPU") {
		t.Errorf("não pode mandar para a CPU sem saber: %s", rec.Why)
	}
}

func TestLlamaNaoDuplicaPlacaJaMedida(t *testing.T) {
	d := withLlama(fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo": meminfo,
		"/sys/class/drm/card0/device/mem_info_vram_total": "8589934592\n",
		"/sys/class/drm/card0/device/vendor":              "0x1002\n",
	}, map[string]string{"llama-server": "Available devices:\n  Vulkan0: AMD Radeon RX 580 (8192 MiB, 8000 MiB free)\n"}))
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 1 || info.GPUs[0].Name == "" || strings.Contains(info.GPUs[0].Name, "[Vulkan]") {
		t.Fatalf("a mesma placa lida pelo sysfs e pelo llama.cpp devia contar uma vez, a do sysfs: %+v", info.GPUs)
	}
	// duas placas de fabricantes diferentes com o mesmo tamanho são duas placas
	d = withLlama(fakeDeps("linux", "amd64", map[string]string{"/proc/meminfo": meminfo},
		map[string]string{"nvidia-smi": "NVIDIA GeForce RTX 3060, 8192, 100\n", "llama-server": "Available devices:\n  Vulkan1: AMD Radeon RX 580 (8192 MiB, 8000 MiB free)\n"}))
	if info := Detect(context.Background(), d); len(info.GPUs) != 2 {
		t.Fatalf("NVIDIA e AMD de 8 GiB não são a mesma placa: %+v", info.GPUs)
	}
}

func TestPlacasSemMedidaPorFabricante(t *testing.T) {
	d := withLlama(fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo":                               meminfo,
		"/sys/class/drm/card0/device/vendor":          "0x10DE\n", // NVIDIA sem nvidia-smi (maiúsculas)
		"/sys/class/drm/card1/device/vendor":          "0x1002\n", // AMD sem mem_info_vram_total
		"/sys/class/drm/card2/device/vendor":          "0x1af4\n", // placa virtual: fora da lista
		"/sys/class/drm/card0-HDMI-A-1/device/vendor": "0x8086\n", // conector, não é placa
		"/sys/class/drm/card10/device/vendor":         "0x8086\n", // dois dígitos também
	}, nil))
	info := Detect(context.Background(), d)
	got := strings.Join(info.UnknownGPUs, "; ")
	if got != "NVIDIA (card0); AMD (card1); Intel (card10)" && got != "Intel (card10); NVIDIA (card0); AMD (card1)" {
		t.Fatalf("UnknownGPUs = %q", got)
	}
	if len(info.UnknownGPUs) != 3 {
		t.Fatalf("%v", info.UnknownGPUs)
	}
}

func TestPlacaMedidaDoMesmoFabricanteNaoViraNaoMedida(t *testing.T) {
	d := fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo": meminfo,
		"/sys/class/drm/card0/device/mem_info_vram_total": "8589934592\n",
		"/sys/class/drm/card0/device/vendor":              "0x1002\n",
	}, nil)
	if info := Detect(context.Background(), d); len(info.UnknownGPUs) != 0 {
		t.Fatalf("%v", info.UnknownGPUs)
	}
}

func TestOverrideDaMemoria(t *testing.T) {
	base := map[string]string{"/proc/meminfo": meminfo, "/sys/class/drm/card1/device/vendor": "0x8086\n"}
	for _, tc := range []struct {
		env  string
		want uint64
	}{{"12", 12 << 30}, {"12,5", 12<<30 + 1<<29}, {" 8 ", 8 << 30}, {"0.5", 1 << 29}} {
		d := withEnv(fakeDeps("linux", "amd64", base, nil), map[string]string{"MTRAD_VRAM_GIB": tc.env})
		info := Detect(context.Background(), d)
		if len(info.GPUs) != 1 || info.GPUs[0].Vendor != "manual" || info.GPUs[0].Total != tc.want {
			t.Fatalf("MTRAD_VRAM_GIB=%q: %+v", tc.env, info.GPUs)
		}
		if len(info.UnknownGPUs) != 0 {
			t.Fatalf("informar a memória resolve a placa não medida: %v", info.UnknownGPUs)
		}
		if !strings.Contains(strings.Join(info.Notes, "\n"), "MTRAD_VRAM_GIB") {
			t.Fatalf("devia avisar que usou o valor informado: %v", info.Notes)
		}
	}
	// valores inválidos são ignorados, com aviso, e a medição segue
	for _, bad := range []string{"abc", "0", "-3", "5000", "NaN", "Inf", "1e999"} {
		d := withEnv(fakeDeps("linux", "amd64", base, nil), map[string]string{"MTRAD_VRAM_GIB": bad})
		info := Detect(context.Background(), d)
		if len(info.GPUs) != 0 || len(info.UnknownGPUs) != 1 {
			t.Fatalf("MTRAD_VRAM_GIB=%q devia ser ignorada: %+v", bad, info)
		}
		if !strings.Contains(strings.Join(info.Warnings, "\n"), "ignorei") {
			t.Fatalf("MTRAD_VRAM_GIB=%q: faltou o aviso: %v", bad, info.Warnings)
		}
	}
	// vazio ou ausente: nada acontece
	d := withEnv(fakeDeps("linux", "amd64", base, nil), map[string]string{"MTRAD_VRAM_GIB": "  "})
	if info := Detect(context.Background(), d); len(info.Notes) != 0 || len(info.Warnings) != 0 {
		t.Fatalf("variável vazia não devia gerar aviso: %v %v", info.Notes, info.Warnings)
	}
	// o valor informado vale no lugar de uma medição
	d = withEnv(fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo": meminfo, "/sys/class/drm/card0/device/mem_info_vram_total": "8589934592\n",
	}, nil), map[string]string{"MTRAD_VRAM_GIB": "16"})
	if info := Detect(context.Background(), d); len(info.GPUs) != 1 || info.GPUs[0].Total != 16<<30 {
		t.Fatalf("%+v", info.GPUs)
	}
	// total informado de 12 GiB: sobram 12 − 1,2 de folga
	g := GPU{Vendor: "manual", Total: 12 << 30}
	if g.Usable() != 12<<30-Buffer {
		t.Fatalf("Usable = %d", g.Usable())
	}
}

func TestMacNaoConsultaOLlama(t *testing.T) {
	d := withLlama(fakeDeps("darwin", "arm64", nil, map[string]string{
		"sysctl":       "34359738368\n",
		"llama-server": "Available devices:\n  Metal0: Apple M2 Max (24576 MiB, 24000 MiB free)\n",
	}))
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 1 || !info.GPUs[0].Unified {
		t.Fatalf("no Mac vale só a memória unificada, sem duplicar pelo llama.cpp: %+v", info.GPUs)
	}
}

func TestSemRunAllNaoQuebra(t *testing.T) {
	d := fakeDeps("linux", "amd64", map[string]string{"/proc/meminfo": meminfo}, map[string]string{"llama-server": llamaGuia})
	d.RunAll, d.Getenv = nil, nil // Deps antigo, sem os campos novos
	if info := Detect(context.Background(), d); len(info.GPUs) != 0 {
		t.Fatalf("%+v", info.GPUs)
	}
}
