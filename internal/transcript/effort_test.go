package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastEffort(t *testing.T) {
	lines := []string{
		`{"type":"assistant","timestamp":"2026-11-08T00:11:09Z","sessionId":"s","effort":"medium","message":{"id":"a","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"assistant","timestamp":"2026-11-08T00:12:09Z","sessionId":"s","effort":"high","message":{"id":"b","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"assistant","timestamp":"2026-11-08T00:13:09Z","sessionId":"s","isSidechain":true,"effort":"low","message":{"id":"c","model":"claude-haiku-5","usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","timestamp":"2026-11-08T00:14:09Z","sessionId":"s","message":{"role":"user","content":"hi"}}`,
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LastEffort(p); got != "high" {
		t.Fatalf("LastEffort = %q, want high", got)
	}
	if got := LastEffort(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("missing file: %q", got)
	}
}

func TestParseEffort(t *testing.T) {
	r, st := ParseLine([]byte(`{"type":"assistant","timestamp":"2026-11-08T00:11:09Z","effort":"xhigh","message":{"id":"a","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}`))
	if st != StatusOK || r.Effort != "xhigh" {
		t.Fatalf("effort = %q, status %v", r.Effort, st)
	}
}
