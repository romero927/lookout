package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
)

// toolStat is one tool's use across the session.
type toolStat struct {
	Name   string
	Calls  int
	Errors int           // calls that ran and failed
	Denied int           // calls blocked before they ran
	Sub    int           // calls made by subagents
	Total  time.Duration // time spent waiting on its results
	Last   time.Time
}

// toolRef is one call shown in the Tools reader.
type toolRef struct {
	id   string
	turn int // index in Session.Turns
}

// deniedMarks are the starts of results Claude Code gives a call that was
// blocked rather than run: a permission rule or prompt, auto mode, a safety
// check, a hook, or you rejecting it.
var deniedMarks = []string{
	"permission for this action was denied",
	"permission for this command was denied",
	"permission to use",
	"auto mode classifier gave no verdict",
	"auto mode cannot determine",
	"the user doesn't want to proceed",
	"the user doesn't want to take this action",
	"user rejected",
	"tool use was rejected",
	"blocked by",
}

// isDenied reports whether a failed call was blocked instead of run.
func isDenied(c activity.Call) bool {
	if !c.Fail {
		return false
	}
	e := strings.ToLower(c.Error)
	if len(e) > 160 {
		e = e[:160]
	}
	for _, d := range deniedMarks {
		if strings.Contains(e, d) {
			return true
		}
	}
	return false
}

// toolStats counts the session's tool calls by tool, most used first.
// It covers the steps Activity keeps (see droppedSteps).
func toolStats(s activity.Session) []toolStat {
	by := map[string]*toolStat{}
	for _, t := range s.Turns {
		for _, c := range t.Calls {
			if c.Name == "" || c.User {
				continue
			}
			st := by[c.Name]
			if st == nil {
				st = &toolStat{Name: c.Name}
				by[c.Name] = st
			}
			st.Calls++
			switch {
			case isDenied(c):
				st.Denied++
			case c.Fail:
				st.Errors++
			}
			if c.Agent != "" || c.Subagent {
				st.Sub++
			}
			st.Total += c.Took
			if c.At.After(st.Last) {
				st.Last = c.At
			}
		}
	}
	out := make([]toolStat, 0, len(by))
	for _, st := range by {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// droppedSteps is how many steps Activity didn't keep, so the counts
// here may be short by that many.
func droppedSteps(s activity.Session) int {
	n := 0
	for _, t := range s.Turns {
		n += t.Dropped
	}
	return n
}

// toolLabel shortens MCP tool names: "mcp__github__create_issue" becomes
// "github: create_issue".
func toolLabel(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if server, tool, ok := strings.Cut(rest, "__"); ok {
			return strings.TrimPrefix(server, "claude_ai_") + ": " + tool
		}
	}
	return name
}

// syncTool keeps the selection on the same tool as counts change its place.
func (m *Model) syncTool() {
	m.tools = toolStats(m.act)
	for i, st := range m.tools {
		if st.Name == m.tlName {
			m.tlSel = i
			return
		}
	}
	m.tlSel = min(m.tlSel, max(0, len(m.tools)-1))
	if m.tlSel < len(m.tools) {
		m.tlName = m.tools[m.tlSel].Name
	}
}

// toolsList is the list of tools used, most used first.
func (m Model) toolsList(lw int) list {
	var l list
	total := 0
	for _, st := range m.tools {
		total += st.Calls
	}
	head := sSection.Render("TOOLS") + " " + sMuted.Render(plural(total, "call"))
	if m.tlFailOnly {
		head += " " + sYellow.Render("· failed only")
	}
	l.add(head, -1)
	if len(m.tools) == 0 {
		l.add(sMuted.Render("  no tool calls yet"), -1)
		return l
	}
	// Columns: name, calls, errors, denied.
	nameW := max(4, lw-20)
	l.add(sLabel.Render(pad("  tool", nameW+2)+lpad("calls", 6)+lpad("err", 5)+lpad("deny", 6)), -1)
	num := func(n, w int) string {
		if n == 0 {
			return strings.Repeat(" ", w)
		}
		return lpad(fmt.Sprint(n), w)
	}
	for i, st := range m.tools {
		name := pad(clip(toolLabel(st.Name), nameW), nameW)
		count, errs, deny := lpad(fmt.Sprint(st.Calls), 6), num(st.Errors, 5), num(st.Denied, 6)
		if i == m.tlSel {
			l.sel = len(l.lines)
			l.add(sSelected.Render(pad("▸ "+name+count+errs+deny, lw-1)), i)
			continue
		}
		l.add("  "+name+sText.Render(count)+sRed.Render(errs)+sYellow.Render(deny), i)
	}
	if n := droppedSteps(m.act); n > 0 {
		l.add("", -1)
		l.add(sMuted.Render(clip(fmt.Sprintf("  %d older steps not kept", n), lw-1)), -1)
	}
	return l
}

func (m Model) viewTools(h int) string {
	lw := m.listWidth()
	l := m.toolsList(lw)
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(l.lines, l.sel, h), "\n"))

	title := sMuted.Render("Each tool Claude uses in this session appears here.")
	meta := ""
	if m.tlSel < len(m.tools) {
		st := m.tools[m.tlSel]
		title = sBold.Render(clip(toolLabel(st.Name), m.tlView.Width))
		if st.Name != toolLabel(st.Name) {
			title += sMuted.Render("  " + clip(st.Name, max(0, m.tlView.Width-lipgloss.Width(title)-2)))
		}
		meta = sMuted.Render(clip(toolMeta(st, m.now), m.tlView.Width))
	}
	border := cFaint
	if m.tlFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		title + "\n" + meta + "\n" + m.tlView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// toolMeta is one line about a tool: calls, failures, time, last use.
func toolMeta(st toolStat, now time.Time) string {
	parts := []string{plural(st.Calls, "call")}
	if st.Errors > 0 {
		parts = append(parts, plural(st.Errors, "error"))
	}
	if st.Denied > 0 {
		parts = append(parts, fmt.Sprintf("%d denied", st.Denied))
	}
	if st.Sub > 0 {
		parts = append(parts, fmt.Sprintf("%d by subagents", st.Sub))
	}
	if st.Total >= time.Second {
		parts = append(parts, took(st.Total)+" total")
	}
	if !st.Last.IsZero() {
		parts = append(parts, "last "+Ago(st.Last, now))
	}
	return strings.Join(parts, " · ")
}

// loadTool shows the selected tool's calls, newest prompt first, each
// call under the prompt it belonged to. Opened calls show their input and
// output; when the reader has focus, the cursor marks one call.
func (m *Model) loadTool() {
	if m.tlSel >= len(m.tools) {
		m.tlView.SetContent(sMuted.Render("Nothing yet."))
		m.tlShown, m.tlCalls, m.tlAt = "", nil, nil
		return
	}
	st := m.tools[m.tlSel]
	key := fmt.Sprintf("%s|%d|%d|%d|%d|%d|%d|%d|%t|%t", st.Name, st.Calls, st.Errors+st.Denied, st.Last.UnixNano(),
		m.tlView.Width, m.aRev, m.tlRev, m.tlCur, m.tlFocus, m.tlFailOnly)
	if key == m.tlShown {
		return
	}
	sameTool := strings.HasPrefix(m.tlShown, st.Name+"|")
	m.tlShown = key

	w := max(20, m.tlView.Width)
	var b []string
	var refs []toolRef
	var at []int // call index per line, -1 for none
	curLine := -1
	push := func(line string, call int) {
		b = append(b, line)
		at = append(at, call)
	}
	for ti, t := range m.act.Turns {
		started := false
		for _, c := range t.Calls {
			if c.Name != st.Name || c.User || (m.tlFailOnly && !c.Fail) {
				continue
			}
			if !started {
				started = true
				if len(b) > 0 {
					push("", -1)
				}
				head := sBlue.Bold(true).Render(clip(turnTitle(t), max(10, w-8)))
				push(head+" "+sFaint.Render(strings.Repeat("─", max(0, w-lipgloss.Width(head)-1))), -1)
			}
			idx := len(refs)
			refs = append(refs, toolRef{id: c.ID, turn: ti})

			denied := isDenied(c)
			mark := sGreen.Render("✓")
			switch {
			case denied:
				mark = sYellow.Render("⊘")
			case c.Fail:
				mark = sRed.Render("✗")
			}
			open := c.ID != "" && m.tlOpen[c.ID]
			fold := " "
			if c.ID != "" {
				fold = sFaint.Render("▸")
				if open {
					fold = sBlue.Render("▾")
				}
			}
			cursor := " "
			if m.tlFocus && idx == m.tlCur {
				cursor = sBlue.Bold(true).Render("›")
				curLine = len(b)
			}
			clock := sMuted.Render(c.At.Local().Format("15:04"))
			extra := ""
			if c.Added > 0 || c.Removed > 0 {
				extra += sGreen.Render(fmt.Sprintf("  +%d", c.Added)) + sRed.Render(fmt.Sprintf(" -%d", c.Removed))
			}
			if c.Took >= time.Second {
				extra += sFaint.Render("  " + took(c.Took))
			}
			if c.Agent != "" || c.Subagent {
				extra += sPurple.Render("  subagent")
			}
			if denied {
				extra += sYellow.Render("  denied")
			}
			head := cursor + fold + " " + clock + " " + mark + " "
			room := max(10, w-lipgloss.Width(head)-lipgloss.Width(extra))
			detail := shortPaths(c.Summary)
			if detail == "" {
				detail = "—"
			}
			text := sText.Render(clip(detail, room))
			if m.tlFocus && idx == m.tlCur {
				text = sBold.Render(clip(detail, room))
			}
			push(head+text+extra, idx)
			const ind = "          "
			if open {
				d, ok := m.aDetail[c.ID]
				for _, l := range stepLines(c, d, ok, max(10, w-len(ind))) {
					push(ind+l, idx)
				}
				continue
			}
			if c.Fail && c.Error != "" {
				first, _, _ := strings.Cut(c.Error, "\n")
				st := sRed
				if denied {
					st = sYellow
				}
				push(ind+st.Render(clip(first, max(10, w-len(ind)))), idx)
			}
		}
	}
	if len(b) == 0 {
		msg := "No calls kept for this tool."
		if m.tlFailOnly {
			msg = "No failed calls. Press f to show them all."
		}
		push(sMuted.Render(msg), -1)
	}
	m.tlCalls, m.tlAt = refs, at
	m.tlView.SetContent(strings.Join(b, "\n"))
	switch {
	case !sameTool:
		m.tlView.GotoTop()
	case curLine >= 0:
		// Keep the cursor's call in view.
		if curLine < m.tlView.YOffset {
			m.tlView.SetYOffset(max(0, curLine-1))
		} else if curLine >= m.tlView.YOffset+m.tlView.Height {
			m.tlView.SetYOffset(curLine - m.tlView.Height + 2)
		}
	}
}

// toolsKey handles keys on the list of tools.
func (m Model) toolsKey(key string) (tea.Model, tea.Cmd) {
	prev := m.tlSel
	switch key {
	case "up", "k":
		if m.tlSel > 0 {
			m.tlSel--
		}
	case "down", "j":
		if m.tlSel < len(m.tools)-1 {
			m.tlSel++
		}
	case "home", "g":
		m.tlSel = 0
	case "end", "G":
		m.tlSel = max(0, len(m.tools)-1)
	case "f":
		m.tlFailOnly = !m.tlFailOnly
		m.tlCur = 0
	case "enter", "right", "l":
		if len(m.tools) > 0 {
			m.tlFocus = true
			m.loadTool()
			m.tlCur = min(m.tlCur, max(0, len(m.tlCalls)-1))
		}
	}
	if m.tlSel != prev {
		m.tlCur = 0
	}
	if m.tlSel < len(m.tools) {
		m.tlName = m.tools[m.tlSel].Name
	}
	m.loadTool()
	return m, nil
}

// toolCallKey handles keys while reading a tool's calls: move between
// calls, open one, filter, or jump to it in Activity. ok is false for keys
// it leaves to the rest of the screen.
func (m Model) toolCallKey(key string) (_ tea.Model, _ tea.Cmd, ok bool) {
	n := len(m.tlCalls)
	switch key {
	case "esc", "left", "h":
		m.tlFocus = false
	case "up", "k":
		m.tlCur = max(0, m.tlCur-1)
	case "down", "j":
		m.tlCur = min(max(0, n-1), m.tlCur+1)
	case "home", "g":
		m.tlCur = 0
	case "end", "G":
		m.tlCur = max(0, n-1)
	case "enter", " ", "right", "l":
		if m.tlCur < n {
			nm, cmd := m.toggleToolCall(m.tlCalls[m.tlCur].id)
			return nm, cmd, true
		}
	case "f":
		m.tlFailOnly = !m.tlFailOnly
		m.tlCur = 0
	case "a":
		if m.tlCur < n {
			nm, cmd := m.showInActivity(m.tlCalls[m.tlCur])
			return nm, cmd, true
		}
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.tlView, cmd = m.tlView.Update(tea.KeyMsg{Type: map[string]tea.KeyType{
			"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "ctrl+u": tea.KeyCtrlU, "ctrl+d": tea.KeyCtrlD,
		}[key]})
		return m, cmd, true
	default:
		return m, nil, false
	}
	m.loadTool()
	return m, nil, true
}

// toggleToolCall opens or closes one call in the Tools reader, loading its
// input and output when needed (shared with Activity's cache).
func (m Model) toggleToolCall(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		return m, nil
	}
	if m.tlOpen == nil {
		m.tlOpen = map[string]bool{}
	}
	if m.tlOpen[id] {
		delete(m.tlOpen, id)
	} else {
		m.tlOpen[id] = true
	}
	m.tlRev++
	m.loadTool()
	if m.tlOpen[id] && !m.aDetail[id].Done {
		return m, m.loadStepsCmd([]string{id})
	}
	return m, nil
}

// showInActivity switches to Activity at the call's prompt, with the call
// opened and scrolled into view.
func (m Model) showInActivity(r toolRef) (tea.Model, tea.Cmd) {
	if r.turn >= len(m.act.Turns) {
		return m, nil
	}
	m.tab = tabActivity
	m.aFilter, m.aTyping = "", false
	m.aSel, m.aFocus = r.turn, true
	var cmd tea.Cmd
	if r.id != "" {
		if m.aOpen == nil {
			m.aOpen = map[string]bool{}
		}
		m.aOpen[r.id] = true
		m.aRev++
		if !m.aDetail[r.id].Done {
			cmd = m.loadStepsCmd([]string{r.id})
		}
	}
	m.aShown = ""
	m.loadTurn()
	for i, id := range m.aStepAt {
		if id == r.id && r.id != "" {
			m.aView.SetYOffset(max(0, i-2))
			break
		}
	}
	return m, cmd
}

// clickToolCall handles a click on line y of the Tools reader's text: it
// moves the cursor to that call and opens or closes it.
func (m Model) clickToolCall(line int) (tea.Model, tea.Cmd, bool) {
	if line < 0 || line >= len(m.tlAt) || m.tlAt[line] < 0 {
		return m, nil, false
	}
	m.tlFocus = true
	m.tlCur = m.tlAt[line]
	nm, cmd := m.toggleToolCall(m.tlCalls[m.tlCur].id)
	return nm, cmd, true
}
