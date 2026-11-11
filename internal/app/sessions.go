package app

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/romero927/lookout/internal/activity"
	"github.com/romero927/lookout/internal/limits"
	"github.com/romero927/lookout/internal/paths"
	"github.com/romero927/lookout/internal/strategy"
	"github.com/romero927/lookout/internal/usage"
)

// State is what a session is doing, as far as Lookout can tell.
type State int

// Session states, from the files Claude Code keeps while it runs, or from
// the transcript when those don't say.
const (
	StateClosed   State = iota // no Claude Code process has it open
	StateIdle                  // open, waiting for your next prompt
	StateWorking               // Claude is working on a prompt
	StateWaiting               // stopped on a permission prompt or a question for you
	StateStarting              // open, but nothing has been sent yet
)

// Overview is one session as the session strip, picker and Sessions panel
// show it.
type Overview struct {
	usage.Session
	Name    string // Claude Code's name for the running process
	NameSet bool   // you gave it that name
	Running bool
	State   State
	Since   time.Time // when State began, if known
	Detail  string    // what it is doing or waiting for
	Tmux    string    // its tmux pane, if it runs in one
	Started time.Time // when the running process started

	Brief   activity.Brief
	Context strategy.ContextAdvice

	Tokens int64   // every token the session used, subagents included
	Cost   float64 // in dollars, as Claude Code reported it; only for
	// pay-as-you-go sessions: a subscription session has no cost of its own
}

// Label is the session's name: one you gave it, else its title (what it
// is about), else the name Claude Code made up, else its folder.
func (o Overview) Label() string {
	if o.Name != "" && o.NameSet {
		return o.Name
	}
	if o.Title != "" {
		return o.Title
	}
	if o.Name != "" {
		return o.Name
	}
	if b := filepath.Base(o.Cwd); o.Cwd != "" && b != "/" && b != "." {
		return b
	}
	if o.Project != "" {
		return o.Project
	}
	if len(o.ID) > 8 {
		return o.ID[:8]
	}
	return o.ID
}

// NeedsYou reports whether the session is stopped on something only you
// can answer.
func (o Overview) NeedsYou() bool { return o.State == StateWaiting }

// runningIdleAfter is how long a session without a reported status may go
// quiet in the middle of a tool call before it is shown as idle.
const runningIdleAfter = 10 * time.Minute

// Sessions lists the running sessions (oldest first, so their order holds
// still) followed by up to recent other sessions, most recently active
// first. Only sessions in the --cwd folder (or near it) are listed when a
// folder is set.
func (c *Core) Sessions(now time.Time, recent int) []Overview {
	live := map[string]LiveSession{}
	if c.LiveDir != "" {
		for _, l := range c.live.get(c.LiveDir, now) {
			if usage.DirMatch(l.Cwd, c.CwdFilter) > 0 {
				live[l.ID] = l
			}
		}
	}
	var running, closed []Overview
	seen := map[string]bool{}
	totals := c.Agg.AllTotals()
	billed := c.payAsYouGo(now)
	for _, s := range c.Agg.Sessions() {
		if usage.DirMatch(s.Cwd, c.CwdFilter) == 0 {
			continue
		}
		seen[s.ID] = true
		o := c.overview(s, now)
		o.Tokens = totals[s.ID].Tokens
		if r, ok := billed(s.ID); ok && r.USD > 0 {
			o.Cost = r.USD
		}
		if l, ok := live[s.ID]; ok {
			applyLive(&o, l, now)
			running = append(running, o)
		} else if len(closed) < recent && (o.Tokens > 0 || o.Brief.First != "") {
			// Sessions with nothing in them (only /clear, say) are left out.
			closed = append(closed, o)
		}
	}
	for id, l := range live {
		if seen[id] {
			continue
		}
		o := Overview{Session: usage.Session{ID: id, Cwd: l.Cwd}}
		applyLive(&o, l, now)
		o.State, o.Since, o.Detail = StateStarting, l.Started, "nothing sent yet"
		running = append(running, o)
	}
	sort.SliceStable(running, func(i, j int) bool {
		if !running[i].Started.Equal(running[j].Started) {
			return running[i].Started.Before(running[j].Started)
		}
		return running[i].ID < running[j].ID
	})
	return append(running, closed...)
}

// overview fills in what the transcript says about s.
func (c *Core) overview(s usage.Session, now time.Time) Overview {
	o := Overview{Session: s, State: StateClosed, Since: s.LastActivity}
	if c.Activity != nil {
		o.Brief = c.Activity.Brief(s.ID)
	}
	if c.Strategy != nil {
		st, _ := c.Strategy.State(s.ID)
		var reported *float64
		if p, ok := limits.Load(limits.FileFor(paths.LimitsDir(), s.ID)).ContextPercent(now, 30*time.Minute); ok {
			reported = &p
		}
		o.Context = strategy.Context(st, c.Cfg.Models, reported)
	}
	return o
}

// applyLive sets o's state from the running process's own report, or,
// when it gives none, from the transcript.
func applyLive(o *Overview, l LiveSession, now time.Time) {
	o.Running, o.Name, o.NameSet, o.Tmux, o.Started = true, l.Name, l.NameSet, l.Tmux, l.Started
	if o.Cwd == "" {
		o.Cwd = l.Cwd
	}
	switch l.Status {
	case "busy":
		o.State, o.Detail = StateWorking, l.Working
		if o.Detail == "" {
			o.Detail = o.Brief.Doing
		}
	case "waiting":
		o.State, o.Detail = StateWaiting, l.Blocked
		if o.Detail == "" {
			o.Detail = l.BlockedOn
		}
		if o.Detail == "" {
			o.Detail = "waiting for you"
		}
	case "idle":
		o.State = StateIdle
	default:
		// No status reported: a tool call still running means it's working.
		o.State = StateIdle
		if o.Brief.Doing != "" && now.Sub(o.LastActivity) < runningIdleAfter {
			o.State, o.Detail = StateWorking, o.Brief.Doing
		}
	}
	if !l.StatusAt.IsZero() {
		o.Since = l.StatusAt
	} else if o.Since.IsZero() {
		o.Since = l.Started
	}
}

// IsLive reports whether a Claude Code process has the session open.
func (c *Core) IsLive(id string) bool {
	if c.LiveDir == "" || id == "" {
		return false
	}
	for _, l := range c.live.get(c.LiveDir, time.Now()) {
		if l.ID == id {
			return true
		}
	}
	return false
}

// liveSession returns the running process for id, if any.
func (c *Core) liveSession(id string) (LiveSession, bool) {
	if c.LiveDir == "" || id == "" {
		return LiveSession{}, false
	}
	for _, l := range c.live.get(c.LiveDir, time.Now()) {
		if l.ID == id {
			return l, true
		}
	}
	return LiveSession{}, false
}
