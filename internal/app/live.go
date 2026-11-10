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
type LiveSession struct {
	ID      string
	Cwd     string
	Started time.Time
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
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
			StartedAt int64  `json:"startedAt"` // Unix milliseconds
		}
		if json.Unmarshal(b, &v) != nil || v.SessionID == "" || v.StartedAt == 0 {
			continue
		}
		if v.PID > 0 && !processAlive(v.PID) {
			continue
		}
		out = append(out, LiveSession{ID: v.SessionID, Cwd: v.Cwd, Started: time.UnixMilli(v.StartedAt)})
	}
	return out
}
