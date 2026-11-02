//go:build linux

package activate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// gameRunning procura, em /proc, um processo java cuja linha de comando cita a
// pasta do jogo (o Prism passa --gameDir ou usa a pasta como diretório de
// trabalho). Só vale para Linux; é uma conferência de boa-fé, por isso existe
// --force.
func gameRunning(gameDir string) bool {
	abs, err := filepath.Abs(gameDir)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	self := os.Getpid()
	for _, e := range entries {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		if pid := atoi(name); pid == self {
			continue
		}
		b, err := os.ReadFile("/proc/" + name + "/cmdline")
		if err != nil || len(b) == 0 {
			continue
		}
		args := strings.Split(string(bytes.TrimRight(b, "\x00")), "\x00")
		if len(args) == 0 || !strings.Contains(filepath.Base(args[0]), "java") {
			continue
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "net.minecraft") || strings.Contains(joined, "KnotClient") || strings.Contains(joined, "cpw.mods") || strings.Contains(joined, "--gameDir") {
			if strings.Contains(joined, abs) {
				return true
			}
			if cwd, err := os.Readlink("/proc/" + name + "/cwd"); err == nil && filepath.Clean(cwd) == abs {
				return true
			}
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
