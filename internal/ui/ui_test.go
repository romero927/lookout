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
	"github.com/romero927/lookout/internal/crosscheck"
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
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("LOOKOUT_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("LOOKOUT_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("PATH", root) // no codex on PATH

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
	_ = crosscheck.Save(filepath.Join(root, "data", "crosscheck", "reviews"), crosscheck.Review{
		ID: crosscheck.NewID(now), Status: crosscheck.StatusDone, Verdict: "CONCERNS", CreatedAt: now,
		Summary: "VERDICT: CONCERNS\n- tests were not run", Excerpt: "All fixed and tested.",
	})
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
			{"1", []string{"LOOKOUT", "LIMITS", "not connected", "SESSION", "sess-ui", "TOKENS", "burn"}},
			{"2", []string{"RECOMMENDATION", "Upgrade to opus", "frustration", "/model opus"}},
			{"3", []string{"FILES", "settings.json"}},
			{"4", []string{"CROSS-CHECK", "CONCERNS"}},
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
			if os.Getenv("LOOKOUT_SHOW") != "" && (size.Width == 70 || p.key == "4") {
				t.Logf("panel %s:\n%s", p.key, v)
			}
		}
	}
}

func TestFilesMasksSecretsAndBlocksEditByDefault(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{}, key("3"), key("enter"))
	v := plain(m.View())
	if strings.Contains(v, "sk-secret") || !strings.Contains(v, "••••••") {
		t.Fatalf("secret not masked:\n%s", v)
	}
	m = drive(m, key("esc"), key("e"))
	if v := plain(m.View()); !strings.Contains(v, "read-only") {
		t.Fatalf("edit not refused:\n%s", v)
	}
}

func TestCrossCheckToggleKey(t *testing.T) {
	core, db := setup(t)
	var m tea.Model = New(Options{Core: core, Store: db})
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40}, LoadedMsg{})
	if !strings.Contains(plain(m.View()), "x-check off") {
		t.Fatal("cross-check should start off")
	}
	m = drive(m, key("x"))
	if !strings.Contains(plain(m.View()), "X-CHECK ON") {
		t.Fatal("x did not turn cross-check on")
	}
	m = drive(m, key("x"))
	if !strings.Contains(plain(m.View()), "x-check off") {
		t.Fatal("x did not turn cross-check off")
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
