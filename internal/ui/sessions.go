package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const maxPick = 40

func (m Model) openPicker() Model {
	m.picking = true
	m.pList = m.o.Core.Agg.Sessions()
	if len(m.pList) > maxPick {
		m.pList = m.pList[:maxPick]
	}
	m.pSel = 0
	for i, s := range m.pList {
		if m.hasSess && s.ID == m.sess.ID {
			m.pSel = i
		}
	}
	return m
}

func (m Model) pickerKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "s", "q":
		m.picking = false
	case "up", "k":
		if m.pSel > 0 {
			m.pSel--
		}
	case "down", "j":
		if m.pSel < len(m.pList)-1 {
			m.pSel++
		}
	case "y":
		if m.pSel < len(m.pList) {
			return m, copyText(m.pList[m.pSel].ID)
		}
	case "a":
		m.o.Core.Pinned = ""
		m.picking = false
		m.setFlash("following the latest session", sMuted, 3*time.Second)
		m.refresh()
	case "enter":
		if m.pSel < len(m.pList) {
			s := m.pList[m.pSel]
			if latest, ok := m.o.Core.Agg.Current(m.o.Core.CwdFilter); ok && latest.ID == s.ID {
				// The latest session: follow it rather than pin it, so a
				// newer session still takes over.
				return m.pickerKey("a")
			}
			m.o.Core.Pinned = s.ID
			m.picking = false
			m.setFlash("showing session "+ShortID(s.ID)+" · s then a to follow the latest again", sGreen, 4*time.Second)
			m.refresh()
		}
	}
	return m, nil
}

// pickerHead is the number of lines above the first session in the picker.
const pickerHead = 3

// pickerRowAt returns the session on body line y of a picker h lines tall,
// or -1. It mirrors the scrolling in viewPicker.
func (m Model) pickerRowAt(y, h int) int {
	start := 0
	if total := pickerHead + len(m.pList); total > h {
		start = max(0, m.pSel+pickerHead-h+2)
	}
	if i := start + y - pickerHead; i >= 0 && i < len(m.pList) {
		return i
	}
	return -1
}

func (m Model) viewPicker(h int) string {
	w := m.width
	mode := "Following the latest session"
	if m.o.Core.CwdFilter != "" {
		mode += " in " + ShortPath(m.o.Core.CwdFilter)
	}
	if m.o.Core.Pinned != "" {
		mode = "Pinned to session " + ShortID(m.o.Core.Pinned)
	}
	out := []string{section("Sessions", w), sMuted.Render(clip(mode+" · enter or click to show one · a follow the latest · y copy ID · esc close", w)), ""}
	if len(m.pList) == 0 {
		out = append(out, sMuted.Render("No sessions found."))
	}
	latest, _ := m.o.Core.Agg.Current(m.o.Core.CwdFilter)
	for i, s := range m.pList {
		live := sMuted.Render("○")
		if m.now.Sub(s.LastActivity) < 2*time.Minute {
			live = sGreen.Render("●")
		}
		proj := filepath.Base(s.Cwd)
		if proj == "." || proj == "/" {
			proj = s.Project
		}
		line := fmt.Sprintf("%s %s  %s  %s  %s", live, ShortID(s.ID), pad(clip(proj, 22), 22),
			pad(ShortModel(s.Model), 12), Ago(s.LastActivity, m.now))
		if s.ID == latest.ID {
			line += "  latest"
		}
		if i == m.pSel {
			out = append(out, sSelected.Render(pad("▸ "+line, w-1)))
		} else {
			out = append(out, "  "+line)
		}
	}
	return strings.Join(scrollTo(out, m.pSel+pickerHead, h), "\n")
}

// togglePin keeps the session on screen even when another one becomes
// active, or, when already pinned, goes back to following the latest.
func (m Model) togglePin() Model {
	if m.o.Core.Pinned != "" {
		m.o.Core.Pinned = ""
		m.setFlash("unpinned · following the latest session", sMuted, 3*time.Second)
		m.refresh()
		return m
	}
	cur, ok := m.o.Core.Current()
	if !ok {
		m.setFlash("no session to pin yet", sMuted, 3*time.Second)
		return m
	}
	m.o.Core.Pinned = cur.ID
	m.setFlash("pinned session "+ShortID(cur.ID)+" · P to unpin", sGreen, 4*time.Second)
	m.refresh()
	return m
}

// needsSession reports whether a panel has nothing to show without a
// session. Usage, Skills, Config and the Cheatsheet still work without one.
func needsSession(tab int) bool {
	switch tab {
	case tabActivity, tabChanges, tabContext, tabModels, tabTools:
		return true
	}
	return false
}

// waitingFor names where Lookout is looking for a session: the --here
// folder when set, otherwise the transcripts folder.
func (m Model) waitingFor() string {
	if d := m.o.Core.CwdFilter; d != "" {
		return ShortPath(d)
	}
	return ShortPath(m.o.Core.Watcher.Root)
}

// viewNoSession is the placeholder shown on session panels until a
// Claude Code session appears.
func (m Model) viewNoSession() string {
	w := max(10, m.width-4)
	if l, ok := m.o.Core.Starting(); ok {
		where := ShortPath(l.Cwd)
		out := []string{
			"",
			"  " + sBold.Render(clip("New Claude Code session "+ShortID(l.ID)+" in "+where, w)),
			"",
			"  " + sMuted.Render(clip("Waiting for its first message. It shows up here once you send one.", w)),
			"  " + sMuted.Render(clip("Press s to choose another session.", w)),
		}
		return strings.Join(out, "\n")
	}
	out := []string{
		"",
		"  " + sBold.Render(clip("Waiting for a Claude Code session in "+m.waitingFor(), w)),
		"",
		"  " + sMuted.Render(clip("Start `claude` in this folder and Lookout picks it up on its own.", w)),
	}
	if len(m.o.Core.Agg.Sessions()) > 0 {
		out = append(out, "  "+sMuted.Render(clip("Press s to choose another session.", w)))
	}
	return strings.Join(out, "\n")
}
