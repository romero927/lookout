package crosscheck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultPrompt is sent to Codex ahead of Claude's response.
const DefaultPrompt = `You are cross-checking a response written by another AI coding assistant (Claude Code) to its user. You only see the response text below, not the conversation or the code.

Check it for:
- factual or technical errors
- bugs, or risky or destructive commands, in any code or shell it contains
- claims of success that look unverified
- important things it missed or got backwards

Begin your answer with exactly one line: "VERDICT: OK", "VERDICT: CONCERNS" or "VERDICT: PROBLEMS".
Then give at most 5 specific findings as bullets, most important first. If it looks fine, say so in one sentence.
Do not modify any files.`

// BuildPrompt assembles exactly what is sent to Codex on stdin.
func BuildPrompt(instructions, response string) string {
	if strings.TrimSpace(instructions) == "" {
		instructions = DefaultPrompt
	}
	return instructions + "\n\n<claude_response>\n" + response + "\n</claude_response>\n"
}

// WorkerOptions configure Run.
type WorkerOptions struct {
	Dirs       Dirs
	CodexPath  string // "" looks up "codex" on PATH
	Model      string
	Prompt     string
	Timeout    time.Duration
	RepoAccess bool
}

// FindCodex locates the codex binary.
func FindCodex(configured string) (string, error) {
	if configured != "" {
		if strings.HasPrefix(configured, "~/") {
			if h, err := os.UserHomeDir(); err == nil {
				configured = filepath.Join(h, configured[2:])
			}
		}
		if fi, err := os.Stat(configured); err != nil || fi.IsDir() {
			return "", fmt.Errorf("crosscheck.codex_path %q not found", configured)
		}
		return configured, nil
	}
	p, err := exec.LookPath("codex")
	if err != nil {
		return "", errors.New(`codex not found on PATH; install the Codex CLI or set crosscheck.codex_path`)
	}
	return p, nil
}

// CodexArgs returns the codex command line used for a review.
func CodexArgs(workDir, lastMsgFile, model string) []string {
	args := []string{"exec", "--json", "--skip-git-repo-check", "--ephemeral",
		"--sandbox", "read-only", "--cd", workDir, "--output-last-message", lastMsgFile}
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "-") // prompt on stdin
}

// Run performs the review queued under id. Errors are recorded on the review.
func Run(id string, o WorkerOptions) error {
	r, err := Load(o.Dirs.Reviews, id)
	if err != nil {
		return err
	}
	jobFile := filepath.Join(o.Dirs.Jobs, id+".txt")
	defer os.Remove(jobFile)

	fail := func(msg string) error {
		r.Status, r.Error, r.FinishedAt = StatusError, msg, time.Now()
		return Save(o.Dirs.Reviews, r)
	}
	msg, err := os.ReadFile(jobFile)
	if err != nil {
		return fail("job file missing: " + err.Error())
	}
	codex, err := FindCodex(o.CodexPath)
	if err != nil {
		return fail(err.Error())
	}

	r.Status, r.StartedAt, r.PID = StatusRunning, time.Now(), os.Getpid()
	_ = Save(o.Dirs.Reviews, r)

	tmp, err := os.MkdirTemp("", "overwatch-review-")
	if err != nil {
		return fail(err.Error())
	}
	defer os.RemoveAll(tmp)
	workDir := filepath.Join(tmp, "work")
	_ = os.Mkdir(workDir, 0o700)
	if o.RepoAccess && r.Cwd != "" {
		if fi, err := os.Stat(r.Cwd); err == nil && fi.IsDir() {
			workDir = r.Cwd
		}
	}
	lastMsg := filepath.Join(tmp, "last-message.txt")

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, codex, CodexArgs(workDir, lastMsg, o.Model)...)
	cmd.Dir = workDir
	killTree(cmd)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdin = strings.NewReader(BuildPrompt(o.Prompt, string(msg)))
	var stdout, stderr capped
	stdout.max, stderr.max = 8<<20, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	ev := ParseEvents(bytes.NewReader(stdout.Bytes()))
	final := ev.LastMessage
	if b, err := os.ReadFile(lastMsg); err == nil && strings.TrimSpace(string(b)) != "" {
		final = string(b)
	}
	r.Usage = ev.Usage
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return fail(fmt.Sprintf("codex timed out after %s", timeout))
	case strings.TrimSpace(final) == "":
		reason := ev.Error
		if reason == "" && runErr != nil {
			reason = runErr.Error()
		}
		if reason == "" {
			reason = "codex returned no review"
		}
		if s := strings.TrimSpace(stderr.String()); s != "" {
			reason += ": " + lastLines(s, 3)
		}
		return fail(reason)
	}
	r.Summary = strings.TrimSpace(final)
	r.Verdict = ParseVerdict(r.Summary)
	r.Status, r.FinishedAt = StatusDone, time.Now()
	return Save(o.Dirs.Reviews, r)
}

// Events is what Overwatch extracts from `codex exec --json` output.
type Events struct {
	LastMessage string
	Usage       *CodexUsage
	Error       string
}

// ParseEvents reads Codex's JSONL event stream. Unknown events are
// ignored; both the current ("item.completed") and older ("msg") shapes
// are understood.
func ParseEvents(r io.Reader) Events {
	var ev Events
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
			Item *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage   *CodexUsage `json:"usage"`
			Message string      `json:"message"`
			Error   *struct {
				Message string `json:"message"`
			} `json:"error"`
			Msg *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"msg"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Item != nil && e.Item.Type == "agent_message" && e.Type == "item.completed":
			ev.LastMessage = e.Item.Text
		case e.Type == "turn.completed" && e.Usage != nil:
			ev.Usage = e.Usage
		case e.Type == "error" && e.Message != "":
			ev.Error = e.Message
		case e.Type == "turn.failed" && e.Error != nil:
			ev.Error = e.Error.Message
		case e.Msg != nil && e.Msg.Type == "agent_message":
			ev.LastMessage = e.Msg.Message
		case e.Msg != nil && e.Msg.Type == "error":
			ev.Error = e.Msg.Message
		}
	}
	return ev
}

var verdictRe = regexp.MustCompile(`(?im)^\W*verdict\W*[:\-–]\W*(ok|concerns|problems)\b`)

// ParseVerdict extracts OK / CONCERNS / PROBLEMS from a review.
func ParseVerdict(s string) string {
	if m := verdictRe.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// capped is a bytes.Buffer that stops growing past max.
type capped struct {
	bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.Len(); room > 0 {
		if len(p) > room {
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	}
	return len(p), nil
}
