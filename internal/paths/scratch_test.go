package paths

import "testing"

func TestIsScratchpad(t *testing.T) {
	const pad = "/tmp/claude-1000/-mnt-repo/9dc527ab-eabc-49a1-8d31-ca0e63b3e824/scratchpad"
	for p, want := range map[string]bool{
		pad:                      true,
		pad + "/notes.md":        true,
		pad + "/a/b.go":          true,
		pad + "-other/x":         false,
		"/mnt/repo/scratchpad/x": false,
		"/tmp/claude-1000/x.md":  false,
		"":                       false,
	} {
		if got := IsScratchpad(p); got != want {
			t.Errorf("IsScratchpad(%q) = %v, want %v", p, got, want)
		}
	}
}
