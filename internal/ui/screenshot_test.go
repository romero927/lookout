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

// TestScreenshot renders the Usage panel, an Activity turn and other panels from synthetic
// data, in color, for the README. It runs only when LOOKOUT_SCREENSHOT names
// an output file; the other screens go next to it as NAME-<panel>.ansi:
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
	t.Setenv("LOOKOUT_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("LOOKOUT_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("PATH", root)
	t.Setenv("TMUX", "/tmp/tmux-shot,1,0") // show the + claude button
	_ = os.MkdirAll(claude, 0o755)
	_ = os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"/usr/local/bin/lookout statusline"}}`), 0o644)

	now := time.Now()
	proj := filepath.Join(claude, "projects", "-home-dev-acme-api")
	_ = os.MkdirAll(proj, 0o755)
	var lines []string
	msg := 0
	ts := func(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
	// Usage-only responses (no content), so they fill the charts without
	// cluttering Activity.
	add := func(at time.Time, model string, in, outTok, cw, cr int) {
		msg++
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"id":"m%d","model":%q,"content":[],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}}`,
			ts(at), msg, model, in, outTok, cw, cr))
	}
	// One scripted turn for the Activity screenshot.
	say := func(at time.Time, content string) {
		msg++
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"id":"m%d","model":"claude-opus-5-5","content":[%s],"usage":{"input_tokens":20,"output_tokens":400,"cache_creation_input_tokens":800,"cache_read_input_tokens":40000}}}`,
			ts(at), msg, content))
	}
	result := func(at time.Time, id, text string) {
		lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}}`,
			ts(at), id, text))
	}
	tool := func(at time.Time, id, name, input string, took time.Duration, out string) {
		say(at, fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":%s}`, id, name, input))
		result(at.Add(took), id, out)
	}
	text := func(at time.Time, s string) {
		say(at, fmt.Sprintf(`{"type":"text","text":%q}`, s))
	}
	lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"7f3c2a91-demo","cwd":"/home/dev/acme-api","timestamp":%q,"message":{"content":"add rate limiting to the orders endpoint"}}`,
		ts(now.Add(-50*time.Minute))))
	t0 := now.Add(-50 * time.Minute)
	text(t0.Add(20*time.Second), "I'll find the orders handler, then put a token-bucket limiter in front of it, keyed by API key.")
	tool(t0.Add(30*time.Second), "toolu_demo1", "Bash", `{"command":"grep -rn \"func.*Order\" internal/http","description":"Find the orders endpoint handlers"}`, 2*time.Second,
		"internal/http/orders.go:18:func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {\ninternal/http/orders.go:52:func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {")
	tool(t0.Add(50*time.Second), "toolu_demo2", "Read", `{"file_path":"/home/dev/acme-api/internal/http/orders.go"}`, time.Second, "package http\n...")
	tool(t0.Add(2*time.Minute), "toolu_demo3", "Write", `{"file_path":"/home/dev/acme-api/internal/ratelimit/bucket.go","content":"package ratelimit\n\n// Bucket allows n requests per interval per key.\ntype Bucket struct {\n\tn        int\n\tinterval time.Duration\n}\n"}`, time.Second, "File created successfully")
	tool(t0.Add(3*time.Minute), "toolu_demo4", "Edit", `{"file_path":"/home/dev/acme-api/internal/http/orders.go","old_string":"func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {","new_string":"func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {\n\tif !h.limit.Allow(apiKey(r)) {\n\t\thttp.Error(w, \"too many requests\", http.StatusTooManyRequests)\n\t\treturn\n\t}"}`, time.Second, "The file has been updated.")
	tool(t0.Add(4*time.Minute), "toolu_demo5", "Bash", `{"command":"go test ./internal/... 2>&1 | tail -4","description":"Run the tests"}`, 14*time.Second,
		"ok  \tacme/internal/http\t0.412s\nok  \tacme/internal/orders\t0.233s\nok  \tacme/internal/ratelimit\t0.118s")
	text(t0.Add(5*time.Minute), "Rate limiting is in. **POST /orders** now allows 20 requests a minute per API key and answers `429 Too Many Requests` beyond that. The limiter lives in `internal/ratelimit`, and the tests pass.")
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

	// A second project earlier in the 5-hour window, so the session's share
	// of the limits is a realistic part of the whole.
	other := filepath.Join(claude, "projects", "-home-dev-web")
	_ = os.MkdirAll(other, 0o755)
	var olines []string
	for i := 0; i < 30; i++ {
		olines = append(olines, fmt.Sprintf(`{"type":"assistant","sessionId":"b51e07c4-demo","cwd":"/home/dev/web","timestamp":%q,"message":{"id":"w%d","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":30,"output_tokens":1500,"cache_creation_input_tokens":3000,"cache_read_input_tokens":90000}}}`,
			ts(now.Add(-3*time.Hour+time.Duration(i)*3*time.Minute)), i))
	}
	_ = os.WriteFile(filepath.Join(other, "b51e07c4-demo.jsonl"), []byte(strings.Join(olines, "\n")+"\n"), 0o644)

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

	// Activity: the scripted turn, reading it, with the test run opened.
	m = drive(m, tea.WindowSizeMsg{Width: 110, Height: 40}, key("2"), key("enter"))
	m, cmd := m.(Model).toggleStep("toolu_demo5")
	if cmd == nil {
		t.Fatal("opening a step should load it")
	}
	m, _ = m.Update(cmd())
	if v := m.View(); !strings.Contains(v, "acme/internal/ratelimit") {
		t.Fatalf("step output not shown:\n%s", v)
	}
	act := strings.TrimSuffix(out, ".ansi") + "-activity.ansi"
	if err := os.WriteFile(act, []byte(m.View()), 0o644); err != nil {
		t.Fatal(err)
	}

	// The other panels, each at its starting view, as NAME-<panel>.ansi.
	for _, p := range []struct{ key, name string }{
		{"4", "context"}, {"5", "models"}, {"9", "cheatsheet"},
	} {
		m = drive(m, tea.WindowSizeMsg{Width: 110, Height: 40}, key("esc"), key("esc"), key(p.key))
		file := strings.TrimSuffix(out, ".ansi") + "-" + p.name + ".ansi"
		if err := os.WriteFile(file, []byte(m.View()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
