package providers

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Provider health
//
// Providers come and go: sites close, addons move, scrapers break. KumoBox
// keeps, for each provider, how its last searches went, so the user can see
// which ones work (Settings › Providers), and so a provider that keeps
// failing stops slowing down the automatic picks (the Play button, auto
// play next), which otherwise wait for its timeout every time.

// DemoteAfter is how many failures in a row demote a provider: its streams
// are listed after the others' and the automatic picks leave it out, until
// it answers again (it's still asked in Watch online, and by its Test
// button).
const DemoteAfter = 3

// latencyWindow is how many of the last answers' times are kept.
const latencyWindow = 10

// HealthStat is how a provider's searches went.
type HealthStat struct {
	Successes int `json:"successes"`
	Failures  int `json:"failures"`
	// ConsecutiveFailures counts the failures since the last success.
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	LastError           string `json:"lastError,omitempty"`
	// LastErrorAt and LastSuccessAt are unix seconds (0: never).
	LastErrorAt   int64 `json:"lastErrorAt,omitempty"`
	LastSuccessAt int64 `json:"lastSuccessAt,omitempty"`
	// Latencies are the last answers' times in milliseconds, oldest first.
	Latencies []int64 `json:"latencies,omitempty"`
}

// Demoted reports whether the provider failed DemoteAfter times in a row.
func (s HealthStat) Demoted() bool { return s.ConsecutiveFailures >= DemoteAfter }

// AvgMs is the average time of the last answers, 0 when there were none.
func (s HealthStat) AvgMs() int64 {
	if len(s.Latencies) == 0 {
		return 0
	}
	var sum int64
	for _, ms := range s.Latencies {
		sum += ms
	}
	return sum / int64(len(s.Latencies))
}

// Health records how every provider's searches went. The zero value isn't
// usable: use NewHealth. A nil *Health records nothing and demotes nobody.
type Health struct {
	// OnChange is called after each change, outside the lock (to save the
	// stats).
	OnChange func()

	now   func() time.Time
	mu    sync.Mutex
	stats map[string]*HealthStat
}

// NewHealth returns an empty health record.
func NewHealth() *Health {
	return &Health{now: time.Now, stats: map[string]*HealthStat{}}
}

// Record notes a provider's answer: err nil is a success (even with no
// streams: the provider worked, it just had nothing). Errors marked with
// Skip (the provider had no way to look the title up) and cancellations
// (KumoBox shutting down) say nothing about the provider and aren't
// counted.
func (h *Health) Record(id string, err error, took time.Duration) {
	if h == nil || id == "" || IsSkip(err) || errors.Is(err, context.Canceled) {
		return
	}
	h.mu.Lock()
	st := h.stats[id]
	if st == nil {
		st = &HealthStat{}
		h.stats[id] = st
	}
	now := h.now().Unix()
	if err == nil {
		st.Successes++
		st.ConsecutiveFailures = 0
		st.LastSuccessAt = now
	} else {
		st.Failures++
		st.ConsecutiveFailures++
		st.LastError, st.LastErrorAt = ErrorText(err), now
	}
	st.Latencies = append(st.Latencies, took.Milliseconds())
	if n := len(st.Latencies); n > latencyWindow {
		st.Latencies = append([]int64(nil), st.Latencies[n-latencyWindow:]...)
	}
	fn := h.OnChange
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Get returns the stats of a provider (empty when it was never asked).
func (h *Health) Get(id string) HealthStat {
	if h == nil {
		return HealthStat{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if st := h.stats[id]; st != nil {
		return copyStat(*st)
	}
	return HealthStat{}
}

// Demoted reports whether a provider failed DemoteAfter times in a row.
func (h *Health) Demoted(id string) bool { return h.Get(id).Demoted() }

// All returns a copy of every provider's stats, by provider id.
func (h *Health) All() map[string]HealthStat {
	out := map[string]HealthStat{}
	if h == nil {
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, st := range h.stats {
		out[id] = copyStat(*st)
	}
	return out
}

// Load replaces the stats with saved ones (when KumoBox starts).
func (h *Health) Load(stats map[string]HealthStat) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats = map[string]*HealthStat{}
	for id, st := range stats {
		c := copyStat(st)
		h.stats[id] = &c
	}
}

func copyStat(s HealthStat) HealthStat {
	s.Latencies = append([]int64(nil), s.Latencies...)
	return s
}

// skipError marks an error that says the provider had no way to look the
// title up (no id it understands), not that it failed.
type skipError struct{ error }

func (e skipError) Unwrap() error { return e.error }

// Skip marks err as a provider having nothing to look the title up with
// (an addon that needs an IMDb id TMDB doesn't know...): it's shown like any
// other error, but doesn't count against the provider's health.
func Skip(err error) error {
	if err == nil {
		return nil
	}
	return skipError{err}
}

// IsSkip reports whether err was marked with Skip.
func IsSkip(err error) bool {
	var s skipError
	return errors.As(err, &s)
}
