// Package scan lista os jars de mods, segue os jars aninhados (campo jars do
// fabric.mod.json) e anota id, versão, hash e arquivos de idioma de cada um.
// Não lê o conteúdo dos arquivos de idioma.
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mtrad/internal/jarx"
	"mtrad/internal/schema"
)

// langRe pega os arquivos de idioma: .json (1.13 em diante) e .lang (1.12 e
// anteriores, "chave=valor", com en_US/en_us conforme a versão).
var langRe = regexp.MustCompile(`^assets/([^/]+)/lang/([^/]+)\.(json|lang)$`)

// Sink recebe avisos; *schema.ErrLog satisfaz.
type Sink interface {
	Add(src, id, msg string)
}

type nopSink struct{}

func (nopSink) Add(string, string, string) {}

// Scan percorre modsDir (recursivamente) atrás de *.jar. Os caminhos da cadeia
// ficam relativos a gameDir.
func Scan(gameDir, modsDir string, errs Sink) (schema.Inventory, error) {
	if errs == nil {
		errs = nopSink{}
	}
	inv := schema.Inventory{Schema: schema.VInventory, GameDir: gameDir}
	if _, err := os.Stat(modsDir); err != nil {
		return inv, fmt.Errorf("pasta de mods: %w", err)
	}
	var paths []string
	err := filepath.WalkDir(modsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			errs.Add(p, "", err.Error())
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".jar") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return inv, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, err := filepath.Rel(gameDir, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		sum, size, err := fileHash(p)
		if err != nil {
			errs.Add(rel, "", err.Error())
			continue
		}
		j, err := jarx.Open([]string{p})
		if err != nil {
			errs.Add(rel, "", "jar ilegível: "+err.Error())
			inv.Jars = append(inv.Jars, schema.JarInfo{Chain: []string{rel}, SHA256: sum, Size: size, Notes: []string{"jar ilegível: " + err.Error()}})
			continue
		}
		inv.Jars = scanJar(inv.Jars, j, []string{rel}, sum, size, 0, errs, &nestedBudget{})
		j.Close()
	}
	return inv, nil
}

func fileHash(p string) (string, int64, error) {
	sum, err := schema.FileSHA256(p)
	if err != nil {
		return "", 0, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", 0, err
	}
	return sum, st.Size(), nil
}

// Limites de jars dentro de jars por jar da pasta mods (um jar hostil ou
// quebrado pode listar o mesmo jar aninhado milhares de vezes; sem teto, cada
// leitura ocupa até jarx.MaxNested de memória e CPU). São variáveis para os
// testes poderem baixá-los.
var (
	MaxNestedBytes int64 = 2 << 30
	MaxNestedJars        = 2000
)

// nestedBudget conta o que já foi lido de jars aninhados dentro de um jar.
type nestedBudget struct {
	bytes int64
	jars  int
}

func scanJar(out []schema.JarInfo, j *jarx.Jar, chain []string, sum string, size int64, depth int, errs Sink, bud *nestedBudget) []schema.JarInfo {
	src := strings.Join(chain, " > ")
	info := schema.JarInfo{Chain: append([]string(nil), chain...), SHA256: sum, Size: size}
	meta, merr := j.Meta()
	if merr != nil {
		info.Notes = append(info.Notes, merr.Error())
		errs.Add(src, "", merr.Error())
	}
	info.ModID, info.Version, info.Name, info.Loader = meta.ID, meta.Version, meta.Name, meta.Loader
	if !meta.Found {
		info.Notes = append(info.Notes, "sem metadados de mod (fabric.mod.json, mods.toml, mcmod.info)")
	} else if meta.Found && meta.ID == "" {
		info.Notes = append(info.Notes, meta.Format+" sem o id do mod")
	}
	for _, n := range j.Names() {
		m := langRe.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		if !schema.SafeEntryName(n) {
			info.Notes = append(info.Notes, "arquivo de idioma com nome suspeito ignorado: "+n)
			continue
		}
		low := strings.ToLower(m[2])
		switch {
		case m[3] == "lang" && (low == "en_us" || low == "pt_br"):
			// formato antigo: o jogo lê en_US.lang e en_us.lang conforme a versão
			info.Langs = append(info.Langs, schema.LangRef{NS: m[1], Locale: low, Path: n})
		case m[3] == "json" && (m[2] == "en_us" || m[2] == "pt_br"):
			info.Langs = append(info.Langs, schema.LangRef{NS: m[1], Locale: m[2], Path: n})
		case m[3] == "json" && (low == "en_us" || low == "pt_br"):
			info.Notes = append(info.Notes, "idioma com letras maiúsculas ignorado: "+n)
		}
	}
	out = append(out, info)
	self := len(out) - 1
	if depth >= jarx.MaxDepth {
		if len(meta.Jars) > 0 {
			out[self].Notes = append(out[self].Notes, "jars aninhados além da profundidade máxima ignorados")
		}
		return out
	}
	seenNested := map[string]bool{}
	for _, f := range meta.Jars {
		if seenNested[f] {
			continue // o mesmo jar aninhado listado mais de uma vez: lido uma só
		}
		seenNested[f] = true
		if bud.jars >= MaxNestedJars || bud.bytes >= MaxNestedBytes {
			out[self].Notes = append(out[self].Notes, "limite de jars aninhados atingido; os demais não foram lidos (a partir de "+f+")")
			errs.Add(src, "", "limite de jars aninhados atingido; "+f+" e os seguintes não foram lidos")
			break
		}
		bud.jars++
		b, err := j.Read(f, jarx.MaxNested)
		if err != nil {
			out[self].Notes = append(out[self].Notes, "jar aninhado declarado "+f+" mas não foi possível ler: "+err.Error())
			errs.Add(src, "", "jar aninhado "+f+": "+err.Error())
			continue
		}
		bud.bytes += int64(len(b))
		nj, err := jarx.FromBytes(b)
		if err != nil {
			out[self].Notes = append(out[self].Notes, "jar aninhado ilegível "+f+": "+err.Error())
			errs.Add(src, "", "jar aninhado "+f+": "+err.Error())
			continue
		}
		h := sha256.Sum256(b)
		out = scanJar(out, nj, append(append([]string(nil), chain...), f), hex.EncodeToString(h[:]), int64(len(b)), depth+1, errs, bud)
	}
	for _, o := range j.Orphans(meta) {
		out[self].Notes = append(out[self].Notes, "jar em META-INF/jars não declarado em jars (não lido): "+o)
	}
	return out
}

// Run lê instance.json e grava inventory.json em workDir.
func Run(workDir string, errs Sink) (schema.Inventory, error) {
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(workDir, "instance.json"), &in, schema.VInstance); err != nil {
		return schema.Inventory{}, err
	}
	if _, err := schema.RequireFresh(workDir, "version", "instance.json"); err != nil {
		return schema.Inventory{}, err
	}
	inv, err := Scan(in.GameDir, filepath.Join(in.GameDir, "mods"), errs)
	if err != nil {
		return inv, err
	}
	if err := schema.WriteJSON(filepath.Join(workDir, "inventory.json"), inv); err != nil {
		return inv, err
	}
	langs := 0
	for _, j := range inv.Jars {
		langs += len(j.Langs)
	}
	err = schema.WriteDone(workDir, "scan", []string{"instance.json"}, []string{"inventory.json"}, map[string]int{"jars": len(inv.Jars), "lang_files": langs})
	return inv, err
}
