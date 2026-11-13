package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/store"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/update"
)

var testANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func plain(s string) string { return testANSI.ReplaceAllString(s, "") }

// setup builds a fake ~/.claude with a synthetic session timestamped "now".
func setup(t *testing.T) (*app.Core, *store.Store) {
	t.Helper()
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("LOOKOUT_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("LOOKOUT_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("PATH", root) // no git or notifier on PATH

	proj := filepath.Join(claude, "projects", "-work-demo")
	_ = os.MkdirAll(proj, 0o755)
	_ = os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"model":"opus","env":{"API_TOKEN":"sk-secret"}}`), 0o644)
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	lines := []string{
		`{"type":"user","sessionId":"sess-ui","cwd":"/work/demo","timestamp":"` + ts(-3*time.Minute) + `","message":{"content":"this still doesn't work"}}`,
		`{"type":"assistant","sessionId":"sess-ui","cwd":"/work/demo","timestamp":"` + ts(-2*time.Minute) + `","message":{"id":"m1","model":"claude-sonnet-test","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/demo/a.go"}}],"usage":{"input_tokens":10,"output_tokens":500,"cache_creation_input_tokens":2000,"cache_read_input_tokens":40000}}}`,
		`{"type":"assistant","sessionId":"sess-ui","cwd":"/work/demo","timestamp":"` + ts(-1*time.Minute) + `","message":{"id":"m2","model":"claude-sonnet-test","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":5,"output_tokens":300,"cache_creation_input_tokens":100,"cache_read_input_tokens":150000}}}`,
	}
	_ = os.WriteFile(filepath.Join(proj, "sess-ui.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	cfg := config.Default()
	core := app.New(cfg, "", nil)
	db, err := store.Open(filepath.Join(root, "data", "lookout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	core.Load()
	return core, db
}

func drive(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPanelsRender(t *testing.T) {
	core, db := setup(t)
	sizes := []tea.WindowSizeMsg{{Width: 130, Height: 45}, {Width: 70, Height: 24}, {Width: 30, Height: 10}}
	for _, size := range sizes {
		var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
		m = drive(m, size, LoadedMsg{})
		panels := []struct {
			key  string
			want []string
		}{
			{"1", []string{"◉", "STATUS LINE NOT INSTALLED", "statusline install --print", "settings.json", "LIMITS", "THIS SESSION", "TOKENS"}},
			{"2", []string{"ACTIVITY", "this still doesn't work", "WHAT CLAUDE DID", "Edit"}},
			{"3", []string{"CHANGES", "1 edited file no longer"}},
			{"4", []string{"CONTEXT", "Startup", "LARGEST ITEMS"}},
			{"5", []string{"RECOMMENDATION", "Upgrade to opus", "frustration", "/model opus", "MODELS YOU CAN SWITCH TO", "Fastest for quick answers"}},
			{"6", []string{"TOOLS", "Edit", "1 call", "a.go"}},
			{"7", []string{"SKILLS"}},
			{"8", []string{"CONFIG", "settings.json", "Lookout", "config.toml"}},
			{"9", []string{"CHEATSHEET"}},
		}
		for _, p := range panels {
			m = drive(m, key(p.key))
			v := plain(m.View())
			if size.Width >= 70 {
				for _, w := range p.want {
					if !strings.Contains(v, w) {
						t.Errorf("%dx%d panel %s: missing %q\n%s", size.Width, size.Height, p.key, w, v)
					}
				}
			}
			for i, line := range strings.Split(v, "\n") {
				if n := len([]rune(line)); n > size.Width {
					t.Errorf("%dx%d panel %s line %d is %d wide: %q", size.Width, size.Height, p.key, i, n, line)
				}
			}
			if n := strings.Count(v, "\n") + 1; n > size.Height {
				t.Errorf("%dx%d panel %s is %d lines tall", size.Width, size.Height, p.key, n)
			}
			if os.Getenv("LOOKOUT_SHOW") != "" && size.Width == 70 {
				t.Logf("panel %s:\n%s", p.key, v)
			}
		}
	}
}

func TestFilesMasksSecretsAndBlocksEditByDefault(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("8"), key("enter"))
	v := plain(m.View())
	if strings.Contains(v, "sk-secret") || !strings.Contains(v, "••••••") {
		t.Fatalf("secret not masked:\n%s", v)
	}
	m = drive(m, key("esc"), key("e"))
	if v := plain(m.View()); !strings.Contains(v, "read-only") {
		t.Fatalf("edit not refused:\n%s", v)
	}
}

func TestModelsLogsSuggestion(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{})
	if rows, _ := db.Recent(5); len(rows) != 0 {
		t.Fatalf("a suggestion shown for under a minute shouldn't be logged: %+v", rows)
	}
	mm := m.(Model)
	mm.shownSince = time.Now().Add(-2 * time.Minute)
	_ = drive(mm, DataMsg{})
	rows, err := db.Recent(5)
	if err != nil || len(rows) != 1 || rows[0].Action != "upgrade" || rows[0].Suggested != "opus" {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
}

func TestFormat(t *testing.T) {
	tests := map[int64]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 999_949: "999.9k", 999_999: "1M", 999_999_999: "1B", 1_500_000: "1.5M", 12_345_678: "12.35M", 2_000_000_000: "2B"}
	for n, want := range tests {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestLimitsPanel(t *testing.T) {
	core, db := setup(t)
	now := time.Now()
	st := limits.State{
		UpdatedAt: now.Add(-30 * time.Second).Unix(),
		FiveHour:  &limits.Window{UsedPercent: 62, ResetsAt: now.Add(2 * time.Hour).Unix()},
		SevenDay:  &limits.Window{UsedPercent: 30, ResetsAt: now.Add(80 * time.Hour).Unix()},
		Samples: []limits.Sample{
			{At: now.Add(-20 * time.Minute).Unix(), FiveHour: ptrF(42), SevenDay: ptrF(28)},
		},
	}
	if err := limits.Save(limits.FileFor(paths.LimitsDir(), "sess-ui"), st); err != nil {
		t.Fatal(err)
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 130, Height: 45}, {Width: 72, Height: 30}} {
		var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
		m = drive(m, size, LoadedMsg{})
		v := plain(m.View())
		// 20 points in 20 minutes = 60%/h: the limit is hit well before reset.
		for _, want := range []string{"5-HOUR", "62% used", "38% left", "recent pace 60%/h", "before reset", "WEEKLY", "30% used", "average pace", "5h 62%", "wk 30%"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d: missing %q\n%s", size.Width, size.Height, want, v)
			}
		}
		for i, line := range strings.Split(v, "\n") {
			if n := len([]rune(line)); n > size.Width {
				t.Errorf("%dx%d line %d is %d wide: %q", size.Width, size.Height, i, n, line)
			}
		}
	}
}

func TestLimitsWaitingWhenStatusLineInstalled(t *testing.T) {
	core, db := setup(t)
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/lookout statusline"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	v := plain(drive(m, tea.WindowSizeMsg{Width: 100, Height: 40}, LoadedMsg{}).View())
	if !strings.Contains(v, "Waiting for Claude Code") {
		t.Errorf("missing waiting message:\n%s", v)
	}
	if strings.Contains(v, "est. block") {
		t.Errorf("token estimate shown while real limits are on the way:\n%s", v)
	}
}

// A session that opened but hasn't replied yet has context but no cost or
// limits; it must not be taken for an API-key account.
func TestLimitsWaitingBeforeFirstReply(t *testing.T) {
	c, zero := 3.0, 0.0
	m := Model{slOurs: true, hasSess: true, limits: limits.State{Context: &c, ContextAt: time.Now().Unix(), CostUSD: &zero}}
	v := plain(strings.Join(m.viewLimitsMissing(100), "\n"))
	if strings.Contains(v, "pay-as-you-go") || !strings.Contains(v, "Waiting for Claude Code") {
		t.Errorf("fresh session shown as API account:\n%s", v)
	}
}

func TestLimitsWaitingWithoutSession(t *testing.T) {
	m := Model{slOurs: true}
	v := plain(strings.Join(m.viewLimitsMissing(100), "\n"))
	if !strings.Contains(v, "No Claude Code session yet") || strings.Contains(v, "next reply") {
		t.Errorf("no-session message wrong:\n%s", v)
	}
}

func ptrF(v float64) *float64 { return &v }

func TestHistoryAlertsAndRestart(t *testing.T) {
	core, db := setup(t)
	core.Cfg.Alerts.Desktop = false
	core.Cfg.Alerts.Bell = false
	now := time.Now()
	st := limits.State{
		UpdatedAt: now.Unix(),
		FiveHour:  &limits.Window{UsedPercent: 85, ResetsAt: now.Add(30 * time.Minute).Unix()},
		SevenDay:  &limits.Window{UsedPercent: 30, ResetsAt: now.Add(80 * time.Hour).Unix()},
	}
	if err := limits.Save(limits.FileFor(paths.LimitsDir(), "sess-ui"), st); err != nil {
		t.Fatal(err)
	}
	today := now.Local().Format("2006-01-02")
	h := limits.History{Days: []limits.DayUse{{Day: today, Weekly: 9, FiveHourPeak: 100}}}
	if err := limits.SaveHistory(paths.HistoryFile(), h); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "lookout")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	var m tea.Model = New(Options{Core: core, Store: db, Version: "test", Exe: exe})
	m = drive(m, tea.WindowSizeMsg{Width: 110, Height: 90}, LoadedMsg{}, tickMsg(now))
	v := plain(m.View())
	for _, want := range []string{"LAST 7 DAYS", "+9% of weekly · 5h limit hit", "5-hour limit at 85%"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	if f := limits.LoadFired(paths.AlertsFile()); len(f) == 0 {
		t.Error("fired alert not saved")
	}
	// A second dashboard doesn't repeat it.
	var m2 tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m2 = drive(m2, tea.WindowSizeMsg{Width: 110, Height: 90}, LoadedMsg{}, tickMsg(now))
	if strings.Contains(plain(m2.View()), "5-hour limit at 85%") {
		t.Error("alert repeated by a second dashboard")
	}

	// Replace the binary: after it settles, R restart is offered.
	if err := os.WriteFile(exe, []byte("v2-longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	m = drive(m, tickMsg(now.Add(time.Second)))
	if strings.Contains(plain(m.View()), "R restart") {
		t.Error("restart offered before the build settled")
	}
	m = drive(m, tickMsg(time.Now().Add(5*time.Second)))
	if !strings.Contains(plain(m.View()), "R restart") {
		t.Errorf("restart not offered\n%s", plain(m.View()))
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if !m.(Model).RestartRequested() || cmd == nil {
		t.Error("R should request a restart and quit")
	}
}

func TestActivityDetailAndSessionPicker(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("2"), key("enter"))
	v := plain(m.View())
	for _, want := range []string{"│ this still doesn't work", "1 tool call", "FILES EDITED ─", "WHAT CLAUDE DID · 1 STEP ─", "Edit", "CLAUDE'S LAST MESSAGE", "│ done"} {
		if !strings.Contains(v, want) {
			t.Errorf("activity detail missing %q\n%s", want, v)
		}
	}
	// The latest message is shown once, after the steps, in reading order.
	if n := strings.Count(v, "│ done"); n != 1 {
		t.Errorf("latest message shown %d times, want once\n%s", n, v)
	}
	if you, did, last := strings.Index(v, "YOU ─"), strings.Index(v, "WHAT CLAUDE DID"), strings.Index(v, "CLAUDE'S LAST MESSAGE"); !(you >= 0 && you < did && did < last) {
		t.Errorf("sections out of order (you %d, did %d, latest %d)\n%s", you, did, last, v)
	}
	// An older second session, so the picker has one to pin.
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	_ = os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-demo", "sess-old.jsonl"),
		[]byte(`{"type":"user","sessionId":"sess-old","cwd":"/work/demo","timestamp":"`+old+`","message":{"content":"older"}}`+"\n"), 0o644)
	core.Load()

	m = drive(m, key("esc"), key("s"))
	if v := plain(m.View()); m.(Model).tab != tabSessions || !strings.Contains(v, "SESSIONS") || !strings.Contains(v, "Following the session you last prompted") {
		t.Fatalf("s should open Sessions:\n%s", v)
	}
	// Picking any session, even the latest, gives it its own pinned tab
	// and opens Usage; Auto stays a click away.
	m = drive(m, key("enter"))
	if core.Pinned != "sess-ui" || m.(Model).tab != tabUsage {
		t.Errorf("enter should pin the session (pinned=%q, tab %d)", core.Pinned, m.(Model).tab)
	}
	// Claude Code lists its running sessions (none here), so sess-old is
	// known to be closed.
	_ = os.MkdirAll(core.LiveDir, 0o755)
	m = drive(m, key("s"), key("down"), key("enter"))
	if v := plain(m.View()); !strings.Contains(v, "◷ closed") || strings.Contains(v, "◆") || core.Pinned != "sess-old" {
		t.Errorf("enter on a closed session should show it as closed, not pinned (pinned=%q)\n%s", core.Pinned, v)
	}
	// Without that list, Lookout can't tell, so a pin is just a pin.
	_ = os.RemoveAll(core.LiveDir)
	m = drive(m, key("s"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if core.Pinned != "" || strings.Contains(plain(m.View()), "◆ pinned") || m.(Model).tab != tabActivity {
		t.Error("a should go back to following the latest session")
	}

	// P pins the session on screen, and P again unpins it.
	m = drive(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	if core.Pinned != "sess-ui" || !strings.Contains(plain(m.View()), "◆ pinned") {
		t.Errorf("P should pin the current session (pinned=%q)", core.Pinned)
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	if core.Pinned != "" {
		t.Errorf("P again should unpin (pinned=%q)", core.Pinned)
	}

	// Clicking a session in Sessions does what enter does.
	rowOf := func(text string) int {
		for y, l := range strings.Split(plain(m.View()), "\n") {
			if strings.Contains(l, text) {
				return y
			}
		}
		t.Fatalf("row with %q not found:\n%s", text, plain(m.View()))
		return -1
	}
	m = drive(m, key("s"))
	m = drive(m, click(10, rowOf("Following the session"))...) // the summary line above the list
	if core.Pinned != "" || m.(Model).tab != tabSessions {
		t.Fatal("clicking outside the list should do nothing")
	}
	m = drive(m, click(10, rowOf("older"))...)
	if core.Pinned != "sess-old" || m.(Model).tab != tabUsage {
		t.Errorf("click should pin the session and open Usage (pinned=%q)", core.Pinned)
	}
	m = drive(m, key("s"))
	m = drive(m, click(10, rowOf("this still doesn't work"))...)
	if core.Pinned != "sess-ui" {
		t.Errorf("clicking the latest session should pin it (pinned=%q)", core.Pinned)
	}
}

func TestChangesListsExistingFiles(t *testing.T) {
	core, db := setup(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-tmp-x")
	_ = os.MkdirAll(proj, 0o755)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	lines := `{"type":"user","sessionId":"sess-x","cwd":"` + dir + `","timestamp":"` + ts + `","message":{"content":"write main"}}` + "\n" +
		`{"type":"assistant","sessionId":"sess-x","cwd":"` + dir + `","timestamp":"` + ts + `","message":{"id":"x1","model":"claude-opus-test","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"` + file + `","content":"package main\n"}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "sess-x.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	core.Load()
	core.Pinned = "sess-x"
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 30}, LoadedMsg{}, key("3"))
	v := plain(m.View())
	for _, want := range []string{"main.go", "+1 -0", "written"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	if strings.Contains(v, "no longer exist") {
		t.Errorf("existing file counted as gone\n%s", v)
	}
}

func TestLimitsForAPIKeyAccount(t *testing.T) {
	core, db := setup(t)
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/lookout statusline"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, cost := 12.0, 3.5
	st := limits.State{Context: &c, ContextAt: time.Now().Unix(), CostUSD: &cost}
	if err := limits.Save(limits.FileFor(paths.LimitsDir(), "sess-ui"), st); err != nil {
		t.Fatal(err)
	}
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	v := plain(drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}).View())
	if !strings.Contains(v, "API key or pay-as-you-go") || !strings.Contains(v, "$3.50 this session") || strings.Contains(v, "Waiting for Claude Code") {
		t.Errorf("API-key account not recognised\n%s", v)
	}
	// The reported context replaces the token estimate.
	if !strings.Contains(v, " 12%") {
		t.Errorf("reported context not shown\n%s", v)
	}
	// Dollars across the panel: the session's own figure, estimates for the
	// rest, and a link to the claude.ai usage page.
	for _, want := range []string{"cost", "$3.50", "~$", "estimated from tokens", UsagePageURL, "o open usage on claude.ai"} {
		if !strings.Contains(v, want) {
			t.Errorf("pay-as-you-go view missing %q\n%s", want, v)
		}
	}
}

// A pay-as-you-go session that hasn't had a reply yet has no limits file of
// its own: the day and week dollars still show (from the latest session's
// file), and the header carries today's cost on every panel.
func TestCostsBeforeFirstReply(t *testing.T) {
	core, db := setup(t)
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/lookout statusline"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cost := 3.5
	st := limits.State{ContextAt: time.Now().Unix(), CostUSD: &cost}
	if err := limits.Save(limits.FileFor(paths.LimitsDir(), "other-session"), st); err != nil {
		t.Fatal(err)
	}
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{})
	v := plain(m.View())
	if !strings.Contains(v, "cost") || strings.Contains(v, "$3.50") {
		t.Errorf("want estimated costs, not another session's reported cost\n%s", v)
	}
	for _, k := range []string{"1", "2", "4"} {
		if v := plain(drive(m, key(k)).View()); !strings.Contains(strings.SplitN(v, "\n", 2)[0], "today $") &&
			!strings.Contains(strings.SplitN(v, "\n", 2)[0], "today ~$") {
			t.Errorf("panel %s: header has no cost badge\n%s", k, v)
		}
	}
}

func TestNoCostsForSubscribers(t *testing.T) {
	core, db := setup(t)
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/x/lookout statusline"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cost := 3.5
	st := limits.State{ContextAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(), CostUSD: &cost,
		FiveHour: &limits.Window{UsedPercent: 20, ResetsAt: time.Now().Add(2 * time.Hour).Unix()}}
	if err := limits.Save(limits.FileFor(paths.LimitsDir(), "sess-ui"), st); err != nil {
		t.Fatal(err)
	}
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	v := plain(drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}).View())
	if strings.Contains(v, "$") {
		t.Errorf("subscriber view shows dollars\n%s", v)
	}
	if !strings.Contains(strings.SplitN(v, "\n", 2)[0], "5h 20%") {
		t.Errorf("header has no 5h badge\n%s", v)
	}
	if !strings.Contains(v, UsagePageURL) || !strings.Contains(v, "o open usage on claude.ai") {
		t.Errorf("usage link missing for subscribers\n%s", v)
	}
}

func TestContextItemsSeeAllAndDetail(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("4"))
	v := plain(m.View())
	if !strings.Contains(v, "LARGEST ITEMS") || !strings.Contains(v, "a see all") {
		t.Fatalf("context list:\n%s", v)
	}
	m = drive(m, key("a"))
	if v := plain(m.View()); !strings.Contains(v, "ALL ITEMS") {
		t.Errorf("see all not shown:\n%s", v)
	}
	m = drive(m, key("enter"))
	v = plain(m.View())
	for _, want := range []string{"TO FREE UP ROOM", "/rewind", "/compact"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail missing %q\n%s", want, v)
		}
	}
	m = drive(m, key("esc"))
	if strings.Contains(plain(m.View()), "TO FREE UP ROOM") {
		t.Error("esc should return to the list")
	}
}

func TestCheatsheet(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 50}, LoadedMsg{}, key("9"))
	v := plain(m.View())
	for _, want := range []string{"CHEATSHEET", "FOR THIS SESSION", "/model opus", "claude --resume sess-ui", "/compact", "/rewind", "/resume"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	if _, cmd := m.Update(key("enter")); cmd == nil {
		t.Error("enter should copy the command")
	}
	if got := shellQuote("/work/my app"); got != "'/work/my app'" {
		t.Errorf("shellQuote = %q", got)
	}
	if got := shellQuote("/work/demo"); got != "/work/demo" {
		t.Errorf("shellQuote = %q", got)
	}
}

func TestMarkdownAndJSONHighlight(t *testing.T) {
	got := plain(strings.Join(markdown("# Title\nSome **bold** and `code`.\n- one\n- two that is long enough to wrap onto the next line here\n```\nraw *text*\n```", 30), "\n"))
	for _, want := range []string{"Title", "Some bold and code.", "• one", "• two that", "  raw *text*"} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**") || strings.Contains(got, "```") {
		t.Errorf("markup left in output:\n%s", got)
	}
	// Wrapped bullet lines are indented under the text.
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "wrap") {
			t.Errorf("wrapped bullet line not indented: %q", l)
		}
	}
	if got := plain(highlightLine("x.json", `  "model": "opus",`)); got != `  "model": "opus",` {
		t.Errorf("json highlight changed text: %q", got)
	}
}

func TestActivityPromptShownOnce(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("2"), key("enter"))
	v := plain(m.View())
	// Once in the turn list on the left, once in the viewer: never twice in the viewer.
	right := ""
	for _, l := range strings.Split(v, "\n") {
		if _, r, ok := strings.Cut(l, "│"); ok {
			right += r + "\n"
		}
	}
	if n := strings.Count(right, "this still doesn't work"); n != 1 {
		t.Errorf("prompt shown %d times in the viewer:\n%s", n, right)
	}
}

func TestActivityShowsWhatAPromptAnswers(t *testing.T) {
	core, db := setup(t)
	proj := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-demo")
	ts := func(d time.Duration) string { return time.Now().UTC().Add(d).Format(time.RFC3339Nano) }
	more := `{"type":"assistant","sessionId":"sess-ui","cwd":"/work/demo","timestamp":"` + ts(-30*time.Second) + `","message":{"id":"m3","model":"claude-sonnet-test","content":[{"type":"text","text":"Want me to add the cache?\nIt takes a minute.\nThird line.\nFourth line."}],"usage":{"input_tokens":1,"output_tokens":5}}}` + "\n" +
		`{"type":"user","sessionId":"sess-ui","cwd":"/work/demo","timestamp":"` + ts(-10*time.Second) + `","message":{"content":"yes"}}` + "\n"
	f, err := os.OpenFile(filepath.Join(proj, "sess-ui.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(more)
	f.Close()
	core.Load()

	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("2"), key("enter"))
	v := plain(m.View())
	for _, want := range []string{"Before this, Claude said:", "Want me to add the cache?", "Third line.", "full message: the next entry down", "│ yes"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	if strings.Contains(v, "Fourth line.") {
		t.Errorf("only the first %d lines should be quoted\n%s", inReplyLines, v)
	}
}

func TestMouseSelectCopies(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{})
	// A drag that starts on the tabs is a click, not a selection.
	m = drive(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m, cmd := m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionRelease})
	if cmd != nil {
		t.Error("a drag on the tabs shouldn't copy anything")
	}
	y := lipgloss.Height(m.(Model).viewHeader()) + 1
	m = drive(m, tea.MouseMsg{X: 0, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		tea.MouseMsg{X: 60, Y: y + 6, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if !strings.Contains(m.View(), "\x1b[0;7m") {
		t.Error("drag should highlight the selection")
	}
	m, cmd = m.Update(tea.MouseMsg{X: 60, Y: y + 6, Action: tea.MouseActionRelease})
	if cmd == nil {
		t.Fatal("release should copy the selection")
	}
	if strings.Contains(m.View(), "\x1b[0;7m") {
		t.Error("highlight should clear after release")
	}
}

func TestSelectionText(t *testing.T) {
	s := selection{from: cell{6, 1}, to: cell{2, 0}, box: [4]int{0, 100, 0, 10}}
	got := s.text([]string{"\x1b[1mhello\x1b[0m world  ", "second line"})
	if got != "llo world\nsecond" {
		t.Errorf("got %q", got)
	}
}

func TestSelectionStaysInPane(t *testing.T) {
	// A reader pane from column 6: the list on the left isn't copied.
	s := selection{from: cell{6, 0}, to: cell{10, 1}, box: [4]int{6, 22, 0, 10}}
	got := s.text([]string{"lst │ first line", "lst │ second line"})
	if got != "first line\nsecon" {
		t.Errorf("got %q", got)
	}
}

func click(x, y int) []tea.Msg {
	return []tea.Msg{
		tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease},
	}
}

func TestClickTabAndRow(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 160, Height: 40}, LoadedMsg{})
	mm := m.(Model)
	tab := -1
	for x := 0; x < 160 && tab != tabActivity; x++ {
		if mm.tabAt(x) == tabActivity {
			tab = x
		}
	}
	if tab < 0 {
		t.Fatal("Activity tab not found in the header")
	}
	m = drive(m, click(tab, 0)...)
	if m.(Model).tab != tabActivity {
		t.Fatalf("click on the tab should switch to Activity, got %d", m.(Model).tab)
	}
	// Clicking a list row selects that turn; clicking the reader focuses it.
	mm = m.(Model)
	top := lipgloss.Height(mm.viewHeader())
	l := mm.activityList(mm.listWidth())
	for y, row := range l.rows {
		if row == 0 {
			m = drive(m, click(2, top+y)...)
			break
		}
	}
	if m.(Model).aSel != 0 {
		t.Errorf("row click should select turn 0, got %d", m.(Model).aSel)
	}
	m = drive(m, click(mm.listWidth()+5, top+3)...)
	if !m.(Model).aFocus {
		t.Error("click in the reader should focus it")
	}
}

func TestClickRowOpensReaderAndContextItem(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 160, Height: 40}, LoadedMsg{}, key("2"))
	mm := m.(Model)
	top := lipgloss.Height(mm.viewHeader())
	for y, row := range mm.activityList(mm.listWidth()).rows {
		if row == 0 {
			m = drive(m, click(2, top+y)...)
			break
		}
	}
	if !m.(Model).aFocus {
		t.Error("clicking a turn should open it in the reader")
	}

	m = drive(m, key("4"))
	mm = m.(Model)
	lines := strings.Split(plain(mm.View()), "\n")
	for y, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "LARGEST ITEMS") {
			m = drive(m, click(5, y+1)...)
			break
		}
	}
	if !m.(Model).xFocus || !strings.Contains(plain(m.View()), "TO FREE UP ROOM") {
		t.Errorf("clicking an item should open its details\n%s", plain(m.View()))
	}
}

func TestDeclinedSuggestionGoesQuiet(t *testing.T) {
	m := Model{sugg: strategy.Suggestion{SessionID: "s", Action: strategy.Downgrade, Suggested: "sonnet", Current: "claude-opus-5-5"}}
	no := store.Row{SessionID: "s", Action: "downgrade", Suggested: "sonnet", NextModel: "claude-opus-5-5"}
	m.logRows = []store.Row{no}
	if m.declined() != 1 {
		t.Fatalf("declined = %d", m.declined())
	}
	m.logRows = append(m.logRows, no, store.Row{SessionID: "other", Action: "downgrade", Suggested: "sonnet", NextModel: "x"})
	if m.declined() != 2 {
		t.Fatalf("declined = %d", m.declined())
	}
}

func TestClickCheatsheetCopies(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("9"))
	mm := m.(Model)
	top := lipgloss.Height(mm.viewHeader())
	l := mm.cheatList(mm.width)
	y := -1
	for i, r := range l.rows {
		if r == 3 {
			y = i
			break
		}
	}
	m, _ = m.Update(click(4, top+y)[0])
	m, cmd := m.Update(click(4, top+y)[1])
	if m.(Model).kSel != 3 || cmd == nil {
		t.Errorf("click should select row 3 and copy it: sel=%d cmd=%v", m.(Model).kSel, cmd != nil)
	}
}

func TestActivityStepsOpen(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("2"), key("enter"))

	// x opens every step; the details load in the background.
	m, cmd := m.Update(key("x"))
	if cmd == nil {
		t.Fatal("x should load the step details")
	}
	if v := plain(m.View()); !strings.Contains(v, "loading…") {
		t.Errorf("expected a loading note\n%s", v)
	}
	m, _ = m.Update(cmd())
	v := plain(m.View())
	if !strings.Contains(v, "▾") || !strings.Contains(v, "still running") {
		t.Fatalf("step not shown open\n%s", v)
	}

	// Clicking the step's line closes it again.
	y := -1
	for i, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "▾") && strings.Contains(l, "Edit") {
			y = i
		}
	}
	if y < 0 {
		t.Fatalf("step line not found\n%s", v)
	}
	m = drive(m, click(m.(Model).listWidth()+10, y)...)
	if v := plain(m.View()); strings.Contains(v, "still running") {
		t.Errorf("click should close the step\n%s", v)
	}
}

func TestClickDoesNotHighlight(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{},
		tea.MouseMsg{X: 60, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if strings.Contains(m.View(), "\x1b[0;7m") {
		t.Error("a press without a drag should not highlight anything")
	}
	m = drive(m, tea.MouseMsg{X: 70, Y: 10, Action: tea.MouseActionMotion})
	if !strings.Contains(m.View(), "\x1b[0;7m") {
		t.Error("dragging should highlight the selection")
	}
}

func TestActivitySearch(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	typeText := func(s string) []tea.Msg {
		var out []tea.Msg
		for _, r := range s {
			out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return out
	}
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("2"), key("/"))
	m = drive(m, typeText("zzq")...)
	if v := plain(m.View()); !strings.Contains(v, "/zzq") || !strings.Contains(v, "no matches") {
		t.Fatalf("search not shown\n%s", v)
	}
	// Moving with no matches must not crash.
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter}, key("up"), key("down"), key("G"), key("n"))
	m = drive(m, key("/"))
	// Typing q must not quit or switch tabs while searching.
	if !m.(Model).aTyping || m.(Model).tab != tabActivity {
		t.Fatal("search lost focus")
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyEsc}, key("/"))
	m = drive(m, typeText("STILL")...)
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter})
	v := plain(m.View())
	if !strings.Contains(v, "this still doesn't work") || strings.Contains(v, "no matches") {
		t.Errorf("case-insensitive match not listed\n%s", v)
	}
	// n / p never move past the ends.
	m = drive(m, key("n"), key("n"), key("p"), key("p"))
	if m.(Model).aSel < 0 {
		t.Error("selection lost")
	}
}

func TestToolsPanel(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("6"))
	v := plain(m.View())
	for _, want := range []string{"6 Tools", "TOOLS", "1 call", "Edit", "this still doesn't work", "a.go"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	m = drive(m, key("enter"))
	if !m.(Model).tlFocus {
		t.Error("enter should focus the calls")
	}
	m = drive(m, key("esc"))
	if m.(Model).tlFocus {
		t.Error("esc should return to the list")
	}
}

func TestToolLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Bash":                             "Bash",
		"mcp__github__create_issue":        "github: create_issue",
		"mcp__claude_ai_Gmail__send_email": "Gmail: send_email",
	} {
		if got := toolLabel(in); got != want {
			t.Errorf("toolLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolsOpenFilterAndJump(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("6"), key("enter"))
	mm := m.(Model)
	if !mm.tlFocus || len(mm.tlCalls) != 1 || mm.tlCalls[0].id != "t1" {
		t.Fatalf("reader should hold the Edit call, got focus=%v calls=%v", mm.tlFocus, mm.tlCalls)
	}
	m, cmd := m.Update(key("enter"))
	if !m.(Model).tlOpen["t1"] || cmd == nil {
		t.Error("enter should open the call and load its details")
	}
	m = drive(m, key("f"))
	if v := plain(m.View()); !strings.Contains(v, "No failed calls") || !strings.Contains(v, "failed only") {
		t.Errorf("f should show failed calls only\n%s", v)
	}
	m = drive(m, key("f"), key("a"))
	mm = m.(Model)
	if mm.tab != tabActivity || !mm.aFocus || !mm.aOpen["t1"] {
		t.Errorf("a should open the call in Activity: tab=%d focus=%v", mm.tab, mm.aFocus)
	}
}

func TestIsDenied(t *testing.T) {
	cases := []struct {
		err  string
		fail bool
		want bool
	}{
		{"Permission for this action was denied by the Claude Code auto mode classifier.", true, true},
		{"The user doesn't want to proceed with this tool use.", true, true},
		{"Exit code 1\nTraceback", true, false},
		{"Permission for this action was denied", false, false},
	}
	for _, c := range cases {
		if got := isDenied(activity.Call{Fail: c.fail, Error: c.err}); got != c.want {
			t.Errorf("isDenied(%q, fail=%v) = %v, want %v", c.err, c.fail, got, c.want)
		}
	}
}

func TestToolKeySplitsSkills(t *testing.T) {
	s := activity.Session{Turns: []activity.Turn{{Calls: []activity.Call{
		{Name: "Skill", Summary: "pdf"},
		{Name: "Skill", Summary: "pdf"},
		{Name: "Skill", Summary: "docx"},
		{Name: "Bash", Summary: "run script"},
	}}}}
	got := map[string]int{}
	for _, st := range toolStats(s) {
		got[st.Name] = st.Calls
	}
	want := map[string]int{"skill: pdf": 2, "skill: docx": 1, "Bash": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toolStats = %v, want %v", got, want)
	}
}

func TestUpdateNotice(t *testing.T) {
	m := Model{o: Options{Version: "v0.6.3", Core: &app.Core{}}, now: time.Now()}
	m.onUpdate(updateMsg{latest: "v0.6.3"})
	if m.updNew != "" {
		t.Errorf("same version should not be reported, got %q", m.updNew)
	}
	m.onUpdate(updateMsg{latest: "v0.6.4"})
	if m.updNew != "v0.6.4" || !strings.Contains(m.flash, update.InstallCmd) {
		t.Errorf("newer release should be reported (updNew=%q flash=%q)", m.updNew, m.flash)
	}
	if m.updateCheck() != nil {
		t.Error("no check should run while [updates] check is off")
	}
}

func TestNoSessionPlaceholder(t *testing.T) {
	core, db := setup(t)
	core.CwdFilter = "/work/elsewhere" // --here in a folder with no session
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30}, LoadedMsg{})
	for _, k := range []string{"2", "3", "4", "5", "6"} {
		v := plain(drive(m, key(k)).View())
		for _, w := range []string{"Waiting for a Claude Code session in /work/elsewhere", "Start `claude`", "Press s"} {
			if !strings.Contains(v, w) {
				t.Errorf("panel %s: missing %q\n%s", k, w, v)
			}
		}
	}
	if v := plain(drive(m, key("1")).View()); !strings.Contains(v, "LIMITS") || !strings.Contains(v, "Waiting for a Claude Code session") {
		t.Errorf("usage panel should keep limits and say it's waiting:\n%s", v)
	}
	if v := plain(drive(m, key("7")).View()); strings.Contains(v, "Waiting for a Claude Code session") {
		t.Errorf("skills panel works without a session:\n%s", v)
	}
}

func TestChangesListsActions(t *testing.T) {
	core, db := setup(t)
	proj := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-demo")
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	lines := `{"type":"user","sessionId":"sess-act","cwd":"/work/demo","timestamp":"` + ts + `","message":{"content":"ship"}}` + "\n" +
		`{"type":"assistant","sessionId":"sess-act","cwd":"/work/demo","timestamp":"` + ts + `","message":{"id":"a1","model":"claude-opus-test","content":[{"type":"tool_use","id":"p1","name":"Bash","input":{"command":"git push origin main","description":"Push the fix"}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" +
		`{"type":"user","sessionId":"sess-act","cwd":"/work/demo","timestamp":"` + ts + `","message":{"content":[{"type":"tool_result","tool_use_id":"p1","content":"To github.com:x/y.git\n   abc..def  main -> main"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "sess-act.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	core.Load()
	core.Pinned = "sess-act"
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 140, Height: 30}, LoadedMsg{})
	// Opening Changes selects the only row, the action: its command and
	// output load from the transcript.
	m, cmd := m.Update(key("3"))
	v := plain(m.View())
	for _, want := range []string{"Other actions · best guess", "git push · Push the fix", "✓"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	m = drive(m, findSteps(cmd)...)
	v = plain(m.View())
	for _, want := range []string{"$ git push origin main", "main -> main", "succeeded"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
}

// findSteps runs a command batch and returns the step details it loads.
func findSteps(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case stepsMsg:
		return []tea.Msg{msg}
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, findSteps(c)...)
		}
		return out
	}
	return nil
}

func TestLongPagesScroll(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 70, Height: 24}, LoadedMsg{}, key("1"))
	v := plain(m.View())
	if !strings.Contains(v, "more lines") || strings.Contains(v, "last 10 min") {
		t.Fatalf("a page taller than the window should say how much is hidden:\n%s", v)
	}
	m = drive(m, key("G"))
	if v := plain(m.View()); !strings.Contains(v, "last 10 min") || strings.Contains(v, "more lines") {
		t.Fatalf("at the end, the last lines show and the hint goes:\n%s", v)
	}
	m = drive(m, key("g"))
	if v := plain(m.View()); !strings.Contains(v, "LIMITS") {
		t.Fatalf("g goes back to the top:\n%s", v)
	}
	// A tall window shows the whole page with no hint.
	m = drive(m, tea.WindowSizeMsg{Width: 130, Height: 60})
	if v := plain(m.View()); !strings.Contains(v, "last 10 min") || strings.Contains(v, "more lines") {
		t.Fatalf("whole page expected:\n%s", v)
	}
}
