package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
)

const (
	// maxRecent is how many closed sessions the Sessions panel lists.
	maxRecent = 12
)

// refreshSessions re-reads every session's state and notes which ones
// need you or finished while another session was on screen.
func (m *Model) refreshSessions(now time.Time) {
	m.ovs = m.o.Core.Sessions(now, maxRecent)
	m.share5h = map[string]float64{}
	if five, _ := m.limits.Paces(now); five != nil && !five.Expired {
		for _, o := range m.ovs {
			if !o.Running {
				continue
			}
			if mine, all := m.o.Core.Agg.Share(o.ID, five.Start, now); all > 0 && mine > 0 {
				m.share5h[o.ID] = mine / all * five.Used
			}
		}
	}
	if m.unseen == nil {
		m.unseen = map[string]bool{}
	}
	next := map[string]app.State{}
	first := m.ovPrev == nil
	for _, o := range m.ovs {
		if !o.Running {
			delete(m.unseen, o.ID)
			continue
		}
		prev, had := m.ovPrev[o.ID]
		next[o.ID] = o.State
		if o.State != app.StateIdle {
			delete(m.unseen, o.ID)
		}
		// The session you were already looking at doesn't alert; one
		// Lookout has just switched to because it needs you does.
		if first || !had || o.ID == m.prevShown {
			continue
		}
		switch {
		case o.State == app.StateWaiting && prev != app.StateWaiting:
			m.queueSessionAlert(o, o.Label()+" needs you: "+o.Detail)
		case o.State == app.StateIdle && prev == app.StateWorking:
			m.unseen[o.ID] = true
			m.queueSessionAlert(o, o.Label()+" finished")
		}
	}
	m.ovLast, m.ovPrev = m.ovPrev, next
	if m.hasSess {
		delete(m.unseen, m.sess.ID)
	}
	m.keepSessionSel()
}

// noteAutoSwitch says so when Lookout has just moved to a session because
// it needs you, or back once you've answered it.
func (m *Model) noteAutoSwitch() {
	cur := m.shownID()
	if m.o.Core.Pinned != "" || m.ovLast == nil || cur == m.prevShown || m.prevShown == "" {
		return
	}
	if o, ok := m.overviewOf(cur); ok && o.NeedsYou() {
		m.setFlash("▲ showing "+o.Label()+": it needs you ("+o.Detail+") · back to the latest once you answer or turn to another session · P to stay", sYellow.Bold(true), 10*time.Second)
		return
	}
	if prev, ok := m.overviewOf(m.prevShown); ok && m.ovLast[prev.ID] == app.StateWaiting && !prev.NeedsYou() {
		if o, ok := m.overviewOf(cur); ok {
			m.setFlash("back to "+o.Label()+", the session you last prompted", sMuted, 4*time.Second)
		}
	}
}

// onScreen reports whether id is the session the panels show.
func (m Model) onScreen(id string) bool { return m.hasSess && m.sess.ID == id }

type sessionAlert struct{ key, text string }

func (m *Model) queueSessionAlert(o app.Overview, text string) {
	at := o.Since
	if at.IsZero() {
		at = m.now
	}
	// The key's second part is a time, so old keys expire with the rest.
	key := fmt.Sprintf("session:%d:%s:%d", at.Unix(), o.ID, o.State)
	m.sessAlerts = append(m.sessAlerts, sessionAlert{key: key, text: text})
}

// checkSessionAlerts shows queued session alerts: a footer message, and a
// bell and desktop notification as set in [alerts]. Each fires once, even
// with several dashboards open.
func (m *Model) checkSessionAlerts() tea.Cmd {
	queued := m.sessAlerts
	m.sessAlerts = nil
	cfg := m.o.Core.Cfg.Alerts
	if len(queued) == 0 || !cfg.Sessions {
		return nil
	}
	m.fired = limits.LoadFired(paths.AlertsFile())
	var show []string
	for _, a := range queued {
		if _, ok := m.fired[a.key]; !ok {
			m.fired[a.key] = m.now.Unix()
			show = append(show, a.text)
		}
	}
	if len(show) == 0 {
		return nil
	}
	_ = limits.SaveFired(paths.AlertsFile(), m.fired, m.now)
	text := strings.Join(show, " · ")
	m.setFlash("◆ "+text+" · ] next session · 0 all sessions", sYellow.Bold(true), 10*time.Second)
	return notify(text, cfg.Bell, cfg.Desktop)
}

// running returns the running sessions, in the order the strip shows them.
func (m Model) running() []app.Overview {
	var out []app.Overview
	for _, o := range m.ovs {
		if o.Running {
			out = append(out, o)
		}
	}
	return out
}

// shownID is the session on screen, or the open one Lookout waits on.
func (m Model) shownID() string {
	if m.hasSess {
		return m.sess.ID
	}
	if l, ok := m.o.Core.Waiting(); ok {
		return l.ID
	}
	return ""
}

// cycle steps through the strip's tabs: Auto (follow), then each running
// session (pin it). dir is 1 for the next, -1 for the previous.
func (m Model) cycle(dir int) Model {
	run := m.running()
	if len(run) == 0 {
		m.setFlash("no Claude Code session is running", sMuted, 3*time.Second)
		return m
	}
	i := 0 // Auto
	if p := m.o.Core.Pinned; p != "" {
		for j, o := range run {
			if o.ID == p {
				i = j + 1
			}
		}
	}
	n := len(run) + 1
	j := ((i+dir)%n + n) % n
	if j == 0 {
		return m.follow()
	}
	return m.pin(run[j-1])
}

// closed reports whether session o has ended, as far as Lookout can tell.
func (m Model) closed(o app.Overview) bool {
	return !o.Running && m.o.Core.LiveKnown()
}

// follow goes back to showing the session you last prompted (or one that
// needs you): the Auto tab.
func (m Model) follow() Model {
	m.o.Core.Pinned = ""
	m.setFlash("↻ Auto: following the session you last prompted", sMuted, 3*time.Second)
	m.refresh()
	return m
}

// pin keeps session o on screen until you pick Auto again.
func (m Model) pin(o app.Overview) Model {
	m.o.Core.Pinned = o.ID
	if !m.closed(o) {
		m.setFlash("◆ pinned "+o.Label()+" · ↻ Auto (or P) to follow the latest again", sGreen, 4*time.Second)
	} else {
		m.setFlash("◷ viewing closed session "+o.Label()+" · ↻ Auto (or P) to go back", sMuted, 4*time.Second)
	}
	delete(m.unseen, o.ID)
	m.refresh()
	return m
}

// stateGlyph is a one-cell mark for what a session is doing.
func (m Model) stateGlyph(o app.Overview) string {
	switch o.State {
	case app.StateWorking:
		return sGreen.Render("●")
	case app.StateWaiting:
		return sYellow.Bold(true).Render("▲")
	case app.StateStarting:
		return sMuted.Render("◌")
	case app.StateIdle:
		if m.unseen[o.ID] {
			return sGreen.Bold(true).Render("✓")
		}
		return sMuted.Render("○")
	}
	return sMuted.Render("·")
}

// stateColor is the color of a session's state mark.
func (m Model) stateColor(o app.Overview) lipgloss.TerminalColor {
	switch {
	case o.State == app.StateWorking, o.State == app.StateIdle && m.unseen[o.ID]:
		return cGreen
	case o.State == app.StateWaiting:
		return cYellow
	}
	return cMuted
}

// stateText says what a session is doing, e.g. "working · Bash: go test".
func (m Model) stateText(o app.Overview) (string, lipgloss.Style) {
	since := ""
	if !o.Since.IsZero() {
		since = " " + strings.TrimSuffix(Ago(o.Since, m.now), " ago")
	}
	switch o.State {
	case app.StateWorking:
		if o.Detail != "" {
			return "working · " + o.Detail, sGreen
		}
		return "working" + since, sGreen
	case app.StateWaiting:
		return "needs you · " + o.Detail, sYellow.Bold(true)
	case app.StateStarting:
		return "open · nothing sent yet", sMuted
	case app.StateIdle:
		if m.unseen[o.ID] {
			return "finished" + since + " ago · not seen yet", sGreen.Bold(true)
		}
		return "idle" + since, sMuted
	}
	return "closed", sMuted
}

// ---- Header strip ----

// The line under the tabs always names the session on screen, drawn like
// the active tab. When other sessions are running it lists them too, each
// with its state; with none, it adds the session's folder and state.

// stripItem is one session in the header's session strip.
type stripItem struct {
	id, text string
	pre      string // drawn before it: spacing, or a divider
	x, w     int    // where text is drawn on the line
}

// stripName draws one session in the strip.
func (m Model) stripName(o app.Overview, current bool) string {
	label := clip(o.Label(), 26)
	if current {
		glyph := plainText(m.stateGlyph(o))
		if m.o.Core.Pinned != "" && m.closed(o) {
			// A closed session you opened: ◷, no state mark.
			return sChip.Foreground(cMuted).Bold(true).Render(" ◷") +
				sChip.Foreground(cText).Bold(true).Render(" "+label+" ")
		}
		if m.o.Core.Pinned != "" {
			// Pinned: a bold button with ◆.
			return sChip.Foreground(m.stateColor(o)).Bold(true).Render(" "+glyph) +
				sChip.Foreground(cYellow).Bold(true).Render(" ◆") +
				sChip.Foreground(cText).Bold(true).Render(" "+label+" ")
		}
		// The session Auto is following: shaded, with Auto's ↻ in blue.
		return sChip.Foreground(m.stateColor(o)).Render(" "+glyph) +
			sChip.Foreground(cBlue).Bold(true).Render(" ↻") +
			sChip.Foreground(cText).Render(" "+label+" ")
	}
	if o.ID == m.hoverStrip {
		glyph := plainText(m.stateGlyph(o))
		return sChip.Foreground(m.stateColor(o)).Render(glyph) + sChip.Foreground(cText).Render(" "+label)
	}
	name := sMuted.Render(label)
	switch {
	case o.State == app.StateWaiting:
		name = sYellow.Bold(true).Render(label)
	case m.unseen[o.ID]:
		name = sGreen.Render(label)
	}
	return m.stateGlyph(o) + " " + name
}

// shownOverview is the session on screen, as the strip shows it.
func (m Model) shownOverview() (app.Overview, bool) {
	if o, ok := m.overviewOf(m.shownID()); ok {
		return o, true
	}
	if m.hasSess {
		// Older than the sessions Lookout lists.
		return app.Overview{Session: m.sess, Since: m.sess.LastActivity}, true
	}
	return app.Overview{}, false
}

// Special tabs in the strip, alongside session IDs.
const (
	stripAuto = "\x00auto" // follow the session you last prompted
	stripNew  = "\x00new"  // open a new Claude Code in tmux
)

// autoChip is the strip's first tab: lit while Lookout follows the
// session you last prompted, grey while a session is pinned.
func (m Model) autoChip() string {
	const text = " ↻ Auto "
	switch {
	case m.o.Core.Pinned == "":
		return lipgloss.NewStyle().Background(cAccent).Foreground(cOnAccent).Bold(true).Render(text)
	case m.hoverStrip == stripAuto:
		return sChip.Foreground(cText).Render(text)
	}
	return sMuted.Render(text)
}

// strip lays out the line under the tabs: Auto, then the session on
// screen and the other running sessions. more counts running sessions
// left out for lack of room; detail is drawn after the items when only
// the session on screen is listed.
func (m Model) strip() (items []stripItem, more int, detail string) {
	cur, hasCur := m.shownOverview()
	run := m.running()
	if !hasCur && len(run) == 0 {
		return nil, 0, ""
	}
	curRunning := false
	for _, o := range run {
		if o.ID == cur.ID {
			curRunning = true
		}
	}
	limit := m.ruleWidth()
	x := 2 // after "━ "
	addText := func(id, text, pre string) {
		x += lipgloss.Width(pre)
		w := lipgloss.Width(text)
		items = append(items, stripItem{id: id, text: text, pre: pre, x: x, w: w})
		x += w
	}
	add := func(o app.Overview, pre string) {
		addText(o.ID, m.stripName(o, hasCur && o.ID == cur.ID), pre)
	}
	addText(stripAuto, m.autoChip(), "")
	// Room kept for the session on screen while the others are laid out,
	// and for "  +N ━" at the end.
	reserve := 0
	if hasCur && curRunning {
		reserve = lipgloss.Width(m.stripName(cur, true)) + 2
	}
	if hasCur && !curRunning {
		add(cur, " ")
	}
	for i, o := range run {
		pre := "  "
		if i == 0 {
			pre = " "
			if hasCur && !curRunning {
				pre = sFaint.Render("  │  ")
			}
		}
		if o.ID == cur.ID {
			add(o, pre)
			reserve = 0
			continue
		}
		if x+lipgloss.Width(pre)+lipgloss.Width(m.stripName(o, false))+reserve+7 > limit {
			more++
			continue
		}
		add(o, pre)
	}
	if len(items) == 2 && hasCur {
		state, st := m.stateText(cur)
		if !cur.Running {
			state = "closed"
			if !cur.LastActivity.IsZero() {
				state += " · last active " + Ago(cur.LastActivity, m.now)
			}
		}
		parts := []string{}
		if cur.Cwd != "" {
			parts = append(parts, sMuted.Render(ShortPath(cur.Cwd)))
		}
		// Only the state itself in color: "working", the rest grey.
		head, rest, _ := strings.Cut(state, " · ")
		s := st.Render(head)
		if rest != "" {
			s += sMuted.Render(" · " + rest)
		}
		parts = append(parts, s)
		detail = clip("  "+strings.Join(parts, sMuted.Render(" · ")), max(0, limit-x-4))
	}
	return items, more, detail
}

// newButton is the "+ claude" button at the right end of the strip, shown
// when Lookout runs in tmux; "" otherwise.
func (m Model) newButton() string {
	if !canNewClaude() {
		return ""
	}
	const text = " + claude "
	if m.hoverStrip == stripNew {
		return lipgloss.NewStyle().Background(cAccent).Foreground(cOnAccent).Bold(true).Render(text)
	}
	return sChip.Foreground(cText).Render(text)
}

// ruleWidth is the room on the strip's line left of the "+ claude" button.
func (m Model) ruleWidth() int {
	if b := m.newButton(); b != "" {
		return max(0, m.width-lipgloss.Width(b)-1)
	}
	return m.width
}

// viewRule is the line under the tabs: the session strip, or a plain rule
// when there is no session yet.
func (m Model) viewRule() string {
	line := m.ruleLine()
	if b := m.newButton(); b != "" {
		line += strings.Repeat(" ", max(0, m.ruleWidth()-lipgloss.Width(line))) + " " + b
	}
	return line
}

// ruleLine is the strip without the "+ claude" button, ruleWidth wide.
func (m Model) ruleLine() string {
	width := m.ruleWidth()
	items, more, detail := m.strip()
	if len(items) == 0 {
		return sMuted.Render(strings.Repeat("━", max(0, width)))
	}
	line := sMuted.Render("━ ")
	for _, it := range items {
		line += it.pre + it.text
	}
	line += detail
	if more > 0 {
		line += sMuted.Render(fmt.Sprintf("  +%d", more))
	}
	// With others to switch to, say how, at the right end of the line.
	hint := ""
	if len(m.running()) >= 2 {
		hint = footerKeys("[ ] switch · 0 all")
		if width-lipgloss.Width(line)-lipgloss.Width(hint)-6 < 0 {
			hint = ""
		}
	}
	if rest := width - lipgloss.Width(line) - 1 - lipgloss.Width(hint); rest > 0 {
		fill := strings.Repeat("━", rest)
		if hint != "" {
			fill = strings.Repeat("━", max(0, rest-3))
			line += " " + sMuted.Render(fill) + " " + hint + " " + sMuted.Render("━")
			return line
		}
		line += " " + sMuted.Render(fill)
	}
	return line
}

// roomy reports whether the window is tall enough for blank lines around
// the session strip.
func (m Model) roomy() bool { return m.height >= 30 }

// stripRow is the screen row of the session strip.
func (m Model) stripRow() int {
	if m.roomy() {
		return 2
	}
	return 1
}

// stripAt is the session under column x of the strip, or "".
func (m Model) stripAt(x int) string {
	if b := m.newButton(); b != "" && x >= m.width-lipgloss.Width(b) {
		return stripNew
	}
	items, _, _ := m.strip()
	for _, it := range items {
		if x >= it.x && x < it.x+it.w {
			return it.id
		}
	}
	return ""
}

// overviewOf finds a session in the list.
func (m Model) overviewOf(id string) (app.Overview, bool) {
	for _, o := range m.ovs {
		if o.ID == id {
			return o, true
		}
	}
	return app.Overview{}, false
}

// openSessions shows the Sessions panel with the session on screen
// selected.
func (m Model) openSessions() Model {
	m.tab = tabSessions
	if m.loaded {
		m.refresh()
	}
	cur := m.shownID()
	for i, o := range m.ovs {
		if o.ID == cur && i < m.listedSessions() {
			m.svSel, m.svID = i, o.ID
		}
	}
	return m
}

// followMode says which session Lookout shows and why.
func (m Model) followMode() string {
	if m.o.Core.Pinned != "" {
		return "Pinned to session " + ShortID(m.o.Core.Pinned)
	}
	mode := "Following the session you last prompted (or one that needs you)"
	if m.o.Core.CwdFilter != "" {
		mode += " in " + ShortPath(m.o.Core.CwdFilter)
	}
	return mode
}

// ctxText is how full a session's context window is, e.g. "ctx 42%".
func ctxText(o app.Overview) string {
	if o.Context.Percent <= 0 {
		return ""
	}
	return fmt.Sprintf("ctx %.0f%%", o.Context.Percent)
}

// togglePin keeps the session on screen even when you prompt another one,
// or, when already pinned, goes back to following the latest.
func (m Model) togglePin() Model {
	if m.o.Core.Pinned != "" {
		return m.follow()
	}
	cur, ok := m.o.Core.Current()
	if !ok {
		m.setFlash("no session to pin yet", sMuted, 3*time.Second)
		return m
	}
	if o, ok := m.overviewOf(cur.ID); ok {
		return m.pin(o)
	}
	return m.pin(app.Overview{Session: cur})
}

// ---- Going to a session's tmux pane ----

type paneMsg struct {
	label string
	err   error
}

// gotoPane switches tmux to the pane the session runs in. It only moves
// tmux's focus; it never types into Claude Code.
func (m Model) gotoPane(o app.Overview) tea.Cmd {
	label := o.Label()
	fail := func(err error) tea.Cmd { return func() tea.Msg { return paneMsg{label: label, err: err} } }
	switch {
	case !o.Running:
		return fail(fmt.Errorf("it isn't running"))
	case o.Tmux == "":
		return fail(fmt.Errorf("it isn't running in tmux"))
	case os.Getenv("TMUX") == "":
		return fail(fmt.Errorf("Lookout isn't running in tmux (the session is in pane %s)", o.Tmux))
	}
	args := tmuxGotoArgs(o.Tmux)
	return func() tea.Msg {
		p, err := exec.LookPath("tmux")
		if err == nil {
			var b []byte
			if b, err = exec.Command(p, args...).CombinedOutput(); err != nil && len(b) > 0 {
				err = fmt.Errorf("%s", strings.TrimSpace(string(b)))
			}
		}
		return paneMsg{label: label, err: err}
	}
}

// canGoTo reports whether g can switch to the session's pane: it runs in
// tmux, and so does Lookout.
func canGoTo(o app.Overview) bool {
	return o.Running && o.Tmux != "" && os.Getenv("TMUX") != ""
}

// tmuxGotoArgs turns a pane reference like "main:@2.%7" (session, window
// ID, pane ID) into one tmux command line that shows that pane.
func tmuxGotoArgs(ref string) []string {
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return []string{"select-pane", "-t", ref}
	}
	sess, rest := ref[:i], ref[i+1:]
	win, pane, ok := strings.Cut(rest, ".")
	if !ok {
		return []string{"switch-client", "-t", sess, ";", "select-window", "-t", rest}
	}
	return []string{"switch-client", "-t", sess, ";", "select-window", "-t", win, ";", "select-pane", "-t", pane}
}

// ---- Sessions panel (0) ----

// sessionLines draws the Sessions panel: running sessions first, two lines
// each; then recent closed ones, one quiet line each. at maps each line to
// its session's index in m.ovs, or -1; sel is the first line of the
// selected session.
func (m Model) sessionLines(w int) (lines []string, at []int, sel int) {
	run, needs := 0, 0
	for _, o := range m.ovs {
		if o.Running {
			run++
			if o.NeedsYou() {
				needs++
			}
		}
	}
	summary := fmt.Sprintf("%d running", run)
	switch {
	case needs == 1:
		summary += " · 1 needs you"
	case needs > 1:
		summary += fmt.Sprintf(" · %d need you", needs)
	}
	lines = []string{section("Sessions", w), sMuted.Render(clip(summary+" · "+m.followMode(), w)), ""}
	at = []int{-1, -1, -1}
	if len(m.ovs) == 0 {
		return append(lines, sMuted.Render("No Claude Code sessions found yet.")), append(at, -1), 0
	}
	if run == 0 {
		lines = append(lines, sMuted.Render("No Claude Code session is running."))
		at = append(at, -1)
	}
	cur := m.shownID()
	recent := 0
	for i, o := range m.ovs {
		if !o.Running {
			if recent >= maxRecent {
				break
			}
			if recent == 0 {
				lines = append(lines, "", section("Recent", w))
				at = append(at, -1, -1)
			}
			recent++
		}
		if i == m.svSel {
			sel = len(lines)
		}
		mark := "  "
		if o.ID == cur {
			mark = sBlue.Bold(true).Render("▶ ")
		}
		var rows []string
		if o.Running {
			rows = m.runningRows(o, mark, w)
		} else {
			rows = m.recentRows(o, mark, w, i == m.svSel)
		}
		if i == m.svSel {
			rows[0] = sSelected.Render(pad(clip(plainText(rows[0]), w-1), w-1))
		}
		for _, r := range rows {
			lines = append(lines, r)
			at = append(at, i)
		}
	}
	return lines, at, sel
}

// runningRows draws a running session: its name, model, context, share of
// the 5-hour limit and state; then your last prompt and where it runs.
func (m Model) runningRows(o app.Overview, mark string, w int) []string {
	share := ""
	if p, ok := m.share5h[o.ID]; ok {
		share = fmt.Sprintf("5h %.0f%%", p)
	}
	ctxStyle := sMuted
	switch {
	case o.Context.Level >= 2:
		ctxStyle = sRed
	case o.Context.Level == 1:
		ctxStyle = sYellow
	}
	// Narrow panes drop the model, the limit share and the time first.
	nameW := 30
	switch {
	case w < 90:
		nameW = 14
	case w < 110:
		nameW = 20
	}
	cols := mark + m.stateGlyph(o) + " " + sBold.Render(pad(clip(o.Label(), nameW), nameW)) + "  "
	if w >= 90 {
		model := ""
		if o.Model != "" {
			model = ShortModel(o.Model)
		}
		cols += sPurple.Render(pad(model, 11)) + " "
	}
	cols += ctxStyle.Render(lpad(ctxText(o), 7)) + "  "
	if w >= 100 {
		cols += sMuted.Render(lpad(share, 6)) + "  "
	}
	ago := ""
	if !o.LastActivity.IsZero() && w >= 70 {
		ago = Ago(o.LastActivity, m.now)
	}
	state, st := m.stateText(o)
	head, rest, _ := strings.Cut(state, " · ")
	room := max(0, w-1-lipgloss.Width(cols)-len(ago)-1)
	text := st.Render(clip(head, room))
	if rest != "" && room > len(head)+3 {
		text += sMuted.Render(clip(" · "+rest, room-len(head)))
	}
	line1 := cols + text
	line1 += strings.Repeat(" ", max(1, w-1-lipgloss.Width(line1)-len(ago))) + sMuted.Render(ago)

	var tail []string
	if c := costText(o); c != "" {
		tail = append(tail, c)
	}
	if n := o.Brief.Files; n > 0 {
		tail = append(tail, plural(n, "file")+" edited")
	}
	tail = append(tail, ShortPath(o.Cwd))
	if o.Tmux != "" {
		tail = append(tail, "tmux "+o.Tmux)
	}
	tail = append(tail, ShortID(o.ID))
	t := strings.Join(tail, " · ")
	line2 := "     "
	if p := o.Brief.Prompt; p != "" {
		room := max(16, w-10-min(lipgloss.Width(t), w/3))
		line2 += sText.Render("“"+clip(p, room)+"”") + sMuted.Render(" · ")
	}
	return []string{line1, clip(line2+sMuted.Render(t), w-1)}
}

// recentRows draws a closed session on one quiet line: when it was last
// active, what it was about (your first prompt), then its model, size, cost
// and context. The selected one gets a second line: your last prompt, how
// long it ran, its tokens and its folder.
func (m Model) recentRows(o app.Overview, mark string, w int, selected bool) []string {
	ago := strings.TrimSuffix(Ago(o.LastActivity, m.now), " ago")
	title := o.Title
	if title == "" {
		title = o.Brief.First
	}
	if title == "" {
		title = o.Brief.Prompt
	}
	if title == "" {
		title = o.Label()
	}
	left := mark + sFaint.Render("·") + " " + sMuted.Render(lpad(ago, 4)) + "  "
	// Columns on the right, lined up from row to row; the least useful
	// are dropped when the title would get too short.
	cols := []string{pad(ShortModel(o.Model), 10), lpad(plural(o.Brief.Prompts, "prompt"), 11),
		lpad(fileCount(o.Brief.Files), 8), lpad(ctxText(o), 7)}
	if o.Model == "" {
		cols[0] = pad("", 10)
	}
	if m.anyCost() {
		cols = append([]string{lpad(costText(o), 8)}, cols...)
	}
	right := ""
	for len(cols) > 0 {
		right = strings.Join(cols, "  ")
		if w-1-lipgloss.Width(left)-lipgloss.Width(right)-2 >= 24 {
			break
		}
		cols = cols[:len(cols)-1]
		right = ""
	}
	room := max(10, w-1-lipgloss.Width(left)-lipgloss.Width(right)-2)
	line := left + sText.Render(clip(title, room))
	line += strings.Repeat(" ", max(2, w-1-lipgloss.Width(line)-lipgloss.Width(right))) + sMuted.Render(right)
	if !selected {
		return []string{line}
	}
	var more []string
	if p := o.Brief.Prompt; p != "" && p != title {
		more = append(more, "last: “"+p+"”")
	}
	if d := o.LastActivity.Sub(o.FirstSeen); !o.FirstSeen.IsZero() && d > 0 {
		more = append(more, "ran "+Dur(d))
	}
	if o.Tokens > 0 {
		more = append(more, Tokens(o.Tokens)+" tokens")
	}
	more = append(more, ShortPath(o.Cwd), ShortID(o.ID))
	return []string{line, sMuted.Render(clip("          "+strings.Join(more, " · "), w-1))}
}

// costText is a pay-as-you-go session's cost as Claude Code reported it,
// e.g. "$4.20"; "" for subscription sessions, which have no cost of their own.
func costText(o app.Overview) string {
	switch {
	case o.Cost < 0.005:
		return ""
	case o.Cost >= 100:
		return fmt.Sprintf("$%.0f", o.Cost)
	}
	return fmt.Sprintf("$%.2f", o.Cost)
}

// anyCost reports whether any listed session has a cost to show.
func (m Model) anyCost() bool {
	for _, o := range m.ovs {
		if o.Cost >= 0.005 {
			return true
		}
	}
	return false
}

// fileCount is "3 files", or "" for none.
func fileCount(n int) string {
	if n == 0 {
		return ""
	}
	return plural(n, "file")
}

func (m Model) viewSessions(h int) string {
	lines, _, sel := m.sessionLines(m.width)
	return strings.Join(scrollTo(lines, sel+1, h), "\n")
}

// sessionRowAt is the session on body line y of a panel h lines tall, or
// -1. It mirrors scrollTo in viewSessions.
func (m Model) sessionRowAt(y, h int) int {
	lines, at, sel := m.sessionLines(m.width)
	start := 0
	if len(lines) > h {
		start = max(0, sel+1-h+2)
	}
	if i := start + y; i >= 0 && i < len(at) {
		return at[i]
	}
	return -1
}

// listedSessions is how many sessions the panel lists.
func (m Model) listedSessions() int {
	n, recent := 0, 0
	for _, o := range m.ovs {
		if !o.Running {
			if recent >= maxRecent {
				break
			}
			recent++
		}
		n++
	}
	return n
}

func (m Model) sessionsKey(key string) (tea.Model, tea.Cmd) {
	n := m.listedSessions()
	switch key {
	case "up", "k":
		if m.svSel > 0 {
			m.svSel--
		}
	case "down", "j":
		if m.svSel < n-1 {
			m.svSel++
		}
	case "home":
		m.svSel = 0
	case "end", "G":
		m.svSel = max(0, n-1)
	case "g":
		if m.svSel < n {
			return m, m.gotoPane(m.ovs[m.svSel])
		}
	case "y":
		if m.svSel < n {
			return m, copyText(m.ovs[m.svSel].ID)
		}
	case "c":
		if m.svSel < n {
			return m, copyText("claude --resume " + m.ovs[m.svSel].ID)
		}
	case "a":
		m = m.follow()
		m.tab = m.backTab()
		m.refresh()
	case "esc", "left", "h":
		m.tab = m.backTab()
		m.refresh()
	case "enter", "right", "l":
		if m.svSel < n {
			o := m.ovs[m.svSel]
			// Its own tab, pinned; Auto keeps following and is a click away.
			m = m.pin(o)
			m.tab = tabUsage
			m.refresh()
		}
	}
	if m.svSel < len(m.ovs) {
		m.svID = m.ovs[m.svSel].ID
	}
	return m, nil
}

// backTab is where leaving Sessions goes: the panel you came from, or
// Activity, which shows what a session did, when that panel isn't about
// one session.
func (m Model) backTab() int {
	if needsSession(m.prevTab) {
		return m.prevTab
	}
	return tabActivity
}

// keepSessionSel keeps the same session selected as the list changes.
func (m *Model) keepSessionSel() {
	if m.svID != "" {
		for i, o := range m.ovs {
			if o.ID == m.svID {
				m.svSel = i
				return
			}
		}
	}
	if n := m.listedSessions(); m.svSel >= n {
		m.svSel = max(0, n-1)
	}
}

// needsSession reports whether a panel has nothing to show without a
// session. Usage, Skills, Config, the Cheatsheet and Sessions still work
// without one.
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
	if l, ok := m.o.Core.Waiting(); ok {
		out := []string{
			"",
			"  " + sBold.Render(clip("New Claude Code session "+ShortID(l.ID)+" in "+ShortPath(l.Cwd), w)),
			"",
			"  " + sMuted.Render(clip("Waiting for its first message. It shows up here once you send one.", w)),
			"  " + sMuted.Render(clip("Press s or 0 to choose another session.", w)),
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
		out = append(out, "  "+sMuted.Render(clip("Press s or 0 to choose another session.", w)))
	}
	return strings.Join(out, "\n")
}
