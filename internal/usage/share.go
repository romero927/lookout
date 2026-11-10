package usage

import (
	"strings"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

// price is a model's API list price in US dollars per million tokens. It is
// used only as a relative weight: how much more of a subscription limit a
// token of one model uses than a token of another. Anthropic doesn't publish
// those weights, so anything built on this is an estimate.
type price struct{ in, out, cacheWrite, cacheRead float64 }

// priceFor maps a model ID to its price. Unknown models get Sonnet 4.6's.
func priceFor(model string) price {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"), strings.Contains(m, "mythos"):
		return price{10, 50, 12.5, 0.25}
	case strings.Contains(m, "opus-5-5"):
		return price{4, 20, 5, 0.20}
	case strings.Contains(m, "opus"):
		return price{5, 25, 6.25, 0.50}
	case strings.Contains(m, "sonnet-5"):
		return price{2, 10, 2.5, 0.20}
	case strings.Contains(m, "haiku-5"):
		return price{0.10, 0.50, 0.125, 0.01}
	case strings.Contains(m, "haiku"):
		return price{1, 5, 1.25, 0.10}
	default:
		return price{3, 15, 3.75, 0.30}
	}
}

// Weight is the token usage priced at the model's API rate, in dollars.
func Weight(model string, u transcript.Usage) float64 {
	p := priceFor(model)
	return (float64(u.Input)*p.in + float64(u.Output)*p.out +
		float64(u.CacheCreate)*p.cacheWrite + float64(u.CacheRead)*p.cacheRead) / 1e6
}

// Share returns the weighted usage of one session (its subagents included)
// and of every session, between from and to.
func (a *Aggregator) Share(sessionID string, from, to time.Time) (session, all float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if e.Time.Before(from) || e.Time.After(to) {
			continue
		}
		w := Weight(e.Model, e.Usage)
		all += w
		if e.SessionID == sessionID {
			session += w
		}
	}
	return session, all
}
