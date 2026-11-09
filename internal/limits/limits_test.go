package limits

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParseInput(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantLimit bool
		wantErr   bool
	}{
		{"subscriber", `{"model":{"display_name":"Opus"},"context_window":{"used_percentage":12.5},"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":1762700000},"seven_day":{"used_percentage":23.5,"resets_at":1763000000}},"extra":{"x":1}}`, true, false},
		{"api key user: no rate_limits", `{"model":{"display_name":"Sonnet"},"context_window":{"used_percentage":null}}`, false, false},
		{"only weekly", `{"rate_limits":{"seven_day":{"used_percentage":5,"resets_at":1763000000}}}`, true, false},
		{"empty rate_limits", `{"rate_limits":{}}`, false, false},
		{"garbage", `not json`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := ParseInput(strings.NewReader(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			var s State
			if got := s.Apply(in, t0); got != tt.wantLimit {
				t.Fatalf("Apply = %v, want %v", got, tt.wantLimit)
			}
		})
	}
}

func TestApplySamplesAndPrune(t *testing.T) {
	var s State
	in := func(p float64) StatusInput {
		var x StatusInput
		x.RateLimits = &struct {
			FiveHour *Window `json:"five_hour"`
			SevenDay *Window `json:"seven_day"`
		}{FiveHour: &Window{UsedPercent: p, ResetsAt: t0.Add(time.Hour).Unix()}}
		return x
	}
	s.Apply(in(10), t0)
	s.Apply(in(10), t0.Add(10*time.Second)) // unchanged, too soon: no sample
	s.Apply(in(11), t0.Add(20*time.Second)) // changed: sample
	s.Apply(in(11), t0.Add(2*time.Minute))  // unchanged but a minute passed: sample
	if len(s.Samples) != 3 {
		t.Fatalf("samples = %d, want 3", len(s.Samples))
	}
	s.Apply(in(12), t0.Add(7*time.Hour)) // older samples pruned
	if len(s.Samples) != 1 || *s.Samples[0].FiveHour != 12 {
		t.Fatalf("after prune: %+v", s.Samples)
	}
	if s.SevenDay != nil || s.FiveHour.UsedPercent != 12 {
		t.Fatalf("state = %+v", s)
	}
}

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "limits.json")
	if Load(p).HasData() {
		t.Fatal("missing file should be empty")
	}
	want := State{UpdatedAt: t0.Unix(), FiveHour: &Window{42, t0.Unix()}, Samples: []Sample{{At: 1, FiveHour: f(42)}}}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	got := Load(p)
	if !got.HasData() || got.FiveHour.UsedPercent != 42 || len(got.Samples) != 1 {
		t.Fatalf("got %+v", got)
	}
	if err := os.WriteFile(p, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Load(p).HasData() {
		t.Fatal("broken file should load as empty")
	}
}

func TestCompute(t *testing.T) {
	now := t0
	reset := now.Add(2 * time.Hour) // window started 3h ago
	tests := []struct {
		name        string
		used        float64
		resetsAt    time.Time
		samples     []Sample
		wantKnown   bool
		wantRecent  bool
		wantRate    float64
		wantProj    float64
		wantHits    bool
		wantLevel   Level
		wantExpired bool
	}{
		{
			name: "average pace, comfortable", used: 30, resetsAt: reset,
			wantKnown: true, wantRate: 10, wantProj: 50, wantLevel: LevelOK,
		},
		{
			name: "recent pace beats average", used: 60, resetsAt: reset,
			samples:   []Sample{{At: now.Add(-40 * time.Minute).Unix(), FiveHour: f(10)}, {At: now.Add(-30 * time.Minute).Unix(), FiveHour: f(45)}},
			wantKnown: true, wantRecent: true, wantRate: 30, wantProj: 120, wantHits: true, wantLevel: LevelOver,
		},
		{
			name: "recent pace while idle is low", used: 50, resetsAt: reset,
			samples:   []Sample{{At: now.Add(-20 * time.Minute).Unix(), FiveHour: f(50)}},
			wantKnown: true, wantRecent: true, wantRate: 0, wantProj: 50, wantLevel: LevelOK,
		},
		{
			name: "samples from before a reset are ignored", used: 4, resetsAt: now.Add(4*time.Hour + 55*time.Minute),
			samples:   []Sample{{At: now.Add(-20 * time.Minute).Unix(), FiveHour: f(90)}},
			wantKnown: false, wantProj: 4, wantLevel: LevelOK,
		},
		{
			name: "watch above 80 projected", used: 51, resetsAt: reset,
			wantKnown: true, wantRate: 17, wantProj: 85, wantLevel: LevelWatch,
		},
		{
			name: "already at the limit", used: 100, resetsAt: reset,
			wantKnown: true, wantRate: 100.0 / 3, wantProj: 100 + 200.0/3, wantLevel: LevelOver,
		},
		{
			name: "expired window", used: 70, resetsAt: now.Add(-time.Minute),
			wantExpired: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Compute(Window{tt.used, tt.resetsAt.Unix()}, FiveHour, tt.samples, FiveHourOf, now)
			if p.Expired != tt.wantExpired {
				t.Fatalf("expired = %v", p.Expired)
			}
			if tt.wantExpired {
				return
			}
			if p.Known != tt.wantKnown || p.Recent != tt.wantRecent {
				t.Fatalf("known=%v recent=%v", p.Known, p.Recent)
			}
			if !near(p.RatePerHour, tt.wantRate) || !near(p.Projected, tt.wantProj) {
				t.Fatalf("rate=%.2f proj=%.2f, want %.2f %.2f", p.RatePerHour, p.Projected, tt.wantRate, tt.wantProj)
			}
			if !p.HitsAt.IsZero() != tt.wantHits {
				t.Fatalf("hitsAt = %v", p.HitsAt)
			}
			if p.Level() != tt.wantLevel {
				t.Fatalf("level = %v, want %v", p.Level(), tt.wantLevel)
			}
		})
	}
}

func TestStatusLine(t *testing.T) {
	in, err := ParseInput(strings.NewReader(`{"model":{"display_name":"Opus 5.5"},"context_window":{"used_percentage":12.4}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := State{FiveHour: &Window{42, t0.Add(2*time.Hour + 23*time.Minute).Unix()}, SevenDay: &Window{23, t0.Add(80 * time.Hour).Unix()}}
	got := StatusLine(in, s, t0, false)
	want := "Opus 5.5 · ctx 12% · 5h 42% (resets 2h23m) · week 23%"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := StatusLine(StatusInput{}, State{}, t0, false); got != "lookout" {
		t.Fatalf("empty = %q", got)
	}
}

func TestStatusLineSetting(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		content          string
		configured, ours bool
	}{
		{`{}`, false, false},
		{`{"statusLine":{"type":"command","command":"~/bin/my-line.sh"}}`, true, false},
		{`{"statusLine":{"type":"command","command":"/x/bin/lookout statusline"}}`, true, true},
		{`not json`, false, false},
	}
	for _, tt := range tests {
		c, o := StatusLineSetting(write(tt.content))
		if c != tt.configured || o != tt.ours {
			t.Errorf("%s: got %v %v", tt.content, c, o)
		}
	}
	if !strings.Contains(StatusLineSnippet("/a b/lookout"), `"'/a b/lookout' statusline"`) {
		t.Errorf("snippet should quote paths with spaces: %s", StatusLineSnippet("/a b/lookout"))
	}
}

func TestPerSessionFiles(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := Pick(dir, "a"); ok {
		t.Fatal("empty dir should have nothing")
	}
	if got := filepath.Base(FileFor(dir, "../../etc/x y")); got != "etcxy.json" {
		t.Fatalf("FileFor not sanitized: %q", got)
	}
	save := func(id string, used float64, age time.Duration) {
		p := FileFor(dir, id)
		if err := Save(p, State{UpdatedAt: t0.Unix(), FiveHour: &Window{used, t0.Unix()}}); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	save("old", 10, 9*24*time.Hour)
	save("other", 33, time.Minute)
	save("mine", 39, time.Hour)

	// Two sessions reporting different numbers stay separate.
	if p, _, _ := Pick(dir, "mine"); Load(p).FiveHour.UsedPercent != 39 {
		t.Fatalf("exact pick = %s", p)
	}
	// Unknown session: the most recently updated file.
	if p, _, _ := Pick(dir, "nope"); Load(p).FiveHour.UsedPercent != 33 {
		t.Fatalf("fallback pick = %s", p)
	}
	Prune(dir, time.Now())
	if _, err := os.Stat(FileFor(dir, "old")); !os.IsNotExist(err) {
		t.Fatal("old session file should be pruned")
	}
	if _, err := os.Stat(FileFor(dir, "mine")); err != nil {
		t.Fatal("recent file pruned")
	}
}

func TestApplyContextWithoutLimits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var in StatusInput
	c := 37.0
	in.ContextWindow.UsedPercentage = &c
	var s State
	if !s.Apply(in, now) {
		t.Fatal("context-only input should be recorded")
	}
	if !s.SeenNoLimits() || s.HasData() {
		t.Errorf("API-key style state: %+v", s)
	}
	if p, ok := s.ContextPercent(now.Add(time.Minute), 30*time.Minute); !ok || p != 37 {
		t.Errorf("context %v %v", p, ok)
	}
	if _, ok := s.ContextPercent(now.Add(time.Hour), 30*time.Minute); ok {
		t.Error("stale context should not be used")
	}
	if (&State{}).Apply(StatusInput{}, now) {
		t.Error("empty input should not be recorded")
	}
	cost := 1.234
	in.Cost.TotalCostUSD = &cost
	in.Model.DisplayName = "Opus 5.5"
	s.Apply(in, now)
	if s.CostUSD == nil || *s.CostUSD != cost {
		t.Errorf("cost not recorded: %+v", s)
	}
	if got := StatusLine(in, s, now, false); got != "Opus 5.5 · ctx 37% · $1.23" {
		t.Errorf("status line %q", got)
	}
	if Money(0.004) != "<$0.01" || Money(0) != "$0.00" {
		t.Error("Money formatting")
	}
}
