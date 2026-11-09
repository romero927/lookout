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
