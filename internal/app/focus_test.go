package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/usage"
)

// writeLiveIn writes a running session's file under its own name, in pane.
func writeLiveIn(t *testing.T, dir, id, pane string, started time.Time) {
	t.Helper()
	writeLiveStatus(t, dir, os.Getpid(), id, "idle", fmt.Sprintf(`,"tmux":"main:@0.%s"`, pane), started)
	if err := os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())), filepath.Join(dir, id+".json")); err != nil {
		t.Fatal(err)
	}
}

// noTmux stands in for tmux in tests, which drive update directly.
func noTmux() (string, bool) { return "", false }

func TestLatestFollowsTmuxFocus(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9") // Lookout's own pane
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	c.focus.query = noTmux
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	writeLiveIn(t, dir, "a", "%1", now.Add(-time.Hour))
	writeLiveIn(t, dir, "b", "%2", now.Add(-time.Hour))

	c.focus.update("%2", true, time.Now()) // active when Lookout starts
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q, want b (last prompted)", s.ID)
	}
	// Clicking into Lookout's own pane changes nothing.
	c.focus.update("%9", true, time.Now())
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q after focusing Lookout, want b", s.ID)
	}
	// Clicking into a's pane moves Auto there without a message.
	c.focus.update("%1", true, time.Now())
	if s, _ := c.Latest(); s.ID != "a" {
		t.Fatalf("Latest() = %q after focusing a's pane, want a", s.ID)
	}
	// A message to b moves it back, though a's pane stays focused.
	feed(c, prompt("b", time.Now().Add(time.Second)))
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q after prompting b, want b", s.ID)
	}
}

func TestFocusIgnoresPaneActiveAtStart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	c.focus.query = noTmux
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	writeLiveIn(t, dir, "a", "%1", now.Add(-time.Hour))
	writeLiveIn(t, dir, "b", "%2", now.Add(-time.Hour))
	c.focus.update("%1", true, now)
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q, want b: a's pane was active before Lookout looked", s.ID)
	}
}

func TestFocusSkipsTmuxWithoutTmuxSessions(t *testing.T) {
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	writeLiveStatus(t, dir, os.Getpid(), "a", "idle", "", time.Now())
	c.focus.query = noTmux
	c.focusTimes(time.Now())
	if !c.focus.at.IsZero() {
		t.Fatal("asked tmux with no session in tmux")
	}
}

func TestFocusNeverWaitsOnTmux(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := &focusTracker{query: func() (string, bool) { <-release; return "%1", true }}
	start := time.Now()
	f.get(start, nil)
	f.get(start.Add(2*time.Second), nil) // a poll is still running: no second one
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("get waited %v on tmux", d)
	}
}

func TestFocusForgetsClosedPanes(t *testing.T) {
	f := &focusTracker{query: noTmux}
	now := time.Now()
	f.update("%1", true, now.Add(-2*time.Minute))
	f.update("%2", true, now.Add(-2*time.Minute))
	f.update("%3", true, now)
	got := f.get(now, map[string]bool{"%3": true})
	if len(got) != 1 || got["%3"].IsZero() {
		t.Fatalf("get = %v, want only %%3", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.since["%2"]; ok {
		t.Fatal("a closed pane's old switch should be forgotten")
	}
}

func TestLastActiveClientPane(t *testing.T) {
	out := "1700000000 %3\n1700000050 %7\nbad line\n1700000010 %1\n"
	if p, ok := lastActiveClientPane(out); !ok || p != "%7" {
		t.Fatalf("lastActiveClientPane = %q, %v; want %%7", p, ok)
	}
	if _, ok := lastActiveClientPane(""); ok {
		t.Fatal("no clients should give no pane")
	}
	for ref, want := range map[string]string{"main:@0.%3": "%3", "%12": "%12", "main:1.0": "", "": "", "a%b": ""} {
		if got := TmuxPane(ref); got != want {
			t.Errorf("TmuxPane(%q) = %q, want %q", ref, got, want)
		}
	}
}
