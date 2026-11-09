package activity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Git is only ever run read-only: diff, status, ls-files and rev-parse, with
// optional locks off so it never rewrites the index.

const gitTimeout = 3 * time.Second

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "color.ui=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), errors.New(msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// Diff returns the uncommitted changes to one file (staged and unstaged,
// against HEAD). When there are none, it shows the file's commits since
// `since` (the session start), or a short note.
func Diff(path string, since time.Time) (diff, note string) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", "git isn't installed, so no diff."
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		return "", "The file's folder no longer exists."
	}
	if _, err := git(dir, "rev-parse", "--show-toplevel"); err != nil {
		return "", "Not in a git repository, so no diff."
	}
	base := filepath.Base(path)
	if _, err := git(dir, "ls-files", "--error-unmatch", "--", base); err != nil {
		if _, serr := os.Stat(path); serr != nil {
			return "", "The file no longer exists."
		}
		return newFileDiff(path)
	}
	d, err := git(dir, "diff", "--no-ext-diff", "HEAD", "--", base)
	if err != nil {
		// No commits yet: compare with the index.
		d, err = git(dir, "diff", "--no-ext-diff", "--", base)
	}
	if err != nil {
		return "", "git diff failed: " + err.Error()
	}
	if strings.TrimSpace(d) == "" {
		if !since.IsZero() {
			if l, err := git(dir, "log", "-p", "--no-ext-diff", "--since="+since.Format(time.RFC3339),
				"--format=commit %h  %s  (%ar)", "--", base); err == nil && strings.TrimSpace(l) != "" {
				return capLines(l, maxNewLines*2), ""
			}
		}
		return "", "No uncommitted changes, and no commits to it since the session started."
	}
	return d, ""
}

func capLines(s string, n int) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "") + fmt.Sprintf("… %d more lines\n", len(lines)-n)
}

// CommittedSince lists files changed by commits since t in the repository
// top, newest status first.
func CommittedSince(top string, t time.Time) ([]WorkChange, error) {
	if top == "" || t.IsZero() {
		return nil, nil
	}
	out, err := git(top, "log", "--since="+t.Format(time.RFC3339), "--name-status", "--no-renames", "--format=commit %H")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var res []WorkChange
	commit := ""
	for _, line := range strings.Split(out, "\n") {
		if h, ok := strings.CutPrefix(line, "commit "); ok {
			commit = h
			continue
		}
		code, name, ok := strings.Cut(line, "\t")
		if !ok || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		res = append(res, WorkChange{Path: filepath.Join(top, name), Status: code, Commit: commit})
	}
	return res, nil
}

// WorkChange is one path with uncommitted changes in a repository.
type WorkChange struct {
	Path   string // absolute
	Status string // porcelain XY code, e.g. " M", "??"
	Commit string // for committed changes: the latest commit's full hash
}

// WorkingTree lists uncommitted changes in the repository containing dir.
func WorkingTree(dir string) ([]WorkChange, string, error) {
	if dir == "" {
		return nil, "", errors.New("no folder")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "", err
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", err
	}
	top = strings.TrimSpace(top)
	out, err := git(top, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, top, err
	}
	var res []WorkChange
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		code, name := p[:2], p[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the original name follows
		}
		res = append(res, WorkChange{Path: filepath.Join(top, name), Status: code})
	}
	return res, top, nil
}

// maxNewLines caps how much of an untracked file is shown.
const maxNewLines = 500

// newFileDiff shows an untracked file as all-added lines.
func newFileDiff(path string) (diff, note string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "New file, not yet tracked by git."
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return "", "New binary file, not yet tracked by git."
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var sb strings.Builder
	fmt.Fprintf(&sb, "new file, not yet tracked by git (%d lines)\n", len(lines))
	for i, l := range lines {
		if i == maxNewLines {
			fmt.Fprintf(&sb, "… %d more lines\n", len(lines)-maxNewLines)
			break
		}
		sb.WriteString("+" + l + "\n")
	}
	return sb.String(), ""
}
