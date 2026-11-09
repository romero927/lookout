// Package config loads Lookout's config.toml. Every field has a default, so
// a missing or partial file is fine.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the full set of tunables.
type Config struct {
	UI         UI         `toml:"ui"`
	Usage      Usage      `toml:"usage"`
	Files      Files      `toml:"files"`
	Models     Models     `toml:"models"`
	CrossCheck CrossCheck `toml:"crosscheck"`
}

// UI controls appearance.
type UI struct {
	// Theme is "auto" (ask the terminal; inside tmux/screen dark is
	// assumed), "dark" or "light".
	Theme string `toml:"theme"`
}

// Usage controls how token usage is windowed.
type Usage struct {
	// HistoryDays limits the initial scan to transcripts modified in the last N days.
	HistoryDays int `toml:"history_days"`
	// BurnWindowMinutes is the look-back window for the tokens-per-minute figure.
	BurnWindowMinutes int `toml:"burn_window_minutes"`
	// BlockHours is the length of the rolling window and of an estimated usage block.
	BlockHours int `toml:"block_hours"`
}

// Files controls the config/memory file browser.
type Files struct {
	// AllowEdit enables the "e" key, which opens the file in $EDITOR after a backup.
	AllowEdit bool `toml:"allow_edit"`
	// ExtraFiles are additional files to list.
	ExtraFiles []string `toml:"extra_files"`
}

// Models holds the thresholds for model suggestions.
type Models struct {
	// ModelTiers lists /model aliases from cheapest to most capable. A model
	// name matches a tier when it contains the alias.
	ModelTiers []string `toml:"model_tiers"`

	// RecentTurns is how many recent turns the upgrade signals look at.
	RecentTurns int `toml:"recent_turns"`
	// RecentToolCalls caps the look-back within those turns, so one long
	// autonomous turn doesn't count forever.
	RecentToolCalls int `toml:"recent_tool_calls"`
	// RepeatedEditThreshold: the same file edited this many times in RecentTurns.
	RepeatedEditThreshold int `toml:"repeated_edit_threshold"`
	// ToolErrorThreshold: this many failing tool results in RecentTurns.
	ToolErrorThreshold int `toml:"tool_error_threshold"`
	// MultiFileThreshold: this many distinct files edited in one turn.
	MultiFileThreshold int `toml:"multi_file_threshold"`
	// LongPromptChars: a prompt at least this long counts as a large task.
	LongPromptChars int `toml:"long_prompt_chars"`
	// FrustrationPhrases are matched case-insensitively against the latest prompt.
	FrustrationPhrases []string `toml:"frustration_phrases"`

	// SimpleTurnStreak: consecutive simple, successful turns on the top tier.
	SimpleTurnStreak int `toml:"simple_turn_streak"`
	// SimpleMaxToolCalls / SimpleMaxOutputTokens define a "simple" turn.
	SimpleMaxToolCalls    int `toml:"simple_max_tool_calls"`
	SimpleMaxOutputTokens int `toml:"simple_max_output_tokens"`
	// ShortPromptChars: prompts at or under this length count as trivial.
	ShortPromptChars int `toml:"short_prompt_chars"`

	// ContextWindowTokens is the context size used for the percentage.
	ContextWindowTokens int `toml:"context_window_tokens"`
	// CompactAtPercent suggests /compact; FreshSessionAtPercent suggests /clear.
	CompactAtPercent      int `toml:"compact_at_percent"`
	FreshSessionAtPercent int `toml:"fresh_session_at_percent"`
}

// CrossCheck controls the Codex cross-check.
type CrossCheck struct {
	// CodexPath overrides looking up "codex" on PATH.
	CodexPath string `toml:"codex_path"`
	// Model is passed to codex exec -m when set.
	Model string `toml:"model"`
	// TimeoutSeconds kills a review that runs too long.
	TimeoutSeconds int `toml:"timeout_seconds"`
	// MaxChars truncates Claude's response before it is sent.
	MaxChars int `toml:"max_chars"`
	// MaxParallel caps concurrently running reviews; extra ones are skipped.
	MaxParallel int `toml:"max_parallel"`
	// RepoAccess runs Codex (read-only sandbox) in the session's working
	// directory so it can read project files. Off: an empty temp directory.
	RepoAccess bool `toml:"repo_access"`
	// Prompt replaces the default review instructions.
	Prompt string `toml:"prompt"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		UI:    UI{Theme: "auto"},
		Usage: Usage{HistoryDays: 30, BurnWindowMinutes: 10, BlockHours: 5},
		Files: Files{AllowEdit: false, ExtraFiles: []string{}},
		Models: Models{
			ModelTiers:            []string{"haiku", "sonnet", "opus"},
			RecentTurns:           3,
			RecentToolCalls:       30,
			RepeatedEditThreshold: 4,
			ToolErrorThreshold:    3,
			MultiFileThreshold:    5,
			LongPromptChars:       1500,
			FrustrationPhrases: []string{
				"still broken", "doesn't work", "does not work", "still not working",
				"still failing", "not working", "same error", "still wrong", "try again",
				"that's wrong", "you broke", "didn't work", "did not work",
			},
			SimpleTurnStreak:      3,
			SimpleMaxToolCalls:    3,
			SimpleMaxOutputTokens: 1500,
			ShortPromptChars:      40,
			ContextWindowTokens:   200000,
			CompactAtPercent:      60,
			FreshSessionAtPercent: 85,
		},
		CrossCheck: CrossCheck{
			TimeoutSeconds: 300,
			MaxChars:       20000,
			MaxParallel:    2,
			RepoAccess:     false,
		},
	}
}

// Load reads path on top of the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	cfg.fill()
	return cfg, nil
}

// fill replaces nonsensical zero values with defaults.
func (c *Config) fill() {
	d := Default()
	switch c.UI.Theme {
	case "auto", "dark", "light":
	default:
		c.UI.Theme = d.UI.Theme
	}
	pos := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	pos(&c.Usage.HistoryDays, d.Usage.HistoryDays)
	pos(&c.Usage.BurnWindowMinutes, d.Usage.BurnWindowMinutes)
	pos(&c.Usage.BlockHours, d.Usage.BlockHours)
	s, ds := &c.Models, d.Models
	if len(s.ModelTiers) == 0 {
		s.ModelTiers = ds.ModelTiers
	}
	pos(&s.RecentTurns, ds.RecentTurns)
	pos(&s.RecentToolCalls, ds.RecentToolCalls)
	pos(&s.RepeatedEditThreshold, ds.RepeatedEditThreshold)
	pos(&s.ToolErrorThreshold, ds.ToolErrorThreshold)
	pos(&s.MultiFileThreshold, ds.MultiFileThreshold)
	pos(&s.LongPromptChars, ds.LongPromptChars)
	pos(&s.SimpleTurnStreak, ds.SimpleTurnStreak)
	pos(&s.SimpleMaxToolCalls, ds.SimpleMaxToolCalls)
	pos(&s.SimpleMaxOutputTokens, ds.SimpleMaxOutputTokens)
	pos(&s.ShortPromptChars, ds.ShortPromptChars)
	pos(&s.ContextWindowTokens, ds.ContextWindowTokens)
	pos(&s.CompactAtPercent, ds.CompactAtPercent)
	pos(&s.FreshSessionAtPercent, ds.FreshSessionAtPercent)
	pos(&c.CrossCheck.TimeoutSeconds, d.CrossCheck.TimeoutSeconds)
	pos(&c.CrossCheck.MaxChars, d.CrossCheck.MaxChars)
	pos(&c.CrossCheck.MaxParallel, d.CrossCheck.MaxParallel)
}

// WriteDefault writes a commented default config to path if none exists.
func WriteDefault(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(DefaultTOML), 0o644)
}

// DefaultTOML is the commented config written by `lookout config init`.
const DefaultTOML = `# Lookout configuration. Every value shown is the default.

[ui]
theme = "auto"             # "auto" asks the terminal (inside tmux, dark is assumed); or "dark" / "light"

[usage]
history_days = 30          # only scan transcripts modified in the last N days
burn_window_minutes = 10   # look-back window for tokens/minute
block_hours = 5            # rolling window and estimated usage block length

[files]
allow_edit = false         # true enables "e" to open a file in $EDITOR (a backup is made first)
extra_files = []           # more files to list, e.g. ["~/.claude/keybindings.json"]

[models]
model_tiers = ["haiku", "sonnet", "opus"]   # cheapest -> most capable (/model aliases)
recent_turns = 3              # upgrade signals look at the last N turns...
recent_tool_calls = 30        # ...and at most the last N tool calls within them
repeated_edit_threshold = 4   # same file edited N times recently -> upgrade
tool_error_threshold = 3      # N failing tool results recently -> upgrade
multi_file_threshold = 5      # N distinct files edited in one turn -> upgrade
long_prompt_chars = 1500      # a prompt this long is a large task -> upgrade
frustration_phrases = ["still broken", "doesn't work", "does not work", "still not working", "still failing", "not working", "same error", "still wrong", "try again", "that's wrong", "you broke", "didn't work", "did not work"]
simple_turn_streak = 3        # N simple, successful turns on the top tier -> downgrade
simple_max_tool_calls = 3
simple_max_output_tokens = 1500
short_prompt_chars = 40       # prompts this short count as trivial
context_window_tokens = 200000
compact_at_percent = 60       # suggest /compact
fresh_session_at_percent = 85 # suggest a fresh session (/clear)

[crosscheck]
codex_path = ""        # empty: look up "codex" on PATH
model = ""             # passed to codex exec -m when set
timeout_seconds = 300
max_chars = 20000      # Claude's response is truncated to this many characters
max_parallel = 2       # extra reviews are skipped while this many are running
repo_access = false    # true: Codex runs (read-only sandbox) in the session's working directory
prompt = ""            # custom review instructions; empty uses the built-in prompt
`
