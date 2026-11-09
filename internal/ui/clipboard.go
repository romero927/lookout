package ui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type copiedMsg struct {
	how, err string
	warn     bool // it didn't reach the OS clipboard
}

// copyText puts text on the clipboard, trying in order: a clipboard tool,
// tmux's paste buffer (which tmux also forwards to the terminal's clipboard
// when it can), then the OSC 52 terminal escape.
func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		run := func(name string, args ...string) bool {
			p, err := exec.LookPath(name)
			if err != nil {
				return false
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, p, args...)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run() == nil
		}
		switch {
		case os.Getenv("WAYLAND_DISPLAY") != "" && run("wl-copy"):
			return copiedMsg{how: "copied to the clipboard"}
		case run("pbcopy"), run("clip.exe"):
			return copiedMsg{how: "copied to the clipboard"}
		case os.Getenv("DISPLAY") != "" && (run("xclip", "-selection", "clipboard") || run("xsel", "--clipboard", "--input")):
			return copiedMsg{how: "copied to the clipboard"}
		case os.Getenv("TMUX") != "" && run("tmux", "load-buffer", "-w", "-"):
			return copiedMsg{how: "copied to tmux's buffer only (paste: prefix then ]). For the OS clipboard, install xclip: sudo apt install xclip", warn: true}
		}
		seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
		if _, err := os.Stdout.WriteString(seq); err != nil {
			return copiedMsg{err: err.Error()}
		}
		return copiedMsg{how: "sent to the terminal's clipboard, if it allows that. For the OS clipboard, install xclip (or xsel, wl-copy)", warn: true}
	}
}
