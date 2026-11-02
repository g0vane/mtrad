// Package patchouli acha os livros do Patchouli dentro dos jars e extrai os
// textos deles. Cada arquivo do livro em en_us vira um "documento": uma cópia
// do original em work/docs/ e uma linha em docs.patchouli.jsonl. Cada texto
// vira uma linha em strings.patchouli.jsonl, com o caminho dele no documento.
//
// Onde fica o conteúdo (conferido no código do Patchouli, ramo 26.1):
// data/<ns>/patchouli_books/<livro>/book.json e
// assets/<ns>/patchouli_books/<livro>/<idioma>/{categories,entries,templates}/**.json.
// O livro só é lido em en_us; para cada arquivo o jogo usa o do idioma atual e,
// se não existir, o de en_us.
package patchouli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mtrad/internal/jarx"
	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

const Kind = "patchouli"

// Campos de texto, conferidos no código do Patchouli (ramo 26.1):
// entrada: name; categoria: name, description; páginas: title, name (entidade e
// multibloco), text, link_text; componentes de modelo: text. Uma string solta
// dentro de "pages" é atalho para página de texto (ClientBookRegistry.LexiconPageAdapter).
// Modelos (templates) podem dar nomes próprios às variáveis ("header",
// "flavor"...); esses campos NÃO são pegos.
var textKeys = map[string]bool{
	"name": true, "title": true, "text": true, "description": true, "link_text": true,
}

var (
	reAsset = regexp.MustCompile(`^assets/([^/]+)/patchouli_books/([^/]+)/en_us/((?:categories|entries|templates)/.+\.json)$`)
	reBook  = regexp.MustCompile(`^data/([^/]+)/patchouli_books/([^/]+)/book\.json$`)
	reOld   = regexp.MustCompile(`^data/([^/]+)/patchouli_books/([^/]+)/en_us/`)
	// variável de modelo ("#text", "#text#") e chave de idioma ("item.mod.nome",
	// "botania.page.spellCloth0", "mod.page.a/b/c", "mod.action.mod:nome"):
	// sem espaço, pelo menos um ponto seguido de letra ou número.
	reVar = regexp.MustCompile(`^#[\w.#]+$`)
	// variável de modelo no meio do texto ("$(n)#input#/$"): o modelo troca na hora
	reTplVar = regexp.MustCompile(`#[A-Za-z_][A-Za-z0-9_]*#`)
	reKey    = regexp.MustCompile(`^[A-Za-z0-9_\-]+(\.[A-Za-z0-9_\-/:]+)+$`)
)

type Sink interface {
	Add(src, id, msg string)
}

type Counts struct {
	Jars, Books, Docs, Keys, ExistingPt, Skipped, FileErrors int
}

// Out recebe o resultado.
type Out struct {
	Str func(schema.StringRec) error
	Doc func(schema.DocRec, []byte) error
}

func translatable(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && !reVar.MatchString(t) && !reKey.MatchString(t) && !reTplVar.MatchString(t)
}

type found struct {
	path string
	ns   string
	book string
	rest string // categories/x.json
}

func Extract(inv schema.Inventory, errs Sink, out Out) (Counts, error) {
	var c Counts
	seen := map[string]bool{}
	for _, ji := range inv.Jars {
		chain := append([]string(nil), ji.Chain...)
		if !filepath.IsAbs(chain[0]) {
			chain[0] = filepath.Join(inv.GameDir, filepath.FromSlash(chain[0]))
		}
		src := strings.Join(ji.Chain, " > ")
		mod := ji.ModID
		if mod == "" {
			mod = strings.TrimSuffix(filepath.Base(chain[len(chain)-1]), filepath.Ext(chain[len(chain)-1]))
		}
		j, err := jarx.Open(chain)
		if err != nil {
			// scan e extract já relataram jar ilegível; aqui só conta
			c.FileErrors++
			continue
		}
		names := j.Names()
		sort.Strings(names)
		var files []found
		books := map[string]bool{}
		i18n := map[string]bool{}
		warnedOld := map[string]bool{}
		for _, n := range names {
			if m := reAsset.FindStringSubmatch(n); m != nil {
				if !schema.SafeEntryName(n) {
					errs.Add(src, "", n+": nome de arquivo suspeito (caminho fora da pasta); ignorado")
					c.FileErrors++
					continue
				}
				files = append(files, found{n, m[1], m[2], m[3]})
				books[m[1]+"/"+m[2]] = true
			} else if m := reBook.FindStringSubmatch(n); m != nil {
				b, err := j.Read(n, jarx.MaxEntry)
				if err == nil {
					if d, err := relaxed.Parse(b); err == nil {
						if v := d.Get("/i18n"); v != nil && v.Kind == relaxed.Raw && v.Raw == "true" {
							i18n[m[1]+"/"+m[2]] = true
						}
					}
				}
			} else if m := reOld.FindStringSubmatch(n); m != nil && !warnedOld[m[1]+"/"+m[2]] {
				warnedOld[m[1]+"/"+m[2]] = true
				errs.Add(src, "", fmt.Sprintf("livro %s:%s tem conteúdo em data/ (Patchouli antigo, antes do 1.20); não suportado", m[1], m[2]))
				c.Skipped++
			}
		}
		if len(files) > 0 {
			c.Jars++
		}
		bookCount := map[string]bool{}
		for _, f := range files {
			bk := f.ns + "/" + f.book
			if i18n[bk] {
				if !bookCount[bk+"!"] {
					bookCount[bk+"!"] = true
					errs.Add(src, "", fmt.Sprintf("livro %s usa i18n: os textos vêm do arquivo de idioma, não dos JSON; pulado", bk))
					c.Skipped++
				}
				continue
			}
			b, err := j.Read(f.path, jarx.MaxEntry)
			if err != nil {
				errs.Add(src, "", f.path+": "+err.Error())
				c.FileErrors++
				continue
			}
			en, err := relaxed.Parse(b)
			if err != nil {
				errs.Add(src, "", f.path+": JSON inválido: "+err.Error())
				c.FileErrors++
				continue
			}
			ptPath := strings.Replace(f.path, "/en_us/", "/pt_br/", 1)
			var pt *relaxed.Node
			if j.Has(ptPath) {
				if pb, err := j.Read(ptPath, jarx.MaxEntry); err == nil {
					if p, err := relaxed.Parse(pb); err == nil {
						pt = p
					} else {
						errs.Add(src, "", ptPath+": JSON inválido: "+err.Error())
					}
				}
			}
			docID := schema.ID(mod, Kind, ptPath)
			if seen[docID] {
				errs.Add(src, "", f.path+": mesmo documento de outro jar; ignorado")
				continue
			}
			var recs []schema.StringRec
			collect(en, "", "", func(ptr, key string, s string) {
				ns := Kind + ":" + f.ns + "/" + f.book
				k := f.rest + "#" + ptr
				rec := schema.StringRec{
					ID: schema.ID(mod, ns, k), Mod: mod, NS: ns, Key: k,
					En: s, EnSha: schema.Sha(s), Src: src,
					Kind: Kind, Loc: &schema.Loc{Doc: ptPath, Ptr: ptr},
				}
				if pt != nil {
					if n := pt.Get(ptr); n != nil && n.Kind == relaxed.Str {
						v := n.Str
						rec.Pt = &v
					}
				}
				recs = append(recs, rec)
			})
			if len(recs) == 0 {
				continue
			}
			seen[docID] = true
			if !bookCount[bk] {
				bookCount[bk] = true
				c.Books++
			}
			if err := out.Doc(schema.DocRec{ID: docID, Kind: Kind, Mod: mod, Orig: "docs/" + docID + ".json", Out: ptPath, Format: "json", Src: src}, b); err != nil {
				j.Close()
				return c, err
			}
			c.Docs++
			for _, r := range recs {
				if r.Pt != nil {
					c.ExistingPt++
				}
				c.Keys++
				if err := out.Str(r); err != nil {
					j.Close()
					return c, err
				}
			}
		}
		j.Close()
	}
	return c, nil
}

// collect percorre a árvore e entrega cada texto de um campo traduzível.
func collect(n *relaxed.Node, ptr, key string, fn func(ptr, key, s string)) {
	switch n.Kind {
	case relaxed.Str:
		if (textKeys[key] || key == "pages") && translatable(n.Str) {
			fn(ptr, key, n.Str)
		}
	case relaxed.Obj:
		for _, p := range n.Pairs {
			collect(p.Val, relaxed.Child(ptr, p.Key), p.Key, fn)
		}
	case relaxed.Arr:
		for i, it := range n.Items {
			collect(it, relaxed.Child(ptr, fmt.Sprint(i)), key, fn)
		}
	}
}

// Run lê inventory.json e grava strings.patchouli.jsonl, docs.patchouli.jsonl e docs/.
func Run(workDir string, errs Sink) (Counts, error) {
	var inv schema.Inventory
	if err := schema.ReadJSON(filepath.Join(workDir, "inventory.json"), &inv, schema.VInventory); err != nil {
		return Counts{}, err
	}
	if _, err := schema.RequireFresh(workDir, "scan", "inventory.json"); err != nil {
		return Counts{}, err
	}
	sw, err := schema.Create(filepath.Join(workDir, "strings.patchouli.jsonl"), schema.VStrings)
	if err != nil {
		return Counts{}, err
	}
	dw, err := schema.Create(filepath.Join(workDir, "docs.patchouli.jsonl"), schema.VDocs)
	if err != nil {
		sw.Abort()
		return Counts{}, err
	}
	// documentos de uma execução anterior ficam velhos: começa limpo
	docsDir := filepath.Join(workDir, "docs", Kind)
	newDocs := docsDir + ".new" // os documentos entram aqui e só trocam os antigos no fim
	if err := os.RemoveAll(newDocs); err != nil {
		sw.Abort()
		dw.Abort()
		return Counts{}, err
	}
	c, err := Extract(inv, errs, Out{
		Str: func(r schema.StringRec) error { return sw.Write(r) },
		Doc: func(d schema.DocRec, orig []byte) error {
			d.Orig = "docs/" + Kind + "/" + d.ID + ".json"
			p := filepath.Join(newDocs, filepath.Base(filepath.FromSlash(d.Orig)))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, orig, 0o644); err != nil {
				return err
			}
			return dw.Write(d)
		},
	})
	if err != nil {
		sw.Abort()
		dw.Abort()
		os.RemoveAll(newDocs)
		return c, err
	}
	if _, serr := os.Stat(newDocs); serr == nil {
		err = schema.SwapDir(newDocs, docsDir)
	} else {
		err = os.RemoveAll(docsDir) // nenhum documento nesta execução
	}
	if err != nil {
		sw.Abort()
		dw.Abort()
		os.RemoveAll(newDocs)
		return c, err
	}
	if err := sw.Close(); err != nil {
		return c, err
	}
	if err := dw.Close(); err != nil {
		return c, err
	}
	err = schema.WriteDone(workDir, Kind, []string{"inventory.json"}, []string{"strings.patchouli.jsonl", "docs.patchouli.jsonl"},
		map[string]int{"jars": c.Jars, "books": c.Books, "docs": c.Docs, "keys": c.Keys, "existing_pt": c.ExistingPt, "skipped": c.Skipped, "file_errors": c.FileErrors})
	return c, err
}
