package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/store"
)

// TestScreenshot renders the Usage panel from synthetic data, in color, for
// the README. It runs only when LOOKOUT_SCREENSHOT names an output file:
//
//	LOOKOUT_SCREENSHOT=/tmp/shot.ansi go test ./internal/ui -run Screenshot
func TestScreenshot(t *testing.T) {
	out := os.Getenv("LOOKOUT_SCREENSHOT")
	if out == "" {
		t.Skip("set LOOKOUT_SCREENSHOT to render")
	}
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("LOOKOUT_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("LOOKOUT_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("PATH", root)
	_ = os.MkdirAll(claude, 0o755)
	_ = os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"/usr/local/bin/lookout statusline"}}`), 0o644)

	now := time.Now()
	proj := filepath.Join(claude, "projects", "-home-dev-acme-api")
	_ = os.MkdirAll(proj, 0o755)
	var lines []string
	msg := 0
	add := func(at time.Time, model string, in, outTok, cw, cr int) {
		msg++
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"id":"m%d","model":%q,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}}`,
			at.UTC().Format(time.RFC3339Nano), msg, model, in, outTok, cw, cr))
	}
	lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"content":"add rate limiting to the orders endpoint"}}`,
		now.Add(-50*time.Minute).UTC().Format(time.RFC3339Nano)))
	weights := []int{3, 5, 2, 6, 4, 7, 5}
	for d := 6; d >= 1; d-- {
		for i := 0; i < weights[d]*6; i++ {
			add(now.AddDate(0, 0, -d).Add(time.Duration(i)*4*time.Minute), "claude-sonnet-5-5", 40, 900, 3000, 60000)
		}
	}
	for i := 0; i < 40; i++ {
		model := "claude-opus-5-5"
		if i%4 == 0 {
			model = "claude-haiku-5-5"
		}
		add(now.Add(-47*time.Minute+time.Duration(i)*70*time.Second), model, 30, 1200, 2500, 85000)
	}
	_ = os.WriteFile(filepath.Join(proj, "7f3c2a91-demo.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	st := limits.State{
		UpdatedAt: now.Add(-20 * time.Second).Unix(),
		FiveHour:  &limits.Window{UsedPercent: 46, ResetsAt: now.Add(2*time.Hour + 41*time.Minute).Unix()},
		SevenDay:  &limits.Window{UsedPercent: 38, ResetsAt: now.Add(3*24*time.Hour + 7*time.Hour).Unix()},
		Samples:   []limits.Sample{{At: now.Add(-28 * time.Minute).Unix(), FiveHour: ptrF(38), SevenDay: ptrF(37)}},
	}
	_ = limits.Save(limits.FileFor(paths.LimitsDir(), "7f3c2a91-demo"), st)
	var h limits.History
	for i, wk := range []float64{4, 7, 3, 9, 5, 6, 4} {
		h.Days = append(h.Days, limits.DayUse{Day: now.AddDate(0, 0, i-6).Format("2006-01-02"), Weekly: wk, FiveHourPeak: []float64{52, 88, 30, 100, 64, 71, 46}[i]})
	}
	_ = limits.SaveHistory(paths.HistoryFile(), h)
	// Mark today's alerts as shown so the footer shows the keys.
	_ = limits.SaveFired(paths.AlertsFile(), limits.Fired{}, now)

	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	cfg := config.Default()
	core := app.New(cfg, "", nil)
	core.Load()
	db, err := store.Open(filepath.Join(root, "data", "lookout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var m tea.Model = New(Options{Core: core, Store: db, Version: "demo"})
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 46}, LoadedMsg{}, tickMsg(now))
	if err := os.WriteFile(out, []byte(m.View()), 0o644); err != nil {
		t.Fatal(err)
	}
}
