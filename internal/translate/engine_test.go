package translate

import "testing"

func TestMatchEdgesTrimsNewlines(t *testing.T) {
	if got := matchEdges("a b", "\n  olá mundo \r\n"); got != "olá mundo" {
		t.Fatalf("%q", got)
	}
	if got := matchEdges("  a ", "olá\n"); got != "  olá " {
		t.Fatalf("%q", got)
	}
}
