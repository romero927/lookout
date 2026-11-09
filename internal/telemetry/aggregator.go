// Package telemetry tallies token usage from transcript records.
//
// Tokens only: Lookout never converts usage into money.
package telemetry

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
}

// Aggregator collects usage entries. It is safe for concurrent use.
type Aggregator struct {
	mu       sync.Mutex
	index    map[string]int // usage key -> index in entries
	entries  []Entry
	sorted   bool
	sessions map[string]*Session
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
		return
	}
	e := Entry{Time: r.Timestamp, SessionID: sid, Model: r.Model, Subagent: sub, Usage: r.Usage, key: key}
	if n := len(a.entries); n > 0 && e.Time.Before(a.entries[n-1].Time) {
		a.sorted = false
	}
	a.index[key] = len(a.entries)
	a.entries = append(a.entries, e)
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
}

func (w *Window) add(e Entry) {
	w.Usage.Add(e.Usage)
	w.Messages++
	if w.ByModel == nil {
		w.ByModel = map[string]transcript.Usage{}
	}
	u := w.ByModel[e.Model]
	u.Add(e.Usage)
	w.ByModel[e.Model] = u
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
}

// Options tune a Snapshot.
type Options struct {
	SessionID   string
	BurnWindow  time.Duration
	BlockLength time.Duration
	Days        int
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

	var blk *Block
	for _, e := range entries {
		if o.SessionID != "" && e.SessionID == o.SessionID {
			snap.Session.add(e)
			if e.Subagent {
				snap.SessionSub.Add(e.Usage)
			}
		}
		if e.Time.IsZero() || e.Time.After(now) {
			continue
		}
		lt := e.Time.In(loc)
		if !lt.Before(today) {
			snap.Today.add(e)
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
			}
		}
		if e.Time.After(rollStart) {
			snap.Rolling.add(e)
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
		blk.add(e)
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
