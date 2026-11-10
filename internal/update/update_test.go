package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.6.3", "v0.6.4", true},
		{"v0.6.3", "v0.6.3", false},
		{"0.6.3", "v0.7.0", true},
		{"0.6.3", "v1.0.0", true},
		{"0.7.0", "v0.6.9", false},
		{"v0.6.3-2-gabc1234", "v0.6.3", false}, // past the tag already
		{"v0.6.3-2-gabc1234", "v0.6.4", true},
		{"v0.6.3-2-gabc1234-dirty", "v0.6.4", true},
		{"dev", "v0.6.4", false},
		{"0.6.3", "nightly", false},
		{"", "v0.6.4", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestLatestUsesFreshCache(t *testing.T) {
	f := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	b, _ := json.Marshal(cache{Checked: now.Add(-time.Hour), Latest: "v9.9.9"})
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// A cancelled context makes any network request fail, so a result
	// proves the cache was used.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := Latest(ctx, f, now)
	if err != nil || got != "v9.9.9" {
		t.Fatalf("Latest = %q, %v; want cached v9.9.9", got, err)
	}
	// A stale cache goes to the network (which fails here).
	if _, err := Latest(ctx, f, now.Add(2*Every)); err == nil {
		t.Error("stale cache should not be used")
	}
}
