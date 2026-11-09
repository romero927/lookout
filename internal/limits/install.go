package limits

import (
	"encoding/json"
	"os"
	"strings"
)

// StatusLineCommand is the command Claude Code should run for its status line.
func StatusLineCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"$`\\") {
		exe = "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	}
	return exe + " statusline"
}

// StatusLineSnippet is the settings.json fragment that installs the status
// line. Overwatch prints it; it never edits settings.json itself.
func StatusLineSnippet(exe string) string {
	b, _ := json.MarshalIndent(map[string]any{
		"statusLine": map[string]any{"type": "command", "command": StatusLineCommand(exe)},
	}, "", "  ")
	return string(b)
}

// StatusLineSetting reads settings.json and reports whether a status line is
// configured at all, and whether it is Overwatch's. Read-only.
func StatusLineSetting(settingsPath string) (configured, ours bool) {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return false, false
	}
	var s struct {
		StatusLine *struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if json.Unmarshal(b, &s) != nil || s.StatusLine == nil {
		return false, false
	}
	c := s.StatusLine.Command
	return true, strings.Contains(c, "overwatch") && strings.Contains(c, "statusline")
}
