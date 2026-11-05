package protect

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"mtrad/internal/schema"
)

// legacyMaskKind é a MaskKind de antes das classes, copiada como estava. O
// passo 1 promete não mudar nada do que ela devolvia (texto protegido, T e Raw).
func legacyMaskKind(text, style, kind string) (string, []schema.Token) {
	locs := reFor(kind).FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return text, nil
	}
	var b strings.Builder
	var toks []schema.Token
	last := 0
	for i, l := range locs {
		b.WriteString(text[last:l[0]])
		m := marker(style, i+1)
		b.WriteString(m)
		toks = append(toks, schema.Token{T: m, Raw: text[l[0]:l[1]]})
		last = l[1]
	}
	b.WriteString(text[last:])
	return b.String(), toks
}

var allKinds = []string{"", schema.KindPatchouli, schema.KindFTBQ, schema.KindFTBQInl, schema.KindFTBQLang, schema.KindFTBQRich}

// os 12 casos do documento de pesquisa, mais os exemplos do FTB Quests
var extraSamples = []string{
	"Collect &6Dried Earth&r from the desert.",
	"§7Right click to open the §bbackpack§7.",
	"Stored: %s / %s buckets",
	"Hold [Shift] to show %s",
	"%1$s was slain by %2$s",
	"$(l:basics)Basics$(/l) are the first step.$(br)Read on.",
	"&#FFAA00Welcome!&r Click {image:ftbquests:textures/logo.png width:100 height:50}",
	"%s item(s)",
	"§a%s§r",
	"Page one{@pagebreak}Page two",
	"&6Gold &lbold&r and {open_url:https://example.com}",
	"$(br2)end$()",
}

func equivalent(t *testing.T, text string) {
	t.Helper()
	for _, kind := range allKinds {
		for _, style := range []string{StyleBrackets, StyleXML, StyleCurly} {
			wm, wt := legacyMaskKind(text, style, kind)
			gm, gt := MaskKind(text, style, kind)
			if wm != gm || len(wt) != len(gt) {
				t.Fatalf("kind %q style %s %q: texto protegido mudou: %q -> %q", kind, style, text, wm, gm)
			}
			for i := range wt {
				if wt[i].T != gt[i].T || wt[i].Raw != gt[i].Raw {
					t.Fatalf("kind %q %q: marca %d mudou: %+v -> %+v", kind, text, i, wt[i], gt[i])
				}
				if gt[i].Class == "" {
					t.Fatalf("kind %q %q: marca %q sem classe", kind, text, gt[i].Raw)
				}
			}
		}
	}
}

func TestClassesNaoMudamOTextoProtegido(t *testing.T) {
	for _, s := range append(append([]string{}, samples...), extraSamples...) {
		equivalent(t, s)
	}
}

// Textos montados ao acaso com pedaços de marca e de palavra: a nova MaskKind
// precisa dar o mesmo resultado que a antiga em todos.
func TestClassesEquivalenciaAleatoria(t *testing.T) {
	frags := []string{"%s", "%d", "%1$s", "%2$d", "%%", "§a", "§r", "§l", "§x", "&6", "&r", "&#FFAA00", "<b>", "</b>", "<br/>",
		"{0}", "{nome}", "{}", "{image:a.png}", "{@pagebreak}", "$(br)", "$(l:x)", "$(/l)", "$()", "https://a.b/c", `\n`, "\n", "\r\n",
		"Word", " ", "  ", "é", "ão", "[Shift]", "1", "(s)", "&", "§", "%", "$", "{", "<", ">"}
	rng := rand.New(rand.NewSource(20261104))
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for n := rng.Intn(9); n >= 0; n-- {
			b.WriteString(frags[rng.Intn(len(frags))])
		}
		equivalent(t, b.String())
	}
}

func TestClassificacao(t *testing.T) {
	cases := []struct{ text, kind, raw, class string }{
		{"Hello %s", "", "%s", ClassPlaceholder},
		{"%1$s x", "", "%1$s", ClassPlaceholder},
		{"%.2f", "", "%.2f", ClassPlaceholder},
		{"{0}", "", "{0}", ClassPlaceholder},
		{"{player_name}", "", "{player_name}", ClassPlaceholder},
		{"§aok", "", "§a", ClassStyle},
		{"§lok", "", "§l", ClassStyle},
		{"ok§r", "", "§r", ClassReset},
		{"ok§R", "", "§R", ClassReset},
		{"&6x", schema.KindFTBQ, "&6", ClassStyle},
		{"&rx", schema.KindFTBQ, "&r", ClassReset},
		{"&#FFAA00x", schema.KindFTBQ, "&#FFAA00", ClassStyle},
		{"<b>x", "", "<b>", ClassTagOpen},
		{"x</b>", "", "</b>", ClassTagClose},
		{"x<br/>", "", "<br/>", ClassTagVoid},
		{"x<lang:key.name/>", "", "<lang:key.name/>", ClassTagVoid},
		{"a$(br)b", schema.KindPatchouli, "$(br)", ClassBreak},
		{"a$(br2)b", schema.KindPatchouli, "$(br2)", ClassBreak},
		{"$(l:basics)x", schema.KindPatchouli, "$(l:basics)", ClassMacro},
		{"a{@pagebreak}b", schema.KindFTBQ, "{@pagebreak}", ClassBreak},
		{"{image:x.png}", schema.KindFTBQ, "{image:x.png}", ClassCommand},
		{"{open_url:https://e.com}", schema.KindFTBQ, "{open_url:https://e.com}", ClassCommand},
		{"see https://example.com/a?b=1 now", "", "https://example.com/a?b=1", ClassURL},
		{`a\nb`, "", `\n`, ClassBreak},
		{"a\nb", "", "\n", ClassBreak},
		{"a\r\nb", "", "\r\n", ClassBreak},
	}
	for _, c := range cases {
		ms := Marks(c.text, c.kind)
		var got *Mark
		for i := range ms {
			if ms[i].Raw == c.raw {
				got = &ms[i]
			}
		}
		if got == nil {
			t.Errorf("%q: marca %q não achada (achou %+v)", c.text, c.raw, ms)
			continue
		}
		if got.Class != c.class {
			t.Errorf("%q: %q é %q, esperado %q", c.text, c.raw, got.Class, c.class)
		}
		if c.text[got.Start:got.End] != got.Raw {
			t.Errorf("%q: posição não bate com o trecho: %+v", c.text, got)
		}
	}
}

// Só macros do Patchouli podem cair na classe genérica: qualquer outra marca
// sem classe própria seria um padrão novo que classify ainda não conhece.
func TestSoMacroFicaGenerica(t *testing.T) {
	for _, s := range append(append([]string{}, samples...), extraSamples...) {
		for _, kind := range allKinds {
			for _, m := range Marks(s, kind) {
				if m.Class == ClassMacro && !strings.HasPrefix(m.Raw, "$(") {
					t.Errorf("kind %q %q: %q caiu em macro", kind, s, m.Raw)
				}
			}
		}
	}
}

func TestTokenAntigoSemClasseContinuaLendo(t *testing.T) {
	var tok schema.Token
	if err := json.Unmarshal([]byte(`{"t":"⟦1⟧","raw":"%s"}`), &tok); err != nil || tok.T != "⟦1⟧" || tok.Raw != "%s" || tok.Class != "" {
		t.Fatalf("token antigo: %+v %v", tok, err)
	}
	_, toks := Mask("a %s", StyleBrackets)
	b, _ := json.Marshal(toks[0])
	if !strings.Contains(string(b), `"class":"placeholder"`) {
		t.Fatalf("faltou a classe no JSON: %s", b)
	}
	b, _ = json.Marshal(schema.Token{T: "⟦1⟧", Raw: "%s"})
	if strings.Contains(string(b), "class") {
		t.Fatalf("classe vazia não devia sair no JSON: %s", b)
	}
}
