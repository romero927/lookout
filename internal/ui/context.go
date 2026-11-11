package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
)

// topItems is how many items the Context panel lists until "see all".
const topItems = 12

// viewContext breaks the context window down by what filled it.
func (m Model) viewContext(h int) string {
	w := m.width
	v := m.ctxView
	out := []string{section("Context", w)}
	if v.Total == 0 {
		return strings.Join(append(out, sMuted.Render("Nothing yet: the breakdown appears after Claude's first reply.")), "\n")
	}

	// Headline: total, window and where it's counted from.
	c := m.sugg.Context
	head := sBold.Render(Tokens(v.Total))
	if c.Window > 0 {
		head += sMuted.Render(" of ") + Tokens(c.Window) + sMuted.Render(fmt.Sprintf(" (%.0f%%)", c.Percent))
	}
	since := "since the session started"
	if v.Compacted {
		since = "since the last /compact, " + Ago(v.Since, m.now)
	}
	out = append(out, head+sMuted.Render(" · "+since))
	if c.Message != "" {
		st := sYellow
		if c.Level >= 2 {
			st = sRed
		}
		out = append(out, st.Render(clip(c.Message, w)))
	}

	// Parts, largest first, as shares of the total. In a short window only
	// the biggest parts are listed.
	labelW := min(42, max(20, w-36))
	gw := max(8, min(30, w-labelW-18))
	row := func(name string, tokens int64, note string) string {
		frac := float64(tokens) / float64(max(1, v.Total))
		return fmt.Sprintf("%s %s %s %s %s", pad(clip(name, labelW), labelW), bar(frac, gw, sBlue),
			lpad(Tokens(tokens), 6), sMuted.Render(lpad(fmt.Sprintf("%.0f%%", frac*100), 4)), sMuted.Render(note))
	}
	parts := []string{row("Startup (system prompt, tools, memory)", v.Startup, "")}
	for _, p := range v.Parts {
		note := ""
		if p.Count > 1 {
			note = fmt.Sprintf("%d items", p.Count)
		}
		parts = append(parts, row(p.Name, p.Tokens, note))
	}
	if v.Other > 0 {
		parts = append(parts, row("Other: thinking, attachments, estimate gap", v.Other, ""))
	}
	maxParts := max(3, h/3)
	if len(parts) > maxParts {
		parts = append(parts[:maxParts-1], sMuted.Render(fmt.Sprintf("… %d smaller parts", len(parts)-maxParts+1)))
	}
	out = append(out, "")
	out = append(out, parts...)
	if tip := contextTip(v); tip != "" {
		out = append(out, sYellow.Render(clip("Tip: "+tip, w)))
	}

	// Items: a list, or one item's details.
	title := fmt.Sprintf("Largest items (%d of %d)", len(v.Largest), v.Items)
	if m.xAll {
		title = fmt.Sprintf("All items (%d)", v.Items)
	}
	out = append(out, "", section(title, w))
	room := max(3, h-len(out)-1)
	if m.xFocus && m.xSel < len(v.Largest) {
		out = append(out, m.itemDetail(v.Largest[m.xSel], v, w, room)...)
	} else {
		out = append(out, m.itemList(v, w, room)...)
	}
	return strings.Join(out, "\n")
}

func itemKind(it activity.CtxItem) string {
	switch it.Kind {
	case activity.KindPrompt:
		return "prompt"
	case activity.KindReply:
		return "reply"
	case activity.KindInjected:
		return "injected"
	case activity.KindSummary:
		return "summary"
	case activity.KindCall:
		return it.Tool + " input"
	}
	return it.Tool
}

func (m Model) itemList(v activity.ContextView, w, room int) []string {
	var lines []string
	for i, it := range v.Largest {
		size, kind := lpad("~"+Tokens(it.Tokens), 7), pad(clip(itemKind(it.CtxItem), 14), 14)
		label, ago := clip(shortPaths(it.Label), max(10, w-42)), Ago(it.At, m.now)
		if i == m.xSel {
			line := fmt.Sprintf("%s  %s %s  %s", size, kind, label, ago)
			lines = append(lines, sSelected.Render(pad(clip("▸"+line, w), w)))
			continue
		}
		// The size and kind stand out; the description reads as plain text.
		line := sBold.Render(size) + "  " + sPurple.Render(kind) + " " + sText.Render(label) + "  " + sMuted.Render(ago)
		lines = append(lines, " "+clip(line, w-1))
	}
	if len(lines) == 0 {
		return []string{sMuted.Render("no items yet")}
	}
	return scrollTo(lines, m.xSel, room)
}

func (m Model) itemDetail(it activity.Sized, v activity.ContextView, w, room int) []string {
	wrap := lipgloss.NewStyle().Width(max(20, w-2))
	share := 100 * float64(it.Tokens) / float64(max(1, v.Total))
	lines := []string{
		sBold.Render(clip(itemKind(it.CtxItem)+" · "+shortPaths(it.Label), w)),
		sMuted.Render(fmt.Sprintf("~%s tokens · %.1f%% of the context · %s", Tokens(it.Tokens), share, Ago(it.At, m.now))),
	}
	if it.Images > 0 {
		lines = append(lines, sMuted.Render(fmt.Sprintf("includes %d image(s)", it.Images)))
	}
	if it.Preview != "" {
		lines = append(lines, "", sLabel.Render("STARTS WITH"))
		lines = append(lines, strings.Split(wrap.Render(sText.Render(strings.ReplaceAll(it.Preview, "\t", "  "))), "\n")...)
	}
	lines = append(lines, "", sLabel.Render("TO FREE UP ROOM"),
		sText.Render(clip("/rewind (Esc Esc): go back to before it. Removes it and everything after.", w)),
		sText.Render(clip("/compact: replaces the whole conversation with a summary.", w)),
		"", sMuted.Render("esc back"))
	if len(lines) > room {
		lines = lines[:room]
	}
	return lines
}

func (m Model) contextKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.ctxView.Largest)
	if m.xFocus {
		switch key {
		case "esc", "left", "h", "enter":
			m.xFocus = false
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		if m.xSel > 0 {
			m.xSel--
		}
	case "down", "j":
		if m.xSel < n-1 {
			m.xSel++
		}
	case "home", "g":
		m.xSel = 0
	case "end", "G":
		m.xSel = max(0, n-1)
	case "pgdown":
		m.xSel = min(max(0, n-1), m.xSel+10)
	case "pgup":
		m.xSel = max(0, m.xSel-10)
	case "enter", "right", "l":
		if n > 0 {
			m.xFocus = true
		}
	case "a":
		m.xAll = !m.xAll
		m.xSel = 0
		m.refreshContext()
	}
	return m, nil
}

// refreshContext reloads the breakdown (all items, or the largest few).
func (m *Model) refreshContext() {
	m.ctxView = activity.ContextView{}
	if !m.hasSess {
		return
	}
	top := topItems
	if m.xAll {
		top = 1 << 30
	}
	m.ctxView = m.o.Core.Activity.Context(m.sess.ID, top)
	if n := len(m.ctxView.Largest); m.xSel >= n {
		m.xSel = max(0, n-1)
	}
}

// contextTip suggests one way to keep the context smaller, based on what
// takes the most room.
func contextTip(v activity.ContextView) string {
	if len(v.Parts) == 0 || v.Total == 0 {
		return ""
	}
	p := v.Parts[0]
	if float64(p.Tokens)/float64(v.Total) < 0.25 {
		return ""
	}
	switch p.Name {
	case "Read output":
		return "file reads fill the most room. Pointing Claude at specific functions or line ranges keeps it smaller."
	case "Bash output":
		return "command output fills the most room. Quieter commands (no -v, | tail, | head) keep it smaller."
	case "Grep output", "Glob output":
		return "search results fill the most room. Narrower patterns or paths keep it smaller."
	case "Tool inputs (commands, files Claude wrote)":
		return "files Claude wrote stay in the context. /compact replaces them (and everything else) with a summary."
	case "Claude's replies":
		return "long replies fill the most room. Asking for brief answers keeps it smaller."
	}
	return ""
}

// clickContext opens the item clicked on line y of the Context body, or
// goes back to the list when its details are showing.
func (m Model) clickContext(y, h int) (tea.Model, tea.Cmd) {
	if m.xFocus {
		m.xFocus = false
		return m, nil
	}
	// The items start below the "Largest items" / "All items" heading.
	lines := strings.Split(m.viewContext(h), "\n")
	top := -1
	for i, l := range lines {
		if t := strings.TrimSpace(plainText(l)); strings.HasPrefix(t, "LARGEST ITEMS") || strings.HasPrefix(t, "ALL ITEMS") {
			top = i + 1
		}
	}
	n := len(m.ctxView.Largest)
	if top < 0 || y < top || n == 0 {
		return m, nil
	}
	room := max(3, h-top-1) // as viewContext sizes it
	start := 0
	if n > room {
		start = max(0, m.xSel-room+2)
	}
	if i := start + y - top; i < n {
		m.xSel = i
		m.xFocus = true
	}
	return m, nil
}
