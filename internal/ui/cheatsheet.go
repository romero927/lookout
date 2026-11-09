package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/strategy"
)

// cheat is one copyable command.
type cheat struct {
	Group string
	Cmd   string
	Desc  string
}

// Commands checked against Claude Code 2.1.x. Lookout only copies them; you
// paste them into Claude Code (or a terminal) yourself.
var staticCheats = []cheat{
	{"Sessions", "/resume", "Pick a previous conversation to continue"},
	{"Sessions", "claude --continue", "In a terminal: continue the latest conversation in this folder"},
	{"Sessions", "/rename ", "Name this conversation, so --resume can find it by name"},
	{"Sessions", "/branch", "Branch the conversation at this point"},
	{"Sessions", "/clear", "Start a new session with empty context (the old one stays resumable)"},
	{"Context", "/context", "Show context usage as a grid"},
	{"Context", "/compact", "Summarize the conversation to free up context"},
	{"Context", "/compact Keep only the current task, decisions made and files changed; drop old tool output.", "Summarize everything, steering what the summary keeps"},
	{"Context", "/rewind", "Go back to an earlier point (also Esc Esc)"},
	{"Context", "/btw ", "Ask a side question without adding it to the main conversation"},
	{"Model & usage", "/model", "Choose the model"},
	{"Model & usage", "/effort", "Set the effort level"},
	{"Model & usage", "/fast", "Toggle fast mode"},
	{"Model & usage", "/usage", "Session cost, plan usage and activity stats"},
	{"Project", "/memory", "Edit CLAUDE.md files and memory settings"},
	{"Project", "/init", "Create a CLAUDE.md for this codebase"},
	{"Project", "/plan", "Turn on plan mode, or view the session plan"},
	{"Project", "/add-dir ", "Add another working directory"},
	{"Project", "/permissions", "Manage allow and deny rules for tools"},
	{"Project", "/hooks", "View hook configurations"},
	{"Project", "/mcp", "Manage MCP servers"},
	{"Project", "/export", "Export the conversation to a file or the clipboard"},
	{"Project", "/status", "Version, model, account and connectivity"},
}

// cheats is the full list: suggestions for the session in view first.
func (m Model) cheats() []cheat {
	var out []cheat
	if m.sugg.Action != strategy.Keep && m.sugg.Suggested != "" {
		out = append(out, cheat{"For this session", "/model " + m.sugg.Suggested, "Lookout's model suggestion: " + m.sugg.Reason})
	}
	if c := m.sugg.Context; c.Level >= strategy.ContextCompact {
		out = append(out, cheat{"For this session", "/compact", fmt.Sprintf("Context is %.0f%% full", c.Percent)})
	}
	if m.hasSess {
		out = append(out, cheat{"For this session", m.sess.ID, "This session's ID"})
		cmd := "claude --resume " + m.sess.ID
		if m.sess.Cwd != "" {
			cmd = fmt.Sprintf("cd %s && %s", shellQuote(m.sess.Cwd), cmd)
		}
		out = append(out, cheat{"For this session", cmd, "In a terminal: reopen the session Lookout is showing (" + ShortID(m.sess.ID) + ")"})
	}
	return append(out, staticCheats...)
}

// shellQuote quotes a path for a POSIX shell when it needs it.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_' || r == '~' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	if strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m Model) viewCheatsheet(h int) string {
	w := m.width
	items := m.cheats()
	lines := []string{section("Cheatsheet", w), sMuted.Render(clip("enter or c copies the command; paste it into Claude Code yourself. Lookout never types into it.", w))}
	sel := 0
	group := ""
	cmdW := min(44, max(16, w/2-4))
	for i, c := range items {
		if c.Group != group {
			group = c.Group
			lines = append(lines, "", sLabel.Render(strings.ToUpper(group)))
		}
		cmd := pad(clip(c.Cmd, cmdW), cmdW)
		desc := clip(c.Desc, max(10, w-cmdW-5))
		if i == m.kSel {
			sel = len(lines)
			lines = append(lines, sSelected.Render(pad("▸ "+cmd+"  "+desc, w-1)))
			continue
		}
		lines = append(lines, "  "+sGreen.Render(cmd)+"  "+sMuted.Render(desc))
	}
	if m.kSel < len(items) {
		if full := items[m.kSel].Cmd; len(full) > cmdW {
			lines = append(lines, "", sMuted.Render("selected: ")+clip(full, w-10))
		}
	}
	return strings.Join(scrollTo(lines, sel, h), "\n")
}

func (m Model) cheatsheetKey(key string) (tea.Model, tea.Cmd) {
	items := m.cheats()
	switch key {
	case "up", "k":
		if m.kSel > 0 {
			m.kSel--
		}
	case "down", "j":
		if m.kSel < len(items)-1 {
			m.kSel++
		}
	case "home", "g":
		m.kSel = 0
	case "end", "G":
		m.kSel = max(0, len(items)-1)
	case "enter", "c":
		if m.kSel < len(items) {
			return m, copyText(items[m.kSel].Cmd)
		}
	}
	return m, nil
}
