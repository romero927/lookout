package transcript

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"time"
)

// ParseLine parses one JSONL line. It never panics. Unknown record types
// return KindOther with StatusOK; unknown fields are ignored.
func ParseLine(line []byte) (rec Record, st Status) {
	defer func() {
		if recover() != nil {
			rec, st = Record{}, StatusMalformed
		}
	}()

	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Record{}, StatusBlank
	}
	if line[0] != '{' {
		return Record{}, StatusMalformed
	}

	degraded := false
	var typ, subtype, ts []byte
	var message, compact, attachment []byte
	setStr := func(dst *string, v []byte) {
		s, ok := str(v)
		if !ok && string(v) != "null" {
			degraded = true
		}
		*dst = s
	}
	setBool := func(dst *bool, v []byte) {
		b, ok := boolean(v)
		if !ok {
			degraded = true
		}
		*dst = b
	}
	ok := eachField(line, func(k, v []byte) {
		switch string(k) {
		case "type":
			typ = v
		case "subtype":
			subtype = v
		case "timestamp":
			ts = v
		case "message":
			message = v
		case "compactMetadata":
			compact = v
		case "attachment":
			attachment = v
		case "uuid":
			setStr(&rec.UUID, v)
		case "sessionId":
			setStr(&rec.SessionID, v)
		case "agentId":
			setStr(&rec.AgentID, v)
		case "cwd":
			setStr(&rec.Cwd, v)
		case "requestId":
			setStr(&rec.RequestID, v)
		case "isSidechain":
			setBool(&rec.Sidechain, v)
		case "isMeta":
			setBool(&rec.Meta, v)
		case "isCompactSummary":
			setBool(&rec.CompactSummary, v)
		}
	})
	if !ok {
		return Record{}, StatusMalformed
	}

	if typ != nil {
		setStr(&rec.Type, typ)
	}
	if ts != nil {
		s, _ := str(ts)
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			rec.Timestamp = t
		} else {
			degraded = true
		}
	}

	switch rec.Type {
	case "assistant":
		rec.Kind = KindAssistant
		if !parseAssistant(&rec, message) {
			degraded = true
		}
	case "user":
		rec.Kind = KindUser
		if !parseUser(&rec, message) {
			degraded = true
		}
	case "system":
		if s, _ := str(subtype); s == "compact_boundary" {
			rec.Kind = KindCompact
			if compact != nil {
				eachField(compact, func(k, v []byte) {
					switch string(k) {
					case "preTokens":
						rec.CompactPre = integer(v)
					case "postTokens":
						rec.CompactPost = integer(v)
					}
				})
			}
		}
	case "attachment":
		parseQueued(&rec, attachment)
	case "":
		degraded = true
	}

	if rec.Kind != KindOther && rec.Timestamp.IsZero() {
		degraded = true
	}
	if degraded {
		return rec, StatusDegraded
	}
	return rec, StatusOK
}

// parseAssistant fills assistant fields. It returns false when the fields
// Lookout depends on (model, usage) are missing or mistyped.
func parseAssistant(rec *Record, msg []byte) bool {
	ok := true
	var content, usage []byte
	if msg == nil || !eachField(msg, func(k, v []byte) {
		var good bool
		switch string(k) {
		case "id":
			rec.MessageID, good = str(v)
		case "model":
			rec.Model, good = str(v)
		case "stop_reason":
			rec.StopReason, _ = str(v)
			good = true
		case "content":
			content, good = v, true
		case "usage":
			usage, good = v, true
		default:
			good = true
		}
		if !good {
			ok = false
		}
	}) {
		return false
	}
	if usage != nil {
		eachField(usage, func(k, v []byte) {
			switch string(k) {
			case "input_tokens":
				rec.Usage.Input, rec.HasUsage = integer(v), true
			case "output_tokens":
				rec.Usage.Output, rec.HasUsage = integer(v), true
			case "cache_creation_input_tokens":
				rec.Usage.CacheCreate = integer(v)
			case "cache_read_input_tokens":
				rec.Usage.CacheRead = integer(v)
			case "output_tokens_details":
				eachField(v, func(k, v []byte) {
					if string(k) == "thinking_tokens" {
						rec.Usage.Thinking = integer(v)
					}
				})
			}
		})
	}

	var texts []string
	eachElem(content, func(b []byte) {
		var typ, text, thinking, sig, id, name, input []byte
		if !eachField(b, func(k, v []byte) {
			switch string(k) {
			case "type":
				typ = v
			case "text":
				text = v
			case "thinking":
				thinking = v
			case "signature":
				sig = v
			case "id":
				id = v
			case "name":
				name = v
			case "input":
				input = v
			}
		}) {
			return
		}
		switch t, _ := str(typ); t {
		case "text":
			if s, _ := str(text); s != "" {
				texts = append(texts, s)
			}
		case "thinking":
			// The short updates Claude Code shows while Claude works are
			// stored as thinking blocks marked as narration.
			if g, _ := str(sig); isNarration(g) {
				if s, _ := str(thinking); strings.TrimSpace(s) != "" {
					texts = append(texts, strings.TrimSpace(s))
				}
			}
		case "tool_use":
			tu := ToolUse{}
			tu.ID, _ = str(id)
			tu.Name, _ = str(name)
			tu.InputSize = len(input)
			var oldS, newS, content string
			var edits []byte
			eachField(input, func(k, v []byte) {
				switch string(k) {
				case "file_path":
					tu.FilePath, _ = str(v)
				case "notebook_path":
					if tu.FilePath == "" {
						tu.FilePath, _ = str(v)
					}
				case "command":
					tu.Command, _ = strPrefix(v, 500)
				case "pattern", "description", "url", "query", "path", "skill":
					if tu.Detail == "" {
						tu.Detail, _ = strPrefix(v, 200)
					}
				case "old_string":
					oldS, _ = str(v)
				case "new_string", "new_source":
					newS, _ = str(v)
				case "content":
					content, _ = str(v)
				case "edits":
					edits = v
				}
			})
			switch tu.Name {
			case "Edit", "NotebookEdit":
				tu.Added, tu.Removed = LineChange(oldS, newS)
			case "Write":
				tu.Added, tu.Whole = countLines(content), true
			case "MultiEdit":
				eachElem(edits, func(e []byte) {
					var o, n string
					eachField(e, func(k, v []byte) {
						switch string(k) {
						case "old_string":
							o, _ = str(v)
						case "new_string":
							n, _ = str(v)
						}
					})
					a, r := LineChange(o, n)
					tu.Added += a
					tu.Removed += r
				})
			}
			rec.ToolUses = append(rec.ToolUses, tu)
		}
	})
	rec.Text = strings.Join(texts, "\n")

	if rec.Model == SyntheticModel {
		return ok
	}
	return ok && rec.Model != "" && rec.HasUsage
}

var commandNameRe = regexp.MustCompile(`<command-name>\s*(/?[\w:.-]+)\s*</command-name>`)

// parseUser fills prompt / tool-result fields.
func parseUser(rec *Record, msg []byte) bool {
	var content []byte
	if msg == nil || !eachField(msg, func(k, v []byte) {
		if string(k) == "content" {
			content = v
		}
	}) || len(content) == 0 {
		return false
	}
	rec.Size = len(content)
	switch content[0] {
	case '"':
		s, ok := str(content)
		if !ok {
			return false
		}
		setPrompt(rec, s)
		return true
	case '[':
	default:
		return false
	}
	var texts []string
	eachElem(content, func(b []byte) {
		var typ, text, toolUseID, isError, inner []byte
		if !eachField(b, func(k, v []byte) {
			switch string(k) {
			case "type":
				typ = v
			case "text":
				text = v
			case "tool_use_id":
				toolUseID = v
			case "is_error":
				isError = v
			case "content":
				inner = v
			}
		}) {
			return
		}
		switch t, _ := str(typ); t {
		case "text":
			s, _ := str(text)
			texts = append(texts, s)
		case "tool_result":
			tr := ToolResult{}
			tr.ToolUseID, _ = str(toolUseID)
			tr.IsError, _ = boolean(isError)
			tr.Text = contentText(inner, MaxResultText)
			tr.Size = len(inner)
			if n := bytes.Count(inner, []byte(`"type":"image"`)); n > 0 {
				tr.Images = n
				tr.Size = len(contentText(inner, 1<<20))
			}
			rec.ToolResults = append(rec.ToolResults, tr)
		}
	})
	if len(rec.ToolResults) == 0 && len(texts) > 0 {
		setPrompt(rec, strings.Join(texts, "\n"))
	}
	return true
}

// setPrompt classifies a user text: a slash command, a typed prompt, or
// injected markup (which is ignored).
func setPrompt(rec *Record, s string) {
	if m := commandNameRe.FindStringSubmatch(s); m != nil {
		cmd := m[1]
		if !strings.HasPrefix(cmd, "/") {
			cmd = "/" + cmd
		}
		rec.Command = cmd
		return
	}
	t := strings.TrimSpace(s)
	if rec.Meta || strings.HasPrefix(t, "<") {
		// Injected content: bash output, task notifications, caveats, etc.
		return
	}
	rec.Prompt = t
}

// contentText flattens a tool_result content value (string or text
// blocks), decoding at most max bytes.
func contentText(c []byte, max int) string {
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		s, _ := strPrefix(c, max)
		return s
	}
	var parts []string
	n := 0
	eachElem(c, func(b []byte) {
		if n >= max {
			return
		}
		var typ, text []byte
		eachField(b, func(k, v []byte) {
			switch string(k) {
			case "type":
				typ = v
			case "text":
				text = v
			}
		})
		if t, _ := str(typ); t == "text" {
			s, _ := strPrefix(text, max-n)
			parts = append(parts, s)
			n += len(s) + 1
		}
	})
	return string(cutRune([]byte(strings.Join(parts, "\n")), max))
}

// LineChange estimates the lines added and removed when old is replaced by
// new: lines shared at the start and end are not counted.
func LineChange(old, new string) (added, removed int) {
	if old == new {
		return 0, 0
	}
	a, b := splitLines(old), splitLines(new)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	return len(b), len(a)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func countLines(s string) int { return len(splitLines(s)) }

// parseQueued recognises a message the user typed while Claude was working.
// Claude Code records it as an attachment of type "queued_command"; other
// attachments (reminders, file notices) are left as KindOther.
func parseQueued(rec *Record, att []byte) {
	if att == nil {
		return
	}
	var typ, prompt, origin []byte
	eachField(att, func(k, v []byte) {
		switch string(k) {
		case "type":
			typ = v
		case "prompt":
			prompt = v
		case "origin":
			origin = v
		}
	})
	if t, _ := str(typ); t != "queued_command" {
		return
	}
	if origin != nil {
		var kind []byte
		eachField(origin, func(k, v []byte) {
			if string(k) == "kind" {
				kind = v
			}
		})
		if k, _ := str(kind); k != "" && k != "human" {
			return // queued by a tool or a notification, not typed
		}
	}
	text := ""
	if len(prompt) > 0 && prompt[0] == '"' {
		text, _ = str(prompt)
	} else {
		text = contentText(prompt, 1<<16)
	}
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	rec.Kind, rec.Prompt, rec.Size = KindQueued, text, len(prompt)
}

// isNarration reports whether a thinking block's signature marks it as
// narration: text Claude Code shows the user, unlike hidden thinking. The
// signature is otherwise opaque; only its start is decoded to find the mark.
// An unrecognised format counts as hidden.
func isNarration(sig string) bool {
	if len(sig) < 8 {
		return false
	}
	n := min(len(sig), 64) / 4 * 4
	b, err := base64.StdEncoding.DecodeString(sig[:n])
	return err == nil && bytes.Contains(b, []byte("narration"))
}
