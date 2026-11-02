package validate

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"mtrad/internal/schema"
)

type engStat struct{ ok, failed int }

type sample struct{ key, en, out, why string }

type report struct {
	byEngine map[string]*engStat
	reasons  map[string]int
	warnKind map[string]int
	failEx   []sample
	warnEx   []sample
}

func newReport() *report {
	return &report{byEngine: map[string]*engStat{}, reasons: map[string]int{}, warnKind: map[string]int{}}
}

// reasonKind reduz um motivo detalhado a uma categoria para a contagem.
func reasonKind(s string) string {
	for _, k := range []string{"marca perdida", "marca repetida", "marca desconhecida", "marcas diferentes", "tamanho suspeito", "texto vazio", "caractere inválido", "parece explicação", "bloco de código", "texto entre aspas"} {
		if strings.HasPrefix(s, k) {
			return k
		}
	}
	return s
}

func (r *report) add(engine string, t schema.TodoRec, rawOut, pt string, fails, warns []string) {
	e := r.byEngine[engine]
	if e == nil {
		e = &engStat{}
		r.byEngine[engine] = e
	}
	if len(fails) > 0 {
		e.failed++
		seen := map[string]bool{}
		for _, f := range fails {
			k := reasonKind(f)
			if !seen[k] {
				r.reasons[k]++
				seen[k] = true
			}
		}
		if len(r.failEx) < 15 {
			r.failEx = append(r.failEx, sample{t.NS + ":" + t.Key, t.En, rawOut, strings.Join(fails, "; ")})
		}
	} else {
		e.ok++
	}
	for _, w := range warns {
		k := w
		if i := strings.Index(w, ":"); i > 0 {
			k = w[:i]
		}
		r.warnKind[k]++
		if len(r.warnEx) < 10 {
			r.warnEx = append(r.warnEx, sample{t.NS + ":" + t.Key, t.En, pt, w})
		}
	}
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

func (r *report) render(c Counts) string {
	var b strings.Builder
	b.WriteString("# Relatório de validação\n\n")
	fmt.Fprintf(&b, "- Textos a traduzir: %d\n- Aprovados: %d\n- Reprovados: %d\n- Ainda sem tradução: %d\n- Com aviso: %d\n\n", c.Todo, c.OK, c.Failed, c.Pending, c.Warnings)
	b.WriteString("## Por motor\n\n| Motor | Aprovados | Reprovados |\n| --- | --- | --- |\n")
	var names []string
	for n := range r.byEngine {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", n, r.byEngine[n].ok, r.byEngine[n].failed)
	}
	if len(r.reasons) > 0 {
		b.WriteString("\n## Motivos de reprovação\n\n| Motivo | Textos |\n| --- | --- |\n")
		for _, k := range sortedKeys(r.reasons) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, r.reasons[k])
		}
	}
	if len(r.warnKind) > 0 {
		b.WriteString("\n## Avisos (não impedem o uso)\n\n| Aviso | Textos |\n| --- | --- |\n")
		for _, k := range sortedKeys(r.warnKind) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, r.warnKind[k])
		}
	}
	writeSamples(&b, "Exemplos de reprovação", r.failEx)
	writeSamples(&b, "Exemplos de aviso", r.warnEx)
	return b.String()
}

func writeSamples(b *strings.Builder, title string, s []sample) {
	if len(s) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n| Chave | Original | Saída | Motivo |\n| --- | --- | --- | --- |\n", title)
	for _, x := range s {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", esc(x.key), esc(x.en), esc(x.out), esc(x.why))
	}
}

func sortedKeys(m map[string]int) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Slice(k, func(i, j int) bool {
		if m[k[i]] != m[k[j]] {
			return m[k[i]] > m[k[j]]
		}
		return k[i] < k[j]
	})
	return k
}

func writeFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
