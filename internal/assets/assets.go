// Package assets guarda dentro do binário os arquivos que o mtrad precisa em
// tempo de execução, para que ele funcione sozinho, sem a pasta do código.
package assets

import (
	_ "embed"
	"os"
	"path/filepath"
)

// CT2Worker é o tradutor neural em CPU (processo filho do motor "worker").
//
//go:embed ct2worker.py
var CT2Worker []byte

// WriteIfMissing grava data em path (criando as pastas) só se o arquivo não
// existir, para nunca sobrescrever uma cópia que o usuário tenha editado.
// Devolve true se gravou.
func WriteIfMissing(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return false, err
	}
	return true, nil
}
