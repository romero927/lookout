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

// A pay-as-you-go session (an enterprise account billed by usage, say)
// shows its cost even when another account on this computer has limits.
func TestSubscriberPerSession(t *testing.T) {
	dir := t.TempDir()
	_ = Save(filepath.Join(dir, "personal.json"), State{UpdatedAt: 1, SevenDay: &Window{UsedPercent: 5}})
	zero, spent := 0.0, 1.25

	if !Subscriber(State{}, dir) {
		t.Error("before the first reply, other sessions decide")
	}
	if !Subscriber(State{CostUSD: &zero, ContextAt: 1}, dir) {
		t.Error("no cost yet: other sessions decide")
	}
	st := State{CostUSD: &spent, ContextAt: 1}
	if Subscriber(st, dir) {
		t.Error("cost without limits is pay-as-you-go")
	}
	if !Subscriber(State{CostUSD: &spent, UpdatedAt: 1, FiveHour: &Window{UsedPercent: 3}}, dir) {
		t.Error("a session with limits is a subscriber")
	}

	in := StatusInput{}
	in.Model.DisplayName = "Opus 5.5"
	in.Cost.TotalCostUSD = &spent
	if got := StatusLine(in, st, time.Unix(1_000_000, 0), Subscriber(st, dir)); got != "Opus 5.5 · $1.25" {
		t.Errorf("status line = %q", got)
	}
}
