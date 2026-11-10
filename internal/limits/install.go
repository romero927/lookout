package limits

import (
	"encoding/json"
	"os"
	"strings"
)

// StatusLineCommand is the command Claude Code should run for its status line.
func StatusLineCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"$`\\") {
		exe = shellQuote(exe)
	}
	return exe + " statusline"
}

// WrapCommand is the status line command that runs existing first and adds
// Lookout's line after its output.
func WrapCommand(exe, existing string) string {
	return StatusLineCommand(exe) + " --wrap " + shellQuote(existing)
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StatusLineSnippet is the settings.json fragment that installs the status
// line. Lookout prints it; it never edits settings.json itself.
func StatusLineSnippet(exe string) string {
	return snippet(map[string]any{"type": "command", "command": StatusLineCommand(exe)})
}

// WrappedSnippet is the settings.json fragment that keeps an existing status
// line and adds Lookout's after it. Other fields of the existing entry, such
// as padding, are kept.
func WrappedSnippet(exe string, existing map[string]any) string {
	sl := make(map[string]any, len(existing))
	for k, v := range existing {
		sl[k] = v
	}
	cmd, _ := existing["command"].(string)
	sl["command"] = WrapCommand(exe, cmd)
	return snippet(sl)
}

func snippet(sl map[string]any) string {
	b, _ := json.MarshalIndent(map[string]any{"statusLine": sl}, "", "  ")
	return string(b)
}

// ExistingStatusLine returns the statusLine entry in settings.json when it is
// a command that can be wrapped. Read-only.
func ExistingStatusLine(settingsPath string) (map[string]any, bool) {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, false
	}
	var s struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if json.Unmarshal(b, &s) != nil || s.StatusLine == nil {
		return nil, false
	}
	cmd, _ := s.StatusLine["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return nil, false
	}
	if t, ok := s.StatusLine["type"].(string); ok && t != "command" {
		return nil, false
	}
	return s.StatusLine, true
}

// StatusLineSetting reads settings.json and reports whether a status line is
// configured at all, and whether it is Lookout's. Read-only.
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
	return true, strings.Contains(c, "lookout") && strings.Contains(c, "statusline")
}
