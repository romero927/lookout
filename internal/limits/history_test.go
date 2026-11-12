package limits

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func reading(five, week float64, fiveReset, weekReset time.Time) StatusInput {
	var in StatusInput
	in.RateLimits = &struct {
		FiveHour *Window `json:"five_hour"`
		SevenDay *Window `json:"seven_day"`
	}{
		FiveHour: &Window{UsedPercent: five, ResetsAt: fiveReset.Unix()},
		SevenDay: &Window{UsedPercent: week, ResetsAt: weekReset.Unix()},
	}
	return in
}

func TestHistoryRecord(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	r1 := now.Add(2 * time.Hour)
	wk := now.Add(3 * 24 * time.Hour)
	var h History

	// First reading ever: a baseline, nothing counted.
	h.Record(reading(40, 30, r1, wk), now)
	if d := h.Days[0]; d.Weekly != 0 || d.FiveHour != 0 || d.FiveHourPeak != 40 {
		t.Fatalf("baseline: %+v", d)
	}
	// Rises count. A reset time a few seconds off is the same window.
	h.Record(reading(55, 34, r1.Add(3*time.Second), wk), now.Add(time.Hour))
	if d := h.Days[0]; d.Weekly != 4 || d.FiveHour != 15 || d.FiveHourPeak != 55 {
		t.Fatalf("rise: %+v", d)
	}
	// After the 5h window resets, the new window's usage counts in full.
	r2 := r1.Add(5 * time.Hour)
	h.Record(reading(10, 36, r2, wk), r1.Add(time.Minute))
	if d := h.Days[0]; d.FiveHour != 25 || d.Weekly != 6 {
		t.Fatalf("after reset: %+v", d)
	}
	// Another account's active window alongside: baseline only.
	h.Record(reading(70, 90, r2.Add(time.Hour), wk.Add(48*time.Hour)), r1.Add(2*time.Minute))
	if d := h.Days[0]; d.FiveHour != 25 || d.Weekly != 6 {
		t.Fatalf("second account: %+v", d)
	}
	// Next day gets its own bucket.
	h.Record(reading(20, 40, r2, wk), now.Add(24*time.Hour))
	if len(h.Days) != 2 || h.Days[1].Weekly != 4 {
		t.Fatalf("next day: %+v", h.Days)
	}

	days := h.Last(7, now.Add(24*time.Hour))
	if len(days) != 7 || days[6].Weekly != 4 || days[5].Weekly != 6 || days[0].Weekly != 0 {
		t.Fatalf("Last: %+v", days)
	}
	if (History{}).Last(7, now) != nil {
		t.Fatal("empty history should give nil")
	}

	path := filepath.Join(t.TempDir(), "h.json")
	if err := SaveHistory(path, h); err != nil {
		t.Fatal(err)
	}
	if got := LoadHistory(path); len(got.Days) != 2 || len(got.Windows) != len(h.Windows) {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestHistoryPrunesOldDays(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	h := History{Days: []DayUse{{Day: "2026-09-01", Weekly: 5}}}
	h.Record(reading(1, 1, now.Add(time.Hour), now.Add(48*time.Hour)), now)
	if len(h.Days) != 1 || h.Days[0].Day != "2026-11-09" {
		t.Fatalf("old day kept: %+v", h.Days)
	}
}

func TestAlerts(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	st := State{FiveHour: &Window{UsedPercent: 96, ResetsAt: reset.Unix()}}

	a := Alerts(st, now, []float64{80, 95}, true)
	if len(a) < 1 || a[0].Level != LevelOver || a[0].Key != "h:"+itoa(reset.Unix())+":95" {
		t.Fatalf("alerts: %+v", a)
	}
	// Only the highest threshold, not one per threshold.
	for _, x := range a {
		if x.Key == "h:"+itoa(reset.Unix())+":80" {
			t.Fatal("80% alert should be folded into 95%")
		}
	}
	if got := Alerts(State{FiveHour: &Window{UsedPercent: 50, ResetsAt: reset.Unix()}}, now, []float64{80, 95}, false); len(got) != 0 {
		t.Fatalf("50%% should not alert: %+v", got)
	}

	// Fast pace: 40% used one hour into the window projects past 100%.
	st = State{FiveHour: &Window{UsedPercent: 40, ResetsAt: now.Add(4 * time.Hour).Unix()}}
	got := Alerts(st, now, nil, true)
	if len(got) != 1 || got[0].Key[len(got[0].Key)-4:] != "pace" {
		t.Fatalf("pace alert: %+v", got)
	}

	path := filepath.Join(t.TempDir(), "alerts.json")
	f := Fired{"h:" + itoa(now.Unix()) + ":95": now.Unix(), "h:1:80": 1, "junk": 1}
	if err := SaveFired(path, f, now); err != nil {
		t.Fatal(err)
	}
	if got := LoadFired(path); len(got) != 1 {
		t.Fatalf("expired keys kept: %v", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHistoryIgnoresOlderReadingFromAnotherSession(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	r1, wk := now.Add(2*time.Hour), now.Add(3*24*time.Hour)
	var h History
	// Session A rises 38 → 42 while idle session B keeps reporting 38.
	for i, five := range []float64{38, 40, 38, 41, 38, 42} {
		h.Record(reading(five, 30, r1, wk), now.Add(time.Duration(i)*time.Minute))
	}
	if d := h.Days[0]; d.FiveHour != 4 {
		t.Fatalf("FiveHour = %v, want 4 (38 → 42)", d.FiveHour)
	}
}

func TestAlertFiresOnceWhenResetTimeJitters(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	key := func(r time.Time) string {
		a := Alerts(State{FiveHour: &Window{UsedPercent: 85, ResetsAt: r.Unix()}}, now, []float64{80}, false)
		if len(a) != 1 {
			t.Fatalf("alerts: %+v", a)
		}
		return a[0].Key
	}
	if a, b := key(reset.Add(-time.Second)), key(reset.Add(time.Second)); a != b {
		t.Fatalf("keys differ for one window: %s vs %s", a, b)
	}
}
