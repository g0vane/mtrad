package pack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

// maxVerifyEntry limita o que Verify lê de uma entrada do zip (o pacote é
// nosso, mas o arquivo pode ter sido trocado por outro).
const maxVerifyEntry = 64 << 20

// Limites globais impedem que um ZIP com muitas entradas ou grande volume
// descompactado consuma recursos excessivos durante a verificação.
const (
	maxVerifyEntries = 100_000
	maxVerifyTotal   = 512 << 20
)

// VerifyReport é o resultado de Verify.
type VerifyReport struct {
	Zip         string
	Docs        int      // documentos do Patchouli conferidos
	DocsBad     int      // destes, os que têm algum problema
	DocsMissing int      // documentos que não estão no pacote (ficam em inglês: não é erro)
	LangFiles   int      // arquivos de idioma conferidos
	Keys        int      // chaves de idioma conferidas
	Problems    []string // o que está errado; vazio = o pacote bate com os originais
}

// Verify confere um pacote já montado contra o que a pasta de trabalho diz que
// devia haver nele. Serve para responder, com evidência, "o pacote estragou
// isto?": o pack.mcmeta tem o formato certo, nenhuma entrada tem caminho
// perigoso, cada documento do Patchouli é igual ao original em tudo, menos nos
// textos que o mtrad traduziu, e todo arquivo de idioma só tem chaves que
// existem no original. zipPath vazio = <pasta do jogo>/resourcepacks/mtrad-pt_br.zip.
func Verify(workDir, zipPath string) (VerifyReport, error) {
	var rep VerifyReport
	var in schema.Instance
	if err := schema.ReadJSON(filepath.Join(workDir, "instance.json"), &in, schema.VInstance); err != nil {
		return rep, err
	}
	if zipPath == "" {
		zipPath = filepath.Join(in.GameDir, "resourcepacks", "mtrad-pt_br.zip")
	}
	rep.Zip = zipPath
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return rep, fmt.Errorf("não consegui abrir o pacote %s: %w", zipPath, err)
	}
	defer zr.Close()
	problem := func(format string, a ...any) { rep.Problems = append(rep.Problems, fmt.Sprintf(format, a...)) }

	// Entradas: limita quantidade e volume descompactado antes de indexar.
	files := map[string]*zip.File{}
	var totalUncompressed uint64
	for i, f := range zr.File {
		if i >= maxVerifyEntries {
			problem("pacote excede o limite de %d entradas", maxVerifyEntries)
			break
		}
		if f.UncompressedSize64 > maxVerifyTotal-totalUncompressed {
			problem("pacote excede o limite de %d MiB descompactados", maxVerifyTotal>>20)
			break
		}
		totalUncompressed += f.UncompressedSize64

		n := f.Name
		switch {
		case strings.HasSuffix(n, "/"):
			continue
		case strings.Contains(n, "\\") || path.IsAbs(n) || n != path.Clean(n) || n == ".." || strings.HasPrefix(n, "../"):
			problem("entrada com caminho suspeito: %q", n)
			continue
		case files[n] != nil:
			problem("entrada repetida: %q", n)
			continue
		case f.UncompressedSize64 > maxVerifyEntry:
			problem("entrada %q excede o limite de %d MiB", n, maxVerifyEntry>>20)
			continue
		}
		files[n] = f
	}
	read := func(n string) ([]byte, error) {
		f := files[n]
		if f == nil {
			return nil, os.ErrNotExist
		}
		if f.UncompressedSize64 > maxVerifyEntry {
			return nil, fmt.Errorf("entrada grande demais (%d bytes)", f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxVerifyEntry+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxVerifyEntry {
			return nil, fmt.Errorf("entrada excedeu o limite de %d MiB durante a leitura", maxVerifyEntry>>20)
		}
		return b, nil
	}

	// pack.mcmeta: o mesmo formato que a versão da instância pede
	if in.PackFormat.Major > 0 {
		got, err := read("pack.mcmeta")
		if err != nil {
			problem("pack.mcmeta: %v", err)
		} else if want, err := McMeta(in.PackFormat, "x"); err == nil {
			var g, w map[string]map[string]any
			if err := json.Unmarshal(got, &g); err != nil {
				problem("pack.mcmeta não é JSON: %v", err)
			} else {
				_ = json.Unmarshal(want, &w)
				delete(g["pack"], "description")
				delete(w["pack"], "description")
				if !reflect.DeepEqual(g["pack"], w["pack"]) {
					problem("pack.mcmeta com formato %v, mas a instância pede %v", g["pack"], w["pack"])
				}
			}
		}
	}

	// onde cada texto extraído vive: por documento (pelo caminho de saída, que é o
	// Loc.Doc), os caminhos que podem mudar
	// e, por namespace, as chaves de idioma que existem
	allowed := map[string]map[string]bool{}  // doc -> caminho
	langKeys := map[string]map[string]bool{} // ns -> chave
	for _, f := range []string{"strings.jsonl", "strings.patchouli.jsonl", "strings.ftbq.jsonl"} {
		p := filepath.Join(workDir, f)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := schema.Each(p, schema.VStrings, func(r schema.StringRec) error {
			if r.Loc != nil {
				ptr := r.Loc.Ptr
				if outer, _, ok := strings.Cut(ptr, "#"); ok {
					ptr = outer // texto dentro de uma linha de JSON rico: a linha toda muda
				}
				if allowed[r.Loc.Doc] == nil {
					allowed[r.Loc.Doc] = map[string]bool{}
				}
				allowed[r.Loc.Doc][ptr] = true
			} else if r.Kind == "" {
				if langKeys[r.NS] == nil {
					langKeys[r.NS] = map[string]bool{}
				}
				langKeys[r.NS][r.Key] = true
			}
			return nil
		}); err != nil {
			return rep, fmt.Errorf("%s: %w", f, err)
		}
	}

	// documentos do Patchouli (só os que vão no zip; os do FTB Quests saem em work/out)
	docs, _, err := readDocs(workDir)
	if err != nil {
		return rep, err
	}
	for _, d := range docs {
		if d.Rec.Kind != "patchouli" || d.Rec.Format != "json" {
			continue
		}
		b, err := read(d.Rec.Out)
		if err != nil {
			if os.IsNotExist(err) {
				rep.DocsMissing++
			} else {
				problem("%s: %v", d.Rec.Out, err)
			}
			continue
		}
		rep.Docs++
		on, err := relaxed.Parse(d.Orig)
		if err != nil {
			problem("%s: o original guardado não abre: %v", d.Rec.Out, err)
			rep.DocsBad++
			continue
		}
		pn, err := relaxed.Parse(b)
		if err != nil {
			problem("%s: o documento do pacote não abre como JSON: %v", d.Rec.Out, err)
			rep.DocsBad++
			continue
		}
		if diff := sameShape(on, pn, "", allowed[d.Rec.Out]); diff != "" {
			problem("%s: %s", d.Rec.Out, diff)
			rep.DocsBad++
		}
	}

	// arquivos de idioma .json do pacote
	var langNames []string
	for n := range files {
		if strings.HasPrefix(n, "assets/") && strings.HasSuffix(n, "/lang/pt_br.json") {
			langNames = append(langNames, n)
		}
	}
	sort.Strings(langNames)
	for _, n := range langNames {
		ns := strings.Split(n, "/")[1]
		b, err := read(n)
		if err != nil {
			problem("%s: %v", n, err)
			continue
		}
		rep.LangFiles++
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			problem("%s: não é um objeto JSON: %v", n, err)
			continue
		}
		bad := 0
		for k, v := range m {
			rep.Keys++
			if _, ok := v.(string); !ok {
				problem("%s: a chave %q não tem texto como valor", n, k)
			} else if len(langKeys[ns]) > 0 && !langKeys[ns][k] {
				bad++
				if bad <= 3 {
					problem("%s: a chave %q não existe no original", n, k)
				}
			}
		}
		if bad > 3 {
			problem("%s: mais %d chaves que não existem no original", n, bad-3)
		}
	}
	sort.Strings(rep.Problems)
	return rep, nil
}

// sameShape compara dois documentos: devem ter os mesmos objetos, as mesmas
// chaves (na mesma ordem), as mesmas listas (do mesmo tamanho) e os mesmos
// valores, menos nos textos cujo caminho está em allowed, que devem continuar
// sendo textos. Devolve a primeira diferença, ou vazio.
func sameShape(a, b *relaxed.Node, ptr string, allowed map[string]bool) string {
	if a.Kind != b.Kind {
		return fmt.Sprintf("em %s o tipo mudou", showPtr(ptr))
	}
	switch a.Kind {
	case relaxed.Obj:
		if len(a.Pairs) != len(b.Pairs) {
			return fmt.Sprintf("em %s o objeto tem %d chaves e devia ter %d", showPtr(ptr), len(b.Pairs), len(a.Pairs))
		}
		for i := range a.Pairs {
			if a.Pairs[i].Key != b.Pairs[i].Key {
				return fmt.Sprintf("em %s a chave %q virou %q", showPtr(ptr), a.Pairs[i].Key, b.Pairs[i].Key)
			}
			if d := sameShape(a.Pairs[i].Val, b.Pairs[i].Val, relaxed.Child(ptr, a.Pairs[i].Key), allowed); d != "" {
				return d
			}
		}
	case relaxed.Arr:
		if len(a.Items) != len(b.Items) {
			return fmt.Sprintf("em %s a lista tem %d itens e devia ter %d", showPtr(ptr), len(b.Items), len(a.Items))
		}
		for i := range a.Items {
			if d := sameShape(a.Items[i], b.Items[i], relaxed.Child(ptr, fmt.Sprint(i)), allowed); d != "" {
				return d
			}
		}
	case relaxed.Str:
		if a.Str != b.Str && !allowed[ptr] {
			return fmt.Sprintf("em %s um texto que não era para traduzir mudou: %s", showPtr(ptr), short(a.Str))
		}
	default:
		if !bytes.Equal([]byte(a.Raw), []byte(b.Raw)) {
			return fmt.Sprintf("em %s o valor mudou de %s para %s", showPtr(ptr), short(a.Raw), short(b.Raw))
		}
	}
	return ""
}

func showPtr(p string) string {
	if p == "" {
		return "(raiz)"
	}
	return p
}

func short(s string) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return string(r)
}
