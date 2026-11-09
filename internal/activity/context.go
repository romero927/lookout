package activity

import (
	"sort"
	"strings"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// What's in the context window, estimated from the transcript. Claude Code
// reports the total (the latest response's input, cache read and cache
// write); the parts are sized from their text, about 4 bytes per token, and
// scaled down if they add up to more than the total.

const (
	bytesPerToken = 4
	imageTokens   = 1600 // rough cost of one image
	maxCtxItems   = 10000
	maxPreview    = 240
)

// Item kinds.
const (
	KindPrompt   = "prompt"   // something you typed
	KindReply    = "reply"    // Claude's text
	KindCall     = "call"     // a tool call's input (e.g. a file Claude wrote)
	KindResult   = "result"   // a tool's output
	KindInjected = "injected" // text Claude Code added (command output, reminders)
	KindSummary  = "summary"  // the summary written by the last /compact
)

// CtxItem is one thing in the context.
type CtxItem struct {
	Kind    string
	Tool    string // for calls and results
	Label   string
	Bytes   int
	Images  int
	At      time.Time
	Preview string // the start of the text, for the details view
}

func (it CtxItem) estimate() int64 {
	return int64(it.Bytes/bytesPerToken) + int64(it.Images*imageTokens)
}

type ctxState struct {
	items     []CtxItem
	baseSet   bool
	base      int64 // context of the first response: startup overhead
	latest    int64
	latestAt  time.Time
	compacted bool
	since     time.Time // session start or last compaction
	calls     map[string]CtxItem
}

func (c *ctxState) add(it CtxItem) {
	if it.Bytes <= 0 && it.Images == 0 {
		return
	}
	c.items = append(c.items, it)
	if len(c.items) > maxCtxItems {
		c.items = c.items[len(c.items)-maxCtxItems:]
	}
}

// observe follows the main conversation only: subagents have their own.
func (c *ctxState) observe(r transcript.Record) {
	if c.calls == nil {
		c.calls = map[string]CtxItem{}
	}
	switch r.Kind {
	case transcript.KindCompact:
		*c = ctxState{compacted: true, since: r.Timestamp, calls: map[string]CtxItem{}}
	case transcript.KindQueued:
		line, _, _ := strings.Cut(r.Prompt, "\n")
		c.add(CtxItem{Kind: KindPrompt, Label: line, Bytes: r.Size, At: r.Timestamp, Preview: preview(r.Prompt)})
	case transcript.KindUser:
		if c.since.IsZero() {
			c.since = r.Timestamp
		}
		for _, tr := range r.ToolResults {
			call := c.calls[tr.ToolUseID]
			delete(c.calls, tr.ToolUseID)
			c.add(CtxItem{Kind: KindResult, Tool: orUnknown(call.Tool), Label: call.Label, Bytes: tr.Size, Images: tr.Images, At: r.Timestamp, Preview: preview(tr.Text)})
		}
		if len(r.ToolResults) > 0 {
			return
		}
		switch {
		case r.CompactSummary:
			c.add(CtxItem{Kind: KindSummary, Label: "Summary from the last /compact", Bytes: r.Size, At: r.Timestamp, Preview: preview(r.Prompt)})
		case r.IsHumanPrompt() && r.Prompt != "":
			line, _, _ := strings.Cut(r.Prompt, "\n")
			c.add(CtxItem{Kind: KindPrompt, Label: line, Bytes: r.Size, At: r.Timestamp, Preview: preview(r.Prompt)})
		case r.Command != "":
			c.add(CtxItem{Kind: KindInjected, Label: r.Command, Bytes: r.Size, At: r.Timestamp})
		default:
			c.add(CtxItem{Kind: KindInjected, Label: "added by Claude Code", Bytes: r.Size, At: r.Timestamp})
		}
	case transcript.KindAssistant:
		if r.Model == transcript.SyntheticModel {
			return
		}
		if c.since.IsZero() {
			c.since = r.Timestamp
		}
		if r.HasUsage {
			ctx := r.Usage.Context()
			if !c.baseSet && ctx > 0 {
				var before int64
				for _, it := range c.items {
					before += it.estimate()
				}
				c.base, c.baseSet = max(0, ctx-before), true
			}
			if !r.Timestamp.Before(c.latestAt) {
				c.latest, c.latestAt = ctx, r.Timestamp
			}
		}
		if r.Text != "" {
			line, _, _ := strings.Cut(strings.TrimSpace(r.Text), "\n")
			c.add(CtxItem{Kind: KindReply, Label: line, Bytes: len(r.Text), At: r.Timestamp, Preview: preview(r.Text)})
		}
		for _, tu := range r.ToolUses {
			it := CtxItem{Kind: KindCall, Tool: orUnknown(tu.Name), Label: summary(tu), Bytes: tu.InputSize, At: r.Timestamp}
			if tu.ID != "" {
				c.calls[tu.ID] = it
			}
			c.add(it)
		}
	}
}

// preview keeps the start of a text, cut at a rune boundary.
func preview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxPreview {
		return s
	}
	r := []rune(s[:maxPreview])
	if len(r) > 0 {
		r = r[:len(r)-1] // the last rune may be cut
	}
	return string(r) + "…"
}

func orUnknown(s string) string {
	if s == "" {
		return "tool"
	}
	return s
}

// Category is one slice of the context.
type Category struct {
	Name   string
	Tokens int64
	Count  int
}

// Sized is one item with its estimated share of the context.
type Sized struct {
	CtxItem
	Tokens int64
}

// ContextView is the breakdown shown in the Context panel.
type ContextView struct {
	Total     int64 // as reported with the latest response
	At        time.Time
	Since     time.Time // session start, or the last compaction
	Compacted bool
	Startup   int64 // system prompt, tools, CLAUDE.md, memory (+ summary after /compact)
	Parts     []Category
	Other     int64 // not accounted for: attachments, images, estimate error
	Largest   []Sized
	Items     int  // all items counted
	Estimated bool // the parts were scaled to fit the total
}

func (c *ctxState) view(top int) ContextView {
	v := ContextView{Total: c.latest, At: c.latestAt, Since: c.since, Compacted: c.compacted, Startup: c.base}
	if c.latest == 0 {
		return v
	}
	var sum int64
	for _, it := range c.items {
		sum += it.estimate()
	}
	avail := max(0, c.latest-c.base)
	scale := 1.0
	if sum > avail && sum > 0 {
		scale = float64(avail) / float64(sum)
		v.Estimated = true
	} else {
		v.Other = avail - sum
	}
	byName := map[string]*Category{}
	var order []string
	sized := make([]Sized, 0, len(c.items))
	for _, it := range c.items {
		t := int64(float64(it.estimate()) * scale)
		name := categoryName(it)
		cat := byName[name]
		if cat == nil {
			cat = &Category{Name: name}
			byName[name] = cat
			order = append(order, name)
		}
		cat.Tokens += t
		cat.Count++
		sized = append(sized, Sized{it, t})
	}
	for _, n := range order {
		v.Parts = append(v.Parts, *byName[n])
	}
	sort.SliceStable(v.Parts, func(i, j int) bool { return v.Parts[i].Tokens > v.Parts[j].Tokens })
	sort.SliceStable(sized, func(i, j int) bool { return sized[i].Tokens > sized[j].Tokens })
	v.Items = len(sized)
	if len(sized) > top {
		sized = sized[:top]
	}
	v.Largest = sized
	return v
}

func categoryName(it CtxItem) string {
	switch it.Kind {
	case KindPrompt:
		return "Your prompts"
	case KindReply:
		return "Claude's replies"
	case KindCall:
		return "Tool inputs (commands, files Claude wrote)"
	case KindResult:
		return it.Tool + " output"
	default:
		return "Added by Claude Code"
	}
}

// Context returns what is in a session's context window, with the largest
// top items.
func (e *Engine) Context(id string, top int) ContextView {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[id]
	if s == nil {
		return ContextView{}
	}
	return s.ctx.view(top)
}
