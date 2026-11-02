// Package ftbq extrai os textos das missões do FTB Quests.
//
// Onde ficam (conferido no código e no CHANGELOG do FTB Quests):
//   - 1.21 até 2111.x: config/ftbquests/quests/lang/<idioma>.snbt
//   - 26.1.2.1 em diante: config/ftbquests/quests/lang/<idioma>/*.json5
//   - 1.20.1 e antes: texto dentro dos arquivos de capítulo (ver inline.go): cada
//     texto vira "{chave}" no capítulo e a chave vai para o resource pack.
//
// O arquivo é um mapa simples: chave -> texto, ou chave -> lista de textos
// (quest_desc, chapter_subtitle). Cada texto vira uma linha em
// strings.ftbq.jsonl; o resultado vai para work/out/ftbquests/, com o caminho
// relativo à pasta do jogo, e o install copia para lá.
package ftbq

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

const (
	Kind    = "ftbq"
	ModName = "ftbquests"
	// relativo à pasta do jogo
	QuestsDir = "config/ftbquests/quests"
)

type Sink interface {
	Add(src, id, msg string)
}

type Counts struct {
	Files, Keys, ExistingPt, Skipped, FileErrors int
	Era                                          string // "", "snbt", "json5", "inline"
}

type Out struct {
	Str func(schema.StringRec) error
	Doc func(schema.DocRec, []byte) error
}

type source struct {
	rel string // relativo à pasta do jogo, com /
	pt  string // caminho do pt_br existente (pode ser vazio)
	out string
}

func fmtOf(name string) (string, relaxed.Format) {
	switch strings.ToLower(path.Ext(name)) {
	case ".snbt":
		return "snbt", relaxed.SNBT
	case ".json":
		return "json", relaxed.JSON
	}
	return "json5", relaxed.JSON
}

// find acha os arquivos en_us da pasta do jogo.
func find(game string, errs Sink, c *Counts) []source {
	lang := filepath.Join(game, filepath.FromSlash(QuestsDir), "lang")
	var out []source
	if st, err := os.Stat(filepath.Join(lang, "en_us.snbt")); err == nil && st.Mode().IsRegular() {
		c.Era = "snbt"
		s := source{rel: QuestsDir + "/lang/en_us.snbt", out: QuestsDir + "/lang/pt_br.snbt"}
		if _, err := os.Stat(filepath.Join(lang, "pt_br.snbt")); err == nil {
			s.pt = QuestsDir + "/lang/pt_br.snbt"
		}
		return append(out, s)
	}
	dir := filepath.Join(lang, "en_us")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		c.Era = "json5"
		var names []string
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				switch strings.ToLower(filepath.Ext(p)) {
				case ".json5", ".json", ".snbt":
					r, _ := filepath.Rel(dir, p)
					names = append(names, filepath.ToSlash(r))
				}
			}
			return nil
		})
		sort.Strings(names)
		for _, n := range names {
			s := source{rel: QuestsDir + "/lang/en_us/" + n, out: QuestsDir + "/lang/pt_br/" + n}
			if _, err := os.Stat(filepath.Join(lang, "pt_br", filepath.FromSlash(n))); err == nil {
				s.pt = s.out
			}
			out = append(out, s)
		}
		return out
	}
	if ents, err := os.ReadDir(filepath.Join(game, filepath.FromSlash(QuestsDir), "chapters")); err == nil && len(ents) > 0 {
		c.Era = "inline"
	}
	return nil
}

type nopSink struct{}

func (nopSink) Add(string, string, string) {}

// Era diz como o FTB Quests guarda os textos na pasta do jogo: "snbt", "json5",
// "inline" (versões antigas, sem suporte) ou "" (não há FTB Quests).
func Era(game string) string {
	var c Counts
	find(game, nopSink{}, &c)
	return c.Era
}

func isText(s string) bool { return strings.TrimSpace(s) != "" }

// Extract lê as pastas de missões de game e entrega textos e documentos.
func Extract(game string, errs Sink, out Out) (Counts, error) {
	var c Counts
	srcs := find(game, errs, &c)
	if c.Era == "inline" {
		return c, extractInline(game, errs, out, &c)
	}
	for _, s := range srcs {
		b, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(s.rel)))
		if err != nil {
			errs.Add(s.rel, "", err.Error())
			c.FileErrors++
			continue
		}
		en, err := relaxed.Parse(b)
		if err != nil {
			errs.Add(s.rel, "", "arquivo inválido: "+err.Error())
			c.FileErrors++
			continue
		}
		if en.Kind != relaxed.Obj {
			errs.Add(s.rel, "", "esperava um mapa de chave para texto")
			c.FileErrors++
			continue
		}
		if en.Dups > 0 {
			errs.Add(s.rel, "", fmt.Sprintf("%d chaves repetidas (a última vale)", en.Dups))
		}
		var pt *relaxed.Node
		if s.pt != "" {
			if pb, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(s.pt))); err == nil {
				if p, err := relaxed.Parse(pb); err == nil {
					pt = p
				} else {
					errs.Add(s.pt, "", "pt_br inválido, ignorado: "+err.Error())
				}
			}
		}
		format, _ := fmtOf(s.out)
		docID := schema.ID(ModName, Kind, s.out)
		var recs []schema.StringRec
		en.Walk(func(ptr string, n *relaxed.Node) {
			if !isText(n.Str) {
				return
			}
			key := s.rel + "#" + ptr
			rec := schema.StringRec{
				ID: schema.ID(ModName, ModName, key), Mod: ModName, NS: ModName, Key: key,
				En: n.Str, EnSha: schema.Sha(n.Str), Src: s.rel,
				Kind: Kind, Loc: &schema.Loc{Doc: s.out, Ptr: ptr},
			}
			if pt != nil {
				if p := pt.Get(ptr); p != nil && p.Kind == relaxed.Str {
					v := p.Str
					rec.Pt = &v
				}
			}
			recs = append(recs, rec)
		})
		if len(recs) == 0 {
			continue
		}
		c.Files++
		if err := out.Doc(schema.DocRec{ID: docID, Kind: Kind, Mod: ModName, Orig: "", Out: s.out, Format: format, Src: s.rel}, b); err != nil {
			return c, err
		}
		for _, r := range recs {
			if r.Pt != nil {
				c.ExistingPt++
			}
			c.Keys++
			if err := out.Str(r); err != nil {
				return c, err
			}
		}
	}
	return c, nil
}

// Run lê inventory.json (para achar a pasta do jogo) e grava strings.ftbq.jsonl,
// docs.ftbq.jsonl e docs/ftbq/.
func Run(workDir string, errs Sink) (Counts, error) {
	var inv schema.Inventory
	if err := schema.ReadJSON(filepath.Join(workDir, "inventory.json"), &inv, schema.VInventory); err != nil {
		return Counts{}, err
	}
	if _, err := schema.RequireFresh(workDir, "scan", "inventory.json"); err != nil {
		return Counts{}, err
	}
	sw, err := schema.Create(filepath.Join(workDir, "strings.ftbq.jsonl"), schema.VStrings)
	if err != nil {
		return Counts{}, err
	}
	dw, err := schema.Create(filepath.Join(workDir, "docs.ftbq.jsonl"), schema.VDocs)
	if err != nil {
		sw.Abort()
		return Counts{}, err
	}
	docsDir := filepath.Join(workDir, "docs", Kind)
	newDocs := docsDir + ".new" // os documentos entram aqui e só trocam os antigos no fim
	if err := os.RemoveAll(newDocs); err != nil {
		sw.Abort()
		dw.Abort()
		return Counts{}, err
	}
	c, err := Extract(inv.GameDir, errs, Out{
		Str: func(r schema.StringRec) error { return sw.Write(r) },
		Doc: func(d schema.DocRec, orig []byte) error {
			d.Orig = "docs/" + Kind + "/" + d.ID + path.Ext(d.Out)
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
	err = schema.WriteDone(workDir, Kind, []string{"inventory.json"}, []string{"strings.ftbq.jsonl", "docs.ftbq.jsonl"},
		map[string]int{"files": c.Files, "keys": c.Keys, "existing_pt": c.ExistingPt, "skipped": c.Skipped, "file_errors": c.FileErrors})
	return c, err
}
