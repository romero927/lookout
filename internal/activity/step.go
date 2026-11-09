package activity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxStepOutput caps how much of a tool call's output LoadSteps keeps.
const MaxStepOutput = 20000

// StepDetail is a tool call's full input and output. Activity keeps only a
// summary of each step; this is read from the transcript when a step is
// opened, so long sessions don't hold every output in memory.
type StepDetail struct {
	Input  map[string]any
	Output string
	Error  bool
	Done   bool // the output was found (the call has finished)
}

// SessionFiles returns a session's transcript files: the main one, then any
// subagent transcripts.
func SessionFiles(projectsDir, project, sessionID string) []string {
	if project == "" || sessionID == "" {
		return nil
	}
	dir := filepath.Join(projectsDir, project)
	files := []string{filepath.Join(dir, sessionID+".jsonl")}
	subs, _ := filepath.Glob(filepath.Join(dir, sessionID, "subagents", "*.jsonl"))
	sort.Strings(subs)
	return append(files, subs...)
}

// LoadSteps reads the input and output of the tool calls with the given IDs
// from the transcript files. IDs not found are left out.
func LoadSteps(files, ids []string) map[string]StepDetail {
	want := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	out := map[string]StepDetail{}
	for _, f := range files {
		if len(want) == 0 {
			break
		}
		scanSteps(f, want, out)
	}
	return out
}

type stepLine struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type stepBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

func scanSteps(path string, want map[string]bool, out map[string]StepDetail) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for len(want) > 0 {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			matchSteps(line, want, out)
		}
		if err == io.EOF || (err != nil && len(line) == 0) {
			return
		}
	}
}

func matchSteps(line []byte, want map[string]bool, out map[string]StepDetail) {
	hit := false
	for id := range want {
		if bytes.Contains(line, []byte(id)) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	var l stepLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	var blocks []stepBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch {
		case b.Type == "tool_use" && want[b.ID]:
			d := out[b.ID]
			d.Input = b.Input
			out[b.ID] = d
		case b.Type == "tool_result" && want[b.ToolUseID]:
			d := out[b.ToolUseID]
			d.Output, d.Error, d.Done = resultText(b.Content), b.IsError, true
			out[b.ToolUseID] = d
			delete(want, b.ToolUseID) // the result comes after the call
		}
	}
}

// resultText is a tool result's text: a plain string, or the text blocks of
// a list. Other blocks (images) are noted, not kept.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return clip(s, MaxStepOutput)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var sb []string
	for _, p := range parts {
		switch p.Type {
		case "text":
			sb = append(sb, p.Text)
		case "":
		default:
			sb = append(sb, "["+p.Type+"]")
		}
	}
	return clip(strings.Join(sb, "\n"), MaxStepOutput)
}

// FileEdit is one edit Claude's tools made to a file, from the transcript.
type FileEdit struct {
	Tool    string // Edit, MultiEdit or Write
	At      string // the record's timestamp, as written
	Old     []string
	New     []string // for Write: the whole new content
	Content bool     // true for Write
}

// LoadFileEdits reads the successful edits made to path from the
// transcript files, oldest first. It lets Changes show what Claude did to a
// file that git can't diff.
func LoadFileEdits(files []string, path string) []FileEdit {
	type pend struct {
		e  FileEdit
		id string
	}
	var all []pend
	failed := map[string]bool{}
	// Match the path as it is written inside JSON (backslashes escaped).
	quoted, _ := json.Marshal(path)
	needle := quoted[1 : len(quoted)-1]
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		r := bufio.NewReaderSize(fh, 1<<16)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 && (bytes.Contains(line, needle) || bytes.Contains(line, []byte(`"is_error":true`))) {
				var l struct {
					Timestamp string `json:"timestamp"`
					stepLine
				}
				if json.Unmarshal(line, &l) == nil {
					var blocks []struct {
						stepBlock
						Name string `json:"name"`
					}
					if json.Unmarshal(l.Message.Content, &blocks) == nil {
						for _, b := range blocks {
							switch {
							case b.Type == "tool_result" && b.IsError:
								failed[b.ToolUseID] = true
							case b.Type == "tool_use" && editTools[b.Name]:
								if p, _ := b.Input["file_path"].(string); p == path {
									for _, e := range editsOf(b.Name, b.Input) {
										e.At = l.Timestamp
										all = append(all, pend{e, b.ID})
									}
								}
							}
						}
					}
				}
			}
			if err != nil {
				break
			}
		}
		fh.Close()
	}
	var out []FileEdit
	for _, p := range all {
		if !failed[p.id] {
			out = append(out, p.e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

func editsOf(tool string, in map[string]any) []FileEdit {
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	lines := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	}
	switch tool {
	case "Edit":
		return []FileEdit{{Tool: tool, Old: lines(str(in, "old_string")), New: lines(str(in, "new_string"))}}
	case "MultiEdit":
		var out []FileEdit
		list, _ := in["edits"].([]any)
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				out = append(out, FileEdit{Tool: tool, Old: lines(str(m, "old_string")), New: lines(str(m, "new_string"))})
			}
		}
		return out
	case "Write":
		return []FileEdit{{Tool: tool, New: lines(str(in, "content")), Content: true}}
	}
	return nil
}
