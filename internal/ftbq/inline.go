package ftbq

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"mtrad/internal/relaxed"
	"mtrad/internal/schema"
)

// Era 1 (FTB Quests 1.20.1 e anteriores): o texto fica dentro dos arquivos
// config/ftbquests/quests/{data.snbt,chapter_groups.snbt,chapters/*.snbt,
// reward_tables/*.snbt}. Conferido no código (FTB Quests 1.20.1/main, a74ee04):
// Quest.java e QuestObjectBase.java gravam "title", "subtitle" e "description";
// Chapter.java grava "subtitle" (lista) e "images" (cada uma com "hover").
//
// Como traduzir sem mudar o mod: cada texto vira "{chave}" no arquivo e o
// resource pack ganha a chave em en_us.json e pt_br.json. O FTB Library
// (1.20.1/main, TextComponentParser e ClientTextComponentUtils) troca todo
// "{...}" sem ":" por I18n.get(...) e lê o resultado de novo como texto rico.
// A chave leva o ID hexadecimal do objeto (missão, tarefa, recompensa,
// capítulo), não a posição na lista: reordenar missões não troca as chaves.

// LangNS é o namespace do resource pack que guarda as chaves das missões.
const LangNS = "mtrad_ftbq"

// KeyPrefix começa toda chave gerada.
const KeyPrefix = "mtrad.ftbq."

var (
	inlineFields     = map[string]bool{"title": true, "subtitle": true, "description": true, "hover": true}
	inlineContainers = map[string]bool{"quests": true, "tasks": true, "rewards": true, "images": true, "chapter_groups": true}
	reRichJSON       = regexp.MustCompile(`^\s*[{\[]\s*"`)
	reMarks          = regexp.MustCompile(`[&§]#[0-9a-fA-F]{6}|[&§][0-9a-fk-or]|\{[^}\n]*\}`)
	reBadKey         = regexp.MustCompile(`[^a-z0-9_.]`)
)

// hasText diz se, tirando as marcas, sobra alguma letra.
func hasText(s string) bool {
	for _, r := range reMarks.ReplaceAllString(s, "") {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// inlineFiles lista os arquivos de missões com texto inline, relativos à
// pasta do jogo.
func inlineFiles(game string) []string {
	root := filepath.Join(game, filepath.FromSlash(QuestsDir))
	var out []string
	for _, f := range []string{"data.snbt", "chapter_groups.snbt"} {
		if st, err := os.Stat(filepath.Join(root, f)); err == nil && st.Mode().IsRegular() {
			out = append(out, QuestsDir+"/"+f)
		}
	}
	for _, d := range []string{"chapters", "reward_tables"} {
		base := filepath.Join(root, d)
		_ = filepath.WalkDir(base, func(p string, e os.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() && strings.EqualFold(filepath.Ext(p), ".snbt") {
				r, _ := filepath.Rel(root, p)
				out = append(out, QuestsDir+"/"+filepath.ToSlash(r))
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

type inlineWalker struct {
	rel   string
	seen  map[string]string // chave -> arquivo que a usou primeiro (vale para todos os arquivos da execução)
	recs  []schema.StringRec
	rich  int
	dups  []string
	fbase string
	// collided: textos cuja chave já era de outro arquivo e foi desambiguada
	collided int
}

func (w *inlineWalker) emit(ptr, base, rel, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if reRichJSON.MatchString(s) {
		if n := w.emitRich(ptr, base, rel, s); n == 0 {
			w.rich++ // JSON que não abre ou sem nenhum texto traduzível
		}
		return
	}
	if !hasText(s) {
		return
	}
	key := KeyPrefix + base + "." + rel
	if owner, taken := w.seen[key]; taken {
		if owner == w.rel {
			w.dups = append(w.dups, key) // repetido dentro do mesmo arquivo
			return
		}
		// o mesmo ID de missão em outro arquivo (cópia do capítulo, pacote
		// montado por cima de outro): sem isto os dois textos ficariam com o
		// mesmo ID e um sobrescreveria o outro. A chave leva o arquivo junto.
		w.collided++
		key += "@" + w.fbase
		if _, again := w.seen[key]; again {
			w.dups = append(w.dups, key)
			return
		}
	}
	w.seen[key] = w.rel
	w.recs = append(w.recs, schema.StringRec{
		ID: schema.ID(ModName, ModName, key), Mod: ModName, NS: ModName, Key: key,
		En: s, EnSha: schema.Sha(s), Src: w.rel,
		Kind: schema.KindFTBQInl, Loc: &schema.Loc{Doc: w.rel, Ptr: ptr},
	})
}

// emitRich lê uma linha de JSON de texto rico e entrega os textos de dentro
// dela (campo "text", textos soltos em listas, "extra", "with" e o que o
// hoverEvent mostra). Devolve quantos textos entregou. O JSON em si não muda:
// o pack põe cada tradução de volta no mesmo lugar.
func (w *inlineWalker) emitRich(ptr, base, rel, line string) int {
	root, err := relaxed.Parse([]byte(line))
	if err != nil {
		return 0
	}
	n := 0
	var comp func(node *relaxed.Node, p string, inList bool)
	comp = func(node *relaxed.Node, p string, inList bool) {
		switch node.Kind {
		case relaxed.Str:
			if inList {
				n += w.richText(ptr, base, rel, p, node.Str)
			}
		case relaxed.Arr:
			for i, it := range node.Items {
				comp(it, relaxed.Child(p, strconv.Itoa(i)), true)
			}
		case relaxed.Obj:
			for _, pr := range node.Pairs {
				cp := relaxed.Child(p, pr.Key)
				switch pr.Key {
				case "text":
					if pr.Val.Kind == relaxed.Str {
						n += w.richText(ptr, base, rel, cp, pr.Val.Str)
					}
				case "extra", "with":
					if pr.Val.Kind == relaxed.Arr {
						comp(pr.Val, cp, false)
					}
				case "hoverEvent":
					if pr.Val.Kind == relaxed.Obj {
						for _, hv := range pr.Val.Pairs {
							if hv.Key == "contents" || hv.Key == "value" {
								comp(hv.Val, relaxed.Child(cp, hv.Key), true)
							}
						}
					}
				}
			}
		}
	}
	comp(root, "", root.Kind == relaxed.Arr)
	return n
}

// richText entrega um texto de dentro de um JSON rico, com a chave própria.
func (w *inlineWalker) richText(ptr, base, rel, inner, s string) int {
	if !hasText(s) {
		return 0
	}
	key := KeyPrefix + base + "." + rel + ".rich" + strings.ReplaceAll(inner, "/", ".")
	key = strings.ReplaceAll(key, "..", ".")
	if owner, taken := w.seen[key]; taken {
		if owner == w.rel {
			w.dups = append(w.dups, key)
			return 1
		}
		w.collided++
		key += "@" + w.fbase
		if _, again := w.seen[key]; again {
			w.dups = append(w.dups, key)
			return 1
		}
	}
	w.seen[key] = w.rel
	w.recs = append(w.recs, schema.StringRec{
		ID: schema.ID(ModName, ModName, key), Mod: ModName, NS: ModName, Key: key,
		En: s, EnSha: schema.Sha(s), Src: w.rel,
		Kind: schema.KindFTBQRich, Loc: &schema.Loc{Doc: w.rel, Ptr: ptr + "#" + inner},
	})
	return 1
}

func idBase(id string) string {
	return reBadKey.ReplaceAllString(strings.ToLower(strings.TrimSpace(id)), "_")
}

// obj percorre um objeto: base é o ID do dono do texto; rel é o caminho até
// ele quando o objeto não tem ID próprio (por exemplo "images.2").
func (w *inlineWalker) obj(n *relaxed.Node, ptr, base, rel string) {
	if idn := n.Get("/id"); idn != nil && idn.Kind == relaxed.Str && strings.TrimSpace(idn.Str) != "" {
		base, rel = idBase(idn.Str), ""
	}
	join := func(a, b string) string {
		if a == "" {
			return b
		}
		return a + "." + b
	}
	for _, p := range n.Pairs {
		cp := relaxed.Child(ptr, p.Key)
		switch {
		case inlineFields[p.Key]:
			switch p.Val.Kind {
			case relaxed.Str:
				w.emit(cp, base, join(rel, p.Key), p.Val.Str)
			case relaxed.Arr:
				for i, it := range p.Val.Items {
					if it.Kind == relaxed.Str {
						w.emit(relaxed.Child(cp, strconv.Itoa(i)), base, join(rel, p.Key)+"."+strconv.Itoa(i), it.Str)
					}
				}
			}
		case inlineContainers[p.Key] && p.Val.Kind == relaxed.Arr:
			for i, it := range p.Val.Items {
				if it.Kind == relaxed.Obj {
					w.obj(it, relaxed.Child(cp, strconv.Itoa(i)), base, join(rel, p.Key+"."+strconv.Itoa(i)))
				}
			}
		}
	}
}

// extractInline lê os capítulos e entrega textos (Kind ftbqi) e documentos.
func extractInline(game string, errs Sink, out Out, c *Counts) error {
	seen := map[string]string{}
	for _, rel := range inlineFiles(game) {
		b, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
		if err != nil {
			errs.Add(rel, "", err.Error())
			c.FileErrors++
			continue
		}
		root, err := relaxed.Parse(b)
		if err != nil {
			errs.Add(rel, "", "arquivo inválido: "+err.Error())
			c.FileErrors++
			continue
		}
		if root.Kind != relaxed.Obj {
			errs.Add(rel, "", "esperava um objeto")
			c.FileErrors++
			continue
		}
		base := "file." + idBase(strings.TrimSuffix(strings.TrimPrefix(rel, QuestsDir+"/"), path.Ext(rel)))
		base = strings.ReplaceAll(base, "/", "_")
		w := &inlineWalker{rel: rel, seen: seen, fbase: base}
		w.obj(root, "", base, "")
		if w.collided > 0 {
			errs.Add(rel, "", fmt.Sprintf("%d textos têm o mesmo ID de outro arquivo (capítulo copiado?); ficaram com a chave @%s para não se misturarem", w.collided, base))
		}
		if w.rich > 0 {
			errs.Add(rel, "", fmt.Sprintf("%d linhas em JSON de texto rico: não traduzidas (o FTB Quests não aceita {chave} nelas)", w.rich))
			c.Skipped += w.rich
		}
		if len(w.dups) > 0 {
			errs.Add(rel, "", fmt.Sprintf("%d textos com ID repetido, ignorados (ex.: %s)", len(w.dups), w.dups[0]))
			c.Skipped += len(w.dups)
		}
		if len(w.recs) == 0 {
			continue
		}
		c.Files++
		docID := schema.ID(ModName, schema.KindFTBQInl, rel)
		if err := out.Doc(schema.DocRec{ID: docID, Kind: schema.KindFTBQInl, Mod: ModName, Orig: "", Out: rel, Format: "snbt", Src: rel}, b); err != nil {
			return err
		}
		for _, r := range w.recs {
			c.Keys++
			if err := out.Str(r); err != nil {
				return err
			}
		}
	}
	return nil
}
