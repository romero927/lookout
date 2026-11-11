package usage

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

func TestSharePriceOverrides(t *testing.T) {
	now := time.Now()
	a := New()
	add := func(sid, model string, out int64) {
		a.entries = append(a.entries, Entry{Time: now.Add(-time.Hour), SessionID: sid, Model: model, Usage: transcript.Usage{Output: out}})
	}
	add("s1", "claude-opus-5-5", 1_000_000)
	add("s2", "claude-sonnet-5-5", 1_000_000)
	add("s3", "claude-haiku-5-5", 1_000_000) // no override: built-in $0.50

	a.SetPrices(map[string]Price{
		"OPUS":     {Output: 100}, // case-insensitive
		"opus-5-5": {Output: 30},  // longer key wins over "opus"
		"sonnet":   {Output: 10},
	})
	mine, all := a.Share("s1", now.Add(-5*time.Hour), now)
	if math.Abs(mine-30) > 1e-9 || math.Abs(all-40.5) > 1e-9 {
		t.Errorf("mine %.2f all %.2f, want 30 and 40.5", mine, all)
	}
	a.SetPrices(nil)
	if mine, _ := a.Share("s1", now.Add(-5*time.Hour), now); math.Abs(mine-20) > 1e-9 {
		t.Errorf("clearing overrides should restore built-in prices, got %.2f", mine)
	}
}

func TestSnapshotCostsBilledSessionsOnly(t *testing.T) {
	now := time.Date(2026, 3, 4, 15, 0, 0, 0, time.Local)
	a := New()
	add := func(sid, model string, at time.Time, out int64) {
		a.entries = append(a.entries, Entry{Time: at, SessionID: sid, Model: model, Usage: transcript.Usage{Output: out}})
	}
	add("api", "claude-opus-5-5", now.Add(-time.Hour), 1_000_000)    // $20
	add("api", "claude-sonnet-5-5", now.Add(-time.Hour), 1_000_000)  // $10
	add("sub", "claude-opus-5-5", now.Add(-time.Hour), 1_000_000)    // subscriber: not priced
	add("api", "claude-opus-5-5", now.Add(-24*time.Hour), 1_000_000) // yesterday: $20

	billed := func(id string) (float64, time.Time, bool) { return 0, time.Time{}, id == "api" }
	s := a.Snapshot(now, Options{SessionID: "api", Billed: billed})
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(s.Today.Cost, 30) || !near(s.Rolling.Cost, 30) || !near(s.Session.Cost, 50) {
		t.Errorf("today %.2f rolling %.2f session %.2f, want 30, 30, 50", s.Today.Cost, s.Rolling.Cost, s.Session.Cost)
	}
	if !near(s.Today.CostByModel["claude-opus-5-5"], 20) || !near(s.Today.CostByModel["claude-sonnet-5-5"], 10) {
		t.Errorf("by model %v", s.Today.CostByModel)
	}
	if last, prev := s.Days[len(s.Days)-1], s.Days[len(s.Days)-2]; !near(last.Cost, 30) || !near(prev.Cost, 20) {
		t.Errorf("days: today %.2f yesterday %.2f, want 30 and 20", last.Cost, prev.Cost)
	}
	// Price overrides apply to costs too.
	a.SetPrices(map[string]Price{"opus": {Output: 100}})
	if s := a.Snapshot(now, Options{Billed: billed}); !near(s.Today.Cost, 110) {
		t.Errorf("with override today %.2f, want 110", s.Today.Cost)
	}
	if s := a.Snapshot(now, Options{}); s.Today.Cost != 0 || s.Today.CostByModel != nil {
		t.Errorf("no Billed should mean no costs: %+v", s.Today)
	}
}

func TestWeightCacheWriteTTL(t *testing.T) {
	// Opus 5.5: 5-minute writes at $5/M, 1-hour writes at twice input ($8/M).
	u := transcript.Usage{CacheCreate: 3_000_000, CacheCreate1h: 2_000_000}
	if w := Weight("claude-opus-5-5", u); math.Abs(w-(5+16)) > 1e-9 {
		t.Errorf("weight %.3f, want 21", w)
	}
	// An override without a 1-hour rate falls back to twice its input.
	if w := weigh(Price{Input: 1, CacheWrite: 3}, u); math.Abs(w-(3+4)) > 1e-9 {
		t.Errorf("override weight %.3f, want 7", w)
	}
}

func TestSnapshotCostsScaleToReported(t *testing.T) {
	now := time.Date(2026, 3, 4, 15, 0, 0, 0, time.Local)
	a := New()
	add := func(sid string, at time.Time, out int64) {
		a.entries = append(a.entries, Entry{Time: at, SessionID: sid, Model: "claude-opus-5-5", Usage: transcript.Usage{Output: out}})
	}
	reportAt := now.Add(-30 * time.Minute)
	add("api", now.Add(-26*time.Hour), 1_000_000) // $20, two days back
	add("api", now.Add(-time.Hour), 1_000_000)    // $20
	add("api", now.Add(-10*time.Minute), 500_000) // $10, after the report
	add("resumed", now.Add(-time.Hour), 1_000_000)
	add("odd", now.Add(-time.Hour), 1_000_000)
	reported := map[string]float64{
		"api":     44,  // $40 in the transcript up to the report: scale 1.1
		"resumed": 5,   // below the estimate: Claude Code restarted its count
		"odd":     100, // far above: capped
	}
	billed := func(id string) (float64, time.Time, bool) { return reported[id], reportAt, true }
	s := a.Snapshot(now, Options{SessionID: "api", Billed: billed})
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(s.Session.Cost, 55) {
		t.Errorf("session %.2f, want 55 (all three responses at 1.1)", s.Session.Cost)
	}
	if want := 20*1.1 + 10*1.1 + 20 + 20*maxCostScale; !near(s.Today.Cost, want) {
		t.Errorf("today %.2f, want %.2f", s.Today.Cost, want)
	}
}
