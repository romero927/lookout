package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/romero927/overwatch/internal/transcript"
)

func line(id string) string {
	return `{"type":"assistant","sessionId":"s","timestamp":"2026-01-10T10:00:00Z","message":{"id":"` + id + `","model":"x","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
}

func TestWatcherFollowsNewFilesAndAppends(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-work-demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(proj, "old.jsonl")
	_ = os.WriteFile(old, []byte(line("old")), 0o644)
	past := time.Now().Add(-90 * 24 * time.Hour)
	_ = os.Chtimes(old, past, past)
	first := filepath.Join(proj, "a.jsonl")
	_ = os.WriteFile(first, []byte(line("a1")), 0o644)

	var mu sync.Mutex
	got := map[string]transcript.Source{}
	changed := make(chan struct{}, 10)
	w := New(root, 30*24*time.Hour, func(r transcript.Record, s transcript.Source) {
		mu.Lock()
		got[r.MessageID] = s
		mu.Unlock()
	}, func() { changed <- struct{}{} })
	w.Poll = 200 * time.Millisecond

	if n := w.Scan(); n != 1 {
		t.Fatalf("initial scan delivered %d, want 1 (old file skipped)", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	f, _ := os.OpenFile(first, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(line("a2"))
	f.Close()
	sub := filepath.Join(proj, "a", "subagents")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "agent-1.jsonl"), []byte(line("sub1")), 0o644)

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		_, a2 := got["a2"]
		s, sub1 := got["sub1"]
		_, oldSeen := got["old"]
		mu.Unlock()
		if oldSeen {
			t.Fatal("stale file was read")
		}
		if a2 && sub1 {
			if !s.IsSubagent || s.SessionID != "a" {
				t.Fatalf("subagent source = %+v", s)
			}
			// Let a couple of polls run to confirm the old file stays skipped.
			time.Sleep(500 * time.Millisecond)
			mu.Lock()
			_, oldSeen = got["old"]
			mu.Unlock()
			if oldSeen {
				t.Fatal("stale file read by poll")
			}
			return
		}
		select {
		case <-changed:
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out; got %v", got)
		}
	}
}
