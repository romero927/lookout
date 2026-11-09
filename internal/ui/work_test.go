package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckWorkAcrossRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	plain := t.TempDir() // the session's own folder: not a repository
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	since := time.Now().Add(-time.Minute)
	run("init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "done.go"), []byte("package x\n"), 0o644)
	run("add", ".")
	run("commit", "-qm", "first")
	_ = os.WriteFile(filepath.Join(repo, "wip.go"), []byte("package x\n"), 0o644)

	sub := filepath.Join(repo, "sub")
	_ = os.Mkdir(sub, 0o755)
	msg := checkWork("k", []string{plain, repo, sub, "/no/such/dir"}, since)
	if msg.err != nil || len(msg.tops) != 1 {
		t.Fatalf("tops = %v, err = %v", msg.tops, msg.err)
	}
	if len(msg.items) != 1 || filepath.Base(msg.items[0].Path) != "wip.go" {
		t.Errorf("uncommitted = %+v", msg.items)
	}
	if len(msg.committed) != 1 || filepath.Base(msg.committed[0].Path) != "done.go" {
		t.Errorf("committed = %+v", msg.committed)
	}
	if msg := checkWork("k", []string{plain}, since); msg.err == nil {
		t.Error("no repository at all should report an error")
	}
}
