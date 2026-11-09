// Package paths resolves the directories Lookout reads from and writes to.
//
// Lookout reads Claude Code's and Codex's directories, and keeps its own
// config and data in standard per-OS locations:
//
//	Linux:  $XDG_CONFIG_HOME/lookout (~/.config/lookout)
//	        $XDG_DATA_HOME/lookout   (~/.local/share/lookout)
//	macOS:  ~/Library/Application Support/lookout (both)
//
// LOOKOUT_CONFIG_DIR and LOOKOUT_DATA_DIR override these (useful for tests).
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "lookout"

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

// ConfigDir is where Lookout's config.toml lives.
func ConfigDir() string {
	if d := os.Getenv("LOOKOUT_CONFIG_DIR"); d != "" {
		return d
	}
	return configDir(appName)
}

func configDir(appName string) string {
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

// DataDir is where Lookout keeps its database, limits, backups and logs.
func DataDir() string {
	if d := os.Getenv("LOOKOUT_DATA_DIR"); d != "" {
		return d
	}
	return dataDir(appName)
}

func dataDir(appName string) string {
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
func DBFile() string { return filepath.Join(DataDir(), "lookout.db") }

// BackupDir holds timestamped backups made before any edit.
func BackupDir() string { return filepath.Join(DataDir(), "backups") }

// LimitsDir holds the usage limits Claude Code passed to the status line,
// one file per session.
func LimitsDir() string { return filepath.Join(DataDir(), "limits") }

// HistoryFile holds limit usage per day.
func HistoryFile() string { return filepath.Join(DataDir(), "history.json") }

// AlertsFile records which limit alerts were already shown.
func AlertsFile() string { return filepath.Join(DataDir(), "alerts.json") }

// LogDir holds the status line log.
func LogDir() string { return filepath.Join(DataDir(), "logs") }

// legacyName is the project's former name. Its directories are moved to the
// new ones the first time Lookout runs.
const legacyName = "overwatch"

// MigrateLegacy moves the config and data directories from the old name to
// the new one, when the old ones exist and the new ones don't. Directories
// set through LOOKOUT_CONFIG_DIR / LOOKOUT_DATA_DIR are left alone.
func MigrateLegacy() error {
	var errs []error
	if os.Getenv("LOOKOUT_CONFIG_DIR") == "" {
		errs = append(errs, moveDir(configDir(legacyName), configDir(appName)))
	}
	if os.Getenv("LOOKOUT_DATA_DIR") == "" {
		from, to := dataDir(legacyName), dataDir(appName)
		if err := moveDir(from, to); err != nil {
			errs = append(errs, err)
		} else {
			for _, ext := range []string{"", "-wal", "-shm"} {
				old := filepath.Join(to, legacyName+".db"+ext)
				if _, err := os.Stat(old); err == nil {
					errs = append(errs, os.Rename(old, filepath.Join(to, appName+".db"+ext)))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func moveDir(from, to string) error {
	if from == to {
		return nil
	}
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.Rename(from, to)
}
