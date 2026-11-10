package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/paths"
)

const statusJSON = `{"session_id":"s1","model":{"display_name":"Opus 5.5"},"context_window":{"used_percentage":12}}`

func statusEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOKOUT_DATA_DIR", t.TempDir())
	t.Setenv("LOOKOUT_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
}

func runStatus(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(append([]string{"statusline"}, args...), strings.NewReader(statusJSON), &out, &errOut)
	if errOut.Len() > 0 {
		t.Errorf("stderr should stay empty: %q", errOut.String())
	}
	return strings.TrimRight(out.String(), "\n"), code
}

func TestStatusLineWrap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell commands")
	}
	statusEnv(t)
	ours := "Opus 5.5 · ctx 12%"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"plain", nil, ours},
		{"wrap gets same stdin", []string{"--wrap", `grep -o '"session_id":"s1"'`}, `"session_id":"s1" · ` + ours},
		{"before with separator", []string{"--wrap", "echo mine", "--order", "before", "--sep", " | "}, ours + " | mine"},
		{"wrapped fails", []string{"--wrap", "exit 1"}, ours},
		{"wrapped missing", []string{"--wrap", "/nonexistent/statusline-cmd"}, ours},
		{"wrapped times out", []string{"--wrap", "sleep 10", "--timeout", "200ms"}, ours},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got, code := runStatus(t, tt.args...)
			if code != 0 || got != tt.want {
				t.Fatalf("got %q (exit %d), want %q", got, code, tt.want)
			}
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("took %s", d)
			}
		})
	}
	// Failures are logged, not printed.
	b, _ := os.ReadFile(filepath.Join(os.Getenv("LOOKOUT_DATA_DIR"), "logs", "hook.log"))
	if !strings.Contains(string(b), "timed out") {
		t.Errorf("hook.log should record the timeout:\n%s", b)
	}
}

func TestStatusLineBadFlags(t *testing.T) {
	statusEnv(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"statusline", "--order", "sideways"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestStatusLineInstallWraps(t *testing.T) {
	statusEnv(t)
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	var out bytes.Buffer

	run([]string{"statusline", "install", "--print"}, nil, &out, &out)
	if strings.Contains(out.String(), "--wrap") {
		t.Fatalf("no existing status line, so no wrap:\n%s", out.String())
	}

	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"~/bin/line.sh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"statusline", "install", "--print"}, nil, &out, &out)
	if !strings.Contains(out.String(), `statusline --wrap '~/bin/line.sh'`) || !strings.Contains(out.String(), "This keeps it") {
		t.Fatalf("existing status line should be wrapped:\n%s", out.String())
	}

	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"lookout statusline --wrap x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"statusline", "install", "--print"}, nil, &out, &out)
	if !strings.Contains(out.String(), "already set up") || strings.Contains(out.String(), "--wrap") {
		t.Fatalf("Lookout's own line should not be wrapped again:\n%s", out.String())
	}
}

// Every file or folder Lookout writes must be listed by `lookout paths`.
func TestPathsListsEverything(t *testing.T) {
	statusEnv(t)
	var out bytes.Buffer
	if code := run([]string{"paths"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, p := range []string{
		paths.ConfigFile(), paths.DataDir(), paths.DBFile(), paths.BackupDir(),
		paths.LimitsDir(), paths.HistoryFile(), paths.AlertsFile(), paths.LogDir(),
	} {
		if !strings.Contains(out.String(), p) {
			t.Errorf("lookout paths is missing %s:\n%s", p, out.String())
		}
	}
}

func TestHookLogRotates(t *testing.T) {
	statusEnv(t)
	dir := paths.LogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "hook.log")
	if err := os.WriteFile(log, bytes.Repeat([]byte("x"), maxHookLog), 0o600); err != nil {
		t.Fatal(err)
	}
	hookLog("after rotation")
	old, err := os.Stat(log + ".1")
	if err != nil || old.Size() != maxHookLog {
		t.Fatalf("hook.log.1: %v %v", old, err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "after rotation") || len(b) > 200 {
		t.Fatalf("new hook.log = %q", b)
	}
}
