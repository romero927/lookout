package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
)

type (
	diffMsg struct{ path, diff, note string }
	workMsg struct {
		dir, top string
		items    []activity.WorkChange
		err      error
	}
)

// Groups in the Changes list, in display order.
const (
	grpClaude = iota // edited by Claude, still on disk
	grpOther         // uncommitted, not edited by Claude
	grpGone          // edited by Claude, no longer on disk
)

// changeItem is one row of the Changes list.
type changeItem struct {
	Path   string
	F      *activity.FileChange // nil when Claude didn't edit it
	Status string               // git porcelain code; "" when clean or unknown
	Group  int
}

func (m Model) changeItems() []changeItem {
	status := map[string]string{}
	for _, w := range m.work {
		status[w.Path] = w.Status
	}
	var items, gone []changeItem
	seen := map[string]bool{}
	for i := range m.act.Files {
		f := &m.act.Files[i]
		seen[f.Path] = true
		it := changeItem{Path: f.Path, F: f, Status: status[f.Path]}
		if m.gone[f.Path] {
			it.Group = grpGone
			gone = append(gone, it)
		} else {
			items = append(items, it)
		}
	}
	for _, w := range m.work {
		if !seen[w.Path] {
			items = append(items, changeItem{Path: w.Path, Status: w.Status, Group: grpOther})
		}
	}
	_ = gone // listed only as a count: usually old paths from moved folders
	return items
}

func (m Model) selectedChange() (changeItem, bool) {
	items := m.changeItems()
	if m.cSel < len(items) {
		return items[m.cSel], true
	}
	return changeItem{}, false
}

// relPath shows a path relative to the session folder when it is inside it.
func (m Model) relPath(p string) string {
	if m.hasSess && m.sess.Cwd != "" {
		if r, err := filepath.Rel(m.sess.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return ShortPath(p)
}

// refreshGone notes which edited files no longer exist.
func (m *Model) refreshGone() {
	m.gone = map[string]bool{}
	for _, f := range m.act.Files {
		if _, err := os.Stat(f.Path); err != nil {
			m.gone[f.Path] = true
		}
	}
}

func (m Model) viewChanges(h int) string {
	lw := m.listWidth()
	items := m.changeItems()
	list := []string{sSection.Render("CHANGES") + " " + sMuted.Render("this session")}
	if len(items) == 0 || items[0].Group != grpClaude {
		list = append(list, sMuted.Render("  no edits yet"))
	}
	selLine, group := 0, grpClaude
	for i, it := range items {
		if it.Group != group {
			group = it.Group
			list = append(list, "", sLabel.Render(clip("Also uncommitted in "+filepath.Base(m.workTop), lw)))
		}
		var stat string
		switch {
		case it.F != nil:
			stat = sGreen.Render(fmt.Sprintf("+%d", it.F.Added)) + " " + sRed.Render(fmt.Sprintf("-%d", it.F.Removed))
		default:
			stat = sYellow.Render(strings.TrimSpace(it.Status))
		}
		nameW := max(4, lw-lipgloss.Width(stat)-4)
		name := pad(clipLeft(m.relPath(it.Path), nameW), nameW)
		if i == m.cSel {
			selLine = len(list)
			list = append(list, sSelected.Render("▸ "+name)+" "+stat)
			continue
		}
		st := sText
		if it.Group == grpGone || (it.F != nil && m.workTop != "" && it.Status == "" && strings.HasPrefix(it.Path, m.workTop)) {
			st = sMuted // gone, committed, or changed back
		}
		list = append(list, "  "+st.Render(name)+" "+stat)
	}
	if n := len(m.gone); n > 0 {
		what := "edited files no longer exist"
		if n == 1 {
			what = "edited file no longer exists"
		}
		list = append(list, "", sMuted.Render(clip(fmt.Sprintf("%d %s", n, what), lw)))
	}
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(list, selLine, h), "\n"))

	var title, meta string
	if it, ok := m.selectedChange(); ok {
		title = sBold.Render(clipLeft(m.relPath(it.Path), m.cView.Width))
		var parts []string
		if f := it.F; f != nil {
			edits := fmt.Sprintf("edited %d×", f.Edits)
			if f.Written {
				edits = "written"
				if f.Edits > 1 {
					edits = fmt.Sprintf("written + edited %d×", f.Edits-1)
				}
			}
			parts = append(parts, edits, fmt.Sprintf("+%d -%d lines (est.)", f.Added, f.Removed), "last "+Ago(f.Last, m.now))
			if f.Failed > 0 {
				parts = append(parts, fmt.Sprintf("%d failed", f.Failed))
			}
			if f.Subagent {
				parts = append(parts, "incl. subagents")
			}
		} else {
			parts = append(parts, "not edited by Claude ("+describeStatus(it.Status)+")")
		}
		meta = sMuted.Render(clip(strings.Join(parts, " · "), m.cView.Width))
	} else {
		title = sMuted.Render("Files Claude edits in this session appear here.")
	}
	border := cFaint
	if m.cFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		title + "\n" + meta + "\n" + m.cView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func describeStatus(code string) string {
	switch {
	case code == "??":
		return "new, untracked"
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "A"):
		return "added"
	case strings.Contains(code, "R"):
		return "renamed"
	default:
		return "modified"
	}
}

func (m Model) changesKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.changeItems())
	switch key {
	case "up", "k":
		if m.cSel > 0 {
			m.cSel--
		}
	case "down", "j":
		if m.cSel < n-1 {
			m.cSel++
		}
	case "home", "g":
		m.cSel = 0
	case "end", "G":
		m.cSel = max(0, n-1)
	case "enter", "right", "l":
		if n > 0 {
			m.cFocus = true
		}
	}
	m.showDiff()
	return m, nil
}

// showDiff puts the loaded diff (or a placeholder) in the viewer.
func (m *Model) showDiff() {
	it, ok := m.selectedChange()
	switch {
	case !ok:
		m.cView.SetContent("")
	case it.Path != m.diffPath:
		m.cView.SetContent(sMuted.Render("loading diff…"))
	}
}

// changesCmd starts background git work the Changes panel needs.
func (m *Model) changesCmd() tea.Cmd {
	if m.tab != tabChanges || !m.loaded {
		return nil
	}
	var cmds []tea.Cmd
	dir := ""
	if m.hasSess {
		dir = m.sess.Cwd
	}
	if !m.workBusy && dir != "" && (dir != m.workDir || m.now.Sub(m.workAt) > 5*time.Second) {
		m.workBusy = true
		cmds = append(cmds, func() tea.Msg {
			items, top, err := activity.WorkingTree(dir)
			return workMsg{dir: dir, top: top, items: items, err: err}
		})
	}
	if it, ok := m.selectedChange(); ok && !m.diffBusy && (it.Path != m.diffPath || m.now.Sub(m.diffAt) > 3*time.Second) {
		m.diffBusy = true
		p := it.Path
		cmds = append(cmds, func() tea.Msg {
			d, note := activity.Diff(p)
			return diffMsg{path: p, diff: d, note: note}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyDiff(msg diffMsg) {
	m.diffBusy = false
	m.diffAt = time.Now()
	it, ok := m.selectedChange()
	if !ok || it.Path != msg.path {
		return
	}
	text := msg.note
	if msg.diff != "" {
		text = msg.diff
	}
	if msg.path == m.diffPath && text == m.diffText {
		return // unchanged: keep the scroll position
	}
	moved := msg.path != m.diffPath
	m.diffPath, m.diffText = msg.path, text
	if msg.diff == "" {
		m.cView.SetContent(sMuted.Render(msg.note))
	} else {
		m.cView.SetContent(colorDiff(msg.diff))
	}
	if moved {
		m.cView.GotoTop()
	}
}

func (m *Model) applyWork(msg workMsg) {
	m.workBusy = false
	m.workAt = time.Now()
	m.workDir = msg.dir
	if msg.err != nil {
		m.work, m.workTop = nil, ""
		return
	}
	m.work, m.workTop = msg.items, msg.top
}

func colorDiff(d string) string {
	lines := strings.Split(strings.TrimRight(d, "\n"), "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(l, "\t", "    ")
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			lines[i] = sMuted.Render(l)
		case strings.HasPrefix(l, "+"):
			lines[i] = sGreen.Render(l)
		case strings.HasPrefix(l, "-"):
			lines[i] = sRed.Render(l)
		case strings.HasPrefix(l, "@@"):
			lines[i] = sBlue.Render(l)
		default:
			lines[i] = sText.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}
