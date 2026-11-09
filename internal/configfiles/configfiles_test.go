package configfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	proj := filepath.Join(root, "work", "app")
	write(t, filepath.Join(claude, "settings.json"), `{"model":"opus"}`)
	write(t, filepath.Join(claude, ".credentials.json"), `secret`)
	write(t, filepath.Join(claude, "projects", "-work-app", "memory", "MEMORY.md"), "- note")
	write(t, filepath.Join(proj, "CLAUDE.md"), "# app")

	files := Discover(Sources{ClaudeDir: claude, ProjectDirs: []string{proj, proj}})
	byLabel := map[string]File{}
	for _, f := range files {
		byLabel[string(f.Group)+"|"+f.Label] = f
		if strings.Contains(f.Path, "credentials") || strings.Contains(f.Path, "auth.json") {
			t.Fatalf("credential file listed: %s", f.Path)
		}
	}
	tests := []struct {
		label  string
		exists bool
		listed bool
	}{
		{"Claude|settings.json", true, true},
		{"Claude|CLAUDE.md (global)", false, true}, // missing globals are still shown
		{"Project · app|CLAUDE.md", true, true},
		{"Project · app|CLAUDE.local.md", false, false}, // missing project files are hidden
		{"Memory · app|MEMORY.md", true, true},
		{"Claude|settings.local.json", false, false},
	}
	for _, tt := range tests {
		f, ok := byLabel[tt.label]
		if ok != tt.listed || (ok && f.Exists != tt.exists) {
			t.Errorf("%s: listed=%v exists=%v; want listed=%v exists=%v", tt.label, ok, f.Exists, tt.listed, tt.exists)
		}
	}
	if n := strings.Count(strings.Join(labels(files), ","), "Project · app|CLAUDE.md,"); n != 1 {
		t.Errorf("duplicate project listed %d times", n)
	}
}

func labels(fs []File) []string {
	var out []string
	for _, f := range fs {
		out = append(out, string(f.Group)+"|"+f.Label)
	}
	return out
}

func TestReadRefusesCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".credentials.json")
	write(t, p, "x")
	if _, err := Read(p); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"ANTHROPIC_API_KEY": "sk-abc"`, `"ANTHROPIC_API_KEY": "••••••"`},
		{`github_token = 'ghp_123'`, `github_token = '••••••'`},
		{`export MY_SECRET=hunter2`, `export MY_SECRET=••••••`},
		{`"model": "opus"`, `"model": "opus"`},
		{`"maxTokens": 5`, `"maxTokens": 5`},
		{`MAX_THINKING_TOKENS=1000`, `MAX_THINKING_TOKENS=1000`},
		{`"CLAUDE_CODE_OAUTH_TOKEN": "abc"`, `"CLAUDE_CODE_OAUTH_TOKEN": "••••••"`},
		{`"apiKeyHelper": ""`, `"apiKeyHelper": ""`},
		{"password: abc # comment", "password: •••••• # comment"},
	}
	for _, tt := range tests {
		if got := Mask(tt.in); got != tt.want {
			t.Errorf("Mask(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "src", "CLAUDE.md")
	write(t, p, "original")
	now := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	b, err := Backup(p, filepath.Join(dir, "backups"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(b), "20260110-100000.000") {
		t.Fatalf("backup name %s lacks timestamp", b)
	}
	got, _ := os.ReadFile(b)
	if string(got) != "original" {
		t.Fatalf("backup content %q", got)
	}
	if !Unchanged(p, b) {
		t.Fatal("expected unchanged")
	}
	write(t, p, "edited")
	if Unchanged(p, b) {
		t.Fatal("expected changed")
	}
	if _, err := Backup(p, filepath.Join(dir, "backups"), now); err == nil {
		t.Fatal("same-timestamp backup must not overwrite")
	}
}
