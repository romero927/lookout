package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/store"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

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
			{"1", []string{"LOOKOUT", "LIMITS", "not connected", "SESSION", "sess-ui", "TOKENS", "last 10 min"}},
			{"2", []string{"ACTIVITY", "this still doesn't work", "STEP BY STEP", "Edit"}},
			{"3", []string{"CHANGES", "1 edited file no longer"}},
			{"4", []string{"CONTEXT", "Startup", "LARGEST ITEMS"}},
			{"5", []string{"RECOMMENDATION", "Upgrade to opus", "frustration", "/model opus", "MODELS YOU CAN SWITCH TO", "Fastest for quick answers"}},
			{"7", []string{"SKILLS"}},
			{"8", []string{"FILES", "settings.json"}},
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
	_ = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{})
	rows, err := db.Recent(5)
	if err != nil || len(rows) != 1 || rows[0].Action != "upgrade" || rows[0].Suggested != "opus" {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
}

func TestFormat(t *testing.T) {
	tests := map[int64]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 999_999: "1000k", 1_500_000: "1.5M", 12_345_678: "12.35M", 2_000_000_000: "2B"}
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
		for _, want := range []string{"5-HOUR", "62% used", "38% left", "recent pace 60%/h", "before reset", "WEEKLY", "30% used", "avg pace", "5h 62%", "wk 30%"} {
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
	for _, want := range []string{"WEEKLY LIMIT BY DAY", "+9% of weekly · hit 5h limit", "5-hour limit at 85%"} {
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
	for _, want := range []string{"│ this still doesn't work", "1 tool call", "FILES EDITED ─", "STEP BY STEP ─", "Edit", "CLAUDE'S FINAL MESSAGE ─", "┄┄┄", "│ done", "(final message, shown above)"} {
		if !strings.Contains(v, want) {
			t.Errorf("activity detail missing %q\n%s", want, v)
		}
	}
	m = drive(m, key("esc"), key("s"))
	if v := plain(m.View()); !strings.Contains(v, "SESSIONS") || !strings.Contains(v, "Following the latest") {
		t.Fatalf("picker not shown:\n%s", v)
	}
	m = drive(m, key("enter"))
	if v := plain(m.View()); !strings.Contains(v, "pinned") || core.Pinned != "sess-ui" {
		t.Errorf("enter should pin the session (pinned=%q)\n%s", core.Pinned, v)
	}
	m = drive(m, key("s"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if core.Pinned != "" || strings.Contains(plain(m.View()), "◆ pinned") {
		t.Error("a should go back to following the latest session")
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
	for _, want := range []string{"TO FREE UP THIS ROOM", "/compact Leave out", "/rewind", "c copies"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail missing %q\n%s", want, v)
		}
	}
	if _, cmd := m.Update(key("c")); cmd == nil {
		t.Error("c should copy the /compact line")
	}
	m = drive(m, key("esc"))
	if strings.Contains(plain(m.View()), "TO FREE UP") {
		t.Error("esc should return to the list")
	}
}

func TestCheatsheet(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db, Version: "test"})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 50}, LoadedMsg{}, key("6"))
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
	for _, want := range []string{"In reply to Claude:", "Want me to add the cache?", "Third line.", "full message: the next entry down", "│ yes"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q\n%s", want, v)
		}
	}
	if strings.Contains(v, "Fourth line.") {
		t.Errorf("only the first %d lines should be quoted\n%s", inReplyLines, v)
	}
}
