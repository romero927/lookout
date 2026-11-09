package limits

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHasLimits(t *testing.T) {
	dir := t.TempDir()
	if HasLimits(dir) {
		t.Fatal("empty dir has no limits")
	}
	ctx := 3.0
	_ = Save(filepath.Join(dir, "a.json"), State{Context: &ctx, ContextAt: 1})
	if HasLimits(dir) {
		t.Fatal("context-only session is not a subscriber")
	}
	_ = Save(filepath.Join(dir, "b.json"), State{UpdatedAt: 1, SevenDay: &Window{UsedPercent: 5}})
	if !HasLimits(dir) {
		t.Fatal("expected limits")
	}
}

func TestStatusLinePending(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	future := now.Add(time.Hour).Unix()
	cost := 0.5
	in := StatusInput{}
	in.Cost.TotalCostUSD = &cost

	// First message of a subscriber's session: no limits sent yet.
	got := StatusLine(in, State{}, now, true)
	if !strings.Contains(got, FiveHourPending) || strings.Contains(got, "$") {
		t.Fatalf("got %q, want pending note and no cost", got)
	}

	// Weekly sent but no 5-hour yet.
	got = StatusLine(StatusInput{}, State{SevenDay: &Window{UsedPercent: 20, ResetsAt: future}}, now, false)
	if !strings.Contains(got, FiveHourPending) || !strings.Contains(got, "week 20%") {
		t.Fatalf("got %q", got)
	}

	// 5-hour reading has reset.
	s := State{FiveHour: &Window{UsedPercent: 90, ResetsAt: now.Add(-time.Minute).Unix()}}
	if got := StatusLine(StatusInput{}, s, now, true); !strings.Contains(got, FiveHourPending) || strings.Contains(got, "90%") {
		t.Fatalf("got %q", got)
	}

	// API-key account: no note, cost shown.
	if got := StatusLine(in, State{}, now, false); strings.Contains(got, "5h") || !strings.Contains(got, "$0.50") {
		t.Fatalf("got %q", got)
	}
}
