package ui

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Light formatting for text shown in the viewers: enough markdown to make
// Claude's messages and .md files readable, and colour for JSON. No
// layout changes beyond wrapping, so what you see matches the source.

var (
	mdBold   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	mdCode   = regexp.MustCompile("`([^`\n]+)`")
	mdBullet = regexp.MustCompile(`^(\s*)[-*+] `)
	mdNumber = regexp.MustCompile(`^(\s*)(\d+)\. `)
	mdLink   = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	sCode    = lipgloss.NewStyle().Foreground(cPurple)
)

// markdown renders s for a terminal of width w: headings and bold in bold,
// `code` highlighted, bullets as •, fenced code dimmed, then wrapped.
func markdown(s string, w int) []string {
	wrap := lipgloss.NewStyle().Width(max(10, w))
	var out []string
	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(s, "\t", "    "), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, sMuted.Render("  "+clip(line, max(10, w-2))))
			continue
		}
		switch {
		case strings.HasPrefix(trim, "#"):
			out = append(out, strings.Split(wrap.Render(sBold.Render(strings.TrimSpace(strings.TrimLeft(trim, "#")))), "\n")...)
			continue
		case trim == "---" || trim == "***":
			out = append(out, sMuted.Render(strings.Repeat("─", min(w, 20))))
			continue
		}
		indent := ""
		if m := mdBullet.FindStringSubmatch(line); m != nil {
			indent = m[1] + "  "
			line = m[1] + "• " + line[len(m[0]):]
		} else if m := mdNumber.FindStringSubmatch(line); m != nil {
			indent = m[1] + strings.Repeat(" ", len(m[2])+2)
		}
		line = inline(line)
		wrapped := strings.Split(lipgloss.NewStyle().Width(max(10, w)).Render(line), "\n")
		for i, l := range wrapped {
			if i > 0 && indent != "" {
				l = indent + strings.TrimLeft(l, " ")
			}
			out = append(out, l)
		}
	}
	return out
}

func inline(s string) string {
	s = mdLink.ReplaceAllString(s, "$1 ($2)")
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string { return sCode.Render(m[1 : len(m)-1]) })
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string { return sBold.Render(m[2 : len(m)-2]) })
	return s
}

var (
	jsonKey   = regexp.MustCompile(`^(\s*)("(?:[^"\\]|\\.)*")(\s*:)`)
	jsonStr   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	jsonValue = regexp.MustCompile(`\b(true|false|null|-?\d+(?:\.\d+)?)\b`)
)

// highlightLine colours one line of a file by its type.
func highlightLine(path, line string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		key := ""
		if m := jsonKey.FindStringSubmatch(line); m != nil {
			key = m[1] + sBlue.Render(m[2]) + m[3]
			line = line[len(m[0]):]
		}
		line = jsonStr.ReplaceAllStringFunc(line, func(s string) string { return sGreen.Render(s) })
		if !strings.Contains(line, "\x1b") {
			line = jsonValue.ReplaceAllStringFunc(line, func(s string) string { return sPurple.Render(s) })
		}
		return key + line
	case ".md", ".markdown":
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "#"):
			return sBold.Render(line)
		case strings.HasPrefix(t, "---"):
			return sMuted.Render(line)
		}
		return inline(line)
	}
	return line
}
