// Package pack monta o resource pack com as traduções: pack.mcmeta no formato
// da versão da instância, assets/<ns>/lang/pt_br.json de cada namespace e, para
// o Patchouli, os documentos assets/<ns>/patchouli_books/<livro>/pt_br/...
// reescritos a partir dos originais. Os documentos do FTB Quests não vão no
// zip (não são recurso do jogo): saem em work/out/ftbquests/ para o install.
package pack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mtrad/internal/jsonx"
	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

// A partir deste formato o pack.mcmeta usa min_format e max_format
// (desde a snapshot 25w31a; formatos abaixo de 65 usam pack_format).
const minMaxFormatFrom = 65

const DefaultDescription = "Tradução PT-BR (mtrad)"

// McMeta devolve o conteúdo do pack.mcmeta para o formato dado.
func McMeta(pf schema.PackFormat, description string) ([]byte, error) {
	if pf.Major <= 0 {
		return nil, fmt.Errorf("formato de pacote inválido: %+v", pf)
	}
	pack := map[string]any{}
	if pf.Major < minMaxFormatFrom {
		pack["pack_format"] = pf.Major
	} else {
		var v any = pf.Major
		if pf.Minor > 0 {
			v = []int{pf.Major, pf.Minor}
		}
		pack["min_format"] = v
		pack["max_format"] = v
	}
	pack["description"] = description
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"pack": pack}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type Stats struct {
	Namespaces, Keys, FromReused, FromOK int
	PatchouliDocs, PatchouliKeys         int
	FTBFiles, FTBKeys                    int
	Warnings                             []string
}

// inlineLangNS é o namespace do pack que guarda as chaves das missões do
// FTB Quests 1.20.1 (texto dentro dos capítulos).
const inlineLangNS = "mtrad_ftbq"

// escPercent dobra o "%": o jogo passa todo texto de idioma por String.format
// (I18n.get), e um "%" solto daria "Format error".
func escPercent(s string) string { return strings.ReplaceAll(s, "%", "%%") }

type nsData struct {
	pairs []jsonx.Pair
	idx   map[string]int
}

func (n *nsData) set(k, v string) {
	if i, ok := n.idx[k]; ok {
		n.pairs[i].Text = v
		return
	}
	n.idx[k] = len(n.pairs)
	n.pairs = append(n.pairs, jsonx.Pair{Key: k, Text: v})
}

// DocInput é um documento original e a descrição dele.
type DocInput struct {
	Rec  schema.DocRec
	Orig []byte
}

// Input reúne tudo que entra no pacote.
type Input struct {
	Reused []schema.ReusedRec
	OK     []schema.OKRec
	Docs   []DocInput
	// LegacyLang: nome do arquivo de idioma no formato antigo (pt_br.lang ou
	// pt_BR.lang, até o Minecraft 1.12). Vazio = JSON (pt_br.json).
	LegacyLang string
}

// LegacyLangName diz como o pacote deve gravar o idioma para um formato de
// pacote: .json do formato 4 (1.13) em diante; .lang antes disso, com pt_BR
// nos formatos 1 e 2 (1.6 a 1.10) e pt_br a partir do 3 (1.11 e 1.12).
func LegacyLangName(pf schema.PackFormat) string {
	switch {
	case pf.Major <= 0 || pf.Major >= 4:
		return ""
	case pf.Major >= 3:
		return "pt_br.lang"
	default:
		return "pt_BR.lang"
	}
}

// File é um arquivo pronto para gravar.
type File struct {
	Name string
	Data []byte
}

// Plan é o resultado de Assemble: nada foi gravado ainda.
type Plan struct {
	Stats
	Zip []File // assets/**: idiomas e documentos do Patchouli, em ordem
	FTB []File // caminho relativo à pasta do jogo
}

func render(d DocInput, repl map[string]string) ([]byte, error) {
	n, err := relaxed.Parse(d.Orig)
	if err != nil {
		return nil, fmt.Errorf("%s: o original não abre mais: %w", d.Rec.Out, err)
	}
	// Os caminhos "<linha>#<caminho no JSON>" são textos de dentro de uma linha
	// de JSON rico: a linha é lida como JSON, recebe as traduções e volta a ser
	// um texto numa linha só.
	plain := map[string]string{}
	rich := map[string]map[string]string{}
	for p, v := range repl {
		if outer, inner, ok := strings.Cut(p, "#"); ok {
			if rich[outer] == nil {
				rich[outer] = map[string]string{}
			}
			rich[outer][inner] = v
			continue
		}
		plain[p] = v
	}
	outers := make([]string, 0, len(rich))
	for o := range rich {
		outers = append(outers, o)
	}
	sort.Strings(outers)
	for _, o := range outers {
		line := n.Get(o)
		if line == nil || line.Kind != relaxed.Str {
			return nil, fmt.Errorf("%s: a linha de texto rico %s não existe mais", d.Rec.Out, o)
		}
		jn, err := relaxed.Parse([]byte(line.Str))
		if err != nil {
			return nil, fmt.Errorf("%s: a linha de texto rico %s não abre como JSON: %w", d.Rec.Out, o, err)
		}
		inners := make([]string, 0, len(rich[o]))
		for i := range rich[o] {
			inners = append(inners, i)
		}
		sort.Strings(inners)
		for _, i := range inners {
			if err := jn.SetString(i, rich[o][i]); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", d.Rec.Out, o, err)
			}
		}
		b, err := relaxed.WriteCompact(jn)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", d.Rec.Out, o, err)
		}
		line.Str = string(b)
	}
	ptrs := make([]string, 0, len(plain))
	for p := range plain {
		ptrs = append(ptrs, p)
	}
	sort.Strings(ptrs)
	for _, p := range ptrs {
		if err := n.SetString(p, plain[p]); err != nil {
			return nil, fmt.Errorf("%s: %w", d.Rec.Out, err)
		}
	}
	f := relaxed.JSON
	if d.Rec.Format == "snbt" {
		f = relaxed.SNBT
	}
	return relaxed.Write(n, f)
}

// Assemble decide o conteúdo do pacote. Um texto de documento que não achou
// mais o caminho no original vira aviso e é ignorado; o resto segue.
func Assemble(in Input) (*Plan, error) {
	pl := &Plan{}
	byNS := map[string]*nsData{}
	get := func(ns string) *nsData {
		d := byNS[ns]
		if d == nil {
			d = &nsData{idx: map[string]int{}}
			byNS[ns] = d
		}
		return d
	}
	docRepl := map[string]map[string]string{} // Loc.Doc -> ptr -> texto
	put := func(kind, ns, key string, loc *schema.Loc, pt string, fromOK bool) {
		switch kind {
		case "", schema.KindFTBQLang:
			get(ns).set(key, pt)
		case schema.KindFTBQInl:
			// o capítulo passa a dizer "{chave}" e o texto vai para o lang do pack
			if loc == nil {
				pl.Warnings = append(pl.Warnings, fmt.Sprintf("%s %s sem loc; ignorado", kind, key))
				return
			}
			get(inlineLangNS).set(key, escPercent(pt))
			m := docRepl[loc.Doc]
			if m == nil {
				m = map[string]string{}
				docRepl[loc.Doc] = m
			}
			m[loc.Ptr] = "{" + key + "}"
		case "patchouli", "ftbq", schema.KindFTBQRich:
			if loc == nil {
				pl.Warnings = append(pl.Warnings, fmt.Sprintf("%s %s sem loc; ignorado", kind, key))
				return
			}
			m := docRepl[loc.Doc]
			if m == nil {
				m = map[string]string{}
				docRepl[loc.Doc] = m
			}
			m[loc.Ptr] = pt
		default:
			pl.Warnings = append(pl.Warnings, fmt.Sprintf("tipo desconhecido %q em %s; ignorado", kind, key))
			return
		}
		if fromOK {
			pl.FromOK++
		} else {
			pl.FromReused++
		}
	}
	for _, r := range in.Reused {
		put(r.Kind, r.NS, r.Key, r.Loc, r.Pt, false)
	}
	for _, r := range in.OK {
		put(r.Kind, r.NS, r.Key, r.Loc, r.Pt, true)
	}
	if len(byNS) > 0 {
		var names []string
		for n := range byNS {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d := byNS[n]
			pl.Keys += len(d.pairs)
			if in.LegacyLang != "" {
				pl.Zip = append(pl.Zip, File{"assets/" + n + "/lang/" + in.LegacyLang, jsonx.MarshalLang(d.pairs)})
			} else {
				pl.Zip = append(pl.Zip, File{"assets/" + n + "/lang/pt_br.json", jsonx.Marshal(d.pairs)})
			}
		}
		pl.Namespaces = len(names)
	}
	enInline := &nsData{idx: map[string]int{}}
	docs := append([]DocInput(nil), in.Docs...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Rec.Out < docs[j].Rec.Out })
	used := map[string]bool{}
	for _, d := range docs {
		repl := docRepl[d.Rec.Out]
		if len(repl) == 0 {
			continue // nada traduzido neste documento: o jogo usa o inglês
		}
		used[d.Rec.Out] = true
		data, err := render(d, repl)
		if err != nil {
			return nil, err
		}
		if d.Rec.Kind == schema.KindFTBQInl {
			// o inglês original de cada chave: sem ele, quem joga em outro
			// idioma (ou com o pt_br incompleto) veria o nome da chave
			orig, err := relaxed.Parse(d.Orig)
			if err != nil {
				return nil, fmt.Errorf("%s: o original não abre mais: %w", d.Rec.Out, err)
			}
			ptrs := make([]string, 0, len(repl))
			for p := range repl {
				ptrs = append(ptrs, p)
			}
			sort.Strings(ptrs)
			for _, p := range ptrs {
				if strings.Contains(p, "#") {
					continue // texto de dentro de JSON rico: não passa pelo lang
				}
				if t := orig.Get(p); t != nil && t.Kind == relaxed.Str {
					enInline.set(strings.TrimSuffix(strings.TrimPrefix(repl[p], "{"), "}"), escPercent(t.Str))
				}
			}
		}
		switch d.Rec.Kind {
		case "patchouli":
			pl.Zip = append(pl.Zip, File{d.Rec.Out, data})
			pl.PatchouliDocs++
			pl.PatchouliKeys += len(repl)
		case "ftbq", schema.KindFTBQInl:
			pl.FTB = append(pl.FTB, File{d.Rec.Out, data})
			pl.FTBFiles++
			pl.FTBKeys += len(repl)
		}
	}
	if len(enInline.pairs) > 0 {
		pl.Zip = append(pl.Zip, File{"assets/" + inlineLangNS + "/lang/en_us.json", jsonx.Marshal(enInline.pairs)})
	}
	var orphan []string
	for doc := range docRepl {
		if !used[doc] {
			orphan = append(orphan, doc)
		}
	}
	sort.Strings(orphan)
	for _, o := range orphan {
		pl.Warnings = append(pl.Warnings, fmt.Sprintf("%d textos para %s, mas o documento não está em docs.*.jsonl; ignorados", len(docRepl[o]), o))
	}
	// última defesa: nenhum nome vindo de um jar sai do pacote com caminho perigoso
	pl.Zip = dropUnsafe(pl.Zip, &pl.Warnings, "pacote")
	pl.FTB = dropUnsafe(pl.FTB, &pl.Warnings, "FTB Quests")
	if len(pl.Zip) == 0 && len(pl.FTB) == 0 {
		return nil, fmt.Errorf("não há nenhum texto para empacotar")
	}
	return pl, nil
}

// dropUnsafe tira de uma lista de arquivos os de nome perigoso (com "..", \,
// caminho absoluto...) e avisa de cada um.
func dropUnsafe(files []File, warns *[]string, where string) []File {
	var keep []File
	for _, f := range files {
		if schema.SafeEntryName(f.Name) {
			keep = append(keep, f)
			continue
		}
		*warns = append(*warns, fmt.Sprintf("arquivo com nome suspeito não vai para o %s: %q", where, f.Name))
	}
	return keep
}

// WriteZip grava o .zip em outPath. O resultado é o mesmo byte a byte para a
// mesma entrada (datas fixas dentro do zip).
func WriteZip(outPath string, pf schema.PackFormat, description string, files []File) error {
	if description == "" {
		description = DefaultDescription
	}
	meta, err := McMeta(pf, description)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fixed})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	err = put("pack.mcmeta", meta)
	for _, fl := range files {
		if err != nil {
			break
		}
		err = put(fl.Name, fl.Data)
	}
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, outPath)
}

// FTBOutDir é onde o pack deixa os arquivos do FTB Quests, dentro da pasta de trabalho.
func FTBOutDir(workDir string) string { return filepath.Join(workDir, "out", "ftbquests") }

// WriteFTB recria dir com os arquivos (caminhos relativos à pasta do jogo). Os
// arquivos são escritos numa pasta vizinha e só no fim ela troca a antiga, então
// um erro no meio não destrói a saída anterior.
func WriteFTB(dir string, files []File) error {
	tmp := dir + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	for _, f := range files {
		p := filepath.Join(tmp, filepath.FromSlash(f.Name))
		if rel, err := filepath.Rel(tmp, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			os.RemoveAll(tmp)
			return fmt.Errorf("caminho fora da pasta de saída: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := os.WriteFile(p, f.Data, 0o644); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if len(files) == 0 {
		if err := os.MkdirAll(tmp, 0o755); err != nil {
			return err
		}
	}
	if err := schema.SwapDir(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// Build monta só o que vai no zip, a partir de textos de arquivos de idioma
// (e de documentos, se vierem em Input). Mantido para quem só tem reused e ok.
func Build(outPath string, pf schema.PackFormat, description string, reused []schema.ReusedRec, ok []schema.OKRec) (Stats, error) {
	pl, err := Assemble(Input{Reused: reused, OK: ok, LegacyLang: LegacyLangName(pf)})
	if err != nil {
		return Stats{}, err
	}
	if len(pl.Zip) == 0 {
		return pl.Stats, fmt.Errorf("não há nenhum texto para o pacote")
	}
	return pl.Stats, WriteZip(outPath, pf, description, pl.Zip)
}

func readDocs(workDir string) ([]DocInput, []string, error) {
	m, err := filepath.Glob(filepath.Join(workDir, "docs.*.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(m)
	var out []DocInput
	var inputs []string
	for _, f := range m {
		b := filepath.Base(f)
		stage := strings.TrimSuffix(strings.TrimPrefix(b, "docs."), ".jsonl")
		if _, err := schema.RequireFresh(workDir, stage, b); err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, b)
		recs, err := schema.ReadAll[schema.DocRec](f, schema.VDocs)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range recs {
			orig, err := os.ReadFile(filepath.Join(workDir, filepath.FromSlash(r.Orig)))
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", b, err)
			}
			out = append(out, DocInput{Rec: r, Orig: orig})
		}
	}
	return out, inputs, nil
}

// Run lê instance.json, ok.jsonl, reused.jsonl e docs.*.jsonl; grava o pacote
// (se houver algo para ele) e os arquivos do FTB Quests em work/out/ftbquests.
// Sem outPath, usa <pasta do jogo>/resourcepacks/mtrad-pt_br.zip. O caminho
// devolvido é vazio quando não havia nada para o zip.
func Run(workDir, outPath, description string) (Stats, string, error) {
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(workDir, "instance.json"), &in, schema.VInstance); err != nil {
		return Stats{}, "", err
	}
	for _, f := range [][2]string{{"diff", "reused.jsonl"}, {"validate", "ok.jsonl"}} {
		if _, err := schema.RequireFresh(workDir, f[0], f[1]); err != nil {
			return Stats{}, "", err
		}
	}
	reused, err := schema.ReadAll[schema.ReusedRec](filepath.Join(workDir, "reused.jsonl"), schema.VReused)
	if err != nil {
		return Stats{}, "", err
	}
	ok, err := schema.ReadAll[schema.OKRec](filepath.Join(workDir, "ok.jsonl"), schema.VOK)
	if err != nil {
		return Stats{}, "", err
	}
	docs, _, err := readDocs(workDir)
	if err != nil {
		return Stats{}, "", err
	}
	pl, err := Assemble(Input{Reused: reused, OK: ok, Docs: docs, LegacyLang: LegacyLangName(in.PackFormat)})
	if err != nil {
		return Stats{}, "", err
	}
	if outPath == "" {
		outPath = filepath.Join(in.GameDir, "resourcepacks", "mtrad-pt_br.zip")
	}
	if len(pl.Zip) > 0 {
		if err := WriteZip(outPath, in.PackFormat, description, pl.Zip); err != nil {
			return pl.Stats, "", err
		}
	} else {
		outPath = ""
	}
	if err := WriteFTB(FTBOutDir(workDir), pl.FTB); err != nil {
		return pl.Stats, outPath, err
	}
	return pl.Stats, outPath, nil
}
