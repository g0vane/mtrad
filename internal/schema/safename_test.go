package schema

import "testing"

func TestSafeEntryName(t *testing.T) {
	good := []string{"assets/m/lang/pt_br.json", "pack.mcmeta", "config/ftbquests/quests/a.snbt", "assets/..x/lang/a.json"}
	bad := []string{"", "/etc/passwd", "../x", "a/../b", "a/./b", "a//b", "a\\b", "C:x", "assets/../lang/pt_br.json", "a/", "a\x00b", ".."}
	for _, n := range good {
		if !SafeEntryName(n) {
			t.Errorf("%q devia ser aceito", n)
		}
	}
	for _, n := range bad {
		if SafeEntryName(n) {
			t.Errorf("%q devia ser recusado", n)
		}
	}
}
