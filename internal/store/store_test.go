package store

import (
	"path/filepath"
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
	if err := s.ResolveNext("s1", "claude-opus-x", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A later response must not overwrite the recorded next model.
	_ = s.ResolveNext("s1", "claude-haiku-x", t0.Add(2*time.Minute))

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
	resolved, followed, err := s.Adherence()
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
