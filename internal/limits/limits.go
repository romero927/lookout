// Package limits records the subscription usage limits that Claude Code
// passes to its status line command, and projects them forward.
//
// Claude Code runs `lookout statusline` itself and hands it JSON on stdin.
// That JSON carries rate_limits.five_hour / seven_day (percent used and reset
// time) for Claude.ai subscribers. Lookout never asks Anthropic for these
// numbers; it only keeps what Claude Code already gave the status line.
package limits

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Window lengths of the two subscription limits.
const (
	FiveHour = 5 * time.Hour
	SevenDay = 7 * 24 * time.Hour
)

// How long samples are kept, and how far back "recent pace" looks.
const (
	keepSamples  = 6 * time.Hour
	maxSamples   = 720
	sampleEvery  = time.Minute
	RecentWindow = 30 * time.Minute
)

// Window is one limit as Claude Code reports it.
type Window struct {
	UsedPercent float64 `json:"used_percentage"`
	ResetsAt    int64   `json:"resets_at"` // Unix seconds
}

// Reset returns the reset time.
func (w Window) Reset() time.Time { return time.Unix(w.ResetsAt, 0) }

// Sample is a point-in-time reading, used for the recent pace.
type Sample struct {
	At       int64    `json:"t"`
	FiveHour *float64 `json:"h,omitempty"`
	SevenDay *float64 `json:"d,omitempty"`
}

// State is what Lookout keeps on disk for one session.
type State struct {
	UpdatedAt int64    `json:"updated_at"`
	FiveHour  *Window  `json:"five_hour,omitempty"`
	SevenDay  *Window  `json:"seven_day,omitempty"`
	Samples   []Sample `json:"samples,omitempty"`

	// Context is the context window use Claude Code last reported, in
	// percent, and ContextAt when. Reported for every account type.
	Context   *float64 `json:"ctx,omitempty"`
	ContextAt int64    `json:"ctx_at,omitempty"`

	// CostUSD is the session's cost so far as Claude Code reports it. It is
	// shown only for accounts without subscription limits, where it is what
	// you actually pay.
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// ContextPercent returns the reported context use if it is recent.
func (s State) ContextPercent(now time.Time, maxAge time.Duration) (float64, bool) {
	if s.Context == nil || s.ContextAt == 0 || now.Sub(time.Unix(s.ContextAt, 0)) > maxAge {
		return 0, false
	}
	return *s.Context, true
}

// SeenNoLimits reports whether Claude Code has run the status line but never
// sent limits: an API-key (pay-as-you-go) account, most likely.
func (s State) SeenNoLimits() bool { return s.ContextAt != 0 && !s.HasData() }

// Updated returns when Claude Code last reported limits.
func (s State) Updated() time.Time {
	if s.UpdatedAt == 0 {
		return time.Time{}
	}
	return time.Unix(s.UpdatedAt, 0)
}

// HasData reports whether any limit has ever been recorded.
func (s State) HasData() bool { return s.FiveHour != nil || s.SevenDay != nil }

// StatusInput is the part of Claude Code's status line JSON Lookout uses.
type StatusInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	// EffortRaw is an "effort" field, if Claude Code sends one: a level
	// string or an object with a "level". Effort holds the level, filled by
	// ParseInput or by the caller from the transcript.
	EffortRaw json.RawMessage `json:"effort"`
	Effort    string          `json:"-"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	RateLimits *struct {
		FiveHour *Window `json:"five_hour"`
		SevenDay *Window `json:"seven_day"`
	} `json:"rate_limits"`
}

// ParseInput reads the status line JSON. Unknown fields are ignored.
func ParseInput(r io.Reader) (StatusInput, error) {
	var in StatusInput
	b, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return in, err
	}
	err = json.Unmarshal(b, &in)
	in.Effort = effortLevel(in.EffortRaw)
	return in, err
}

func effortLevel(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Level string `json:"level"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Level
	}
	return ""
}

// Load reads the state file. A missing or unreadable file is an empty state.
func Load(path string) State {
	var s State
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if json.Unmarshal(b, &s) != nil {
		return State{}
	}
	return s
}

// Apply merges a status line reading into the state. It returns false when
// there was nothing to record.
func (s *State) Apply(in StatusInput, now time.Time) bool {
	ctx := false
	if c := in.ContextWindow.UsedPercentage; c != nil {
		v := *c
		s.Context, s.ContextAt, ctx = &v, now.Unix(), true
	}
	if c := in.Cost.TotalCostUSD; c != nil {
		v := *c
		s.CostUSD, s.ContextAt, ctx = &v, now.Unix(), true
	}
	if in.RateLimits == nil || (in.RateLimits.FiveHour == nil && in.RateLimits.SevenDay == nil) {
		return ctx
	}
	s.UpdatedAt = now.Unix()
	if w := in.RateLimits.FiveHour; w != nil {
		c := *w
		s.FiveHour = &c
	}
	if w := in.RateLimits.SevenDay; w != nil {
		c := *w
		s.SevenDay = &c
	}

	smp := Sample{At: now.Unix()}
	if s.FiveHour != nil {
		v := s.FiveHour.UsedPercent
		smp.FiveHour = &v
	}
	if s.SevenDay != nil {
		v := s.SevenDay.UsedPercent
		smp.SevenDay = &v
	}
	if n := len(s.Samples); n == 0 || changed(s.Samples[n-1], smp) || now.Sub(time.Unix(s.Samples[n-1].At, 0)) >= sampleEvery {
		s.Samples = append(s.Samples, smp)
	}
	cut := now.Add(-keepSamples).Unix()
	i := 0
	for i < len(s.Samples) && s.Samples[i].At < cut {
		i++
	}
	s.Samples = s.Samples[i:]
	if len(s.Samples) > maxSamples {
		s.Samples = s.Samples[len(s.Samples)-maxSamples:]
	}
	return true
}

func changed(a, b Sample) bool {
	eq := func(x, y *float64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return !eq(a.FiveHour, b.FiveHour) || !eq(a.SevenDay, b.SevenDay)
}

// Save writes the state atomically.
func Save(path string, s State) error { return writeJSON(path, s) }

// writeJSON writes v to path atomically (temp file, then rename).
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".limits-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Pace is a projection for one limit window.
type Pace struct {
	Used     float64   // percent used now
	Start    time.Time // window start (reset minus window length)
	ResetsAt time.Time
	Expired  bool // the reset time has passed; Used is out of date

	// RatePerHour is percent per hour: the recent pace when there are
	// enough samples, else the average since the window started.
	RatePerHour float64
	Recent      bool
	Known       bool // false when there is too little history to tell

	Projected float64   // percent at reset if the pace holds
	HitsAt    time.Time // when 100% is reached at this pace; zero if not before reset
}

// Level is how worrying a projection is.
type Level int

const (
	LevelOK Level = iota
	LevelWatch
	LevelOver
)

// Level classifies the projection: Over if the limit is reached before
// reset, Watch above 80% (projected or actual).
func (p Pace) Level() Level {
	switch {
	case p.Used >= 100 || !p.HitsAt.IsZero():
		return LevelOver
	case p.Used >= 80 || (p.Known && p.Projected >= 80):
		return LevelWatch
	}
	return LevelOK
}

// Compute projects window w of the given length forward from now. which
// selects that window's value from a sample.
func Compute(w Window, length time.Duration, samples []Sample, which func(Sample) *float64, now time.Time) Pace {
	p := Pace{Used: w.UsedPercent, ResetsAt: w.Reset()}
	p.Start = p.ResetsAt.Add(-length)
	if !now.Before(p.ResetsAt) {
		p.Expired = true
		return p
	}

	// Recent pace: from the oldest sample in the last RecentWindow (and in
	// this window) to now, using the latest value. Idle time counts, so the
	// pace falls when you stop.
	since := now.Add(-RecentWindow)
	if p.Start.After(since) {
		since = p.Start
	}
	var first *Sample
	for i := range samples {
		s := &samples[i]
		if which(*s) == nil || time.Unix(s.At, 0).Before(since) {
			continue
		}
		first = s
		break
	}
	if first != nil {
		span := now.Sub(time.Unix(first.At, 0))
		if delta := w.UsedPercent - *which(*first); span >= 5*time.Minute && delta >= 0 {
			p.RatePerHour = delta / span.Hours()
			p.Recent, p.Known = true, true
		}
	}
	if !p.Known {
		if elapsed := now.Sub(p.Start); elapsed >= 10*time.Minute {
			p.RatePerHour = w.UsedPercent / elapsed.Hours()
			p.Known = true
		}
	}
	if !p.Known {
		p.Projected = p.Used
		return p
	}

	left := p.ResetsAt.Sub(now).Hours()
	p.Projected = p.Used + p.RatePerHour*left
	if p.Used < 100 && p.RatePerHour > 0 {
		toFull := (100 - p.Used) / p.RatePerHour
		if toFull < left {
			p.HitsAt = now.Add(time.Duration(toFull * float64(time.Hour)))
		}
	}
	return p
}

// FiveHourOf / SevenDayOf select a window's value from a sample.
func FiveHourOf(s Sample) *float64 { return s.FiveHour }
func SevenDayOf(s Sample) *float64 { return s.SevenDay }

// Paces returns projections for both windows (nil when not reported).
func (s State) Paces(now time.Time) (five, week *Pace) {
	if s.FiveHour != nil {
		p := Compute(*s.FiveHour, FiveHour, s.Samples, FiveHourOf, now)
		five = &p
	}
	if s.SevenDay != nil {
		// A 30-minute slope says little about a week: use the average.
		p := Compute(*s.SevenDay, SevenDay, nil, SevenDayOf, now)
		week = &p
	}
	return five, week
}

// FiveHourPending is shown when a subscriber's session has no current 5-hour
// reading yet. Claude Code only sends one after a reply, so rather than guess
// (or reuse another session's old number) the line says when it will appear.
const FiveHourPending = "5h: shows after next reply"

// StatusLine is the short text printed under Claude Code's prompt. subscriber
// says the account is known to have usage limits, even if this session has
// not been sent them yet.
func StatusLine(in StatusInput, s State, now time.Time, subscriber bool) string {
	var parts []string
	if m := in.Model.DisplayName; m != "" {
		if in.Effort != "" {
			m += " · effort " + in.Effort
		}
		parts = append(parts, m)
	}
	if c := in.ContextWindow.UsedPercentage; c != nil {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", *c))
	}
	five, week := s.Paces(now)
	if five != nil && !five.Expired {
		parts = append(parts, fmt.Sprintf("5h %.0f%% (resets %s)", five.Used, shortDur(five.ResetsAt.Sub(now))))
	} else if subscriber || week != nil {
		parts = append(parts, FiveHourPending)
	}
	if week != nil && !week.Expired {
		parts = append(parts, fmt.Sprintf("week %.0f%%", week.Used))
	}
	if c := in.Cost.TotalCostUSD; c != nil && in.RateLimits == nil && !s.HasData() && !subscriber {
		parts = append(parts, Money(*c))
	}
	if len(parts) == 0 {
		return "lookout"
	}
	return strings.Join(parts, " · ")
}

func shortDur(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(math.Max(0, d.Minutes())))
	}
}

// Money formats US dollars: "$0.42", "$12.30".
func Money(usd float64) string {
	if usd > 0 && usd < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", usd)
}
