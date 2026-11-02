// Package extract lê en_us.json e pt_br.json de cada jar do inventário e
// grava uma linha por chave em strings.jsonl.
package extract

import (
	"fmt"
	"path/filepath"
	"strings"

	"mtrad/internal/jarx"
	"mtrad/internal/jsonx"
	"mtrad/internal/schema"
)

type Sink interface {
	Add(src, id, msg string)
}

type Counts struct {
	Jars, Files, Keys, ExistingPt, Duplicates, FileErrors, KubeJS int
}

// Extract percorre o inventário e entrega cada texto a emit.
func Extract(inv schema.Inventory, errs Sink, emit func(schema.StringRec) error) (Counts, error) {
	var c Counts
	seen := map[string]string{} // id -> src
	for _, ji := range inv.Jars {
		hasEn := false
		for _, l := range ji.Langs {
			if l.Locale == "en_us" {
				hasEn = true
			}
		}
		if !hasEn {
			continue
		}
		src := strings.Join(ji.Chain, " > ")
		chain := append([]string(nil), ji.Chain...)
		if !filepath.IsAbs(chain[0]) {
			chain[0] = filepath.Join(inv.GameDir, filepath.FromSlash(chain[0]))
		}
		j, err := jarx.Open(chain)
		if err != nil {
			errs.Add(src, "", "não consegui abrir: "+err.Error())
			c.FileErrors++
			continue
		}
		c.Jars++
		enBy, ptBy := map[string]string{}, map[string]string{}
		var order []string
		for _, l := range ji.Langs {
			switch l.Locale {
			case "en_us":
				prev, has := enBy[l.NS]
				if !has {
					order = append(order, l.NS)
					enBy[l.NS] = l.Path
				} else if isLegacyLang(prev) && !isLegacyLang(l.Path) {
					enBy[l.NS] = l.Path // o .json vale mais que o .lang do mesmo namespace
				}
			case "pt_br":
				if prev, has := ptBy[l.NS]; !has || (isLegacyLang(prev) && !isLegacyLang(l.Path)) {
					ptBy[l.NS] = l.Path
				}
			}
		}
		mod := ji.ModID
		if mod == "" {
			mod = strings.TrimSuffix(filepath.Base(chain[len(chain)-1]), filepath.Ext(chain[len(chain)-1]))
		}
		for _, ns := range order {
			en, ok := readLang(j, enBy[ns], src, errs)
			if !ok {
				c.FileErrors++
				continue
			}
			c.Files++
			pt := map[string]string{}
			if p, has := ptBy[ns]; has {
				if d, ok := readLang(j, p, src, errs); ok {
					pt = d.Map()
				} else {
					c.FileErrors++
				}
			}
			dups := 0
			for _, kv := range en.Pairs {
				id := schema.ID(mod, ns, kv.Key)
				if prev, dup := seen[id]; dup {
					dups++
					_ = prev
					continue
				}
				seen[id] = src
				rec := schema.StringRec{ID: id, Mod: mod, NS: ns, Key: kv.Key, En: kv.Text, EnSha: schema.Sha(kv.Text), Src: src}
				if p, has := pt[kv.Key]; has {
					pp := p
					rec.Pt = &pp
					c.ExistingPt++
				}
				if err := emit(rec); err != nil {
					j.Close()
					return c, err
				}
				c.Keys++
			}
			if dups > 0 {
				c.Duplicates += dups
				errs.Add(src, "", fmt.Sprintf("%d chaves de %s já vieram de outro jar com o mesmo mod e namespace; ignoradas", dups, ns))
			}
		}
		j.Close()
	}
	if err := extractKubeJS(inv, errs, emit, seen, &c); err != nil {
		return c, err
	}
	return c, nil
}

// isLegacyLang diz se o arquivo é do formato antigo (.lang, até o 1.12).
func isLegacyLang(path string) bool { return strings.HasSuffix(strings.ToLower(path), ".lang") }

func readLang(j *jarx.Jar, path, src string, errs Sink) (*jsonx.Doc, bool) {
	b, err := j.Read(path, jarx.MaxEntry)
	if err != nil {
		errs.Add(src, "", path+": "+err.Error())
		return nil, false
	}
	var d *jsonx.Doc
	if isLegacyLang(path) {
		d = jsonx.ParseLang(b)
		if d.NonString > 0 {
			errs.Add(src, "", fmt.Sprintf("%s: %d linhas sem \"=\" foram ignoradas", path, d.NonString))
			d.NonString = 0
		}
	} else if d, err = jsonx.Parse(b); err != nil {
		errs.Add(src, "", path+": JSON inválido: "+err.Error())
		return nil, false
	}
	if d.NonString > 0 {
		errs.Add(src, "", fmt.Sprintf("%s: %d valores que não são texto foram ignorados", path, d.NonString))
	}
	if d.Dups > 0 {
		errs.Add(src, "", fmt.Sprintf("%s: %d chaves repetidas (a última vale)", path, d.Dups))
	}
	return d, true
}

// Run lê inventory.json e grava strings.jsonl.
func Run(workDir string, errs Sink) (Counts, error) {
	var inv schema.Inventory
	if err := schema.ReadJSON(filepath.Join(workDir, "inventory.json"), &inv, schema.VInventory); err != nil {
		return Counts{}, err
	}
	if _, err := schema.RequireFresh(workDir, "scan", "inventory.json"); err != nil {
		return Counts{}, err
	}
	w, err := schema.Create(filepath.Join(workDir, "strings.jsonl"), schema.VStrings)
	if err != nil {
		return Counts{}, err
	}
	c, err := Extract(inv, errs, func(r schema.StringRec) error { return w.Write(r) })
	if err != nil {
		w.Abort()
		return c, err
	}
	if err := w.Close(); err != nil {
		return c, err
	}
	err = schema.WriteDone(workDir, "extract", []string{"inventory.json"}, []string{"strings.jsonl"},
		map[string]int{"jars": c.Jars, "files": c.Files, "keys": c.Keys, "existing_pt": c.ExistingPt, "duplicates": c.Duplicates, "file_errors": c.FileErrors, "kubejs_files": c.KubeJS})
	return c, err
}
