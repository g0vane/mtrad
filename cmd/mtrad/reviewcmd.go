package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mtrad/internal/glossary"
	"mtrad/internal/review"
	"mtrad/internal/schema"
	"mtrad/internal/validate"
)

const reviewUsage = `uso: mtrad review [-w PASTA] <ação> ...

  export [-o ARQ.csv] [--scope failed|warnings|all]
        grava a lista do que precisa de revisão (padrão: só os reprovados) num
        CSV; preencha a coluna new_pt com a tradução certa, no Excel/Calc ou à mão
  import ARQ.csv     lê o CSV e guarda as linhas com new_pt preenchida como
                     correções manuais (work/review.jsonl)
  set ID TEXTO       corrige um texto só
  drop ID...         desfaz correções manuais
  status             mostra quantas correções existem

Depois de importar, rode "mtrad validate" e "mtrad pack": o texto corrigido entra
no pacote sem ser refeito. A correção vale enquanto o texto original não mudar.
`

func stageReview(w io.Writer, work string, args []string) error {
	if len(args) == 0 {
		io.WriteString(os.Stderr, reviewUsage)
		return exitErr{schema.ExitUsage, "review: falta a ação (export, import, set, drop, status)"}
	}
	act, rest := args[0], args[1:]
	switch act {
	case "export":
		fs := flag.NewFlagSet("review export", flag.ContinueOnError)
		out := fs.String("o", filepath.Join(work, "review.csv"), "arquivo CSV de saída")
		scope := fs.String("scope", "failed", "failed, warnings ou all")
		if err := fs.Parse(rest); err != nil {
			return exitErr{schema.ExitUsage, ""}
		}
		if *scope != "failed" && *scope != "warnings" && *scope != "all" {
			return exitErr{schema.ExitUsage, "--scope: use failed, warnings ou all"}
		}
		var g *glossary.Glossary
		if gl, err := glossary.Load(filepath.Join(work, "glossary.json")); err == nil {
			g = gl
		}
		items, err := validate.ReviewItems(work, validate.Options{Gloss: g}, *scope)
		if err != nil {
			return err
		}
		var buf strings.Builder
		if err := review.WriteCSV(&buf, items); err != nil {
			return err
		}
		if err := writeFileAtomic(*out, []byte(buf.String())); err != nil {
			return err
		}
		fmt.Fprintf(w, "review: %d textos em %s (preencha a coluna new_pt e rode `mtrad review import`)\n", len(items), *out)
		return nil
	case "import":
		if len(rest) != 1 {
			return exitErr{schema.ExitUsage, "review import: informe o arquivo CSV"}
		}
		f, err := os.Open(rest[0])
		if err != nil {
			return err
		}
		defer f.Close()
		rows, err := review.ReadCSV(f)
		if err != nil {
			return err
		}
		todo, err := todoShas(work)
		if err != nil {
			return err
		}
		var es []review.Entry
		unknown := 0
		for _, r := range rows {
			sha, ok := todo[r.ID]
			if !ok {
				unknown++
				fmt.Fprintf(w, "  linha %d: id %s não está em todo.jsonl (ignorada)\n", r.Line, r.ID)
				continue
			}
			es = append(es, review.Entry{ID: r.ID, Pt: r.NewPt, EnSha: sha, Note: "csv"})
		}
		if len(es) > 0 {
			if err := review.Append(work, es...); err != nil {
				return err
			}
		}
		fmt.Fprintf(w, "review: %d correções guardadas, %d ignoradas. Rode `mtrad validate` e `mtrad pack` para aplicar\n", len(es), unknown)
		return nil
	case "set":
		if len(rest) < 2 {
			return exitErr{schema.ExitUsage, "review set: informe ID e o texto"}
		}
		todo, err := todoShas(work)
		if err != nil {
			return err
		}
		sha, ok := todo[rest[0]]
		if !ok {
			return fmt.Errorf("id %s não está em todo.jsonl", rest[0])
		}
		if err := review.Append(work, review.Entry{ID: rest[0], Pt: strings.Join(rest[1:], " "), EnSha: sha, Note: "cli"}); err != nil {
			return err
		}
		fmt.Fprintln(w, "review: correção guardada; rode `mtrad validate` e `mtrad pack`")
		return nil
	case "drop":
		if len(rest) == 0 {
			return exitErr{schema.ExitUsage, "review drop: informe os IDs"}
		}
		// uma correção com texto vazio desfaz a anterior (a última de cada ID vale)
		var es []review.Entry
		for _, id := range rest {
			es = append(es, review.Entry{ID: id, Pt: "", Note: "drop"})
		}
		if err := review.Append(work, es...); err != nil {
			return err
		}
		fmt.Fprintf(w, "review: %d correções desfeitas\n", len(es))
		return nil
	case "status":
		m, err := review.Load(work)
		if err != nil {
			return err
		}
		n, stale := 0, 0
		todo, _ := todoShas(work)
		for id, e := range m {
			if strings.TrimSpace(e.Pt) == "" {
				continue
			}
			n++
			if sha, ok := todo[id]; ok && e.EnSha != "" && e.EnSha != sha {
				stale++
			}
		}
		fmt.Fprintf(w, "review: %d correções manuais (%d desatualizadas porque o original mudou)\n", n, stale)
		return nil
	}
	io.WriteString(os.Stderr, reviewUsage)
	return exitErr{schema.ExitUsage, "review: ação desconhecida: " + act}
}

// todoShas devolve o hash do texto original de cada ID de todo.jsonl.
func todoShas(work string) (map[string]string, error) {
	m := map[string]string{}
	err := schema.Each(filepath.Join(work, "todo.jsonl"), schema.VTodo, func(r schema.TodoRec) error {
		m[r.ID] = r.EnSha
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("não consegui ler todo.jsonl (rode a etapa diff antes): %w", err)
	}
	return m, nil
}
