package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	ts := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		line   string
		status Status
		check  func(t *testing.T, r Record)
	}{
		{
			name:   "blank line",
			line:   "   ",
			status: StatusBlank,
		},
		{
			name:   "not json",
			line:   "garbage here",
			status: StatusMalformed,
		},
		{
			name:   "truncated json",
			line:   `{"type":"assistant","message":{"id":"m"`,
			status: StatusMalformed,
		},
		{
			name:   "json array is not a record",
			line:   `[1,2,3]`,
			status: StatusMalformed,
		},
		{
			name:   "unknown record type is ignored cleanly",
			line:   `{"type":"shiny-new-thing","foo":{"bar":1}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Kind != KindOther {
					t.Fatalf("kind = %v", r.Kind)
				}
			},
		},
		{
			name:   "missing type",
			line:   `{"foo":1}`,
			status: StatusDegraded,
		},
		{
			name:   "assistant with usage and unknown fields",
			line:   `{"type":"assistant","uuid":"a","sessionId":"s","timestamp":"2026-01-10T10:00:00Z","requestId":"r1","newTopLevel":true,"message":{"id":"msg_a","model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"hi"},{"type":"mystery_block","data":1}],"usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":4,"output_tokens_details":{"thinking_tokens":1},"iterations":[{}]}}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				want := Usage{Input: 1, Output: 2, CacheCreate: 3, CacheRead: 4, Thinking: 1}
				if r.Usage != want || !r.HasUsage {
					t.Fatalf("usage = %+v", r.Usage)
				}
				if r.Model != "claude-x" || r.MessageID != "msg_a" || r.RequestID != "r1" || r.Text != "hi" {
					t.Fatalf("fields = %+v", r)
				}
				if !r.Timestamp.Equal(ts) {
					t.Fatalf("ts = %v", r.Timestamp)
				}
				if r.UsageKey() != "m:msg_a" {
					t.Fatalf("key = %s", r.UsageKey())
				}
			},
		},
		{
			name:   "numbers as strings and nulls are tolerated",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"claude-x","usage":{"input_tokens":"12","output_tokens":3.0,"cache_read_input_tokens":null,"cache_creation_input_tokens":"n/a"}}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Usage.Input != 12 || r.Usage.Output != 3 || r.Usage.CacheRead != 0 || r.Usage.CacheCreate != 0 {
					t.Fatalf("usage = %+v", r.Usage)
				}
			},
		},
		{
			name:   "assistant missing usage is degraded",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"claude-x","content":[]}}`,
			status: StatusDegraded,
		},
		{
			name:   "assistant missing model is degraded",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","usage":{"input_tokens":1,"output_tokens":1}}}`,
			status: StatusDegraded,
		},
		{
			name:   "assistant message of wrong type is degraded",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":"oops"}`,
			status: StatusDegraded,
		},
		{
			name:   "mistyped top-level field still yields a record",
			line:   `{"type":"assistant","isSidechain":"yes","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"claude-x","usage":{"input_tokens":1,"output_tokens":1}}}`,
			status: StatusDegraded,
			check: func(t *testing.T, r Record) {
				if r.Kind != KindAssistant || r.Usage.Input != 1 {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "bad timestamp is degraded",
			line:   `{"type":"assistant","timestamp":"yesterday","message":{"id":"m","model":"claude-x","usage":{"input_tokens":1,"output_tokens":1}}}`,
			status: StatusDegraded,
		},
		{
			name:   "synthetic model with zero usage is fine",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Model != SyntheticModel || !r.Usage.IsZero() {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "tool use extracts file path and command",
			line:   `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"claude-x","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"/a/b.go","content":"x"}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"make test"}},{"type":"tool_use","id":"t3","name":"Weird","input":"not an object"}]}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if len(r.ToolUses) != 3 {
					t.Fatalf("tool uses = %+v", r.ToolUses)
				}
				if r.ToolUses[0].FilePath != "/a/b.go" || r.ToolUses[1].Command != "make test" || r.ToolUses[2].Name != "Weird" {
					t.Fatalf("tool uses = %+v", r.ToolUses)
				}
			},
		},
		{
			name:   "human prompt as string",
			line:   `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"  fix the bug  "}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Prompt != "fix the bug" || !r.IsHumanPrompt() {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "human prompt as text blocks",
			line:   `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"line one"},{"type":"image","source":{}}]}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Prompt != "line one" {
					t.Fatalf("prompt = %q", r.Prompt)
				}
			},
		},
		{
			name:   "meta user text is not a human prompt",
			line:   `{"type":"user","isMeta":true,"timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"scheduled task text"}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.IsHumanPrompt() {
					t.Fatal("meta counted as human prompt")
				}
			},
		},
		{
			name:   "injected markup is not a prompt",
			line:   `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"<bash-stdout>ok</bash-stdout>"}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Prompt != "" || r.IsHumanPrompt() {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "slash command is captured",
			line:   `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"<command-name>/model</command-name><command-args>opus</command-args>"}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Command != "/model" || r.Prompt != "" {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "tool result error with block content",
			line:   `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"boom"}]}]}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if len(r.ToolResults) != 1 || !r.ToolResults[0].IsError || r.ToolResults[0].Text != "boom" || r.Prompt != "" {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "compact boundary",
			line:   `{"type":"system","subtype":"compact_boundary","timestamp":"2026-01-10T10:00:00Z","compactMetadata":{"preTokens":150000,"postTokens":8000}}`,
			status: StatusOK,
			check: func(t *testing.T, r Record) {
				if r.Kind != KindCompact || r.CompactPre != 150000 || r.CompactPost != 8000 {
					t.Fatalf("record = %+v", r)
				}
			},
		},
		{
			name:   "other system subtypes are ignored",
			line:   `{"type":"system","subtype":"turn_duration","durationMs":5}`,
			status: StatusOK,
		},
		{
			name:   "deeply odd but valid json",
			line:   `{"type":"user","message":{"content":[null,1,"x",{"type":"tool_result","content":{"nested":true}}]},"timestamp":"2026-01-10T10:00:00Z"}`,
			status: StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, st := ParseLine([]byte(tt.line))
			if st != tt.status {
				t.Fatalf("status = %v, want %v (record %+v)", st, tt.status, r)
			}
			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
}

func TestParseLineTruncatesLongToolResult(t *testing.T) {
	long := strings.Repeat("é", MaxResultText) // 2 bytes each
	line := `{"type":"user","timestamp":"2026-01-10T10:00:00Z","message":{"content":[{"type":"tool_result","content":"` + long + `"}]}}`
	r, st := ParseLine([]byte(line))
	if st != StatusOK || len(r.ToolResults) != 1 {
		t.Fatalf("status %v record %+v", st, r)
	}
	txt := r.ToolResults[0].Text
	if len(txt) > MaxResultText || !strings.HasPrefix(long, txt) {
		t.Fatalf("bad truncation: len %d", len(txt))
	}
}

// Fuzz-ish: random byte mutations of a valid line must never panic.
func TestParseLineNeverPanics(t *testing.T) {
	base := []byte(`{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"m","model":"x","content":[{"type":"tool_use","input":{"file_path":"a"}}],"usage":{"input_tokens":1}}}`)
	for i := range base {
		for _, c := range []byte{'"', '{', '}', '[', ']', ':', ',', 'x', 0, 0xff} {
			b := append([]byte(nil), base...)
			b[i] = c
			ParseLine(b)
			ParseLine(b[:i])
		}
	}
}

func FuzzParseLine(f *testing.F) {
	f.Add([]byte(`{"type":"assistant","message":{"id":"m","model":"x","usage":{"input_tokens":1}}}`))
	f.Add([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"x"}]}]}}`))
	f.Fuzz(func(t *testing.T, b []byte) { ParseLine(b) })
}

func TestFixtureFile(t *testing.T) {
	var recs []Record
	if err := ReadAll(filepath.Join("testdata", "session_basic.jsonl"), func(r Record) { recs = append(recs, r) }); err != nil {
		t.Fatal(err)
	}
	var assistants, prompts, compacts, errs int
	ids := map[string]bool{}
	for _, r := range recs {
		switch r.Kind {
		case KindAssistant:
			assistants++
			ids[r.UsageKey()] = true
		case KindCompact:
			compacts++
		case KindUser:
			if r.IsHumanPrompt() {
				prompts++
			}
			for _, tr := range r.ToolResults {
				if tr.IsError {
					errs++
				}
			}
		}
	}
	if assistants != 6 || len(ids) != 4 || prompts != 2 || compacts != 1 || errs != 1 {
		t.Fatalf("assistants=%d ids=%d prompts=%d compacts=%d errs=%d", assistants, len(ids), prompts, compacts, errs)
	}
}

func TestSourceFor(t *testing.T) {
	root := "/h/.claude/projects"
	tests := []struct {
		path string
		want Source
	}{
		{"/h/.claude/projects/-work-demo/abc.jsonl", Source{Project: "-work-demo", SessionID: "abc"}},
		{"/h/.claude/projects/-work-demo/abc/subagents/agent-1.jsonl", Source{Project: "-work-demo", SessionID: "abc", IsSubagent: true}},
	}
	for _, tt := range tests {
		got := SourceFor(root, tt.path)
		tt.want.Path = tt.path
		if got != tt.want {
			t.Errorf("SourceFor(%s) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestTailerIncremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	line := func(id string) string {
		return `{"type":"assistant","timestamp":"2026-01-10T10:00:00Z","message":{"id":"` + id + `","model":"x","usage":{"input_tokens":1,"output_tokens":1}}}`
	}
	tl := NewTailer()
	var got []string
	read := func() {
		t.Helper()
		if _, err := tl.Read(p, Source{}, func(r Record, _ Source) { got = append(got, r.MessageID) }); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		name   string
		write  string
		append bool
		want   []string
	}{
		{"first line", line("a") + "\n", false, []string{"a"}},
		{"partial line is held back", line("b")[:20], true, []string{"a"}},
		{"rest of partial line arrives", line("b")[20:] + "\n", true, []string{"a", "b"}},
		{"malformed line skipped", "{{{\n" + line("c") + "\n", true, []string{"a", "b", "c"}},
		{"truncation restarts from zero", line("z") + "\n", false, []string{"a", "b", "c", "z"}},
	}
	for _, s := range steps {
		flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if s.append {
			flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(p, flag, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s.write)
		f.Close()
		read()
		if strings.Join(got, ",") != strings.Join(s.want, ",") {
			t.Fatalf("%s: got %v want %v", s.name, got, s.want)
		}
	}
	if h := tl.Health(); h.Malformed != 1 {
		t.Fatalf("malformed = %d", h.Malformed)
	}
}

func TestHealthWarning(t *testing.T) {
	tests := []struct {
		name     string
		ok, bad  int
		wantWarn bool
	}{
		{"all good", 100, 0, false},
		{"one stray bad line", 400, 1, false},
		{"failing", 50, 20, true},
		{"everything failing", 0, 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h Health
			for i := 0; i < tt.ok; i++ {
				h.observe(StatusOK, "f", "")
			}
			for i := 0; i < tt.bad; i++ {
				h.observe(StatusMalformed, "f", "")
			}
			if (h.Warning() != "") != tt.wantWarn {
				t.Fatalf("warning = %q", h.Warning())
			}
		})
	}
}
