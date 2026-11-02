//go:build !unix

package schema

// pidAlive: fora do Unix não há como conferir com segurança; a trava vale
// sempre (use `mtrad unlock` se ela sobrar).
func pidAlive(pid int) bool { return true }
