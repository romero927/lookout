package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Mouse selection, like Claude Code's: drag to highlight text, and it's
// copied when you let go. A click picks a tab, a list row, or the reader. A drag stays inside the pane it starts in (the
// list or the reader in split panels). The wheel scrolls like ↑↓.

type cell struct{ x, y int }

type selection struct {
	on       bool // dragging
	from, to cell
	box      [4]int // pane bounds: x0, x1 (exclusive), y0, y1 (inclusive)
}

// pane is the area a drag starting at column x is kept within.
func (m Model) pane(x int) [4]int {
	y0 := lipgloss.Height(m.viewHeader())
	y1 := m.height - footerHeight - 1
	b := [4]int{0, m.width, y0, max(y0, y1)}
	split := !m.help && m.loaded &&
		(m.tab == tabActivity || m.tab == tabChanges || m.tab == tabSkills || m.tab == tabFiles || m.tab == tabTools)
	if split {
		if lw := m.listWidth(); x < lw {
			b[1] = lw
		} else {
			b[0] = lw + 2 // past the border and padding
		}
	}
	return b
}

func (s selection) clamp(x, y int) cell {
	return cell{min(max(x, s.box[0]), s.box[1]-1), min(max(y, s.box[2]), s.box[3])}
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		return m.wheel(msg.X, -1)
	case msg.Button == tea.MouseButtonWheelDown:
		return m.wheel(msg.X, 1)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if box := m.pane(msg.X); msg.Y < box[2] {
			m.sel, m.hdrPress = selection{}, true
			return m, nil
		}
		m.sel = selection{on: true, box: m.pane(msg.X)}
		m.sel.from = m.sel.clamp(msg.X, msg.Y)
		m.sel.to = m.sel.from
	case msg.Action == tea.MouseActionMotion && m.sel.on:
		m.sel.to = m.sel.clamp(msg.X, msg.Y)
	case msg.Action == tea.MouseActionMotion:
		// The pointer moved with no button down: highlight the tab or
		// session under it.
		m.hoverTab, m.hoverStrip = m.hoverAt(msg.X, msg.Y)
	case msg.Action == tea.MouseActionRelease && m.hdrPress:
		m.hdrPress = false
		return m.click(msg.X, msg.Y)
	case msg.Action == tea.MouseActionRelease && m.sel.on:
		m.sel.to = m.sel.clamp(msg.X, msg.Y)
		if m.sel.from == m.sel.to {
			m.sel = selection{}
			return m.click(msg.X, msg.Y)
		}
		text := m.sel.text(strings.Split(m.render(), "\n"))
		m.sel = selection{}
		if strings.TrimSpace(text) != "" {
			return m, copyText(text)
		}
	}
	return m, nil
}

// span orders the ends and returns the columns [a, b) picked on line y.
func (s selection) span(y, width int) (a, b int, ok bool) {
	p, q := s.from, s.to
	if q.y < p.y || (q.y == p.y && q.x < p.x) {
		p, q = q, p
	}
	if y < p.y || y > q.y {
		return 0, 0, false
	}
	a, b = s.box[0], min(width, s.box[1])
	if y == p.y {
		a = p.x
	}
	if y == q.y {
		b = q.x + 1
	}
	return a, b, a < b
}

// text is the selected text, without colours or trailing spaces.
func (s selection) text(lines []string) string {
	var out []string
	for y, line := range lines {
		a, b, ok := s.span(y, ansi.StringWidth(line))
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Cut(ansi.Strip(line), a, b), " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// highlight shows the selection in reverse video while dragging.
func (s selection) highlight(view string) string {
	if !s.on || s.from == s.to {
		return view // a click, not a drag: nothing to show yet
	}
	lines := strings.Split(view, "\n")
	for y, line := range lines {
		w := ansi.StringWidth(line)
		a, b, ok := s.span(y, w)
		if !ok || a >= w {
			continue
		}
		b = min(b, w)
		lines[y] = ansi.Truncate(line, a, "") + "\x1b[0;7m" + ansi.Cut(ansi.Strip(line), a, b) + "\x1b[0m" + ansi.TruncateLeft(line, b, "")
	}
	return strings.Join(lines, "\n")
}

// splitPane is a split panel's list and how to drive it.
type splitPane struct {
	l      list
	idx    *int
	focus  *bool
	reload func(Model, string) (tea.Model, tea.Cmd)
	view   *viewport.Model
}

// split returns the current panel's list and reader, if it has them.
func (m *Model) split() (splitPane, bool) {
	if m.help || !m.loaded {
		return splitPane{}, false
	}
	lw := m.listWidth()
	switch m.tab {
	case tabActivity:
		return splitPane{m.activityList(lw), &m.aSel, &m.aFocus, Model.activityKey, &m.aView}, true
	case tabChanges:
		return splitPane{m.changesList(lw), &m.cSel, &m.cFocus, Model.changesKey, &m.cView}, true
	case tabSkills:
		return splitPane{m.skillsList(lw), &m.sSel, &m.sFocus, Model.skillsKey, &m.sView}, true
	case tabFiles:
		return splitPane{m.filesList(lw), &m.gSel, &m.gFocus, Model.filesKey, &m.gView}, true
	case tabTools:
		return splitPane{m.toolsList(lw), &m.tlSel, &m.tlFocus, Model.toolsKey, &m.tlView}, true
	}
	return splitPane{}, false
}

// wheel scrolls what's under the pointer: the list moves the selection,
// the reader scrolls its text.
func (m Model) wheel(x, dir int) (tea.Model, tea.Cmd) {
	if m.help {
		return m, nil // any key closes help; scrolling shouldn't
	}
	key := tea.KeyMsg{Type: tea.KeyDown}
	if dir < 0 {
		key = tea.KeyMsg{Type: tea.KeyUp}
	}
	p, ok := m.split()
	if !ok {
		return m.handleKey(key)
	}
	if x < m.listWidth() {
		*p.focus = false
		return m.handleKey(key)
	}
	if dir < 0 {
		p.view.ScrollUp(3)
	} else {
		p.view.ScrollDown(3)
	}
	return m, nil
}

// click handles a click without a drag: a tab in the header; a row in a
// list, which opens it as enter does; or the reader beside the list.
func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	if m.help || !m.loaded {
		return m, nil
	}
	if y == 0 {
		if t := m.tabAt(x); t >= 0 && t != m.tab {
			m.tab = t
			m.refresh()
		}
		return m, nil
	}
	if y == m.stripRow() {
		// The session strip under the tabs.
		switch id := m.stripAt(x); id {
		case "":
		case stripAuto:
			if m.o.Core.Pinned != "" {
				m = m.follow()
			}
		case stripNew:
			return m, m.newClaude()
		default:
			if o, ok := m.overviewOf(id); ok {
				m = m.pin(o)
			}
		}
		return m, nil
	}
	top := lipgloss.Height(m.viewHeader())
	h := max(3, m.height-top-footerHeight)
	if y < top || y >= top+h {
		return m, nil
	}
	switch m.tab {
	case tabSessions:
		// A click on a session shows it, as enter does.
		if row := m.sessionRowAt(y-top, h); row >= 0 {
			m.svSel, m.svID = row, m.ovs[row].ID
			return m.sessionsKey("enter")
		}
		return m, nil
	case tabContext:
		return m.clickContext(y-top, h)
	case tabCheatsheet:
		// A click picks the command and copies it, as enter does.
		if row := m.cheatList(m.width).rowAt(y-top, h); row >= 0 {
			m.kSel = row
			return m.cheatsheetKey("enter")
		}
		return m, nil
	}
	p, ok := m.split()
	if !ok {
		return m, nil
	}
	if x >= m.listWidth() {
		if m.tab == tabTools && y-top >= 2 {
			// Like Activity: a title and a summary line above the text.
			if nm, cmd, ok := m.clickToolCall(m.tlView.YOffset + y - top - 2); ok {
				return nm, cmd
			}
		}
		if m.tab == tabActivity {
			// The reader shows a title and a summary line above the text.
			if line := m.aView.YOffset + y - top - 2; y-top >= 2 && line < len(m.aStepAt) && m.aStepAt[line] != "" {
				m.aFocus = true
				return m.toggleStep(m.aStepAt[line])
			}
		}
		return p.reload(m, "enter")
	}
	row := p.l.rowAt(y-top, h)
	if row < 0 {
		return m, nil
	}
	*p.focus = false
	*p.idx = row
	nm, c1 := p.reload(m, "")
	nm, c2 := p.reload(nm.(Model), "enter")
	return nm, tea.Batch(c1, c2)
}

// hoverAt is the tab, or the session in the strip, under the pointer.
func (m Model) hoverAt(x, y int) (tab int, session string) {
	switch {
	case m.help || !m.loaded:
	case y == 0:
		return m.tabAt(x), ""
	case y == m.stripRow():
		return -1, m.stripAt(x)
	}
	return -1, ""
}

// idleMotion reports whether msg is a mouse movement that changes nothing:
// no drag in progress, and the same tab or session (or none) under the
// pointer as before.
func (m Model) idleMotion(msg tea.MouseMsg) bool {
	if msg.Action != tea.MouseActionMotion || m.sel.on || msg.Button != tea.MouseButtonNone {
		return false
	}
	tab, session := m.hoverAt(msg.X, msg.Y)
	return tab == m.hoverTab && session == m.hoverStrip
}
