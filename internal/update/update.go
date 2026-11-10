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
	cur, ok1 := parse(current)
	lat, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parse reads the major, minor and patch numbers at the start of v.
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	v, _, _ = strings.Cut(v, "+")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
