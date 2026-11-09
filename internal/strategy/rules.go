package strategy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/romero927/lookout/internal/config"
)

// Action is what the suggestion asks for.
type Action string

const (
	Keep      Action = "keep"
	Upgrade   Action = "upgrade"
	Downgrade Action = "downgrade"
)

// Signal is one observation that fed the suggestion.
type Signal struct {
	Direction Action // Upgrade or Downgrade
	Name      string
	Detail    string
}

// ContextLevel grades how full the context is.
type ContextLevel int

const (
	ContextOK ContextLevel = iota
	ContextCompact
	ContextFresh
)

// ContextAdvice flags when /compact or a fresh session is worth it.
type ContextAdvice struct {
	Level      ContextLevel
	Tokens     int64
	Window     int64
	AutoWindow bool // the window was raised because usage exceeded the configured size
	Percent    float64
	Message    string
}

// Suggestion is the Strategy panel's output.
type Suggestion struct {
	SessionID string
	Current   string // full model name
	Suggested string // /model alias, or the current model when keeping
	Action    Action
	Reason    string
	Signals   []Signal
	Context   ContextAdvice
	At        time.Time
}

// largeWindow is assumed when a session's context exceeds the configured window.
const largeWindow = 1_000_000

// TierOf returns the index of the tier alias contained in model, or -1.
func TierOf(model string, tiers []string) int {
	m := strings.ToLower(model)
	best := -1
	for i, t := range tiers {
		if t != "" && strings.Contains(m, strings.ToLower(t)) {
			best = i
		}
	}
	return best
}

// Evaluate applies the rules to a session. It is pure: same input, same output.
func Evaluate(s SessionState, c config.Models, now time.Time) Suggestion {
	sg := Suggestion{SessionID: s.ID, Current: s.Model, Suggested: s.Model, Action: Keep, At: now}
	sg.Context = contextAdvice(s, c)

	active := activeTurns(s.Turns)
	if s.Model == "" || len(active) == 0 {
		sg.Reason = "Waiting for activity in this session."
		return sg
	}
	recent := active[max(0, len(active)-c.RecentTurns):]
	up := upgradeSignals(s, recent, c)
	top := len(c.ModelTiers) - 1
	cur := TierOf(s.Model, c.ModelTiers)
	down := downgradeSignals(s, active, cur == top, c)
	sg.Signals = append(append([]Signal{}, up...), down...)

	switch {
	case cur < 0:
		sg.Reason = fmt.Sprintf("%s isn't in model_tiers; add it to the config to get suggestions.", s.Model)
	case len(up) > 0 && cur < top:
		next := cur + 1
		if len(up) >= 2 {
			next = top
		}
		sg.Action, sg.Suggested = Upgrade, c.ModelTiers[next]
		sg.Reason = up[0].Detail + " — a stronger model may get there faster."
	case len(up) > 0:
		sg.Reason = up[0].Detail + " — already on the top tier; keep going or rethink the approach."
	case len(down) > 0 && cur > 0:
		sg.Action, sg.Suggested = Downgrade, c.ModelTiers[cur-1]
		sg.Reason = down[0].Detail + " — a lighter model should handle this and save limits."
	default:
		sg.Reason = "No strong signals; the current model fits the work."
	}
	return sg
}

func activeTurns(turns []*Turn) []*Turn {
	var out []*Turn
	for _, t := range turns {
		if t.Active() {
			out = append(out, t)
		}
	}
	return out
}

func upgradeSignals(s SessionState, recent []*Turn, c config.Models) []Signal {
	var sig []Signal

	// Frustration in the latest prompt.
	if p := strings.ToLower(s.LastPrompt()); p != "" {
		for _, ph := range c.FrustrationPhrases {
			if ph != "" && strings.Contains(p, strings.ToLower(ph)) {
				sig = append(sig, Signal{Upgrade, "frustration", fmt.Sprintf("Your last prompt says %q", ph)})
				break
			}
		}
	}

	// Look at the last RecentToolCalls tool calls within the recent turns.
	var calls []ToolCall
	for _, t := range recent {
		calls = append(calls, t.Calls...)
	}
	if len(calls) > c.RecentToolCalls {
		calls = calls[len(calls)-c.RecentToolCalls:]
	}
	span := fmt.Sprintf("the last %d tool calls", len(calls))
	if len(calls) == 1 {
		span = "the last tool call"
	}
	edits := map[string]int{}
	errs, tests := 0, 0
	for _, call := range calls {
		if call.File != "" {
			edits[call.File]++
		}
		if call.Fail {
			errs++
		}
		if call.Test {
			tests++
		}
	}

	// Repeated edits to the same file.
	if f, n := maxEntry(edits); n >= c.RepeatedEditThreshold {
		sig = append(sig, Signal{Upgrade, "repeated-edits", fmt.Sprintf("%s edited %d× in %s", filepath.Base(f), n, span)})
	}

	// Failing tests / tool errors.
	if errs >= c.ToolErrorThreshold {
		what := "failing tool results"
		if tests > 0 {
			what = fmt.Sprintf("failing tool results (%d look like test/build failures)", tests)
		}
		sig = append(sig, Signal{Upgrade, "tool-errors", fmt.Sprintf("%d %s in %s", errs, what, span)})
	}

	// Large multi-file task.
	if len(recent) > 0 {
		last := recent[len(recent)-1]
		if n := len(last.Edits); n >= c.MultiFileThreshold {
			sig = append(sig, Signal{Upgrade, "multi-file", fmt.Sprintf("%d files edited in this turn", n)})
		}
	}
	if p := s.LastPrompt(); len(p) >= c.LongPromptChars {
		sig = append(sig, Signal{Upgrade, "long-prompt", fmt.Sprintf("Large task: %d-character prompt", len(p))})
	}
	return sig
}

func (c simpleCfg) simple(t *Turn) bool {
	return t.ToolErrors == 0 && t.ToolCalls <= c.SimpleMaxToolCalls &&
		t.OutputTokens <= int64(c.SimpleMaxOutputTokens) && len(t.Edits) <= 1
}

type simpleCfg struct{ config.Models }

func downgradeSignals(s SessionState, active []*Turn, onTop bool, c config.Models) []Signal {
	var sig []Signal
	sc := simpleCfg{c}
	streak := 0
	for i := len(active) - 1; i >= 0; i-- {
		t := active[i]
		if !sc.simple(t) || TierOf(t.Model, c.ModelTiers) != len(c.ModelTiers)-1 {
			break
		}
		streak++
	}
	if onTop && streak >= c.SimpleTurnStreak {
		sig = append(sig, Signal{Downgrade, "simple-streak", fmt.Sprintf("%d simple, successful turns in a row on the top model", streak)})
	}
	if len(active) > 0 && sc.simple(active[len(active)-1]) {
		p := strings.TrimSpace(s.LastPrompt())
		if p != "" && len(p) <= c.ShortPromptChars && onTop {
			sig = append(sig, Signal{Downgrade, "short-prompt", fmt.Sprintf("Short, simple request (%q)", p)})
		}
	}
	return sig
}

func contextAdvice(s SessionState, c config.Models) ContextAdvice {
	a := ContextAdvice{Tokens: s.Context, Window: int64(c.ContextWindowTokens)}
	if s.MaxContext > a.Window || s.Context > a.Window {
		a.Window, a.AutoWindow = largeWindow, true
	}
	if a.Window <= 0 || a.Tokens <= 0 {
		return a
	}
	a.Percent = 100 * float64(a.Tokens) / float64(a.Window)
	switch {
	case a.Percent >= float64(c.FreshSessionAtPercent):
		a.Level = ContextFresh
		a.Message = fmt.Sprintf("Context is %.0f%% full — start a fresh session (/clear) with a short summary.", a.Percent)
	case a.Percent >= float64(c.CompactAtPercent):
		a.Level = ContextCompact
		a.Message = fmt.Sprintf("Context is %.0f%% full — /compact is worth it.", a.Percent)
	}
	return a
}

func maxEntry(m map[string]int) (string, int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	best, n := "", 0
	for _, k := range keys {
		if m[k] > n {
			best, n = k, m[k]
		}
	}
	return best, n
}
