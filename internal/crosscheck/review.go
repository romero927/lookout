// Package crosscheck runs optional Codex cross-check reviews of Claude's
// responses. Claude Code's Stop hook runs `overwatch hook stop`, which queues
// a review and starts a detached worker, then exits at once. The worker runs
// `codex exec --json` and saves the result for the TUI.
//
// Cross-check is off unless the flag file exists.
package crosscheck

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Review statuses.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusError   = "error"
	StatusSkipped = "skipped"
)

// CodexUsage is the token usage Codex reported for a review.
type CodexUsage struct {
	Input       int64 `json:"input_tokens"`
	CachedInput int64 `json:"cached_input_tokens"`
	Output      int64 `json:"output_tokens"`
}

// Review is one cross-check, stored as <ReviewsDir>/<ID>.json.
type Review struct {
	ID         string      `json:"id"`
	SessionID  string      `json:"session_id"`
	Cwd        string      `json:"cwd"`
	Status     string      `json:"status"`
	CreatedAt  time.Time   `json:"created_at"`
	StartedAt  time.Time   `json:"started_at,omitempty"`
	FinishedAt time.Time   `json:"finished_at,omitempty"`
	Verdict    string      `json:"verdict,omitempty"` // OK, CONCERNS, PROBLEMS
	Summary    string      `json:"summary,omitempty"` // Codex's final message
	Error      string      `json:"error,omitempty"`
	Excerpt    string      `json:"excerpt"` // start of the reviewed response
	SentChars  int         `json:"sent_chars"`
	Truncated  bool        `json:"truncated"`
	PID        int         `json:"pid,omitempty"`
	Usage      *CodexUsage `json:"codex_usage,omitempty"`
}

// Finished reports whether the review reached a final state.
func (r Review) Finished() bool {
	return r.Status == StatusDone || r.Status == StatusError || r.Status == StatusSkipped
}

// NewID returns a sortable unique review id.
func NewID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return now.UTC().Format("20060102T150405.000Z") + "-" + hex.EncodeToString(b)
}

// Save writes r atomically.
func Save(dir string, r Review) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, r.ID+".json"))
}

// Load reads one review.
func Load(dir, id string) (Review, error) {
	var r Review
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}

// List returns up to limit reviews, newest first. Unreadable files are skipped.
func List(dir string, limit int) ([]Review, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			names = append(names, strings.TrimSuffix(n, ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var out []Review
	for _, id := range names {
		if limit > 0 && len(out) >= limit {
			break
		}
		if r, err := Load(dir, id); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// InFlight counts reviews still queued or running that started recently
// enough to be alive (older ones are assumed dead).
func InFlight(dir string, within time.Duration, now time.Time) int {
	rs, _ := List(dir, 50)
	n := 0
	for _, r := range rs {
		if !r.Finished() && now.Sub(r.CreatedAt) < within {
			n++
		}
	}
	return n
}

// Enabled reports whether cross-check mode is on.
func Enabled(flagPath string) bool {
	_, err := os.Stat(flagPath)
	return err == nil
}

// SetEnabled turns cross-check mode on or off.
func SetEnabled(flagPath string, on bool) error {
	if !on {
		err := os.Remove(flagPath)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(flagPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(flagPath, []byte("on\n"), 0o600)
}

// Toggle flips cross-check mode and returns the new state.
func Toggle(flagPath string) (bool, error) {
	on := !Enabled(flagPath)
	return on, SetEnabled(flagPath, on)
}
