// Package evalset (parte em Go do módulo eval) monta um conjunto de teste com
// textos que já têm tradução humana, para medir um motor. A pontuação em si é
// feita por tools/eval.py.
package evalset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode"

	"mtrad/internal/schema"
)

const VReference = "reference/1"

type ReferenceRec struct {
	ID  string `json:"id"`
	En  string `json:"en"`
	Pt  string `json:"pt"`
	Mod string `json:"mod"`
	NS  string `json:"ns"`
	Key string `json:"key"`
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// Build lê strings.jsonl de srcWork e cria em outWork o todo.jsonl (a traduzir)
// e o reference.jsonl (tradução humana) com até n textos escolhidos por um
// sorteio fixo (seed). Só entram textos com pt diferente do inglês.
func Build(srcWork, outWork string, n int, seed string) (int, error) {
	type cand struct {
		rec  schema.StringRec
		sort string
	}
	var cs []cand
	err := schema.Each(filepath.Join(srcWork, "strings.jsonl"), schema.VStrings, func(r schema.StringRec) error {
		if r.Pt == nil || *r.Pt == "" || *r.Pt == r.En || !hasLetter(r.En) {
			return nil
		}
		h := sha256.Sum256([]byte(seed + "\x00" + r.ID))
		cs = append(cs, cand{r, hex.EncodeToString(h[:])})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(cs) == 0 {
		return 0, fmt.Errorf("nenhum texto com tradução humana em %s", srcWork)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].sort < cs[j].sort })
	if n > 0 && len(cs) > n {
		cs = cs[:n]
	}
	if err := os.MkdirAll(outWork, 0o755); err != nil {
		return 0, err
	}
	tw, err := schema.Create(filepath.Join(outWork, "todo.jsonl"), schema.VTodo)
	if err != nil {
		return 0, err
	}
	rw, err := schema.Create(filepath.Join(outWork, "reference.jsonl"), VReference)
	if err != nil {
		tw.Abort()
		return 0, err
	}
	for _, c := range cs {
		r := c.rec
		if err := tw.Write(schema.TodoRec{ID: r.ID, Mod: r.Mod, NS: r.NS, Key: r.Key, Reason: "missing", En: r.En, EnSha: r.EnSha}); err != nil {
			tw.Abort()
			rw.Abort()
			return 0, err
		}
		if err := rw.Write(ReferenceRec{ID: r.ID, En: r.En, Pt: *r.Pt, Mod: r.Mod, NS: r.NS, Key: r.Key}); err != nil {
			tw.Abort()
			rw.Abort()
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := rw.Close(); err != nil {
		return 0, err
	}
	// o glossário acompanha, se existir
	if b, err := os.ReadFile(filepath.Join(srcWork, "glossary.json")); err == nil {
		_ = os.WriteFile(filepath.Join(outWork, "glossary.json"), b, 0o644)
	}
	return len(cs), nil
}
