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
	out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("CURRENT "), sPurple.Render(ShortModel(sg.Current))))
	var call string
	switch sg.Action {
	case strategy.Upgrade:
		call = sRed.Bold(true).Render("▲ Upgrade to " + sg.Suggested)
	case strategy.Downgrade:
		call = sBlue.Bold(true).Render("▼ Downgrade to " + sg.Suggested)
	default:
		call = sGreen.Bold(true).Render("● Keep " + ShortModel(sg.Current))
	}
	out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("SUGGEST "), call))
	reason := lipgloss.NewStyle().Width(max(10, w-10)).Render(sg.Reason)
	out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, sLabel.Render("REASON  ")+"  ", sText.Render(reason)))
	if sg.Action != strategy.Keep {
		out = append(out, fmt.Sprintf("%s  %s", sLabel.Render("        "), sMuted.Render("switch in Claude Code with: ")+sBold.Render("/model "+sg.Suggested)))
	}
	if p := m.pending; p != nil {
		what := "keep " + ShortModel(p.Current)
		switch p.Action {
		case strategy.Upgrade:
			what = "upgrade to " + p.Suggested
		case strategy.Downgrade:
			what = "downgrade to " + p.Suggested
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
			next := r.NextModel
			if next == "" {
				next = "pending"
			}
			call := ShortModel(r.Current) + " → " + r.Suggested
			if r.Action == string(strategy.Keep) {
				call = ShortModel(r.Current)
			}
			line := fmt.Sprintf("%s %s %-9s %s  next: %s",
				sMuted.Render(r.At.Local().Format("01-02 15:04")), mark, strings.ToUpper(r.Action), call, ShortModel(next))
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
			out = append(out, sMuted.Render("✓ the model you used next matched · ✗ it didn't · … waiting for your next message"))
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
		case m.sugg.Action != strategy.Keep && m.sugg.Suggested == g.alias:
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
