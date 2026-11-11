package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Usage and Models are one long page each rather than a list. When the
// window is too short for one, it scrolls, and a line at the bottom says
// how much is hidden.

// bodyHeight is how many lines the panel gets between header and footer.
func (m Model) bodyHeight() int {
	// The footer is always one line (and asks canScroll, which asks this).
	return max(3, m.height-lipgloss.Height(m.viewHeader())-1)
}

// page is the full text of a scrolling panel.
func (m Model) page() string {
	switch m.tab {
	case tabUsage:
		return m.viewUsage()
	case tabModels:
		return m.viewModels()
	}
	return ""
}

// scrolled shows the part of a page that fits in h lines, from the
// panel's scroll position, with a hint on the last line when more is
// hidden below.
func (m Model) scrolled(page string, h int) string {
	lines := pageLines(page)
	if len(lines) <= h {
		return page
	}
	off := min(max(0, m.scroll[m.tab]), maxScroll(len(lines), h))
	if off+h >= len(lines) {
		return strings.Join(lines[off:], "\n") // the end: no hint
	}
	end := off + h - 1 // the last line is the hint
	hint := fmt.Sprintf("↓ %d more %s · ↑↓ or the mouse wheel to scroll", len(lines)-end, map[bool]string{true: "line", false: "lines"}[len(lines)-end == 1])
	return strings.Join(append(lines[off:end:end], sMuted.Render(clip(hint, m.width))), "\n")
}

// maxScroll is the furthest a page of n lines scrolls in h lines: the
// last page shows every remaining line, with no hint.
func maxScroll(n, h int) int { return max(0, n-h) }

// scrollKey moves a scrolling panel.
func (m Model) scrollKey(key string) Model {
	h := m.bodyHeight()
	n := len(pageLines(m.page()))
	off := m.scroll[m.tab]
	switch key {
	case "up", "k":
		off--
	case "down", "j":
		off++
	case "pgup", "b":
		off -= h - 1
	case "pgdown", "f", " ":
		off += h - 1
	case "home", "g":
		off = 0
	case "end", "G":
		off = n
	default:
		return m
	}
	if m.scroll == nil {
		m.scroll = map[int]int{}
	}
	m.scroll[m.tab] = min(max(0, off), maxScroll(n, h))
	return m
}

// canScroll reports whether the panel on screen is a page too long for it.
func (m Model) canScroll() bool {
	if m.tab != tabUsage && m.tab != tabModels {
		return false
	}
	return len(pageLines(m.page())) > m.bodyHeight()
}

// pageLines splits a page into lines, leaving out blank lines at the end.
func pageLines(page string) []string {
	lines := strings.Split(page, "\n")
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
