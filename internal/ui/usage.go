package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/transcript"
)

func (m Model) viewUsage() string {
	w := m.width
	out := m.viewLimits(w)
	out = append(out, "")

	// Session line.
	if m.hasSess {
		s := m.sess
		live := sMuted.Render("○ idle")
		if m.now.Sub(s.LastActivity) < 2*time.Minute {
			live = sGreen.Render("● active")
		}
		line := fmt.Sprintf("%s %s  %s  %s  %s  %s",
			sLabel.Render("SESSION"), sBold.Render(ShortID(s.ID)),
			sText.Render(ShortPath(s.Cwd)), sPurple.Render(ShortModel(s.Model)),
			sMuted.Render(Ago(s.LastActivity, m.now)), live)
		out = append(out, clip(line, w+80))
	} else {
		out = append(out, sMuted.Render("No Claude Code sessions found in "+ShortPath(m.o.Core.Watcher.Root)+" yet."))
	}
	if m.o.Core.CwdFilter != "" {
		out = append(out, sMuted.Render("focused on "+ShortPath(m.o.Core.CwdFilter)))
	}
	// Context.
	if c := m.sugg.Context; c.Tokens > 0 {
		gw := max(10, min(40, w-46))
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
			auto = ""
		case c.AutoWindow:
			auto = sMuted.Render(" (1M assumed)")
		default:
			auto = sMuted.Render(" (est.)")
		}
		out = append(out,
			fmt.Sprintf("%s %s %s  %s of %s%s", sMuted.Render("context"), bar(c.Percent/100, gw, st), st.Render(fmt.Sprintf("%3.0f%%", c.Percent)),
				sBold.Render(Tokens(c.Tokens)), Tokens(c.Window), auto))
		if c.Message != "" {
			out = append(out, st.Render(c.Message))
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
	cardW := 24
	if w >= len(cards)*(cardW+5) {
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
			{"msgs", func(r UsageRow) string { return fmt.Sprint(r.N) }, sMuted},
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

	// Burn rate.
	out = append(out, fmt.Sprintf("%s %s %s %s %s %s",
		sMuted.Render(fmt.Sprintf("burn (%dm)", m.o.Core.Cfg.Usage.BurnWindowMinutes)),
		sBold.Render(Rate(m.snap.FreshPerMin)+"/min"),
		sMuted.Render("· with cache reads"), sBold.Render(Rate(m.snap.BurnPerMin)),
		sMuted.Render("· output"), sBold.Render(Rate(m.snap.OutputPerMin))))

	// Models today + last 7 days, side by side when wide.
	models := m.viewModelsToday(min(w, 48))
	days := m.viewDays(min(w, 44))
	if w >= 96 {
		out = append(out, "", lipgloss.JoinHorizontal(lipgloss.Top, pad(models, 50), "  ", days))
	} else {
		out = append(out, "", models, "", days)
	}
	if h := m.viewHistory(w); h != nil {
		out = append(out, "")
		out = append(out, h...)
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
	left := fmt.Sprintf("%d msgs", r.N)
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

func (m Model) viewDays(w int) string {
	lines := []string{section("Last 7 days", w)}
	var peak int64
	for _, d := range m.snap.Days {
		peak = max(peak, d.Usage.Total())
	}
	for _, d := range m.snap.Days {
		frac := float64(d.Usage.Total()) / float64(max(1, peak))
		label := d.Day.Format("Mon 01-02")
		st := sBlue
		if d.Day.Format("2006-01-02") == m.now.Format("2006-01-02") {
			st = sRed
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", sMuted.Render(label), bar(frac, 16, st), lpad(Tokens(d.Usage.Total()), 7)))
	}
	return strings.Join(lines, "\n")
}
