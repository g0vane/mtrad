// Package vanilla acha os textos do próprio Minecraft (en_us dentro do jar do
// cliente, pt_br no cache de assets do launcher) para montar o glossário.
//
// Os caminhos seguem o que o Prism Launcher costuma usar, mas isso NÃO foi
// conferido num Prism real (ver doc §22): o módulo tenta vários lugares, diz
// exatamente o que achou e o que não achou, e o usuário pode passar --en e --pt.
//
//	<dados>/libraries/com/mojang/minecraft/<versão>/minecraft-<versão>-client.jar
//	<dados>/assets/indexes/<índice>.json   (objects: "minecraft/lang/pt_br.json" -> hash)
//	<dados>/assets/objects/<2 primeiros do hash>/<hash>
package vanilla

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"mtrad/internal/jarx"
	"mtrad/internal/schema"
)

type Found struct {
	En, Pt         []byte
	EnFrom, PtFrom string
	Notes          []string
}

var reHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Roots lista as pastas de dados do Prism que vale tentar, em ordem.
func Roots(in schema.Instance, extra []string, home string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range extra {
		add(e)
	}
	if in.InstanceDir != "" {
		add(filepath.Dir(filepath.Dir(in.InstanceDir)))
	}
	if home != "" {
		add(filepath.Join(home, ".var", "app", "org.prismlauncher.PrismLauncher", "data", "PrismLauncher"))
		add(filepath.Join(home, ".local", "share", "PrismLauncher"))
	}
	return out
}

// Find procura en_us e pt_br. Nunca falha por não achar: devolve o que achou e
// uma nota por coisa que faltou.
func Find(in schema.Instance, roots []string) Found {
	var f Found
	for _, root := range roots {
		if f.En == nil && in.McVersion != "" {
			jar := filepath.Join(root, "libraries", "com", "mojang", "minecraft", in.McVersion, "minecraft-"+in.McVersion+"-client.jar")
			if b, err := readFromJar(jar, "assets/minecraft/lang/en_us.json"); err == nil {
				f.En, f.EnFrom = b, jar+" > assets/minecraft/lang/en_us.json"
			}
		}
		if f.Pt == nil {
			if b, from, err := ptFromAssets(root, in.McVersion); err == nil {
				f.Pt, f.PtFrom = b, from
			}
		}
	}
	if f.En == nil {
		f.Notes = append(f.Notes, "não achei o jar do cliente com en_us.json (procurei em "+fmt.Sprint(roots)+")")
	}
	if f.Pt == nil {
		f.Notes = append(f.Notes, "não achei minecraft/lang/pt_br.json nos assets (procurei em "+fmt.Sprint(roots)+"); abra o jogo uma vez com o idioma pt_br para o launcher baixar")
	}
	return f
}

func readFromJar(jar, name string) ([]byte, error) {
	j, err := jarx.Open([]string{jar})
	if err != nil {
		return nil, err
	}
	defer j.Close()
	return j.Read(name, jarx.MaxEntry)
}

func ptFromAssets(root, mc string) ([]byte, string, error) {
	idxs, _ := filepath.Glob(filepath.Join(root, "assets", "indexes", "*.json"))
	type cand struct {
		path string
		mod  int64
		hash string
	}
	var cs []cand
	for _, p := range idxs {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var ix struct {
			Objects map[string]struct {
				Hash string `json:"hash"`
			} `json:"objects"`
		}
		if json.Unmarshal(b, &ix) != nil {
			continue
		}
		o, ok := ix.Objects["minecraft/lang/pt_br.json"]
		if !ok || !reHash.MatchString(o.Hash) {
			continue
		}
		st, _ := os.Stat(p)
		var mod int64
		if st != nil {
			mod = st.ModTime().UnixNano()
		}
		if filepath.Base(p) == mc+".json" {
			mod = 1<<62 - 1 // índice com o nome da versão ganha
		}
		cs = append(cs, cand{p, mod, o.Hash})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].mod > cs[j].mod })
	for _, c := range cs {
		obj := filepath.Join(root, "assets", "objects", c.hash[:2], c.hash)
		if b, err := os.ReadFile(obj); err == nil {
			return b, obj, nil
		}
	}
	return nil, "", fmt.Errorf("sem pt_br em %s", root)
}
