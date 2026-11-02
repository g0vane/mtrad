package protect

import (
	"strings"
	"testing"
)

var samples = []string{
	"Hello %s, you have %d items",
	"%1$s killed %2$s with %3$s",
	"§aGreen §lbold§r normal",
	"Line one\nLine two\r\nLine three",
	"Literal\\nbackslash n",
	"$(l:patchouli:basics)Click here$(/l) and $(br)new line",
	"<b>Bold</b> and <color:red>red</color> and <lang:key.name/>",
	"Visit https://example.com/path?a=1&b=2 now",
	"Use {0} and {player_name} and {}",
	"100%% sure, 50% off, 3 < 4 > 2",
	"%.2f%% of %5d (%-10s)",
	"Plain text without anything",
	"",
	"§x§1§2§3§4§5§6Hex color",
	"Mixed: %s§a%s§r\n<i>x</i>",
}

func TestRoundTripAllStyles(t *testing.T) {
	for _, style := range []string{StyleBrackets, StyleXML, StyleCurly} {
		for _, s := range samples {
			masked, toks := Mask(s, style)
			back, problems := Unmask(masked, toks, style)
			if back != s || len(problems) != 0 {
				t.Errorf("%s %q: voltou %q problemas %v", style, s, back, problems)
			}
		}
	}
}

func TestMaskContents(t *testing.T) {
	masked, toks := Mask("Hello %s, §aok§r\n", StyleBrackets)
	if masked != "Hello ⟦1⟧, ⟦2⟧ok⟦3⟧⟦4⟧" {
		t.Fatalf("%q", masked)
	}
	if len(toks) != 4 || toks[0].Raw != "%s" || toks[3].Raw != "\n" {
		t.Fatalf("%+v", toks)
	}
	// "50% off" e "3 < 4 > 2" não são marcas
	m, toks := Mask("50% off, 3 < 4 > 2", StyleBrackets)
	if m != "50% off, 3 < 4 > 2" || len(toks) != 0 {
		t.Fatalf("%q %+v", m, toks)
	}
	if m, toks := Mask("100%% sure", StyleBrackets); !strings.HasPrefix(m, "100⟦1⟧") || toks[0].Raw != "%%" {
		t.Fatalf("%q %+v", m, toks)
	}
}

func TestUnmaskProblems(t *testing.T) {
	_, toks := Mask("A %s B %d C", StyleBrackets)
	// motor perdeu uma, repetiu outra e inventou uma
	_, p := Unmask("A ⟦1⟧ ⟦1⟧ C ⟦9⟧", toks, StyleBrackets)
	joined := strings.Join(p, "|")
	for _, want := range []string{"perdida ⟦2⟧", "repetida ⟦1⟧", "desconhecida ⟦9⟧"} {
		if !strings.Contains(joined, want) {
			t.Errorf("faltou %q em %v", want, p)
		}
	}
	// espaços e ordem diferentes são aceitos
	out, p := Unmask("⟦2⟧ antes ⟦1⟧", toks, StyleBrackets)
	if len(p) != 0 || out != "%d antes %s" {
		t.Fatalf("%q %v", out, p)
	}
}

func TestValidStyle(t *testing.T) {
	if !ValidStyle("xml") || ValidStyle("x") {
		t.Fatal()
	}
}

func TestMaskKindFTBQ(t *testing.T) {
	en := "&aFirst &6quest&r {image:ftbquests:textures/a.png} see §a"
	masked, toks := MaskKind(en, StyleBrackets, "ftbq")
	// &a, &6, &r, {image...}, §a
	if len(toks) != 5 || strings.Contains(masked, "&a") || strings.Contains(masked, "image") {
		t.Fatalf("%q %v", masked, toks)
	}
	back, problems := Unmask(masked, toks, StyleBrackets)
	if back != en || len(problems) != 0 {
		t.Fatalf("%q %v", back, problems)
	}
	// sem a fonte, & e {image:..} ficam como texto
	m2, t2 := MaskKind(en, StyleBrackets, "")
	if len(t2) != 1 || !strings.Contains(m2, "&a") {
		t.Fatalf("%q %v", m2, t2)
	}
	// Patchouli usa as marcas de base ($(...))
	m3, t3 := MaskKind("A $(l:x)b$(/l)", StyleBrackets, "patchouli")
	if len(t3) != 2 || m3 != "A ⟦1⟧b⟦2⟧" {
		t.Fatalf("%q %v", m3, t3)
	}
}

func TestMaskKindFTBQHexColorAndPagebreak(t *testing.T) {
	for _, kind := range []string{"ftbq", "ftbqi"} {
		en := "&#FF8800Ouro&r e §#00ff00verde {@pagebreak}"
		masked, toks := MaskKind(en, StyleBrackets, kind)
		// &#FF8800, &r, §#00ff00, {@pagebreak}
		if len(toks) != 4 || strings.Contains(masked, "FF8800") || strings.Contains(masked, "pagebreak") {
			t.Fatalf("%s: %q %v", kind, masked, toks)
		}
		back, problems := Unmask(masked, toks, StyleBrackets)
		if back != en || len(problems) != 0 {
			t.Fatalf("%s: %q %v", kind, back, problems)
		}
	}
}

func TestJavaDateAndRelativeFormats(t *testing.T) {
	for _, s := range []string{"%tY", "%1$tB", "%<s", "%2$d", "%.2f", "%%"} {
		loc := tokenRe.FindString("x " + s + " y")
		if loc != s {
			t.Errorf("%q: casou %q", s, loc)
		}
	}
}
