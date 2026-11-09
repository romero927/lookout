// Package activity records what each Claude Code session did: which files
// it changed, and the tools it used in each turn. Everything comes from the
// session transcripts.
package activity

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// FileChange is one file a session edited.
type FileChange struct {
	Path           string
	Edits          int  // successful edit tool calls
	Failed         int  // edit calls that returned an error
	Added, Removed int  // estimated lines
	Written        bool // created or overwritten with Write
	Subagent       bool // at least one edit came from a subagent
	First, Last    time.Time
}

// Call is one tool call.
type Call struct {
	Name     string
	Summary  string // command, file or pattern
	Fail     bool
	Subagent bool
}

// Turn is everything between two prompts you typed.
type Turn struct {
	Start, End time.Time
	Prompt     string
	Command    string // slash command that opened the turn
	Model      string
	Calls      []Call
	Dropped    int // calls beyond maxCalls, not kept
	Failed     int
	Files      []string // files edited, in first-edit order
	Output     int64    // output tokens
	Reply      string   // Claude's last text in the turn
}

// Session is one session's activity.
type Session struct {
	ID    string
	Turns []Turn       // newest first
	Files []FileChange // most recently edited first
}

const (
	maxTurns  = 100
	maxCalls  = 300
	maxPrompt = 2000
	maxReply  = 2000
)

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

type pending struct {
	turn           *turn
	call           int // index in turn.calls, -1 if not kept
	file           string
	added, removed int
}

type turn struct {
	Turn
	seen  map[string]bool
	files map[string]bool
}

type session struct {
	turns   []*turn
	files   map[string]*FileChange
	pending map[string]pending // tool_use id -> call
	tools   map[string]bool    // tool_use ids already counted
	ctx     ctxState
}

// Engine tracks every session. Safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// New returns an empty Engine.
func New() *Engine { return &Engine{sessions: map[string]*session{}} }

// Observe feeds one transcript record into the engine.
func (e *Engine) Observe(r transcript.Record, src transcript.Source) {
	sub := r.Sidechain || src.IsSubagent
	sid := r.SessionID
	if src.IsSubagent || sid == "" {
		sid = src.SessionID
	}
	if sid == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[sid]
	if s == nil {
		s = &session{files: map[string]*FileChange{}, pending: map[string]pending{}, tools: map[string]bool{}}
		e.sessions[sid] = s
	}
	if !sub {
		s.ctx.observe(r)
	}
	switch r.Kind {
	case transcript.KindUser:
		if r.IsHumanPrompt() && !sub {
			s.newTurn(r)
			return
		}
		for _, tr := range r.ToolResults {
			p, ok := s.pending[tr.ToolUseID]
			if !ok {
				continue
			}
			delete(s.pending, tr.ToolUseID)
			if !tr.IsError {
				continue
			}
			if p.turn != nil {
				p.turn.Failed++
				if p.call >= 0 && p.call < len(p.turn.Calls) {
					p.turn.Calls[p.call].Fail = true
				}
			}
			if f := s.files[p.file]; f != nil {
				f.Failed++
				f.Edits--
				f.Added -= p.added
				f.Removed -= p.removed
			}
		}
	case transcript.KindAssistant:
		if r.Model == transcript.SyntheticModel {
			return
		}
		t := s.current(sub)
		if t != nil && !sub {
			if key := r.UsageKey(); !t.seen[key] {
				t.seen[key] = true
				t.Output += r.Usage.Output
			}
			if r.Model != "" {
				t.Model = r.Model
			}
			if r.Text != "" {
				t.Reply = clip(r.Text, maxReply)
			}
		}
		if t != nil && r.Timestamp.After(t.End) {
			t.End = r.Timestamp
		}
		for _, tu := range r.ToolUses {
			if tu.ID != "" {
				if s.tools[tu.ID] {
					continue
				}
				s.tools[tu.ID] = true
			}
			p := pending{turn: t, call: -1}
			if t != nil {
				if len(t.Calls) < maxCalls {
					p.call = len(t.Calls)
					t.Calls = append(t.Calls, Call{Name: tu.Name, Summary: summary(tu), Subagent: sub})
				} else {
					t.Dropped++
				}
			}
			if editTools[tu.Name] && tu.FilePath != "" {
				f := s.files[tu.FilePath]
				if f == nil {
					f = &FileChange{Path: tu.FilePath, First: r.Timestamp}
					s.files[tu.FilePath] = f
				}
				f.Edits++
				f.Added += tu.Added
				f.Removed += tu.Removed
				f.Written = f.Written || tu.Whole
				f.Subagent = f.Subagent || sub
				if r.Timestamp.After(f.Last) {
					f.Last = r.Timestamp
				}
				p.file, p.added, p.removed = tu.FilePath, tu.Added, tu.Removed
				if t != nil && !t.files[tu.FilePath] {
					t.files[tu.FilePath] = true
					t.Files = append(t.Files, tu.FilePath)
				}
			}
			if tu.ID != "" {
				s.pending[tu.ID] = p
			}
		}
	}
}

func (s *session) newTurn(r transcript.Record) {
	s.turns = append(s.turns, &turn{
		Turn: Turn{Start: r.Timestamp, End: r.Timestamp, Prompt: clip(r.Prompt, maxPrompt), Command: r.Command},
		seen: map[string]bool{}, files: map[string]bool{},
	})
	if len(s.turns) > maxTurns {
		s.turns = s.turns[len(s.turns)-maxTurns:]
	}
	// Results for calls in dropped turns will never matter.
	if len(s.pending) > 1000 {
		s.pending = map[string]pending{}
	}
}

// current is the turn records belong to. Subagent records go to the latest
// turn; main-chain records before any prompt start an untitled turn.
func (s *session) current(sub bool) *turn {
	if len(s.turns) == 0 {
		if sub {
			return nil
		}
		s.newTurn(transcript.Record{})
	}
	return s.turns[len(s.turns)-1]
}

// Session returns a copy of one session's activity.
func (e *Engine) Session(id string) Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := Session{ID: id}
	s := e.sessions[id]
	if s == nil {
		return out
	}
	for i := len(s.turns) - 1; i >= 0; i-- {
		t := s.turns[i].Turn
		t.Calls = append([]Call(nil), t.Calls...)
		t.Files = append([]string(nil), t.Files...)
		out.Turns = append(out.Turns, t)
	}
	for _, f := range s.files {
		if f.Edits > 0 || f.Failed > 0 {
			out.Files = append(out.Files, *f)
		}
	}
	sort.Slice(out.Files, func(i, j int) bool {
		if !out.Files[i].Last.Equal(out.Files[j].Last) {
			return out.Files[i].Last.After(out.Files[j].Last)
		}
		return out.Files[i].Path < out.Files[j].Path
	})
	return out
}

// summary is a one-line description of a tool call.
func summary(tu transcript.ToolUse) string {
	switch {
	case tu.Command != "":
		line, _, _ := strings.Cut(strings.TrimSpace(tu.Command), "\n")
		return line
	case tu.FilePath != "":
		return filepath.Base(tu.FilePath)
	default:
		line, _, _ := strings.Cut(strings.TrimSpace(tu.Detail), "\n")
		return line
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
