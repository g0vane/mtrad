package protect

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// MTRAD_CORPUS aponta para um JSONL com o campo "en" (saída do prescript).
func TestCorpusRoundTrip(t *testing.T) {
	p := os.Getenv("MTRAD_CORPUS")
	if p == "" {
		t.Skip("MTRAD_CORPUS não definido")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	n, withTok, bad := 0, 0, 0
	kinds := map[string]int{}
	for sc.Scan() {
		var r struct{ En string }
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		n++
		m, toks := Mask(r.En, StyleBrackets)
		if len(toks) > 0 {
			withTok++
		}
		for _, tk := range toks {
			k := tk.Raw
			if len(k) > 2 {
				k = k[:2]
			}
			kinds[k]++
		}
		back, pr := Unmask(m, toks, StyleBrackets)
		if back != r.En || len(pr) > 0 {
			bad++
			t.Errorf("falhou: %q -> %q %v", r.En, back, pr)
		}
	}
	type kc struct {
		k string
		n int
	}
	var l []kc
	for k, c := range kinds {
		l = append(l, kc{k, c})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
	if len(l) > 12 {
		l = l[:12]
	}
	t.Logf("textos=%d com marcas=%d falhas=%d; tipos mais comuns: %v", n, withTok, bad, l)
}
