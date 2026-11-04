package hardware

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
)

// fakeDeps monta um computador de mentira: arquivos e saída de comandos.
func fakeDeps(goos, goarch string, files map[string]string, cmds map[string]string) Deps {
	return Deps{
		GOOS: goos, GOARCH: goarch, NumCPU: 8,
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		},
		Glob: func(pat string) ([]string, error) {
			var out []string
			for p := range files {
				if ok, _ := path.Match(pat, p); ok {
					out = append(out, p)
				}
			}
			sort.Strings(out)
			return out, nil
		},
		Run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if s, ok := cmds[name]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("comando não encontrado")
		},
	}
}

const meminfo = "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    9000000 kB\n"

func TestLinuxAMDPelaSysfs(t *testing.T) {
	d := fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo": meminfo,
		"/sys/class/drm/card0/device/mem_info_vram_total": "8589934592\n",
		"/sys/class/drm/card0/device/mem_info_vram_used":  "524288000\n",
		"/sys/class/drm/card0/device/product_name":        "Radeon RX 580\n",
	}, nil)
	info := Detect(context.Background(), d)
	if info.RAMTotal != 16384000*1024 || info.RAMAvail != 9000000*1024 {
		t.Fatalf("RAM errada: %d / %d", info.RAMTotal, info.RAMAvail)
	}
	if len(info.GPUs) != 1 {
		t.Fatalf("esperava 1 placa, veio %+v", info.GPUs)
	}
	g := info.GPUs[0]
	if g.Vendor != "amd" || g.Name != "Radeon RX 580" || g.Total != 8<<30 || !g.UsedKnown || g.Used != 524288000 {
		t.Fatalf("placa errada: %+v", g)
	}
	// 8 GiB - 500 MiB em uso - 1,2 GiB de folga
	want := uint64(8<<30) - 524288000 - Buffer
	if g.Usable() != want {
		t.Fatalf("Usable = %d, queria %d", g.Usable(), want)
	}
}

func TestGPUIntegradaGeraAviso(t *testing.T) {
	d := fakeDeps("linux", "amd64", map[string]string{
		"/proc/meminfo": meminfo,
		"/sys/class/drm/card1/device/mem_info_vram_total": "536870912",
	}, nil)
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 1 || info.GPUs[0].Usable() != 0 {
		t.Fatalf("GPU de 512 MiB não deve ter memória utilizável: %+v", info.GPUs)
	}
	if !strings.Contains(strings.Join(info.Notes, "\n"), "integrada") {
		t.Fatalf("faltou o aviso de GPU integrada: %v", info.Notes)
	}
}

func TestNvidiaSMI(t *testing.T) {
	out := "NVIDIA GeForce RTX 3060, 12288, 1024\nQuadro, com vírgula, 8192, [N/A]\nJetson, [N/A], [N/A]\n\n"
	d := fakeDeps("linux", "amd64", map[string]string{"/proc/meminfo": meminfo}, map[string]string{"nvidia-smi": out})
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 2 {
		t.Fatalf("esperava 2 placas (a de memória N/A é ignorada), veio %+v", info.GPUs)
	}
	if info.GPUs[0].Name != "NVIDIA GeForce RTX 3060" || info.GPUs[0].Total != 12288<<20 || info.GPUs[0].Used != 1024<<20 {
		t.Fatalf("primeira placa errada: %+v", info.GPUs[0])
	}
	if info.GPUs[1].Name != "Quadro, com vírgula" || info.GPUs[1].UsedKnown {
		t.Fatalf("segunda placa errada (nome com vírgula, uso N/A): %+v", info.GPUs[1])
	}
}

func TestSemPlacaNemFerramenta(t *testing.T) {
	d := fakeDeps("linux", "amd64", map[string]string{"/proc/meminfo": meminfo}, nil)
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 0 {
		t.Fatalf("não devia achar placa: %+v", info.GPUs)
	}
	if _, ok := info.BestGPU(); ok {
		t.Fatal("BestGPU deveria dizer que não há placa")
	}
	if !strings.Contains(strings.Join(info.Lines(), "\n"), "nenhuma encontrada") {
		t.Fatalf("o resumo deveria dizer que não há placa: %v", info.Lines())
	}
}

func TestMacAppleSilicon(t *testing.T) {
	d := fakeDeps("darwin", "arm64", nil, map[string]string{"sysctl": "34359738368\n"}) // 32 GiB
	info := Detect(context.Background(), d)
	if info.RAMTotal != 32<<30 || len(info.GPUs) != 1 || !info.GPUs[0].Unified {
		t.Fatalf("Mac arm64 errado: %+v", info)
	}
	// 2/3 de 32 GiB menos a folga
	want := uint64(32<<30)/3*2 - Buffer
	if got := info.GPUs[0].Usable(); got != want {
		t.Fatalf("Usable = %d, queria %d", got, want)
	}
}

func TestMacIntelNaoInventaPlaca(t *testing.T) {
	d := fakeDeps("darwin", "amd64", nil, map[string]string{"sysctl": "17179869184"})
	info := Detect(context.Background(), d)
	if len(info.GPUs) != 0 || info.RAMTotal != 16<<30 {
		t.Fatalf("Mac Intel: %+v", info)
	}
}

func TestSistemaDesconhecidoAvisa(t *testing.T) {
	d := fakeDeps("windows", "amd64", nil, nil)
	info := Detect(context.Background(), d)
	if info.RAMTotal != 0 || len(info.Notes) == 0 {
		t.Fatalf("no Windows a RAM não é medida e deve haver aviso: %+v", info)
	}
}

func TestUsableNaoDaVoltaNoUnsigned(t *testing.T) {
	g := GPU{Total: 1 * GiB, Used: 3 * GiB, UsedKnown: true}
	if g.Usable() != 0 {
		t.Fatalf("Usable deve parar em 0, veio %d", g.Usable())
	}
}

func TestBestGPUEscolheAComMaisEspaco(t *testing.T) {
	info := Info{GPUs: []GPU{
		{Name: "pequena", Total: 4 * GiB},
		{Name: "grande, mas ocupada", Total: 12 * GiB, Used: 10 * GiB, UsedKnown: true},
		{Name: "média livre", Total: 8 * GiB},
	}}
	g, ok := info.BestGPU()
	if !ok || g.Name != "média livre" {
		t.Fatalf("BestGPU = %+v", g)
	}
}

func TestMeminfoLixo(t *testing.T) {
	total, avail := parseMeminfo([]byte("lixo\nMemTotal: abc kB\nMemAvailable: 10 kB\n"))
	if total != 0 || avail != 10*1024 {
		t.Fatalf("total=%d avail=%d", total, avail)
	}
}

func TestFormatGiBUsaVirgula(t *testing.T) {
	if got := FormatGiB(8.1); got != "8,1" {
		t.Fatalf("got %q", got)
	}
	if got := FormatGiB(0); got != "0,0" {
		t.Fatalf("got %q", got)
	}
	info := Info{CPUCores: 4, RAMTotal: 16 * GiB}
	if l := strings.Join(info.Lines(), "\n"); !strings.Contains(l, "16,0 GiB") || strings.Contains(l, "16.0") {
		t.Fatalf("o resumo deve usar vírgula: %q", l)
	}
}
