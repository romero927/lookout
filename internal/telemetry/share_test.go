package telemetry

import (
	"math"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

func TestShare(t *testing.T) {
	now := time.Now()
	a := New()
	add := func(sid, model string, at time.Time, out int64) {
		a.entries = append(a.entries, Entry{Time: at, SessionID: sid, Model: model, Usage: transcript.Usage{Output: out}})
	}
	add("s1", "claude-opus-5-5", now.Add(-time.Hour), 1_000_000)   // $20
	add("s2", "claude-sonnet-5-5", now.Add(-time.Hour), 1_000_000) // $10
	add("s1", "claude-opus-5-5", now.Add(-6*time.Hour), 1_000_000) // outside the window

	mine, all := a.Share("s1", now.Add(-5*time.Hour), now)
	if math.Abs(mine-20) > 1e-9 || math.Abs(all-30) > 1e-9 {
		t.Errorf("mine %.2f all %.2f, want 20 and 30", mine, all)
	}
	if w := Weight("claude-haiku-5-5", transcript.Usage{Input: 1_000_000}); math.Abs(w-0.10) > 1e-9 {
		t.Errorf("haiku weight %.3f", w)
	}
	if priceFor("some-new-model") != priceFor("claude-sonnet-4-6") {
		t.Error("unknown models should fall back to Sonnet 4.6")
	}
}
