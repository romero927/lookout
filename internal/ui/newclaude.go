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
	dir string
	err error
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

// newClaude opens Claude Code in a new tmux pane: split from the pane of
// the session on screen (new_claude = "split", the default), or in a new
// window when that session isn't in tmux or new_claude = "window". tmux
// then shows the new pane. Lookout follows it on Auto once it starts.
func (m Model) newClaude() tea.Cmd {
	dir := m.newClaudeDir()
	if !canNewClaude() {
		return func() tea.Msg { return newClaudeMsg{dir: dir, err: errors.New("Lookout isn't running in tmux")} }
	}
	target := ""
	if m.o.Core.Cfg.UI.NewClaude != "window" {
		if o, ok := m.shownOverview(); ok && o.Running && o.Tmux != "" {
			target = paneOf(o.Tmux)
		}
	}
	return func() tea.Msg {
		tmux, err := exec.LookPath("tmux")
		if err != nil {
			return newClaudeMsg{dir: dir, err: errors.New("tmux isn't on the PATH")}
		}
		claude, err := exec.LookPath("claude")
		if err != nil {
			return newClaudeMsg{dir: dir, err: errors.New("claude isn't on the PATH")}
		}
		split := ""
		if target != "" {
			split = "-v"
			out, err := exec.Command(tmux, "display-message", "-p", "-t", target, "#{pane_width} #{pane_height}").Output()
			if err != nil {
				target = "" // the pane is gone: open a window instead
			} else if f := strings.Fields(string(out)); len(f) == 2 {
				w, _ := strconv.Atoi(f[0])
				h, _ := strconv.Atoi(f[1])
				split = splitFlag(w, h)
			}
		}
		out, err := exec.Command(tmux, newClaudeArgs(target, split, dir, claude)...).CombinedOutput()
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
		return newClaudeMsg{dir: dir}
	}
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
	if m.o.Core.Pinned != "" {
		text += " · click ↻ Auto to follow it"
	}
	m.setFlash(text, sGreen, 5*time.Second)
}
