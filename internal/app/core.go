// Package app wires the watcher, telemetry and strategy together. It is
// shared by the TUI and the `lookout stats` command.
package app

import (
	"path/filepath"
	"time"

	"github.com/romero927/lookout/internal/config"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/telemetry"
	"github.com/romero927/lookout/internal/transcript"
	"github.com/romero927/lookout/internal/watch"
)

// Core holds Lookout's in-memory state.
type Core struct {
	Cfg      config.Config
	Agg      *telemetry.Aggregator
	Strategy *strategy.Engine
	Watcher  *watch.Watcher
	// CwdFilter restricts the "current session" to one working directory.
	CwdFilter string
}

// New builds a Core. onChange is called (debounced) when new records arrive.
func New(cfg config.Config, cwdFilter string, onChange func()) *Core {
	c := &Core{
		Cfg:       cfg,
		Agg:       telemetry.New(),
		Strategy:  strategy.NewEngine(),
		CwdFilter: cwdFilter,
	}
	maxAge := time.Duration(cfg.Usage.HistoryDays) * 24 * time.Hour
	c.Watcher = watch.New(paths.ClaudeProjectsDir(), maxAge, c.handle, onChange)
	return c
}

func (c *Core) handle(r transcript.Record, src transcript.Source) {
	c.Agg.Add(r, src)
	c.Strategy.Observe(r, src)
}

// Load performs the initial scan.
func (c *Core) Load() int { return c.Watcher.Scan() }

// Current returns the session in focus.
func (c *Core) Current() (telemetry.Session, bool) { return c.Agg.Current(c.CwdFilter) }

// Snapshot computes telemetry for the current session.
func (c *Core) Snapshot(now time.Time) (telemetry.Snapshot, telemetry.Session, bool) {
	s, ok := c.Current()
	snap := c.Agg.Snapshot(now, telemetry.Options{
		SessionID:   s.ID,
		BurnWindow:  time.Duration(c.Cfg.Usage.BurnWindowMinutes) * time.Minute,
		BlockLength: time.Duration(c.Cfg.Usage.BlockHours) * time.Hour,
		Days:        7,
	})
	return snap, s, ok
}

// Suggest evaluates the Strategy rules for the current session.
func (c *Core) Suggest(now time.Time) (strategy.Suggestion, strategy.SessionState) {
	s, ok := c.Current()
	if !ok {
		return strategy.Suggestion{Action: strategy.Keep, Reason: "No Claude Code sessions found yet.", At: now}, strategy.SessionState{}
	}
	st, _ := c.Strategy.State(s.ID)
	return strategy.Evaluate(st, c.Cfg.Models, now), st
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
