package strategy

import (
	"testing"
	"time"
)

func TestStabilizerHoldsBeforeSwitching(t *testing.T) {
	var st Stabilizer
	t0 := time.Now()
	keep := Suggestion{SessionID: "s", Action: Keep, Suggested: "opus"}
	down := Suggestion{SessionID: "s", Action: Downgrade, Suggested: "sonnet"}

	if got, p := st.Update(keep, 5, t0); got.Action != Keep || p != nil {
		t.Fatal("first suggestion should show at once")
	}
	// A flip that lasts under a minute never shows.
	if got, p := st.Update(down, 5, t0.Add(10*time.Second)); got.Action != Keep || p == nil || p.Action != Downgrade {
		t.Fatalf("downgrade should wait: %v %v", got.Action, p)
	}
	if got, p := st.Update(keep, 5, t0.Add(40*time.Second)); got.Action != Keep || p != nil {
		t.Fatal("back to keep clears the candidate")
	}
	// Held for 2 minutes: shown.
	st.Update(down, 5, t0.Add(time.Minute))
	if got, _ := st.Update(down, 5, t0.Add(3*time.Minute)); got.Action != Downgrade {
		t.Fatal("downgrade held 2 minutes should show")
	}
	// Or across two new turns, sooner.
	up := Suggestion{SessionID: "s", Action: Upgrade, Suggested: "opus"}
	st.Update(up, 5, t0.Add(4*time.Minute))
	if got, _ := st.Update(up, 7, t0.Add(4*time.Minute+20*time.Second)); got.Action != Upgrade {
		t.Fatal("upgrade held across two turns should show")
	}
	// A new session starts fresh.
	if got, p := st.Update(Suggestion{SessionID: "x", Action: Keep}, 0, t0); got.SessionID != "x" || p != nil {
		t.Fatal("new session should reset")
	}
}
