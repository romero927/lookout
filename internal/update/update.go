// Package update checks GitHub for a newer Lookout release. It is only used
// when [updates] check = true: Lookout makes no network calls otherwise.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LatestURL is GitHub's latest-release API for the Lookout repository.
// A variable so tests can point it at a local server.
var LatestURL = "https://api.github.com/repos/romero927/lookout/releases/latest"

// ReleasesURL is where release binaries are downloaded.
const ReleasesURL = "https://github.com/romero927/lookout/releases/latest"

// InstallCmd updates a copy installed with go install.
const InstallCmd = "go install github.com/romero927/lookout/cmd/lookout@latest"

// Every is how often GitHub is asked; the answer is cached in between.
const Every = 24 * time.Hour

// cache is what the check keeps on disk between runs.
type cache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

// Latest returns the newest release tag (e.g. "v0.6.4"). It reads the cache
// file when it was written less than Every ago; otherwise it asks GitHub
// and rewrites the cache. The request carries no data about you or your
// usage: it is a plain GET with a generic User-Agent.
func Latest(ctx context.Context, cacheFile string, now time.Time) (string, error) {
	var c cache
	if b, err := os.ReadFile(cacheFile); err == nil && json.Unmarshal(b, &c) == nil {
		if c.Latest != "" && now.Sub(c.Checked) < Every && now.After(c.Checked) {
			return c.Latest, nil
		}
	}
	return Refresh(ctx, cacheFile, now)
}

// Refresh asks GitHub for the newest release tag now, ignoring the cache,
// and saves the answer for Latest.
func Refresh(ctx context.Context, cacheFile string, now time.Time) (string, error) {
	tag, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	c := cache{Checked: now, Latest: tag}
	if b, err := json.Marshal(c); err == nil {
		_ = os.MkdirAll(filepath.Dir(cacheFile), 0o700)
		_ = os.WriteFile(cacheFile, b, 0o600)
	}
	return tag, nil
}

func fetch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lookout")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", err
	}
	if _, ok := parse(r.TagName); !ok {
		return "", fmt.Errorf("github: unexpected tag %q", r.TagName)
	}
	return r.TagName, nil
}

// Newer reports whether latest is a newer release than current. current
// may be a release ("0.6.3", "v0.6.3") or a git describe version
// ("v0.6.3-2-gabc1234", two commits past v0.6.3), which is newer than
// v0.6.3 but older than v0.6.4. An unknown version ("dev") is never
// reported as out of date.
func Newer(current, latest string) bool {
	cur, curPre, ok1 := parseVersion(current)
	lat, latPre, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	// Same numbers: a release is newer than a pre-release or a build
	// made before it.
	return curPre && !latPre
}

// parse reads the major, minor and patch numbers at the start of v. A
// pre-release ("0.9.5-rc1") or Go pseudo-version ("0.9.5-0.20261112194312-
// cde033955423", a build before 0.9.5) comes before its numbers; a git
// describe version ("0.9.4-3-gabc1234", after 0.9.4) and "-dirty" don't.
func parse(v string) ([3]int, bool) {
	n, _, ok := parseVersion(v)
	return n, ok
}

// gitDescribeRe matches what git describe adds after a tag.
var gitDescribeRe = regexp.MustCompile(`^(\d+-g[0-9a-f]+)?(-?dirty)?$`)

// parseVersion is parse, plus whether v comes before its numbers.
func parseVersion(v string) ([3]int, bool, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	v, suffix, _ := strings.Cut(v, "-")
	pre := suffix != "" && !gitDescribeRe.MatchString(suffix)
	v, _, _ = strings.Cut(v, "+")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false, false
		}
		out[i] = n
	}
	return out, pre, true
}

// pseudoRe matches a Go pseudo-version's suffix.
var pseudoRe = regexp.MustCompile(`-(0\.)?\d{14}-[0-9a-f]{12}$`)

// FromSource reports whether version v is a build from a source checkout
// (git describe or a Go pseudo-version) rather than a release.
func FromSource(v string) bool {
	return v == "dev" || strings.Contains(v, "-g") || pseudoRe.MatchString(v) || strings.HasSuffix(v, "dirty")
}
