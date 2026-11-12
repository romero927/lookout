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
// message to. tmux is asked for the active pane at most once a second, in
// the background: callers get the last answer and never wait on tmux.
type focusTracker struct {
	mu      sync.Mutex
	at      time.Time            // when tmux was last asked
	polling bool                 // a background poll is running
	pane    string               // the active pane then, e.g. "%3"
	seen    bool                 // pane holds a real answer
	since   map[string]time.Time // pane -> when you switched to it
	// query returns the active pane; nil asks tmux (see activeTmuxPane).
	query func() (string, bool)
}

// get returns when you last switched to each pane in live (a copy), and
// starts a background poll of tmux when the last one is over a second old.
// Panes not in live are forgotten once their switch is a minute old.
func (f *focusTracker) get(now time.Time, live map[string]bool) map[string]time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.polling && (f.at.IsZero() || now.Sub(f.at) > time.Second || now.Before(f.at)) {
		f.at, f.polling = now, true
		q := f.query
		if q == nil {
			q = activeTmuxPane
		}
		go func() {
			pane, ok := q()
			f.update(pane, ok, time.Now())
		}()
	}
	out := map[string]time.Time{}
	for p, at := range f.since {
		switch {
		case live[p]:
			out[p] = at
		case now.Sub(at) > time.Minute:
			delete(f.since, p)
		}
	}
	return out
}

// update records tmux's answer: the active pane, or ok false when tmux
// gave none.
func (f *focusTracker) update(pane string, ok bool, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polling = false
	if !ok || pane == "" || pane == os.Getenv("TMUX_PANE") {
		// No answer, or Lookout's own pane: going from a session to
		// Lookout and back isn't a switch.
		return
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

// TmuxPane is the pane ID in Claude Code's tmux field, e.g. "%3" from
// "main:@0.%3", or "" when it names none.
func TmuxPane(ref string) string {
	if i := strings.LastIndex(ref, "%"); i >= 0 {
		if _, err := strconv.Atoi(ref[i+1:]); err == nil {
			return ref[i:]
		}
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
	live := map[string]bool{}
	for _, l := range c.live.get(c.LiveDir, now) {
		if p := TmuxPane(l.Tmux); p != "" {
			panes[p], live[p] = l.ID, true
		}
	}
	if len(panes) == 0 {
		return nil
	}
	out := map[string]time.Time{}
	for p, at := range c.focus.get(now, live) {
		if id := panes[p]; at.After(out[id]) {
			out[id] = at
		}
	}
	return out
}
