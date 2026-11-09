package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/configfiles"
)

func removeFile(p string) error { return os.Remove(p) }

// ---- Files ----

func (m Model) viewFiles(h int) string {
	lw := m.listWidth()
	var list []string
	mode := sMuted.Render("read-only")
	if m.o.Core.Cfg.Files.AllowEdit {
		mode = sYellow.Render("editing enabled")
	}
	list = append(list, sSection.Render("FILES")+" "+mode)
	var group configfiles.Group
	for i, f := range m.files {
		if f.Group != group {
			group = f.Group
			list = append(list, sLabel.Render(string(group)))
		}
		label := clip(f.Label, lw-4)
		var line string
		switch {
		case i == m.gSel:
			line = sSelected.Render(pad("▸ "+label, lw-1))
		case !f.Exists:
			line = sMuted.Render("  " + clip(label, lw-14) + " (not created)")
		default:
			line = "  " + label
		}
		list = append(list, line)
	}
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(list, m.gSel+groupOffset(m.files, m.gSel)+1, h), "\n"))

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

// groupOffset counts the group header lines above item i.
func groupOffset(files []configfiles.File, i int) int {
	n := 1 // panel title
	var g configfiles.Group
	for j := 0; j <= i && j < len(files); j++ {
		if files[j].Group != g {
			g = files[j].Group
			n++
		}
	}
	return n
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
