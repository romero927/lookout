package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/app"
)

// newClaudeMsg reports how opening a new Claude Code went.
type newClaudeMsg struct {
	dir  string
	note string // why it didn't split the session on screen, if it didn't
	err  error
}

// canNewClaude reports whether N (and the "+ claude" button) can open a
// new Claude Code: only inside tmux.
func canNewClaude() bool { return os.Getenv("TMUX") != "" }

// newClaudeDir is the folder a new Claude Code starts in: the one the
// session on screen works in, else the --cwd folder, else Lookout's own.
func (m Model) newClaudeDir() string {
	if o, ok := m.shownOverview(); ok && o.Cwd != "" {
		if st, err := os.Stat(o.Cwd); err == nil && st.IsDir() {
			return o.Cwd
		}
	}
	if m.o.Core.CwdFilter != "" {
		return m.o.Core.CwdFilter
	}
	d, _ := os.Getwd()
	return d
}

// newClaude opens Claude Code in a new tmux pane (new_claude = "split",
// the default), split from the pane of the session on screen. When that
// session can't be split (it has closed, isn't in tmux, or its pane is
// gone) it splits the largest running Claude Code pane in Lookout's own
// window, else Lookout's own pane; only when none works, or with
// new_claude = "window", it opens a new window. tmux then shows the new
// pane. Lookout follows it on Auto once it starts.
func (m Model) newClaude() tea.Cmd {
	dir := m.newClaudeDir()
	if !canNewClaude() {
		return func() tea.Msg { return newClaudeMsg{dir: dir, err: errors.New("Lookout isn't running in tmux")} }
	}
	split := m.o.Core.Cfg.UI.NewClaude != "window"
	shown, why := "", ""
	if o, ok := m.shownOverview(); ok {
		switch {
		case !o.Running:
			why = o.Label() + " is closed"
		case app.TmuxPane(o.Tmux) == "":
			why = o.Label() + " isn't in tmux"
		default:
			shown = app.TmuxPane(o.Tmux)
		}
	}
	var others []string
	for _, o := range m.running() {
		if p := app.TmuxPane(o.Tmux); p != "" && p != shown {
			others = append(others, p)
		}
	}
	own := os.Getenv("TMUX_PANE")
	return func() tea.Msg {
		tmux, err := exec.LookPath("tmux")
		if err != nil {
			return newClaudeMsg{dir: dir, err: errors.New("tmux isn't on the PATH")}
		}
		claude, err := exec.LookPath("claude")
		if err != nil {
			return newClaudeMsg{dir: dir, err: errors.New("claude isn't on the PATH")}
		}
		target, flag, note := "", "", ""
		if split {
			panes := tmuxPanes(tmux)
			info := func(pane string) (paneInfo, bool) { p, ok := panes[pane]; return p, ok }
			var p paneInfo
			var ok bool
			p, ok, note = chooseSplit(shown, why, others, own, info)
			if ok {
				target, flag = p.id, splitFlag(p.w, p.h)
			}
		}
		out, err := exec.Command(tmux, newClaudeArgs(target, flag, dir, claude)...).CombinedOutput()
		if err != nil {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				err = errors.New(msg)
			}
			return newClaudeMsg{dir: dir, err: err}
		}
		if ref := strings.TrimSpace(string(out)); ref != "" {
			if flag != "" {
				evenStack(tmux, ref, flag)
			}
			// Bring it into view, even in another tmux window or session.
			_ = exec.Command(tmux, tmuxGotoArgs(ref)...).Run()
		}
		return newClaudeMsg{dir: dir, note: note}
	}
}

// paneInfo is what tmux says about a pane.
type paneInfo struct {
	id, window string
	w, h       int
}

// tmuxPanes asks tmux about every pane, in one call, by pane ID; empty
// when tmux doesn't answer within a second.
func tmuxPanes(tmux string) map[string]paneInfo {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmux, "list-panes", "-a", "-F", "#{pane_id} #{window_id} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return nil
	}
	return parsePanes(string(out))
}

// parsePanes reads list-panes lines of "<pane> <window> <width> <height>".
func parsePanes(out string) map[string]paneInfo {
	panes := map[string]paneInfo{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || !strings.HasPrefix(f[0], "%") {
			continue
		}
		w, _ := strconv.Atoi(f[2])
		h, _ := strconv.Atoi(f[3])
		panes[f[0]] = paneInfo{id: f[0], window: f[1], w: w, h: h}
	}
	return panes
}

// chooseSplit picks the pane a new Claude Code splits: the session on
// screen's (shown), else the largest of the other running sessions' panes
// (others) in Lookout's own window, else Lookout's own pane (own). false
// means a new window. note says why it didn't split the session on screen
// ("" when it did); why is the reason shown can't be split, if known.
func chooseSplit(shown, why string, others []string, own string, info func(string) (paneInfo, bool)) (paneInfo, bool, string) {
	if p, ok := info(shown); ok {
		return p, true, ""
	}
	if why == "" {
		why = "no session on screen in tmux"
		if shown != "" {
			why = "its pane is gone"
		}
	}
	me, haveMe := info(own)
	if !haveMe {
		return paneInfo{}, false, why
	}
	best, found := paneInfo{}, false
	for _, id := range others {
		p, ok := info(id)
		if !ok || p.window != me.window {
			continue
		}
		if !found || p.w*p.h > best.w*best.h {
			best, found = p, true
		}
	}
	if found {
		return best, true, why
	}
	return me, true, why
}

// newClaudeArgs is the tmux command line that starts claude in dir: a
// split of pane target (split is -h or -v), or a new window when target
// is "". It prints the new pane as session:@window.%pane.
func newClaudeArgs(target, split, dir, claude string) []string {
	args := []string{"new-window"}
	if target != "" {
		args = []string{"split-window", split, "-t", target}
	}
	return append(args, "-P", "-F", "#{session_name}:#{window_id}.#{pane_id}", "-c", dir, shellQuote(claude))
}

// stackPane is a pane's place on screen within its window.
type stackPane struct {
	id                       string
	left, top, width, height int
}

// evenStack gives every pane in the new pane's stack the same size: the
// panes one above the other in its column (split -v) or side by side in
// its row (split -h). Splitting only halves the target pane, so without
// this the third panel would leave 50/25/25.
func evenStack(tmux, ref, flag string) {
	i := strings.LastIndex(ref, ".")
	if i < 0 {
		return
	}
	window, pane := ref[:i], ref[i+1:]
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmux, "list-panes", "-t", window, "-F", layoutFormat).Output()
	if err != nil {
		return
	}
	var panes []stackPane
	for _, p := range parseLayout(string(out)) {
		panes = append(panes, p.stackPane)
	}
	resizeEven(tmux, evenSizes(panes, pane, flag == "-h"), flag == "-h")
}

// resizeEven applies evenSizes' answer: heights, or widths when wide.
func resizeEven(tmux string, sizes []paneSize, wide bool) {
	axis := "-y"
	if wide {
		axis = "-x"
	}
	for _, r := range sizes {
		_ = exec.Command(tmux, "resize-pane", "-t", r.id, axis, strconv.Itoa(r.size)).Run()
	}
}

// layoutFormat is what list-panes prints for parseLayout.
const layoutFormat = "#{pane_id} #{window_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height}"

// layoutPane is a pane's place on screen and the window it's in.
type layoutPane struct {
	stackPane
	window string
}

// parseLayout reads list-panes lines in layoutFormat, by pane ID.
func parseLayout(out string) map[string]layoutPane {
	panes := map[string]layoutPane{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 6 || !strings.HasPrefix(f[0], "%") {
			continue
		}
		n := make([]int, 4)
		for j := range n {
			n[j], _ = strconv.Atoi(f[j+2])
		}
		panes[f[0]] = layoutPane{stackPane{f[0], n[0], n[1], n[2], n[3]}, f[1]}
	}
	return panes
}

// layoutMsg is every tmux pane's place on screen, asked each second so a
// closed Claude Code pane's stack can be evened out. nil when tmux didn't
// answer.
type layoutMsg map[string]layoutPane

// layoutCmd asks tmux where every pane is; nil outside tmux.
func layoutCmd() tea.Cmd {
	if !canNewClaude() {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", layoutFormat).Output()
		if err != nil {
			return layoutMsg(nil)
		}
		return layoutMsg(parseLayout(string(out)))
	}
}

// onLayout compares the panes now with a second ago: when a pane that
// ran Claude Code has closed, the panes left in its column (or row) share
// the space evenly, as + claude leaves them when it opens one. Other
// panes closing (an editor split, a shell) change nothing.
func (m *Model) onLayout(now layoutMsg) tea.Cmd {
	if now == nil {
		return nil
	}
	prev, claude := m.layout, m.claudePanes
	m.layout = now
	m.claudePanes = map[string]bool{}
	for _, o := range m.running() {
		if p := app.TmuxPane(o.Tmux); p != "" {
			m.claudePanes[p] = true
		}
	}
	var jobs []evenJob
	for id, gone := range prev {
		if _, ok := now[id]; ok || !claude[id] {
			continue
		}
		if j, ok := afterClose(gone, now); ok {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	return func() tea.Msg {
		if tmux, err := exec.LookPath("tmux"); err == nil {
			for _, j := range jobs {
				resizeEven(tmux, j.sizes, j.wide)
			}
		}
		return nil
	}
}

// evenJob is the resizes that even out one stack.
type evenJob struct {
	sizes []paneSize
	wide  bool
}

// afterClose says how to even out the stack a closed pane (gone) was in:
// its column when panes are left there (same left edge and width, in the
// same window), else its row.
func afterClose(gone layoutPane, now map[string]layoutPane) (evenJob, bool) {
	var col, row []stackPane
	for _, p := range now {
		if p.window != gone.window {
			continue
		}
		if p.left == gone.left && p.width == gone.width {
			col = append(col, p.stackPane)
		}
		if p.top == gone.top && p.height == gone.height {
			row = append(row, p.stackPane)
		}
	}
	if len(col) >= 2 {
		return evenJob{evenSizes(col, col[0].id, false), false}, true
	}
	if len(row) >= 2 {
		return evenJob{evenSizes(row, row[0].id, true), true}, true
	}
	return evenJob{}, false
}

// paneSize is one resize: pane id to size cells tall (or wide).
type paneSize struct {
	id   string
	size int
}

// evenSizes picks the panes stacked with pane (same left edge and width
// for a column; same top edge and height for a row, when wide) and says
// how big each should be so they share the space evenly, in order from
// the top (or left). The last pane takes what's left, so it isn't listed.
func evenSizes(panes []stackPane, pane string, wide bool) []paneSize {
	var me stackPane
	found := false
	for _, p := range panes {
		if p.id == pane {
			me, found = p, true
		}
	}
	if !found {
		return nil
	}
	var stack []stackPane
	for _, p := range panes {
		if (!wide && p.left == me.left && p.width == me.width) || (wide && p.top == me.top && p.height == me.height) {
			stack = append(stack, p)
		}
	}
	if len(stack) < 2 {
		return nil
	}
	pos := func(p stackPane) int {
		if wide {
			return p.left
		}
		return p.top
	}
	size := func(p stackPane) int {
		if wide {
			return p.width
		}
		return p.height
	}
	sort.Slice(stack, func(a, b int) bool { return pos(stack[a]) < pos(stack[b]) })
	n := len(stack)
	total := 0
	for _, p := range stack {
		total += size(p)
	}
	// The borders between panes take one cell each and stay put.
	each, extra := total/n, total%n
	var out []paneSize
	for k, p := range stack[:n-1] {
		s := each
		if k < extra {
			s++
		}
		out = append(out, paneSize{p.id, s})
	}
	return out
}

// splitFlag splits side by side when both halves stay at least 80 columns
// wide and the pane is wider than tall on screen; otherwise one above the
// other.
func splitFlag(w, h int) string {
	if w >= 160 && w >= 2*h {
		return "-h"
	}
	return "-v"
}

// onNewClaude says how opening a new Claude Code went.
func (m *Model) onNewClaude(msg newClaudeMsg) {
	if msg.err != nil {
		m.setFlash("can't open Claude: "+msg.err.Error(), sYellow, 6*time.Second)
		return
	}
	text := fmt.Sprintf("opened Claude in %s", ShortPath(msg.dir))
	if msg.note != "" {
		text += " (" + msg.note + ")"
	}
	if m.o.Core.Pinned != "" {
		text += " · click ↻ Auto to follow it"
	}
	m.setFlash(text, sGreen, 5*time.Second)
}
