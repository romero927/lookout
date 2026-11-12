package main

import (
	"testing"

	"github.com/muesli/termenv"
)

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// tmuxPane is the environment inside a tmux 3.8 pane with no tmux config.
var tmuxPane = []string{"TMUX", "/private/tmp/tmux-501/default,123,0", "TERM", "tmux-256color", "COLORTERM", "truecolor", "TERM_PROGRAM", "tmux"}

func TestColorProfile(t *testing.T) {
	cases := []struct {
		name    string
		setting string
		env     []string
		outer   string // the tmux client's TERM_PROGRAM
		want    termenv.Profile
		ok      bool // false: keep termenv's guess
	}{
		{"Terminal.app", "auto", []string{"TERM", "xterm-256color", "TERM_PROGRAM", "Apple_Terminal"}, "", termenv.ANSI256, true},
		{"Terminal.app claiming truecolor", "auto", []string{"TERM", "xterm-256color", "COLORTERM", "truecolor", "TERM_PROGRAM", "Apple_Terminal"}, "", termenv.ANSI256, true},
		{"tmux in Terminal.app", "auto", tmuxPane, "Apple_Terminal", termenv.ANSI256, true},
		{"tmux, outer terminal unknown", "auto", tmuxPane, "", 0, false},
		{"tmux in iTerm2", "auto", tmuxPane, "iTerm.app", 0, false},
		{"tmux in Ghostty", "auto", tmuxPane, "ghostty", 0, false},
		{"iTerm2", "auto", []string{"TERM", "xterm-256color", "COLORTERM", "truecolor", "TERM_PROGRAM", "iTerm.app"}, "", 0, false},
		{"Ghostty", "auto", []string{"TERM", "xterm-ghostty", "COLORTERM", "truecolor", "TERM_PROGRAM", "ghostty"}, "", 0, false},
		{"plain xterm", "auto", []string{"TERM", "xterm"}, "", termenv.ANSI256, true},
		{"plain xterm with truecolor", "auto", []string{"TERM", "xterm", "COLORTERM", "truecolor"}, "", 0, false},
		{"config setting", "16", []string{"TERM", "xterm-kitty"}, "", termenv.ANSI, true},
		{"LOOKOUT_COLOR over config", "16", append([]string{"LOOKOUT_COLOR", "256"}, tmuxPane...), "", termenv.ANSI256, true},
		{"LOOKOUT_COLOR=truecolor in Terminal.app", "auto", []string{"LOOKOUT_COLOR", "truecolor", "TERM_PROGRAM", "Apple_Terminal"}, "", termenv.TrueColor, true},
		{"LOOKOUT_COLOR=none", "auto", []string{"LOOKOUT_COLOR", "none", "TERM", "xterm-256color"}, "", termenv.Ascii, true},
	}
	for _, c := range cases {
		asked := false
		outer := func() string { asked = true; return c.outer }
		got, ok := colorProfile(c.setting, envOf(c.env...), outer)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: got %v (set %v), want %v (set %v)", c.name, got, ok, c.want, c.ok)
		}
		inTmux := envOf(c.env...)("TMUX") != ""
		if asked && !inTmux {
			t.Errorf("%s: asked tmux for the outer terminal outside tmux", c.name)
		}
	}
}

// termenv on its own picks 24-bit color in a default tmux pane, whatever
// the outer terminal: the reason colorProfile asks tmux.
func TestTermenvTrustsColortermInTmux(t *testing.T) {
	env := envOf(tmuxPane...)
	o := termenv.NewOutput(nil, termenv.WithTTY(true), termenv.WithEnvironment(environ{env}))
	if p := o.EnvColorProfile(); p != termenv.TrueColor {
		t.Fatalf("termenv picked %v in a tmux pane with COLORTERM=truecolor; colorProfile's comment is out of date", p)
	}
}

type environ struct{ get func(string) string }

func (e environ) Environ() []string      { return nil }
func (e environ) Getenv(k string) string { return e.get(k) }

func TestPsEnvValue(t *testing.T) {
	out := "tmux attach TERM_PROGRAM=Apple_Terminal SHELL=/bin/zsh TERM_PROGRAM_VERSION=455\n"
	if v := psEnvValue(out, "TERM_PROGRAM"); v != "Apple_Terminal" {
		t.Errorf("got %q", v)
	}
	if v := psEnvValue("tmux NOT_TERM_PROGRAM=x", "TERM_PROGRAM"); v != "" {
		t.Errorf("matched a longer key: %q", v)
	}
	if v := envValue([]string{"A=1", "TERM_PROGRAM=iTerm.app"}, "TERM_PROGRAM"); v != "iTerm.app" {
		t.Errorf("envValue: %q", v)
	}
}
