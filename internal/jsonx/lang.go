package jsonx

import (
	"strings"
)

// ParseLang lê um arquivo de idioma do formato antigo (.lang, até o Minecraft
// 1.12): uma linha "chave=valor" por texto, "#" começa um comentário. O
// Minecraft separa só no primeiro "=" e não desfaz escapes, então o valor é
// guardado como está (inclusive um "\n" escrito com barra, que o jogo mostra
// como o autor escreveu). Linhas sem "=" são ignoradas e contadas em NonString.
func ParseLang(b []byte) *Doc {
	d := &Doc{}
	s := strings.TrimPrefix(string(b), "\uFEFF")
	idx := map[string]int{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			d.NonString++
			continue
		}
		k = strings.TrimSpace(k)
		if i, dup := idx[k]; dup {
			d.Dups++
			d.Pairs[i].Text = v // a última vale
			continue
		}
		idx[k] = len(d.Pairs)
		d.Pairs = append(d.Pairs, Pair{Key: k, Text: v})
	}
	return d
}

// MarshalLang grava os pares no formato .lang, um por linha, em ordem. Quebras
// de linha reais no texto viram "\n" escrito, porque o formato não as comporta.
func MarshalLang(pairs []Pair) []byte {
	var b strings.Builder
	for _, p := range pairs {
		t := strings.ReplaceAll(strings.ReplaceAll(p.Text, "\r", ""), "\n", `\n`)
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
