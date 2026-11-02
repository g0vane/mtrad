// prescript: pre-script de teste. Lê .jar (e jars aninhados declarados no
// fabric.mod.json), encontra assets/<ns>/lang/en_us.json e pt_br.json e conta
// as chaves. Não escreve nada nos jars. Só biblioteca padrão.
//
// Uso: prescript [-out strings.jsonl] <jar ou pasta> ...
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxLang   = 64 << 20  // maior arquivo de idioma aceito
	maxNested = 512 << 20 // maior jar aninhado lido na memória
	maxDepth  = 5
)

var warnings []string

var langRe = regexp.MustCompile(`^assets/([^/]+)/lang/([^/]+)\.json$`)

type kv struct {
	Key  string
	Text string
}

type langInfo struct {
	Keys      []kv
	NonString int
	Dups      int
	Lenient   bool
	Err       string
}

type entry struct {
	Chain   string // jar > jar aninhado
	ModID   string
	Version string
	NS      string
	En      *langInfo
	Pt      *langInfo
}

type fabricMod struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Jars    []struct {
		File string `json:"file"`
	} `json:"jars"`
}

func main() {
	out := flag.String("out", "", "grava as strings em JSONL")
	flag.Parse()
	var jars []string
	for _, a := range flag.Args() {
		st, err := os.Stat(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if st.IsDir() {
			m, _ := filepath.Glob(filepath.Join(a, "*.jar"))
			sort.Strings(m)
			jars = append(jars, m...)
		} else {
			jars = append(jars, a)
		}
	}
	var all []entry
	for _, j := range jars {
		zr, err := zip.OpenReader(j)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", j, err)
			continue
		}
		all = append(all, scan(&zr.Reader, filepath.Base(j), 0)...)
		zr.Close()
	}
	var w *json.Encoder
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer f.Close()
		w = json.NewEncoder(f)
	}
	var tEn, tPt, tHave, tSame int
	fmt.Printf("%-34s %-14s %-12s %6s %6s %6s %6s\n", "jar", "mod", "ns", "en", "pt_br", "tem", "igual")
	for _, e := range all {
		if e.En == nil {
			continue
		}
		pt := map[string]string{}
		if e.Pt != nil {
			for _, k := range e.Pt.Keys {
				pt[k.Key] = k.Text
			}
		}
		have, same := 0, 0
		for _, k := range e.En.Keys {
			if p, ok := pt[k.Key]; ok {
				have++
				if p == k.Text {
					same++
				}
			}
			if w != nil {
				_ = w.Encode(map[string]string{"jar": e.Chain, "mod": e.ModID, "ns": e.NS, "key": k.Key, "en": k.Text, "pt": pt[k.Key]})
			}
		}
		ptn := 0
		if e.Pt != nil {
			ptn = len(e.Pt.Keys)
		}
		tEn += len(e.En.Keys)
		tPt += ptn
		tHave += have
		tSame += same
		note := ""
		if e.En.Lenient {
			note += " [json tolerante]"
		}
		if e.En.NonString > 0 {
			note += fmt.Sprintf(" [%d valores não texto]", e.En.NonString)
		}
		if e.En.Dups > 0 {
			note += fmt.Sprintf(" [%d chaves duplicadas]", e.En.Dups)
		}
		if e.En.Err != "" {
			note += " [ERRO en_us: " + e.En.Err + "]"
		}
		fmt.Printf("%-34s %-14s %-12s %6d %6d %6d %6d%s\n", trunc(e.Chain, 34), trunc(e.ModID, 14), trunc(e.NS, 12), len(e.En.Keys), ptn, have, same, note)
	}
	fmt.Printf("TOTAL en=%d pt_br=%d tem=%d igual_ao_en=%d\n", tEn, tPt, tHave, tSame)
	for _, w := range warnings {
		fmt.Println(w)
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}

func scan(zr *zip.Reader, chain string, depth int) []entry {
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	var mod fabricMod
	if f, ok := files["fabric.mod.json"]; ok {
		if b, err := readAll(f, maxLang); err == nil {
			if li, _ := parseLang(b); li != nil {
				_ = json.Unmarshal(clean(b), &mod)
			}
		}
	}
	byNS := map[string]*entry{}
	get := func(ns string) *entry {
		if e, ok := byNS[ns]; ok {
			return e
		}
		e := &entry{Chain: chain, ModID: mod.ID, Version: mod.Version, NS: ns}
		byNS[ns] = e
		return e
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m := langRe.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		loc := strings.ToLower(m[2])
		if loc != "en_us" && loc != "pt_br" {
			continue
		}
		b, err := readAll(files[n], maxLang)
		li, _ := parseLang(b)
		if err != nil {
			li = &langInfo{Err: err.Error()}
		}
		e := get(m[1])
		if loc == "en_us" {
			e.En = li
		} else {
			e.Pt = li
		}
	}
	var res []entry
	var nsList []string
	for ns := range byNS {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)
	for _, ns := range nsList {
		res = append(res, *byNS[ns])
	}
	// jars aninhados: seguir o campo jars
	declared := map[string]bool{}
	var missing []string
	if depth < maxDepth {
		for _, j := range mod.Jars {
			declared[j.File] = true
			f, ok := files[j.File]
			if !ok {
				missing = append(missing, j.File)
				continue
			}
			b, err := readAll(f, maxNested)
			if err != nil {
				continue
			}
			nz, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				continue
			}
			res = append(res, scan(nz, chain+" > "+filepath.Base(j.File), depth+1)...)
		}
	}
	var orphans []string
	for _, n := range names {
		if strings.HasSuffix(n, ".jar") && !declared[n] && strings.HasPrefix(n, "META-INF/jars/") {
			orphans = append(orphans, n)
		}
	}
	for _, o := range orphans {
		warnings = append(warnings, fmt.Sprintf("AVISO %s: jar aninhado não declarado em jars: %s", chain, o))
	}
	for _, o := range missing {
		warnings = append(warnings, fmt.Sprintf("AVISO %s: jars declara %s mas o arquivo não existe", chain, o))
	}
	return res
}

func readAll(f *zip.File, limit int64) ([]byte, error) {
	if int64(f.UncompressedSize64) > limit {
		return nil, fmt.Errorf("%s: maior que o limite de %d bytes", f.Name, limit)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// clean remove BOM, comentários e vírgulas finais (JSON "tolerante").
func clean(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var out bytes.Buffer
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(b) {
				i++
				out.WriteByte(b[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	s := out.Bytes()
	// vírgula final antes de } ou ]
	var fin bytes.Buffer
	inStr = false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			fin.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				fin.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		fin.WriteByte(c)
	}
	return fin.Bytes()
}

// parseLang lê um objeto JSON de nível único preservando a ordem.
func parseLang(b []byte) (*langInfo, error) {
	li := &langInfo{}
	try := func(data []byte) error {
		dec := json.NewDecoder(bytes.NewReader(data))
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); !ok || d != '{' {
			return fmt.Errorf("raiz não é objeto")
		}
		seen := map[string]bool{}
		li.Keys = nil
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			key := kt.(string)
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return err
			}
			var s string
			if json.Unmarshal(raw, &s) != nil {
				li.NonString++
				continue
			}
			if seen[key] {
				li.Dups++
				for i := range li.Keys {
					if li.Keys[i].Key == key {
						li.Keys[i].Text = s
					}
				}
				continue
			}
			seen[key] = true
			li.Keys = append(li.Keys, kv{key, s})
		}
		_, err = dec.Token()
		return err
	}
	if err := try(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))); err != nil {
		li = &langInfo{Lenient: true}
		if err2 := try(clean(b)); err2 != nil {
			li.Err = err2.Error()
			return li, err2
		}
	}
	return li, nil
}
