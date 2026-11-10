package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/update"
)

// updateMsg is the result of an update check.
type updateMsg struct {
	latest string
	err    error
}

// updateCheck asks for the latest release when [updates] check is on and
// the last check was a day ago or more. It runs off the UI goroutine.
func (m *Model) updateCheck() tea.Cmd {
	if !m.o.Core.Cfg.Updates.Check || m.now.Sub(m.updAsked) < update.Every {
		return nil
	}
	m.updAsked = m.now
	now := m.now
	return func() tea.Msg {
		tag, err := update.Latest(context.Background(), paths.UpdateFile(), now)
		return updateMsg{latest: tag, err: err}
	}
}

// onUpdate records a newer release and says so once.
func (m *Model) onUpdate(msg updateMsg) {
	if msg.err != nil {
		// Try again in an hour rather than a day.
		m.updAsked = m.now.Add(time.Hour - update.Every)
		return
	}
	if !update.Newer(m.o.Version, msg.latest) || msg.latest == m.updNew {
		return
	}
	m.updNew = msg.latest
	m.setFlash("Lookout "+msg.latest+" is out · update: "+update.InstallCmd+" (or grab a release binary)", sGreen, 15*time.Second)
}

// updateHelp is the help screen's line about the update check.
func (m Model) updateHelp() string {
	switch {
	case !m.o.Core.Cfg.Updates.Check:
		return "Off. Set [updates] check = true in config.toml to hear about new releases (asks GitHub once a day)."
	case m.updNew != "":
		return m.updNew + " is out (you have " + m.o.Version + "). Update: " + update.InstallCmd
	default:
		return "On: GitHub is asked once a day. You have the latest release (" + m.o.Version + ")."
	}
}
