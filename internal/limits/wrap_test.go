package limits

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipWithoutSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell commands")
	}
}

func TestRunWrappedPassesStdin(t *testing.T) {
	skipWithoutSh(t)
	out, err := RunWrapped("cat; echo", []byte(`{"a":1}`), time.Second)
	if err != nil || out != `{"a":1}` {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestRunWrappedFailure(t *testing.T) {
	skipWithoutSh(t)
	// Output from a command that exits non-zero is kept.
	out, err := RunWrapped("echo partial; exit 3", nil, time.Second)
	if err == nil || out != "partial" {
		t.Fatalf("got %q, %v", out, err)
	}
	out, err = RunWrapped("", nil, time.Second)
	if err == nil || out != "" {
		t.Fatalf("empty command: got %q, %v", out, err)
	}
}

func TestRunWrappedTimeout(t *testing.T) {
	skipWithoutSh(t)
	start := time.Now()
	// The child sleep keeps stdout open after sh is killed.
	out, err := RunWrapped("echo early; sleep 10", nil, 200*time.Millisecond)
	if err == nil || out != "" {
		t.Fatalf("got %q, %v", out, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("timeout took %s", d)
	}
}

func TestComposeWrapped(t *testing.T) {
	tests := []struct {
		wrapped, ours, sep string
		before             bool
		want               string
	}{
		{"", "lk", " · ", false, "lk"},
		{"mine", "lk", " · ", false, "mine · lk"},
		{"mine", "lk", " | ", true, "lk | mine"},
		{"a\nb", "lk", " · ", false, "a\nb · lk"},
	}
	for _, tt := range tests {
		if got := ComposeWrapped(tt.wrapped, tt.ours, tt.sep, tt.before); got != tt.want {
			t.Errorf("%q %q: got %q want %q", tt.wrapped, tt.ours, got, tt.want)
		}
	}
}

func TestWrappedSnippet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	write := func(s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"statusLine":{"type":"command","command":"~/bin/my line.sh 'x'","padding":0}}`)
	sl, ok := ExistingStatusLine(p)
	if !ok {
		t.Fatal("expected a wrappable status line")
	}
	got := WrappedSnippet("/usr/bin/lookout", sl)
	want := `/usr/bin/lookout statusline --wrap '~/bin/my line.sh '\\''x'\\'''`
	if !strings.Contains(got, want) {
		t.Errorf("snippet:\n%s\nwant command %s", got, want)
	}
	if !strings.Contains(got, `"padding": 0`) {
		t.Errorf("snippet should keep padding:\n%s", got)
	}
	for _, s := range []string{`{}`, `{"statusLine":{"type":"command","command":""}}`, `{"statusLine":{"type":"static","command":"x"}}`, `nope`} {
		write(s)
		if _, ok := ExistingStatusLine(p); ok {
			t.Errorf("%s: should not be wrappable", s)
		}
	}
}
