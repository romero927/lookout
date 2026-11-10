package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/paths"
)

type (
	diffMsg struct{ path, diff, note string }
	workMsg struct {
		dir       string   // the folders checked, joined: see workDirs
		tops      []string // their repositories' top folders
		items     []activity.WorkChange
		committed []activity.WorkChange
		err       error
	}
)

// Groups in the Changes list, in display order.
const (
	grpClaude    = iota // edited by Claude, still on disk
	grpOther            // uncommitted, not edited by Claude's edit tools
	grpCommitted        // committed since the session started
	grpActions          // commands and tools that changed something else
	grpGone             // edited by Claude, no longer on disk
)

// changeItem is one row of the Changes list.
type changeItem struct {
	Path   string
	F      *activity.FileChange // nil when Claude didn't edit it
	Status string               // git porcelain code; "" when clean or unknown
	Group  int
	Repo   string           // the repository's top folder; "" when not in one
	A      *activity.Action // set for an action row; Path is then actionKey
}

// actionKey is the Path of an action row, so it can be told apart from files.
func actionKey(a activity.Action) string { return "\x00action:" + a.ID + a.At.String() }

func (m Model) changeItems() []changeItem {
	status := map[string]string{}
	for _, w := range m.work {
		status[w.Path] = w.Status
	}
	committed := map[string]bool{}
	for _, w := range m.committed {
		committed[w.Path] = true
	}
	var items, gone []changeItem
	seen := map[string]bool{}
	for i := range m.act.Files {
		f := &m.act.Files[i]
		seen[f.Path] = true
		it := changeItem{Path: f.Path, F: f, Status: status[f.Path]}
		switch {
		case m.gone[f.Path]:
			it.Group = grpGone
			gone = append(gone, it)
		case it.Status == "" && committed[f.Path]:
			// Claude's edits are committed now: list it there, with the
			// lines Claude changed.
			it.Group = grpCommitted
			items = append(items, it)
		default:
			items = append(items, it)
		}
	}
	for _, w := range m.work {
		if !seen[w.Path] {
			seen[w.Path] = true
			items = append(items, changeItem{Path: w.Path, Status: w.Status, Group: grpOther})
		}
	}
	for _, w := range m.committed {
		if !seen[w.Path] {
			seen[w.Path] = true
			items = append(items, changeItem{Path: w.Path, Status: w.Status, Group: grpCommitted})
		}
	}
	_ = gone // listed only as a count: usually old paths from moved folders

	// Within each group, files are grouped by repository: the session's own
	// first, then the others in the order they first appear.
	order := map[string]int{}
	if m.hasSess {
		order[m.repoOf(m.sess.Cwd)] = 0
	}
	for i := range items {
		items[i].Repo = m.repoOf(items[i].Path)
		if _, ok := order[items[i].Repo]; !ok {
			order[items[i].Repo] = len(order)
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Group != items[b].Group {
			return items[a].Group < items[b].Group
		}
		return order[items[a].Repo] < order[items[b].Repo]
	})
	for i := range m.act.Actions {
		a := &m.act.Actions[i]
		items = append(items, changeItem{Path: actionKey(*a), A: a, Group: grpActions})
	}
	return items
}

// repoOf is the checked repository containing p (the deepest, when they
// nest), or "".
func (m Model) repoOf(p string) string {
	best := ""
	for _, top := range m.workTops {
		if (p == top || strings.HasPrefix(p, top+string(filepath.Separator))) && len(top) > len(best) {
			best = top
		}
	}
	return best
}

// repoName names a repository heading in the Changes list.
func repoName(top string) string {
	if top == "" {
		return "not in a git repository"
	}
	return filepath.Base(top)
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
	// In another repository the session worked in: name it by its folder.
	for _, top := range m.workTops {
		if r, err := filepath.Rel(top, p); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.Join(filepath.Base(top), r)
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

// changesList is the list of changed files, by group.
func (m Model) changesList(lw int) list {
	var l list
	items := m.changeItems()
	l.add(sSection.Render("CHANGES")+" "+sMuted.Render("this session"), -1)
	l.add(sLabel.Render("Edited by Claude"), -1)
	if len(items) == 0 || items[0].Group != grpClaude {
		l.add(sMuted.Render("  none yet"), -1)
	}
	// Repository headings only help when there is more than one.
	repos := map[string]bool{}
	for _, it := range items {
		if it.A == nil {
			repos[it.Repo] = true
		}
	}
	byRepo := len(repos) > 1
	group, repo := grpClaude, "\x00"
	for i, it := range items {
		if it.Group != group {
			group = it.Group
			title := "Other uncommitted changes"
			switch group {
			case grpCommitted:
				title = "Committed since the session started"
			case grpActions:
				title = "Other actions · best guess"
			}
			l.add("", -1)
			l.add(sLabel.Render(clip(title, lw)), -1)
			repo = "\x00"
		}
		if it.A != nil {
			l.add(m.actionRow(it.A, i == m.cSel, lw), i)
			if i == m.cSel {
				l.sel = len(l.lines) - 1
			}
			continue
		}
		indent := ""
		if byRepo {
			if it.Repo != repo {
				repo = it.Repo
				l.add(sBlue.Render(clip("  "+repoName(repo), lw)), -1)
			}
			indent = "  "
		}
		var stat string
		switch {
		case it.F != nil:
			stat = sGreen.Render(fmt.Sprintf("+%d", it.F.Added)) + " " + sRed.Render(fmt.Sprintf("-%d", it.F.Removed))
		default:
			stat = sYellow.Render(strings.TrimSpace(it.Status))
		}
		nameW := max(4, lw-lipgloss.Width(stat)-4-len(indent))
		path := m.relPath(it.Path)
		if byRepo && it.Repo != "" {
			if r, err := filepath.Rel(it.Repo, it.Path); err == nil {
				path = r // the heading names the repository
			}
		}
		name := indent + pad(clipLeft(path, nameW), nameW)
		if i == m.cSel {
			l.sel = len(l.lines)
			l.add(sSelected.Render("▸ "+name)+" "+stat, i)
			continue
		}
		st := sText
		if it.Group == grpGone || (it.Group == grpClaude && it.Status == "" && m.inWorkTree(it.Path)) {
			st = sMuted // gone, or changed back to how it was
		}
		l.add("  "+st.Render(name)+" "+stat, i)
	}
	if n := len(m.gone); n > 0 {
		what := "edited files no longer exist"
		if n == 1 {
			what = "edited file no longer exists"
		}
		l.add("", -1)
		l.add(sMuted.Render(clip(fmt.Sprintf("%d %s", n, what), lw)), -1)
	}
	return l
}

// actionRow is an action's line in the Changes list: what it did, then
// whether it worked and when.
func (m Model) actionRow(a *activity.Action, selected bool, lw int) string {
	mark := sGreen.Render("✓")
	switch {
	case a.Fail:
		mark = sRed.Render("✗")
	case !a.Done:
		mark = sYellow.Render("…")
	}
	stat := mark + " " + sMuted.Render(Ago(a.At, m.now))
	nameW := max(4, lw-lipgloss.Width(stat)-4)
	name := a.Kind
	if a.Summary != "" && a.Summary != a.Kind {
		name += " · " + a.Summary
	}
	name = pad(clip(cleanText(name), nameW), nameW)
	if selected {
		return sSelected.Render("▸ "+name) + " " + stat
	}
	st := sText
	if a.Fail {
		st = sMuted
	}
	return "  " + st.Render(name) + " " + stat
}

// actionMeta is the line under an action's title.
func (m Model) actionMeta(a *activity.Action) string {
	parts := []string{a.Tool}
	switch {
	case a.Fail:
		parts = append(parts, "failed")
	case !a.Done:
		parts = append(parts, "running, or no result yet")
	default:
		parts = append(parts, "succeeded")
	}
	parts = append(parts, Ago(a.At, m.now))
	if a.Subagent {
		parts = append(parts, "by a subagent")
	}
	parts = append(parts, "a guess from its name or command")
	return strings.Join(parts, " · ")
}

// showAction puts an action's input and output in the viewer, loading
// them from the transcript first when needed.
func (m *Model) showAction(it changeItem) {
	d, loaded := m.aDetail[it.A.ID]
	c := activity.Call{ID: it.A.ID, Name: it.A.Tool}
	text := strings.Join(stepLines(c, d, loaded, max(10, m.cView.Width-1)), "\n")
	if it.Path == m.diffPath && text == m.diffText {
		return // unchanged: keep the scroll position
	}
	moved := it.Path != m.diffPath
	m.diffPath, m.diffText = it.Path, text
	m.cView.SetContent(text)
	if moved {
		m.cView.GotoTop()
	}
}

func (m Model) viewChanges(h int) string {
	lw := m.listWidth()
	l := m.changesList(lw)
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(l.lines, l.sel, h), "\n"))

	var title, meta string
	if it, ok := m.selectedChange(); ok && it.A != nil {
		title = sBold.Render(clip(cleanText(it.A.Kind+" · "+it.A.Summary), m.cView.Width))
		meta = sMuted.Render(clip(m.actionMeta(it.A), m.cView.Width))
	} else if ok {
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
			how := "uncommitted"
			if it.Group == grpCommitted {
				how = "committed"
			}
			parts = append(parts, describeStatus(it.Status)+", "+how+" · changed by a command, by you, or by another tool")
		}
		meta = sMuted.Render(clip(strings.Join(parts, " · "), m.cView.Width))
	} else {
		title = sMuted.Render("Files Claude edits, and things its commands change, appear here.")
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
	case "o":
		if it, ok := m.selectedChange(); ok && it.A == nil {
			return m, openFolder(it.Path)
		}
	case "y":
		if it, ok := m.selectedChange(); ok && it.A != nil {
			if c, _ := m.aDetail[it.A.ID].Input["command"].(string); c != "" {
				return m, copyText(c)
			}
			return m, copyText(it.A.Summary)
		} else if ok {
			if h := m.commitOf(it.Path); h != "" {
				return m, copyText(h)
			}
			m.setFlash("no commit of this file since the session started", sMuted, 3*time.Second)
		}
	}
	m.showDiff()
	return m, nil
}

// commitOf is the latest commit since the session start that touched path.
func (m Model) commitOf(path string) string {
	for _, c := range m.committed {
		if c.Path == path {
			return c.Commit
		}
	}
	return ""
}

// showDiff puts the loaded diff (or a placeholder) in the viewer.
func (m *Model) showDiff() {
	it, ok := m.selectedChange()
	switch {
	case !ok:
		m.cView.SetContent("")
	case it.A != nil:
		m.showAction(it)
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
	dirs := m.workDirs()
	dir := strings.Join(dirs, "\n")
	since := time.Time{}
	if m.hasSess {
		since = m.sess.FirstSeen
	}
	if !m.workBusy && dir != "" && (dir != m.workDir || m.now.Sub(m.workAt) > 5*time.Second) {
		m.workBusy = true
		cmds = append(cmds, func() tea.Msg { return checkWork(dir, dirs, since) })
	}
	if it, ok := m.selectedChange(); ok && it.A != nil {
		// An action: load its input and output once it has finished, and
		// check again every few seconds while it hasn't.
		d := m.aDetail[it.A.ID]
		if !m.diffBusy && !d.Done && (it.A.ID != m.actAsked || m.now.Sub(m.diffAt) > 3*time.Second) {
			if cmd := m.loadStepsCmd([]string{it.A.ID}); cmd != nil {
				m.diffBusy, m.diffAt, m.actAsked = true, m.now, it.A.ID
				cmds = append(cmds, cmd)
			}
		}
	} else if ok && !m.diffBusy && (it.Path != m.diffPath || m.now.Sub(m.diffAt) > 3*time.Second) {
		m.diffBusy = true
		p := it.Path
		var files []string
		if it.F != nil && m.hasSess {
			files = activity.SessionFiles(paths.ClaudeProjectsDir(), m.sess.Project, m.sess.ID)
		}
		cmds = append(cmds, func() tea.Msg {
			d, note := activity.Diff(p, since)
			if d == "" && note == activity.NoRepoNote && files != nil {
				// git can't help: show the edits Claude made instead.
				if edits := activity.LoadFileEdits(files, p); len(edits) > 0 {
					d = editsDiff(edits)
				}
			}
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
		m.work, m.committed, m.workTops = nil, nil, nil
		return
	}
	m.work, m.committed, m.workTops = msg.items, msg.committed, msg.tops
}

// maxWorkDirs caps how many folders Changes checks with git.
const maxWorkDirs = 8

// workDirs are the folders whose git changes Changes shows: the session's
// own, then the ones it worked in elsewhere, most recent first.
func (m Model) workDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d != "" && !seen[d] && len(dirs) < maxWorkDirs {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	if m.hasSess {
		add(m.sess.Cwd)
	}
	for _, d := range m.act.Dirs {
		add(d)
	}
	return dirs
}

// checkWork lists uncommitted changes and commits since the session began
// in each folder's repository. Folders outside a repository are skipped.
func checkWork(key string, dirs []string, since time.Time) workMsg {
	msg := workMsg{dir: key}
	seen := map[string]bool{}
	var firstErr error
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			continue
		}
		items, top, err := activity.WorkingTree(d)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		msg.tops = append(msg.tops, top)
		msg.items = append(msg.items, items...)
		committed, _ := activity.CommittedSince(top, since)
		msg.committed = append(msg.committed, committed...)
	}
	if len(msg.tops) == 0 {
		msg.err = firstErr
	}
	return msg
}

// inWorkTree reports whether p is inside one of the checked repositories.
func (m Model) inWorkTree(p string) bool {
	for _, top := range m.workTops {
		if p == top || strings.HasPrefix(p, top+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func colorDiff(d string) string {
	lines := strings.Split(strings.TrimRight(d, "\n"), "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(l, "\t", "    ")
		switch {
		case strings.HasPrefix(l, "commit "):
			lines[i] = sBold.Render(l)
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

// maxEditLines caps how many lines editsDiff shows.
const maxEditLines = 2000

// editsDiff shows the edits Claude made to a file outside git, oldest
// first, in diff form so it is coloured like one.
func editsDiff(edits []activity.FileEdit) string {
	n := len(edits)
	what := "edits"
	if n == 1 {
		what = "edit"
	}
	lines := []string{
		fmt.Sprintf("Not in a git repository: showing the %d %s Claude made this session.", n, what),
		"",
	}
	for i, e := range edits {
		head := fmt.Sprintf("@@ %d. %s", i+1, e.Tool)
		if at := editTime(e.At); at != "" {
			head += " · " + at
		}
		if e.Content {
			head += " (whole file)"
		}
		lines = append(lines, head+" @@")
		for _, l := range e.Old {
			lines = append(lines, "-"+l)
		}
		for _, l := range e.New {
			lines = append(lines, "+"+l)
		}
		lines = append(lines, "")
		if len(lines) > maxEditLines {
			lines = append(lines[:maxEditLines], fmt.Sprintf("… %d more edits not shown", n-i-1))
			break
		}
	}
	return strings.Join(lines, "\n")
}

// editTime is a transcript timestamp as local HH:MM, or "".
func editTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04")
}
