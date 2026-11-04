package main

import (
	"fmt"

	"mtrad/internal/fmtx"
	"mtrad/internal/translate"
)

// progressLine é a linha de andamento da tradução no terminal: textos feitos,
// tempo decorrido, velocidade e o que falta.
func progressLine(done, total int, r translate.Rate) string {
	s := fmt.Sprintf("  traduzidos %d de %d · %s decorridos", done, total, fmtx.Duration(r.Elapsed))
	if r.WordsPerSec > 0 {
		s += " · " + fmtx.Decimal1(r.WordsPerSec) + " palavras/s"
	}
	if r.RemainingKnown && r.Remaining > 0 {
		s += " · faltam ~" + fmtx.Duration(r.Remaining)
	}
	return s
}
