package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/romero927/lookout/internal/paths"
)

// Tokens formats a token count compactly: 999, 12.3k, 4.56M, 1.2B.
func Tokens(n int64) string {
	f := float64(n)
	switch {
	case n < 0:
		return "-" + Tokens(-n)
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return trim(fmt.Sprintf("%.1f", f/1e3)) + "k"
	case n < 1_000_000_000:
		return trim(fmt.Sprintf("%.2f", f/1e6)) + "M"
	default:
		return trim(fmt.Sprintf("%.2f", f/1e9)) + "B"
	}
}

// Rate formats a per-minute rate.
func Rate(perMin float64) string {
	if perMin <= 0 || math.IsNaN(perMin) {
		return "0"
	}
	return Tokens(int64(math.Round(perMin)))
}

func trim(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Ago formats how long ago t was.
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// Dur formats a duration as 2h13m / 45m / 30s.
func Dur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// ShortModel names a model the way Claude Code shows it: "claude-opus-5-5"
// becomes "Opus 5.5", "claude-haiku-4-5-20251001" "Haiku 4.5". IDs it
// doesn't recognise lose only the "claude-" prefix.
func ShortModel(m string) string {
	if m == "" {
		return "—"
	}
	id := strings.TrimPrefix(m, "claude-")
	suffix := ""
	if i := strings.Index(id, "["); i > 0 {
		id, suffix = id[:i], " "+id[i:] // e.g. "[1m]"
	}
	var family string
	var version, extra []string
	for _, p := range strings.Split(id, "-") {
		switch {
		case p == "":
		case isDigits(p) && len(p) >= 8:
			// a release date: left out
		case isDigits(p):
			version = append(version, p)
		case family == "":
			family = strings.ToUpper(p[:1]) + p[1:]
		default:
			extra = append(extra, p)
		}
	}
	if family == "" {
		return strings.TrimPrefix(m, "claude-")
	}
	out := family
	if len(version) > 0 {
		out += " " + strings.Join(version, ".")
	}
	if len(extra) > 0 {
		out += " " + strings.Join(extra, " ")
	}
	return out + suffix
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// ShortPath replaces the home directory with ~.
func ShortPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if p == h {
			return "~"
		}
		if strings.HasPrefix(p, h+string(filepath.Separator)) {
			return "~" + p[len(h):]
		}
	}
	return p
}

// shortPaths shortens paths inside a command or label: Claude Code's
// session scratchpad becomes "scratchpad", your home folder "~".
func shortPaths(s string) string {
	s = paths.ScratchRe.ReplaceAllString(s, "scratchpad")
	if h, err := os.UserHomeDir(); err == nil && len(h) > 1 {
		s = strings.ReplaceAll(s, h+"/", "~/")
	}
	return s
}

// ShortID shortens a session id.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
