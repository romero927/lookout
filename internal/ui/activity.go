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
	// Most useful first: the end is cut when the list is narrow.
	if t.Tools > 0 {
		parts = append(parts, plural(t.Tools, "step"))
	}
	if len(t.Files) > 0 {
		parts = append(parts, plural(len(t.Files), "file"))
	}
	if t.SubTools > 0 {
		parts = append(parts, fmt.Sprintf("%d by subagents", t.SubTools))
	}
	if t.Queued > 0 {
		parts = append(parts, fmt.Sprintf("+%d from you", t.Queued))
	}
	if t.Output > 0 {
		parts = append(parts, Tokens(t.Output)+" out")
	}
	return strings.Join(parts, " · ")
}

// activityList is the turn list: two lines per turn.
func (m Model) activityList(lw int) list {
	var l list
	switch {
	case m.aTyping || m.aFilter != "":
		cursor := ""
		if m.aTyping {
			cursor = "▏"
		}
		l.add(sSection.Render("ACTIVITY")+" "+sYellow.Render(clip("/"+m.aFilter+cursor, max(4, lw-10))), -1)
	default:
		l.add(sSection.Render("ACTIVITY")+" "+sMuted.Render("newest first"), -1)
	}
	if len(m.act.Turns) == 0 {
		l.add(sMuted.Render("  nothing yet"), -1)
	} else if m.aFilter != "" && m.stepTurn(-1, 1) == -1 {
		l.add(sMuted.Render("  no matches"), -1)
	}
	for i, t := range m.act.Turns {
		if !m.turnMatches(i) {
			continue
		}
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
	move := func(j int) {
		if j >= 0 { // -1: nothing matches the search
			m.aSel = j
		}
	}
	switch key {
	case "up", "k":
		move(m.stepTurn(m.aSel, -1))
	case "down", "j":
		move(m.stepTurn(m.aSel, 1))
	case "home", "g":
		move(m.stepTurn(-1, 1))
	case "end", "G":
		move(m.stepTurn(len(m.act.Turns), -1))
	case "/":
		m.aTyping = true
	case "esc":
		if m.aFilter != "" {
			m.aFilter = ""
		}
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
	key := fmt.Sprintf("%s|%d|%d|%d|%d|%d", t.Start, len(t.Calls), t.Failed, t.End.UnixNano(), len(t.Reply), m.aRev)
	if key == m.aShown {
		return
	}
	moved := !strings.HasPrefix(m.aShown, fmt.Sprint(t.Start)+"|")
	m.aShown = key
	prev := ""
	if m.aSel+1 < len(m.act.Turns) {
		prev = m.act.Turns[m.aSel+1].Reply
	}
	text, at := m.renderTurn(t, prev, max(20, m.aView.Width))
	follow := !moved && m.aView.AtBottom()
	m.aView.SetContent(text)
	m.aStepAt = at
	switch {
	case moved:
		m.aView.GotoTop()
	case follow:
		// Reading the end of a turn that is still running: stay there as
		// new steps arrive.
		m.aView.GotoBottom()
	}
}

// inReplyLines is how much of Claude's previous message is shown above a
// prompt, so a short reply ("yes", "do all of them") has its context.
const inReplyLines = 3

// renderTurn draws a turn for the viewer. It also returns, for each line,
// the tool ID of the step on it ("" for other lines), so a click can open
// that step.
func (m Model) renderTurn(t activity.Turn, prev string, w int) (string, []string) {
	var b []string
	stepAt := map[int]string{}
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
	gap := func() {
		if len(b) > 0 && b[len(b)-1] != "" {
			b = append(b, "")
		}
	}

	// 1. Context, dimmed: the start of what Claude said before this prompt.
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
		b = append(b, sFaint.Render("Before this, Claude said:"))
		for _, l := range lines {
			b = append(b, sFaint.Render("│ "+plainText(l)))
		}
		if more {
			b = append(b, sFaint.Render("│ … (full message: the next entry down)"))
		}
	}

	// 2. What you asked.
	switch {
	case t.Prompt != "":
		gap()
		b = append(b, sectionIn(sYellow, "You", w))
		quote(sYellow, t.Prompt, "")
	case t.Command != "":
		gap()
		b = append(b, sectionIn(sYellow, "You ran "+t.Command, w))
	}

	// The final message is shown once, in its own section after the steps,
	// when nothing came after it.
	last := -1
	for i, c := range t.Calls {
		if c.Agent == "" && !c.User {
			last = i
		}
	}
	reply := -1
	if last >= 0 && t.Calls[last].Name == "" {
		reply = last
	}

	// 3. Everything Claude did, in order.
	steps := 0
	for _, c := range t.Calls {
		if c.Name != "" {
			steps++
		}
	}
	if len(t.Calls) > 0 && (steps > 0 || reply != 0) {
		gap()
		title := "What Claude did"
		if steps > 0 {
			title += " · " + plural(steps, "step")
		}
		b = append(b, sectionIn(sBlue, title, w))
	}
	agent := ""
	speaker := "" // who the last message header was for
	for i, c := range t.Calls {
		if i == reply {
			continue
		}
		if c.Agent != agent {
			gap()
			if c.Agent != "" {
				task := m.act.AgentTasks[c.Agent]
				if task == "" {
					task = "subagent " + shortAgent(c.Agent)
				}
				b = append(b, sPurple.Bold(true).Render(clip("↳ Subagent: "+task+at(c.At), w)))
			} else {
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
			gap()
			b = append(b, sYellow.Bold(true).Render("You, while Claude was working"+at(c.At)))
			quote(sYellow, c.Text, "")
			speaker = ""
		case c.Name == "":
			st, who := sBlue, "Claude"
			if c.Agent != "" {
				st, who = sPurple, "Subagent"
			}
			if speaker != who {
				gap()
				b = append(b, indent+st.Bold(true).Render(who+at(c.At)))
			}
			quote(st, c.Text, indent)
			speaker = who
		default:
			if speaker == "" || speaker == "You" {
				speaker = "steps"
			}
			mark := sGreen.Render("✓")
			if c.Fail {
				mark = sRed.Render("✗")
			}
			name := sBold.Render(pad(clip(c.Name, 10), 10))
			if c.Agent != "" {
				name = sPurple.Render(pad(clip(c.Name, 10), 10))
			}
			detail := shortPaths(c.Summary)
			extra := ""
			if c.Added > 0 || c.Removed > 0 {
				extra += sGreen.Render(fmt.Sprintf("  +%d", c.Added)) + sRed.Render(fmt.Sprintf(" -%d", c.Removed))
			}
			if c.Took >= time.Second {
				extra += sFaint.Render("  " + took(c.Took))
			}
			open := c.ID != "" && m.aOpen[c.ID]
			fold := " "
			if c.ID != "" {
				fold = sFaint.Render("▸")
				if open {
					fold = sBlue.Render("▾")
				}
			}
			head := fmt.Sprintf("%s%s %s %s ", indent, fold, mark, name)
			room := max(10, w-lipgloss.Width(head)-lipgloss.Width(extra))
			stepAt[len(b)] = c.ID
			b = append(b, head+sText.Render(clip(detail, room))+extra)
			if open {
				d, ok := m.aDetail[c.ID]
				for _, l := range stepLines(c, d, ok, max(10, w-len(indent)-5)) {
					stepAt[len(b)] = c.ID
					b = append(b, indent+"     "+l)
				}
				continue // the output above includes any error
			}
			if c.Fail && c.Error != "" {
				for _, l := range strings.Split(cleanText(c.Error), "\n") {
					b = append(b, indent+"     "+sRed.Render(clip(l, max(10, w-len(indent)-5))))
				}
			}
		}
	}
	if t.Dropped > 0 {
		b = append(b, sMuted.Render(fmt.Sprintf("… and %d more steps not kept", t.Dropped)))
	}

	// 4. Where it ended up.
	if reply >= 0 {
		gap()
		b = append(b, sectionIn(sBlue, "Claude's last message"+at(t.Calls[reply].At), w))
		quote(sBlue, t.Calls[reply].Text, "")
	}
	if len(t.Files) > 0 {
		gap()
		b = append(b, section("Files edited", w))
		for _, f := range t.Files {
			b = append(b, "  "+clip(m.relPath(f), w-2))
		}
	}

	ids := make([]string, len(b))
	for i, id := range stepAt {
		ids[i] = id
	}
	return strings.Join(b, "\n"), ids
}

// sectionIn is a section heading in the given colour.
func sectionIn(st lipgloss.Style, title string, width int) string {
	t := st.Bold(true).Render(strings.ToUpper(title))
	return t + " " + sFaint.Render(strings.Repeat("─", max(0, width-lipgloss.Width(t)-1)))
}

// took is a step's duration: "4s", "1m 20s".
func took(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
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

// turnMatches reports whether turn i matches the search: its prompt,
// command or Claude's reply contains the text, ignoring case.
func (m Model) turnMatches(i int) bool {
	if m.aFilter == "" {
		return true
	}
	t := m.act.Turns[i]
	q := strings.ToLower(m.aFilter)
	for _, s := range []string{t.Prompt, t.Command, t.Reply} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// stepTurn returns the next matching turn from i in direction dir (+1 is
// older), i itself when there is none, or -1 when nothing matches at all.
func (m Model) stepTurn(i, dir int) int {
	for j := i + dir; j >= 0 && j < len(m.act.Turns); j += dir {
		if m.turnMatches(j) {
			return j
		}
	}
	if i >= 0 && i < len(m.act.Turns) && m.turnMatches(i) {
		return i
	}
	return -1
}

// searchKey handles typing a search in the Activity list.
func (m Model) searchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.aTyping, m.aFilter = false, ""
	case tea.KeyEnter:
		m.aTyping = false
	case tea.KeyBackspace:
		if r := []rune(m.aFilter); len(r) > 0 {
			m.aFilter = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.aFilter += string(k.Runes)
		if k.Type == tea.KeySpace && len(k.Runes) == 0 {
			m.aFilter += " " // bubbletea gives the space as a rune; older events may not
		}
	default:
		return m, nil
	}
	if len(m.act.Turns) > 0 && !m.turnMatches(m.aSel) {
		m.aSel = max(0, m.stepTurn(-1, 1))
	}
	m.loadTurn()
	return m, nil
}
