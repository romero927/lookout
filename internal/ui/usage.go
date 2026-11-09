package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/transcript"
)

func (m Model) viewUsage() string {
	w := m.width
	out := m.viewLimits(w)
	out = append(out, "")

	// This session.
	out = append(out, section("This session", w))
	if m.hasSess {
		s := m.sess
		live := sMuted.Render("○ idle")
		if m.now.Sub(s.LastActivity) < 2*time.Minute {
			live = sGreen.Render("● active")
		}
		out = append(out, clip(fmt.Sprintf("%s%s  %s  %s  %s",
			sLabel.Render(pad("FOLDER", 9)), sText.Render(ShortPath(s.Cwd)), sPurple.Render(ShortModel(s.Model)),
			live, sMuted.Render(Ago(s.LastActivity, m.now)+" · "+ShortID(s.ID))), w+80))
	} else {
		out = append(out, sMuted.Render("No Claude Code sessions found in "+ShortPath(m.o.Core.Watcher.Root)+" yet."))
	}
	if m.o.Core.CwdFilter != "" {
		out = append(out, sMuted.Render("focused on "+ShortPath(m.o.Core.CwdFilter)))
	}
	if c := m.sugg.Context; c.Tokens > 0 {
		gw := max(10, min(40, w-28))
		st := sGreen
		switch {
		case c.Level >= 2:
			st = sRed
		case c.Level == 1:
			st = sYellow
		}
		auto := ""
		switch {
		case c.Reported:
		case c.AutoWindow:
			auto = " (1M assumed)"
		default:
			auto = " (est.)"
		}
		out = append(out,
			fmt.Sprintf("%s%s  %s %s", sLabel.Render(pad("CONTEXT", 9)), bar(c.Percent/100, gw, st),
				st.Bold(true).Render(fmt.Sprintf("%3.0f%% full", c.Percent)),
				sMuted.Render("· "+Tokens(c.Tokens)+" of "+Tokens(c.Window)+auto)))
		if c.Message != "" {
			out = append(out, strings.Repeat(" ", 9)+st.Render(clip(c.Message, w-9)))
		}
	}

	out = append(out, "", section("Tokens", w))

	// Cards.
	hours := m.o.Core.Cfg.Usage.BlockHours
	cards := []struct {
		title string
		row   UsageRow
		hot   bool
		note  string
	}{
		{"Session", UsageRow{m.snap.Session.Usage, m.snap.Session.Messages}, true, subNote(m.snap.SessionSub)},
		{"Today", UsageRow{m.snap.Today.Usage, m.snap.Today.Messages}, false, ""},
		{fmt.Sprintf("Last %dh", hours), UsageRow{m.snap.Rolling.Usage, m.snap.Rolling.Messages}, false, "rolling"},
	}
	cardW := min(34, (w-2)/len(cards)-4) // each card adds a border and padding
	if cardW >= 22 {
		var rendered []string
		for _, c := range cards {
			rendered = append(rendered, card(c.title, c.row, c.note, cardW, c.hot))
		}
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(rendered, " ")...))
	} else {
		// Narrow (e.g. a tmux side pane): one compact table.
		colW := 9
		head := pad("", 12)
		for _, c := range cards {
			head += lpad(strings.ToUpper(strings.TrimSuffix(c.title, " (est.)")), colW)
		}
		out = append(out, sSection.Render(head))
		rows := []struct {
			label string
			get   func(UsageRow) string
			st    lipgloss.Style
		}{
			{"input", func(r UsageRow) string { return Tokens(r.U.Input) }, sText},
			{"output", func(r UsageRow) string { return Tokens(r.U.Output) }, sText},
			{"cache write", func(r UsageRow) string { return Tokens(r.U.CacheCreate) }, sText},
			{"cache read", func(r UsageRow) string { return Tokens(r.U.CacheRead) }, sText},
			{"total", func(r UsageRow) string { return Tokens(r.U.Total()) }, sBold},
			{"responses", func(r UsageRow) string { return fmt.Sprint(r.N) }, sMuted},
		}
		for _, row := range rows {
			line := sMuted.Render(pad(row.label, 12))
			for _, c := range cards {
				line += row.st.Render(lpad(row.get(c.row), colW))
			}
			out = append(out, line)
		}
		var notes []string
		for _, c := range cards {
			if c.note != "" && c.note != "rolling" {
				notes = append(notes, strings.TrimSuffix(c.title, " (est.)")+": "+c.note)
			}
		}
		if len(notes) > 0 {
			out = append(out, sMuted.Render(clip(strings.Join(notes, " · "), w)))
		}
	}

	// Pace.
	out = append(out, clip(sMuted.Render(fmt.Sprintf("pace, last %d min: ", m.o.Core.Cfg.Usage.BurnWindowMinutes))+
		sBold.Render(Rate(m.snap.OutputPerMin))+sMuted.Render(" output/min · ")+
		sBold.Render(Rate(m.snap.FreshPerMin))+sMuted.Render(" new tokens/min (cache reads not counted)"), w+60))

	// Models today + last 7 days, side by side when wide.
	if w >= 110 {
		models := m.viewModelsToday(44)
		out = append(out, "", lipgloss.JoinHorizontal(lipgloss.Top, pad(models, 46), "  ", m.viewDays(w-48)))
	} else {
		out = append(out, "", m.viewModelsToday(min(w, 48)), "", m.viewDays(w))
	}
	return strings.Join(out, "\n")
}

func subNote(sub transcript.Usage) string {
	if sub.IsZero() {
		return ""
	}
	return "subagents " + Tokens(sub.Total())
}

func joinWithGap(items []string, gap string) []string {
	var out []string
	for i, it := range items {
		if i > 0 {
			out = append(out, gap)
		}
		out = append(out, it)
	}
	return out
}

func card(title string, r UsageRow, note string, width int, hot bool) string {
	inner := width
	row := func(label string, n int64, st lipgloss.Style) string {
		v := Tokens(n)
		return sMuted.Render(label) + strings.Repeat(" ", max(1, inner-len(label)-len(v))) + st.Render(v)
	}
	lines := []string{
		sSection.Render(strings.ToUpper(title)),
		row("input", r.U.Input, sText),
		row("output", r.U.Output, sText),
		row("cache write", r.U.CacheCreate, sText),
		row("cache read", r.U.CacheRead, sText),
		sMuted.Render(strings.Repeat("─", inner)),
		row("total", r.U.Total(), sBold),
	}
	left := fmt.Sprintf("%d responses", r.N)
	right := clip(note, max(0, inner-len(left)-1))
	foot := left + strings.Repeat(" ", max(1, inner-len(left)-lipgloss.Width(right))) + right
	lines = append(lines, sMuted.Render(foot))
	st := sCard
	if hot {
		st = sCardHot
	}
	return st.Width(inner + 2).Render(strings.Join(lines, "\n"))
}

func (m Model) viewModelsToday(w int) string {
	lines := []string{section("Models today", w)}
	if len(m.snap.Today.ByModel) == 0 {
		return strings.Join(append(lines, sMuted.Render("no usage today")), "\n")
	}
	type mt struct {
		name  string
		total int64
		out   int64
	}
	var ms []mt
	var grand int64
	for name, u := range m.snap.Today.ByModel {
		ms = append(ms, mt{name, u.Total(), u.Output})
		grand += u.Total()
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].total > ms[j].total })
	for _, x := range ms {
		frac := float64(x.total) / float64(max(1, grand))
		lines = append(lines, fmt.Sprintf("%s %s %s",
			pad(clip(ShortModel(x.name), 16), 16), bar(frac, 12, sPurple),
			lpad(Tokens(x.total), 7)+sMuted.Render(" · out "+Tokens(x.out))))
	}
	return strings.Join(lines, "\n")
}

// viewDays shows tokens per day for the last week and, once tracked, how
// much of the weekly limit each day used.
func (m Model) viewDays(w int) string {
	lines := []string{section("Last 7 days", w)}
	var peak int64
	for _, d := range m.snap.Days {
		peak = max(peak, d.Usage.Total())
	}
	limitUse := map[string]limits.DayUse{}
	tracked := ""
	if days := m.history.Last(7, m.now); days != nil {
		tracked = m.history.Days[0].Day
		for _, d := range days {
			limitUse[d.Day] = d
		}
	}
	for _, d := range m.snap.Days {
		frac := float64(d.Usage.Total()) / float64(max(1, peak))
		day := d.Day.Format("2006-01-02")
		st := sBlue
		if day == m.now.Format("2006-01-02") {
			st = sRed
		}
		line := fmt.Sprintf("%s %s %s", sMuted.Render(d.Day.Format("Mon 01-02")), bar(frac, 10, st), lpad(Tokens(d.Usage.Total()), 7))
		if u, ok := limitUse[day]; ok && tracked != "" && day >= tracked {
			note := sMuted.Render("  " + shortDayNote(u))
			if u.FiveHourPeak >= 100 {
				note = sYellow.Render("  " + shortDayNote(u))
			}
			line += note
		}
		lines = append(lines, clip(line, w+40))
	}
	if tracked != "" {
		lines = append(lines, sMuted.Render(clip("% of weekly = how much of your weekly limit that day used; tracked since "+tracked[5:], w)))
	}
	return strings.Join(lines, "\n")
}

// shortDayNote is DayNote sized for the Last 7 days chart.
func shortDayNote(d limits.DayUse) string {
	if d.Weekly == 0 && d.FiveHourPeak == 0 {
		return "no use"
	}
	s := fmt.Sprintf("+%.0f%% of weekly", d.Weekly)
	switch {
	case d.FiveHourPeak >= 100:
		s += " · 5h limit hit"
	case d.FiveHourPeak > 0:
		s += fmt.Sprintf(" · 5h %.0f%%", d.FiveHourPeak)
	}
	return s
}
