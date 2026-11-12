package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
		case o.Tmux == "":
			why = o.Label() + " isn't in tmux"
		default:
			shown = paneOf(o.Tmux)
		}
	}
	var others []string
	for _, o := range m.running() {
		if p := paneOf(o.Tmux); o.Tmux != "" && p != shown {
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
			info := func(pane string) (paneInfo, bool) { return tmuxPaneInfo(tmux, pane) }
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

// tmuxPaneInfo asks tmux about pane; false when it is gone.
func tmuxPaneInfo(tmux, pane string) (paneInfo, bool) {
	if pane == "" {
		return paneInfo{}, false
	}
	out, err := exec.Command(tmux, "display-message", "-p", "-t", pane, "#{pane_id} #{window_id} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return paneInfo{}, false
	}
	f := strings.Fields(string(out))
	if len(f) != 4 || f[0] != pane {
		return paneInfo{}, false
	}
	w, _ := strconv.Atoi(f[2])
	h, _ := strconv.Atoi(f[3])
	return paneInfo{id: f[0], window: f[1], w: w, h: h}, true
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

// splitFlag splits side by side when both halves stay at least 80 columns
// wide and the pane is wider than tall on screen; otherwise one above the
// other.
func splitFlag(w, h int) string {
	if w >= 160 && w >= 2*h {
		return "-h"
	}
	return "-v"
}

// paneOf is the pane ID in a reference like "main:@2.%7", or the whole
// reference when it has none.
func paneOf(ref string) string {
	if i := strings.LastIndex(ref, "."); i >= 0 && strings.HasPrefix(ref[i+1:], "%") {
		return ref[i+1:]
	}
	return ref
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
