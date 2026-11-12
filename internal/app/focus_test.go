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

func TestLatestFollowsTmuxFocus(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9") // Lookout's own pane
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	writeLiveIn(t, dir, "a", "%1", now.Add(-time.Hour))
	writeLiveIn(t, dir, "b", "%2", now.Add(-time.Hour))

	active := "%2"
	c.focus.query = func() (string, bool) { return active, true }
	poll := func() { c.focus.at = time.Time{} } // skip the one-second cache

	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q, want b (last prompted)", s.ID)
	}
	// Clicking into Lookout's own pane changes nothing.
	active = "%9"
	poll()
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q after focusing Lookout, want b", s.ID)
	}
	// Clicking into a's pane moves Auto there without a message.
	active = "%1"
	poll()
	if s, _ := c.Latest(); s.ID != "a" {
		t.Fatalf("Latest() = %q after focusing a's pane, want a", s.ID)
	}
	// A message to b moves it back, though a's pane stays focused.
	feed(c, prompt("b", time.Now().Add(time.Second)))
	poll()
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q after prompting b, want b", s.ID)
	}
}

func TestFocusIgnoresPaneActiveAtStart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	writeLiveIn(t, dir, "a", "%1", now.Add(-time.Hour))
	writeLiveIn(t, dir, "b", "%2", now.Add(-time.Hour))
	c.focus.query = func() (string, bool) { return "%1", true }
	if s, _ := c.Latest(); s.ID != "b" {
		t.Fatalf("Latest() = %q, want b: a's pane was active before Lookout looked", s.ID)
	}
}

func TestFocusSkipsTmuxWithoutTmuxSessions(t *testing.T) {
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	writeLiveStatus(t, dir, os.Getpid(), "a", "idle", "", time.Now())
	c.focus.query = func() (string, bool) { t.Fatal("asked tmux with no session in tmux"); return "", false }
	c.focusTimes(time.Now())
}

func TestLastActiveClientPane(t *testing.T) {
	out := "1700000000 %3\n1700000050 %7\nbad line\n1700000010 %1\n"
	if p, ok := lastActiveClientPane(out); !ok || p != "%7" {
		t.Fatalf("lastActiveClientPane = %q, %v; want %%7", p, ok)
	}
	if _, ok := lastActiveClientPane(""); ok {
		t.Fatal("no clients should give no pane")
	}
	if got := paneID("main:@0.%3"); got != "%3" {
		t.Fatalf("paneID = %q, want %%3", got)
	}
}
