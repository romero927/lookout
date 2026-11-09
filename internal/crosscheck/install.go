package crosscheck

import (
	"encoding/json"
	"os"
	"strings"
)

// HookCommand is the command line Claude Code should run on Stop.
func HookCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"$`\\") {
		exe = "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	}
	return exe + " hook stop"
}

// HookSnippet is the settings.json fragment that installs the hook.
// Lookout prints it; it never edits settings.json itself.
func HookSnippet(exe string) string {
	snippet := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": HookCommand(exe), "timeout": 10},
					},
				},
			},
		},
	}
	b, _ := json.MarshalIndent(snippet, "", "  ")
	return string(b)
}

// HookInstalled reports whether settings.json appears to run `lookout hook stop`.
// Read-only.
func HookInstalled(settingsPath string) bool {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &s) != nil {
		return false
	}
	for _, m := range s.Hooks["Stop"] {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, "lookout") && strings.Contains(h.Command, "hook stop") {
				return true
			}
		}
	}
	return false
}
