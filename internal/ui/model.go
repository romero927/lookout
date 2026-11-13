// Package ui is Lookout's Bubble Tea interface.
package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/configfiles"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/skills"
	"github.com/romero927/lookout/internal/store"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/usage"
)

// Messages sent into the program from outside.
type (
	// DataMsg: new transcript records arrived.
	DataMsg struct{}
	// LoadedMsg: the initial scan finished.
	LoadedMsg struct{ Records int }
)

type (
	tickMsg       time.Time
	editorDoneMsg struct {
		path, backup string
		err          error
	}
)

// UsageRow pairs usage with a message count (shared with `lookout stats`).
type UsageRow struct {
	U transcript.Usage
	N int
}

// Panels, in order: watching the session first (usage, what Claude is
// doing, what changed, what's in context), then the model, the tools
// Claude used, skills and configuration, and the cheatsheet last.
const (
	// tabSessions lists every session. It comes first, ahead of the
	// panels about one session, and is key 0.
	tabSessions = iota
	tabUsage
	tabActivity
	tabChanges
	tabContext
	tabModels
	tabTools
	tabSkills
	tabFiles
	tabCheatsheet
)

var tabNames = []string{"Sessions", "Usage", "Activity", "Changes", "Context", "Models", "Tools", "Skills", "Config", "Cheatsheet"}

// tabKey is the key that opens tab i.
func tabKey(i int) string { return fmt.Sprint(i) }

// tabFor is the tab key k opens ("0"-"9"), or -1.
func tabFor(k string) int {
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		return int(k[0] - '0')
	}
	return -1
}

// Options configure the model.
type Options struct {
	Core      *app.Core
	Store     *store.Store
	StoreErr  error
	ConfigErr error
	Version   string
	Exe       string
	// Session is a session ID (or the start of one) to pin at startup.
	Session string
	// Tab is the key of the panel to open first ("0"-"9"); Usage when "".
	Tab string
}

// Model is the root Bubble Tea model.
type Model struct {
	o      Options
	width  int
	height int
	tab    int
	help   bool
	sel    selection // mouse drag in progress
	// limitFlashUntil is when the limit warning on show expires, so a
	// session alert in the same moment joins it instead of replacing it.
	limitFlashUntil time.Time
	// headerH is the header's height when the readers were last sized.
	headerH int
	// hdrPress is a press above the panel body (tabs, session strip): its
	// release is a click, never a text selection.
	hdrPress bool
	loaded   bool
	now      time.Time

	// Usage
	snap    usage.Snapshot
	sess    usage.Session
	hasSess bool
	health  transcript.Health

	// Models
	sugg       strategy.Suggestion
	pending    *strategy.Suggestion // a new suggestion still settling
	stab       *strategy.Stabilizer
	smooth     int // followed suggestions that went smoothly
	sstate     strategy.SessionState
	logRows    []store.Row
	resolved   int
	followed   int
	lastLogAt  time.Time
	shownKey   string // the suggestion on screen, and since when
	shownSince time.Time

	// Changes and Activity (from the activity engine)
	act       activity.Session
	cSel      int
	cFocus    bool
	cView     viewport.Model
	diffPath  string // path whose diff is in cView
	diffText  string
	diffBusy  bool
	diffAt    time.Time
	gone      map[string]bool // edited files no longer on disk
	work      []activity.WorkChange
	committed []activity.WorkChange
	workTops  []string
	workDir   string
	workBusy  bool
	workAt    time.Time
	aSel      int
	aFocus    bool
	aView     viewport.Model
	aShown    string
	aOpen     map[string]bool                // steps opened to show input and output
	aDetail   map[string]activity.StepDetail // loaded step details, by tool ID
	actAsked  string                         // the action whose details Changes last asked for
	aStepAt   []string                       // tool ID per line of the shown turn
	aRev      int                            // bumped when steps open or load
	aFilter   string                         // Activity search text
	aTyping   bool                           // typing the search

	// Estimated share of each limit window used by the focused session:
	// its fraction of all Claude Code usage on this computer in the window
	// (weighted by model price), or -1 when unknown.
	shareFive, shareWeek float64

	ctxView activity.ContextView
	xSel    int    // selected context item
	xAll    bool   // list every item, not just the largest
	xFocus  bool   // showing one item's details
	kSel    int    // selected cheatsheet command
	kCmd    string // its text: suggestions come and go above it, so find it again

	// Tools
	tools      []toolStat
	tlSel      int
	tlName     string // the selected tool, kept as counts reorder the list
	tlFocus    bool
	tlView     viewport.Model
	tlShown    string
	tlOpen     map[string]bool // calls opened to show input and output
	tlCur      int             // the call under the cursor in the reader
	tlCalls    []toolRef       // calls shown in the reader, in order
	tlAt       []int           // call index per reader line, -1 for none
	tlRev      int             // bumped when calls open
	tlFailOnly bool            // show failed calls only

	// Every session (running ones first) and what each is doing.
	ovs        []app.Overview
	ovPrev     map[string]app.State // each running session's state at the last refresh
	ovLast     map[string]app.State // ovPrev as it was before that refresh
	share5h    map[string]float64   // each running session's estimated share of the 5-hour limit, in points
	unseen     map[string]bool      // finished while another session was on screen
	sessAlerts []sessionAlert       // waiting for the next tick
	svSel      int                  // selected row in the Sessions panel
	svID       string               // the selected session, kept as the list reorders
	prevTab    int                  // the panel on screen before Sessions, to return to
	prevShown  string               // the session on screen before the last refresh
	scroll     map[int]int          // how far Usage and Models are scrolled down, in lines

	vc       *viewCache // the last screen drawn, until the next change
	overflow bool       // while drawing: the page on screen is taller than the window

	// Under the mouse pointer, to highlight what a click would pick.
	hoverTab   int    // a tab, or -1
	hoverStrip string // a session in the strip under the tabs, or ""

	// Skills
	skills []skills.Item
	// skillsFor is the project folder skills were last scanned for, so
	// they are rescanned when the session on screen moves to another.
	skillsFor string
	sSel      int
	sFocus    bool
	sView     viewport.Model
	sShown    string

	// Files
	files    []configfiles.File
	gSel     int
	gFocus   bool // viewer has focus
	gView    viewport.Model
	gLoaded  string // path currently in the viewer
	gReadErr string

	// Limits (from the status line)
	limits     limits.State
	limitsMod  time.Time
	limitsPath string
	// acct is the account's limits: m.limits, or, until the session's
	// file has them, the newest file that does.
	acct         limits.State
	acctPath     string
	acctMod      time.Time
	slConfigured bool
	slOurs       bool
	history      limits.History
	histMod      time.Time
	fired        limits.Fired

	// Restart after a rebuild
	exeMod   time.Time
	exeSize  int64
	newBuild bool
	restart  bool

	// Update check ([updates] check = true)
	updAsked time.Time // when the check last ran
	updNew   string    // a newer release tag, once one is found

	flash      string
	flashStyle lipgloss.Style
	flashUntil time.Time
}

// New creates the model.
func New(o Options) Model {
	m := Model{o: o, tab: tabUsage, hoverTab: -1, vc: &viewCache{}, now: time.Now(), shareFive: -1, shareWeek: -1, stab: &strategy.Stabilizer{}, gView: viewport.New(40, 10), cView: viewport.New(40, 10), aView: viewport.New(40, 10), sView: viewport.New(40, 10), tlView: viewport.New(40, 10)}
	if t := tabFor(o.Tab); t >= 0 {
		m.tab = t
	}
	m.refreshLimits(true)
	m.fired = limits.LoadFired(paths.AlertsFile())
	m.checkBuild()
	if config.IsWarning(o.ConfigErr) {
		m.setFlash("config: "+o.ConfigErr.Error(), sYellow, 10*time.Second)
	} else if o.ConfigErr != nil {
		m.setFlash("config error: "+o.ConfigErr.Error()+" — using defaults", sRed, 10*time.Second)
	}
	return m
}

// Init starts the clock.
func (m Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) setFlash(s string, st lipgloss.Style, d time.Duration) {
	m.flash, m.flashStyle, m.flashUntil = s, st, time.Now().Add(d)
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The terminal reports every mouse movement (for the hover highlight),
	// often 100 a second. One that changes nothing on screen is dropped
	// here, before it costs a redraw.
	if mm, ok := msg.(tea.MouseMsg); ok && m.idleMotion(mm) {
		return m, nil
	}
	if m.vc != nil {
		m.vc.ok = false
	}
	return m.update(msg)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutViewports()
		return m, nil

	case LoadedMsg:
		m.loaded = true
		m.pinAtStart()
		m.refresh()
		m.refreshFiles()
		m.refreshSkills()
		return m, nil

	case DataMsg:
		if m.loaded {
			m.refresh()
		}
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		m.refreshLimits(false)
		m.checkBuild()
		if m.loaded {
			m.refresh()
		}
		// Run these before the return: they change m, and Go doesn't say
		// whether a return reads m before or after calls in the same list.
		alerts, sessAlerts, changes, upd := m.checkAlerts(), m.checkSessionAlerts(), m.changesCmd(), m.updateCheck()
		return m, tea.Batch(tick(), alerts, sessAlerts, changes, upd)

	case newClaudeMsg:
		m.onNewClaude(msg)
		return m, nil

	case paneMsg:
		if msg.err != nil {
			m.setFlash("can't go to "+msg.label+": "+msg.err.Error(), sYellow, 6*time.Second)
		}
		return m, nil

	case updateMsg:
		m.onUpdate(msg)
		return m, nil

	case openedMsg:
		if msg.err != nil {
			m.setFlash("couldn't open "+ShortPath(msg.dir)+": "+msg.err.Error(), sRed, 6*time.Second)
		} else {
			m.setFlash("opened "+ShortPath(msg.dir), sGreen, 4*time.Second)
		}
		return m, nil

	case copiedMsg:
		switch {
		case msg.err != "":
			m.setFlash("copy failed: "+msg.err+" (select the text on screen instead)", sRed, 6*time.Second)
		case msg.warn:
			m.setFlash(msg.how, sYellow, 8*time.Second)
		default:
			m.setFlash(msg.how, sGreen, 6*time.Second)
		}
		return m, nil

	case diffMsg:
		m.applyDiff(msg)
		return m, m.changesCmd()

	case workMsg:
		m.applyWork(msg)
		return m, nil

	case stepsMsg:
		m.applySteps(msg)
		return m, nil

	case editorDoneMsg:
		switch {
		case msg.err != nil:
			m.setFlash("editor: "+msg.err.Error(), sRed, 6*time.Second)
		case configfiles.Unchanged(msg.path, msg.backup):
			_ = removeFile(msg.backup)
			m.setFlash("no changes; backup discarded", sMuted, 4*time.Second)
		default:
			m.setFlash("saved · backup: "+ShortPath(msg.backup), sGreen, 8*time.Second)
		}
		m.gLoaded = ""
		m.refreshFiles()
		return m, nil

	case tea.MouseMsg:
		nm, cmd := m.mouse(msg)
		mm := nm.(Model)
		return mm, tea.Batch(cmd, mm.changesCmd())

	case tea.KeyMsg:
		nm, cmd := m.handleKey(msg)
		mm := nm.(Model)
		return mm, tea.Batch(cmd, mm.changesCmd())
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.help {
		m.help = false
		return m, nil
	}
	// Keys the focused viewers consume first.
	var focus *bool
	var view *viewport.Model
	switch {
	case m.tab == tabFiles && m.gFocus:
		focus, view = &m.gFocus, &m.gView
	case m.tab == tabChanges && m.cFocus:
		focus, view = &m.cFocus, &m.cView
	case m.tab == tabActivity && m.aFocus:
		focus, view = &m.aFocus, &m.aView
	case m.tab == tabSkills && m.sFocus:
		focus, view = &m.sFocus, &m.sView
	}
	if m.tab == tabTools && m.tlFocus {
		if nm, cmd, ok := m.toolCallKey(key); ok {
			return nm, cmd
		}
	}
	if m.tab == tabActivity && m.aTyping {
		return m.searchKey(k)
	}
	if m.tab == tabActivity && m.loaded {
		switch key {
		case "x":
			m.loadTurn()
			return m.toggleAllSteps()
		case "n", "p":
			// Next / previous turn, even while reading one.
			dir := 1
			if key == "p" {
				dir = -1
			}
			if j := m.stepTurn(m.aSel, dir); j >= 0 {
				m.aSel = j
			}
			m.loadTurn()
			return m, nil
		}
	}
	if focus != nil {
		switch key {
		case "esc", "left", "h", "enter":
			*focus = false
			return m, nil
		case "q", "ctrl+c", "e", "c", "o", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "tab", "shift+tab", "?", "s", "P", "N", "R", "[", "]":
		default:
			var cmd tea.Cmd
			*view, cmd = view.Update(k)
			return m, cmd
		}
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "R":
		m.restart = true
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if key == "0" {
			return m.openSessions(), nil
		}
		m.tab = tabFor(key)
		if m.loaded {
			m.refresh()
		}
		return m, nil
	case "s":
		if m.tab == tabSessions {
			return m.sessionsKey("esc")
		}
		return m.openSessions(), nil
	case "P":
		return m.togglePin(), nil
	case "N":
		return m, m.newClaude()
	case "]", "[":
		if !m.loaded {
			return m, nil
		}
		dir := 1
		if key == "[" {
			dir = -1
		}
		return m.cycle(dir), nil
	case "tab":
		m.tab = (m.tab + 1) % len(tabNames)
		if m.loaded {
			m.refresh()
		}
		return m, nil
	case "shift+tab":
		m.tab = (m.tab + len(tabNames) - 1) % len(tabNames)
		if m.loaded {
			m.refresh()
		}
		return m, nil
	}

	switch m.tab {
	case tabSessions:
		return m.sessionsKey(key)
	case tabUsage:
		if key == "o" {
			return m, openURL(UsagePageURL)
		}
		return m.scrollKey(key), nil
	case tabModels:
		return m.scrollKey(key), nil
	case tabFiles:
		return m.filesKey(key)
	case tabChanges:
		return m.changesKey(key)
	case tabActivity:
		return m.activityKey(key)
	case tabContext:
		return m.contextKey(key)
	case tabCheatsheet:
		return m.cheatsheetKey(key)
	case tabSkills:
		return m.skillsKey(key)
	case tabTools:
		return m.toolsKey(key)
	}
	return m, nil
}

func (m Model) filesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.gSel > 0 {
			m.gSel--
		}
	case "down", "j":
		if m.gSel < len(m.files)-1 {
			m.gSel++
		}
	case "home", "g":
		m.gSel = 0
	case "end", "G":
		m.gSel = max(0, len(m.files)-1)
	case "enter", "right", "l":
		if m.gSel < len(m.files) && m.files[m.gSel].Exists {
			m.gFocus = true
		}
	case "r":
		m.gLoaded = ""
		m.refreshFiles()
		m.setFlash("rescanned files", sMuted, 2*time.Second)
	case "e":
		return m.editSelected()
	case "o":
		if m.gSel < len(m.files) {
			return m, openFolder(m.files[m.gSel].Path)
		}
	}
	m.loadSelectedFile()
	return m, nil
}

func (m Model) editSelected() (tea.Model, tea.Cmd) {
	if !m.o.Core.Cfg.Files.AllowEdit {
		m.setFlash("read-only: set [files] allow_edit = true in "+ShortPath(paths.ConfigFile())+" to enable editing", sYellow, 6*time.Second)
		return m, nil
	}
	if m.gSel >= len(m.files) {
		return m, nil
	}
	f := m.files[m.gSel]
	if !f.Exists {
		m.setFlash("file doesn't exist; Lookout only edits existing files", sYellow, 5*time.Second)
		return m, nil
	}
	backup, err := configfiles.Backup(f.Path, paths.BackupDir(), time.Now())
	if err != nil {
		m.setFlash("backup failed, not editing: "+err.Error(), sRed, 6*time.Second)
		return m, nil
	}
	ed := configfiles.Editor()
	cmd := exec.Command(ed[0], append(ed[1:], f.Path)...)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{path: f.Path, backup: backup, err: err}
	})
}

// refresh recomputes usage and strategy.
func (m *Model) refresh() {
	now := time.Now()
	m.prevShown = m.shownID()
	if p := m.o.Core.Pinned; p != "" && !m.o.Core.Agg.Known(p) && !m.o.Core.IsLive(p) {
		// Pinned before it sent anything, and closed since: nothing left
		// to show, so don't keep saying it's pinned.
		m.o.Core.Pinned = ""
		m.setFlash("the pinned session closed before it sent anything · back on ↻ Auto", sMuted, 5*time.Second)
	}
	m.snap, m.sess, m.hasSess = m.o.Core.Snapshot(now)
	m.refreshSessions(now)
	m.noteAutoSwitch()
	if m.tab != tabSessions {
		m.prevTab = m.tab
	}
	m.health = m.o.Core.Watcher.Health()
	if m.width > 0 && lipgloss.Height(m.viewHeader()) != m.headerH {
		m.layoutViewports() // a warning line came or went: resize the readers
	}
	m.refreshShare(now)
	raw, st := m.o.Core.Suggest(now, SuggestExtra(m.acct, m.limits, m.limitsExact(), now))
	m.sstate = st
	m.sugg, m.pending = m.stab.Update(raw, st.TurnCount, now)
	if k := m.sugg.SessionID + "|" + string(m.sugg.Action) + "|" + m.sugg.Suggested; k != m.shownKey {
		m.shownKey, m.shownSince = k, now
	}
	prev := m.act.ID
	// Lists are rebuilt below, newest first: note what is selected so a
	// new turn or file doesn't move the selection to another one.
	var turnKey time.Time
	if m.aSel < len(m.act.Turns) {
		turnKey = m.act.Turns[m.aSel].Start
	}
	changeKey := m.changeKey()
	m.act = activity.Session{}
	if m.hasSess {
		m.act = m.o.Core.Activity.Session(m.sess.ID)
	}
	m.refreshGone()
	if m.tab == tabContext || prev != m.act.ID {
		if prev != m.act.ID {
			m.xSel, m.xFocus = 0, false
		}
		m.refreshContext()
	}
	if m.loaded && m.hasSess && m.sess.Cwd != m.skillsFor {
		m.refreshSkills()
	}
	if m.act.ID != prev {
		// Another session: start the lists at the top.
		m.cSel, m.aSel, m.cFocus, m.aFocus = 0, 0, false, false
		m.tlSel, m.tlName, m.tlFocus, m.tlShown = 0, "", false, ""
		m.tlOpen, m.tlCur = nil, 0
		m.diffPath, m.diffText, m.aShown = "", "", ""
		m.aOpen, m.aDetail = nil, nil
		m.showDiff()
	}
	if m.act.ID == prev {
		for i, t := range m.act.Turns {
			if !turnKey.IsZero() && t.Start.Equal(turnKey) {
				m.aSel = i
				break
			}
		}
		m.keepChange(changeKey)
	}
	if n := len(m.act.Turns); m.aSel >= n {
		m.aSel = max(0, n-1)
	}
	if n := len(m.changeItems()); m.cSel >= n {
		m.cSel = max(0, n-1)
	}
	m.loadTurn()
	m.syncTool()
	m.loadTool()
	if db := m.o.Store; db != nil && m.hasSess {
		// Log a suggestion once it has been on screen for a minute, so the
		// first moments after a start (before your pace is known) aren't logged.
		if now.Sub(m.shownSince) >= time.Minute {
			if _, err := db.LogSuggestion(m.sugg); err != nil {
				m.setFlash("strategy log: "+err.Error(), sRed, 5*time.Second)
			}
		}
		_ = db.ResolveNext(m.sstate.ID, m.sstate.Model, m.sstate.Effort, m.sstate.LastAssistant)
		if now.Sub(m.lastLogAt) > 3*time.Second {
			m.logRows, _ = db.Recent(8)
			m.judgeOutcomes(db)
			m.resolved, m.followed, m.smooth, _ = db.Adherence()
			m.lastLogAt = now
		}
	}
	if m.tab == tabCheatsheet {
		m.keepCheat()
	}
}

func (m *Model) refreshFiles() {
	c := m.o.Core
	m.files = configfiles.Discover(configfiles.Sources{
		ClaudeDir:     paths.ClaudeDir(),
		ProjectDirs:   c.ProjectDirs(),
		ProjectNames:  c.ProjectNames(),
		Extra:         c.Cfg.Files.ExtraFiles,
		LookoutConfig: paths.ConfigFile(),
	})
	if m.gSel >= len(m.files) {
		m.gSel = max(0, len(m.files)-1)
	}
	m.loadSelectedFile()
}

func (m *Model) loadSelectedFile() {
	if m.gSel >= len(m.files) {
		m.gView.SetContent("")
		return
	}
	f := m.files[m.gSel]
	if m.gLoaded == f.Path {
		return
	}
	m.gLoaded = f.Path
	m.gReadErr = ""
	if !f.Exists {
		msg := sMuted.Render("Not present: " + ShortPath(f.Path))
		if f.Group == configfiles.GroupLookout {
			msg += "\n\n" + sText.Render("Lookout is using its defaults. To change them, run ") + sBold.Render("lookout config init") +
				"\n" + sText.Render("to write a commented config.toml, then press R to restart after editing it.")
		}
		m.gView.SetContent(msg)
		m.gView.GotoTop()
		return
	}
	s, err := configfiles.Read(f.Path)
	if err != nil {
		m.gReadErr = err.Error()
		m.gView.SetContent(sRed.Render(err.Error()))
		return
	}
	m.gView.SetContent(numbered(f.Path, s))
	m.gView.GotoTop()
}

func numbered(path, s string) string {
	s = cleanText(s)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	w := len(fmt.Sprint(len(lines)))
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(sMuted.Render(lpad(fmt.Sprint(i+1), w)))
		b.WriteString("  ")
		b.WriteString(highlightLine(path, l))
		b.WriteByte('\n')
	}
	return b.String()
}

// layoutViewports sizes the viewers for the current window.
func (m *Model) layoutViewports() {
	m.headerH = lipgloss.Height(m.viewHeader())
	bodyH := max(5, m.height-3-m.headerH)
	listW := m.listWidth()
	viewW := max(20, m.width-listW-3)
	for _, v := range []*viewport.Model{&m.gView, &m.cView, &m.aView, &m.sView, &m.tlView} {
		v.Width, v.Height = viewW, max(3, bodyH-2)
	}
	m.gLoaded, m.aShown, m.sShown, m.tlShown = "", "", "", ""
	m.loadSkill()
	m.loadSelectedFile()
	m.loadTurn()
	m.loadTool()
}

func (m Model) listWidth() int {
	return min(44, max(26, m.width/3))
}

// View renders the screen.
func (m Model) View() string {
	if m.vc != nil && m.vc.ok {
		return m.vc.out
	}
	out := m.sel.highlight(m.render())
	if m.vc != nil {
		m.vc.out, m.vc.ok = out, true
	}
	return out
}

// viewCache keeps the last screen drawn, so View can return it again when
// nothing has changed since. It is shared by every copy of the Model.
type viewCache struct {
	out string
	ok  bool
}

// render draws the screen; View adds any mouse selection on top.
func (m Model) render() string {
	if m.width == 0 {
		return "starting lookout…"
	}
	header := m.viewHeader()
	bodyH := max(3, m.height-lipgloss.Height(header)-footerHeight)
	var body string
	switch {
	case m.help:
		body = m.viewHelp()
	case !m.loaded:
		body = "\n  " + sMuted.Render("scanning transcripts in "+ShortPath(paths.ClaudeProjectsDir())+" …")
	case !m.hasSess && needsSession(m.tab):
		body = m.viewNoSession()
	default:
		switch m.tab {
		case tabUsage:
			body, m.overflow = m.scrolled(m.viewUsage(), bodyH)
		case tabChanges:
			body = m.viewChanges(bodyH)
		case tabActivity:
			body = m.viewActivity(bodyH)
		case tabContext:
			body = m.viewContext(bodyH)
		case tabCheatsheet:
			body = m.viewCheatsheet(bodyH)
		case tabSkills:
			body = m.viewSkills(bodyH)
		case tabModels:
			body, m.overflow = m.scrolled(m.viewModels(), bodyH)
		case tabTools:
			body = m.viewTools(bodyH)
		case tabFiles:
			body = m.viewFiles(bodyH)
		case tabSessions:
			body = m.viewSessions(bodyH)
		}
	}
	footer := m.viewFooter()
	body = lipgloss.NewStyle().MaxHeight(bodyH).Height(bodyH).Render(body)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
}

// headerParts are the header's pieces: the brand, the tabs (full names,
// or numbers when space is short) and the badges on the right.
func (m Model) headerParts() (brand string, tabs, right []string) {
	// Right side, trimmed from the end to fit. The first limit badge (5h,
	// or today's cost) comes first so it stays on every panel; the rest
	// follow the other badges.
	badges := m.limitBadges()
	if len(badges) > 0 {
		right = append(right, badges[0])
	}
	if w := m.health.Warning(); w != "" {
		right = append(right, sWarn.Render("! PARSER"))
	}
	if m.newBuild {
		right = append(right, sGreen.Bold(true).Render("↻ R restart"))
	}
	if m.o.Core.Pinned != "" {
		if o, ok := m.shownOverview(); ok && m.closed(o) {
			right = append(right, sMuted.Bold(true).Render("◷ closed"))
		} else {
			right = append(right, sYellow.Bold(true).Render("◆ pinned"))
		}
	}
	if m.updNew != "" {
		right = append(right, sGreen.Render("↑ "+m.updNew))
	}
	if len(badges) > 1 {
		right = append(right, badges[1:]...)
	}

	fits := func(b string, t, r []string) bool {
		return lipgloss.Width(b+"  "+strings.Join(t, ""))+lipgloss.Width(strings.Join(r, "  "))+1 <= m.width
	}
	// Keep tab names as long as possible: first tighten the spacing, then
	// shorten tabs to their number one at a time (the setup panels first,
	// the one on screen never), and only then drop badges.
	short := make([]bool, len(tabNames))
	tight := false
	full := sBrand.Render("◉ LOOKOUT")
	brand = full
	steps := []func(){func() { tight = true }}
	for _, i := range []int{tabCheatsheet, tabFiles, tabSkills} {
		steps = append(steps, func() { short[i] = true })
	}
	steps = append(steps, func() { short[tabSessions] = true }, func() { brand = sBrand.Render("◉") })
	for i := tabTools; i >= tabUsage; i-- {
		steps = append(steps, func() { short[i] = true })
	}
	tabs = m.tabLabels(short, tight)
	for _, step := range steps {
		if fits(brand, tabs, right) {
			break
		}
		step()
		tabs = m.tabLabels(short, tight)
	}
	for len(right) > 0 && !fits(brand, tabs, right) {
		right = right[:len(right)-1]
	}
	return brand, tabs, right
}

// tabLabels draws the tabs. The one on screen is a filled button; the
// others are their key in blue and name in grey, or only the key where
// short says so. tight puts one space between tabs instead of two.
func (m Model) tabLabels(short []bool, tight bool) []string {
	gap := "  "
	if tight {
		gap = " "
	}
	tabs := make([]string, 0, len(tabNames))
	for i, n := range tabNames {
		var t string
		switch {
		case i == m.tab:
			t = sTabOn.Render(tabKey(i) + " " + n)
		case i == m.hoverTab && short[i]:
			t = sChip.Foreground(cBlue).Render(" " + tabKey(i) + " ")
		case i == m.hoverTab:
			t = sChip.Foreground(cBlue).Render(" "+tabKey(i)) + sChip.Foreground(cText).Render(" "+n+" ")
		case short[i]:
			t = " " + sTabKey.Render(tabKey(i)) + " "
		default:
			t = " " + sTabKey.Render(tabKey(i)) + " " + sTabName.Render(n) + " "
		}
		// A divider sets the panels about one session apart from Sessions
		// before them and the setup panels (skills, config, cheatsheet) after.
		switch {
		case i == tabSkills || i == tabUsage:
			t = gap[1:] + sFaint.Render("│") + gap[1:] + t
		case i > 0:
			t = gap[1:] + t
		}
		tabs = append(tabs, t)
	}
	return tabs
}

// tabAt is the tab under column x of the header's first line, or -1.
func (m Model) tabAt(x int) int {
	brand, tabs, _ := m.headerParts()
	pos := lipgloss.Width(brand) + 2
	for i, t := range tabs {
		w := lipgloss.Width(t)
		if x >= pos && x < pos+w {
			return i
		}
		pos += w
	}
	return -1
}

func (m Model) viewHeader() string {
	brand, tabs, right := m.headerParts()
	left := brand + "  " + strings.Join(tabs, "")
	line := left
	if r := strings.Join(right, "  "); len(right) > 0 {
		line = left + strings.Repeat(" ", m.width-lipgloss.Width(left)-lipgloss.Width(r)) + r
	}
	out := line + "\n"
	if m.stripRow() == 2 {
		// Room between the tabs and the sessions under them.
		out += "\n"
	}
	out += m.viewRule()
	if w := m.health.Warning(); w != "" {
		out += "\n" + sYellow.Render(clip("! "+w, m.width))
	}
	// A blank line between the header and the panel, when there's room.
	if m.roomy() {
		out += "\n"
	}
	return out
}

func (m Model) viewFooter() string {
	var keys string
	switch {
	case m.tab == tabFiles && m.gFocus:
		keys = "↑↓/pgup/pgdn scroll · esc back · e edit · o open folder · q quit"
	case m.tab == tabFiles:
		keys = "↑↓ select · enter view · e edit · o open folder · r rescan · ? help · q quit"
	case m.tab == tabActivity && m.aTyping:
		keys = "type to search prompts and replies · enter keep · esc clear"
	case m.tab == tabActivity && m.aFocus:
		keys = "↑↓/pgup/pgdn scroll · n/p next/previous turn · click a step to open it · x open all · esc back"
	case m.tab == tabChanges && m.cFocus:
		keys = "↑↓/pgup/pgdn scroll · esc back · q quit"
	case m.tab == tabChanges:
		keys = "↑↓ select · enter read diff · o open folder · y copy commit ID · s sessions · ? help · q quit"
		if it, ok := m.selectedChange(); ok && it.A != nil {
			keys = "↑↓ select · enter read output · y copy command · s sessions · ? help · q quit"
		}
	case m.tab == tabActivity:
		keys = "↑↓ select · enter details · / search · x open all steps · s sessions · ? help · q quit"
	case m.tab == tabSkills && m.sFocus:
		keys = "↑↓/pgup/pgdn scroll · c copy · esc back · q quit"
	case m.tab == tabSkills:
		keys = "↑↓ select · enter read · c copy how to use it · r rescan · ? help · q quit"
	case m.tab == tabTools && m.tlFocus:
		keys = "↑↓ move · enter open the call · a show in Activity · f failed only · esc back"
	case m.tab == tabTools:
		keys = "↑↓ select · enter read its calls · f failed only · s sessions · ? help · q quit"
	case m.tab == tabCheatsheet:
		keys = "↑↓ select · enter/c copy · ? help · q quit"
	case m.tab == tabContext && m.xFocus:
		keys = "esc back · q quit"
	case m.tab == tabContext && m.xAll:
		keys = "↑↓ select · enter details · a largest only · ? help · q quit"
	case m.tab == tabContext:
		keys = "↑↓ select · enter details · a see all · ? help · q quit"
	case m.tab == tabUsage && m.canScroll():
		keys = "↑↓ scroll · o open usage on claude.ai · 0-9/tab panels · [ ] switch session · ? help · q quit"
	case m.tab == tabUsage:
		keys = "o open usage on claude.ai · 0-9/tab panels · [ ] switch session · ? help · q quit"
	case m.tab == tabModels && m.canScroll():
		keys = "↑↓ scroll · 0-9/tab panels · s sessions · [ ] switch session · ? help · q quit"
	case m.tab == tabSessions:
		keys = "↑↓ select · enter show it · esc back · a follow the latest · y copy ID · c copy resume command"
		if m.svSel < m.listedSessions() && canGoTo(m.ovs[m.svSel]) {
			keys = "↑↓ select · enter show it · g go to its tmux pane · esc back · a follow the latest · y copy ID · c copy resume command"
		}
	default:
		keys = "0-9/tab panels · s sessions · [ ] switch session · ? help · q quit"
	}
	if m.newBuild {
		keys = "R restart into new build · " + keys
	}
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		return m.flashStyle.Render(clip(m.flash, m.width))
	}
	return footerKeys(clip(keys, m.width))
}

// footerKeys draws a footer hint like "↑↓ select · enter details": each
// key in bold, what it does in grey.
func footerKeys(s string) string {
	parts := strings.Split(s, " · ")
	for i, p := range parts {
		key, rest := p, ""
		switch {
		case strings.HasPrefix(p, "[ ] "):
			key, rest = "[ ]", p[3:]
		default:
			if j := strings.IndexByte(p, ' '); j > 0 {
				key, rest = p[:j], p[j:]
			}
		}
		// Plain prose (e.g. "type to search…") isn't a key.
		if len([]rune(key)) > 14 || key == "type" {
			parts[i] = sMuted.Render(p)
			continue
		}
		parts[i] = sBold.Render(key) + sMuted.Render(rest)
	}
	return strings.Join(parts, sMuted.Render(" · "))
}

func (m Model) viewHelp() string {
	lines := []string{
		section("Help", m.width),
		"",
		sBold.Render("Panels"),
		"  0 Sessions    every running session: working, needs you (▲), finished (✓) or idle, with\n" +
			"                context, limit share and your last prompt; enter shows one in Usage,\n" +
			"                esc goes back, g goes to its tmux pane",
		"  1 Usage       limits, token usage for the session, today and the last 5h, and limit use per day",
		"  2 Activity    one entry per prompt: Claude's messages and tool calls in order, files edited;\n" +
			"                click a step (or x for all) to see its full command and output",
		"  3 Changes     every file changed this session (Claude's edits, uncommitted, committed), with diffs",
		"  4 Context     what fills the context window, by kind and largest items",
		"  5 Models      model suggestion and what each model is for; switch yourself with /model",
		"  6 Tools       every tool Claude used this session (Bash, Edit, MCP tools…): calls, errors,\n" +
			"                denials, time, and each call under its prompt; enter opens a call,\n" +
			"                a shows it in Activity, f shows failed calls only",
		"  7 Skills      skills, custom commands and subagents Claude can use, and how to run them",
		"  8 Config      Claude Code settings, CLAUDE.md, memory and Lookout's config.toml, read-only by default",
		"  9 Cheatsheet  Claude Code commands to copy and paste, incl. ones for this session",
		"",
		sBold.Render("Keys"),
		"  0-9, tab      switch panel",
		"  [ ]           step through the tabs under the panels: ↻ Auto, then each running session (also: click one)",
		"                ↻ Auto follows the session you last prompted or clicked into (tmux); any other tab pins that session",
		"  c             copy (Skills: how to run it · Cheatsheet: the command)",
		"  a             Context: see all items / only the largest · Tools: show the call in Activity",
		"  f             Tools: show failed calls only",
		"  y             copy an ID (Changes: the file's commit · Sessions: the session)",
		"  s, 0          open Sessions to pick which session to show (a: follow the latest again, esc: back)",
		"  P             pin the session shown, or go back to ↻ Auto",
		"  N             in tmux: open a new Claude Code in the folder of the session shown (also: + claude)",
		"  ↑↓ / j k      move · enter to focus the viewer · esc to go back",
		"  e             edit the selected file in $EDITOR (needs [files] allow_edit = true)",
		"  o             Config, Changes: open the selected file's folder in your file manager",
		"                Usage: open your usage page on claude.ai",
		"  r             rescan the file list",
		"  R             restart Lookout (e.g. after a rebuild)",
		"  mouse         click a tab, or an item to open it · the wheel scrolls what's under it",
		"                drag to select text: it's copied when you let go (Shift+drag: the terminal's own)",
		"  q             quit",
		"",
		sBold.Render("Session estimates"),
		"  \"this session: about N of the X% used\" (Usage) and \"5h N%\" (Sessions) are the session's share",
		"  of the Claude Code tokens used on this computer in that limit window, weighted by model price.",
		"  They assume no other Claude usage (claude.ai, other computers) in the window.",
		"",
		sBold.Render("Alerts"),
		"  A bell, a desktop notification and a footer message when a limit passes 80% or 95%,",
		"  or when your pace would reach it before the reset, and when another session needs you",
		"  or finishes. Set them in [alerts] in config.toml.",
		"",
		sBold.Render("Updates"),
		"  " + m.updateHelp(),
		"",
		sBold.Render("Files"),
		"  config   " + ShortPath(paths.ConfigFile()),
		"  data     " + ShortPath(paths.DataDir()),
		"",
		sMuted.Render("Lookout only reads what Claude Code writes to disk. It never calls Claude."),
		sMuted.Render("Git is run read-only (diff, status, log) for the Changes panel."),
		sMuted.Render("Unofficial. Not affiliated with Anthropic. " + m.o.Version),
		"",
		sMuted.Render("press any key"),
	}
	return strings.Join(lines, "\n")
}
