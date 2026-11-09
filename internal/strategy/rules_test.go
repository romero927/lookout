package strategy

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/transcript"
)

var now = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

const (
	opus   = "claude-opus-test"
	sonnet = "claude-sonnet-test"
	haiku  = "claude-haiku-test"
)

// turn builds an active turn.
func turn(model, prompt string, opts ...func(*Turn)) *Turn {
	t := &Turn{Model: model, Prompt: prompt, Responses: 1, OutputTokens: 300, Edits: map[string]int{}}
	for _, o := range opts {
		o(t)
	}
	return t
}

func edits(files ...string) func(*Turn) {
	return func(t *Turn) {
		for _, f := range files {
			t.Edits[f]++
			t.ToolCalls++
			t.Calls = append(t.Calls, ToolCall{File: f})
		}
	}
}
func errs(n, tests int) func(*Turn) {
	return func(t *Turn) {
		t.ToolErrors += n
		t.TestFailures += tests
		t.ToolCalls += n
		for i := 0; i < n; i++ {
			t.Calls = append(t.Calls, ToolCall{Fail: true, Test: i < tests})
		}
	}
}
func tools(n int) func(*Turn) {
	return func(t *Turn) {
		t.ToolCalls += n
		for i := 0; i < n; i++ {
			t.Calls = append(t.Calls, ToolCall{})
		}
	}
}
func output(n int64) func(*Turn) {
	return func(t *Turn) { t.OutputTokens = n }
}

func state(model string, turns ...*Turn) SessionState {
	return SessionState{ID: "s", Model: model, Turns: turns}
}

func TestEvaluate(t *testing.T) {
	cfg := config.Default().Models
	long := strings.Repeat("x", cfg.LongPromptChars)
	tests := []struct {
		name      string
		st        SessionState
		cfg       func(*config.Models)
		action    Action
		suggested string
		signal    string // a signal name that must be present
		reasonHas string
		noSignals bool
	}{
		{
			name:   "no activity",
			st:     SessionState{ID: "s"},
			action: Keep, reasonHas: "Waiting",
		},
		{
			name:   "unknown model tier",
			st:     state("gpt-something", turn("gpt-something", "hi")),
			action: Keep, reasonHas: "model_tiers",
		},
		{
			name: "repeated edits on sonnet upgrades to opus",
			st: state(sonnet,
				turn(sonnet, "fix it", edits("/p/app.go", "/p/app.go")),
				turn(sonnet, "and again", edits("/p/app.go", "/p/app.go")),
			),
			action: Upgrade, suggested: "opus", signal: "repeated-edits", reasonHas: "app.go edited 4× in the last 4 tool calls",
		},
		{
			name: "repeated edits below threshold keeps",
			st: state(sonnet,
				turn(sonnet, "fix it please now", edits("/p/app.go", "/p/app.go", "/p/x.go")),
				turn(sonnet, "ok continue with that", edits("/p/app.go")),
			),
			action: Keep,
		},
		{
			name: "edits outside recent window don't count",
			st: state(sonnet,
				turn(sonnet, "old work on app", edits("/p/app.go", "/p/app.go", "/p/app.go")),
				turn(sonnet, "something else 1", edits("/p/app.go")),
				turn(sonnet, "something else 2", tools(4)),
				turn(sonnet, "something else 3", tools(4)),
			),
			action: Keep,
		},
		{
			name: "old trouble inside one long turn ages out",
			st: state(sonnet,
				turn(sonnet, "build the whole feature end to end", edits("/p/a.go", "/p/a.go", "/p/a.go", "/p/a.go"), errs(4, 4), tools(30)),
			),
			action: Keep,
		},
		{
			name: "recent trouble inside one long turn counts",
			st: state(sonnet,
				turn(sonnet, "build the whole feature end to end", tools(40), edits("/p/a.go", "/p/a.go", "/p/a.go", "/p/a.go")),
			),
			action: Upgrade, suggested: "opus", signal: "repeated-edits",
		},
		{
			name: "failing tests upgrade",
			st: state(sonnet,
				turn(sonnet, "run the tests", errs(2, 2)),
				turn(sonnet, "try to fix those", errs(1, 1)),
			),
			action: Upgrade, suggested: "opus", signal: "tool-errors", reasonHas: "test/build failures",
		},
		{
			name: "frustration phrase upgrades haiku one tier",
			st: state(haiku,
				turn(haiku, "it's STILL BROKEN after that change", tools(4)),
			),
			action: Upgrade, suggested: "sonnet", signal: "frustration",
		},
		{
			name: "two signals jump straight to top tier",
			st: state(haiku,
				turn(haiku, "this doesn't work", errs(3, 0)),
			),
			action: Upgrade, suggested: "opus",
		},
		{
			name: "multi-file turn upgrades",
			st: state(sonnet,
				turn(sonnet, "refactor the storage layer across packages", edits("/a", "/b", "/c", "/d", "/e")),
			),
			action: Upgrade, suggested: "opus", signal: "multi-file",
		},
		{
			name:   "long prompt upgrades",
			st:     state(sonnet, turn(sonnet, long, tools(4))),
			action: Upgrade, suggested: "opus", signal: "long-prompt",
		},
		{
			name: "upgrade signals on top tier keep",
			st: state(opus,
				turn(opus, "still broken", tools(4)),
			),
			action: Keep, signal: "frustration", reasonHas: "top tier",
		},
		{
			name: "simple streak on opus downgrades",
			st: state(opus,
				turn(opus, "rename the variable foo to bar in util"),
				turn(opus, "now add a doc comment to that function"),
				turn(opus, "and update the changelog line for it", edits("/p/CHANGELOG.md")),
			),
			action: Downgrade, suggested: "sonnet", signal: "simple-streak",
		},
		{
			name: "streak broken by an error",
			st: state(opus,
				turn(opus, "rename the variable foo to bar in util"),
				turn(opus, "now add a doc comment to that function", errs(1, 0)),
				turn(opus, "and update the changelog entry for this change too"),
			),
			action: Keep,
		},
		{
			name: "streak broken by heavy output",
			st: state(opus,
				turn(opus, "rename the variable foo to bar in util"),
				turn(opus, "write the whole module from scratch now", output(20000)),
				turn(opus, "and update the changelog entry for this change too"),
			),
			action: Keep,
		},
		{
			name: "streak doesn't count turns on lower tiers",
			st: state(opus,
				turn(sonnet, "rename the variable foo to bar in util"),
				turn(sonnet, "now add a doc comment to that function"),
				turn(opus, "and update the changelog entry for this change too"),
			),
			action: Keep,
		},
		{
			name: "two short simple prompts on opus downgrade",
			st: state(opus,
				turn(opus, "what's 2+2?"),
				turn(opus, "and 3+3?"),
			),
			action: Downgrade, suggested: "sonnet", signal: "short-prompt",
		},
		{
			name:   "one short prompt alone keeps",
			st:     state(opus, turn(opus, "please write the whole migration plan for the database layer"), turn(opus, "thanks")),
			action: Keep,
		},
		{
			name: "short prompt on sonnet doesn't push to haiku",
			st: state(sonnet,
				turn(sonnet, "what's 2+2?"),
			),
			action: Keep,
		},
		{
			name: "upgrade beats downgrade",
			st: state(opus,
				turn(opus, "a"),
				turn(opus, "b"),
				turn(opus, "doesn't work"),
			),
			action: Keep, signal: "frustration",
		},
		{
			name: "thresholds come from config",
			st: state(sonnet,
				turn(sonnet, "fix it", edits("/p/app.go", "/p/app.go")),
			),
			cfg:    func(c *config.Models) { c.RepeatedEditThreshold = 2 },
			action: Upgrade, suggested: "opus", signal: "repeated-edits",
		},
		{
			name:   "custom tiers",
			st:     state("claude-fable-test", turn("claude-fable-test", "what's 2+2?"), turn("claude-fable-test", "and 3+3?")),
			cfg:    func(c *config.Models) { c.ModelTiers = []string{"haiku", "sonnet", "opus", "fable"} },
			action: Downgrade, suggested: "opus",
		},
		{
			name: "inactive turns are ignored",
			st: state(sonnet,
				turn(sonnet, "fix it", errs(3, 0)),
				&Turn{Command: "/model", Edits: map[string]int{}},
			),
			action: Upgrade, suggested: "opus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cfg
			c.ModelTiers = append([]string(nil), cfg.ModelTiers...)
			if tt.cfg != nil {
				tt.cfg(&c)
			}
			sg := Evaluate(tt.st, c, now, Extra{})
			if sg.Action != tt.action {
				t.Fatalf("action = %s, want %s (reason %q, signals %+v)", sg.Action, tt.action, sg.Reason, sg.Signals)
			}
			if tt.suggested != "" && sg.Suggested != tt.suggested {
				t.Fatalf("suggested = %s, want %s", sg.Suggested, tt.suggested)
			}
			if tt.signal != "" && !hasSignal(sg, tt.signal) {
				t.Fatalf("missing signal %s in %+v", tt.signal, sg.Signals)
			}
			if tt.reasonHas != "" && !strings.Contains(sg.Reason, tt.reasonHas) {
				t.Fatalf("reason %q lacks %q", sg.Reason, tt.reasonHas)
			}
			if sg.Reason == "" {
				t.Fatal("empty reason")
			}
		})
	}
}

func hasSignal(sg Suggestion, name string) bool {
	for _, s := range sg.Signals {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestContextAdvice(t *testing.T) {
	cfg := config.Default().Models // 200k window, 60% / 85%
	tests := []struct {
		name    string
		ctx     int64
		maxCtx  int64
		level   ContextLevel
		auto    bool
		percent float64
	}{
		{"empty", 0, 0, ContextOK, false, 0},
		{"comfortable", 50_000, 50_000, ContextOK, false, 25},
		{"compact", 130_000, 130_000, ContextCompact, false, 65},
		{"fresh", 180_000, 180_000, ContextFresh, false, 90},
		{"1M window inferred", 300_000, 300_000, ContextOK, true, 30},
		{"1M window inferred from compaction", 650_000, 960_000, ContextCompact, true, 65},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := SessionState{Context: tt.ctx, MaxContext: tt.maxCtx}
			a := contextAdvice(s, cfg, nil)
			if a.Level != tt.level || a.AutoWindow != tt.auto || int(a.Percent) != int(tt.percent) {
				t.Fatalf("advice = %+v", a)
			}
			if a.Level != ContextOK && a.Message == "" {
				t.Fatal("missing message")
			}
		})
	}
}

func TestTierOf(t *testing.T) {
	tiers := []string{"haiku", "sonnet", "opus"}
	tests := map[string]int{
		"claude-haiku-5-5": 0, "claude-sonnet-5-5": 1, "claude-opus-5-5": 2,
		"CLAUDE-OPUS": 2, "gpt-5": -1, "": -1,
	}
	for m, want := range tests {
		if got := TierOf(m, tiers); got != want {
			t.Errorf("TierOf(%q) = %d, want %d", m, got, want)
		}
	}
}

func TestEngineFromFixture(t *testing.T) {
	e := NewEngine()
	feed := func(name string, sub bool) {
		err := transcript.ReadAll(filepath.Join("testdata", name), func(r transcript.Record) {
			e.Observe(r, transcript.Source{SessionID: "sess-s", IsSubagent: sub})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	feed("struggle.jsonl", false)
	st, ok := e.State("sess-s")
	if !ok {
		t.Fatal("no state")
	}
	if len(st.Turns) != 3 {
		t.Fatalf("turns = %d", len(st.Turns))
	}
	last := st.Turns[len(st.Turns)-1]
	if last.Edits["/work/demo/parser.go"] != 2 || last.ToolErrors != 1 || last.TestFailures != 1 || last.Responses != 2 {
		t.Fatalf("last turn = %+v", last)
	}
	if st.Turns[1].Command != "/model" || st.Turns[1].Active() {
		t.Fatalf("command turn = %+v", st.Turns[1])
	}
	if st.Model != sonnet || st.Context != 20305 {
		t.Fatalf("state model=%s ctx=%d", st.Model, st.Context)
	}
	c := config.Default().Models
	c.RepeatedEditThreshold = 3
	sg := Evaluate(st, c, now, Extra{})
	if sg.Action != Upgrade || sg.Suggested != "opus" {
		t.Fatalf("suggestion = %+v", sg)
	}
}

func TestLooksFailing(t *testing.T) {
	tests := map[string]bool{
		"--- FAIL: TestX (0.00s)":                 true,
		"FAIL\tgithub.com/x/y\t0.1s":              true,
		"ok  \tgithub.com/x/y\t0.1s":              false,
		"3 tests failed":                          true,
		"Traceback (most recent call last):":      true,
		"AssertionError: expected 1":              true,
		"panic: runtime error":                    true,
		"npm ERR! code 1":                         true,
		"error[E0308]: mismatched types":          true,
		"BUILD FAILED in 3s":                      true,
		"failover configured":                     false,
		"All 12 tests passed":                     false,
		"the word failure in prose is not enough": false,
	}
	for in, want := range tests {
		if got := looksFailing(in); got != want {
			t.Errorf("looksFailing(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEvaluateWithLimitPressure(t *testing.T) {
	cfg := config.Default().Models
	now := time.Now()
	over := Extra{Pressure: PressureOver, PressureDetail: "At this pace you'll reach your 5-hour limit at 15:10, before it resets"}
	watch := Extra{Pressure: PressureWatch, PressureDetail: "Your 5-hour limit is 85% used"}

	// Over: on the top model with ordinary work, step down.
	sg := Evaluate(state(opus, turn(opus, "add a field to the struct", tools(8))), cfg, now, over)
	if sg.Action != Downgrade || sg.Suggested != "sonnet" || !strings.Contains(sg.Reason, "last longer") {
		t.Errorf("over: %+v", sg)
	}
	// Over: struggling on sonnet, but no upgrade (it would burn the limit).
	struggling := state(sonnet, turn(sonnet, "run the tests", errs(2, 2)), turn(sonnet, "fix those", errs(1, 1)))
	sg = Evaluate(struggling, cfg, now, over)
	if sg.Action == Upgrade || !strings.Contains(sg.Reason, "Narrow the task") {
		t.Errorf("over + struggling: %+v", sg)
	}
	// Without pressure the same session upgrades.
	if sg = Evaluate(struggling, cfg, now, Extra{}); sg.Action != Upgrade {
		t.Errorf("no pressure: %+v", sg)
	}
	// Watch: one simple turn on the top model is enough to step down.
	simple := state(opus, turn(opus, "please rename the variable foo to bar in the request handler file", tools(1)))
	if sg = Evaluate(simple, cfg, now, Extra{}); sg.Action != Keep {
		t.Errorf("simple, no pressure: %+v", sg)
	}
	sg = Evaluate(simple, cfg, now, watch)
	if sg.Action != Downgrade {
		t.Errorf("simple + watch: %+v", sg)
	}
	found := false
	for _, s := range sg.Signals {
		found = found || s.Name == "limits"
	}
	if !found {
		t.Errorf("watch should show a limits signal: %+v", sg.Signals)
	}

	// Reported context replaces the estimate.
	p := 42.0
	st := state(opus, turn(opus, "hi", tools(1)))
	st.Context = 84000
	sg = Evaluate(st, cfg, now, Extra{ContextPercent: &p})
	if !sg.Context.Reported || sg.Context.Percent != 42 || sg.Context.Window != 200000 {
		t.Errorf("reported context: %+v", sg.Context)
	}
}
