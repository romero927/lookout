package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/overwatch/internal/limits"
	"github.com/romero927/overwatch/internal/paths"
)

// refreshLimits loads the focused session's limits (or the latest session's)
// when they changed on disk, and re-checks the status line setting.
func (m *Model) refreshLimits(force bool) {
	sid := ""
	if m.hasSess {
		sid = m.sess.ID
	}
	path, mod, ok := limits.Pick(paths.LimitsDir(), sid)
	if !force && path == m.limitsPath && mod.Equal(m.limitsMod) {
		return
	}
	m.limitsPath, m.limitsMod = path, mod
	m.limits = limits.State{}
	if ok {
		m.limits = limits.Load(path)
	}
	m.slConfigured, m.slOurs = limits.StatusLineSetting(filepath.Join(paths.ClaudeDir(), "settings.json"))
}

// isWeekly tells the two windows apart by length.
func isWeekly(p limits.Pace) bool { return p.ResetsAt.Sub(p.Start) > 24*time.Hour }

// resetText is "14:00 (in 2h23m)" or "Fri 09:00 (in 3d 21h)".
func resetText(p limits.Pace, now time.Time) string {
	at := p.ResetsAt.Local()
	left := p.ResetsAt.Sub(now)
	if left >= 24*time.Hour {
		return fmt.Sprintf("%s (in %dd %dh)", at.Format("Mon 15:04"), int(left.Hours())/24, int(left.Hours())%24)
	}
	return fmt.Sprintf("%s (in %s)", at.Format("15:04"), Dur(left))
}

// paceText describes where the window is heading.
func paceText(p limits.Pace) string {
	if !p.Known {
		return "pace: not enough history yet"
	}
	kind := "avg pace"
	if p.Recent {
		kind = "recent pace"
	}
	rate := fmt.Sprintf("%.0f%%/h", p.RatePerHour)
	if isWeekly(p) {
		rate = fmt.Sprintf("%.0f%%/day", p.RatePerHour*24)
	}
	if !p.HitsAt.IsZero() {
		return fmt.Sprintf("%s %s → limit at %s, %s before reset",
			kind, rate, p.HitsAt.Local().Format("15:04"), Dur(p.ResetsAt.Sub(p.HitsAt)))
	}
	return fmt.Sprintf("%s %s → ~%.0f%% by reset", kind, rate, min(p.Projected, 100))
}

// PaceSummary is a one-line plain-text summary (used by `overwatch stats`).
func PaceSummary(p limits.Pace, now time.Time) string {
	if p.Expired {
		return fmt.Sprintf("reset at %s; new figures arrive with Claude's next reply", p.ResetsAt.Local().Format("Mon 15:04"))
	}
	return fmt.Sprintf("%.0f%% used, %.0f%% left · resets %s · %s",
		p.Used, max(0, 100-p.Used), resetText(p, now), paceText(p))
}

func levelStyle(l limits.Level) lipgloss.Style {
	switch l {
	case limits.LevelOver:
		return sRed
	case limits.LevelWatch:
		return sYellow
	}
	return sGreen
}

// limitBadges are the compact header badges, e.g. "5h 42%".
func (m Model) limitBadges() []string {
	five, week := m.limits.Paces(m.now)
	var out []string
	for _, x := range []struct {
		name string
		p    *limits.Pace
	}{{"5h", five}, {"wk", week}} {
		if x.p == nil || x.p.Expired {
			continue
		}
		out = append(out, levelStyle(x.p.Level()).Bold(true).Render(fmt.Sprintf("%s %.0f%%", x.name, x.p.Used)))
	}
	return out
}

// viewLimits is the top of the Usage panel.
func (m Model) viewLimits(w int) []string {
	five, week := m.limits.Paces(m.now)
	if five == nil && week == nil {
		return m.viewLimitsMissing(w)
	}
	out := []string{section("Limits", w)}
	gw := max(10, min(40, w-28))
	for _, x := range []struct {
		name string
		p    *limits.Pace
	}{{"5-hour", five}, {"Weekly", week}} {
		if x.p == nil {
			continue
		}
		p := *x.p
		label := sLabel.Render(pad(strings.ToUpper(x.name), 8))
		indent := strings.Repeat(" ", 8)
		if p.Expired {
			out = append(out,
				label+bar(0, gw, sMuted)+"  "+sMuted.Render("reset"),
				indent+sMuted.Render(clip("reset at "+p.ResetsAt.Local().Format("Mon 15:04")+"; new figures arrive with Claude's next reply", w-8)))
			continue
		}
		st := levelStyle(p.Level())
		out = append(out,
			fmt.Sprintf("%s%s  %s %s", label, bar(p.Used/100, gw, st),
				st.Bold(true).Render(fmt.Sprintf("%3.0f%% used", p.Used)),
				sMuted.Render(fmt.Sprintf("· %.0f%% left", max(0, 100-p.Used)))),
			indent+sMuted.Render("resets ")+sText.Render(resetText(p, m.now)),
			indent+st.Render(clip(paceText(p), w-8)))
	}
	note := "updated " + Ago(m.limits.Updated(), m.now) + " · counts all your Claude usage, on any device"
	if m.now.Sub(m.limits.Updated()) > 15*time.Minute {
		note = "last updated " + Ago(m.limits.Updated(), m.now) + " · refreshes when Claude Code replies"
	}
	return append(out, sMuted.Render(clip(note, w)))
}

// viewLimitsMissing explains how to get real limits and falls back to the
// token-based block estimate.
func (m Model) viewLimitsMissing(w int) []string {
	out := []string{section("Limits", w)}
	switch {
	case m.slOurs:
		out = append(out, sYellow.Render(clip("Waiting for Claude Code: limits arrive after its next reply.", w)),
			sMuted.Render(clip("Only Claude.ai subscriptions report limits; API-key use doesn't.", w)))
	case m.slConfigured:
		out = append(out, sYellow.Render(clip("Real 5-hour and weekly limits: not connected.", w)),
			sMuted.Render(clip("Your settings.json already has a different statusLine. See: overwatch statusline install --print", w)))
	default:
		out = append(out, sYellow.Render(clip("Real 5-hour and weekly limits: not connected.", w)),
			sMuted.Render("Connect them: ")+sBold.Render("overwatch statusline install --print"))
	}
	if b := m.snap.Block; b != nil && b.Active {
		span := b.End.Sub(b.Start)
		frac := float64(m.now.Sub(b.Start)) / float64(span)
		gw := max(10, min(40, w-54))
		out = append(out, "", fmt.Sprintf("%s %s %s  ends in %s · projected %s",
			sMuted.Render("est. block"), bar(frac, gw, sBlue), sMuted.Render(fmt.Sprintf("%3.0f%%", frac*100)),
			sBold.Render(Dur(b.End.Sub(m.now))), sBold.Render(Tokens(m.snap.ProjectedBlock))))
		out = append(out, sMuted.Render(clip("Estimated from your tokens, not Anthropic's meter.", w)))
	}
	return out
}
