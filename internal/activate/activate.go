// Package activate liga o resource pack do mtrad e o idioma pt_br no
// options.txt do jogo, para o usuário não ter de ir em Opções > Pacotes de
// recursos. O options.txt é regravado pelo Minecraft ao sair, então a mudança
// só vale com o jogo fechado: Run recusa se achar o jogo aberto.
package activate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mtrad/internal/schema"
)

type Options struct {
	GameDir    string
	PackFormat schema.PackFormat
	PackFile   string // nome do .zip dentro de resourcepacks (padrão mtrad-pt_br.zip)
	SetLang    bool   // também põe lang:pt_br
	DryRun     bool
	Force      bool // ignora a detecção do jogo aberto
	// Running: como saber se o jogo está aberto (testes trocam; padrão olha /proc).
	Running func(gameDir string) bool
}

type Result struct {
	Path        string
	PackEntry   string // o texto que entrou em resourcePacks
	PackAdded   bool
	LangChanged bool
	Lang        string
	Created     bool // o options.txt não existia
	Backup      string
	Changed     bool
	Notes       []string
}

// entryName é como o options.txt chama o pacote: "file/<nome>" do 1.13 em
// diante (pack_format 4+), só o nome nas versões antigas.
func entryName(pf schema.PackFormat, file string) string {
	if pf.Major > 0 && pf.Major < 4 {
		return file
	}
	return "file/" + file
}

// langCode: de 1.11 em diante é pt_br em minúsculas; antes, pt_BR.
func langCode(pf schema.PackFormat) string {
	if pf.Major > 0 && pf.Major < 3 {
		return "pt_BR"
	}
	return "pt_br"
}

func Run(o Options) (Result, error) {
	var res Result
	if o.GameDir == "" {
		return res, errors.New("falta a pasta do jogo")
	}
	if o.PackFile == "" {
		o.PackFile = "mtrad-pt_br.zip"
	}
	if o.Running == nil {
		o.Running = gameRunning
	}
	res.Path = filepath.Join(o.GameDir, "options.txt")
	res.PackEntry = entryName(o.PackFormat, o.PackFile)
	res.Lang = langCode(o.PackFormat)
	if !o.DryRun && !o.Force && o.Running(o.GameDir) {
		return res, errors.New("o Minecraft parece estar aberto: ele regrava o options.txt ao sair e desfaria a mudança. Feche o jogo e rode de novo (ou use --force)")
	}
	if _, err := os.Stat(filepath.Join(o.GameDir, "resourcepacks", o.PackFile)); err != nil {
		res.Notes = append(res.Notes, "o pacote "+o.PackFile+" ainda não está em resourcepacks (rode `mtrad pack`)")
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return res, err
		}
		res.Created = true
		b = nil
	}
	nl := "\n"
	if bytes.Contains(b, []byte("\r\n")) {
		nl = "\r\n"
	}
	lines := strings.Split(strings.TrimSuffix(string(b), nl), nl)
	if len(b) == 0 {
		lines = nil
	}
	packIdx, langIdx := -1, -1
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "resourcePacks:"):
			packIdx = i
		case strings.HasPrefix(l, "lang:"):
			langIdx = i
		}
	}
	// resourcePacks
	if packIdx >= 0 {
		var list []string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[packIdx], "resourcePacks:")), &list); err != nil {
			return res, fmt.Errorf("não entendi a linha resourcePacks do options.txt (%v); nada foi alterado", err)
		}
		has := false
		for _, e := range list {
			if e == res.PackEntry {
				has = true
			}
		}
		if !has {
			list = append(list, res.PackEntry) // o último da lista tem a maior prioridade
			j, _ := json.Marshal(list)
			lines[packIdx] = "resourcePacks:" + string(j)
			res.PackAdded = true
		}
	} else {
		j, _ := json.Marshal([]string{"vanilla", res.PackEntry})
		lines = append(lines, "resourcePacks:"+string(j))
		res.PackAdded = true
	}
	// idioma
	if o.SetLang {
		want := "lang:" + res.Lang
		switch {
		case langIdx < 0:
			lines = append(lines, want)
			res.LangChanged = true
		case !strings.EqualFold(lines[langIdx], want):
			lines[langIdx] = want
			res.LangChanged = true
		}
	}
	res.Changed = res.PackAdded || res.LangChanged
	if !res.Changed || o.DryRun {
		return res, nil
	}
	if !res.Created {
		bak, err := freeBackupName(res.Path + ".mtrad.bak")
		if err != nil {
			return res, err
		}
		res.Backup = bak
		if err := os.WriteFile(res.Backup, b, 0o644); err != nil {
			return res, err
		}
	}
	out := strings.Join(lines, nl) + nl
	tmp := res.Path + ".mtrad.tmp"
	if err := schema.WriteFileSync(tmp, []byte(out), 0o644); err != nil {
		return res, err
	}
	return res, os.Rename(tmp, res.Path)
}

// freeBackupName devolve base se ainda não existe; senão base.2, base.3... A
// primeira cópia é a do options.txt original do jogador e nunca é sobrescrita
// por uma ativação seguinte (que já copiaria o arquivo mexido pelo mtrad).
func freeBackupName(base string) (string, error) {
	name := base
	for i := 2; i < 1000; i++ {
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return name, nil
		} else if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s.%d", base, i)
	}
	return "", fmt.Errorf("há cópias demais de %s; apague as antigas e tente de novo", base)
}
