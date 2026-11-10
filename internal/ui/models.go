package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/store"
	"github.com/romero927/lookout/internal/strategy"
)

func (m Model) viewModels() string {
	w := m.width
	sg := m.sugg
	var out []string

	out = append(out, section("Recommendation", w))
	current := sPurple.Render(ShortModel(sg.Current))
	if sg.Effort != "" {
		current += sMuted.Render(" · effort ") + sPurple.Render(sg.Effort)
	}
	out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("CURRENT "), current))
	var call string
	switch sg.Action {
	case strategy.Upgrade:
		call = sRed.Bold(true).Render("▲ Upgrade to " + sg.Suggested)
	case strategy.Downgrade:
		call = sBlue.Bold(true).Render("▼ Downgrade to " + sg.Suggested)
	case strategy.RaiseEffort:
		call = sRed.Bold(true).Render("▲ Raise effort to " + sg.Suggested)
	case strategy.LowerEffort:
		call = sBlue.Bold(true).Render("▼ Lower effort to " + sg.Suggested)
	default:
		call = sGreen.Bold(true).Render("● Keep " + ShortModel(sg.Current))
	}
	out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("SUGGEST "), call))
	reason := lipgloss.NewStyle().Width(max(10, w-10)).Render(sg.Reason)
	out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, sLabel.Render("REASON  ")+"  ", sText.Render(reason)))
	switch {
	case sg.Action != strategy.Keep && m.declined() >= 2:
		out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("        "), sMuted.Render(clip(
			fmt.Sprintf("You've kept %s after this suggestion %d times, so Lookout won't push it. %s if you change your mind.",
				stayedOn(sg), m.declined(), sg.Command()), max(10, w-10)))))
	case sg.Action != strategy.Keep:
		out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("        "), sMuted.Render("change in Claude Code with: ")+sBold.Render(sg.Command())))
	}
	if p := m.pending; p != nil {
		what := "keep " + ShortModel(p.Current)
		switch p.Action {
		case strategy.Upgrade:
			what = "upgrade to " + p.Suggested
		case strategy.Downgrade:
			what = "downgrade to " + p.Suggested
		case strategy.RaiseEffort:
			what = "raise effort to " + p.Suggested
		case strategy.LowerEffort:
			what = "lower effort to " + p.Suggested
		}
		out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("WATCHING"),
			sMuted.Render(clip(fmt.Sprintf("may change to %s if that holds for %d minutes or %d turns", what, int(strategy.SettleTime.Minutes()), strategy.SettleTurns), max(10, w-10)))))
	}

	// Signals.
	out = append(out, "", section("Signals", w))
	if len(sg.Signals) == 0 {
		out = append(out, sMuted.Render("none — no upgrade or downgrade signals in recent turns"))
	}
	for _, s := range sg.Signals {
		icon := sRed.Render("▲")
		if s.Direction == strategy.Downgrade {
			icon = sBlue.Render("▼")
		}
		out = append(out, fmt.Sprintf("%s %s %s", icon, pad(sMuted.Render(s.Name), 15), clip(s.Detail, max(10, w-20))))
	}
	if c := sg.Context; c.Message != "" {
		st := sYellow
		if c.Level == strategy.ContextFresh {
			st = sRed
		}
		out = append(out, st.Render("! "+c.Message))
	}

	out = append(out, "")
	out = append(out, m.modelGuide(w)...)
	out = append(out, "")
	out = append(out, m.effortGuide(w)...)

	// Log.
	out = append(out, "", section("Suggestion log", w))
	if m.o.Store == nil {
		msg := "log unavailable"
		if m.o.StoreErr != nil {
			msg += ": " + m.o.StoreErr.Error()
		}
		out = append(out, sRed.Render(msg))
	} else {
		if m.resolved > 0 {
			line := fmt.Sprintf("You followed %d of %d upgrade/downgrade suggestions.", m.followed, m.resolved)
			if m.followed > 0 {
				line += fmt.Sprintf(" %d of those went smoothly (fewer than %d failing tool calls in the next %d turns).",
					m.smooth, m.o.Core.Cfg.Models.ToolErrorThreshold, outcomeTurns)
			}
			out = append(out, sMuted.Render(clip(line, w+60)))
		}
		for _, r := range m.logRows {
			mark := sMuted.Render("…")
			if f := r.Followed(); f != nil {
				if *f {
					mark = sGreen.Render("✓")
				} else {
					mark = sRed.Render("✗")
				}
			}
			next := ShortModel(r.NextModel)
			call := ShortModel(r.Current) + " → " + r.Suggested
			switch {
			case r.Action == string(strategy.Keep):
				call = ShortModel(r.Current)
			case strategy.Action(r.Action).IsEffort():
				call = "effort " + orDash(r.CurrentEffort) + " → " + r.Suggested
				next = "effort " + orDash(r.NextEffort)
			}
			if r.NextModel == "" {
				next = "pending"
			}
			line := fmt.Sprintf("%s %s %-11s %s  next: %s",
				sMuted.Render(r.At.Local().Format("01-02 15:04")), mark, strings.ToUpper(r.Action), call, next)
			switch r.Outcome {
			case store.OutcomeSmooth:
				line += sGreen.Render("  → went smoothly")
			case store.OutcomeRough:
				line += sRed.Render("  → rough")
			}
			out = append(out, clip(line, w+40))
		}
		if len(m.logRows) == 0 {
			out = append(out, sMuted.Render("no suggestions logged yet"))
		} else {
			out = append(out, sMuted.Render("✓ the model or effort you used next matched · ✗ it didn't · … waiting for your next message"))
		}
	}
	return strings.Join(out, "\n")
}

// modelGuide lists the /model choices. Descriptions follow Claude Code's own
// model picker (2.1.x).
var modelGuide = []struct{ alias, use, note string }{
	{"fable", "For your toughest challenges", "may need usage credits, depending on your plan; has its own limit"},
	{"opus", "For complex work and everyday tasks", "Claude Code's default"},
	{"sonnet", "Most efficient for simpler tasks", "stretches your limits further than Opus"},
	{"haiku", "Fastest for quick answers", "lookups, small edits, short questions"},
	{"opusplan", "Opus while planning, Sonnet while doing the work", ""},
	{"opus[1m]", "Opus with a 1M-token context window", "for long sessions and large codebases"},
	{"sonnet[1m]", "Sonnet with a 1M-token context window", "for long sessions and large codebases"},
}

func (m Model) modelGuide(w int) []string {
	out := []string{section("Models you can switch to", w),
		sMuted.Render(clip("Switch with /model <name> in Claude Code. ● current · ▸ suggested", w))}
	cur := strings.ToLower(m.sugg.Current)
	for _, g := range modelGuide {
		mark := "  "
		base := strings.TrimSuffix(g.alias, "[1m]")
		switch {
		case !m.sugg.Action.IsEffort() && m.sugg.Action != strategy.Keep && m.sugg.Suggested == g.alias:
			mark = sYellow.Render("▸ ")
		case cur != "" && g.alias == base && base != "opusplan" && strings.Contains(cur, base):
			mark = sGreen.Render("● ")
		}
		line := mark + sBold.Render(pad(g.alias, 11)) + " " + sText.Render(g.use)
		if g.note != "" {
			line += sMuted.Render(" · " + g.note)
		}
		out = append(out, clip(line, w+40))
	}
	return out
}

// effortGuide lists the /effort levels. More effort means more thinking per
// reply: better on hard problems, slower, and it uses your limits faster.
var effortGuide = []struct{ level, use string }{
	{"low", "Quick, simple tasks; fastest and lightest on limits"},
	{"medium", "Everyday work; Claude Code's usual default"},
	{"high", "Harder bugs and multi-step changes"},
	{"xhigh", "Tough problems where the model keeps getting it wrong"},
	{"max", "Most thinking per reply; heaviest on limits"},
}

func (m Model) effortGuide(w int) []string {
	out := []string{section("Effort levels", w),
		sMuted.Render(clip("Change with /effort <level> in Claude Code. Effort is a cheaper step than switching models. ● current · ▸ suggested", w))}
	for _, g := range effortGuide {
		mark := "  "
		switch {
		case m.sugg.Action.IsEffort() && strings.EqualFold(m.sugg.Suggested, g.level):
			mark = sYellow.Render("▸ ")
		case strings.EqualFold(m.sugg.Effort, g.level):
			mark = sGreen.Render("● ")
		}
		out = append(out, clip(mark+sBold.Render(pad(g.level, 11))+" "+sText.Render(g.use), w+40))
	}
	if m.sugg.Effort == "" {
		out = append(out, sMuted.Render(clip("This session's effort isn't recorded (older Claude Code, or a model without effort levels).", w)))
	}
	return out
}

// stayedOn names what you kept after declining sg.
func stayedOn(sg strategy.Suggestion) string {
	if sg.Action.IsEffort() && sg.Effort != "" {
		return "effort " + sg.Effort
	}
	return ShortModel(sg.Current)
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// outcomeTurns is how many turns after a switch are judged.
const outcomeTurns = 3

// judgeOutcomes records how followed suggestions went: smooth when the
// first turns on the new model had fewer failing tool calls than the
// upgrade threshold.
func (m *Model) judgeOutcomes(db *store.Store) {
	open, err := db.AwaitingOutcome(m.sstate.ID)
	if err != nil {
		return
	}
	for _, o := range open {
		var errs, n int
		for _, t := range m.sstate.Turns {
			if n == outcomeTurns {
				break
			}
			if t.Active() && !t.Start.Before(o.NextAt.Add(-time.Minute)) {
				errs += t.ToolErrors
				n++
			}
		}
		if n < outcomeTurns {
			continue
		}
		outcome := store.OutcomeSmooth
		if errs >= m.o.Core.Cfg.Models.ToolErrorThreshold {
			outcome = store.OutcomeRough
		}
		_ = db.SetOutcome(o.ID, outcome)
	}
}

// declined counts how often, in the recent log for this session, you kept
// your model after the suggestion now shown.
func (m Model) declined() int {
	n := 0
	for _, r := range m.logRows {
		if r.SessionID != m.sugg.SessionID || r.Action != string(m.sugg.Action) || r.Suggested != m.sugg.Suggested {
			continue
		}
		if f := r.Followed(); f != nil && !*f {
			n++
		}
	}
	return n
}
