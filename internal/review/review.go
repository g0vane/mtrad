// Package review guarda as correções feitas à mão (work/review.jsonl) e
// converte a lista de textos a revisar de e para CSV. A validação aplica essas
// correções por cima da saída do modelo, então o que o usuário corrigiu não é
// refeito nem reprovado de novo.
package review

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mtrad/internal/schema"
)

const VReview = "review/1"

// Entry é uma tradução feita à mão. Pt é o texto final, com as marcas do jogo
// (&6, §a, %s...) como aparecem no original, sem as marcas ⟦n⟧ do mtrad.
type Entry struct {
	ID string `json:"id"`
	Pt string `json:"pt"`
	// EnSha é o hash do texto original no momento da correção; se o original
	// mudar depois, a correção deixa de valer.
	EnSha string `json:"en_sha"`
	Note  string `json:"note,omitempty"`
	Time  string `json:"time"`
}

func Path(work string) string { return filepath.Join(work, "review.jsonl") }

// Load devolve as correções por ID; a última de cada ID vale. Sem arquivo,
// devolve um mapa vazio.
func Load(work string) (map[string]Entry, error) {
	m := map[string]Entry{}
	err := schema.Each(Path(work), VReview, func(e Entry) error { m[e.ID] = e; return nil })
	if err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		return nil, err
	}
	return m, nil
}

// Append acrescenta uma correção.
func Append(work string, es ...Entry) error {
	a, err := schema.OpenAppend(Path(work), VReview)
	if err != nil {
		return err
	}
	defer a.Close()
	now := time.Now().Format(time.RFC3339)
	for _, e := range es {
		if e.Time == "" {
			e.Time = now
		}
		if err := a.Write(e); err != nil {
			return err
		}
	}
	return nil
}

// Item é uma linha da lista de revisão.
type Item struct {
	ID      string
	Status  string // reprovado, aviso, pendente
	Mod     string
	Key     string
	Reasons []string
	En      string
	Current string // tradução atual, já com as marcas do jogo
}

var csvHeader = []string{"id", "status", "mod", "key", "reasons", "en", "current", "new_pt"}

// WriteCSV grava a lista; a coluna new_pt vai vazia para ser preenchida.
func WriteCSV(w io.Writer, items []Item) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, it := range items {
		if err := cw.Write([]string{it.ID, it.Status, it.Mod, it.Key, strings.Join(it.Reasons, " | "), it.En, it.Current, ""}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// Row é uma correção lida do CSV.
type Row struct {
	ID, NewPt string
	Line      int
}

// ReadCSV lê um CSV com as colunas id e new_pt (as outras são ignoradas). A
// separação por vírgula ou ponto e vírgula (planilha em português) é
// reconhecida pelo cabeçalho. Linhas com new_pt vazio são puladas.
func ReadCSV(r io.Reader) ([]Row, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	txt := strings.TrimPrefix(string(b), "\uFEFF") // BOM da planilha
	first := txt
	if i := strings.IndexByte(txt, '\n'); i >= 0 {
		first = txt[:i]
	}
	cr := csv.NewReader(strings.NewReader(txt))
	if strings.Count(first, ";") > strings.Count(first, ",") {
		cr.Comma = ';'
	}
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("CSV sem cabeçalho: %w", err)
	}
	idCol, ptCol := -1, -1
	for i, h := range head {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "id":
			idCol = i
		case "new_pt":
			ptCol = i
		}
	}
	if idCol < 0 || ptCol < 0 {
		return nil, errors.New("o CSV precisa das colunas id e new_pt")
	}
	var rows []Row
	line := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("linha %d: %w", line, err)
		}
		if idCol >= len(rec) || ptCol >= len(rec) {
			continue
		}
		id, pt := strings.TrimSpace(rec[idCol]), strings.TrimRight(rec[ptCol], "\r\n")
		if id == "" || strings.TrimSpace(pt) == "" {
			continue
		}
		rows = append(rows, Row{ID: id, NewPt: pt, Line: line})
	}
}
