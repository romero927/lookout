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
// when they changed on disk, and re-checks the status line setting. Limits
// are per account: when the session's file doesn't have them yet (Claude
// hasn't replied to it), m.acct comes from the newest file that does.
func (m *Model) refreshLimits(force bool) {
	sid := ""
	if m.hasSess {
		sid = m.sess.ID
	}
	dir := paths.LimitsDir()
	path, mod, ok := limits.Pick(dir, sid)
	changed := force || path != m.limitsPath || !mod.Equal(m.limitsMod)
	if changed {
		m.limitsPath, m.limitsMod = path, mod
		m.limits = limits.State{}
		if ok {
			m.limits = limits.Load(path)
		}
	}
	acctPath, acctMod, acct := m.limitsPath, m.limitsMod, m.limits
	if !m.limits.Informative() {
		var found bool
		if acctPath, acctMod, acct, found = limits.Account(dir); !found {
			acctPath, acctMod, acct = m.limitsPath, m.limitsMod, m.limits
		}
	}
	if !changed && acctPath == m.acctPath && acctMod.Equal(m.acctMod) {
		return
	}
	m.acctPath, m.acctMod, m.acct = acctPath, acctMod, acct
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

// longDur is Dur, with days for a day or more ("3d 4h").
func longDur(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
	return Dur(d)
}

// paceText describes where the window is heading.
func paceText(p limits.Pace, now time.Time) string {
	if !p.Known {
		return "pace: not enough history yet"
	}
	kind := "average pace"
	if p.Recent {
		kind = "recent pace"
	}
	rate := fmt.Sprintf("%.0f%%/h", p.RatePerHour)
	if isWeekly(p) {
		rate = fmt.Sprintf("%.0f%%/day", p.RatePerHour*24)
	}
	if !p.HitsAt.IsZero() {
		at := p.HitsAt.Local().Format("15:04")
		if p.HitsAt.Local().Format("2006-01-02") != now.Local().Format("2006-01-02") {
			at = p.HitsAt.Local().Format("Mon 15:04")
		}
		return fmt.Sprintf("%s %s → hits the limit at %s, %s before reset",
			kind, rate, at, longDur(p.ResetsAt.Sub(p.HitsAt)))
	}
	return fmt.Sprintf("%s %s → ~%.0f%% by reset", kind, rate, min(p.Projected, 100))
}

// PaceSummary is a one-line plain-text summary (used by `lookout stats`).
func PaceSummary(p limits.Pace, now time.Time) string {
	if p.Expired {
		return fmt.Sprintf("reset at %s; new figures arrive with Claude's next reply", p.ResetsAt.Local().Format("Mon 15:04"))
	}
	return fmt.Sprintf("%.0f%% used, %.0f%% left · resets %s · %s",
		p.Used, max(0, 100-p.Used), resetText(p, now), paceText(p, now))
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

// limitBadges are the compact header badges, shown on every panel: the
// 5-hour and weekly limits ("5h 42%", "wk 12%") on a subscription, or
// today's estimated cost ("today ~$4.10") on a pay-as-you-go account. The
// first is the one to keep when space is short.
func (m Model) limitBadges() []string {
	if m.showCost() {
		return []string{sGreen.Bold(true).Render("today " + estMoney(m.snap.Today.Cost))}
	}
	five, week := m.acct.Paces(m.now)
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
	five, week := m.acct.Paces(m.now)
	if five == nil && week == nil {
		return m.viewLimitsMissing(w)
	}
	out := []string{limitsHeading(w)}
	gw := max(10, min(40, w-28))
	for _, x := range []struct {
		name string
		p    *limits.Pace
	}{{"5-hour", five}, {"Weekly", week}} {
		if x.p == nil {
			continue
		}
		p := *x.p
		if len(out) > 1 {
			out = append(out, "") // space between the two windows
		}
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
			indent+st.Render(clip(paceText(p, m.now), w-8)))
		share := m.shareFive
		if isWeekly(p) {
			share = m.shareWeek
		}
		if share >= 0 {
			out = append(out, indent+sMuted.Render("this session: ")+
				sText.Render(fmt.Sprintf("about %.0f of the %.0f%% used", share*p.Used, p.Used))+
				sMuted.Render(" (estimate)"))
		}
	}
	note := "limits updated " + Ago(m.acct.Updated(), m.now) + " · they count your Claude usage on every device"
	if m.now.Sub(m.acct.Updated()) > 15*time.Minute {
		note = "limits last updated " + Ago(m.acct.Updated(), m.now) + " · they refresh when Claude Code replies"
	}
	out = append(out, "", sMuted.Render(clip(note, w)))
	return out
}

// limitsHeading is the Limits section heading, with the claude.ai usage
// page at its right end ("o" opens it) when there is room.
func limitsHeading(w int) string {
	link := sBlue.Render(UsagePageURL) + sMuted.Render(" (o)")
	t := sSection.Render("LIMITS")
	rule := w - lipgloss.Width(t) - lipgloss.Width(link) - 2
	if rule < 3 {
		return section("Limits", w)
	}
	return t + " " + sMuted.Render(strings.Repeat("─", rule)) + " " + link
}

// viewLimitsMissing explains how to get real limits. Only without Lookout's
// status line does it fall back to the token-based block estimate: with it,
// real limits are on the way, and an estimate would only mislead.
func (m Model) viewLimitsMissing(w int) []string {
	out := []string{limitsHeading(w)}
	switch {
	case m.slOurs && m.acct.PayAsYouGo():
		if c := m.limits.CostUSD; c != nil && m.limitsExact() {
			out = append(out, sLabel.Render("COST    ")+sBold.Render(limits.Money(*c))+
				sMuted.Render(clip(" this session, as reported by Claude Code", max(0, w-18))))
		}
		out = append(out, sMuted.Render(clip("No 5-hour or weekly limits: this is an API key or pay-as-you-go account.", w)))
		return out
	case m.slOurs:
		wait := "Waiting for Claude Code: limits arrive after its next reply."
		if !m.hasSess {
			wait = "No Claude Code session yet: limits arrive after its first reply."
		}
		out = append(out, sYellow.Render(clip(wait, w)),
			sMuted.Render(clip("Only Claude.ai subscriptions report limits; API-key use doesn't.", w)))
		return out
	default:
		out = append(out, m.statusLineWarning(w)...)
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
	alerts := limits.Alerts(m.acct, m.now, cfg.Thresholds, cfg.Pace)
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
	m.limitFlashUntil = m.flashUntil
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

// Pinned is the pinned session, or "", so a restart can keep it.
func (m Model) Pinned() string { return m.o.Core.Pinned }

// Tab is the key of the panel on screen, so a restart can reopen it.
func (m Model) Tab() string { return tabKey(m.tab) }

// pinAtStart pins the session asked for with --session, once the
// transcripts are loaded and a short ID can be matched.
func (m *Model) pinAtStart() {
	id := m.o.Session
	if id == "" {
		return
	}
	if full, ok := m.o.Core.Resolve(id); ok {
		m.o.Core.Pinned = full
		return
	}
	m.o.Core.Pinned = ""
	m.setFlash("no single session matches "+id+" · following the latest · s to pick one", sYellow, 8*time.Second)
}

// SuggestExtra turns limits into input for the model suggestion. acct has
// the account's limits; st is the session's own file, and exact says whether
// it belongs to the session being evaluated (context use is per session;
// limits are per account).
func SuggestExtra(acct, st limits.State, exact bool, now time.Time) strategy.Extra {
	var x strategy.Extra
	switch lvl, detail := acct.Pressure(now); lvl {
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

// refreshShare estimates how much of each limit window the focused session
// used. Claude Code reports one percentage for the whole account, so this
// splits it by the session's share of the Claude Code usage recorded on this
// computer during the window, with tokens weighted by model price. Use
// elsewhere (claude.ai, other computers) isn't seen, so it is an estimate.
func (m *Model) refreshShare(now time.Time) {
	m.shareFive, m.shareWeek = -1, -1
	if !m.hasSess {
		return
	}
	five, week := m.acct.Paces(now)
	for _, x := range []struct {
		p   *limits.Pace
		out *float64
	}{{five, &m.shareFive}, {week, &m.shareWeek}} {
		if x.p == nil || x.p.Expired {
			continue
		}
		mine, all := m.o.Core.Agg.Share(m.sess.ID, x.p.Start, now)
		if all > 0 {
			*x.out = mine / all
		}
	}
}

// statusLineWarning says how to install Lookout's status line. Until it
// is installed, limits and context are estimates.
func (m Model) statusLineWarning(w int) []string {
	badge := sWarn.Render("⚠ STATUS LINE NOT INSTALLED")
	head := badge + sYellow.Render(clip(" limits and context are estimates", max(0, w-lipgloss.Width(badge))))
	cmd := sBold.Render("lookout statusline install --print")
	// The longest wording that fits, so the settings path isn't cut off.
	rests := []string{" and add what it prints to ~/.claude/settings.json", " → add to ~/.claude/settings.json", " → ~/.claude/settings.json"}
	if m.slConfigured {
		rests = []string{" and use it in place of your statusLine (it keeps yours)", " → replace your statusLine", " → replace statusLine"}
	}
	room := max(0, w-lipgloss.Width(cmd)-4)
	rest := rests[len(rests)-1]
	for _, r := range rests {
		if lipgloss.Width(r) <= room {
			rest = r
			break
		}
	}
	return []string{head, sText.Render("Run ") + cmd + sText.Render(clip(rest, room))}
}
