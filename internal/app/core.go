// Package app wires the watcher, usage tallies and strategy together. It is
// shared by the TUI and the `lookout stats` command.
package app

import (
	"path/filepath"
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
// else the latest (in CwdFilter, when set). When a newer Claude Code session
// has started but has no transcript yet (nothing sent so far), there is no
// current session: Lookout waits for that one instead of showing an older one.
func (c *Core) Current() (usage.Session, bool) {
	if c.Pinned != "" {
		for _, s := range c.Agg.Sessions() {
			if s.ID == c.Pinned {
				return s, true
			}
		}
	}
	s, ok := c.Agg.Current(c.CwdFilter)
	if _, waiting := c.Starting(); waiting {
		return usage.Session{}, false
	}
	return s, ok
}

// Starting returns the newest running session that hasn't sent anything yet
// when it is newer than (or a closer folder match than) the latest session
// with a transcript, so it is the one to show once it gets going. It ignores
// Pinned.
func (c *Core) Starting() (LiveSession, bool) {
	if c.LiveDir == "" {
		return LiveSession{}, false
	}
	cur, ok := c.Agg.Current(c.CwdFilter)
	curRank := 0
	if ok {
		curRank = usage.DirMatch(cur.Cwd, c.CwdFilter)
	}
	var best LiveSession
	found := false
	for _, l := range c.live.get(c.LiveDir, time.Now()) {
		r := usage.DirMatch(l.Cwd, c.CwdFilter)
		if r == 0 || r < curRank || (ok && l.ID == cur.ID) || c.Agg.Known(l.ID) {
			continue
		}
		if r == curRank && !l.Started.After(cur.LastActivity) {
			continue
		}
		if !found || l.Started.After(best.Started) {
			best, found = l, true
		}
	}
	return best, found
}

// Snapshot computes usage for the current session.
func (c *Core) Snapshot(now time.Time) (usage.Snapshot, usage.Session, bool) {
	s, ok := c.Current()
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
	return snap, s, ok
}

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
