package main

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/muesli/termenv"
)

// colorProfile picks how many colors to draw with when LOOKOUT_COLOR, the
// [ui] colors setting or what Lookout knows about the terminal says better
// than termenv's own guess. ok is false to keep that guess.
//
// termenv trusts COLORTERM=truecolor and takes TERM=xterm to mean 16
// colors. Both go wrong in common setups:
//   - Terminal.app before macOS 26 shows 256 colors, not 24-bit, whatever
//     COLORTERM says. Sent 24-bit color, it reads the numbers as other
//     styles: dim text, cyan backgrounds.
//   - Inside tmux, TERM_PROGRAM is "tmux" and COLORTERM is usually
//     truecolor, while tmux's default terminal-features ("*:RGB") passes
//     24-bit color through to any outer terminal. So the outer terminal is
//     looked up through the tmux client (outer).
//   - XQuartz's xterm and some Terminal.app profiles report TERM=xterm,
//     yet every xterm still in use shows 256 colors.
//
// outer returns the outer terminal's TERM_PROGRAM when Lookout runs in
// tmux, or "" when it can't tell; it is only called inside tmux.
func colorProfile(setting string, getenv func(string) string, outer func() string) (termenv.Profile, bool) {
	if v := getenv("LOOKOUT_COLOR"); v != "" {
		setting = v
	}
	switch strings.ToLower(setting) {
	case "16":
		return termenv.ANSI, true
	case "256":
		return termenv.ANSI256, true
	case "truecolor", "24bit":
		return termenv.TrueColor, true
	case "none", "0":
		return termenv.Ascii, true
	}
	program := getenv("TERM_PROGRAM")
	if getenv("TMUX") != "" || program == "tmux" {
		program = outer()
	}
	if program == "Apple_Terminal" {
		return termenv.ANSI256, true
	}
	colorTerm := strings.ToLower(getenv("COLORTERM"))
	truecolor := colorTerm == "truecolor" || colorTerm == "24bit"
	switch getenv("TERM") {
	case "xterm", "xterm-color":
		if !truecolor {
			return termenv.ANSI256, true
		}
	}
	return termenv.Ascii, false
}

// tmuxOuterProgram is the TERM_PROGRAM of the terminal the tmux client
// showing Lookout runs in, read from that client process's environment.
// tmux's own #{client_termtype} can't be used: it comes from the
// terminal's reply to an identification query, and Terminal.app doesn't
// say its name there. It returns "" when it can't tell, for example when
// tmux runs on another computer over ssh.
func tmuxOuterProgram() string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_pid}").Output()
	if err != nil {
		return ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if err != nil {
			return ""
		}
		return envValue(strings.Split(string(b), "\x00"), "TERM_PROGRAM")
	}
	// macOS and the BSDs: ps -E appends the environment to the command.
	out, err = exec.CommandContext(ctx, "ps", "-wwE", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return psEnvValue(string(out), "TERM_PROGRAM")
}

// envValue finds key in a list of KEY=value entries.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// psEnvValue finds key in `ps -E` output, where the environment follows
// the command, separated by spaces. Values with spaces are cut short, which
// TERM_PROGRAM's never have.
func psEnvValue(out, key string) string {
	m := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(key) + `=(\S*)`).FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}
