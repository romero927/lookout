package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/configfiles"
)

func removeFile(p string) error { return os.Remove(p) }

// ---- Config ----

// filesList is the Config panel's list of config and memory files, by group.
func (m Model) filesList(lw int) list {
	var l list
	mode := sMuted.Render("read-only")
	if m.o.Core.Cfg.Files.AllowEdit {
		mode = sYellow.Render("editing enabled")
	}
	l.add(sSection.Render("CONFIG")+" "+mode, -1)
	var group configfiles.Group
	for i, f := range m.files {
		if f.Group != group {
			group = f.Group
			l.add(sLabel.Render(clip(string(group), lw)), -1)
		}
		label := clip(f.Label, lw-4)
		switch {
		case i == m.gSel:
			// One line below, so the next item stays in view (as before).
			l.sel = len(l.lines) + 1
			l.add(sSelected.Render(pad("▸ "+label, lw-1)), i)
		case !f.Exists:
			l.add(sMuted.Render("  "+clip(label, lw-14)+" (not created)"), i)
		default:
			l.add("  "+label, i)
		}
	}
	return l
}

func (m Model) viewFiles(h int) string {
	lw := m.listWidth()
	l := m.filesList(lw)
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(l.lines, l.sel, h), "\n"))

	var title, meta string
	if m.gSel < len(m.files) {
		f := m.files[m.gSel]
		title = sBold.Render(ShortPath(f.Path))
		if f.Exists {
			meta = sMuted.Render(fmt.Sprintf("%s · modified %s", sizeStr(f.Size), Ago(f.ModTime, m.now)))
		} else {
			meta = sMuted.Render("not present")
		}
	}
	border := cFaint
	if m.gFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		clip(title, m.gView.Width) + "\n" + meta + "\n" + m.gView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// list is a panel's left-hand list: its lines, which item each line
// belongs to (-1 for headings), and the line to keep in view.
type list struct {
	lines []string
	rows  []int
	sel   int
}

func (l *list) add(line string, row int) {
	l.lines = append(l.lines, line)
	l.rows = append(l.rows, row)
}

// rowAt is the item on screen line y of a list h lines tall, or -1.
func (l list) rowAt(y, h int) int {
	start := 0
	if len(l.lines) > h {
		start = max(0, l.sel-h+2)
	}
	if i := start + y; y >= 0 && i < len(l.rows) {
		return l.rows[i]
	}
	return -1
}

// scrollTo returns the window of lines that keeps line idx visible.
func scrollTo(lines []string, idx, h int) []string {
	if len(lines) <= h {
		return lines
	}
	start := max(0, idx-h+2)
	end := min(len(lines), start+h)
	return lines[start:end]
}

func sizeStr(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}
