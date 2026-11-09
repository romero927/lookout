package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSteps(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "-p")
	_ = os.MkdirAll(filepath.Join(proj, "s1", "subagents"), 0o755)
	main := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_a","name":"Bash","input":{"command":"ls -la\npwd"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"file1\nfile2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_b","name":"Read","input":{"file_path":"/x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_b","is_error":true,"content":[{"type":"text","text":"no such file"},{"type":"image"}]}]}}`,
	}, "\n") + "\n"
	_ = os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(main), 0o644)
	sub := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_c","name":"Grep","input":{"pattern":"x"}}]}}` + "\n"
	_ = os.WriteFile(filepath.Join(proj, "s1", "subagents", "agent-1.jsonl"), []byte(sub), 0o644)

	files := SessionFiles(dir, "-p", "s1")
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	got := LoadSteps(files, []string{"toolu_a", "toolu_b", "toolu_c", "toolu_missing"})
	if a := got["toolu_a"]; a.Input["command"] != "ls -la\npwd" || a.Output != "file1\nfile2" || !a.Done || a.Error {
		t.Errorf("a = %+v", a)
	}
	if b := got["toolu_b"]; b.Output != "no such file\n[image]" || !b.Error {
		t.Errorf("b = %+v", b)
	}
	if c := got["toolu_c"]; c.Input["pattern"] != "x" || c.Done {
		t.Errorf("c (still running, in a subagent) = %+v", c)
	}
	if _, ok := got["toolu_missing"]; ok {
		t.Error("missing ID should be left out")
	}
}
