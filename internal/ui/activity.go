package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
)

// turnTitle is the first line of a turn's prompt, or its slash command.
func turnTitle(t activity.Turn) string {
	switch {
	case t.Prompt != "":
		line, _, _ := strings.Cut(strings.TrimSpace(t.Prompt), "\n")
		return line
	case t.Command != "":
		return t.Command
	default:
		return "(before the first prompt)"
	}
}

func (m Model) turnTime(t activity.Turn) string {
	if t.Start.IsZero() {
		return "     "
	}
	at := t.Start.Local()
	if at.Format("2006-01-02") != m.now.Local().Format("2006-01-02") {
		return at.Format("Mon 15:04")
	}
	return at.Format("15:04")
}

func turnStats(t activity.Turn) string {
	parts := []string{Dur(t.End.Sub(t.Start))}
	if n := len(t.Calls) + t.Dropped; n > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", n))
	}
	if len(t.Files) > 0 {
		parts = append(parts, fmt.Sprintf("%d files", len(t.Files)))
	}
	if t.Output > 0 {
		parts = append(parts, Tokens(t.Output)+" out")
	}
	return strings.Join(parts, " · ")
}

func (m Model) viewActivity(h int) string {
	lw := m.listWidth()
	list := []string{sSection.Render("ACTIVITY") + " " + sMuted.Render("newest first")}
	if len(m.act.Turns) == 0 {
		list = append(list, sMuted.Render("  nothing yet"))
	}
	selLine := 0
	for i, t := range m.act.Turns {
		head := m.turnTime(t) + " " + turnTitle(t)
		stats := turnStats(t)
		var fail string
		if t.Failed > 0 {
			fail = sMuted.Render(" · ") + sRed.Render(fmt.Sprintf("%d failed", t.Failed))
		}
		if i == m.aSel {
			selLine = len(list) + 1
			list = append(list, sSelected.Render(pad("▸ "+clip(head, lw-3), lw-1)))
		} else {
			list = append(list, "  "+sText.Render(clip(head, lw-3)))
		}
		list = append(list, "  "+sMuted.Render(clip(stats, max(1, lw-3-lipgloss.Width(fail))))+fail)
	}
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(list, selLine, h), "\n"))

	var title, meta string
	if m.aSel < len(m.act.Turns) {
		t := m.act.Turns[m.aSel]
		title = sBold.Render(clip(turnTitle(t), m.aView.Width))
		meta = sMuted.Render(clip(fmt.Sprintf("%s · took %s · %s", m.turnTime(t), Dur(t.End.Sub(t.Start)), ShortModel(t.Model)), m.aView.Width))
	} else {
		title = sMuted.Render("Each prompt you send becomes a turn here.")
	}
	border := cFaint
	if m.aFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		title + "\n" + meta + "\n" + m.aView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) activityKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.aSel > 0 {
			m.aSel--
		}
	case "down", "j":
		if m.aSel < len(m.act.Turns)-1 {
			m.aSel++
		}
	case "home", "g":
		m.aSel = 0
	case "end", "G":
		m.aSel = max(0, len(m.act.Turns)-1)
	case "enter", "right", "l":
		if len(m.act.Turns) > 0 {
			m.aFocus = true
		}
	}
	m.loadTurn()
	return m, nil
}

// loadTurn renders the selected turn into the viewer when it changed.
func (m *Model) loadTurn() {
	if m.aSel >= len(m.act.Turns) {
		m.aView.SetContent("")
		m.aShown = ""
		return
	}
	t := m.act.Turns[m.aSel]
	key := fmt.Sprintf("%s|%d|%d|%d|%d", t.Start, len(t.Calls), t.Failed, t.End.UnixNano(), len(t.Reply))
	if key == m.aShown {
		return
	}
	moved := !strings.HasPrefix(m.aShown, fmt.Sprint(t.Start)+"|")
	m.aShown = key
	m.aView.SetContent(m.renderTurn(t, max(20, m.aView.Width)))
	if moved {
		m.aView.GotoTop()
	}
}

func (m Model) renderTurn(t activity.Turn, w int) string {
	wrap := lipgloss.NewStyle().Width(w)
	var b []string
	if t.Prompt != "" {
		b = append(b, sSection.Render("PROMPT"), wrap.Render(sText.Render(t.Prompt)), "")
	} else if t.Command != "" {
		b = append(b, sSection.Render("COMMAND"), t.Command, "")
	}

	if len(t.Files) > 0 {
		b = append(b, sSection.Render(fmt.Sprintf("FILES EDITED (%d)", len(t.Files))))
		for _, f := range t.Files {
			b = append(b, "  "+clip(m.relPath(f), w-2))
		}
		b = append(b, "")
	}

	if n := len(t.Calls) + t.Dropped; n > 0 {
		counts := map[string]int{}
		for _, c := range t.Calls {
			counts[c.Name]++
		}
		names := make([]string, 0, len(counts))
		for k := range counts {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool {
			if counts[names[i]] != counts[names[j]] {
				return counts[names[i]] > counts[names[j]]
			}
			return names[i] < names[j]
		})
		var tally []string
		for _, k := range names {
			tally = append(tally, fmt.Sprintf("%s %d", k, counts[k]))
		}
		head := fmt.Sprintf("TOOLS (%d)", n)
		if t.Failed > 0 {
			head += fmt.Sprintf(" · %d FAILED", t.Failed)
		}
		b = append(b, sSection.Render(head), sMuted.Render(wrap.Render(strings.Join(tally, " · "))))
		for _, c := range t.Calls {
			mark := sGreen.Render("✓")
			if c.Fail {
				mark = sRed.Render("✗")
			}
			name := c.Name
			if c.Subagent {
				name = "↳" + name
			}
			b = append(b, clip(fmt.Sprintf("%s %s %s", mark, sBold.Render(pad(clip(name, 12), 12)), sMuted.Render(c.Summary)), w+40))
		}
		if t.Dropped > 0 {
			b = append(b, sMuted.Render(fmt.Sprintf("… and %d more", t.Dropped)))
		}
		b = append(b, "")
	}

	if t.Reply != "" {
		b = append(b, sSection.Render("CLAUDE'S LAST MESSAGE"), wrap.Render(sText.Render(t.Reply)))
	}
	return strings.Join(b, "\n")
}
