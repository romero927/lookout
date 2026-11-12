package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/configfiles"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/skills"
)

// refreshSkills rescans skills, commands and subagents for the session's
// project and the user's ~/.claude.
func (m *Model) refreshSkills() {
	var projects []string
	m.skillsFor = ""
	if m.hasSess {
		projects = append(projects, m.sess.Cwd)
		m.skillsFor = m.sess.Cwd
	}
	m.skills = skills.Discover(paths.ClaudeDir(), projects)
	if m.sSel >= len(m.skills) {
		m.sSel = max(0, len(m.skills)-1)
	}
	m.sShown = ""
	m.loadSkill()
}

func skillGroup(it skills.Item) string {
	kind := map[string]string{skills.KindSkill: "Skills", skills.KindCommand: "Commands", skills.KindAgent: "Subagents"}[it.Kind]
	src := it.Source
	if it.Plugin != "" {
		src = "plugin " + it.Plugin
		if it.Disabled {
			src += " (turned off)"
		}
	}
	return kind + " · " + src
}

// skillsList is the list of skills, commands and subagents, by group.
func (m Model) skillsList(lw int) list {
	var l list
	l.add(sSection.Render("SKILLS")+" "+sMuted.Render(fmt.Sprintf("%d available", len(m.skills))), -1)
	if len(m.skills) == 0 {
		l.add(sMuted.Render("  none found"), -1)
	}
	group := ""
	for i, it := range m.skills {
		if g := skillGroup(it); g != group {
			group = g
			l.add(sLabel.Render(clip(g, lw)), -1)
		}
		name := clip(it.Name, lw-4)
		if i == m.sSel {
			l.sel = len(l.lines)
			l.add(sSelected.Render(pad("▸ "+name, lw-1)), i)
		} else {
			l.add("  "+name, i)
		}
	}
	return l
}

func (m Model) viewSkills(h int) string {
	lw := m.listWidth()
	l := m.skillsList(lw)
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(l.lines, l.sel, h), "\n"))

	title := sMuted.Render("Skills, custom commands and subagents Claude Code can use appear here.")
	meta := ""
	if m.sSel < len(m.skills) {
		it := m.skills[m.sSel]
		title = sBold.Render(clip(it.Name, m.sView.Width)) + sMuted.Render("  "+strings.ToLower(skillGroup(it)))
		meta = sMuted.Render(clip(ShortPath(it.Path), m.sView.Width))
	}
	border := cFaint
	if m.sFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		title + "\n" + meta + "\n" + m.sView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// loadSkill shows the selected item's summary and its definition file.
func (m *Model) loadSkill() {
	if m.sSel >= len(m.skills) {
		m.sView.SetContent("")
		return
	}
	it := m.skills[m.sSel]
	if m.sShown == it.Path {
		return
	}
	m.sShown = it.Path
	w := max(20, m.sView.Width)
	wrap := lipgloss.NewStyle().Width(w)
	var b []string
	if it.Description != "" {
		b = append(b, wrap.Render(sText.Render(it.Description)), "")
	}
	if it.WhenToUse != "" {
		b = append(b, sLabel.Render("WHEN TO USE"), wrap.Render(sText.Render(it.WhenToUse)), "")
	}
	b = append(b, sLabel.Render("HOW TO USE IT"))
	switch {
	case it.Disabled:
		b = append(b, wrap.Render(sYellow.Render("Its plugin "+it.Plugin+" is turned off. Turn it on with /plugin in Claude Code to use it.")))
	case it.Kind == skills.KindAgent:
		b = append(b, wrap.Render(sText.Render("Claude hands work to this subagent when it fits, or when you ask for it by name.")))
	case !it.ModelInvocable:
		b = append(b, wrap.Render(sText.Render("Only when you run it: Claude won't use it on its own.")))
	case it.Kind == skills.KindSkill:
		b = append(b, wrap.Render(sText.Render("Claude uses it on its own when a request matches the description. You can also ask for it directly.")))
	}
	inv := it.Invoke()
	b = append(b, sGreen.Render(strings.TrimSpace(inv))+sMuted.Render(argNote(it)), sMuted.Render("c copies this"), "")
	b = append(b, sLabel.Render("DEFINITION"))
	if s, err := configfiles.Read(it.Path); err != nil {
		b = append(b, sRed.Render(err.Error()))
	} else {
		b = append(b, numbered(it.Path, s))
	}
	m.sView.SetContent(strings.Join(b, "\n"))
	m.sView.GotoTop()
}

func argNote(it skills.Item) string {
	switch {
	case it.ArgHint != "":
		return "  then " + it.ArgHint
	case strings.HasSuffix(it.Invoke(), " to "):
		return "  …then say what you want done"
	}
	return ""
}

func (m Model) skillsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.sSel > 0 {
			m.sSel--
		}
	case "down", "j":
		if m.sSel < len(m.skills)-1 {
			m.sSel++
		}
	case "home", "g":
		m.sSel = 0
	case "end", "G":
		m.sSel = max(0, len(m.skills)-1)
	case "enter", "right", "l":
		if len(m.skills) > 0 {
			m.sFocus = true
		}
	case "r":
		m.refreshSkills()
		m.setFlash("rescanned skills", sMuted, 2*time.Second)
	case "c":
		if m.sSel < len(m.skills) {
			return m, copyText(m.skills[m.sSel].Invoke())
		}
	}
	m.loadSkill()
	return m, nil
}
