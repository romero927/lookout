package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/app"
)

// addRunning writes a second transcript and marks both sessions as open
// in Claude Code's list of running sessions.
func addRunning(t *testing.T, core *app.Core, otherStatus string) {
	t.Helper()
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	at := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339Nano)
	line := `{"type":"user","sessionId":"sess-two","cwd":"/work/two","timestamp":"` + at + `","message":{"content":"build the second thing"}}`
	if err := os.WriteFile(filepath.Join(claude, "projects", "-work-demo", "sess-two.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	core.Load()
	live := filepath.Join(claude, "sessions")
	_ = os.MkdirAll(live, 0o755)
	write := func(file, id, name, status string, started time.Time) {
		b := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/work/x","startedAt":%d,"name":%q,"status":%q,"statusUpdatedAt":%d,"tmux":"main:@1.%%4"}`,
			os.Getpid(), id, started.UnixMilli(), name, status, time.Now().UnixMilli())
		if err := os.WriteFile(filepath.Join(live, file), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1.json", "sess-ui", "demo-one", "idle", time.Now().Add(-time.Hour))
	write("2.json", "sess-two", "two-two", otherStatus, time.Now().Add(-30*time.Minute))
	core.LiveDir = live
}

func TestSessionStripAndSwitching(t *testing.T) {
	core, db := setup(t)
	addRunning(t, core, "busy")
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30}, LoadedMsg{})
	// sess-two was prompted last, so it is followed.
	if id := m.(Model).sess.ID; id != "sess-two" {
		t.Fatalf("following %q, want sess-two (the session you last prompted)", id)
	}
	head := plain(strings.Split(m.View(), "\n")[m.(Model).stripRow()])
	if !strings.Contains(head, "demo-one") || !strings.Contains(head, "two-two") || !strings.Contains(head, "[ ] switch") {
		t.Fatalf("session strip missing:\n%s", head)
	}
	m = drive(m, key("]"))
	if core.Pinned != "sess-ui" || m.(Model).sess.ID != "sess-ui" {
		t.Fatalf("] should show and pin the next session (pinned=%q shown=%q)", core.Pinned, m.(Model).sess.ID)
	}
	// Going back to the followed session follows it rather than pinning.
	m = drive(m, key("["))
	if core.Pinned != "" || m.(Model).sess.ID != "sess-two" {
		t.Fatalf("[ back to the latest should unpin (pinned=%q shown=%q)", core.Pinned, m.(Model).sess.ID)
	}
	// A click on a session in the strip shows it.
	items, _, _ := m.(Model).strip()
	row := m.(Model).stripRow()
	m = drive(m, tea.MouseMsg{X: items[0].x, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		tea.MouseMsg{X: items[0].x, Y: row, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.(Model).sess.ID != items[0].id {
		t.Fatalf("click on the strip showed %q, want %q", m.(Model).sess.ID, items[0].id)
	}
}

func TestSessionsPanel(t *testing.T) {
	core, db := setup(t)
	addRunning(t, core, "waiting")
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 130, Height: 30}, LoadedMsg{}, key("0"))
	v := plain(m.View())
	for _, want := range []string{"0 Sessions", "SESSIONS", "2 running", "1 needs you", "demo-one", "two-two", "needs you", "build the second thing", "tmux main:@1.%4"} {
		if !strings.Contains(v, want) {
			t.Errorf("Sessions panel lacks %q:\n%s", want, v)
		}
	}
	// enter shows the selected session in Usage.
	if m.(Model).svID != "sess-two" {
		t.Fatalf("Sessions should open on the session on screen, not %q", m.(Model).svID)
	}
	m = drive(m, key("up"), key("enter"))
	if mm := m.(Model); mm.tab != tabUsage || mm.sess.ID != mm.ovs[0].ID {
		t.Fatalf("enter should open the session in Usage (tab %d, shown %q)", mm.tab, mm.sess.ID)
	}
	// Even coming from Changes; esc goes back to Changes without picking.
	m = drive(m, key("3"), key("0"), key("down"), key("enter"))
	if mm := m.(Model); mm.tab != tabUsage || mm.sess.ID != mm.ovs[1].ID {
		t.Fatalf("enter should open Usage for the chosen session (tab %d, shown %q)", mm.tab, mm.sess.ID)
	}
	m = drive(m, key("3"), key("0"), key("esc"))
	if mm := m.(Model); mm.tab != tabChanges {
		t.Fatalf("esc should go back to Changes (tab %d)", mm.tab)
	}
}

func TestSessionAlerts(t *testing.T) {
	core, db := setup(t)
	addRunning(t, core, "busy")
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30}, LoadedMsg{}, key("]")) // show sess-ui
	mm := m.(Model)
	if len(mm.sessAlerts) != 0 {
		t.Fatalf("no alert expected yet: %+v", mm.sessAlerts)
	}
	// sess-two (not on screen) stops on a permission prompt.
	b := fmt.Sprintf(`{"pid":%d,"sessionId":"sess-two","cwd":"/work/x","startedAt":%d,"name":"two-two","status":"waiting","blockedOn":"permission prompt","statusUpdatedAt":%d}`,
		os.Getpid(), time.Now().Add(-time.Hour).UnixMilli(), time.Now().UnixMilli())
	_ = os.WriteFile(filepath.Join(core.LiveDir, "2.json"), []byte(b), 0o644)
	time.Sleep(1100 * time.Millisecond) // the list of running sessions is cached for a second
	mm.refresh()
	if len(mm.sessAlerts) != 1 || !strings.Contains(mm.sessAlerts[0].text, "two-two needs you: permission prompt") {
		t.Fatalf("alerts = %+v", mm.sessAlerts)
	}
	_ = mm.checkSessionAlerts()
	if !strings.Contains(mm.flash, "two-two needs you") {
		t.Errorf("flash = %q", mm.flash)
	}
	// The same alert doesn't fire twice.
	mm.queueSessionAlert(mm.ovs[1], "again")
	if cmd := mm.checkSessionAlerts(); cmd != nil {
		t.Error("an alert already shown fired again")
	}
}

func TestTmuxGotoArgs(t *testing.T) {
	cases := map[string][]string{
		"main:@2.%7": {"switch-client", "-t", "main", ";", "select-window", "-t", "@2", ";", "select-pane", "-t", "%7"},
		"a:b:@1.%3":  {"switch-client", "-t", "a:b", ";", "select-window", "-t", "@1", ";", "select-pane", "-t", "%3"},
		"%5":         {"select-pane", "-t", "%5"},
	}
	for in, want := range cases {
		if got := tmuxGotoArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("tmuxGotoArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortModel(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":            "Opus 5.5",
		"claude-haiku-4-5-20251001":  "Haiku 4.5",
		"claude-3-5-sonnet-20241022": "Sonnet 3.5",
		"claude-sonnet-test":         "Sonnet test",
		"opus[1m]":                   "Opus [1m]",
		"<synthetic>":                "<synthetic>",
		"":                           "—",
	} {
		if got := ShortModel(in); got != want {
			t.Errorf("ShortModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFollowSwitchesToSessionThatNeedsYou(t *testing.T) {
	core, db := setup(t)
	addRunning(t, core, "busy")
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30}, LoadedMsg{})
	mm := m.(Model)
	if mm.sess.ID != "sess-two" {
		t.Fatalf("following %q, want sess-two", mm.sess.ID)
	}
	setStatus := func(status, extra string) {
		b := fmt.Sprintf(`{"pid":%d,"sessionId":"sess-ui","cwd":"/work/x","startedAt":%d,"name":"demo-one","status":%q,"statusUpdatedAt":%d%s}`,
			os.Getpid(), time.Now().Add(-time.Hour).UnixMilli(), status, time.Now().UnixMilli(), extra)
		_ = os.WriteFile(filepath.Join(core.LiveDir, "1.json"), []byte(b), 0o644)
		time.Sleep(1100 * time.Millisecond) // the running-session list is cached for a second
		mm.refresh()
	}
	setStatus("waiting", `,"blockedOn":"permission prompt"`)
	if mm.sess.ID != "sess-ui" || core.Pinned != "" {
		t.Fatalf("should show the session that needs you without pinning it (shown %q, pinned %q)", mm.sess.ID, core.Pinned)
	}
	if !strings.Contains(mm.flash, "demo-one: it needs you") || len(mm.sessAlerts) != 1 {
		t.Errorf("flash %q, alerts %+v", mm.flash, mm.sessAlerts)
	}
	setStatus("busy", "")
	if mm.sess.ID != "sess-two" || !strings.Contains(mm.flash, "back to two-two") {
		t.Fatalf("once answered, back to the last prompted session (shown %q, flash %q)", mm.sess.ID, mm.flash)
	}
}
