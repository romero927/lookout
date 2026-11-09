package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette: blue accent; green / yellow / red for status.
var (
	cRed      = lipgloss.AdaptiveColor{Light: "#C70000", Dark: "#FF2E2E"}
	cYellow   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FFD43B"}
	cGreen    = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FD47A"}
	cBlue     = lipgloss.AdaptiveColor{Light: "#0550AE", Dark: "#5CB3FF"}
	cPurple   = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#C297FF"}
	cText     = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E6EDF3"}
	cMuted    = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	cFaint    = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#30363D"}
	cOnAccent = lipgloss.Color("#FFFFFF")
	cAccent   = lipgloss.AdaptiveColor{Light: "#0550AE", Dark: "#1F6FEB"}

	sBrand    = lipgloss.NewStyle().Bold(true).Foreground(cBlue)
	sTab      = lipgloss.NewStyle().Padding(0, 1).Foreground(cMuted)
	sTabOn    = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(cOnAccent).Background(cAccent)
	sMuted    = lipgloss.NewStyle().Foreground(cMuted)
	sText     = lipgloss.NewStyle().Foreground(cText)
	sBold     = lipgloss.NewStyle().Bold(true).Foreground(cText)
	sLabel    = lipgloss.NewStyle().Bold(true).Foreground(cMuted)
	sGreen    = lipgloss.NewStyle().Foreground(cGreen)
	sYellow   = lipgloss.NewStyle().Foreground(cYellow)
	sRed      = lipgloss.NewStyle().Foreground(cRed)
	sBlue     = lipgloss.NewStyle().Foreground(cBlue)
	sPurple   = lipgloss.NewStyle().Foreground(cPurple)
	sSelected = lipgloss.NewStyle().Bold(true).Foreground(cText).Background(cFaint)
	sWarn     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(cYellow).Padding(0, 1)
	sCard     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cFaint).Padding(0, 1)
	sCardHot  = sCard.BorderForeground(cBlue)
	sSection  = lipgloss.NewStyle().Bold(true).Foreground(cBlue)
	sFaint    = lipgloss.NewStyle().Foreground(cFaint)
)

// section renders a panel heading with a rule.
func section(title string, width int) string {
	t := sSection.Render(strings.ToUpper(title))
	rule := width - lipgloss.Width(t) - 1
	if rule < 0 {
		rule = 0
	}
	return t + " " + sMuted.Render(strings.Repeat("─", rule))
}

// bar draws a horizontal gauge of the given width.
func bar(frac float64, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	full := int(frac*float64(width) + 0.5)
	return style.Render(strings.Repeat("█", full)) + sMuted.Render(strings.Repeat("░", width-full))
}

// pad right-pads s to width w (by display width).
func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// lpad left-pads s to width w.
func lpad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// clip shortens s to at most w display cells.
// clipLeft shortens s from the left ("…/ui/model.go"), keeping the end.
func clipLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
