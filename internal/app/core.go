// Package app wires the watcher, usage tallies and strategy together. It is
// shared by the TUI and the `lookout stats` command.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/usage"
	"github.com/romero927/lookout/internal/watch"
)

// Core holds Lookout's in-memory state.
type Core struct {
	Cfg      config.Config
	Agg      *usage.Aggregator
	Strategy *strategy.Engine
	Activity *activity.Engine
	Watcher  *watch.Watcher
	// CwdFilter restricts the "current session" to one working directory.
	CwdFilter string
	// Pinned, when set, is the session to show instead of the latest one.
	// Only the UI goroutine changes it.
	Pinned string

	// LiveDir is where Claude Code lists its running sessions; empty
	// turns the check off.
	LiveDir string
	live    liveCache

	ctxMu   sync.Mutex
	ctxSeen map[string]reportedCtx // see reportedContext

	snapMu  sync.Mutex
	snap    usage.Snapshot
	snapID  string
	snapRev int
	snapAt  time.Time
	snapOK  bool

	billedMu sync.Mutex
	billed   func(string) (limits.Reported, bool) // pay-as-you-go sessions, cached
	billedAt time.Time
}

// New builds a Core. onChange is called (debounced) when new records arrive.
func New(cfg config.Config, cwdFilter string, onChange func()) *Core {
	c := &Core{
		Cfg:       cfg,
		Agg:       usage.New(),
		Strategy:  strategy.NewEngine(),
		Activity:  activity.New(),
		CwdFilter: cwdFilter,
		LiveDir:   paths.ClaudeSessionsDir(),
	}
	if len(cfg.Usage.PriceWeights) > 0 {
		prices := make(map[string]usage.Price, len(cfg.Usage.PriceWeights))
		for k, w := range cfg.Usage.PriceWeights {
			prices[k] = usage.Price{Input: w.Input, Output: w.Output, CacheWrite: w.CacheWrite, CacheWrite1h: w.CacheWrite1h, CacheRead: w.CacheRead}
		}
		c.Agg.SetPrices(prices)
	}
	maxAge := time.Duration(cfg.Usage.HistoryDays) * 24 * time.Hour
	c.Watcher = watch.New(paths.ClaudeProjectsDir(), maxAge, c.handle, onChange)
	return c
}

func (c *Core) handle(r transcript.Record, src transcript.Source) {
	c.Agg.Add(r, src)
	c.Strategy.Observe(r, src)
	c.Activity.Observe(r, src)
}

// Load performs the initial scan.
func (c *Core) Load() int { return c.Watcher.Scan() }

// Current returns the session in focus: the pinned one if it still exists,
// else the one Lookout follows (see Followed). When a newer Claude Code
// session has started but has no transcript yet (nothing sent so far), or
// the pinned session is open but hasn't sent anything, there is no current
// session: Lookout waits for that one instead of showing an older one.
func (c *Core) Current() (usage.Session, bool) {
	if c.Pinned != "" {
		for _, s := range c.Agg.Sessions() {
			if s.ID == c.Pinned {
				return s, true
			}
		}
		if c.IsLive(c.Pinned) {
			return usage.Session{}, false
		}
	}
	if s, ok := c.NeedsYou(); ok {
		return s, true
	}
	s, ok := c.Latest()
	if _, waiting := c.Starting(); waiting {
		return usage.Session{}, false
	}
	return s, ok
}

// Followed is the session Lookout shows when nothing is pinned: one that
// needs you if any does, else the latest (see Latest). It doesn't wait for
// newly started sessions as Current does.
func (c *Core) Followed() (usage.Session, bool) {
	if s, ok := c.NeedsYou(); ok {
		return s, true
	}
	return c.Latest()
}

// LiveKnown reports whether Claude Code lists its running sessions, so a
// session missing from that list has ended. Older versions don't, and then
// Lookout can't tell a closed session from a running one.
func (c *Core) LiveKnown() bool {
	if c.LiveDir == "" {
		return false
	}
	st, err := os.Stat(c.LiveDir)
	return err == nil && st.IsDir()
}

// NeedsYou returns the running session (in the --cwd folder, when set)
// that most recently stopped on a permission prompt or a question for you.
// Lookout shows it until you answer, then goes back to the latest.
func (c *Core) NeedsYou() (usage.Session, bool) {
	if c.LiveDir == "" {
		return usage.Session{}, false
	}
	var best LiveSession
	found := false
	for _, l := range c.live.get(c.LiveDir, time.Now()) {
		if l.Status != "waiting" || usage.DirMatch(l.Cwd, c.CwdFilter) == 0 || !c.Agg.Known(l.ID) {
			continue
		}
		if !found || l.StatusAt.After(best.StatusAt) {
			best, found = l, true
		}
	}
	if !found {
		return usage.Session{}, false
	}
	for _, s := range c.Agg.Sessions() {
		if s.ID == best.ID {
			return s, true
		}
	}
	return usage.Session{}, false
}

// Latest is the session Lookout follows when nothing is pinned: among the
// sessions in (or closest to) the --cwd folder, the one you last sent a
// message to. Sessions that are busy on their own don't take over, so with
// several running the view stays on the one you're talking to. Once you
// close a session, Lookout moves on to a running one; it falls back to
// closed sessions only when none is running.
func (c *Core) Latest() (usage.Session, bool) {
	var running map[string]bool
	if c.LiveKnown() {
		running = map[string]bool{}
		for _, l := range c.live.get(c.LiveDir, time.Now()) {
			if usage.DirMatch(l.Cwd, c.CwdFilter) > 0 && c.Agg.Known(l.ID) {
				running[l.ID] = true
			}
		}
		if len(running) == 0 {
			running = nil
		}
	}
	var best usage.Session
	var bestAt time.Time
	bestRank := 0
	for _, s := range c.Agg.Sessions() { // most recently active first
		r := usage.DirMatch(s.Cwd, c.CwdFilter)
		if r == 0 || (running != nil && !running[s.ID]) {
			continue
		}
		at := c.attention(s)
		if r > bestRank || (r == bestRank && at.After(bestAt)) {
			best, bestAt, bestRank = s, at, r
		}
	}
	return best, bestRank > 0
}

// attention is when you last turned to s: your last message to it, or its
// last activity when no message is known.
func (c *Core) attention(s usage.Session) time.Time {
	if c.Activity != nil {
		if t := c.Activity.Brief(s.ID).Prompted; !t.IsZero() {
			return t
		}
	}
	return s.LastActivity
}

// Starting returns the newest running session that hasn't sent anything yet
// when it started after you last sent a message to the followed session
// (or is a closer folder match), so it is the one to show once it gets
// going. It ignores Pinned.
func (c *Core) Starting() (LiveSession, bool) {
	if c.LiveDir == "" {
		return LiveSession{}, false
	}
	cur, ok := c.Latest()
	curRank := 0
	var curAt time.Time
	if ok {
		curRank = usage.DirMatch(cur.Cwd, c.CwdFilter)
		curAt = c.attention(cur)
	}
	var best LiveSession
	found := false
	for _, l := range c.live.get(c.LiveDir, time.Now()) {
		r := usage.DirMatch(l.Cwd, c.CwdFilter)
		if r == 0 || r < curRank || (ok && l.ID == cur.ID) || c.Agg.Known(l.ID) {
			continue
		}
		if r == curRank && !l.Started.After(curAt) {
			continue
		}
		if !found || l.Started.After(best.Started) {
			best, found = l, true
		}
	}
	return best, found
}

// Waiting returns the open session Lookout is waiting on to send its first
// message: the pinned one, or a newly started one (see Starting).
func (c *Core) Waiting() (LiveSession, bool) {
	if c.Pinned != "" && !c.Agg.Known(c.Pinned) {
		if l, ok := c.liveSession(c.Pinned); ok {
			return l, true
		}
	}
	if c.Pinned != "" {
		return LiveSession{}, false
	}
	return c.Starting()
}

// Resolve finds the session whose ID is id or starts with it, among
// sessions with a transcript and running ones. It reports false when none
// or several match.
func (c *Core) Resolve(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	match := map[string]bool{}
	for _, s := range c.Agg.Sessions() {
		if s.ID == id {
			return id, true
		}
		if strings.HasPrefix(s.ID, id) {
			match[s.ID] = true
		}
	}
	if c.LiveDir != "" {
		for _, l := range c.live.get(c.LiveDir, time.Now()) {
			if l.ID == id {
				return id, true
			}
			if strings.HasPrefix(l.ID, id) {
				match[l.ID] = true
			}
		}
	}
	if len(match) != 1 {
		return "", false
	}
	for m := range match {
		return m, true
	}
	return "", false
}

// Snapshot computes usage for the current session.
func (c *Core) Snapshot(now time.Time) (usage.Snapshot, usage.Session, bool) {
	s, ok := c.Current()
	// Adding up every response takes a few milliseconds. With no new usage,
	// the same session and a recent result, reuse it: its figures (today,
	// the last 5 hours, the recent pace) barely move in a few seconds.
	c.snapMu.Lock()
	defer c.snapMu.Unlock()
	if rev := c.Agg.Rev(); c.snapOK && rev == c.snapRev && s.ID == c.snapID &&
		now.Sub(c.snapAt) >= 0 && now.Sub(c.snapAt) < snapshotReuse {
		return c.snap, s, ok
	}
	rev := c.Agg.Rev()
	snap := c.Agg.Snapshot(now, usage.Options{
		SessionID:   s.ID,
		BurnWindow:  time.Duration(c.Cfg.Usage.BurnWindowMinutes) * time.Minute,
		BlockLength: time.Duration(c.Cfg.Usage.BlockHours) * time.Hour,
		Days:        7,
		Billed: func(sid string) (float64, time.Time, bool) {
			r, ok := c.payAsYouGo(now)(sid)
			return r.USD, r.At, ok
		},
	})
	c.snap, c.snapID, c.snapRev, c.snapAt, c.snapOK = snap, s.ID, rev, now, true
	return snap, s, ok
}

// snapshotReuse is how long Snapshot reuses a result while no new usage
// arrives.
const snapshotReuse = 5 * time.Second

// payAsYouGo returns the test for pay-as-you-go sessions, re-reading the
// saved limit files at most every 10 seconds.
func (c *Core) payAsYouGo(now time.Time) func(string) (limits.Reported, bool) {
	c.billedMu.Lock()
	defer c.billedMu.Unlock()
	if c.billed == nil || now.Sub(c.billedAt) > 10*time.Second || now.Before(c.billedAt) {
		c.billed, c.billedAt = limits.PayAsYouGoSessions(paths.LimitsDir()), now
	}
	return c.billed
}

// Suggest evaluates the Strategy rules for the current session.
func (c *Core) Suggest(now time.Time, x strategy.Extra) (strategy.Suggestion, strategy.SessionState) {
	s, ok := c.Current()
	if !ok {
		return strategy.Suggestion{Action: strategy.Keep, Reason: "No Claude Code sessions found yet.", At: now}, strategy.SessionState{}
	}
	st, _ := c.Strategy.State(s.ID)
	return strategy.Evaluate(st, c.Cfg.Models, now, x), st
}

// ProjectNames maps transcript project directory names to the base name of
// their working directory.
func (c *Core) ProjectNames() map[string]string {
	out := map[string]string{}
	for _, s := range c.Agg.Sessions() {
		if s.Project != "" && s.Cwd != "" && out[s.Project] == "" {
			out[s.Project] = filepath.Base(s.Cwd)
		}
	}
	return out
}

// ProjectDirs returns working directories of recent sessions, newest first.
func (c *Core) ProjectDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range c.Agg.Sessions() {
		if s.Cwd != "" && !seen[s.Cwd] {
			seen[s.Cwd] = true
			out = append(out, s.Cwd)
		}
	}
	return out
}
