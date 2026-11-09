package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/overwatch/internal/configfiles"
	"github.com/romero927/overwatch/internal/crosscheck"
	"github.com/romero927/overwatch/internal/paths"
)

func removeFile(p string) error { return os.Remove(p) }

// ---- Files ----

func (m Model) viewFiles(h int) string {
	lw := m.listWidth()
	var list []string
	mode := sMuted.Render("read-only")
	if m.o.Core.Cfg.Files.AllowEdit {
		mode = sYellow.Render("editing enabled")
	}
	list = append(list, sSection.Render("FILES")+" "+mode)
	var group configfiles.Group
	for i, f := range m.files {
		if f.Group != group {
			group = f.Group
			list = append(list, sLabel.Render(string(group)))
		}
		label := clip(f.Label, lw-4)
		var line string
		switch {
		case i == m.gSel:
			line = sSelected.Render(pad("▸ "+label, lw-1))
		case !f.Exists:
			line = sMuted.Render("  " + label + " ·")
		default:
			line = "  " + label
		}
		list = append(list, line)
	}
	left := lipgloss.NewStyle().Width(lw).MaxHeight(h).Render(strings.Join(scrollTo(list, m.gSel+groupOffset(m.files, m.gSel)+1, h), "\n"))

	var title, meta string
	if m.gSel < len(m.files) {
		f := m.files[m.gSel]
		title = sBold.Render(ShortPath(f.Path))
		if f.Exists {
			meta = sMuted.Render(fmt.Sprintf("%s · modified %s", sizeStr(f.Size), Ago(f.ModTime, m.now)))
		} else {
			meta = sMuted.Render("not present")
		}
	}
	border := cFaint
	if m.gFocus {
		border = cRed
	}
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(
		clip(title, m.gView.Width) + "\n" + meta + "\n" + m.gView.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// groupOffset counts the group header lines above item i.
func groupOffset(files []configfiles.File, i int) int {
	n := 1 // panel title
	var g configfiles.Group
	for j := 0; j <= i && j < len(files); j++ {
		if files[j].Group != g {
			g = files[j].Group
			n++
		}
	}
	return n
}

// scrollTo returns the window of lines that keeps line idx visible.
func scrollTo(lines []string, idx, h int) []string {
	if len(lines) <= h {
		return lines
	}
	start := max(0, idx-h+2)
	end := min(len(lines), start+h)
	return lines[start:end]
}

func sizeStr(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}

// ---- Cross-check ----

func (m Model) viewCrossCheck(h int) string {
	w := m.width
	var top []string
	state := sMuted.Render("○ OFF")
	if m.crossOn {
		state = sGreen.Bold(true).Render("● ON")
	}
	top = append(top, fmt.Sprintf("%s %s  %s", sSection.Render("CROSS-CHECK"), state, sMuted.Render("(x to toggle · off by default)")))
	codex := sGreen.Render(ShortPath(m.codexPath))
	if m.codexErr != "" {
		codex = sYellow.Render(m.codexErr)
	}
	hook := sGreen.Render("installed")
	if !m.hookOK {
		hook = sYellow.Render("not installed — run: overwatch hook install --print")
	}
	top = append(top, clip(sLabel.Render("codex ")+codex, w+40), clip(sLabel.Render("hook  ")+hook, w+40))
	if m.crossOn {
		access := "an empty temp dir"
		if m.o.Core.Cfg.CrossCheck.RepoAccess {
			access = "the session's project dir (read-only sandbox)"
		}
		top = append(top, sMuted.Render(clip(fmt.Sprintf("On each Stop, Claude's last reply (≤%d chars) + review prompt → codex exec, run in %s.", m.o.Core.Cfg.CrossCheck.MaxChars, access), w)))
	}
	topS := strings.Join(top, "\n")
	bodyH := max(3, h-lipgloss.Height(topS)-1)

	lw := m.listWidth()
	var list []string
	if len(m.reviews) == 0 {
		list = append(list, sMuted.Render("no reviews yet"))
	}
	for i, r := range m.reviews {
		badge := verdictBadge(r)
		line := fmt.Sprintf("%s %s", r.CreatedAt.Local().Format("01-02 15:04"), badge)
		if i == m.rSel {
			line = sSelected.Render(pad("▸ "+line, lw-1))
		} else {
			line = "  " + line
		}
		list = append(list, line)
		if ex := clip(oneLine(r.Excerpt), lw-5); ex != "" {
			list = append(list, sMuted.Render("    "+ex))
		}
	}
	left := lipgloss.NewStyle().Width(lw).MaxHeight(bodyH).Render(strings.Join(scrollTo(list, m.rSel*2+1, bodyH), "\n"))
	border := cFaint
	if m.rFocus {
		border = cRed
	}
	vh := m.rView.Height
	m.rView.Height = min(vh, bodyH)
	right := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderForeground(border).PaddingLeft(1).Render(m.rView.View())
	return topS + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func verdictBadge(r crosscheck.Review) string {
	switch {
	case r.Status == crosscheck.StatusDone && r.Verdict == "OK":
		return sGreen.Render("■ OK")
	case r.Status == crosscheck.StatusDone && r.Verdict == "CONCERNS":
		return sYellow.Render("■ CONCERNS")
	case r.Status == crosscheck.StatusDone && r.Verdict == "PROBLEMS":
		return sRed.Render("■ PROBLEMS")
	case r.Status == crosscheck.StatusDone:
		return sBlue.Render("■ REVIEWED")
	case r.Status == crosscheck.StatusError:
		return sRed.Render("✗ error")
	case r.Status == crosscheck.StatusSkipped:
		return sMuted.Render("– skipped")
	case r.Status == crosscheck.StatusRunning:
		return sPurple.Render("◌ reviewing…")
	default:
		return sMuted.Render("◌ queued")
	}
}

func (m *Model) loadReview() {
	if m.rSel >= len(m.reviews) {
		if m.rShown != "-" {
			m.rShown = "-"
			txt := "Reviews appear here when cross-check is on and Claude finishes a reply.\n\n" +
				"1. Install the hook:   overwatch hook install --print   (paste into settings.json)\n" +
				"2. Turn cross-check on: press x\n\n" +
				"Reviews are stored in " + ShortPath(paths.ReviewsDir())
			m.rView.SetContent(lipgloss.NewStyle().Width(m.rView.Width).Render(sMuted.Render(txt)))
		}
		return
	}
	r := m.reviews[m.rSel]
	key := r.ID + r.Status
	if m.rShown == key {
		return
	}
	m.rShown = key
	ww := max(20, m.rView.Width)
	var b []string
	b = append(b, verdictBadge(r)+"  "+sMuted.Render(r.CreatedAt.Local().Format("2006-01-02 15:04:05")))
	var parts []string
	if r.SessionID != "" {
		parts = append(parts, "session "+ShortID(r.SessionID))
	}
	if r.Cwd != "" {
		parts = append(parts, ShortPath(r.Cwd))
	}
	sent := fmt.Sprintf("sent %d chars", r.SentChars)
	if r.Truncated {
		sent += " (truncated)"
	}
	parts = append(parts, sent)
	meta := strings.Join(parts, " · ")
	if !r.FinishedAt.IsZero() && !r.StartedAt.IsZero() {
		meta += " · took " + r.FinishedAt.Sub(r.StartedAt).Round(time.Second).String()
	}
	if r.Usage != nil {
		meta += fmt.Sprintf(" · codex %s in / %s out", Tokens(r.Usage.Input), Tokens(r.Usage.Output))
	}
	b = append(b, sMuted.Render(meta), "")
	if r.Error != "" {
		b = append(b, sRed.Render(r.Error), "")
	}
	if r.Summary != "" {
		b = append(b, sSection.Render("SUMMARY"), r.Summary, "")
	}
	b = append(b, sSection.Render("REVIEWED REPLY (excerpt)"), sMuted.Render(r.Excerpt))
	m.rView.SetContent(lipgloss.NewStyle().Width(ww).Render(strings.Join(b, "\n")))
	m.rView.GotoTop()
}
