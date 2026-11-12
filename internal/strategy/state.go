// Package strategy turns session activity into a model suggestion.
//
// It only suggests; the user switches models with /model.
package strategy

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// Turn is everything that happened between two human prompts.
type Turn struct {
	Start        time.Time
	Prompt       string
	Command      string // slash command that opened the turn, if any
	Model        string
	Effort       string // /effort level of the turn's latest response
	Responses    int
	ToolCalls    int
	ToolErrors   int // failing tool results (is_error or failure text)
	TestFailures int // the subset that look like failing tests/builds
	Edits        map[string]int
	OutputTokens int64
	// Calls are this turn's tool calls in order (capped at maxCalls).
	Calls   []ToolCall
	seen    map[string]bool
	callIdx map[string]int // tool_use id -> index in Calls
}

// ToolCall is one tool call and how it went.
type ToolCall struct {
	File string // edited file, for edit tools
	Fail bool   // result was an error or looked like a failure
	Test bool   // failure looked like a test/build failure
}

const maxCalls = 200

// Active reports whether the model did anything in this turn.
func (t *Turn) Active() bool { return t.Responses > 0 }

// SessionState is the rolling per-session view used by the rules.
type SessionState struct {
	ID            string
	Turns         []*Turn // oldest first, at most maxTurns
	Model         string  // latest main-chain model
	Effort        string  // latest main-chain /effort level, "" when unknown
	LastAssistant time.Time
	Context       int64
	MaxContext    int64 // largest context seen, including pre-compaction size
	Compactions   int
	TurnCount     int // turns seen in total (Turns keeps only the latest)
}

// LastPrompt returns the most recent typed prompt.
func (s *SessionState) LastPrompt() string {
	for i := len(s.Turns) - 1; i >= 0; i-- {
		if s.Turns[i].Prompt != "" {
			return s.Turns[i].Prompt
		}
	}
	return ""
}

// clone copies the state so it can be read without the engine lock.
func (s *SessionState) clone() SessionState {
	c := *s
	c.Turns = make([]*Turn, len(s.Turns))
	for i, t := range s.Turns {
		tt := *t
		tt.Edits = make(map[string]int, len(t.Edits))
		for k, v := range t.Edits {
			tt.Edits[k] = v
		}
		tt.Calls = append([]ToolCall(nil), t.Calls...)
		tt.seen, tt.callIdx = nil, nil
		c.Turns[i] = &tt
	}
	return c
}

const maxTurns = 20

// Engine tracks state for every session it sees. Safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	sessions map[string]*SessionState
}

// NewEngine returns an empty Engine.
func NewEngine() *Engine { return &Engine{sessions: map[string]*SessionState{}} }

var editTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
}

// failRe matches tool output that looks like a failing test, build or crash.
var failRe = regexp.MustCompile(`(?m)(^--- FAIL|^FAIL\b|^FAILED\b|\b\d+ (tests? )?failed\b|\btests? failed\b|Traceback \(most recent call last\)|AssertionError|^panic: |npm ERR!|error\[E\d+\]|BUILD FAILED|compilation failed|Compilation failed)`)

// failHints are literals at least one of which appears in anything failRe
// matches. Checking them first skips the regex for almost all output.
var failHints = []string{"FAIL", "fail", "Traceback", "AssertionError", "panic: ", "npm ERR!", "error[E"}

// looksFailing reports whether tool output looks like a failing test or build.
func looksFailing(s string) bool {
	for _, h := range failHints {
		if strings.Contains(s, h) {
			return failRe.MatchString(s)
		}
	}
	return false
}

// Observe feeds one record into the engine. Subagent traffic is ignored:
// the suggestion is about the model the user is talking to.
func (e *Engine) Observe(r transcript.Record, src transcript.Source) {
	if r.Sidechain || src.IsSubagent {
		return
	}
	sid := r.SessionID
	if sid == "" {
		sid = src.SessionID
	}
	if sid == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.Kind == transcript.KindReset {
		delete(e.sessions, sid) // its transcript is read again from the start
		return
	}
	s := e.sessions[sid]
	if s == nil {
		s = &SessionState{ID: sid}
		e.sessions[sid] = s
	}
	s.observe(r)
}

func (s *SessionState) current() *Turn {
	if len(s.Turns) == 0 {
		s.newTurn(transcript.Record{})
	}
	return s.Turns[len(s.Turns)-1]
}

func (s *SessionState) newTurn(r transcript.Record) {
	s.TurnCount++
	s.Turns = append(s.Turns, &Turn{Start: r.Timestamp, Prompt: r.Prompt, Command: r.Command,
		Edits: map[string]int{}, seen: map[string]bool{}, callIdx: map[string]int{}})
	if len(s.Turns) > maxTurns {
		s.Turns = s.Turns[len(s.Turns)-maxTurns:]
	}
}

func (s *SessionState) observe(r transcript.Record) {
	switch r.Kind {
	case transcript.KindUser:
		if r.IsHumanPrompt() {
			s.newTurn(r)
			return
		}
		t := s.current()
		for _, tr := range r.ToolResults {
			failing := looksFailing(tr.Text)
			if tr.IsError || failing {
				t.ToolErrors++
			}
			if failing {
				t.TestFailures++
			}
			if i, ok := t.callIdx[tr.ToolUseID]; ok && tr.ToolUseID != "" {
				t.Calls[i].Fail = tr.IsError || failing
				t.Calls[i].Test = failing
			}
		}
	case transcript.KindAssistant:
		if r.Model == transcript.SyntheticModel {
			return
		}
		t := s.current()
		key := r.UsageKey()
		if !t.seen[key] {
			t.seen[key] = true
			t.Responses++
			t.OutputTokens += r.Usage.Output
		}
		if r.Model != "" {
			t.Model = r.Model
			t.Effort = r.Effort
			if !r.Timestamp.Before(s.LastAssistant) {
				s.Model = r.Model
				s.Effort = r.Effort
				s.LastAssistant = r.Timestamp
				if r.HasUsage {
					s.Context = r.Usage.Context()
					s.MaxContext = max(s.MaxContext, s.Context)
				}
			}
		}
		for _, tu := range r.ToolUses {
			t.ToolCalls++
			call := ToolCall{}
			if editTools[tu.Name] && tu.FilePath != "" {
				t.Edits[tu.FilePath]++
				call.File = tu.FilePath
			}
			t.addCall(tu.ID, call)
		}
	case transcript.KindCompact:
		s.Compactions++
		s.MaxContext = max(s.MaxContext, r.CompactPre)
		s.Context = r.CompactPost
	}
}

func (t *Turn) addCall(id string, c ToolCall) {
	if len(t.Calls) >= maxCalls {
		drop := len(t.Calls) - maxCalls + 1
		t.Calls = append(t.Calls[:0], t.Calls[drop:]...)
		for k, i := range t.callIdx {
			if i < drop {
				delete(t.callIdx, k)
			} else {
				t.callIdx[k] = i - drop
			}
		}
	}
	if id != "" {
		t.callIdx[id] = len(t.Calls)
	}
	t.Calls = append(t.Calls, c)
}

// State returns a copy of a session's state.
func (e *Engine) State(id string) (SessionState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return SessionState{ID: id}, false
	}
	return s.clone(), true
}
