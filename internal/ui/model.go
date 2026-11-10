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
	tabUsage = iota
	tabActivity
	tabChanges
	tabContext
	tabModels
	tabTools
	tabSkills
	tabFiles
	tabCheatsheet
)

var tabNames = []string{"Usage", "Activity", "Changes", "Context", "Models", "Tools", "Skills", "Files", "Cheatsheet"}

// Options configure the model.
type Options struct {
	Core      *app.Core
	Store     *store.Store
	StoreErr  error
	ConfigErr error
	Version   string
	Exe       string
}

// Model is the root Bubble Tea model.
type Model struct {
	o      Options
	width  int
	height int
	tab    int
	help   bool
	sel    selection // mouse drag in progress
	loaded bool
	now    time.Time

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
	aStepAt   []string                       // tool ID per line of the shown turn
	aRev      int                            // bumped when steps open or load
	aFilter   string                         // Activity search text
	aTyping   bool                           // typing the search

	// Estimated share of each limit window used by the focused session:
	// its fraction of all Claude Code usage on this computer in the window
	// (weighted by model price), or -1 when unknown.
	shareFive, shareWeek float64

	ctxView activity.ContextView
	xSel    int  // selected context item
	xAll    bool // list every item, not just the largest
	xFocus  bool // showing one item's details
	kSel    int  // selected cheatsheet command

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

	// Session picker
	picking bool
	pSel    int
	pList   []usage.Session

	// Skills
	skills []skills.Item
	sSel   int
	sFocus bool
	sView  viewport.Model
	sShown string

	// Files
	files    []configfiles.File
	gSel     int
	gFocus   bool // viewer has focus
	gView    viewport.Model
	gLoaded  string // path currently in the viewer
	gReadErr string

	// Limits (from the status line)
	limits       limits.State
	limitsMod    time.Time
	limitsPath   string
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

	flash      string
	flashStyle lipgloss.Style
	flashUntil time.Time
}

// New creates the model.
func New(o Options) Model {
	m := Model{o: o, now: time.Now(), shareFive: -1, shareWeek: -1, stab: &strategy.Stabilizer{}, gView: viewport.New(40, 10), cView: viewport.New(40, 10), aView: viewport.New(40, 10), sView: viewport.New(40, 10), tlView: viewport.New(40, 10)}
	m.refreshLimits(true)
	m.fired = limits.LoadFired(paths.AlertsFile())
	m.checkBuild()
	if o.ConfigErr != nil {
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutViewports()
		return m, nil

	case LoadedMsg:
		m.loaded = true
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
		return m, tea.Batch(tick(), m.checkAlerts(), m.changesCmd())

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
	if m.picking {
		return m.pickerKey(key)
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
		case "q", "ctrl+c", "e", "c", "o", "1", "2", "3", "4", "5", "6", "7", "8", "9", "tab", "shift+tab", "?", "s", "P", "R":
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
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.tab = int(key[0] - '1')
		if m.loaded {
			m.refresh()
		}
		return m, nil
	case "s":
		return m.openPicker(), nil
	case "P":
		return m.togglePin(), nil
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
	m.snap, m.sess, m.hasSess = m.o.Core.Snapshot(now)
	m.health = m.o.Core.Watcher.Health()
	m.refreshShare(now)
	raw, st := m.o.Core.Suggest(now, SuggestExtra(m.limits, m.limitsExact(), now))
	m.sstate = st
	m.sugg, m.pending = m.stab.Update(raw, st.TurnCount, now)
	if k := m.sugg.SessionID + "|" + string(m.sugg.Action) + "|" + m.sugg.Suggested; k != m.shownKey {
		m.shownKey, m.shownSince = k, now
	}
	prev := m.act.ID
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
	if m.act.ID != prev {
		// Another session: start the lists at the top.
		m.cSel, m.aSel, m.cFocus, m.aFocus = 0, 0, false, false
		m.tlSel, m.tlName, m.tlFocus, m.tlShown = 0, "", false, ""
		m.tlOpen, m.tlCur = nil, 0
		m.diffPath, m.diffText, m.aShown = "", "", ""
		m.aOpen, m.aDetail = nil, nil
		m.showDiff()
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
		_ = db.ResolveNext(m.sstate.ID, m.sstate.Model, m.sstate.LastAssistant)
		if now.Sub(m.lastLogAt) > 3*time.Second {
			m.logRows, _ = db.Recent(8)
			m.judgeOutcomes(db)
			m.resolved, m.followed, m.smooth, _ = db.Adherence()
			m.lastLogAt = now
		}
	}
}

func (m *Model) refreshFiles() {
	c := m.o.Core
	m.files = configfiles.Discover(configfiles.Sources{
		ClaudeDir:    paths.ClaudeDir(),
		ProjectDirs:  c.ProjectDirs(),
		ProjectNames: c.ProjectNames(),
		Extra:        c.Cfg.Files.ExtraFiles,
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
		m.gView.SetContent(sMuted.Render("Not present: " + ShortPath(f.Path)))
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
	s = strings.ReplaceAll(s, "\t", "    ")
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
	bodyH := max(5, m.height-5)
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
	return m.sel.highlight(m.render())
}

// render draws the screen; View adds any mouse selection on top.
func (m Model) render() string {
	if m.width == 0 {
		return "starting lookout…"
	}
	header := m.viewHeader()
	footer := m.viewFooter()
	bodyH := max(3, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	var body string
	switch {
	case m.help:
		body = m.viewHelp()
	case m.picking:
		body = m.viewPicker(bodyH)
	case !m.loaded:
		body = "\n  " + sMuted.Render("scanning transcripts in "+ShortPath(paths.ClaudeProjectsDir())+" …")
	default:
		switch m.tab {
		case tabUsage:
			body = m.viewUsage()
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
			body = m.viewModels()
		case tabTools:
			body = m.viewTools(bodyH)
		case tabFiles:
			body = m.viewFiles(bodyH)
		}
	}
	body = lipgloss.NewStyle().MaxHeight(bodyH).Height(bodyH).Render(body)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
}

// headerParts are the header's pieces: the brand, the tabs (full names,
// or numbers when space is short) and the badges on the right.
func (m Model) headerParts() (brand string, tabs, right []string) {
	brand = sBrand.Render("◉ LOOKOUT")
	tabsFor := func(short bool) []string {
		var tabs []string
		for i, n := range tabNames {
			label := fmt.Sprintf("%d %s", i+1, n)
			if short && i != m.tab {
				label = fmt.Sprint(i + 1)
			}
			t := sTab.Render(label)
			if i == m.tab {
				t = sTabOn.Render(label)
			}
			// A divider sets the setup panels (skills, files, cheatsheet)
			// apart from the ones about the session.
			if i == tabSkills {
				t = sFaint.Render("│") + t
			}
			tabs = append(tabs, t)
		}
		return tabs
	}

	// Right side, most important first; trimmed from the end to fit.
	if w := m.health.Warning(); w != "" {
		right = append(right, sWarn.Render("! PARSER"))
	}
	if m.newBuild {
		right = append(right, sGreen.Bold(true).Render("↻ R restart"))
	}
	if m.o.Core.Pinned != "" {
		right = append(right, sYellow.Bold(true).Render("◆ pinned"))
	}
	right = append(right, m.limitBadges()...)

	// Prefer full tab names; fall back to short tabs, then fewer badges.
	fits := func(t, r []string) bool {
		return lipgloss.Width(brand+"  "+strings.Join(t, ""))+lipgloss.Width(strings.Join(r, "  "))+1 <= m.width
	}
	tabs = tabsFor(false)
	if !fits(tabs, right) {
		tabs = tabsFor(true)
	}
	for len(right) > 0 && !fits(tabs, right) {
		right = right[:len(right)-1]
	}
	return brand, tabs, right
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
	rule := sMuted.Render(strings.Repeat("━", max(0, m.width)))
	out := line + "\n" + rule
	if w := m.health.Warning(); w != "" {
		out += "\n" + sYellow.Render(clip("! "+w, m.width))
	}
	return out
}

func (m Model) viewFooter() string {
	var keys string
	switch {
	case m.picking:
		keys = "↑↓ select · enter show · a follow latest · y copy ID · esc close"
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
	default:
		keys = "1-9/tab panels · s sessions · ? help · q quit"
	}
	if m.newBuild {
		keys = "R restart into new build · " + keys
	}
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		return m.flashStyle.Render(clip(m.flash, m.width))
	}
	return sMuted.Render(clip(keys, m.width))
}

func (m Model) viewHelp() string {
	lines := []string{
		section("Help", m.width),
		"",
		sBold.Render("Panels"),
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
		"  8 Files       Claude Code config and memory files, read-only by default",
		"  9 Cheatsheet  Claude Code commands to copy and paste, incl. ones for this session",
		"",
		sBold.Render("Keys"),
		"  1-9, tab      switch panel",
		"  c             copy (Skills: how to run it · Cheatsheet: the command)",
		"  a             Context: see all items / only the largest · Tools: show the call in Activity",
		"  f             Tools: show failed calls only",
		"  y             copy an ID (Changes: the file's commit · session picker: the session)",
		"  s             pick which session to show (a: follow the latest again)",
		"  P             pin the session shown, or unpin to follow the latest",
		"  ↑↓ / j k      move · enter to focus the viewer · esc to go back",
		"  e             edit the selected file in $EDITOR (needs [files] allow_edit = true)",
		"  o             Files, Changes: open the selected file's folder in your file manager",
		"  r             rescan the file list",
		"  R             restart Lookout (e.g. after a rebuild)",
		"  mouse         click a tab, or an item to open it · the wheel scrolls what's under it",
		"                drag to select text: it's copied when you let go (Shift+drag: the terminal's own)",
		"  q             quit",
		"",
		sBold.Render("Alerts"),
		"  A bell, a desktop notification and a footer message when a limit passes 80% or 95%,",
		"  or when your pace would reach it before the reset. Set them in [alerts] in config.toml.",
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
