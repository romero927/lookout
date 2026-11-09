package crosscheck

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// HookInput is the subset of Claude Code's Stop hook input Lookout uses.
type HookInput struct {
	SessionID            string `json:"session_id"`
	TranscriptPath       string `json:"transcript_path"`
	Cwd                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// Dirs are where the hook and worker keep state.
type Dirs struct {
	Flag    string
	Reviews string
	Jobs    string
}

// HookOptions configure HandleStop.
type HookOptions struct {
	Dirs        Dirs
	MaxChars    int
	MaxParallel int
	Timeout     time.Duration
	Now         func() time.Time
	// Spawn starts the detached worker for a review id. It must not wait.
	Spawn func(id string) error
}

const maxHookInput = 16 << 20

// HandleStop processes one Stop hook invocation. It returns the queued
// review id, or "" when nothing was queued. It never blocks on Codex.
func HandleStop(stdin io.Reader, o HookOptions) (string, error) {
	if !Enabled(o.Dirs.Flag) {
		// Drain stdin so Claude Code never sees a broken pipe.
		_, _ = io.Copy(io.Discard, io.LimitReader(stdin, maxHookInput))
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil {
		return "", err
	}
	var in HookInput
	if err := json.Unmarshal(b, &in); err != nil {
		return "", fmt.Errorf("stop hook input: %w", err)
	}
	if in.HookEventName != "" && in.HookEventName != "Stop" {
		return "", nil
	}

	msg := in.LastAssistantMessage
	if strings.TrimSpace(msg) == "" && in.TranscriptPath != "" {
		msg = LastAssistantText(in.TranscriptPath)
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", nil
	}

	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	t := now()
	r := Review{
		ID:        NewID(t),
		SessionID: in.SessionID,
		Cwd:       in.Cwd,
		Status:    StatusQueued,
		CreatedAt: t,
		Excerpt:   excerpt(msg, 280),
	}
	sent, truncated := Truncate(msg, o.MaxChars)
	r.SentChars, r.Truncated = len(sent), truncated

	if o.MaxParallel > 0 && InFlight(o.Dirs.Reviews, o.Timeout+time.Minute, t) >= o.MaxParallel {
		r.Status = StatusSkipped
		r.Error = fmt.Sprintf("skipped: %d reviews already running (crosscheck.max_parallel)", o.MaxParallel)
		r.FinishedAt = t
		return "", Save(o.Dirs.Reviews, r)
	}

	if err := os.MkdirAll(o.Dirs.Jobs, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(o.Dirs.Jobs, r.ID+".txt"), []byte(sent), 0o600); err != nil {
		return "", err
	}
	if err := Save(o.Dirs.Reviews, r); err != nil {
		return "", err
	}
	if o.Spawn != nil {
		if err := o.Spawn(r.ID); err != nil {
			r.Status, r.Error, r.FinishedAt = StatusError, "could not start worker: "+err.Error(), now()
			_ = Save(o.Dirs.Reviews, r)
			return "", err
		}
	}
	return r.ID, nil
}

// LastAssistantText returns the text Claude wrote after the latest human
// prompt in a transcript (fallback when the hook input lacks it).
func LastAssistantText(path string) string {
	var parts []string
	_ = transcript.ReadAll(path, func(r transcript.Record) {
		if r.Sidechain {
			return
		}
		if r.IsHumanPrompt() {
			parts = parts[:0]
			return
		}
		if r.Kind == transcript.KindAssistant && r.Text != "" && r.Model != transcript.SyntheticModel {
			parts = append(parts, r.Text)
		}
	})
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// Truncate cuts s to at most n bytes on a rune boundary.
func Truncate(s string, n int) (string, bool) {
	if n <= 0 || len(s) <= n {
		return s, false
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n], true
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if t, cut := Truncate(s, n); cut {
		return t + "…"
	}
	return s
}
