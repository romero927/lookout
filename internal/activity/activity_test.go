package activity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

func TestEngineTracksFilesAndTurns(t *testing.T) {
	e := New()
	src := transcript.Source{SessionID: "s1"}
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	user := func(d time.Duration, prompt string) transcript.Record {
		return transcript.Record{Kind: transcript.KindUser, SessionID: "s1", Timestamp: at.Add(d), Prompt: prompt}
	}
	asst := func(d time.Duration, id string, tools ...transcript.ToolUse) transcript.Record {
		return transcript.Record{Kind: transcript.KindAssistant, SessionID: "s1", Timestamp: at.Add(d), MessageID: id,
			Model: "claude-opus-x", HasUsage: true, Usage: transcript.Usage{Output: 100}, ToolUses: tools, Text: "done " + id}
	}
	result := func(id string, isErr bool) transcript.Record {
		return transcript.Record{Kind: transcript.KindUser, SessionID: "s1", ToolResults: []transcript.ToolResult{{ToolUseID: id, IsError: isErr}}}
	}

	e.Observe(user(0, "add a feature\nwith details"), src)
	e.Observe(asst(time.Minute, "m1",
		transcript.ToolUse{ID: "t1", Name: "Edit", FilePath: "/p/a.go", Added: 3, Removed: 1},
		transcript.ToolUse{ID: "t2", Name: "Bash", Command: "go test ./...\nsecond line"}), src)
	e.Observe(result("t1", false), src)
	e.Observe(result("t2", true), src)
	e.Observe(asst(2*time.Minute, "m2", transcript.ToolUse{ID: "t3", Name: "Edit", FilePath: "/p/a.go", Added: 5, Removed: 5}), src)
	e.Observe(result("t3", true), src) // failed edit: not counted
	// The same tool call seen again (e.g. a resumed transcript) is ignored.
	e.Observe(asst(2*time.Minute, "m2", transcript.ToolUse{ID: "t3", Name: "Edit", FilePath: "/p/a.go", Added: 5, Removed: 5}), src)
	// A subagent writes a file.
	e.Observe(transcript.Record{Kind: transcript.KindAssistant, SessionID: "s1", Sidechain: true, Timestamp: at.Add(3 * time.Minute),
		MessageID: "sub", Model: "claude-haiku-x", ToolUses: []transcript.ToolUse{{ID: "t4", Name: "Write", FilePath: "/p/b.go", Added: 10, Whole: true}}},
		transcript.Source{SessionID: "s1", IsSubagent: true})
	e.Observe(user(4*time.Minute, "next"), src)

	s := e.Session("s1")
	if len(s.Turns) != 2 || s.Turns[0].Prompt != "next" {
		t.Fatalf("turns: %+v", s.Turns)
	}
	t1 := s.Turns[1]
	var tools []Call
	texts := 0
	for _, c := range t1.Calls {
		if c.Name == "" {
			texts++
		} else {
			tools = append(tools, c)
		}
	}
	if len(tools) != 4 || t1.Tools != 4 || t1.Failed != 2 || !tools[1].Fail || tools[1].Summary != "go test ./..." || !tools[3].Subagent {
		t.Errorf("calls: %+v failed %d", t1.Calls, t1.Failed)
	}
	if texts != 2 || t1.Calls[0].Text != "done m1" {
		t.Errorf("messages in steps: %d, first %q", texts, t1.Calls[0].Text)
	}
	if t1.Output != 200 || t1.Reply != "done m2" || t1.End != at.Add(3*time.Minute) || len(t1.Files) != 2 {
		t.Errorf("turn: output %d reply %q end %v files %v", t1.Output, t1.Reply, t1.End, t1.Files)
	}
	if len(s.Files) != 2 || s.Files[0].Path != "/p/b.go" || !s.Files[0].Written || !s.Files[0].Subagent {
		t.Fatalf("files: %+v", s.Files)
	}
	a := s.Files[1]
	if a.Edits != 1 || a.Failed != 1 || a.Added != 3 || a.Removed != 1 {
		t.Errorf("a.go: %+v", a)
	}
	if got := e.Session("missing"); len(got.Turns) != 0 || len(got.Files) != 0 {
		t.Errorf("unknown session: %+v", got)
	}
}

func TestGitDiffAndWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("a.txt", "one\ntwo\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")

	if d, note := Diff(filepath.Join(dir, "a.txt"), time.Time{}); d != "" || !strings.Contains(note, "No uncommitted") {
		t.Errorf("clean file: %q %q", d, note)
	}
	write("a.txt", "one\nTWO\n")
	write("sub/new.txt", "x\n")
	if d, _ := Diff(filepath.Join(dir, "a.txt"), time.Time{}); !strings.Contains(d, "+TWO") || !strings.Contains(d, "-two") {
		t.Errorf("diff: %q", d)
	}
	if d, _ := Diff(filepath.Join(dir, "sub", "new.txt"), time.Time{}); !strings.Contains(d, "not yet tracked") || !strings.Contains(d, "+x") {
		t.Errorf("untracked file: %q", d)
	}
	items, top, err := WorkingTree(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if top != real && top != dir {
		t.Errorf("top %q, want %q", top, dir)
	}
	got := map[string]string{}
	for _, it := range items {
		got[filepath.Base(it.Path)] = it.Status
	}
	if got["a.txt"] != " M" || got["new.txt"] != "??" {
		t.Errorf("working tree: %v", got)
	}
	// After committing, the change shows as committed since the session start.
	start := time.Now().Add(-time.Hour)
	run("add", ".")
	run("commit", "-q", "-m", "change a")
	if d, _ := Diff(filepath.Join(dir, "a.txt"), start); !strings.Contains(d, "commit ") || !strings.Contains(d, "+TWO") {
		t.Errorf("committed diff: %q", d)
	}
	committed, err := CommittedSince(top, start)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, c := range committed {
		names[filepath.Base(c.Path)] = c.Status
	}
	if names["a.txt"] != "M" || names["new.txt"] != "A" || len(committed[0].Commit) != 40 {
		t.Errorf("committed since: %v", names)
	}
	if _, note := Diff(filepath.Join(t.TempDir(), "x.txt"), time.Time{}); !strings.Contains(note, "Not in a git") {
		t.Errorf("outside repo: %q", note)
	}
}

func TestContextBreakdown(t *testing.T) {
	e := New()
	src := transcript.Source{SessionID: "c"}
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	rec := func(r transcript.Record) { r.SessionID = "c"; e.Observe(r, src) }

	rec(transcript.Record{Kind: transcript.KindUser, Timestamp: at, Prompt: "read the file", Size: 400})
	// First response: 20,100 tokens in context, so startup is about 20,000.
	rec(transcript.Record{Kind: transcript.KindAssistant, Timestamp: at.Add(time.Second), Model: "claude-x", HasUsage: true,
		Usage:    transcript.Usage{Input: 100, CacheCreate: 20000},
		ToolUses: []transcript.ToolUse{{ID: "r1", Name: "Read", FilePath: "/p/big.go", InputSize: 40}}})
	rec(transcript.Record{Kind: transcript.KindUser, Timestamp: at.Add(2 * time.Second),
		ToolResults: []transcript.ToolResult{{ToolUseID: "r1", Size: 40000}}})
	rec(transcript.Record{Kind: transcript.KindAssistant, Timestamp: at.Add(3 * time.Second), Model: "claude-x", HasUsage: true,
		Usage: transcript.Usage{Input: 10, CacheRead: 20100, CacheCreate: 10200}, Text: "It's a big file."})

	v := e.Context("c", 5)
	if v.Total != 30310 || v.Startup != 20000 || v.Estimated {
		t.Fatalf("view: %+v", v)
	}
	if v.Parts[0].Name != "Read output" || v.Parts[0].Tokens != 10000 {
		t.Errorf("parts: %+v", v.Parts)
	}
	if v.Largest[0].Label != "big.go" || v.Largest[0].Tool != "Read" {
		t.Errorf("largest: %+v", v.Largest)
	}
	if v.Other < 0 || v.Other > 400 {
		t.Errorf("other: %d", v.Other)
	}

	// Compaction starts over.
	rec(transcript.Record{Kind: transcript.KindCompact, Timestamp: at.Add(time.Minute), CompactPre: 30310, CompactPost: 5000})
	rec(transcript.Record{Kind: transcript.KindAssistant, Timestamp: at.Add(2 * time.Minute), Model: "claude-x", HasUsage: true,
		Usage: transcript.Usage{Input: 10, CacheCreate: 25000}})
	v = e.Context("c", 5)
	if !v.Compacted || v.Startup != 25010 || len(v.Parts) != 0 {
		t.Errorf("after compact: %+v", v)
	}
}
