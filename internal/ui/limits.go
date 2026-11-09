package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/strategy"
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
	m.refreshHistory()
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

// PaceSummary is a one-line plain-text summary (used by `lookout stats`).
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
	case m.slOurs && m.limits.SeenNoLimits():
		if c := m.limits.CostUSD; c != nil && m.limitsExact() {
			out = append(out, sLabel.Render("COST    ")+sBold.Render(limits.Money(*c))+
				sMuted.Render(clip(" this session, as reported by Claude Code", max(0, w-18))))
		}
		out = append(out, sMuted.Render(clip("No 5-hour or weekly limits: this is an API key or pay-as-you-go account.", w)))
		return out
	case m.slOurs:
		out = append(out, sYellow.Render(clip("Waiting for Claude Code: limits arrive after its next reply.", w)),
			sMuted.Render(clip("Only Claude.ai subscriptions report limits; API-key use doesn't.", w)))
	case m.slConfigured:
		out = append(out, sYellow.Render(clip("Real 5-hour and weekly limits: not connected.", w)),
			sMuted.Render(clip("Your settings.json already has a different statusLine. See: lookout statusline install --print", w)))
	default:
		out = append(out, sYellow.Render(clip("Real 5-hour and weekly limits: not connected.", w)),
			sMuted.Render("Connect them: ")+sBold.Render("lookout statusline install --print"))
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

// refreshHistory reloads the per-day history when the file changed.
func (m *Model) refreshHistory() {
	info, err := os.Stat(paths.HistoryFile())
	if err != nil {
		m.history, m.histMod = limits.History{}, time.Time{}
		return
	}
	if info.ModTime().Equal(m.histMod) {
		return
	}
	m.histMod = info.ModTime()
	m.history = limits.LoadHistory(paths.HistoryFile())
}

// DayNote describes one day's limit use, e.g. "+6% of weekly · hit 5h limit".
func DayNote(d limits.DayUse) string {
	if d.Weekly == 0 && d.FiveHourPeak == 0 {
		return "no use"
	}
	s := fmt.Sprintf("+%.0f%% of weekly", d.Weekly)
	if d.FiveHourPeak >= 100 {
		s += " · hit 5h limit"
	} else if d.FiveHourPeak > 0 {
		s += fmt.Sprintf(" · 5h peak %.0f%%", d.FiveHourPeak)
	}
	return s
}

// checkAlerts fires limit alerts that haven't been shown yet.
func (m *Model) checkAlerts() tea.Cmd {
	cfg := m.o.Core.Cfg.Alerts
	alerts := limits.Alerts(m.limits, m.now, cfg.Thresholds, cfg.Pace)
	var fresh []limits.Alert
	for _, a := range alerts {
		if _, ok := m.fired[a.Key]; !ok {
			fresh = append(fresh, a)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	// Another dashboard may have shown them already.
	m.fired = limits.LoadFired(paths.AlertsFile())
	var show []limits.Alert
	for _, a := range fresh {
		if _, ok := m.fired[a.Key]; !ok {
			m.fired[a.Key] = m.now.Unix()
			show = append(show, a)
		}
	}
	if len(show) == 0 {
		return nil
	}
	_ = limits.SaveFired(paths.AlertsFile(), m.fired, m.now)
	top := show[len(show)-1]
	for _, a := range show {
		if a.Level > top.Level {
			top = a
		}
	}
	m.setFlash("⚠ "+top.Text, levelStyle(top.Level).Bold(true), 15*time.Second)
	return notify(top.Text, cfg.Bell, cfg.Desktop)
}

// notify rings the bell and sends a desktop notification, if enabled.
func notify(text string, bell, desktop bool) tea.Cmd {
	return func() tea.Msg {
		if bell {
			_, _ = os.Stdout.WriteString("\a")
		}
		if !desktop {
			return nil
		}
		if p, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(p, "--app-name=Lookout", "Lookout", text).Run()
		} else if p, err := exec.LookPath("osascript"); err == nil {
			_ = exec.Command(p, "-e", "on run argv", "-e",
				`display notification (item 1 of argv) with title "Lookout"`, "-e", "end run", text).Run()
		}
		return nil
	}
}

// checkBuild notices when the binary on disk was replaced (e.g. make build).
func (m *Model) checkBuild() {
	if m.o.Exe == "" || m.newBuild {
		return
	}
	info, err := os.Stat(m.o.Exe)
	if err != nil {
		return
	}
	if m.exeMod.IsZero() {
		m.exeMod, m.exeSize = info.ModTime(), info.Size()
		return
	}
	// Wait until the file has settled so a build in progress isn't run.
	if (!info.ModTime().Equal(m.exeMod) || info.Size() != m.exeSize) && m.now.Sub(info.ModTime()) > 2*time.Second {
		m.newBuild = true
		m.setFlash("A new Lookout build is ready: press R to restart", sGreen.Bold(true), 15*time.Second)
	}
}

// RestartRequested reports whether the user asked to restart into a new build.
func (m Model) RestartRequested() bool { return m.restart }

// SuggestExtra turns limits into input for the model suggestion. exact says
// whether st belongs to the session being evaluated (context use is
// per session; limits are per account).
func SuggestExtra(st limits.State, exact bool, now time.Time) strategy.Extra {
	var x strategy.Extra
	switch lvl, detail := st.Pressure(now); lvl {
	case limits.LevelOver:
		x.Pressure, x.PressureDetail = strategy.PressureOver, detail
	case limits.LevelWatch:
		x.Pressure, x.PressureDetail = strategy.PressureWatch, detail
	}
	if exact {
		if p, ok := st.ContextPercent(now, 30*time.Minute); ok {
			x.ContextPercent = &p
		}
	}
	return x
}

// limitsExact reports whether the loaded limits file is the focused session's.
func (m Model) limitsExact() bool {
	return m.hasSess && m.limitsPath == limits.FileFor(paths.LimitsDir(), m.sess.ID)
}
