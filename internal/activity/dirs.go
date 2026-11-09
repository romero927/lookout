package activity

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxDirs caps how many folders are remembered per session.
const maxDirs = 20

// dirRe finds folders a shell command works in: "cd DIR" and "git -C DIR".
// Only at the start of a command, so "grep cd ." doesn't count.
var dirRe = regexp.MustCompile(`(?:^|[;&|(\n])\s*(?:cd|pushd|git\s+-C)\s+("[^"]*"|'[^']*'|[^\s;&|)]+)`)

// commandDirs returns the folders a shell command changes into, made
// absolute against cwd. Folders built from variables or other commands
// can't be known and are skipped.
func commandDirs(cmd, cwd string) []string {
	var out []string
	for _, m := range dirRe.FindAllStringSubmatch(cmd, -1) {
		d := strings.Trim(m[1], `"'`)
		switch {
		case d == "" || d == "-" || strings.ContainsAny(d, "$`*?"):
			continue
		case d == "~" || strings.HasPrefix(d, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			d = filepath.Join(home, strings.TrimPrefix(d, "~"))
		case !filepath.IsAbs(d):
			if cwd == "" {
				continue
			}
			d = filepath.Join(cwd, d)
		}
		out = append(out, filepath.Clean(d))
	}
	return out
}

// addDir records a folder, moving it to the most recent end.
func (s *session) addDir(d string) {
	if i := slices.Index(s.dirs, d); i >= 0 {
		s.dirs = slices.Delete(s.dirs, i, i+1)
	}
	s.dirs = append(s.dirs, d)
	if len(s.dirs) > maxDirs {
		s.dirs = s.dirs[len(s.dirs)-maxDirs:]
	}
}
