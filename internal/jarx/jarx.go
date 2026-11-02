// Package jarx abre .jar (zip) e jars aninhados, e lê o fabric.mod.json.
package jarx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"mtrad/internal/jsonx"
)

const (
	MaxEntry  = 64 << 20  // maior arquivo lido de dentro de um jar
	MaxNested = 512 << 20 // maior jar aninhado lido na memória
	MaxDepth  = 5
)

// MaxEntries: jar com mais entradas que isto é recusado (um zip malicioso com
// milhões de entradas gastaria toda a memória só no índice). É variável para os
// testes poderem baixar o limite.
var MaxEntries = 500_000

type Jar struct {
	R       *zip.Reader
	files   map[string]*zip.File
	closers []io.Closer
}

// Open abre chain[0] (caminho em disco) e, para cada nome seguinte, o jar
// aninhado com esse nome dentro do anterior.
func Open(chain []string) (*Jar, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("cadeia vazia")
	}
	zr, err := zip.OpenReader(chain[0])
	if err != nil {
		return nil, err
	}
	if len(zr.File) > MaxEntries {
		zr.Close()
		return nil, fmt.Errorf("%s: %d entradas, mais que o limite de %d", chain[0], len(zr.File), MaxEntries)
	}
	j := newJar(&zr.Reader)
	j.closers = append(j.closers, zr)
	for _, name := range chain[1:] {
		b, err := j.Read(name, MaxNested)
		if err != nil {
			j.Close()
			return nil, err
		}
		nz, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			j.Close()
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(nz.File) > MaxEntries {
			j.Close()
			return nil, fmt.Errorf("%s: %d entradas, mais que o limite de %d", name, len(nz.File), MaxEntries)
		}
		j = &Jar{R: nz, files: indexFiles(nz), closers: j.closers}
	}
	return j, nil
}

// FromBytes abre um jar que já está na memória.
func FromBytes(b []byte) (*Jar, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("%d entradas, mais que o limite de %d", len(zr.File), MaxEntries)
	}
	return newJar(zr), nil
}

func newJar(zr *zip.Reader) *Jar { return &Jar{R: zr, files: indexFiles(zr)} }

func indexFiles(zr *zip.Reader) map[string]*zip.File {
	m := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		m[f.Name] = f
	}
	return m
}

func (j *Jar) Close() {
	for _, c := range j.closers {
		c.Close()
	}
}

func (j *Jar) Has(name string) bool { _, ok := j.files[name]; return ok }

func (j *Jar) Names() []string {
	n := make([]string, 0, len(j.files))
	for k := range j.files {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func (j *Jar) Read(name string, limit int64) ([]byte, error) {
	f, ok := j.files[name]
	if !ok {
		return nil, fmt.Errorf("%s: não existe no jar", name)
	}
	if int64(f.UncompressedSize64) > limit {
		return nil, fmt.Errorf("%s: maior que o limite de %d bytes", name, limit)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// o tamanho declarado no cabeçalho pode mentir (zip bomb): lê um byte a
	// mais que o limite para saber se estourou, em vez de cortar em silêncio
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: descompactado passa do limite de %d bytes (tamanho declarado no zip: %d)", name, limit, f.UncompressedSize64)
	}
	return b, nil
}

// Meta é o que interessa dos metadados do mod: fabric.mod.json (Fabric),
// META-INF/neoforge.mods.toml (NeoForge), META-INF/mods.toml (Forge) ou
// mcmod.info (Forge antigo, até 1.12).
type Meta struct {
	ID      string
	Version string
	Name    string
	Loader  string   // fabric, neoforge, forge ou forge-legacy
	Jars    []string // jars aninhados: "jars" (Fabric) ou META-INF/jarjar (Forge e NeoForge)
	Found   bool
	Format  string // arquivo de onde vieram os dados
}

func (j *Jar) Meta() (Meta, error) {
	if b, err := j.Read("fabric.mod.json", MaxEntry); err == nil {
		var raw struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Name    string `json:"name"`
			Jars    []struct {
				File string `json:"file"`
			} `json:"jars"`
		}
		if err := jsonx.Unmarshal(b, &raw); err != nil {
			return Meta{Found: true, Loader: "fabric", Format: "fabric.mod.json"}, fmt.Errorf("fabric.mod.json: %w", err)
		}
		m := Meta{ID: raw.ID, Version: raw.Version, Name: raw.Name, Found: true, Loader: "fabric", Format: "fabric.mod.json"}
		for _, e := range raw.Jars {
			m.Jars = append(m.Jars, e.File)
		}
		return m, nil
	}
	for _, f := range []struct{ path, loader string }{
		{"META-INF/neoforge.mods.toml", "neoforge"},
		{"META-INF/mods.toml", "forge"},
	} {
		b, err := j.Read(f.path, MaxEntry)
		if err != nil {
			continue
		}
		m := Meta{Found: true, Loader: f.loader, Format: f.path}
		m.ID, m.Version, m.Name = parseModsToml(string(b))
		m.Jars = j.jarJar()
		return m, nil
	}
	if b, err := j.Read("mcmod.info", MaxEntry); err == nil {
		m := Meta{Found: true, Loader: "forge-legacy", Format: "mcmod.info"}
		var list []struct {
			ModID   string `json:"modid"`
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		var one struct {
			ModList []struct {
				ModID   string `json:"modid"`
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"modList"`
		}
		if err := jsonx.Unmarshal(b, &list); err == nil && len(list) > 0 {
			m.ID, m.Name, m.Version = list[0].ModID, list[0].Name, list[0].Version
		} else if err := jsonx.Unmarshal(b, &one); err == nil && len(one.ModList) > 0 {
			m.ID, m.Name, m.Version = one.ModList[0].ModID, one.ModList[0].Name, one.ModList[0].Version
		}
		return m, nil
	}
	return Meta{}, nil // sem metadados de mod: não é erro
}

// jarJar lista os jars aninhados do Forge e do NeoForge (Jar-in-Jar): os
// caminhos de META-INF/jarjar/metadata.json (campo path de cada item de jars)
// mais qualquer outro *.jar dentro de META-INF/jarjar/.
func (j *Jar) jarJar() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] && j.Has(p) {
			seen[p] = true
			out = append(out, p)
		}
	}
	if b, err := j.Read("META-INF/jarjar/metadata.json", MaxEntry); err == nil {
		var raw struct {
			Jars []struct {
				Path string `json:"path"`
			} `json:"jars"`
		}
		if jsonx.Unmarshal(b, &raw) == nil {
			for _, e := range raw.Jars {
				add(strings.TrimPrefix(e.Path, "/"))
			}
		}
	}
	for _, n := range j.Names() {
		if strings.HasPrefix(n, "META-INF/jarjar/") && strings.HasSuffix(n, ".jar") {
			add(n)
		}
	}
	return out
}

// parseModsToml lê só o primeiro [[mods]]: modId, version e displayName.
// É um leitor mínimo de TOML (chave = "texto"), suficiente para esses campos.
func parseModsToml(s string) (id, version, name string) {
	in := false
	done := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if in {
				done = true
			}
			head, _, _ := strings.Cut(line, "#")
			in = strings.TrimSpace(head) == "[[mods]]"
			if done {
				break
			}
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), tomlString(strings.TrimSpace(v))
		switch k {
		case "modId":
			id = v
		case "version":
			version = v
		case "displayName":
			name = v
		}
	}
	return
}

// tomlString devolve o texto entre aspas simples ou duplas (ignora o que vem
// depois, como um comentário); sem aspas, devolve o valor até o "#".
func tomlString(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:]
	}
	if i := strings.IndexByte(v, '#'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// Orphans lista *.jar em META-INF/jars que o campo jars não declara.
func (j *Jar) Orphans(m Meta) []string {
	decl := map[string]bool{}
	for _, d := range m.Jars {
		decl[d] = true
	}
	var o []string
	for _, n := range j.Names() {
		if strings.HasPrefix(n, "META-INF/jars/") && strings.HasSuffix(n, ".jar") && !decl[n] {
			o = append(o, n)
		}
	}
	return o
}

// Chain monta o texto "a.jar > b.jar" para mensagens.
func ChainString(chain []string) string {
	parts := make([]string, len(chain))
	for i, c := range chain {
		if i == 0 {
			parts[i] = filepath.Base(c)
		} else {
			parts[i] = filepath.Base(c)
		}
	}
	return strings.Join(parts, " > ")
}
