package ui

import (
	"fmt"
	"strings"

	"github.com/romero927/lookout/internal/activity"
)

// viewContext breaks the context window down by what filled it.
func (m Model) viewContext() string {
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
	out = append(out, "")

	// Parts, largest first, as shares of the total.
	labelW := min(42, max(20, w-36))
	gw := max(8, min(30, w-labelW-18))
	row := func(name string, tokens int64, note string) string {
		frac := float64(tokens) / float64(max(1, v.Total))
		return fmt.Sprintf("%s %s %s %s %s", pad(clip(name, labelW), labelW), bar(frac, gw, sBlue),
			lpad(Tokens(tokens), 6), sMuted.Render(lpad(fmt.Sprintf("%.0f%%", frac*100), 4)), sMuted.Render(note))
	}
	startup := "Startup: system prompt, tools, CLAUDE.md, memory"
	if v.Compacted {
		startup = "Startup + /compact summary"
	}
	out = append(out, row(startup, v.Startup, ""))
	for _, p := range v.Parts {
		note := ""
		if p.Count > 1 {
			note = fmt.Sprintf("%d items", p.Count)
		}
		out = append(out, row(p.Name, p.Tokens, note))
	}
	if v.Other > 0 {
		out = append(out, row("Other: thinking, attachments, estimate gap", v.Other, ""))
	}

	// Largest single items.
	if len(v.Largest) > 0 {
		out = append(out, "", section("Largest items", w))
		for _, it := range v.Largest {
			what := it.Tool
			switch it.Kind {
			case activity.KindPrompt:
				what = "prompt"
			case activity.KindReply:
				what = "reply"
			case activity.KindInjected:
				what = "injected"
			case activity.KindCall:
				what = it.Tool + " input"
			}
			out = append(out, clip(fmt.Sprintf("%s  %s %s  %s", lpad("~"+Tokens(it.Tokens), 7), sPurple.Render(pad(clip(what, 14), 14)),
				sText.Render(clip(it.Label, max(10, w-40))), sMuted.Render(Ago(it.At, m.now))), w+40))
		}
	}

	if tip := contextTip(v); tip != "" {
		out = append(out, "", sYellow.Render(clip("Tip: "+tip, w)))
	}
	out = append(out, "", sMuted.Render(clip("The total is what Claude Code reported; the parts are estimated from text length.", w)))
	return strings.Join(out, "\n")
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
		return "file reads fill most of the context. Pointing Claude at specific functions or line ranges keeps it smaller."
	case "Bash output":
		return "command output fills most of the context. Quieter commands (no -v, | tail, | head) keep it smaller."
	case "Grep output", "Glob output":
		return "search results fill most of the context. Narrower patterns or paths keep it smaller."
	case "Tool inputs (incl. files Claude wrote)":
		return "files Claude wrote stay in the context. /compact drops them and keeps a summary."
	case "Claude's replies":
		return "long replies fill most of the context. Asking for brief answers keeps it smaller."
	}
	return ""
}
