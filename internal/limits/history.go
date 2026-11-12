package limits

import (
	"encoding/json"
	"os"
	"sort"
	"time"
)

// History records how much of each limit was used per local day. The status
// line command updates it on every reading, so it builds up whether or not
// the dashboard is open.
type History struct {
	Days    []DayUse     `json:"days"`
	Windows []seenWindow `json:"windows"`
}

// DayUse is one local day.
type DayUse struct {
	Day string `json:"day"` // YYYY-MM-DD, local time
	// Weekly and FiveHour are percentage points of each limit used that day.
	Weekly   float64 `json:"wk"`
	FiveHour float64 `json:"h"`
	// FiveHourPeak is the highest 5-hour reading that day.
	FiveHourPeak float64 `json:"h_peak"`
}

// Date parses Day.
func (d DayUse) Date() time.Time {
	t, _ := time.ParseInLocation("2006-01-02", d.Day, time.Local)
	return t
}

// seenWindow is the last reading of one limit window.
type seenWindow struct {
	Kind     string  `json:"k"` // "h" or "d"
	ResetsAt int64   `json:"r"`
	Used     float64 `json:"u"`
}

const (
	keepDays = 35
	// A window's reset time can be reported a little differently between
	// readings; readings this close belong to the same window.
	sameWindow = 10 * 60
)

// LoadHistory reads the history file. Missing or broken means empty.
func LoadHistory(path string) History {
	var h History
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &h) != nil {
		return History{}
	}
	return h
}

// SaveHistory writes the history atomically.
func SaveHistory(path string, h History) error { return writeJSON(path, h) }

// Record adds a status line reading. Usage counts towards today when a
// window's figure rises, and when a new window appears after the previous
// one of that kind expired (the usage since reset). The first window ever
// seen, or a second one alongside a still-active window (another account),
// only sets a baseline.
func (h *History) Record(in StatusInput, now time.Time) {
	if in.RateLimits == nil {
		return
	}
	day := h.today(now)
	if w := in.RateLimits.FiveHour; w != nil {
		d := h.observe("h", *w, now)
		day.FiveHour += d
		day.FiveHourPeak = max(day.FiveHourPeak, w.UsedPercent)
	}
	if w := in.RateLimits.SevenDay; w != nil {
		day.Weekly += h.observe("d", *w, now)
	}
	h.prune(now)
}

func (h *History) today(now time.Time) *DayUse {
	key := now.Local().Format("2006-01-02")
	if n := len(h.Days); n > 0 && h.Days[n-1].Day == key {
		return &h.Days[n-1]
	}
	h.Days = append(h.Days, DayUse{Day: key})
	sort.Slice(h.Days, func(i, j int) bool { return h.Days[i].Day < h.Days[j].Day })
	for i := range h.Days {
		if h.Days[i].Day == key {
			return &h.Days[i]
		}
	}
	return &h.Days[len(h.Days)-1]
}

// observe updates the window's last reading and returns the increase.
func (h *History) observe(kind string, w Window, now time.Time) float64 {
	for i := range h.Windows {
		s := &h.Windows[i]
		if s.Kind == kind && abs64(s.ResetsAt-w.ResetsAt) <= sameWindow {
			// Use only goes up within a window. Another session may still
			// report an older, lower figure: keep the highest, so the next
			// rise isn't counted twice.
			d := max(0, w.UsedPercent-s.Used)
			s.Used, s.ResetsAt = max(s.Used, w.UsedPercent), w.ResetsAt
			return d
		}
	}
	var hadExpired, hasActive bool
	for _, s := range h.Windows {
		if s.Kind != kind {
			continue
		}
		if s.ResetsAt <= now.Unix() {
			hadExpired = true
		} else {
			hasActive = true
		}
	}
	h.Windows = append(h.Windows, seenWindow{Kind: kind, ResetsAt: w.ResetsAt, Used: w.UsedPercent})
	if hadExpired && !hasActive {
		return w.UsedPercent
	}
	return 0
}

func (h *History) prune(now time.Time) {
	cutDay := now.Local().AddDate(0, 0, -keepDays).Format("2006-01-02")
	i := 0
	for i < len(h.Days) && h.Days[i].Day < cutDay {
		i++
	}
	h.Days = h.Days[i:]
	// Keep expired windows for a while: they tell a reset from a first run.
	cut := now.Add(-SevenDay - 24*time.Hour).Unix()
	kept := h.Windows[:0]
	for _, s := range h.Windows {
		if s.ResetsAt >= cut {
			kept = append(kept, s)
		}
	}
	h.Windows = kept
}

// Last returns up to n days ending today, oldest first, with empty days
// filled in. It returns nil when nothing has been recorded.
func (h History) Last(n int, now time.Time) []DayUse {
	if len(h.Days) == 0 {
		return nil
	}
	by := map[string]DayUse{}
	for _, d := range h.Days {
		by[d.Day] = d
	}
	out := make([]DayUse, 0, n)
	today := now.Local()
	for i := n - 1; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		d, ok := by[key]
		if !ok {
			d = DayUse{Day: key}
		}
		out = append(out, d)
	}
	return out
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
