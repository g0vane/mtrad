//go:build !linux

package doctor

import "errors"

func freeBytes(string) (uint64, error) {
	return 0, errors.New("não sei medir o espaço livre neste sistema")
}
