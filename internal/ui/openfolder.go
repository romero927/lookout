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
		return openedMsg{dir: dir, err: osOpen(dir)}
	}
}

// ConsoleCostURL is the Claude Console page with an API account's usage and
// spend.
const ConsoleCostURL = "https://platform.claude.com/cost"

// openURL opens a web page in the default browser.
func openURL(url string) tea.Cmd {
	return func() tea.Msg { return openedMsg{dir: url, err: osOpen(url)} }
}

// osOpen hands target (a folder or URL) to the OS opener.
func osOpen(target string) error {
	name := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "explorer"
	}
	cmd := exec.Command(name, target)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
