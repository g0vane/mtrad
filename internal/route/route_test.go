package route

import (
	"path/filepath"
	"testing"

	"mtrad/internal/schema"
)

func rec(masked string, n int) schema.ProtectedRec {
	return schema.ProtectedRec{ID: masked, Masked: masked, Tokens: make([]schema.Token, n)}
}

func TestChoose(t *testing.T) {
	c := Config{Default: "llm", Rules: []Rule{
		{MaxChars: 30, MaxTokens: 0, Engine: "ct2"},
		{MinTokens: 4, Engine: "llm-strong"},
	}}
	if got := Choose(c, rec("Short", 0)); got != "ct2" {
		t.Fatal(got)
	}
	if got := Choose(c, rec("A text that is certainly longer than thirty characters", 1)); got != "llm" {
		t.Fatal(got)
	}
	if got := Choose(c, rec("Long enough text, that is longer than thirty chars", 5)); got != "llm-strong" {
		t.Fatal(got)
	}
	if got := Choose(c, rec("ação", 0)); got != "ct2" {
		t.Fatalf("conta caracteres, não bytes: %s", got)
	}
}

func TestValidate(t *testing.T) {
	if (Config{}).Validate() == nil || (Config{Default: "a", Rules: []Rule{{}}}).Validate() == nil {
		t.Fatal("esperava erro")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	w, _ := schema.Create(filepath.Join(dir, "protected.jsonl"), schema.VProtected)
	_ = w.Write(rec("a", 0))
	_ = w.Write(rec("a very long text that goes past the limit of thirty", 0))
	w.Close()
	counts, err := Run(dir, Config{Default: "llm", Rules: []Rule{{MaxChars: 30, Engine: "ct2"}}})
	if err != nil || counts["ct2"] != 1 || counts["llm"] != 1 {
		t.Fatalf("%v %v", err, counts)
	}
	got, err := schema.ReadAll[schema.RoutedRec](filepath.Join(dir, "routed.jsonl"), schema.VRouted)
	if err != nil || len(got) != 2 || got[0].Engine != "ct2" {
		t.Fatalf("%v %+v", err, got)
	}
}
