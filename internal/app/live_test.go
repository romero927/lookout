package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/usage"
)

func writeLive(t *testing.T, dir string, pid int, id, cwd string, started time.Time) {
	t.Helper()
	b := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"startedAt":%d,"status":"idle"}`, pid, id, cwd, started.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentWaitsForNewSession(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	pid := os.Getpid() // a process that is running

	cases := []struct {
		name    string
		filter  string
		liveCwd string
		started time.Time
		pinned  string
		want    string // "" means waiting, no current session
	}{
		{name: "newer session without transcript", liveCwd: "/w", started: at.Add(time.Minute), want: ""},
		{name: "older live session", liveCwd: "/w", started: at.Add(-time.Minute), want: "old"},
		{name: "pinned wins", liveCwd: "/w", started: at.Add(time.Minute), pinned: "old", want: "old"},
		{name: "other folder ignored", filter: "/w", liveCwd: "/elsewhere", started: at.Add(time.Minute), want: "old"},
		{name: "same folder", filter: "/w", liveCwd: "/w", started: at.Add(time.Minute), want: ""},
		{name: "subfolder loses to exact match", filter: "/w", liveCwd: "/w/sub", started: at.Add(time.Minute), want: "old"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := &Core{Agg: usage.New(), CwdFilter: tc.filter, LiveDir: dir, Pinned: tc.pinned}
			c.Agg.Add(transcript.Record{Kind: transcript.KindUser, SessionID: "old", Cwd: "/w", Timestamp: at}, transcript.Source{})
			writeLive(t, dir, pid, "new", tc.liveCwd, tc.started)
			s, ok := c.Current()
			if got := s.ID; ok != (tc.want != "") || got != tc.want {
				t.Fatalf("Current() = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestStartingSkipsKnownAndExited(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), LiveDir: dir}
	c.Agg.Add(transcript.Record{Kind: transcript.KindUser, SessionID: "old", Cwd: "/w", Timestamp: at}, transcript.Source{})
	// The running session has a transcript already: it is "old" itself.
	writeLive(t, dir, os.Getpid(), "old", "/w", at.Add(-time.Hour))
	// A file left by a process that has exited.
	writeLive(t, dir, 1<<22+12345, "gone", "/w", at.Add(time.Minute))
	if l, ok := c.Starting(); ok {
		t.Fatalf("Starting() = %+v", l)
	}
	if s, ok := c.Current(); !ok || s.ID != "old" {
		t.Fatalf("Current() = %q, %v", s.ID, ok)
	}
}
