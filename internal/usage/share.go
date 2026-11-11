package usage

import (
	"sort"
	"strings"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// Price is a model's API list price in US dollars per million tokens. It is
// used as a relative weight (how much more of a subscription limit a token of
// one model uses than a token of another; Anthropic doesn't publish those
// weights) and to estimate the cost of pay-as-you-go sessions. Either way the
// result is an estimate.
//
// The built-in prices below are hardcoded from Anthropic's published API
// prices and updated by hand in Lookout releases. [usage.price_weights] in
// config.toml overrides them.
//
// CacheWrite is the 5-minute cache write rate. CacheWrite1h, the 1-hour
// rate Claude Code usually uses, defaults to twice Input when zero.
type Price struct{ Input, Output, CacheWrite, CacheWrite1h, CacheRead float64 }

// priceFor maps a model ID to its built-in price. Unknown models get Sonnet
// 4.6's.
func priceFor(model string) Price {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"), strings.Contains(m, "mythos"):
		return Price{10, 50, 12.5, 20, 0.25}
	case strings.Contains(m, "opus-5-5"):
		return Price{4, 20, 5, 8, 0.20}
	case strings.Contains(m, "opus"):
		return Price{5, 25, 6.25, 10, 0.50}
	case strings.Contains(m, "sonnet-5"):
		return Price{2, 10, 2.5, 4, 0.20}
	case strings.Contains(m, "haiku-5"):
		return Price{0.10, 0.50, 0.125, 0.20, 0.01}
	case strings.Contains(m, "haiku"):
		return Price{1, 5, 1.25, 2, 0.10}
	default:
		return Price{3, 15, 3.75, 6, 0.30}
	}
}

// SetPrices sets price overrides, keyed by a substring of the model ID
// (matched case-insensitively; the longest matching key wins). Models no key
// matches keep their built-in price.
func (a *Aggregator) SetPrices(p map[string]Price) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prices = make(map[string]Price, len(p))
	for k, v := range p {
		a.prices[strings.ToLower(k)] = v
	}
}

// priceLocked is the override for model, or its built-in price. a.mu must
// be held.
func (a *Aggregator) priceLocked(model string) Price {
	m := strings.ToLower(model)
	keys := make([]string, 0, len(a.prices))
	for k := range a.prices {
		if k != "" && strings.Contains(m, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return priceFor(model)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return a.prices[keys[0]]
}

// Weight is the token usage priced at the model's built-in API rate, in
// dollars.
func Weight(model string, u transcript.Usage) float64 { return weigh(priceFor(model), u) }

func weigh(p Price, u transcript.Usage) float64 {
	w1h := p.CacheWrite1h
	if w1h == 0 {
		w1h = 2 * p.Input
	}
	h := min(u.CacheCreate1h, u.CacheCreate)
	return (float64(u.Input)*p.Input + float64(u.Output)*p.Output +
		float64(u.CacheCreate-h)*p.CacheWrite + float64(h)*w1h +
		float64(u.CacheRead)*p.CacheRead) / 1e6
}

// Share returns the weighted usage of one session (its subagents included)
// and of every session, between from and to.
func (a *Aggregator) Share(sessionID string, from, to time.Time) (session, all float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prices := map[string]Price{}
	for _, e := range a.entries {
		if e.Time.Before(from) || e.Time.After(to) {
			continue
		}
		p, ok := prices[e.Model]
		if !ok {
			p = a.priceLocked(e.Model)
			prices[e.Model] = p
		}
		w := weigh(p, e.Usage)
		all += w
		if e.SessionID == sessionID {
			session += w
		}
	}
	return session, all
}

// Totals is one session's token use, its subagents included.
type Totals struct {
	Tokens int64   // every token: input, output, cache writes and reads
	Cost   float64 // the tokens at API list prices, in dollars
}

// AllTotals returns every session's totals, by session ID, in one pass.
func (a *Aggregator) AllTotals() map[string]Totals {
	a.mu.Lock()
	defer a.mu.Unlock()
	prices := map[string]Price{}
	out := map[string]Totals{}
	for _, e := range a.entries {
		p, ok := prices[e.Model]
		if !ok {
			p = a.priceLocked(e.Model)
			prices[e.Model] = p
		}
		t := out[e.SessionID]
		t.Tokens += e.Usage.Total()
		t.Cost += weigh(p, e.Usage)
		out[e.SessionID] = t
	}
	return out
}
