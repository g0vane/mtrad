//go:build unix

package schema

import (
	"errors"
	"syscall"
)

// pidAlive diz se há um processo com esse pid (kill com sinal 0 não envia nada).
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
