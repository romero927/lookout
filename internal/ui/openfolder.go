package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

type openedMsg struct {
	dir string
	err error
}

// openFolder opens the folder holding path (or the nearest one that
// exists) in the OS file manager.
func openFolder(path string) tea.Cmd {
	return func() tea.Msg {
		dir := path
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			dir = filepath.Dir(dir)
		}
		for {
			if _, err := os.Stat(dir); err == nil || filepath.Dir(dir) == dir {
				break
			}
			dir = filepath.Dir(dir)
		}
		name := "xdg-open"
		switch runtime.GOOS {
		case "darwin":
			name = "open"
		case "windows":
			name = "explorer"
		}
		cmd := exec.Command(name, dir)
		if err := cmd.Start(); err != nil {
			return openedMsg{dir: dir, err: err}
		}
		go func() { _ = cmd.Wait() }()
		return openedMsg{dir: dir}
	}
}
