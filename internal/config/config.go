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
	UI      UI      `toml:"ui"`
	Usage   Usage   `toml:"usage"`
	Files   Files   `toml:"files"`
	Alerts  Alerts  `toml:"alerts"`
	Models  Models  `toml:"models"`
	Updates Updates `toml:"updates"`
}

// Updates controls the check for new Lookout releases.
type Updates struct {
	// Check asks GitHub once a day whether a newer release is out. Off by
	// default: it is the only network call Lookout makes.
	Check bool `toml:"check"`
}

// UI controls appearance.
type UI struct {
	// Theme is "auto" (ask the terminal; inside tmux/screen dark is
	// assumed), "dark" or "light".
	Theme string `toml:"theme"`
	// Colors is "auto" (guess from TERM, COLORTERM and TERM_PROGRAM),
	// "16", "256" or "truecolor".
	Colors string `toml:"colors"`
	// NewClaude is where N (or the "+ claude" button) opens a new Claude
	// Code in tmux: "split" splits the pane of the session on screen (or
	// opens a window when it isn't in tmux); "window" always opens a new
	// window.
	NewClaude string `toml:"new_claude"`
}

// Usage controls how token usage is windowed.
type Usage struct {
	// HistoryDays limits the initial scan to transcripts modified in the last N days.
	HistoryDays int `toml:"history_days"`
	// BurnWindowMinutes is the look-back window for the tokens-per-minute figure.
	BurnWindowMinutes int `toml:"burn_window_minutes"`
	// BlockHours is the length of the rolling window and of an estimated usage block.
	BlockHours int `toml:"block_hours"`
	// PriceWeights overrides the built-in per-model prices used to weight
	// tokens when estimating a session's share of the limits, and to estimate
	// dollars on pay-as-you-go accounts. Keys are substrings of the model ID;
	// the longest match wins.
	PriceWeights map[string]PriceWeight `toml:"price_weights"`
}

// PriceWeight is a model's price in US dollars per million tokens.
type PriceWeight struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	CacheWrite float64 `toml:"cache_write"`
	// CacheWrite1h is the 1-hour cache write price; 0 means twice Input.
	CacheWrite1h float64 `toml:"cache_write_1h"`
	CacheRead    float64 `toml:"cache_read"`
}

// Alerts controls limit notifications from the dashboard.
type Alerts struct {
	// Bell rings the terminal bell (tmux marks the window).
	Bell bool `toml:"bell"`
	// Desktop sends a desktop notification (notify-send on Linux,
	// osascript on macOS) when one is available.
	Desktop bool `toml:"desktop"`
	// Thresholds are the used percentages that trigger an alert.
	Thresholds []float64 `toml:"thresholds"`
	// Pace alerts when the current pace reaches 100% before the reset.
	Pace bool `toml:"pace"`
	// Sessions alerts when a session other than the one on screen stops on
	// a permission prompt or question, or finishes what you asked.
	Sessions bool `toml:"sessions"`
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
	// EffortLevels lists /effort levels from lightest to heaviest. Effort is
	// a cheaper step than a model switch, so it is suggested first: raised
	// while below the second-highest level, lowered back to DefaultEffort
	// when the work is simple.
	EffortLevels []string `toml:"effort_levels"`
	// DefaultEffort is the level simple work is brought back down to.
	DefaultEffort string `toml:"default_effort"`

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

	// SimpleTurnStreak: consecutive simple, successful turns on a heavy tier
	// (the top tier, or the top two when there are four or more).
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

	// LogDays is how long the suggestion log keeps a row; 0 keeps them all.
	LogDays int `toml:"log_days"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		UI:     UI{Theme: "auto", Colors: "auto", NewClaude: "split"},
		Usage:  Usage{HistoryDays: 30, BurnWindowMinutes: 10, BlockHours: 5},
		Files:  Files{AllowEdit: false, ExtraFiles: []string{}},
		Alerts: Alerts{Bell: true, Desktop: true, Thresholds: []float64{80, 95}, Pace: true, Sessions: true},
		Models: Models{
			ModelTiers:            []string{"haiku", "sonnet", "opus", "fable"},
			EffortLevels:          []string{"low", "medium", "high", "xhigh"},
			DefaultEffort:         "medium",
			RecentTurns:           3,
			RecentToolCalls:       30,
			RepeatedEditThreshold: 4,
			ToolErrorThreshold:    3,
			MultiFileThreshold:    5,
			LongPromptChars:       1500,
			FrustrationPhrases: []string{
				"still broken", "doesn't work", "does not work", "still not working",
				"still failing", "not working", "same error", "still wrong",
				"that's wrong", "you broke", "didn't work", "did not work",
			},
			SimpleTurnStreak:      3,
			SimpleMaxToolCalls:    3,
			SimpleMaxOutputTokens: 1500,
			ShortPromptChars:      40,
			ContextWindowTokens:   200000,
			CompactAtPercent:      60,
			FreshSessionAtPercent: 85,
			LogDays:               90,
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
	switch c.UI.Colors {
	case "auto", "16", "256", "truecolor":
	default:
		c.UI.Colors = d.UI.Colors
	}
	switch c.UI.NewClaude {
	case "split", "window":
	default:
		c.UI.NewClaude = d.UI.NewClaude
	}
	pos := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	pos(&c.Usage.HistoryDays, d.Usage.HistoryDays)
	pos(&c.Usage.BurnWindowMinutes, d.Usage.BurnWindowMinutes)
	pos(&c.Usage.BlockHours, d.Usage.BlockHours)
	for k, w := range c.Usage.PriceWeights {
		if w.Input < 0 || w.Output < 0 || w.CacheWrite < 0 || w.CacheWrite1h < 0 || w.CacheRead < 0 {
			delete(c.Usage.PriceWeights, k)
		}
	}
	if c.Alerts.Thresholds == nil {
		c.Alerts.Thresholds = d.Alerts.Thresholds
	}
	s, ds := &c.Models, d.Models
	if len(s.ModelTiers) == 0 {
		s.ModelTiers = ds.ModelTiers
	}
	if len(s.EffortLevels) == 0 {
		s.EffortLevels = ds.EffortLevels
	}
	if s.DefaultEffort == "" {
		s.DefaultEffort = ds.DefaultEffort
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
	if s.LogDays < 0 { // 0 is allowed: keep everything
		s.LogDays = ds.LogDays
	}
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
colors = "auto"            # "auto" guesses from TERM/COLORTERM; or "16" / "256" / "truecolor" if colors look wrong
new_claude = "split"       # N in tmux: "split" the pane of the session on screen, or open a new "window"

[usage]
history_days = 30          # only scan transcripts modified in the last N days
burn_window_minutes = 10   # look-back window for tokens/minute
block_hours = 5            # rolling window and estimated usage block length

# Per-model prices (US dollars per million tokens). They weight tokens when
# estimating each session's share of the 5-hour and weekly limits, and on
# pay-as-you-go accounts they price the estimated costs in the Usage panel.
# Built-in values match Anthropic's API list prices as of this release;
# override a model here if they change. Keys are substrings of the model ID;
# the longest match wins.
# [usage.price_weights]
# cache_write is the 5-minute cache rate; cache_write_1h (default: twice input)
# is the 1-hour rate Claude Code usually uses.
# "opus-5-5" = { input = 4, output = 20, cache_write = 5, cache_write_1h = 8, cache_read = 0.20 }

[files]
allow_edit = false         # true enables "e" to open a file in $EDITOR (a backup is made first)
extra_files = []           # more files to list, e.g. ["~/.claude/keybindings.json"]

[alerts]
bell = true                # ring the terminal bell when a limit alert fires
desktop = true             # also send a desktop notification (notify-send / osascript)
thresholds = [80, 95]      # alert when a limit passes these percentages; [] turns them off
pace = true                # alert when the current pace runs out before the reset
sessions = true            # alert when another session needs you or finishes

[updates]
check = false              # true asks GitHub once a day for a newer release (Lookout's only network call)

[models]
model_tiers = ["haiku", "sonnet", "opus", "fable"]   # cheapest -> most capable (/model aliases)
effort_levels = ["low", "medium", "high", "xhigh"]   # lightest -> heaviest (/effort levels); tried before a model switch
default_effort = "medium"     # simple work at a higher effort is brought back to this
recent_turns = 3              # upgrade signals look at the last N turns...
recent_tool_calls = 30        # ...and at most the last N tool calls within them
repeated_edit_threshold = 4   # same file edited N times recently -> upgrade
tool_error_threshold = 3      # N failing tool results recently -> upgrade
multi_file_threshold = 5      # N distinct files edited in one turn -> upgrade
long_prompt_chars = 1500      # a prompt this long is a large task -> upgrade
frustration_phrases = ["still broken", "doesn't work", "does not work", "still not working", "still failing", "not working", "same error", "still wrong", "that's wrong", "you broke", "didn't work", "did not work"]
simple_turn_streak = 3        # N simple, successful turns on opus or fable -> downgrade
simple_max_tool_calls = 3
simple_max_output_tokens = 1500
short_prompt_chars = 40       # prompts this short count as trivial
context_window_tokens = 200000
compact_at_percent = 60       # suggest /compact
fresh_session_at_percent = 85 # suggest a fresh session (/clear)
log_days = 90                 # delete suggestion log rows older than this; 0 keeps them all

`
