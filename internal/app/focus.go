package app

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// focusTracker notes when you switch to a session's tmux pane, so Auto
// follows the session you click into as well as the one you last sent a
// message to. It asks tmux for the active pane at most once a second.
type focusTracker struct {
	mu    sync.Mutex
	at    time.Time            // last time tmux was asked
	pane  string               // the active pane then, e.g. "%3"
	seen  bool                 // pane holds a real answer
	since map[string]time.Time // pane -> when you switched to it
	// query returns the active pane; nil asks tmux (see activeTmuxPane).
	query func() (string, bool)
}

// get returns when you last switched to each pane, polling tmux when the
// last answer is over a second old.
func (f *focusTracker) get(now time.Time) map[string]time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.at.IsZero() && now.Sub(f.at) <= time.Second && !now.Before(f.at) {
		return f.since
	}
	f.at = now
	q := f.query
	if q == nil {
		q = activeTmuxPane
	}
	pane, ok := q()
	if !ok || pane == "" || pane == os.Getenv("TMUX_PANE") {
		// No answer, or Lookout's own pane: going from a session to
		// Lookout and back isn't a switch.
		return f.since
	}
	if f.seen && pane != f.pane {
		if f.since == nil {
			f.since = map[string]time.Time{}
		}
		f.since[pane] = now
	}
	// The pane active when Lookout starts doesn't count as a switch: you
	// may have been there for a while.
	f.pane, f.seen = pane, true
	return f.since
}

// activeTmuxPane asks tmux for the active pane of the client you used
// last, e.g. "%3".
func activeTmuxPane() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-clients", "-F", "#{client_activity} #{pane_id}").Output()
	if err != nil {
		return "", false
	}
	return lastActiveClientPane(string(out))
}

// lastActiveClientPane picks, from list-clients lines of "<activity>
// <pane>", the pane of the client with the latest activity.
func lastActiveClientPane(out string) (string, bool) {
	best, bestAt := "", int64(-1)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		at, pane, ok := strings.Cut(strings.TrimSpace(line), " ")
		n, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil || !strings.HasPrefix(pane, "%") {
			continue
		}
		if n > bestAt {
			best, bestAt = pane, n
		}
	}
	return best, best != ""
}

// paneID is the pane part of Claude Code's tmux field, e.g. "%3" from
// "main:@0.%3".
func paneID(tmux string) string {
	if i := strings.LastIndex(tmux, "%"); i >= 0 {
		return tmux[i:]
	}
	return ""
}

// focusTimes maps each running session in tmux to when you last switched
// to its pane. It is nil when no running session is in tmux, and then
// tmux isn't asked.
func (c *Core) focusTimes(now time.Time) map[string]time.Time {
	if c.LiveDir == "" {
		return nil
	}
	panes := map[string]string{} // pane -> session
	for _, l := range c.live.get(c.LiveDir, now) {
		if p := paneID(l.Tmux); p != "" {
			panes[p] = l.ID
		}
	}
	if len(panes) == 0 {
		return nil
	}
	out := map[string]time.Time{}
	for p, at := range c.focus.get(now) {
		if id, ok := panes[p]; ok && at.After(out[id]) {
			out[id] = at
		}
	}
	return out
}
