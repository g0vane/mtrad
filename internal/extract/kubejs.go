package extract

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mtrad/internal/jsonx"
	"mtrad/internal/schema"
)

// KubeJSMod é o nome de "mod" dos textos que vêm de kubejs/assets.
const KubeJSMod = "kubejs"

// O KubeJS carrega kubejs/assets/<ns>/lang/<idioma>.json como um resource pack
// sempre ligado. Modpacks usam isso para textos próprios, como as missões
// exportadas pelo FTB Quest Localizer (kubejs/assets/ftbquestlocalizer/lang).
// A pasta FTBLang/ (cópias de segurança do Localizer) não é lida.
func extractKubeJS(inv schema.Inventory, errs Sink, emit func(schema.StringRec) error, seen map[string]string, c *Counts) error {
	if inv.GameDir == "" {
		return nil
	}
	root := filepath.Join(inv.GameDir, "kubejs", "assets")
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil // sem KubeJS
	}
	var nss []string
	for _, e := range ents {
		if e.IsDir() {
			nss = append(nss, e.Name())
		}
	}
	sort.Strings(nss)
	for _, ns := range nss {
		enPath := filepath.Join(root, ns, "lang", "en_us.json")
		src := "kubejs/assets/" + ns + "/lang/en_us.json"
		en, state := readLangFile(enPath, src, errs)
		if state == langMissing {
			continue // não há en_us.json neste namespace
		}
		if state == langBroken {
			c.FileErrors++
			continue
		}
		c.Files++
		c.KubeJS++
		pt := map[string]string{}
		p, pstate := readLangFile(filepath.Join(root, ns, "lang", "pt_br.json"), "kubejs/assets/"+ns+"/lang/pt_br.json", errs)
		if pstate == langOK {
			pt = p.Map()
		} else if pstate == langBroken {
			c.FileErrors++
		}
		dups := 0
		for _, kv := range en.Pairs {
			id := schema.ID(KubeJSMod, ns, kv.Key)
			if _, dup := seen[id]; dup {
				dups++
				continue
			}
			seen[id] = src
			rec := schema.StringRec{ID: id, Mod: KubeJSMod, NS: ns, Key: kv.Key, En: kv.Text, EnSha: schema.Sha(kv.Text), Src: src}
			if isQuestLang(ns, kv.Key) {
				rec.Kind = schema.KindFTBQLang
			}
			if p, has := pt[kv.Key]; has {
				pp := p
				rec.Pt = &pp
				c.ExistingPt++
			}
			if err := emit(rec); err != nil {
				return err
			}
			c.Keys++
		}
		if dups > 0 {
			c.Duplicates += dups
			errs.Add(src, "", fmt.Sprintf("%d chaves repetidas, ignoradas", dups))
		}
	}
	return nil
}

// isQuestLang diz se a chave é de missões do FTB Quests (texto com "&a",
// "{image:...}", "{@pagebreak}"): o namespace do FTB Quest Localizer ou uma
// chave "ftbquests.*".
func isQuestLang(ns, key string) bool {
	return ns == "ftbquestlocalizer" || strings.HasPrefix(key, "ftbquests.")
}

const (
	langMissing = iota
	langOK
	langBroken
)

// readLangFile lê um arquivo de idioma solto e diz se ele existe e abre.
func readLangFile(path, src string, errs Sink) (*jsonx.Doc, int) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, langMissing
		}
		errs.Add(src, "", err.Error())
		return nil, langBroken
	}
	d, err := jsonx.Parse(b)
	if err != nil {
		errs.Add(src, "", "JSON inválido: "+err.Error())
		return nil, langBroken
	}
	if d.NonString > 0 {
		errs.Add(src, "", fmt.Sprintf("%d valores que não são texto foram ignorados", d.NonString))
	}
	if d.Dups > 0 {
		errs.Add(src, "", fmt.Sprintf("%d chaves repetidas (a última vale)", d.Dups))
	}
	return d, langOK
}
