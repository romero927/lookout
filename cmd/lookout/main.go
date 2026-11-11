// Command lookout is a read-only dashboard that runs beside an interactive
// Claude Code session. Unofficial; not affiliated with Anthropic or OpenAI.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romero927/lookout/internal/app"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/store"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/ui"
	"github.com/romero927/lookout/internal/update"
)

// Set by the release build.
var (
	version = "dev"
	commit  = ""
)

const usage = `Lookout — a dashboard for Claude Code sessions (unofficial).

Usage:
  lookout [--cwd DIR | --here] [--session ID]   open the dashboard
  lookout stats [--cwd DIR] [--session ID]      print usage and the model suggestion once, then exit
  lookout statusline               status line entry point (Claude Code runs this)
  lookout statusline --wrap CMD    run your existing status line CMD too and add Lookout's line to it
  lookout statusline install --print   print the settings.json snippet for it
  lookout config init|path         write the default config / show its path
  lookout paths                    show every directory Lookout uses
  lookout update                   check GitHub for a newer release (the dashboard can do this daily: [updates] check)
  lookout version

Flags for the dashboard and stats:
  --cwd DIR   focus on the latest session started in DIR
  --here      same as --cwd "$PWD"
  --session ID  show this session (its ID, or the start of it) instead of following the latest

Environment:
  LOOKOUT_THEME=dark|light   skip terminal background detection

Keys: 0-9 or tab switch panels · [ ] switch session · R restart · ? help · q quit
`

// appVersion is the release version, or the module version for builds made
// with `go install ...@vX.Y.Z` (which don't set ldflags).
func appVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return version
}

// restartExe is set when the dashboard should restart into a new build.
var restartExe string

func main() {
	code := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if restartExe != "" {
		if err := restartSelf(restartExe); err != nil {
			fmt.Fprintf(os.Stderr, "lookout: restart: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if err := paths.MigrateLegacy(); err != nil {
		fmt.Fprintf(stderr, "lookout: moving data from the old overwatch folders: %v\n", err)
	}
	if len(args) > 0 {
		switch args[0] {
		case "statusline":
			return cmdStatusLine(args[1:], stdin, stdout, stderr)
		case "stats":
			return cmdStats(args[1:], stdout, stderr)
		case "config":
			return cmdConfig(args[1:], stdout, stderr)
		case "paths":
			return cmdPaths(stdout)
		case "update":
			return cmdUpdate(args[1:], stdout, stderr)
		case "version", "--version", "-v":
			fmt.Fprintln(stdout, strings.TrimSpace("lookout "+appVersion()+" "+commit))
			return 0
		case "help", "--help", "-h":
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	return cmdTUI(args, stderr)
}

func loadConfig(stderr io.Writer) config.Config {
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		fmt.Fprintf(stderr, "lookout: config: %v (using defaults)\n", err)
	}
	return cfg
}

// focus is what the dashboard and stats show: sessions in dir (all when
// empty), or the one session given with --session.
type focus struct {
	dir, session string
}

func focusFlags(name string, args []string, stderr io.Writer) (focus, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", "", "focus on the latest session in this directory")
	here := fs.Bool("here", false, "focus on the latest session in the current directory")
	session := fs.String("session", "", "show this session (its ID or the start of it)")
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return focus{}, false
	}
	dir := *cwd
	if *here {
		dir, _ = os.Getwd()
	}
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	return focus{dir: dir, session: strings.TrimSpace(*session)}, true
}

// Set before restarting into a new build, so the new process shows the
// same session and panel. Read once and cleared, so the programs Lookout
// runs don't inherit them.
const (
	envRestorePin = "LOOKOUT_RESTORE_PIN"
	envRestoreTab = "LOOKOUT_RESTORE_TAB"
)

// restored returns what the previous process left for a restart.
func restored() (pin, tab string, ok bool) {
	pin, okPin := os.LookupEnv(envRestorePin)
	t, okTab := os.LookupEnv(envRestoreTab)
	_ = os.Unsetenv(envRestorePin)
	_ = os.Unsetenv(envRestoreTab)
	if !okPin && !okTab {
		return "", "", false
	}
	return pin, t, true
}

func cmdTUI(args []string, stderr io.Writer) int {
	f, ok := focusFlags("lookout", args, stderr)
	if !ok {
		return 2
	}
	dir, session, tab := f.dir, f.session, ""
	if pin, t, ok := restored(); ok {
		// A restart (R): keep what was on screen, pinned or not.
		session, tab = pin, t
	}
	cfg, cfgErr := config.Load(paths.ConfigFile())
	theme := cfg.UI.Theme
	if t := os.Getenv("LOOKOUT_THEME"); t != "" {
		theme = t
	}
	switch theme {
	case "dark":
		lipgloss.SetHasDarkBackground(true)
	case "light":
		lipgloss.SetHasDarkBackground(false)
	}

	var prog *tea.Program
	core := app.New(cfg, dir, func() {
		if prog != nil {
			prog.Send(ui.DataMsg{})
		}
	})
	db, dbErr := store.Open(paths.DBFile())
	if db != nil {
		defer db.Close()
		if d := cfg.Models.LogDays; d > 0 {
			if _, err := db.Prune(time.Now().AddDate(0, 0, -d)); err != nil {
				hookLog("pruning the suggestion log: %v", err)
			}
		}
	}
	exe, _ := os.Executable()
	model := ui.New(ui.Options{
		Core: core, Store: db, StoreErr: dbErr, ConfigErr: cfgErr, Version: appVersion(), Exe: exe,
		Session: session, Tab: tab,
	})
	prog = tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		n := core.Load()
		prog.Send(ui.LoadedMsg{Records: n})
		_ = core.Watcher.Run(ctx)
	}()
	final, err := prog.Run()
	if err != nil {
		fmt.Fprintf(stderr, "lookout: %v\n", err)
		return 1
	}
	if fm, ok := final.(ui.Model); ok && fm.RestartRequested() && exe != "" {
		restartExe = exe
		_ = os.Setenv(envRestorePin, fm.Pinned())
		_ = os.Setenv(envRestoreTab, fm.Tab())
	}
	return 0
}

func cmdStats(args []string, stdout, stderr io.Writer) int {
	f, ok := focusFlags("stats", args, stderr)
	if !ok {
		return 2
	}
	cfg := loadConfig(stderr)
	core := app.New(cfg, f.dir, nil)
	start := time.Now()
	n := core.Load()
	if f.session != "" {
		id, ok := core.Resolve(f.session)
		if !ok {
			fmt.Fprintf(stderr, "lookout: no single session matches %q\n", f.session)
			return 1
		}
		core.Pinned = id
	}
	now := time.Now()
	snap, sess, hasSess := core.Snapshot(now)
	var extra strategy.Extra
	if hasSess {
		if p, _, ok := limits.Pick(paths.LimitsDir(), sess.ID); ok {
			extra = ui.SuggestExtra(limits.Load(p), p == limits.FileFor(paths.LimitsDir(), sess.ID), now)
		}
	}
	sg, _ := core.Suggest(now, extra)
	h := core.Watcher.Health()

	fmt.Fprintf(stdout, "Scanned %d records in %s from %s\n", n, time.Since(start).Round(time.Millisecond), ui.ShortPath(paths.ClaudeProjectsDir()))
	fmt.Fprintf(stdout, "Parser: %d lines, %d malformed, %d degraded\n", h.Lines, h.Malformed, h.Degraded)
	if w := h.Warning(); w != "" {
		fmt.Fprintf(stdout, "WARNING: %s\n", w)
	}
	if hasSess {
		fmt.Fprintf(stdout, "\nCurrent session %s  %s  %s  last activity %s\n", ui.ShortID(sess.ID), ui.ShortPath(sess.Cwd), ui.ShortModel(sess.Model), ui.Ago(sess.LastActivity, now))
		if sg.Effort != "" {
			fmt.Fprintf(stdout, "  effort %s\n", sg.Effort)
		}
	} else {
		fmt.Fprintln(stdout, "\nNo sessions found.")
	}

	var st limits.State
	exact := false
	if p, _, ok := limits.Pick(paths.LimitsDir(), sess.ID); ok {
		st = limits.Load(p)
		exact = hasSess && p == limits.FileFor(paths.LimitsDir(), sess.ID)
	}
	printLimits(stdout, st, now)
	// Pay-as-you-go: show dollars too (estimated, except the session's).
	showCost := exact && st.PayAsYouGo()
	est := func(usd float64) string {
		if usd <= 0 {
			return "$0.00"
		}
		return "~" + limits.Money(usd)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "\twindow\tinput\toutput\tcache write\tcache read\ttotal\tmsgs\t")
	row := func(name string, w ui.UsageRow) {
		fmt.Fprintf(tw, "\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t\n", name, ui.Tokens(w.U.Input), ui.Tokens(w.U.Output), ui.Tokens(w.U.CacheCreate), ui.Tokens(w.U.CacheRead), ui.Tokens(w.U.Total()), w.N)
	}
	row("session", ui.UsageRow{U: snap.Session.Usage, N: snap.Session.Messages})
	if !snap.SessionSub.IsZero() {
		row("  subagents", ui.UsageRow{U: snap.SessionSub})
	}
	row("today", ui.UsageRow{U: snap.Today.Usage, N: snap.Today.Messages})
	row(fmt.Sprintf("last %dh", cfg.Usage.BlockHours), ui.UsageRow{U: snap.Rolling.Usage, N: snap.Rolling.Messages})
	if b := snap.Block; b != nil && b.Active {
		row("block (est.)", ui.UsageRow{U: b.Usage, N: b.Messages})
	}
	tw.Flush()
	if showCost {
		fmt.Fprintf(stdout, "\nCost: session %s (Claude Code) · today %s · last %dh %s\n", limits.Money(*st.CostUSD),
			est(snap.Today.Cost), cfg.Usage.BlockHours, est(snap.Rolling.Cost))
		fmt.Fprintln(stdout, "  ~ = estimated: pay-as-you-go sessions' tokens at Anthropic's API list prices, scaled to Claude Code's per-session cost")
	}

	fmt.Fprintf(stdout, "\nBurn: %s tok/min (%s excl. cache reads, %s output) over the last %dm\n", ui.Rate(snap.BurnPerMin), ui.Rate(snap.FreshPerMin), ui.Rate(snap.OutputPerMin), cfg.Usage.BurnWindowMinutes)
	if b := snap.Block; b != nil && b.Active {
		fmt.Fprintf(stdout, "Block (estimated): %s–%s, ends in %s, projected %s tokens at this pace\n",
			b.Start.Local().Format("15:04"), b.End.Local().Format("15:04"), ui.Dur(b.End.Sub(now)), ui.Tokens(snap.ProjectedBlock))
	}

	if len(snap.Today.ByModel) > 0 {
		fmt.Fprintln(stdout, "\nToday by model:")
		models := make([]string, 0, len(snap.Today.ByModel))
		for m := range snap.Today.ByModel {
			models = append(models, m)
		}
		sort.Slice(models, func(i, j int) bool {
			return snap.Today.ByModel[models[i]].Total() > snap.Today.ByModel[models[j]].Total()
		})
		for _, m := range models {
			u := snap.Today.ByModel[m]
			fmt.Fprintf(stdout, "  %-24s %8s total  %8s out", ui.ShortModel(m), ui.Tokens(u.Total()), ui.Tokens(u.Output))
			if showCost {
				fmt.Fprintf(stdout, "  %9s", est(snap.Today.CostByModel[m]))
			}
			fmt.Fprintln(stdout)
		}
	}
	fmt.Fprintln(stdout, "\nLast 7 days:")
	for _, d := range snap.Days {
		fmt.Fprintf(stdout, "  %s  %8s", d.Day.Format("Mon 01-02"), ui.Tokens(d.Usage.Total()))
		if showCost {
			fmt.Fprintf(stdout, "  %9s", est(d.Cost))
		}
		fmt.Fprintln(stdout)
	}

	fmt.Fprintf(stdout, "\nSuggestion: %s", strings.ToUpper(string(sg.Action)))
	if cmd := sg.Command(); cmd != "" {
		fmt.Fprintf(stdout, " → %s", cmd)
	}
	fmt.Fprintf(stdout, "\n  %s\n", sg.Reason)
	for _, s := range sg.Signals {
		fmt.Fprintf(stdout, "  [%s] %s\n", s.Direction, s.Detail)
	}
	if sg.Context.Tokens > 0 {
		fmt.Fprintf(stdout, "  context %s of %s (%.0f%%)", ui.Tokens(sg.Context.Tokens), ui.Tokens(sg.Context.Window), sg.Context.Percent)
		switch {
		case sg.Context.Reported:
			fmt.Fprint(stdout, " [reported by Claude Code]")
		case sg.Context.AutoWindow:
			fmt.Fprint(stdout, " [window auto-raised]")
		}
		fmt.Fprintln(stdout)
		if sg.Context.Message != "" {
			fmt.Fprintf(stdout, "  %s\n", sg.Context.Message)
		}
	}
	return 0
}

func printLimits(w io.Writer, st limits.State, now time.Time) {
	five, week := st.Paces(now)
	if st.PayAsYouGo() {
		fmt.Fprint(w, "\nLimits: none reported (API key or pay-as-you-go account).")
		if st.CostUSD != nil {
			fmt.Fprintf(w, " Cost of the latest session so far: %s.", limits.Money(*st.CostUSD))
		}
		fmt.Fprintln(w)
		return
	}
	if five == nil && week == nil {
		fmt.Fprintln(w, "\nLimits: no data yet. See: lookout statusline install --print")
		return
	}
	fmt.Fprintf(w, "\nLimits (from Claude Code, updated %s):\n", ui.Ago(st.Updated(), now))
	for _, x := range []struct {
		name string
		p    *limits.Pace
	}{{"5-hour", five}, {"weekly", week}} {
		if x.p != nil {
			fmt.Fprintf(w, "  %-7s %s\n", x.name, ui.PaceSummary(*x.p, now))
		}
	}
	if days := limits.LoadHistory(paths.HistoryFile()).Last(7, now); days != nil {
		fmt.Fprintln(w, "\nWeekly limit used per day:")
		for _, d := range days {
			fmt.Fprintf(w, "  %s  %s\n", d.Date().Format("Mon 01-02"), ui.DayNote(d))
		}
	}
}

func cmdConfig(args []string, stdout, stderr io.Writer) int {
	sub := "path"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path":
		fmt.Fprintln(stdout, paths.ConfigFile())
	case "init":
		wrote, err := config.WriteDefault(paths.ConfigFile())
		if err != nil {
			fmt.Fprintf(stderr, "lookout: %v\n", err)
			return 1
		}
		if wrote {
			fmt.Fprintf(stdout, "Wrote %s\n", paths.ConfigFile())
		} else {
			fmt.Fprintf(stdout, "%s already exists; left unchanged\n", paths.ConfigFile())
		}
	case "default":
		fmt.Fprint(stdout, config.DefaultTOML)
	default:
		fmt.Fprintln(stderr, "usage: lookout config init|path|default")
		return 2
	}
	return 0
}

func cmdPaths(stdout io.Writer) int {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"claude transcripts (read)", paths.ClaudeProjectsDir()},
		{"config", paths.ConfigFile()},
		{"data", paths.DataDir()},
		{"database", paths.DBFile()},
		{"backups", paths.BackupDir()},
		{"limits", paths.LimitsDir()},
		{"limit history", paths.HistoryFile()},
		{"alert state", paths.AlertsFile()},
		{"logs", paths.LogDir()},
	} {
		fmt.Fprintf(tw, "%s\t%s\n", kv[0], kv[1])
	}
	tw.Flush()
	return 0
}

// maxHookLog is the size at which hook.log is moved to hook.log.1, replacing
// the previous one, so the two together stay under about 2 MB.
const maxHookLog = 1 << 20

// hookLog appends a line to the log. Commands Claude Code runs (the status
// line) must stay quiet on stderr, so problems go here.
func hookLog(format string, a ...any) {
	_ = os.MkdirAll(paths.LogDir(), 0o700)
	name := filepath.Join(paths.LogDir(), "hook.log")
	if fi, err := os.Stat(name); err == nil && fi.Size() >= maxHookLog {
		_ = os.Rename(name, name+".1")
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

const statusLineUsage = `usage: lookout statusline [--wrap CMD] [--order after|before] [--sep TEXT] [--timeout DUR]
       lookout statusline install --print
`

func cmdStatusLine(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "install" {
		return cmdStatusLineInstall(stdout)
	}
	fs := flag.NewFlagSet("statusline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, statusLineUsage) }
	wrap := fs.String("wrap", "", "another status line command to run first; Lookout's line is added to its output")
	order := fs.String("order", "after", `where Lookout's line goes relative to the wrapped output: "after" or "before"`)
	sep := fs.String("sep", limits.DefaultWrapSeparator, "text between the wrapped output and Lookout's line")
	timeout := fs.Duration("timeout", limits.DefaultWrapTimeout, "how long the wrapped command may run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || (*order != "after" && *order != "before") {
		fs.Usage()
		return 2
	}

	// Claude Code runs this on every status line update. Stay fast, always
	// print something, never fail; problems go to the log.
	now := time.Now()
	defer func() {
		if r := recover(); r != nil {
			hookLog("statusline panic: %v", r)
			fmt.Fprintln(stdout, "lookout")
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if err != nil {
		hookLog("statusline: reading stdin: %v", err)
	}
	// Start the wrapped command first so it runs while Lookout saves.
	type result struct {
		out string
		err error
	}
	var wrapped chan result
	if *wrap != "" {
		wrapped = make(chan result, 1)
		go func() {
			out, err := limits.RunWrapped(*wrap, raw, *timeout)
			wrapped <- result{out, err}
		}()
	}

	in, err := limits.ParseInput(bytes.NewReader(raw))
	if err != nil {
		hookLog("statusline: %v", err)
	}
	file := limits.FileFor(paths.LimitsDir(), in.SessionID)
	st := limits.Load(file)
	if st.Apply(in, now) {
		if err := limits.Save(file, st); err != nil {
			hookLog("statusline save: %v", err)
		}
		limits.Prune(paths.LimitsDir(), now)
		h := limits.LoadHistory(paths.HistoryFile())
		h.Record(in, now)
		if err := limits.SaveHistory(paths.HistoryFile(), h); err != nil {
			hookLog("statusline history: %v", err)
		}
	}
	if in.Effort == "" && in.TranscriptPath != "" {
		in.Effort = transcript.LastEffort(in.TranscriptPath)
	}
	subscriber := limits.Subscriber(st, paths.LimitsDir())
	line := limits.StatusLine(in, st, now, subscriber)
	if wrapped != nil {
		r := <-wrapped
		if r.err != nil {
			hookLog("statusline --wrap %q: %v", *wrap, r.err)
		}
		line = limits.ComposeWrapped(r.out, line, *sep, *order == "before")
	}
	fmt.Fprintln(stdout, line)
	return 0
}

func cmdStatusLineInstall(stdout io.Writer) int {
	settings := filepath.Join(paths.ClaudeDir(), "settings.json")
	configured, ours := limits.StatusLineSetting(settings)
	existing, wrappable := limits.ExistingStatusLine(settings)
	fmt.Fprintf(stdout, "Lookout never edits your settings. To show your 5-hour and weekly limits, merge this into\n%s:\n\n", settings)
	if configured && !ours && wrappable {
		fmt.Fprintln(stdout, limits.WrappedSnippet(selfPath(), existing))
	} else {
		fmt.Fprintln(stdout, limits.StatusLineSnippet(selfPath()))
	}
	fmt.Fprintln(stdout, "\nClaude Code then runs it after each reply and passes it your usage limits, context use and cost. Lookout")
	fmt.Fprintln(stdout, "saves only those few numbers and prints a short line under the prompt.")
	switch {
	case ours:
		fmt.Fprintln(stdout, "\nStatus: Lookout's status line is already set up in that file.")
	case configured && wrappable:
		fmt.Fprintln(stdout, "\nNote: that file already has a statusLine. This keeps it: Lookout runs your command with the same input,")
		fmt.Fprintln(stdout, "prints its output, then adds its own line. Use --order before or --sep TEXT to change the layout.")
	case configured:
		fmt.Fprintln(stdout, "\nNote: that file already has a different statusLine. It can only have one, so this would replace it.")
	}
	return 0
}

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "lookout"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// cmdUpdate asks GitHub for the latest release, now, and says how to update.
func cmdUpdate(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: lookout update")
		return 2
	}
	cur := appVersion()
	latest, err := update.Refresh(context.Background(), paths.UpdateFile(), time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "lookout: couldn't check for updates: %v\n", err)
		return 1
	}
	switch {
	case update.Newer(cur, latest):
		fmt.Fprintf(stdout, "Lookout %s is out (you have %s).\n\nUpdate:    %s\nBinaries:  %s\n", latest, cur, update.InstallCmd, update.ReleasesURL)
		if strings.Contains(cur, "-g") || version == "dev" {
			fmt.Fprintln(stdout, "\nThis copy was built from source: pull and rebuild instead (make build or make install).")
		}
	case strings.TrimPrefix(cur, "v") == strings.TrimPrefix(latest, "v"):
		fmt.Fprintf(stdout, "You have the latest release (%s).\n", latest)
	default:
		fmt.Fprintf(stdout, "You have %s; the latest release is %s. Nothing to update.\n", cur, latest)
	}
	if !loadConfig(io.Discard).Updates.Check {
		fmt.Fprintln(stdout, "\nTo hear about new releases in the dashboard, set [updates] check = true in "+paths.ConfigFile())
	}
	return 0
}
