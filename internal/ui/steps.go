package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/paths"
)

// Opened steps show at most this many lines of input and of output.
const (
	stepInputLines  = 30
	stepOutputLines = 40
	maxStepDetails  = 400 // loaded details kept before the cache is cleared
)

// stepsMsg carries step details read from the transcript.
type stepsMsg struct {
	session string
	details map[string]activity.StepDetail
}

// loadStepsCmd reads the given steps' input and output in the background.
func (m Model) loadStepsCmd(ids []string) tea.Cmd {
	if len(ids) == 0 || !m.hasSess {
		return nil
	}
	files := activity.SessionFiles(paths.ClaudeProjectsDir(), m.sess.Project, m.sess.ID)
	session := m.sess.ID
	return func() tea.Msg {
		return stepsMsg{session: session, details: activity.LoadSteps(files, ids)}
	}
}

func (m *Model) applySteps(msg stepsMsg) {
	if !m.hasSess || msg.session != m.sess.ID {
		return
	}
	if m.aDetail == nil || len(m.aDetail) > maxStepDetails {
		m.aDetail = map[string]activity.StepDetail{}
	}
	for id, d := range msg.details {
		m.aDetail[id] = d
	}
	m.aRev++
	m.loadTurn()
	m.loadTool()
	m.diffBusy = false // the Changes panel may have asked for an action
	if it, ok := m.selectedChange(); ok && it.A != nil {
		m.showAction(it)
	}
}

// stepIDs are the tool IDs of the selected turn's steps.
func (m Model) stepIDs() []string {
	if m.aSel >= len(m.act.Turns) {
		return nil
	}
	var ids []string
	for _, c := range m.act.Turns[m.aSel].Calls {
		if c.ID != "" && c.Name != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// toggleStep opens or closes one step, loading its details when needed.
// Details of an unfinished step are loaded again each time it is opened.
func (m Model) toggleStep(id string) (tea.Model, tea.Cmd) {
	if m.aOpen == nil {
		m.aOpen = map[string]bool{}
	}
	m.aOpen[id] = !m.aOpen[id]
	if !m.aOpen[id] {
		delete(m.aOpen, id)
	}
	m.aRev++
	m.loadTurn()
	if m.aOpen[id] && !m.aDetail[id].Done {
		return m, m.loadStepsCmd([]string{id})
	}
	return m, nil
}

// toggleAllSteps opens every step of the selected turn, or closes them all
// when they are all open already.
func (m Model) toggleAllSteps() (tea.Model, tea.Cmd) {
	ids := m.stepIDs()
	if len(ids) == 0 {
		return m, nil
	}
	allOpen := true
	for _, id := range ids {
		if !m.aOpen[id] {
			allOpen = false
		}
	}
	if m.aOpen == nil {
		m.aOpen = map[string]bool{}
	}
	var load []string
	for _, id := range ids {
		if allOpen {
			delete(m.aOpen, id)
			continue
		}
		m.aOpen[id] = true
		if !m.aDetail[id].Done {
			load = append(load, id)
		}
	}
	m.aRev++
	m.loadTurn()
	return m, m.loadStepsCmd(load)
}

// stepLines is an opened step's input and output, ready to show.
func stepLines(c activity.Call, d activity.StepDetail, loaded bool, w int) []string {
	if !loaded {
		return []string{sMuted.Render("loading…")}
	}
	var out []string
	add := func(st func(...string) string, text string, limit int) {
		lines := strings.Split(cleanText(strings.TrimRight(text, "\n")), "\n")
		more := len(lines) - limit
		if more > 0 {
			lines = lines[:limit]
		}
		for _, l := range lines {
			out = append(out, st(clip(l, w)))
		}
		if more > 0 {
			out = append(out, sMuted.Render(fmt.Sprintf("… %d more lines", more)))
		}
	}
	str := func(k string) string { v, _ := d.Input[k].(string); return v }

	switch c.Name {
	case "Bash":
		add(sText.Render, "$ "+str("command"), stepInputLines)
	case "Edit":
		add(sRed.Render, prefixLines("- ", str("old_string")), stepInputLines/2)
		add(sGreen.Render, prefixLines("+ ", str("new_string")), stepInputLines/2)
	case "Write":
		add(sText.Render, str("content"), stepInputLines)
	default:
		keys := make([]string, 0, len(d.Input))
		for k := range d.Input {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, ok := d.Input[k].(string)
			if !ok {
				b, _ := json.Marshal(d.Input[k])
				v = string(b)
			}
			line, _, _ := strings.Cut(v, "\n")
			out = append(out, sMuted.Render(k+": ")+sText.Render(clip(cleanText(line), max(5, w-len(k)-2))))
		}
	}

	switch {
	case !d.Done:
		out = append(out, sMuted.Render("(still running, or no output yet)"))
	case strings.TrimSpace(d.Output) == "":
		out = append(out, sMuted.Render("(no output)"))
	case d.Error:
		out = append(out, sRed.Render("error:"))
		add(sRed.Render, d.Output, stepOutputLines)
	default:
		out = append(out, sMuted.Render("output:"))
		add(sMuted.Render, d.Output, stepOutputLines)
	}
	return out
}

func prefixLines(p, s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = p + lines[i]
	}
	return strings.Join(lines, "\n")
}

// cleanText makes tool output safe to draw: tabs become spaces, colour codes
// and other control characters are dropped.
func cleanText(s string) string {
	s = plainText(strings.ReplaceAll(s, "\t", "    "))
	return strings.Map(func(r rune) rune {
		if r == '\n' || r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
			return r
		}
		return -1
	}, s)
}
