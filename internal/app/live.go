package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LiveSession is a running Claude Code process, read from the files Claude
// Code keeps in ~/.claude/sessions while it runs (one per process). They
// appear as soon as Claude Code starts, before the session writes anything
// to its transcript, which only happens once you send the first message.
//
// Newer Claude Code versions also say what the session is doing (Status:
// "busy", "idle" or "waiting", with BlockedOn saying why it waits) and give
// it a name. These fields are undocumented, so each may be empty.
type LiveSession struct {
	ID      string
	Cwd     string
	Started time.Time

	Name      string    // e.g. "lookout-3f", or one you gave it
	NameSet   bool      // you named it (Name isn't one Claude Code made up)
	Status    string    // "busy", "idle", "waiting", or "" when not reported
	StatusAt  time.Time // when Status last changed
	BlockedOn string    // why it waits: "permission prompt", "input needed", ...
	Blocked   string    // a longer description of what it waits for, if given
	Working   string    // what it is doing, if given
	Tmux      string    // its tmux pane, e.g. "main:@0.%3", if it runs in tmux
}

// liveCache re-reads the sessions folder at most once a second; the
// dashboard asks for the current session several times per refresh.
type liveCache struct {
	mu   sync.Mutex
	at   time.Time
	list []LiveSession
}

func (l *liveCache) get(dir string, now time.Time) []LiveSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at.IsZero() || now.Sub(l.at) > time.Second || now.Before(l.at) {
		l.list, l.at = readLive(dir), now
	}
	return l.list
}

// readLive lists the running sessions in dir. Files left behind by a
// process that has exited are skipped.
func readLive(dir string) []LiveSession {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []LiveSession
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var v struct {
			PID             int    `json:"pid"`
			SessionID       string `json:"sessionId"`
			Cwd             string `json:"cwd"`
			StartedAt       int64  `json:"startedAt"` // Unix milliseconds
			Name            string `json:"name"`
			NameSource      string `json:"nameSource"`
			Status          string `json:"status"`
			StatusUpdatedAt int64  `json:"statusUpdatedAt"`
			BlockedOn       string `json:"blockedOn"`
			BlockedText     string `json:"blockedText"`
			WorkingDetail   string `json:"workingDetail"`
			Tmux            string `json:"tmux"`
			ProcStart       string `json:"procStart"` // the process's start time, as the OS reports it
		}
		if json.Unmarshal(b, &v) != nil || v.SessionID == "" || v.StartedAt == 0 {
			continue
		}
		if v.PID > 0 && !processAlive(v.PID, v.ProcStart) {
			continue // ended; or its PID now belongs to another process
		}
		l := LiveSession{ID: v.SessionID, Cwd: v.Cwd, Started: time.UnixMilli(v.StartedAt),
			Name: v.Name, NameSet: v.NameSource == "user", Status: v.Status, BlockedOn: v.BlockedOn, Blocked: v.BlockedText, Working: v.WorkingDetail, Tmux: v.Tmux}
		if v.StatusUpdatedAt > 0 {
			l.StatusAt = time.UnixMilli(v.StatusUpdatedAt)
		}
		out = append(out, l)
	}
	return out
}
