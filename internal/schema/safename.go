package schema

import (
	"path"
	"strings"
)

// SafeEntryName diz se um nome de arquivo vindo de dentro de um jar (ou o
// caminho que o mtrad monta a partir dele) pode virar uma entrada de zip ou um
// caminho relativo sem escapar da pasta: sem \, sem barra no começo, sem letra
// de unidade, sem NUL, sem "." nem ".." como parte, sem "//" e já na forma
// limpa. Nomes de jars não são de confiança.
func SafeEntryName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	if len(name) >= 2 && name[1] == ':' { // C:foo
		return false
	}
	if path.Clean(name) != name {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
