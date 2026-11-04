// Package fmtx formata números e durações como o mtrad os mostra (português,
// com vírgula decimal). Só biblioteca padrão.
package fmtx

import (
	"fmt"
	"strings"
	"time"
)

// Decimal1 escreve um número com uma casa e vírgula decimal ("7,9").
func Decimal1(f float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1)
}

// Duration escreve uma duração para gente ler: "45 s", "12 min 04 s",
// "1 h 05 min". Arredonda para o segundo; valores negativos viram "0 s".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64((d + 500*time.Millisecond) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min %02d s", s/60, s%60)
	}
	return fmt.Sprintf("%d h %02d min", s/3600, s%3600/60)
}

// Ms escreve milissegundos de um pedido: "850 ms" ou "1,4 s".
func Ms(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return Decimal1(d.Seconds()) + " s"
}
