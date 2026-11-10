package limits

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Limits are kept per Claude Code session (one small JSON file each), because
// two sessions can report different numbers: they may be signed in to
// different accounts, or one may still hold older figures. Per-session files
// also mean concurrent status line runs never overwrite each other.

// keepFiles is how long an idle session's file is kept.
const keepFiles = 8 * 24 * time.Hour

// FileFor is the file holding one session's limits.
func FileFor(dir, sessionID string) string {
	id := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return -1
	}, sessionID)
	if id == "" {
		id = "unknown"
	}
	return filepath.Join(dir, id+".json")
}

// Pick returns the file for sessionID if it exists, else the most recently
// updated one. ok is false when there are none.
func Pick(dir, sessionID string) (path string, mod time.Time, ok bool) {
	if sessionID != "" {
		p := FileFor(dir, sessionID)
		if info, err := os.Stat(p); err == nil {
			return p, info.ModTime(), true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !ok || info.ModTime().After(mod) {
			path, mod, ok = filepath.Join(dir, e.Name()), info.ModTime(), true
		}
	}
	return path, mod, ok
}

// Prune removes files for sessions idle longer than keepFiles.
func Prune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > keepFiles {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Subscriber reports whether the session behind st should be treated as a
// Claude.ai subscription. A session that has received limits is one. Before
// its first reply a session has neither limits nor cost, so the other saved
// sessions decide. Once it has a cost but still no limits it is a
// pay-as-you-go session (an API key, or an enterprise account billed by
// usage), even when other sessions on this computer, on another account,
// had limits.
func Subscriber(st State, dir string) bool {
	if st.HasData() {
		return true
	}
	if st.CostUSD != nil && *st.CostUSD > 0 {
		return false
	}
	return HasLimits(dir)
}

// HasLimits reports whether any saved session has ever received usage limits
// from Claude Code, which means this account is a Claude.ai subscriber.
func HasLimits(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if Load(filepath.Join(dir, e.Name())).HasData() {
			return true
		}
	}
	return false
}
