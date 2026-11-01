package share

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/history"
	"github.com/v0-0x/mediaserver/anime/internal/parental"
)

// Parental controls (internal/parental) between Anikos: a host (a computer
// or an Aniko Hub) sets a device's rules; they go to the device with the
// host's hello, and the device's Aniko keeps them, also when the host is
// away, until the host lifts them (or the PIN set with them does). The
// device tells the host what it watches.

const parentalKey = "parental:received"

// ---------------------------------------------------------------- the host

// SetRules sets the parental controls of a device this Aniko shares with
// (none: no rules). pin "" keeps the PIN there was; "-" removes it.
func (s *Service) SetRules(id string, r parental.Rules, pin string) error {
	r = r.Clean()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok {
		return errors.New("no such device")
	}
	if !p.allowed {
		return errors.New("share the library with it first: its parental controls go with it")
	}
	switch {
	case pin == "-":
		r.PIN = ""
	case pin != "":
		r.PIN = parental.HashPIN(pin)
	case p.rules != nil:
		r.PIN = p.rules.PIN
	}
	r.SetAt = time.Now().UnixMilli()
	if !r.Active() {
		p.rules = nil
	} else {
		p.rules = &r
	}
	s.save()
	go s.hub.Publish("sharing-updated", nil)
	return nil
}

// RulesOf are the parental controls set for a device (nil: none).
func (s *Service) RulesOf(id string) *parental.Rules {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.peers[id]; ok && p.rules != nil {
		r := *p.rules
		return &r
	}
	return nil
}

// Watched is an episode a device with parental controls watched, as it
// told this Aniko.
type Watched struct {
	MediaID  int     `json:"mediaId"`
	Episode  int     `json:"episode"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Source   string  `json:"source"`
	At       int64   `json:"at"` // unix seconds, its last position
}

const (
	watchedSchema = `CREATE TABLE IF NOT EXISTS parental_watched (
		device TEXT NOT NULL,
		media_id INTEGER NOT NULL,
		episode INTEGER NOT NULL,
		position REAL NOT NULL DEFAULT 0,
		duration REAL NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		at INTEGER NOT NULL,
		PRIMARY KEY (device, media_id, episode)
	)`
	// Kept per device: the latest.
	keepWatched = 3000
)

func (s *Service) watchedTable() error {
	s.watchedOnce.Do(func() {
		_, s.watchedErr = s.db.Write(watchedSchema)
	})
	return s.watchedErr
}

// TakeWatched keeps what a device with parental controls says it watched.
func (s *Service) TakeWatched(c Caller, entries []Watched) error {
	if c.Rules == nil {
		return errors.New("no parental controls are set for it here")
	}
	if err := s.watchedTable(); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, e := range entries {
		if e.MediaID <= 0 || e.Episode < 0 || e.At <= 0 || e.At > now+86400 {
			continue
		}
		if len(e.Source) > 16 {
			e.Source = e.Source[:16]
		}
		_, err := s.db.Write(`INSERT INTO parental_watched(device, media_id, episode, position, duration, source, at) VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(device, media_id, episode) DO UPDATE SET position = excluded.position, duration = excluded.duration, source = excluded.source, at = excluded.at
			WHERE excluded.at >= parental_watched.at`, c.ID, e.MediaID, e.Episode, e.Position, e.Duration, e.Source, e.At)
		if err != nil {
			return err
		}
	}
	_, _ = s.db.Write(`DELETE FROM parental_watched WHERE device = ? AND rowid NOT IN (SELECT rowid FROM parental_watched WHERE device = ? ORDER BY at DESC LIMIT ?)`, c.ID, c.ID, keepWatched)
	return nil
}

// WatchedBy is what a device with parental controls watched, the latest
// first (limit at most).
func (s *Service) WatchedBy(id string, limit int) []Watched {
	out := []Watched{}
	if s.watchedTable() != nil {
		return out
	}
	rows, err := s.db.Query(`SELECT media_id, episode, position, duration, source, at FROM parental_watched WHERE device = ? ORDER BY at DESC LIMIT ?`, id, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var w Watched
		if rows.Scan(&w.MediaID, &w.Episode, &w.Position, &w.Duration, &w.Source, &w.At) == nil {
			out = append(out, w)
		}
	}
	return out
}

// -------------------------------------------------------------- the device

// received are the rules hosts set for this Aniko, by host ID, with the
// host's name.
type receivedRules struct {
	Name  string         `json:"name"`
	Rules parental.Rules `json:"rules"`
}

func (s *Service) loadParental() {
	var saved map[string]receivedRules
	if ok, _ := s.db.GetKV(parentalKey, &saved); ok {
		s.received = saved
	}
}

// takeParental keeps (or lifts) the rules a host sends with its hello.
// Must not be called with s.mu held.
func (s *Service) takeParental(p *peer, r *parental.Rules) {
	s.mu.Lock()
	cur, had := s.received[p.id]
	var changed bool
	switch {
	case r.Active():
		if !had || cur.Rules.SetAt != r.SetAt || cur.Name != p.name {
			if s.received == nil {
				s.received = map[string]receivedRules{}
			}
			s.received[p.id] = receivedRules{Name: p.name, Rules: r.Clean()}
			changed = true
		}
	case had:
		delete(s.received, p.id)
		changed = true
	}
	var save map[string]receivedRules
	if changed {
		save = make(map[string]receivedRules, len(s.received))
		for k, v := range s.received {
			save[k] = v
		}
	}
	s.mu.Unlock()
	if !changed {
		return
	}
	if err := s.db.SetKV(parentalKey, save); err != nil {
		log.Printf("sharing: parental controls: %v", err)
	}
	if r.Active() {
		log.Printf("sharing: parental controls from %s", s.nameOf(p))
	} else {
		log.Printf("sharing: %s lifted its parental controls", s.nameOf(p))
	}
	s.hub.Publish("parental-updated", nil)
}

// Parental are the parental controls on this Aniko (nil: none): every
// host's, the strictest of them.
func (s *Service) Parental() *parental.Rules {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.received) == 0 {
		return nil
	}
	all := make([]parental.Rules, 0, len(s.received))
	for _, r := range s.received {
		all = append(all, r.Rules)
	}
	return parental.Merge(all)
}

// ParentalFrom names the hosts whose parental controls are on this Aniko,
// and whether each can be lifted here with a PIN.
type ParentalSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	HasPIN bool   `json:"hasPin"`
}

func (s *Service) ParentalFrom() []ParentalSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ParentalSource{}
	for id, r := range s.received {
		out = append(out, ParentalSource{ID: id, Name: r.Name, HasPIN: r.Rules.PIN != ""})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UnlockParental lifts, on this Aniko, the parental controls of the hosts
// whose PIN this is (they're back when the host sends them again). It
// returns how many were lifted.
func (s *Service) UnlockParental(pin string) int {
	s.mu.Lock()
	n := 0
	for id, r := range s.received {
		if r.Rules.PIN != "" && parental.CheckPIN(r.Rules.PIN, pin) {
			delete(s.received, id)
			n++
		}
	}
	save := make(map[string]receivedRules, len(s.received))
	for k, v := range s.received {
		save[k] = v
	}
	s.mu.Unlock()
	if n > 0 {
		_ = s.db.SetKV(parentalKey, save)
		s.hub.Publish("parental-updated", nil)
	}
	return n
}

// parentalWatched notes a position for the hosts that set parental
// controls here: sent within a few seconds.
func (s *Service) parentalWatched(e history.Entry) {
	s.mu.Lock()
	n := len(s.received)
	s.mu.Unlock()
	if n == 0 {
		return
	}
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watchPending == nil {
		s.watchPending = map[[2]int]Watched{}
	}
	s.watchPending[[2]int{e.MediaID, e.Episode}] = Watched{MediaID: e.MediaID, Episode: e.Episode, Position: e.Position, Duration: e.Duration, Source: e.Source, At: e.UpdatedAt}
	if s.watchTimer == nil {
		s.watchTimer = time.AfterFunc(5*time.Second, s.sendWatched)
	}
}

func (s *Service) sendWatched() {
	s.watchMu.Lock()
	entries := make([]Watched, 0, len(s.watchPending))
	for _, w := range s.watchPending {
		entries = append(entries, w)
	}
	s.watchPending, s.watchTimer = nil, nil
	s.watchMu.Unlock()
	s.mu.Lock()
	var hosts []*peer
	for id := range s.received {
		if p, ok := s.peers[id]; ok && p.shares {
			hosts = append(hosts, p)
		}
	}
	s.mu.Unlock()
	for _, p := range hosts {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := s.postJSON(ctx, p, "/api/peer/parental/watched", entries, nil); err != nil {
			log.Printf("sharing: what was watched, to %s: %v", s.nameOf(p), plainError(err))
		}
		cancel()
	}
}
