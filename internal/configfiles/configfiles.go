// Package configfiles finds and reads Claude Code config and memory
// files. Read-only unless editing is enabled in Lookout's config, and every
// edit is preceded by a timestamped backup.
package configfiles

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Group labels where a file comes from.
type Group string

const (
	GroupClaude  Group = "Claude"
	GroupProject Group = "Project"
	GroupMemory  Group = "Memory"
	GroupExtra   Group = "Extra"
	GroupLookout Group = "Lookout"
)

// File is one entry in the Files list.
type File struct {
	Group   Group
	Label   string
	Path    string
	Exists  bool
	Size    int64
	ModTime time.Time
}

// Sources tells Discover where to look.
type Sources struct {
	ClaudeDir   string
	ProjectDirs []string // working directories of recent sessions
	// ProjectNames maps a projects/ directory name to a readable name
	// (usually the base of the session's working directory).
	ProjectNames map[string]string
	Extra        []string
	// LookoutConfig is Lookout's own config.toml, listed last.
	LookoutConfig string
}

// MaxProjects caps how many project directories are listed.
const MaxProjects = 8

// Discover lists the files Lookout knows about. Missing global files are
// still listed (marked absent) so users can see what's expected.
func Discover(s Sources) []File {
	var out []File
	seen := map[string]bool{}
	add := func(g Group, label, p string, alwaysShow bool) {
		p = expand(p)
		if p == "" || seen[p] {
			return
		}
		fi, err := os.Stat(p)
		exists := err == nil && !fi.IsDir()
		if !exists && !alwaysShow {
			return
		}
		seen[p] = true
		f := File{Group: g, Label: label, Path: p, Exists: exists}
		if exists {
			f.Size, f.ModTime = fi.Size(), fi.ModTime()
		}
		out = append(out, f)
	}

	cd := s.ClaudeDir
	add(GroupClaude, "settings.json", filepath.Join(cd, "settings.json"), true)
	add(GroupClaude, "settings.local.json", filepath.Join(cd, "settings.local.json"), false)
	add(GroupClaude, "CLAUDE.md (global)", filepath.Join(cd, "CLAUDE.md"), true)
	add(GroupClaude, "keybindings.json", filepath.Join(cd, "keybindings.json"), false)

	projects := s.ProjectDirs
	if len(projects) > MaxProjects {
		projects = projects[:MaxProjects]
	}
	for _, dir := range projects {
		name := filepath.Base(dir)
		for _, rel := range []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/settings.json", ".claude/settings.local.json"} {
			add(GroupProject+Group(" · "+name), rel, filepath.Join(dir, rel), false)
		}
	}

	// Auto-memory files: projects/<project>/memory/*.md
	mems, _ := filepath.Glob(filepath.Join(cd, "projects", "*", "memory", "*.md"))
	// One heading per project, the most recently updated project first;
	// newest files first within it.
	projOf := func(m string) string { return filepath.Base(filepath.Dir(filepath.Dir(m))) }
	latest := map[string]time.Time{}
	for _, m := range mems {
		if t := modTime(m); t.After(latest[projOf(m)]) {
			latest[projOf(m)] = t
		}
	}
	sort.SliceStable(mems, func(i, j int) bool {
		pi, pj := projOf(mems[i]), projOf(mems[j])
		if pi != pj {
			return latest[pi].After(latest[pj])
		}
		return modTime(mems[i]).After(modTime(mems[j]))
	})
	for _, m := range mems {
		proj := projOf(m)
		name := s.ProjectNames[proj]
		if name == "" {
			name = shortProject(proj)
		}
		add(GroupMemory+Group(" · "+name), filepath.Base(m), m, false)
	}

	for _, e := range s.Extra {
		add(GroupExtra, filepath.Base(expand(e)), e, true)
	}
	if s.LookoutConfig != "" {
		add(GroupLookout, "config.toml", s.LookoutConfig, true)
	}
	return out
}

// blockedNames are never shown: they hold credentials.
var blockedNames = map[string]bool{".credentials.json": true, "auth.json": true}

// MaxView caps how much of a file is loaded for viewing.
const MaxView = 1 << 20

// Read returns the file's contents for display, with secret-looking values
// masked. Credential files are refused.
func Read(p string) (string, error) {
	if blockedNames[filepath.Base(p)] {
		return "", fmt.Errorf("%s holds credentials; Lookout does not open it", filepath.Base(p))
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxView+1))
	if err != nil {
		return "", err
	}
	s := string(b)
	if len(b) > MaxView {
		s = string(b[:MaxView]) + "\n… (truncated)"
	}
	return Mask(s), nil
}

var secretRe = regexp.MustCompile(`(?i)("?[A-Za-z0-9_.-]*(?:token|secret|password|passwd|api[_-]?key|apikey|auth[_-]?key|authorization|credential|bearer|custom[_-]?headers)[A-Za-z0-9_.-]*"?\s*[:=]\s*)("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\s,#\n]+)`)

// bearerRe matches an HTTP credential wherever it appears, whatever the key.
var bearerRe = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{4,}`)

// Mask hides values whose key looks like a secret. Display only; files on
// disk are never changed by this.
func Mask(s string) string {
	s = bearerRe.ReplaceAllString(s, "$1 ••••••")
	return secretRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretRe.FindStringSubmatch(m)
		if strings.Contains(strings.ToLower(sub[1]), "tokens") {
			return m // token counts/limits, not secrets
		}
		val := sub[2]
		q := ""
		if strings.HasPrefix(val, `"`) || strings.HasPrefix(val, "'") {
			q = val[:1]
		}
		if strings.Trim(val, `"'`) == "" {
			return m
		}
		return sub[1] + q + "••••••" + q
	})
}

// Backup copies p into dir as <name>.<timestamp>.bak and returns the path.
func Backup(p, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return "", err
	}
	name := strings.NewReplacer(string(os.PathSeparator), "_", ":", "_").Replace(strings.TrimPrefix(p, string(os.PathSeparator)))
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s.bak", name, now.Format("20060102-150405.000")))
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm()|0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(dst)
		return "", err
	}
	return dst, out.Close()
}

// Editor returns the command to edit files with ($VISUAL, $EDITOR, vi).
// The file to edit goes after it. On Unix the setting runs through the
// shell, as git does, so a quoted path with spaces and flags both work.
func Editor() []string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if e := strings.TrimSpace(os.Getenv(v)); e != "" {
			if runtime.GOOS == "windows" {
				return strings.Fields(e)
			}
			return []string{"sh", "-c", e + ` "$1"`, "sh"}
		}
	}
	return []string{"vi"}
}

// Unchanged reports whether p still has the backup's contents (so the
// backup can be dropped after an edit that changed nothing).
func Unchanged(p, backup string) bool {
	a, err1 := os.ReadFile(p)
	b, err2 := os.ReadFile(backup)
	return err1 == nil && err2 == nil && string(a) == string(b)
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func modTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// shortProject turns "-home-me-src-app" into "app".
func shortProject(p string) string {
	parts := strings.Split(strings.Trim(p, "-"), "-")
	if len(parts) == 0 {
		return p
	}
	return parts[len(parts)-1]
}
