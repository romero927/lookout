package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/strategy"
)

func TestSuggestionLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	up := strategy.Suggestion{SessionID: "s1", Current: "claude-sonnet-x", Suggested: "opus", Action: strategy.Upgrade, Reason: "r", At: t0}

	steps := []struct {
		name  string
		sg    strategy.Suggestion
		wrote bool
	}{
		{"first suggestion logged", up, true},
		{"identical suggestion skipped", withAt(up, t0.Add(time.Second)), false},
		{"same call with new reason text skipped", withReason(withAt(up, t0.Add(1500*time.Millisecond)), "r2"), false},
		{"changed suggestion logged", strategy.Suggestion{SessionID: "s1", Current: "claude-sonnet-x", Suggested: "claude-sonnet-x", Action: strategy.Keep, Reason: "calm", At: t0.Add(2 * time.Second)}, true},
		{"other session logged", withAt(withSession(up, "s2"), t0.Add(3*time.Second)), true},
		{"empty session ignored", strategy.Suggestion{Current: "x"}, false},
	}
	for _, st := range steps {
		got, err := s.LogSuggestion(st.sg)
		if err != nil || got != st.wrote {
			t.Fatalf("%s: wrote=%v err=%v", st.name, got, err)
		}
	}

	// The user switched to opus; first response after the suggestions.
	if err := s.ResolveNext("s1", "claude-opus-x", "", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A later response must not overwrite the recorded next model.
	_ = s.ResolveNext("s1", "claude-haiku-x", "", t0.Add(2*time.Minute))

	rows, err := s.Recent(10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	for _, r := range rows {
		if r.SessionID == "s1" && r.NextModel != "claude-opus-x" {
			t.Fatalf("row %+v not resolved to opus", r)
		}
		if r.SessionID == "s2" && r.NextModel != "" {
			t.Fatalf("s2 resolved: %+v", r)
		}
	}
	resolved, followed, _, err := s.Adherence()
	if err != nil || resolved != 1 || followed != 1 {
		t.Fatalf("adherence %d/%d err=%v", followed, resolved, err)
	}
	s.Close()

	// Reopening must not re-log the latest suggestion for s1.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	keep := strategy.Suggestion{SessionID: "s1", Current: "claude-sonnet-x", Suggested: "claude-sonnet-x", Action: strategy.Keep, Reason: "calm", At: t0.Add(time.Hour)}
	if wrote, _ := s2.LogSuggestion(keep); wrote {
		t.Fatal("duplicate logged after reopen")
	}
}

func TestFollowed(t *testing.T) {
	tests := []struct {
		row  Row
		want *bool
	}{
		{Row{Action: "upgrade", Suggested: "opus", NextModel: "claude-opus-5"}, ptr(true)},
		{Row{Action: "upgrade", Suggested: "opus", NextModel: "claude-sonnet-5"}, ptr(false)},
		{Row{Action: "keep", Current: "claude-sonnet-5", NextModel: "claude-sonnet-5"}, ptr(true)},
		{Row{Action: "downgrade", Suggested: "sonnet"}, nil},
		{Row{Action: "effort-up", Suggested: "high", NextModel: "claude-opus-5", NextEffort: "high"}, ptr(true)},
		{Row{Action: "effort-up", Suggested: "high", NextModel: "claude-opus-5", NextEffort: "medium"}, ptr(false)},
		{Row{Action: "effort-down", Suggested: "medium", NextModel: "claude-opus-5"}, ptr(false)},
		{Row{Action: "keep", Current: "claude-opus-5", CurrentEffort: "medium", NextModel: "claude-opus-5", NextEffort: "high"}, ptr(false)},
	}
	for _, tt := range tests {
		got := tt.row.Followed()
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("Followed(%+v) = %v", tt.row, got)
		}
	}
}

func withAt(s strategy.Suggestion, at time.Time) strategy.Suggestion { s.At = at; return s }
func withSession(s strategy.Suggestion, id string) strategy.Suggestion {
	s.SessionID = id
	return s
}
func ptr(b bool) *bool { return &b }

func withReason(s strategy.Suggestion, r string) strategy.Suggestion { s.Reason = r; return s }

func TestOutcomesAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	// A database from before the outcome column existed.
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(schema); err != nil { // the original schema, without outcome
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().Add(-time.Hour)
	sg := strategy.Suggestion{SessionID: "s", Current: "claude-opus-x", Suggested: "sonnet", Action: strategy.Downgrade, At: at}
	if _, err := s.LogSuggestion(sg); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveNext("s", "claude-sonnet-x", "", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	open, err := s.AwaitingOutcome("s")
	if err != nil || len(open) != 1 {
		t.Fatalf("awaiting: %v %v", open, err)
	}
	if err := s.SetOutcome(open[0].ID, OutcomeSmooth); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.AwaitingOutcome("s"); len(open) != 0 {
		t.Error("outcome should be recorded")
	}
	resolved, followed, smooth, err := s.Adherence()
	if err != nil || resolved != 1 || followed != 1 || smooth != 1 {
		t.Errorf("adherence %d %d %d %v", resolved, followed, smooth, err)
	}
	rows, _ := s.Recent(5)
	if len(rows) != 1 || rows[0].Outcome != OutcomeSmooth {
		t.Errorf("rows: %+v", rows)
	}
}

func TestPrune(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t0 := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{t0.AddDate(0, 0, -100), t0.AddDate(0, 0, -91), t0.AddDate(0, 0, -89), t0} {
		sg := strategy.Suggestion{SessionID: "s" + string(rune('a'+i)), Current: "claude-opus-x", Suggested: "sonnet", Action: strategy.Downgrade, Reason: "r", At: at}
		if _, err := s.LogSuggestion(sg); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Prune(t0.AddDate(0, 0, -90))
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v; want 2", n, err)
	}
	rows, _ := s.Recent(10)
	if len(rows) != 2 {
		t.Fatalf("%d rows left, want 2", len(rows))
	}
}

func TestOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "p.db")
	// A database left readable by an older version is tightened.
	for _, p := range []string{path, path + "-shm"} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(path) // start fresh, keeping the stale -shm
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LogSuggestion(strategy.Suggestion{SessionID: "a", Current: "x", Suggested: "x", Action: strategy.Keep, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

func TestEffortRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t0 := time.Unix(1_800_000_000, 0)
	sg := strategy.Suggestion{SessionID: "e", Current: "claude-opus-5", Effort: "medium", Suggested: "high",
		Action: strategy.RaiseEffort, Reason: "r", At: t0}
	if wrote, err := s.LogSuggestion(sg); err != nil || !wrote {
		t.Fatalf("log: %v %v", wrote, err)
	}
	if err := s.ResolveNext("e", "claude-opus-5", "high", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Recent(1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("recent: %v %v", rows, err)
	}
	r := rows[0]
	if r.CurrentEffort != "medium" || r.NextEffort != "high" {
		t.Fatalf("efforts = %q -> %q", r.CurrentEffort, r.NextEffort)
	}
	if f := r.Followed(); f == nil || !*f {
		t.Fatal("effort suggestion not followed")
	}
	if open, _ := s.AwaitingOutcome("e"); len(open) != 1 {
		t.Fatalf("awaiting = %d", len(open))
	}
}
