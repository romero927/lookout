package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
)

// turnTitle is the first line of a turn's prompt, or its slash command.
// isCompact reports whether the turn is a /compact command.
func isCompact(t activity.Turn) bool {
	return t.Command == "/compact" || strings.HasPrefix(strings.TrimSpace(t.Prompt), "/compact")
}

func turnTitle(t activity.Turn) string {
	switch {
	case strings.HasPrefix(t.Prompt, "This session is being continued from a previous conversation"):
		return "(session resumed with a summary)"
	case t.Prompt != "":
		line, _, _ := strings.Cut(strings.TrimSpace(t.Prompt), "\n")
		return line
	case t.Command != "":
		return t.Command
	default:
		return "(before the first prompt in this transcript)"
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

func (m Model) turnStats(t activity.Turn, newest bool) string {
	var parts []string
	switch {
	case t.Start.IsZero():
	case newest && m.now.Sub(t.End) < 2*time.Minute:
		parts = append(parts, Dur(m.now.Sub(t.Start))+" so far")
	default:
		parts = append(parts, Dur(t.End.Sub(t.Start)))
	}
	if t.Tools > 0 {
		parts = append(parts, plural(t.Tools, "tool"))
	}
	if t.SubTools > 0 {
		parts = append(parts, fmt.Sprintf("%d by subagents", t.SubTools))
	}
	if t.Queued > 0 {
		parts = append(parts, fmt.Sprintf("+%d from you", t.Queued))
	}
	if len(t.Files) > 0 {
		parts = append(parts, plural(len(t.Files), "file"))
	}
	if t.Output > 0 {
		parts = append(parts, Tokens(t.Output)+" out")
	}
	return strings.Join(parts, " · ")
}

// activityList is the turn list: two lines per turn.
func (m Model) activityList(lw int) list {
	var l list
	l.add(sSection.Render("ACTIVITY")+" "+sMuted.Render("newest first"), -1)
	if len(m.act.Turns) == 0 {
		l.add(sMuted.Render("  nothing yet"), -1)
	}
	for i, t := range m.act.Turns {
		if isCompact(t) {
			// A slim divider; back-to-back /compact entries show once.
			if i > 0 && isCompact(m.act.Turns[i-1]) && i != m.aSel {
				continue
			}
			label := m.turnTime(t) + " /compact: conversation summarized"
			if i == m.aSel {
				l.sel = len(l.lines)
				l.add(sSelected.Render(pad("▸ "+clip(label, lw-3), lw-1)), i)
			} else {
				l.add(sMuted.Render("  ┄ "+clip(label, lw-5)), i)
			}
			continue
		}
		head := m.turnTime(t) + " " + turnTitle(t)
		stats := m.turnStats(t, i == 0)
		var fail string
		if t.Failed > 0 {
			fail = sMuted.Render(" · ") + sRed.Render(fmt.Sprintf("%d failed", t.Failed))
		}
		if i == m.aSel {
			l.sel = len(l.lines) + 1
			l.add(sSelected.Render(pad("▸ "+clip(head, lw-3), lw-1)), i)
		} else {
			l.add("  "+sText.Render(clip(head, lw-3)), i)
		}
		l.add("  "+sMuted.Render(clip(stats, max(1, lw-3-lipgloss.Width(fail))))+fail, i)
	}
	return l
}

func (m Model) viewActivity(h int) string {
	lw := m.listWidth()
	l := m.activityList(lw)
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(l.lines, l.sel, h), "\n"))

	var title, meta string
	if m.aSel < len(m.act.Turns) {
		t := m.act.Turns[m.aSel]
		took := "took " + Dur(t.End.Sub(t.Start))
		switch {
		case t.Start.IsZero():
			took = "started before this transcript"
		case m.aSel == 0 && m.now.Sub(t.End) < 2*time.Minute:
			took = "in progress, " + Dur(m.now.Sub(t.Start)) + " so far"
		}
		// The prompt itself opens the viewer, so the header says when and
		// how it went instead of repeating it.
		head := strings.TrimSpace(m.turnTime(t)) + " · " + took
		if t.Model != "" {
			head += " · " + ShortModel(t.Model)
		}
		title = sBold.Render(clip(head, m.aView.Width))
		meta = sMuted.Render(clip(turnSummary(t), m.aView.Width))
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
	prev := ""
	if m.aSel+1 < len(m.act.Turns) {
		prev = m.act.Turns[m.aSel+1].Reply
	}
	m.aView.SetContent(m.renderTurn(t, prev, max(20, m.aView.Width)))
	if moved {
		m.aView.GotoTop()
	}
}

// inReplyLines is how much of Claude's previous message is shown above a
// prompt, so a short reply ("yes", "do all of them") has its context.
const inReplyLines = 3

func (m Model) renderTurn(t activity.Turn, prev string, w int) string {
	var b []string
	at := func(tm time.Time) string {
		if tm.IsZero() {
			return ""
		}
		return " · " + tm.Local().Format("15:04")
	}
	quote := func(st lipgloss.Style, text string, indent string) {
		for _, l := range markdown(text, max(10, w-len(indent)-2)) {
			b = append(b, indent+st.Render("│ ")+l)
		}
	}

	// What it answers: the start of Claude's previous message.
	if prev != "" && (t.Prompt != "" || t.Command != "") {
		var lines []string
		for _, l := range markdown(prev, max(10, w-2)) {
			if strings.TrimSpace(plainText(l)) != "" {
				lines = append(lines, l)
			}
		}
		more := len(lines) > inReplyLines
		if more {
			lines = lines[:inReplyLines]
		}
		b = append(b, sMuted.Render("In reply to Claude:"))
		for _, l := range lines {
			b = append(b, sFaint.Render("│ ")+sMuted.Render(plainText(l)))
		}
		if more {
			b = append(b, sFaint.Render("│ ")+sMuted.Render("… (full message: the next entry down)"))
		}
		b = append(b, "")
	}

	// What you asked (the time is in the header above).
	switch {
	case t.Prompt != "":
		b = append(b, sYellow.Bold(true).Render("You"))
		quote(sYellow, t.Prompt, "")
	case t.Command != "":
		b = append(b, sYellow.Bold(true).Render("You ran "+t.Command))
	}

	// Where it ended up.
	if t.Reply != "" {
		b = append(b, "", section("Claude's latest message", w))
		quote(sBlue, t.Reply, "")
	}
	if len(t.Files) > 0 {
		b = append(b, "", section("Files edited", w))
		for _, f := range t.Files {
			b = append(b, "  "+clip(m.relPath(f), w-2))
		}
	}

	// Everything in order.
	if len(t.Calls) > 0 {
		b = append(b, "", section("Step by step", w))
	}
	agent := ""
	speaker := "" // who the last message header was for
	divider := func() {
		if len(b) > 0 && !strings.HasSuffix(b[len(b)-1], "─") {
			b = append(b, sFaint.Render(strings.Repeat("┄", max(4, w))))
		}
	}
	for _, c := range t.Calls {
		if c.Agent != agent {
			if c.Agent != "" {
				task := m.act.AgentTasks[c.Agent]
				if task == "" {
					task = "subagent " + shortAgent(c.Agent)
				}
				divider()
				b = append(b, sPurple.Bold(true).Render(clip("↳ Subagent: "+task+at(c.At), w)))
			} else {
				divider()
				b = append(b, sMuted.Render("↩ back in the main conversation"))
			}
			agent, speaker = c.Agent, ""
		}
		indent := ""
		if c.Agent != "" {
			indent = "  "
		}
		switch {
		case c.User:
			divider()
			b = append(b, sYellow.Bold(true).Render("You, while Claude was working"+at(c.At)))
			quote(sYellow, c.Text, "")
			speaker = ""
		case c.Name == "":
			st, who := sBlue, "Claude"
			if c.Agent != "" {
				st, who = sPurple, "Subagent"
			}
			if speaker != who {
				if c.Agent == "" {
					divider()
				} else {
					b = append(b, "")
				}
				b = append(b, indent+st.Bold(true).Render(who+at(c.At)))
			}
			quote(st, c.Text, indent)
			speaker = who
		default:
			mark := sGreen.Render("✓")
			if c.Fail {
				mark = sRed.Render("✗")
			}
			name := sBold.Render(pad(clip(c.Name, 12), 12))
			if c.Agent != "" {
				name = sPurple.Render(pad(clip(c.Name, 12), 12))
			}
			detail := shortPaths(c.Summary)
			if c.Added > 0 || c.Removed > 0 {
				detail += sGreen.Render(fmt.Sprintf("  +%d", c.Added)) + sRed.Render(fmt.Sprintf(" -%d", c.Removed))
			}
			b = append(b, clip(fmt.Sprintf("%s  %s %s %s", indent, mark, name, sMuted.Render(detail)), w+60))
			if c.Fail && c.Error != "" {
				for _, l := range strings.Split(c.Error, "\n") {
					b = append(b, indent+"     "+sRed.Render(clip(l, max(10, w-len(indent)-5))))
				}
			}
		}
	}
	if t.Dropped > 0 {
		b = append(b, sMuted.Render(fmt.Sprintf("… and %d more steps not kept", t.Dropped)))
	}
	return strings.Join(b, "\n")
}

// plural is "1 tool" / "3 tools".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func shortAgent(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// turnSummary is one line about a turn: tool calls, failures, subagents,
// files, extra messages and output.
func turnSummary(t activity.Turn) string {
	var sum []string
	if t.Tools > 0 {
		f := plural(t.Tools, "tool call")
		if t.Failed > 0 {
			f += fmt.Sprintf(" (%d failed)", t.Failed)
		}
		sum = append(sum, f)
	}
	if t.SubTools > 0 {
		sum = append(sum, fmt.Sprintf("%d by subagents", t.SubTools))
	}
	if len(t.Files) > 0 {
		sum = append(sum, plural(len(t.Files), "file")+" edited")
	}
	if t.Queued > 0 {
		sum = append(sum, plural(t.Queued, "extra message")+" from you")
	}
	if t.Output > 0 {
		sum = append(sum, Tokens(t.Output)+" output tokens")
	}
	if len(sum) == 0 {
		return "no tool calls"
	}
	return strings.Join(sum, " · ")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainText drops colour codes, so quoted text can be shown dimmed.
func plainText(s string) string { return ansiRe.ReplaceAllString(s, "") }
