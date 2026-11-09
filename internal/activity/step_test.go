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

func TestLoadFileEdits(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s.jsonl")
	lines := strings.Join([]string{
		`{"timestamp":"2026-01-10T10:00:00Z","message":{"content":[{"type":"tool_use","id":"w","name":"Write","input":{"file_path":"/x/notes.txt","content":"a\nb\n"}}]}}`,
		`{"timestamp":"2026-01-10T10:01:00Z","message":{"content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/x/notes.txt","old_string":"a","new_string":"A"}}]}}`,
		`{"timestamp":"2026-01-10T10:02:00Z","message":{"content":[{"type":"tool_use","id":"bad","name":"Edit","input":{"file_path":"/x/notes.txt","old_string":"zz","new_string":"q"}}]}}`,
		`{"message":{"content":[{"type":"tool_result","tool_use_id":"bad","is_error":true,"content":"not found"}]}}`,
		`{"timestamp":"2026-01-10T10:03:00Z","message":{"content":[{"type":"tool_use","id":"m","name":"MultiEdit","input":{"file_path":"/x/notes.txt","edits":[{"old_string":"b","new_string":"B"},{"old_string":"A","new_string":"A2"}]}}]}}`,
		`{"timestamp":"2026-01-10T10:04:00Z","message":{"content":[{"type":"tool_use","id":"o","name":"Edit","input":{"file_path":"/x/other.txt","old_string":"1","new_string":"2"}}]}}`,
	}, "\n") + "\n"
	_ = os.WriteFile(f, []byte(lines), 0o644)

	got := LoadFileEdits([]string{f}, "/x/notes.txt")
	if len(got) != 4 {
		t.Fatalf("got %d edits: %+v", len(got), got)
	}
	if !got[0].Content || strings.Join(got[0].New, ",") != "a,b" {
		t.Errorf("write = %+v", got[0])
	}
	if got[1].Old[0] != "a" || got[1].New[0] != "A" || got[2].New[0] != "B" || got[3].New[0] != "A2" {
		t.Errorf("edits = %+v", got)
	}
}
