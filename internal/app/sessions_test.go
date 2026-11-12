package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/usage"
)

// feed adds a record to every engine, as the watcher would.
func feed(c *Core, r transcript.Record) {
	c.Agg.Add(r, transcript.Source{})
	if c.Activity != nil {
		c.Activity.Observe(r, transcript.Source{})
	}
}

func prompt(id string, at time.Time) transcript.Record {
	return transcript.Record{Kind: transcript.KindUser, Type: "user", SessionID: id, Cwd: "/w", Timestamp: at, Prompt: "do " + id}
}

func reply(id string, at time.Time, tools ...transcript.ToolUse) transcript.Record {
	return transcript.Record{Kind: transcript.KindAssistant, Type: "assistant", SessionID: id, Cwd: "/w", Timestamp: at,
		MessageID: fmt.Sprintf("m-%s-%d", id, at.UnixNano()), Model: "claude-opus-test", Text: "ok", ToolUses: tools}
}

func TestLatestFollowsLastPromptNotLastActivity(t *testing.T) {
	now := time.Now()
	c := &Core{Agg: usage.New(), Activity: activity.New()}
	// You prompt "a", then "b"; "a" keeps working after that.
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	feed(c, reply("b", now.Add(-4*time.Minute)))
	feed(c, reply("a", now.Add(-time.Minute)))
	if s, ok := c.Latest(); !ok || s.ID != "b" {
		t.Fatalf("Latest() = %q, want b (the session you last prompted)", s.ID)
	}
	// Prompting "a" again moves the view back.
	feed(c, prompt("a", now))
	if s, _ := c.Latest(); s.ID != "a" {
		t.Fatalf("Latest() = %q after prompting a, want a", s.ID)
	}
}

func TestLatestSkipsClosedSessions(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	// You prompt "a", then "b"; then you close "b".
	feed(c, prompt("a", now.Add(-10*time.Minute)))
	feed(c, prompt("b", now.Add(-5*time.Minute)))
	writeLiveStatus(t, dir, os.Getpid(), "a", "idle", "", now.Add(-time.Hour))
	if s, ok := c.Latest(); !ok || s.ID != "a" {
		t.Fatalf("Latest() = %q, want a (b is closed)", s.ID)
	}
	if s, ok := c.Followed(); !ok || s.ID != "a" {
		t.Fatalf("Followed() = %q, want a (b is closed)", s.ID)
	}
	// With nothing running, the session you last prompted is still shown.
	if err := os.Remove(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid()))); err != nil {
		t.Fatal(err)
	}
	c.live.at = time.Time{} // re-read the running sessions
	if s, ok := c.Latest(); !ok || s.ID != "b" {
		t.Fatalf("Latest() = %q with nothing running, want b", s.ID)
	}
}

func writeLiveStatus(t *testing.T, dir string, pid int, id, status, extra string, started time.Time) {
	t.Helper()
	b := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/w","startedAt":%d,"name":"%s-name","status":%q,"statusUpdatedAt":%d%s}`,
		pid, id, started.UnixMilli(), id, status, started.Add(time.Minute).UnixMilli(), extra)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsOverview(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	feed(c, prompt("busy", now.Add(-3*time.Minute)))
	feed(c, reply("busy", now.Add(-2*time.Minute), transcript.ToolUse{ID: "t1", Name: "Bash", Command: "go test ./...", Detail: "Run the tests"}))
	feed(c, prompt("asks", now.Add(-3*time.Minute)))
	feed(c, prompt("closed", now.Add(-time.Hour)))

	pid := os.Getpid() // a running process; the files only need distinct names
	writeLiveStatus(t, dir, pid, "busy", "busy", "", now.Add(-time.Hour))
	if err := os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), filepath.Join(dir, "a.json")); err != nil {
		t.Fatal(err)
	}
	writeLiveStatus(t, dir, pid, "asks", "waiting", `,"blockedOn":"permission prompt"`, now.Add(-30*time.Minute))
	if err := os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), filepath.Join(dir, "b.json")); err != nil {
		t.Fatal(err)
	}
	writeLiveStatus(t, dir, pid, "fresh", "idle", "", now.Add(-time.Minute))

	got := c.Sessions(now, 10)
	want := []struct {
		id     string
		state  State
		detail string
	}{
		{"busy", StateWorking, "Bash: Run the tests"},
		{"asks", StateWaiting, "permission prompt"},
		{"fresh", StateStarting, "nothing sent yet"},
		{"closed", StateClosed, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("Sessions() = %d sessions, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		o := got[i]
		if o.ID != w.id || o.State != w.state || o.Detail != w.detail {
			t.Errorf("session %d = %q state %d detail %q; want %q %d %q", i, o.ID, o.State, o.Detail, w.id, w.state, w.detail)
		}
	}
	if got[0].Label() != "busy-name" || !got[0].Running || got[3].Running {
		t.Errorf("labels/running wrong: %+v", got)
	}
	if !got[1].NeedsYou() {
		t.Error("a session on a permission prompt should need you")
	}
}

func TestResolve(t *testing.T) {
	c := &Core{Agg: usage.New()}
	for _, id := range []string{"abc123", "abd456"} {
		c.Agg.Add(prompt(id, time.Now()), transcript.Source{})
	}
	for in, want := range map[string]string{"abc123": "abc123", "abc": "abc123", "abd4": "abd456", "ab": "", "zz": ""} {
		got, ok := c.Resolve(in)
		if got != want || ok != (want != "") {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestPinnedOpenSessionWaits(t *testing.T) {
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), LiveDir: dir}
	c.Agg.Add(prompt("old", time.Now().Add(-time.Hour)), transcript.Source{})
	writeLiveStatus(t, dir, os.Getpid(), "new", "idle", "", time.Now().Add(-2*time.Hour))
	c.Pinned = "new"
	if s, ok := c.Current(); ok {
		t.Fatalf("Current() = %q; a pinned session with nothing sent yet should be waited on", s.ID)
	}
	if l, ok := c.Waiting(); !ok || l.ID != "new" {
		t.Fatalf("Waiting() = %+v, %v", l, ok)
	}
}

func TestCurrentShowsSessionThatNeedsYou(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	c := &Core{Agg: usage.New(), Activity: activity.New(), LiveDir: dir}
	feed(c, prompt("bg", now.Add(-10*time.Minute)))
	feed(c, prompt("talk", now.Add(-time.Minute)))
	pid := os.Getpid()
	writeLiveStatus(t, dir, pid, "bg", "busy", "", now.Add(-time.Hour))
	_ = os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), filepath.Join(dir, "bg.json"))
	writeLiveStatus(t, dir, pid, "talk", "busy", "", now.Add(-time.Hour))
	_ = os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), filepath.Join(dir, "talk.json"))
	if s, _ := c.Current(); s.ID != "talk" {
		t.Fatalf("Current() = %q, want talk (last prompted)", s.ID)
	}
	// bg stops on a permission prompt: it takes over until answered.
	writeLiveStatus(t, dir, pid, "bg", "waiting", `,"blockedOn":"permission prompt"`, now.Add(-time.Hour))
	_ = os.Rename(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), filepath.Join(dir, "bg.json"))
	c.live = liveCache{} // drop the one-second cache
	if s, _ := c.Current(); s.ID != "bg" {
		t.Fatalf("Current() = %q, want bg (it needs you)", s.ID)
	}
	// A pin still wins.
	c.Pinned = "talk"
	if s, _ := c.Current(); s.ID != "talk" {
		t.Fatalf("Current() = %q with talk pinned", s.ID)
	}
}
