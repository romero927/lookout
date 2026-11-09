package strategy

import "time"

// Settling: a new suggestion must hold this long, or across this many new
// turns, before it replaces the one shown. Limit projections and turn
// signals move a lot from minute to minute; a suggestion that flips back
// within a minute isn't worth acting on.
const (
	SettleTime  = 2 * time.Minute
	SettleTurns = 2
)

// Stabilizer smooths suggestions for one session at a time.
type Stabilizer struct {
	session   string
	shown     *Suggestion
	candidate *Suggestion
	since     time.Time
	turnsAt   int
}

func key(sg Suggestion) string { return string(sg.Action) + "|" + sg.Suggested }

// Update takes the latest raw suggestion and the number of turns in the
// session, and returns the suggestion to show plus a pending one (nil when
// nothing is waiting to settle).
func (st *Stabilizer) Update(sg Suggestion, turns int, now time.Time) (shown Suggestion, pending *Suggestion) {
	if st.session != sg.SessionID || st.shown == nil {
		*st = Stabilizer{session: sg.SessionID, shown: &sg}
		return sg, nil
	}
	if key(sg) == key(*st.shown) {
		// Same call: keep its details fresh, drop any candidate.
		st.shown, st.candidate = &sg, nil
		return sg, nil
	}
	if st.candidate == nil || key(*st.candidate) != key(sg) {
		st.candidate, st.since, st.turnsAt = &sg, now, turns
	} else {
		st.candidate = &sg
	}
	if now.Sub(st.since) >= SettleTime || turns-st.turnsAt >= SettleTurns {
		st.shown, st.candidate = &sg, nil
		return sg, nil
	}
	shownNow := *st.shown
	shownNow.Context = sg.Context // context advice is a measurement, not a call
	return shownNow, st.candidate
}
