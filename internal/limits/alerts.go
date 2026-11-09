package limits

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Alert is one notification about a limit window.
type Alert struct {
	Key   string // unique per window and reason, so it fires once
	Text  string
	Level Level
}

// Alerts lists every alert that currently applies: one per threshold the
// window has passed, and one when the current pace reaches 100% before reset.
func Alerts(s State, now time.Time, thresholds []float64, pace bool) []Alert {
	five, week := s.Paces(now)
	var out []Alert
	for _, x := range []struct {
		kind, name string
		p          *Pace
	}{{"h", "5-hour", five}, {"d", "Weekly", week}} {
		if x.p == nil || x.p.Expired {
			continue
		}
		p := *x.p
		resets := p.ResetsAt.Local().Format("15:04")
		if p.ResetsAt.Sub(now) >= 24*time.Hour {
			resets = p.ResetsAt.Local().Format("Mon 15:04")
		}
		base := fmt.Sprintf("%s:%d:", x.kind, p.ResetsAt.Unix())
		top := -1.0
		for _, t := range thresholds {
			if p.Used >= t && t > top {
				top = t
			}
		}
		// Only the highest threshold passed: no burst of alerts at once.
		if top >= 0 {
			lvl := LevelWatch
			if top >= 95 || p.Used >= 100 {
				lvl = LevelOver
			}
			out = append(out, Alert{
				Key:   base + strconv.FormatFloat(top, 'f', -1, 64),
				Text:  fmt.Sprintf("%s limit at %.0f%% (resets %s)", x.name, p.Used, resets),
				Level: lvl,
			})
		}
		if pace && !p.HitsAt.IsZero() && p.Used < 100 {
			out = append(out, Alert{
				Key: base + "pace",
				Text: fmt.Sprintf("%s limit: at this pace you'll reach it at %s, before the %s reset",
					x.name, p.HitsAt.Local().Format("15:04"), resets),
				Level: LevelOver,
			})
		}
	}
	return out
}

// Fired is the set of alert keys already shown, kept on disk so restarts and
// other dashboards don't repeat them.
type Fired map[string]int64 // key -> when fired (Unix seconds)

// LoadFired reads the fired-alerts file.
func LoadFired(path string) Fired {
	f := Fired{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f
}

// SaveFired drops keys for windows that reset long ago and writes the file.
func SaveFired(path string, f Fired, now time.Time) error {
	for k := range f {
		parts := strings.SplitN(k, ":", 3)
		if len(parts) < 2 {
			delete(f, k)
			continue
		}
		if r, err := strconv.ParseInt(parts[1], 10, 64); err != nil || now.Unix()-r > int64(SevenDay/time.Second) {
			delete(f, k)
		}
	}
	return writeJSON(path, f)
}
