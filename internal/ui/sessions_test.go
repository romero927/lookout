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
	"github.com/charmbracelet/lipgloss"

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
	if !strings.Contains(head, "↻ Auto") || !strings.Contains(head, "↻ two-two") {
		t.Fatalf("strip should start with the Auto tab and mark the session it follows:\n%s", head)
	}
	// [ ] step through Auto, then each running session, pinning it.
	run := m.(Model).running()
	m = drive(m, key("]"))
	if core.Pinned != run[0].ID || m.(Model).sess.ID != run[0].ID {
		t.Fatalf("] from Auto should pin the first session (pinned=%q shown=%q)", core.Pinned, m.(Model).sess.ID)
	}
	if !strings.Contains(plain(strings.Split(m.View(), "\n")[m.(Model).stripRow()]), "◆") {
		t.Fatal("the pinned session's tab should show ◆")
	}
	m = drive(m, key("["))
	if core.Pinned != "" || m.(Model).sess.ID != "sess-two" {
		t.Fatalf("[ back to Auto should follow again (pinned=%q shown=%q)", core.Pinned, m.(Model).sess.ID)
	}
	m = drive(m, key("["))
	if core.Pinned != run[len(run)-1].ID {
		t.Fatalf("[ from Auto should pin the last session (pinned=%q)", core.Pinned)
	}
	// A click on a session tab pins it, even the followed one; a click on
	// Auto follows again.
	row := m.(Model).stripRow()
	clickID := func(id string) {
		t.Helper()
		items, _, _ := m.(Model).strip()
		for _, it := range items {
			if it.id == id {
				m = drive(m, click(it.x, row)...)
				return
			}
		}
		t.Fatalf("no %q in the strip", id)
	}
	clickID("sess-two")
	if core.Pinned != "sess-two" || m.(Model).sess.ID != "sess-two" {
		t.Fatalf("clicking a session tab should pin it (pinned=%q)", core.Pinned)
	}
	clickID(stripAuto)
	if core.Pinned != "" {
		t.Fatalf("clicking Auto should follow again (pinned=%q)", core.Pinned)
	}
}

func TestNewClaudeButton(t *testing.T) {
	core, db := setup(t)
	addRunning(t, core, "busy")
	t.Setenv("TMUX", "")
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30}, LoadedMsg{})
	if strings.Contains(plain(m.View()), "+ claude") {
		t.Fatal("the + claude button should only show inside tmux")
	}
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	line := strings.Split(m.View(), "\n")[m.(Model).stripRow()]
	if w := lipgloss.Width(line); w != 120 || !strings.HasSuffix(plain(line), "+ claude ") {
		t.Fatalf("button should end the %d-wide strip line (width %d):\n%s", 120, w, plain(line))
	}
	if id := m.(Model).stripAt(118); id != stripNew {
		t.Fatalf("stripAt the button = %q", id)
	}
}

func TestNewClaudeArgs(t *testing.T) {
	got := strings.Join(newClaudeArgs("%7", "-h", "/w x", "/bin/claude"), " ")
	if got != "split-window -h -t %7 -P -F #{session_name}:#{window_id}.#{pane_id} -c /w x /bin/claude" {
		t.Errorf("split: %s", got)
	}
	got = strings.Join(newClaudeArgs("", "", "/w", "/my bin/claude"), " ")
	if got != "new-window -P -F #{session_name}:#{window_id}.#{pane_id} -c /w '/my bin/claude'" {
		t.Errorf("window: %s", got)
	}
	panes := parsePanes("%1 @0 100 40\n%2 @1 80 20\nbad\n")
	if len(panes) != 2 || panes["%2"] != (paneInfo{id: "%2", window: "@1", w: 80, h: 20}) {
		t.Errorf("parsePanes = %v", panes)
	}
	if splitFlag(200, 50) != "-h" || splitFlag(120, 40) != "-v" || splitFlag(170, 100) != "-v" {
		t.Error("splitFlag")
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
	// The same alert doesn't fire twice. (Find sess-two by ID: the list's
	// order depends on timing.)
	two, ok := mm.overviewOf("sess-two")
	if !ok {
		t.Fatal("sess-two not listed")
	}
	mm.queueSessionAlert(two, "again")
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

func TestChooseSplit(t *testing.T) {
	panes := map[string]paneInfo{
		"%1": {id: "%1", window: "@0", w: 100, h: 40}, // Lookout
		"%2": {id: "%2", window: "@0", w: 100, h: 20},
		"%3": {id: "%3", window: "@0", w: 100, h: 30},
		"%4": {id: "%4", window: "@1", w: 200, h: 60}, // another window
	}
	info := func(id string) (paneInfo, bool) { p, ok := panes[id]; return p, ok }
	cases := []struct {
		name, shown, why string
		others           []string
		own              string
		want             string // "" is a new window
		note             string
	}{
		{"the session on screen", "%4", "", []string{"%2"}, "%1", "%4", ""},
		{"closed: largest Claude pane in Lookout's window", "", "x is closed", []string{"%2", "%3", "%4"}, "%1", "%3", "x is closed"},
		{"pane gone", "%9", "", []string{"%2"}, "%1", "%2", "its pane is gone"},
		{"no Claude pane here: Lookout's own", "", "x isn't in tmux", []string{"%4"}, "%1", "%1", "x isn't in tmux"},
		{"nothing on screen", "", "", nil, "%1", "%1", "no session on screen in tmux"},
		{"Lookout's pane unknown: a window", "", "x is closed", []string{"%2"}, "", "", "x is closed"},
	}
	for _, c := range cases {
		p, ok, note := chooseSplit(c.shown, c.why, c.others, c.own, info)
		got := ""
		if ok {
			got = p.id
		}
		if got != c.want || note != c.note {
			t.Errorf("%s: got %q (%q), want %q (%q)", c.name, got, note, c.want, c.note)
		}
	}
}
