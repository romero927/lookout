package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/activity"
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

func TestChangesGroupedByRepo(t *testing.T) {
	var m Model
	m.hasSess = true
	m.sess.Cwd = "/w/site"
	m.workTops = []string{"/w/site", "/w/tool"}
	m.work = []activity.WorkChange{
		{Path: "/w/tool/b.go", Status: " M"},
		{Path: "/w/site/a.js", Status: " M"},
		{Path: "/w/tool/c.go", Status: "??"},
	}
	m.committed = []activity.WorkChange{{Path: "/w/tool/d.go", Status: "M"}}

	items := m.changeItems()
	var got []string
	for _, it := range items {
		got = append(got, it.Path)
	}
	want := []string{"/w/site/a.js", "/w/tool/b.go", "/w/tool/c.go", "/w/tool/d.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v (own repo first, then others)", got, want)
	}

	text := plainText(strings.Join(m.changesList(50).lines, "\n"))
	for _, s := range []string{"  site\n", "  tool\n", "    b.go", "Committed since the session started\n  tool\n    d.go"} {
		if !strings.Contains(text, s) {
			t.Errorf("missing %q in\n%s", s, text)
		}
	}
	if strings.Count(text, "  tool\n") != 2 {
		t.Errorf("tool heading should repeat in each group\n%s", text)
	}

	// One repository: no headings.
	m.work, m.committed = m.work[1:2], nil
	if text := plainText(strings.Join(m.changesList(50).lines, "\n")); strings.Contains(text, "  site\n") {
		t.Errorf("single repo should have no heading\n%s", text)
	}
}
