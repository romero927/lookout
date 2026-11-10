package limits

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultWrapTimeout is how long a wrapped status line command may run.
const DefaultWrapTimeout = 2 * time.Second

// DefaultWrapSeparator joins the wrapped command's output and Lookout's line.
const DefaultWrapSeparator = " · "

// RunWrapped runs another status line command through the shell, passing it
// the same stdin JSON Claude Code gave Lookout, and returns what it printed
// with trailing newlines removed.
//
// A command that exits non-zero still has its output returned (with the
// error), since many status line scripts end on a failing test. A command
// that can't start or runs past timeout returns no output.
func RunWrapped(command string, stdin []byte, timeout time.Duration) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := shellCommand(ctx, command)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	// If the shell is killed while a child it started still holds stdout
	// open, stop waiting for that child shortly after.
	cmd.WaitDelay = 200 * time.Millisecond
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("wrapped status line timed out after %s", timeout)
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", err
	}
	return strings.TrimRight(out.String(), "\r\n"), err
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if sh, err := exec.LookPath("sh"); err == nil {
			return exec.CommandContext(ctx, sh, "-c", command)
		}
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// ComposeWrapped joins the wrapped command's output and Lookout's own line.
// Lookout's line goes on the wrapped output's last line, after it, or before
// its first line when before is true. Empty wrapped output leaves Lookout's
// line on its own.
func ComposeWrapped(wrapped, ours, sep string, before bool) string {
	if wrapped == "" {
		return ours
	}
	if before {
		return ours + sep + wrapped
	}
	return wrapped + sep + ours
}
