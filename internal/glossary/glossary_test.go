package glossary

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewVersionAndOrder(t *testing.T) {
	a := New([]Term{{"Iron Ingot", "Barra de ferro"}, {"Iron", "Ferro"}, {"", "x"}})
	b := New([]Term{{"Iron", "Ferro"}, {"Iron Ingot", "Barra de ferro"}})
	if a.Version != b.Version || len(a.Terms) != 2 || a.Terms[0].En != "Iron Ingot" {
		t.Fatalf("%+v %+v", a, b)
	}
	c := New([]Term{{"Iron", "Ferro!"}, {"Iron Ingot", "Barra de ferro"}})
	if c.Version == a.Version {
		t.Fatal("mudar um termo deve mudar a versão")
	}
}

func TestFindWholeWord(t *testing.T) {
	g := New([]Term{{"Iron", "Ferro"}, {"Creeper", "Creeper"}, {"Ore", "Minério"}})
	got := g.Find("An iron golem hates a CREEPER; more ore, but not Core or Ironwood")
	names := map[string]bool{}
	for _, t := range got {
		names[t.En] = true
	}
	if !names["Iron"] || !names["Creeper"] || !names["Ore"] || len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if len(g.Find("Core Ironwood")) != 0 {
		t.Fatal("não deve casar no meio de palavra")
	}
}

func TestMissing(t *testing.T) {
	g := New([]Term{{"Iron", "Ferro"}})
	if m := g.Missing("Iron sword", "Espada de ferro"); len(m) != 0 {
		t.Fatalf("%+v", m)
	}
	if m := g.Missing("Iron sword", "Espada de aço"); len(m) != 1 {
		t.Fatalf("%+v", m)
	}
}

func TestParseTSVAndLang(t *testing.T) {
	ts, err := ParseTSV([]byte("# c\nIron\tFerro\n\nGold\tOuro\r\n"))
	if err != nil || len(ts) != 2 {
		t.Fatalf("%v %+v", err, ts)
	}
	if _, err := ParseTSV([]byte("sem tab")); err == nil {
		t.Fatal("esperava erro")
	}
	en := `{"item.minecraft.iron_ingot":"Iron Ingot","block.minecraft.stone":"Stone","item.minecraft.x.desc":"Long text here","chat.type":"Hi","entity.minecraft.cow":"Cow","item.minecraft.p":"Same","item.minecraft.f":"Has %s"}`
	pt := `{"item.minecraft.iron_ingot":"Barra de ferro","block.minecraft.stone":"Pedra","chat.type":"Oi","entity.minecraft.cow":"Vaca","item.minecraft.p":"Same","item.minecraft.f":"Tem %s"}`
	terms, err := FromLangFiles([]byte(en), []byte(pt))
	if err != nil || len(terms) != 3 {
		t.Fatalf("%v %+v", err, terms)
	}
}

func TestSaveLoad(t *testing.T) {
	g := New([]Term{{"A", "B"}})
	p := filepath.Join(t.TempDir(), "g.json")
	if err := g.Save(p); err != nil {
		t.Fatal(err)
	}
	h, err := Load(p)
	if err != nil || h.Version != g.Version || len(h.Terms) != 1 {
		t.Fatalf("%v %+v", err, h)
	}
}

func TestMissingMenosRuido(t *testing.T) {
	g := New([]Term{{"Iron", "Ferro"}, {"Iron Ingot", "Lingote de ferro"}, {"Anvil", "Bigorna"}, {"Press", "Prensa"}, {"Pickaxe", "Picareta"}})
	cases := []struct {
		name, en, pt string
		want         []string
	}{
		{"termo longo respeitado cobre o curto", "Iron Ingot", "Lingote de ferro", nil},
		{"longo não respeitado: só o longo avisa", "Iron Ingot", "Barra metálica", []string{"Iron Ingot"}},
		{"acento e maiúscula", "Use the Pickaxe", "Use a PICARETA", nil},
		{"termo solto ausente: avisa", "Place the anvil here", "Coloque a peça aqui", []string{"Anvil"}},
		{"nome próprio de mod: não avisa", "Create Mechanical Press", "Create Prensa Mecânica Nova", nil},
		{"nome próprio sem tradução: não avisa", "Applied Energistics Press Mod", "Applied Energistics Press Mod", nil},
		{"capitalizado solto no meio da frase: avisa", "Place the Anvil on the ground", "Coloque a coisa no chão", []string{"Anvil"}},
		{"começo de frase com palavra comum: avisa", "The Anvil is heavy", "A coisa é pesada", []string{"Anvil"}},
	}
	for _, c := range cases {
		var got []string
		for _, m := range g.Missing(c.en, c.pt) {
			got = append(got, m.En)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, queria %v", c.name, got, c.want)
		}
	}
}
