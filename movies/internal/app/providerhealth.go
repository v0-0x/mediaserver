package app

import (
	"log"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Provider health
//
// How each provider's searches went (providers.Health) is kept in the
// database, so a provider that kept failing yesterday is still known as one
// today. A search asks many providers at once: the stats are saved a few
// seconds after a change rather than on each answer, and when KumoBox
// stops.

// healthKey is where the stats are kept (kv table).
const healthKey = "provider-health"

// healthSaveDelay is how long after a change the stats are saved.
const healthSaveDelay = 5 * time.Second

// healthStore saves the providers' health.
type healthStore struct {
	db     *db.DB
	health *providers.Health

	mu      sync.Mutex
	pending *time.Timer
}

// newHealthStore loads the saved stats into a new providers.Health that
// saves itself when it changes.
func newHealthStore(d *db.DB) *healthStore {
	s := &healthStore{db: d, health: providers.NewHealth()}
	var saved map[string]providers.HealthStat
	if ok, err := d.GetKV(healthKey, &saved); err != nil {
		log.Printf("providers: could not read their health: %v", err)
	} else if ok {
		s.health.Load(saved)
	}
	s.health.OnChange = s.changed
	return s
}

func (s *healthStore) changed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = time.AfterFunc(healthSaveDelay, s.flush)
	}
}

// flush saves the stats now (when they changed since they were saved).
func (s *healthStore) flush() {
	s.mu.Lock()
	if s.pending == nil {
		s.mu.Unlock()
		return
	}
	s.pending.Stop()
	s.pending = nil
	s.mu.Unlock()
	if err := s.db.SetKV(healthKey, s.health.All()); err != nil {
		log.Printf("providers: could not save their health: %v", err)
	}
}

// ProviderHealth is a provider's health for the UI (Settings › Providers).
type ProviderHealth struct {
	ProviderInfo
	providers.HealthStat
	// AvgMs is the average time of its last answers.
	AvgMs int64 `json:"avgMs"`
	// Demoted is set after providers.DemoteAfter failures in a row: its
	// streams come last and automatic picks leave it out.
	Demoted bool `json:"demoted"`
}

// ProviderHealth lists every provider (disabled ones too) with how its
// searches went, in the order they're asked.
func (a *App) ProviderHealth() []ProviderHealth {
	stats := a.Health.All()
	out := []ProviderHealth{}
	for _, inf := range a.ProviderInfos() {
		st := stats[inf.ID]
		out = append(out, ProviderHealth{ProviderInfo: inf, HealthStat: st, AvgMs: st.AvgMs(), Demoted: st.Demoted()})
	}
	return out
}
