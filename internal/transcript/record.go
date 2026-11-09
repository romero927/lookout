// Package transcript parses Claude Code's session transcripts
// (~/.claude/projects/<project>/<session>.jsonl).
//
// The format is undocumented and changes between Claude Code releases, so
// the parser decodes only the handful of fields Lookout needs, ignores
// everything else, and never panics on bad input. Each line gets a Status
// so callers can notice when parsing starts failing.
//
// Observed layout (Claude Code 2.1.x):
//
//	{"type":"assistant","uuid":..,"sessionId":..,"timestamp":"2026-..Z",
//	 "isSidechain":false,"cwd":..,
//	 "message":{"id":"msg_..","model":"claude-opus-..","stop_reason":..,
//	            "content":[ONE block],
//	            "usage":{"input_tokens":N,"output_tokens":N,
//	                     "cache_creation_input_tokens":N,"cache_read_input_tokens":N,
//	                     "output_tokens_details":{"thinking_tokens":N}}}}
//
// One API response is written as several lines (one per content block),
// each repeating the same usage. Consumers must dedupe by MessageID.
package transcript

import "time"

// Kind is the coarse record category Lookout cares about.
type Kind uint8

const (
	KindOther     Kind = iota // any record type Lookout doesn't use
	KindAssistant             // a model response line (carries usage)
	KindUser                  // a human prompt or a tool result
	KindCompact               // a compaction boundary
	KindQueued                // a message you sent while Claude was working
)

func (k Kind) String() string {
	switch k {
	case KindAssistant:
		return "assistant"
	case KindUser:
		return "user"
	case KindCompact:
		return "compact"
	case KindQueued:
		return "queued"
	default:
		return "other"
	}
}

// Status describes how well a line parsed.
type Status uint8

const (
	StatusOK        Status = iota // parsed fine (including records Lookout ignores)
	StatusBlank                   // empty line; not counted
	StatusMalformed               // not valid JSON
	StatusDegraded                // valid JSON but expected fields missing or mistyped
)

// SyntheticModel is the placeholder model Claude Code uses for locally
// generated messages. They carry zero usage.
const SyntheticModel = "<synthetic>"

// Usage is the token accounting of one API response.
type Usage struct {
	Input       int64 // uncached input tokens
	Output      int64 // output tokens (includes thinking)
	CacheCreate int64 // tokens written to the prompt cache
	CacheRead   int64 // tokens read from the prompt cache
	Thinking    int64 // portion of Output spent thinking, when reported
}

// Total is every token category added together.
func (u Usage) Total() int64 { return u.Input + u.Output + u.CacheCreate + u.CacheRead }

// Context approximates the context size sent with this request.
func (u Usage) Context() int64 { return u.Input + u.CacheCreate + u.CacheRead }

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheCreate += o.CacheCreate
	u.CacheRead += o.CacheRead
	u.Thinking += o.Thinking
}

// Max keeps the field-wise maximum of u and o. Used when merging the
// duplicate lines of one response.
func (u *Usage) Max(o Usage) {
	u.Input = max(u.Input, o.Input)
	u.Output = max(u.Output, o.Output)
	u.CacheCreate = max(u.CacheCreate, o.CacheCreate)
	u.CacheRead = max(u.CacheRead, o.CacheRead)
	u.Thinking = max(u.Thinking, o.Thinking)
}

// IsZero reports whether no tokens were recorded.
func (u Usage) IsZero() bool { return u.Total() == 0 }

// ToolUse is a tool call made by the assistant.
type ToolUse struct {
	ID       string
	Name     string
	FilePath string // file_path / notebook_path for file tools
	Command  string // Bash command, truncated
	// Detail is a short description for other tools: a search pattern,
	// a task description, a URL. Truncated.
	Detail string
	// Added / Removed are the lines an edit tool changes (an estimate:
	// unchanged lines at the start and end of the replaced text are not
	// counted). Write counts the whole new content as added.
	Added, Removed int
	Whole          bool // Write: the whole file was (re)written
	InputSize      int  // bytes of the call's JSON input (it stays in context)
}

// ToolResult is the outcome of a tool call, as reported back to the model.
type ToolResult struct {
	ToolUseID string
	IsError   bool
	Text      string // truncated to MaxResultText
	Size      int    // bytes of the full result (text only when it has images)
	Images    int    // image blocks in the result
}

// MaxResultText caps how much tool output is kept per result.
const MaxResultText = 4000

// Record is the parsed subset of one transcript line.
type Record struct {
	Kind      Kind
	Type      string // raw "type" value
	UUID      string
	SessionID string
	AgentID   string
	Sidechain bool // subagent traffic
	Meta      bool // isMeta: injected, not typed by the human
	Timestamp time.Time
	Cwd       string

	// Assistant fields.
	MessageID  string
	RequestID  string
	Model      string
	Usage      Usage
	HasUsage   bool
	StopReason string
	Text       string // text content blocks (not thinking)
	ToolUses   []ToolUse

	// User fields.
	Prompt      string // text the human typed; empty for tool results and meta
	Command     string // slash command, e.g. "/model", when the line records one
	ToolResults []ToolResult
	// Size is the byte length of a user message's content: a prompt, or
	// text Claude Code injected (command output, reminders).
	Size int

	// Compaction fields.
	CompactPre  int64
	CompactPost int64
}

// IsHumanPrompt reports whether the record is a prompt the user typed.
func (r Record) IsHumanPrompt() bool {
	return r.Kind == KindUser && !r.Meta && !r.Sidechain && (r.Prompt != "" || r.Command != "")
}

// UsageKey is the dedupe key for usage: one per API response.
func (r Record) UsageKey() string {
	switch {
	case r.MessageID != "":
		return "m:" + r.MessageID
	case r.RequestID != "":
		return "r:" + r.RequestID
	default:
		return "u:" + r.UUID
	}
}
