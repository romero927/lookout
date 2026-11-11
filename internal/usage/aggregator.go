// Package usage tallies token usage from transcript records.
//
// Prices (share.go) are used in two places. The session-share estimate
// weights tokens by each model's API list price so that, say, an Opus token
// counts for more of a limit than a Haiku token. And for pay-as-you-go
// sessions (Options.Billed) each window carries an estimated cost: tokens
// priced at those list prices, then scaled to match the cost Claude Code
// last reported for each session. Claude Code also bills calls it never
// writes to the transcript (a few percent of a session), which the scaling
// spreads over that session's responses. Windows cut sessions by time, so
// these are still estimates.
package usage

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// Entry is the usage of one API response.
type Entry struct {
	Time      time.Time
	SessionID string
	Model     string
	Subagent  bool
	Usage     transcript.Usage
	key       string
}

// Session is what Lookout knows about one Claude Code session.
type Session struct {
	ID           string
	Project      string
	Cwd          string
	FirstSeen    time.Time
	LastActivity time.Time
	Model        string // model of the latest main-chain response
	Context      int64  // context size of the latest main-chain response
	Compactions  int
	// Title is what the session is about: the title you gave it, else the
	// one Claude Code generated. Empty until Claude Code names it.
	Title       string
	titleCustom bool
}

// Aggregator collects usage entries. It is safe for concurrent use.
type Aggregator struct {
	mu       sync.Mutex
	index    map[string]int // usage key -> index in entries
	entries  []Entry
	sorted   bool
	sessions map[string]*Session
	prices   map[string]Price // config overrides, by model ID substring

	rev       int               // bumped whenever entries change
	totals    map[string]Totals // AllTotals' result, valid while totalsRev == rev
	totalsRev int
}

// New returns an empty Aggregator.
func New() *Aggregator {
	return &Aggregator{index: map[string]int{}, sessions: map[string]*Session{}, sorted: true}
}

// Add records one transcript record. Non-usage records only update
// session metadata.
func (a *Aggregator) Add(r transcript.Record, src transcript.Source) {
	sid := r.SessionID
	if sid == "" {
		sid = src.SessionID
	}
	if sid == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	s := a.sessions[sid]
	if s == nil {
		s = &Session{ID: sid, Project: src.Project}
		a.sessions[sid] = s
	}
	sub := r.Sidechain || src.IsSubagent
	if t := strings.TrimSpace(r.Title); t != "" && !sub && (r.TitleCustom || !s.titleCustom) {
		s.Title, s.titleCustom = t, r.TitleCustom
	}
	if !r.Timestamp.IsZero() && !sub {
		if s.FirstSeen.IsZero() || r.Timestamp.Before(s.FirstSeen) {
			s.FirstSeen = r.Timestamp
		}
		if r.Timestamp.After(s.LastActivity) {
			s.LastActivity = r.Timestamp
			if r.Cwd != "" {
				s.Cwd = r.Cwd
			}
		}
	}
	if s.Cwd == "" && r.Cwd != "" && !sub {
		s.Cwd = r.Cwd
	}

	switch r.Kind {
	case transcript.KindCompact:
		s.Compactions++
		if !sub && !r.Timestamp.Before(s.LastActivity) {
			s.Context = r.CompactPost
		}
		return
	case transcript.KindAssistant:
	default:
		return
	}
	if r.Model == transcript.SyntheticModel || !r.HasUsage {
		return
	}
	if !sub && !r.Timestamp.Before(s.LastActivity) {
		s.Model = r.Model
		s.Context = r.Usage.Context()
	}

	key := r.UsageKey()
	if i, ok := a.index[key]; ok {
		// Another content block of the same response: same usage, maybe
		// more complete. Keep the field-wise maximum.
		a.entries[i].Usage.Max(r.Usage)
		a.rev++
		return
	}
	e := Entry{Time: r.Timestamp, SessionID: sid, Model: r.Model, Subagent: sub, Usage: r.Usage, key: key}
	if n := len(a.entries); n > 0 && e.Time.Before(a.entries[n-1].Time) {
		a.sorted = false
	}
	a.index[key] = len(a.entries)
	a.entries = append(a.entries, e)
	a.rev++
}

// Sessions returns all known sessions, most recently active first.
func (a *Aggregator) Sessions() []Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		if !s.LastActivity.IsZero() {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}

// Current picks the session to focus on: the most recently active one,
// optionally restricted to a working directory. A session in exactly that
// directory wins; otherwise one in a folder inside it, or one it is inside
// (Claude Code may have been started in a parent or a subfolder).
func (a *Aggregator) Current(cwd string) (Session, bool) {
	sessions := a.Sessions()
	if cwd == "" {
		if len(sessions) > 0 {
			return sessions[0], true
		}
		return Session{}, false
	}
	for _, s := range sessions {
		if sameDir(s.Cwd, cwd) {
			return s, true
		}
	}
	for _, s := range sessions {
		if s.Cwd != "" && (within(s.Cwd, cwd) || within(cwd, s.Cwd)) {
			return s, true
		}
	}
	return Session{}, false
}

// Known reports whether the session has any timestamped main-chain
// activity, the same test Sessions uses.
func (a *Aggregator) Known(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	return s != nil && !s.LastActivity.IsZero()
}

// DirMatch ranks how well a session started in sessCwd matches the folder
// filter cwd, following Current: 2 for the same folder (or no filter), 1
// for a folder inside it or one it is inside, 0 for no match.
func DirMatch(sessCwd, cwd string) int {
	switch {
	case cwd == "" || sameDir(sessCwd, cwd):
		return 2
	case sessCwd != "" && (within(sessCwd, cwd) || within(cwd, sessCwd)):
		return 1
	}
	return 0
}

func sameDir(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// within reports whether path is inside dir.
func within(path, dir string) bool {
	dir = strings.TrimRight(dir, "/")
	return dir != "" && strings.HasPrefix(path, dir+"/")
}

// Window is a usage total over some span.
type Window struct {
	Usage    transcript.Usage
	Messages int
	ByModel  map[string]transcript.Usage
	// Cost is the estimated cost, in US dollars, of the pay-as-you-go
	// sessions in the window (see Options.Billed). CostByModel splits it.
	Cost        float64
	CostByModel map[string]float64
}

// add counts one entry; cost is its estimated cost, 0 when not billed.
func (w *Window) add(e Entry, cost float64) {
	w.Usage.Add(e.Usage)
	w.Messages++
	if w.ByModel == nil {
		w.ByModel = map[string]transcript.Usage{}
	}
	u := w.ByModel[e.Model]
	u.Add(e.Usage)
	w.ByModel[e.Model] = u
	if cost > 0 {
		w.Cost += cost
		if w.CostByModel == nil {
			w.CostByModel = map[string]float64{}
		}
		w.CostByModel[e.Model] += cost
	}
}

// Block is an estimated usage block: it starts at the first message after
// a gap (floored to the hour) and lasts BlockLength. This mirrors how
// Claude's rolling limits are commonly described, but it is an estimate.
type Block struct {
	Start, End time.Time
	Last       time.Time
	Window
	Active bool
}

// DayTotal is one calendar day (local time).
type DayTotal struct {
	Day   time.Time
	Usage transcript.Usage
	Cost  float64 // estimated, pay-as-you-go sessions only
}

// Options tune a Snapshot.
type Options struct {
	SessionID   string
	BurnWindow  time.Duration
	BlockLength time.Duration
	Days        int
	// Billed, when set, picks the pay-as-you-go sessions whose tokens are
	// priced into each window's Cost (ok), with the cost Claude Code last
	// reported for the session and when (usd is 0 when unknown). Nil means
	// no costs.
	Billed func(sessionID string) (usd float64, at time.Time, ok bool)
}

// Snapshot is everything the Telemetry panel shows.
type Snapshot struct {
	Now          time.Time
	Session      Window
	SessionSub   transcript.Usage // subagent share of Session
	Today        Window
	Rolling      Window  // last BlockLength
	Block        *Block  // latest block; nil if none
	BurnPerMin   float64 // all token categories
	FreshPerMin  float64 // excluding cache reads, which dominate the total
	OutputPerMin float64
	// ProjectedBlock is the block total if the current burn rate holds
	// until the block ends. Zero when no block is active.
	ProjectedBlock int64
	Days           []DayTotal
	TotalEntries   int
}

// maxCostScale caps costScale. Calls missing from the transcript add a few
// percent; far more means the two figures don't describe the same
// responses.
const maxCostScale = 1.5

// costScale is the factor that brings a session's list-price estimate up to
// the cost Claude Code reported for it. It is never below 1: a lower report
// means Claude Code's count restarted (a resumed session) rather than that
// the transcript overcounts.
func costScale(reported, estimate float64) float64 {
	if reported <= 0 || estimate <= 0 {
		return 1
	}
	return min(max(reported/estimate, 1), maxCostScale)
}

// Snapshot computes totals as of now.
func (a *Aggregator) Snapshot(now time.Time, o Options) Snapshot {
	if o.BurnWindow <= 0 {
		o.BurnWindow = 10 * time.Minute
	}
	if o.BlockLength <= 0 {
		o.BlockLength = 5 * time.Hour
	}
	if o.Days <= 0 {
		o.Days = 7
	}
	a.mu.Lock()
	if !a.sorted {
		sort.SliceStable(a.entries, func(i, j int) bool { return a.entries[i].Time.Before(a.entries[j].Time) })
		for i, e := range a.entries {
			a.index[e.key] = i
		}
		a.sorted = true
	}
	entries := a.entries // read-only while locked
	defer a.mu.Unlock()

	snap := Snapshot{Now: now, TotalEntries: len(entries)}
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	firstDay := today.AddDate(0, 0, -(o.Days - 1))
	days := make([]DayTotal, o.Days)
	for i := range days {
		days[i].Day = firstDay.AddDate(0, 0, i)
	}
	rollStart := now.Add(-o.BlockLength)
	burnStart := now.Add(-o.BurnWindow)
	var burn, burnOut, burnFresh int64

	prices := map[string]Price{}
	listCost := func(e Entry) float64 {
		p, ok := prices[e.Model]
		if !ok {
			p = a.priceLocked(e.Model)
			prices[e.Model] = p
		}
		return weigh(p, e.Usage)
	}
	// scale is, per billed session, what to multiply its list-price cost by
	// (absent: not billed).
	scale := map[string]float64{}
	if o.Billed != nil {
		type rep struct {
			usd      float64
			at       time.Time
			ok       bool
			estimate float64
		}
		reps := map[string]*rep{}
		for _, e := range entries {
			r := reps[e.SessionID]
			if r == nil {
				r = &rep{}
				r.usd, r.at, r.ok = o.Billed(e.SessionID)
				reps[e.SessionID] = r
			}
			// Responses up to the report; it comes after the reply, to
			// the second.
			if r.ok && r.usd > 0 && !e.Time.After(r.at.Add(time.Second)) {
				r.estimate += listCost(e)
			}
		}
		for sid, r := range reps {
			if r.ok {
				scale[sid] = costScale(r.usd, r.estimate)
			}
		}
	}
	costOf := func(e Entry) float64 {
		f, ok := scale[e.SessionID]
		if !ok {
			return 0
		}
		return listCost(e) * f
	}

	var blk *Block
	for _, e := range entries {
		cost := costOf(e)
		if o.SessionID != "" && e.SessionID == o.SessionID {
			snap.Session.add(e, cost)
			if e.Subagent {
				snap.SessionSub.Add(e.Usage)
			}
		}
		if e.Time.IsZero() || e.Time.After(now) {
			continue
		}
		lt := e.Time.In(loc)
		if !lt.Before(today) {
			snap.Today.add(e, cost)
		}
		if !lt.Before(firstDay) {
			d := int(lt.Sub(firstDay).Hours() / 24)
			// DST-safe: find the right bucket by date.
			for d >= 0 && d < len(days) && lt.Before(days[d].Day) {
				d--
			}
			for d+1 < len(days) && !lt.Before(days[d+1].Day) {
				d++
			}
			if d >= 0 && d < len(days) {
				days[d].Usage.Add(e.Usage)
				days[d].Cost += cost
			}
		}
		if e.Time.After(rollStart) {
			snap.Rolling.add(e, cost)
		}
		if e.Time.After(burnStart) {
			burn += e.Usage.Total()
			burnOut += e.Usage.Output
			burnFresh += e.Usage.Total() - e.Usage.CacheRead
		}
		// Block estimation over all entries in time order.
		if blk == nil || !e.Time.Before(blk.End) || e.Time.Sub(blk.Last) >= o.BlockLength {
			start := e.Time.Truncate(time.Hour)
			blk = &Block{Start: start, End: start.Add(o.BlockLength)}
		}
		blk.Last = e.Time
		blk.add(e, cost)
	}
	snap.Days = days
	mins := o.BurnWindow.Minutes()
	snap.BurnPerMin = float64(burn) / mins
	snap.OutputPerMin = float64(burnOut) / mins
	snap.FreshPerMin = float64(burnFresh) / mins
	if blk != nil {
		blk.Active = now.Before(blk.End)
		snap.Block = blk
		if blk.Active {
			remaining := blk.End.Sub(now).Minutes()
			snap.ProjectedBlock = blk.Usage.Total() + int64(snap.BurnPerMin*remaining)
		}
	}
	return snap
}
