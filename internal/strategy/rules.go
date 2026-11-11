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
	Keep        Action = "keep"
	Upgrade     Action = "upgrade"
	Downgrade   Action = "downgrade"
	RaiseEffort Action = "effort-up"
	LowerEffort Action = "effort-down"
)

// IsEffort reports whether the action changes /effort rather than /model.
func (a Action) IsEffort() bool { return a == RaiseEffort || a == LowerEffort }

// Command is what to type in Claude Code to follow the suggestion, or ""
// when it says to keep things as they are.
func (sg Suggestion) Command() string {
	switch {
	case sg.Action == Keep || sg.Suggested == "":
		return ""
	case sg.Action.IsEffort():
		return "/effort " + sg.Suggested
	default:
		return "/model " + sg.Suggested
	}
}

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
	Reported   bool // Percent is what Claude Code reported, not an estimate
	Percent    float64
	Message    string
}

// Suggestion is the Strategy panel's output.
type Suggestion struct {
	SessionID string
	Current   string // full model name
	Effort    string // current /effort level, "" when unknown
	Suggested string // /model alias or /effort level, or the current model when keeping
	Action    Action
	Reason    string
	Signals   []Signal
	Context   ContextAdvice
	At        time.Time
}

// Pressure is how close the usage limits are, from the status line.
type Pressure int

const (
	PressureNone  Pressure = iota // fine, or unknown (no subscription limits)
	PressureWatch                 // 80% or more used or projected
	PressureOver                  // the limit will run out before it resets
)

// Extra is what the rules know besides the transcript.
type Extra struct {
	Pressure       Pressure
	PressureDetail string   // e.g. "At this pace you'll reach your 5-hour limit at 15:10"
	ContextPercent *float64 // reported by Claude Code; nil to estimate from tokens
}

// largeWindow is assumed when a session's context exceeds the configured window.
const largeWindow = 1_000_000

// EffortIndex returns the position of effort in levels, or -1.
func EffortIndex(effort string, levels []string) int {
	for i, l := range levels {
		if effort != "" && strings.EqualFold(l, effort) {
			return i
		}
	}
	return -1
}

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

// heavyFrom is the index of the lowest heavy tier: the top tier, or the top
// two once there are four or more (opus and fable by default). Simple work
// on a heavy tier is nudged down a tier, and a two-signal upgrade jumps to
// the lowest heavy tier rather than straight to the most expensive one.
func heavyFrom(tiers []string) int {
	top := len(tiers) - 1
	if top >= 3 {
		return top - 1
	}
	return top
}

// Evaluate applies the rules to a session. It is pure: same input, same output.
func Evaluate(s SessionState, c config.Models, now time.Time, x Extra) Suggestion {
	sg := Suggestion{SessionID: s.ID, Current: s.Model, Effort: s.Effort, Suggested: s.Model, Action: Keep, At: now}
	sg.Context = contextAdvice(s, c, x.ContextPercent)

	active := activeTurns(s.Turns)
	if s.Model == "" || len(active) == 0 {
		sg.Reason = "Waiting for activity in this session."
		return sg
	}
	recent := active[max(0, len(active)-c.RecentTurns):]
	up := upgradeSignals(s, recent, c)
	top := len(c.ModelTiers) - 1
	cur := TierOf(s.Model, c.ModelTiers)
	eff := EffortIndex(s.Effort, c.EffortLevels)
	topEff := len(c.EffortLevels) - 1
	defEff := EffortIndex(c.DefaultEffort, c.EffortLevels)
	// Near a limit, a lighter setting is suggested sooner: one simple turn
	// is enough.
	if x.Pressure >= PressureWatch {
		c.SimpleTurnStreak = 1
	}
	heavy := heavyFrom(c.ModelTiers)
	down := downgradeSignals(active, cur, cur >= heavy, eff > defEff && defEff >= 0, c)
	if x.Pressure == PressureOver && x.PressureDetail != "" {
		down = append([]Signal{{Downgrade, "limits", x.PressureDetail}}, down...)
	} else if x.Pressure == PressureWatch && x.PressureDetail != "" {
		sg.Signals = append(sg.Signals, Signal{Downgrade, "limits", x.PressureDetail})
	}
	sg.Signals = append(append(sg.Signals, up...), down...)

	raise := func(to int, why string) {
		sg.Action, sg.Suggested = RaiseEffort, c.EffortLevels[to]
		sg.Reason = up[0].Detail + " — " + why
	}
	switch {
	case cur < 0:
		sg.Reason = fmt.Sprintf("%s isn't in model_tiers; add it to the config to get suggestions.", s.Model)
	case len(up) > 0 && x.Pressure == PressureOver:
		sg.Reason = up[0].Detail + ", but " + lowerFirst(x.PressureDetail) + " — more effort or a stronger model would use it up faster. Narrow the task instead."
	case len(up) > 0 && cur < top && eff >= 0 && (eff < topEff-1 || (cur >= heavy && eff < topEff)):
		// Effort is the cheaper step: try it before a model switch. Before
		// the top tier (fable), use every effort level first.
		raise(eff+1, "more effort may get there without switching models.")
	case len(up) > 0 && cur < top:
		next := cur + 1
		if len(up) >= 2 && x.Pressure == PressureNone {
			next = max(next, heavy)
		}
		sg.Action, sg.Suggested = Upgrade, c.ModelTiers[next]
		sg.Reason = up[0].Detail + " — a stronger model may get there faster."
	case len(up) > 0 && eff >= 0 && eff < topEff:
		raise(eff+1, "already on the top tier; more effort may help.")
	case len(up) > 0:
		sg.Reason = up[0].Detail + " — already on the top tier; keep going or rethink the approach."
	case len(down) > 0 && eff > defEff && defEff >= 0:
		// Lower effort before a lighter model: it keeps the model you chose.
		sg.Action, sg.Suggested = LowerEffort, c.EffortLevels[defEff]
		sg.Reason = down[0].Detail + " — less effort should handle this and save limits."
		if down[0].Name == "limits" {
			sg.Reason = down[0].Detail + " — less effort makes the rest last longer."
		}
	case len(down) > 0 && cur > 0:
		sg.Action, sg.Suggested = Downgrade, c.ModelTiers[cur-1]
		sg.Reason = down[0].Detail + " — a lighter model should handle this and save limits."
		if down[0].Name == "limits" {
			sg.Reason = down[0].Detail + " — a lighter model makes the rest last longer."
		}
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

	// Frustration in the latest prompt. Only short prompts count: a long
	// request that quotes an error message isn't a complaint.
	if p := strings.ToLower(s.LastPrompt()); p != "" && len(p) <= maxFrustratedPrompt {
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

// downgradeSignals looks for simple work on a heavy tier (onHeavy, cur is
// the session's tier) or at an effort above the default (highEffort).
func downgradeSignals(active []*Turn, cur int, onHeavy, highEffort bool, c config.Models) []Signal {
	var sig []Signal
	if !onHeavy && !highEffort {
		return nil
	}
	sc := simpleCfg{c}
	defEff := EffortIndex(c.DefaultEffort, c.EffortLevels)
	heavy := func(t *Turn) bool {
		if onHeavy && TierOf(t.Model, c.ModelTiers) == cur {
			return true
		}
		return highEffort && EffortIndex(t.Effort, c.EffortLevels) > defEff
	}
	where := "at above-default effort"
	if onHeavy {
		where = "on " + c.ModelTiers[cur]
	}
	streak := 0
	for i := len(active) - 1; i >= 0; i-- {
		t := active[i]
		if !sc.simple(t) || !heavy(t) {
			break
		}
		streak++
	}
	if streak >= c.SimpleTurnStreak {
		sig = append(sig, Signal{Downgrade, "simple-streak", fmt.Sprintf("%d simple, successful turns in a row %s", streak, where)})
	}
	// Two short, simple turns in a row: one short message alone doesn't
	// mean the work got simpler.
	if n := len(active); n >= 2 {
		a, b := active[n-2], active[n-1]
		short := func(t *Turn) bool {
			p := strings.TrimSpace(t.Prompt)
			return p != "" && len(p) <= c.ShortPromptChars
		}
		if sc.simple(a) && sc.simple(b) && short(a) && short(b) {
			sig = append(sig, Signal{Downgrade, "short-prompt", fmt.Sprintf("Two short, simple requests in a row (latest: %q)", strings.TrimSpace(b.Prompt))})
		}
	}
	return sig
}

func contextAdvice(s SessionState, c config.Models, reported *float64) ContextAdvice {
	a := ContextAdvice{Tokens: s.Context, Window: int64(c.ContextWindowTokens)}
	if reported != nil && *reported > 0 {
		// Claude Code knows the real window; derive it from the two figures.
		a.Percent, a.Reported = *reported, true
		if a.Tokens > 0 {
			a.Window = snapWindow(int64(float64(a.Tokens) * 100 / a.Percent))
		}
	} else {
		if s.MaxContext > a.Window || s.Context > a.Window {
			a.Window, a.AutoWindow = largeWindow, true
		}
		if a.Window <= 0 || a.Tokens <= 0 {
			return a
		}
		a.Percent = 100 * float64(a.Tokens) / float64(a.Window)
	}
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

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// maxFrustratedPrompt is the longest prompt checked for frustration phrases.
const maxFrustratedPrompt = 300

// snapWindow rounds a window size worked out from a rounded percentage to
// the standard size it is close to (200k or 1M).
func snapWindow(w int64) int64 {
	for _, std := range []int64{200_000, largeWindow} {
		if d := w - std; d > -std/20 && d < std/20 {
			return std
		}
	}
	return w
}
