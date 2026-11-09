package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

	// Recent turns (shapes only; prompts are not shown).
	out = append(out, "", section("Recent turns", w))
	turns := m.sstate.Turns
	shown := 0
	header := sMuted.Render(fmt.Sprintf("%-4s %-18s %6s %6s %6s %8s", "turn", "model", "tools", "errors", "files", "output"))
	out = append(out, header)
	for i := len(turns) - 1; i >= 0 && shown < 6; i-- {
		t := turns[i]
		if !t.Active() {
			continue
		}
		shown++
		errs := fmt.Sprintf("%6d", t.ToolErrors)
		if t.ToolErrors > 0 {
			errs = sRed.Render(errs)
		}
		out = append(out, fmt.Sprintf("%-4s %-18s %6d %s %6d %8s",
			turnLabel(shown-1), clip(ShortModel(t.Model), 18), t.ToolCalls, errs, len(t.Edits), Tokens(t.OutputTokens)))
	}
	if shown == 0 {
		out = append(out, sMuted.Render("no turns yet"))
	}

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
			out = append(out, sMuted.Render(fmt.Sprintf("followed %d of %d resolved upgrade/downgrade calls", m.followed, m.resolved)))
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
				call = "keep " + ShortModel(r.Current)
			}
			line := fmt.Sprintf("%s %s %-9s %s  next: %s",
				sMuted.Render(r.At.Local().Format("01-02 15:04")), mark, strings.ToUpper(r.Action), call, ShortModel(next))
			out = append(out, clip(line, w+40))
		}
		if len(m.logRows) == 0 {
			out = append(out, sMuted.Render("no suggestions logged yet"))
		}
	}
	return strings.Join(out, "\n")
}

func turnLabel(i int) string {
	if i == 0 {
		return "now"
	}
	return fmt.Sprintf("-%d", i)
}
