// Package paths resolves the directories Overwatch reads from and writes to.
//
// Overwatch reads Claude Code's and Codex's directories, and keeps its own
// config and data in standard per-OS locations:
//
//	Linux:  $XDG_CONFIG_HOME/overwatch (~/.config/overwatch)
//	        $XDG_DATA_HOME/overwatch   (~/.local/share/overwatch)
//	macOS:  ~/Library/Application Support/overwatch (both)
//
// OVERWATCH_CONFIG_DIR and OVERWATCH_DATA_DIR override these (useful for tests).
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "overwatch"

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ClaudeDir is Claude Code's config directory (honours CLAUDE_CONFIG_DIR).
func ClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home(), ".claude")
}

// ClaudeProjectsDir holds the session transcripts.
func ClaudeProjectsDir() string { return filepath.Join(ClaudeDir(), "projects") }

// CodexDir is Codex's home directory (honours CODEX_HOME).
func CodexDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(home(), ".codex")
}

// ConfigDir is where Overwatch's config.toml lives.
func ConfigDir() string {
	if d := os.Getenv("OVERWATCH_CONFIG_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home(), "Library", "Application Support", appName)
	case "linux", "freebsd", "openbsd", "netbsd":
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, appName)
		}
		return filepath.Join(home(), ".config", appName)
	default:
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, appName)
		}
		return filepath.Join(home(), "."+appName)
	}
}

// DataDir is where Overwatch keeps its database, backups, reviews and logs.
func DataDir() string {
	if d := os.Getenv("OVERWATCH_DATA_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home(), "Library", "Application Support", appName)
	case "linux", "freebsd", "openbsd", "netbsd":
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, appName)
		}
		return filepath.Join(home(), ".local", "share", appName)
	default:
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, appName, "data")
		}
		return filepath.Join(home(), "."+appName, "data")
	}
}

// ConfigFile is the path of config.toml.
func ConfigFile() string { return filepath.Join(ConfigDir(), "config.toml") }

// DBFile is the SQLite database used for the Strategy log.
func DBFile() string { return filepath.Join(DataDir(), "overwatch.db") }

// BackupDir holds timestamped backups made before any edit.
func BackupDir() string { return filepath.Join(DataDir(), "backups") }

// LimitsDir holds the usage limits Claude Code passed to the status line,
// one file per session.
func LimitsDir() string { return filepath.Join(DataDir(), "limits") }

// LogDir holds hook and worker logs.
func LogDir() string { return filepath.Join(DataDir(), "logs") }

// CrossCheckDir holds the cross-check flag, jobs and reviews.
func CrossCheckDir() string { return filepath.Join(DataDir(), "crosscheck") }

// CrossCheckFlag exists when cross-check mode is on.
func CrossCheckFlag() string { return filepath.Join(CrossCheckDir(), "enabled") }

// ReviewsDir holds one JSON file per Codex review.
func ReviewsDir() string { return filepath.Join(CrossCheckDir(), "reviews") }

// JobsDir holds pending review jobs written by the hook.
func JobsDir() string { return filepath.Join(CrossCheckDir(), "jobs") }
