package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Mouse selection, like Claude Code's: drag to highlight text, and it's
// copied when you let go. The wheel scrolls like ↑↓.

type cell struct{ x, y int }

type selection struct {
	on       bool // dragging
	from, to cell
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		return m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	case msg.Button == tea.MouseButtonWheelDown:
		return m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		m.sel = selection{on: true, from: cell{msg.X, msg.Y}, to: cell{msg.X, msg.Y}}
	case msg.Action == tea.MouseActionMotion && m.sel.on:
		m.sel.to = cell{msg.X, msg.Y}
	case msg.Action == tea.MouseActionRelease && m.sel.on:
		m.sel.to = cell{msg.X, msg.Y}
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
	a, b = 0, width
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
	if !s.on {
		return view
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
