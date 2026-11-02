// Package install copia para a pasta do jogo os arquivos que o pack deixou em
// work/out/ftbquests/ (os do FTB Quests não podem ir dentro do resource pack).
//
// Regras: nunca sobrescreve sem --overwrite; com --overwrite guarda o arquivo
// antigo como <nome>.bak (ou .bak.1, .bak.2...); arquivo igual ao que já está
// lá não é tocado; nunca escreve fora da pasta do jogo nem através de link
// simbólico.
package install

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mtrad/internal/pack"
	"mtrad/internal/schema"
)

type Action string

const (
	Copied   Action = "copiado"
	Replaced Action = "substituído (cópia em .bak)"
	Same     Action = "já igual"
	Conflict Action = "já existe e é diferente; use --overwrite"
	Refused  Action = "recusado"
	Planned  Action = "seria copiado"
)

type Item struct {
	Rel    string
	Action Action
	Bak    string
	Note   string
}

type Result struct {
	Items     []Item
	GameDir   string
	Conflicts int
}

func Run(workDir string, overwrite, dryRun bool) (Result, error) {
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(workDir, "instance.json"), &in, schema.VInstance); err != nil {
		return Result{}, err
	}
	return Install(pack.FTBOutDir(workDir), in.GameDir, overwrite, dryRun)
}

// Install copia a árvore src para game.
func Install(src, game string, overwrite, dryRun bool) (Result, error) {
	res := Result{GameDir: game}
	if st, err := os.Stat(game); err != nil || !st.IsDir() {
		return res, fmt.Errorf("pasta do jogo não existe: %s", game)
	}
	var rels []string
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			r, _ := filepath.Rel(src, p)
			rels = append(rels, filepath.ToSlash(r))
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil // nada foi gerado para instalar
		}
		return res, err
	}
	sort.Strings(rels)
	gameAbs, err := filepath.Abs(game)
	if err != nil {
		return res, err
	}
	for _, rel := range rels {
		it := Item{Rel: rel}
		target := filepath.Join(gameAbs, filepath.FromSlash(rel))
		if r, err := filepath.Rel(gameAbs, target); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			it.Action, it.Note = Refused, "fora da pasta do jogo"
			res.Items = append(res.Items, it)
			continue
		}
		if why := symlinkOnPath(gameAbs, target); why != "" {
			it.Action, it.Note = Refused, why
			res.Items = append(res.Items, it)
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			return res, err
		}
		old, oerr := os.ReadFile(target)
		switch {
		case oerr == nil && bytes.Equal(old, data):
			it.Action = Same
		case oerr == nil && !overwrite:
			it.Action = Conflict
			res.Conflicts++
		case oerr != nil && !os.IsNotExist(oerr):
			return res, oerr
		default:
			it.Action = Copied
			if oerr == nil {
				it.Action = Replaced
			}
			if dryRun {
				it.Action = Planned
				if oerr == nil {
					it.Note = "o arquivo atual seria guardado em .bak"
				}
				break
			}
			if oerr == nil {
				bak, err := backupName(target)
				if err != nil {
					return res, err
				}
				if err := os.WriteFile(bak, old, 0o644); err != nil {
					return res, err
				}
				it.Bak = bak
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return res, err
			}
			tmp := target + ".mtrad-tmp"
			if err := schema.WriteFileSync(tmp, data, 0o644); err != nil {
				os.Remove(tmp)
				return res, err
			}
			if err := os.Rename(tmp, target); err != nil {
				os.Remove(tmp)
				return res, err
			}
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// symlinkOnPath devolve um motivo se algum pedaço do caminho dentro de base for
// um link simbólico (ou se o arquivo final for).
func symlinkOnPath(base, target string) string {
	rel, _ := filepath.Rel(base, target)
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "link simbólico em " + cur
		}
	}
	return ""
}

func backupName(target string) (string, error) {
	for i := 0; i < 100; i++ {
		name := target + ".bak"
		if i > 0 {
			name = fmt.Sprintf("%s.bak.%d", target, i)
		}
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return name, nil
		}
	}
	return "", fmt.Errorf("já há 100 cópias .bak de %s", target)
}
