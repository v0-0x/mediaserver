package catalog

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// The TV calendar: the episodes of the shows on the user's list airing in a
// time range, from TMDB's air dates.

const scheduleTTL = time.Hour

// Schedule returns the episodes airing in [start, end) (unix seconds) of
// the shows on the list that the user hasn't finished or dropped, by air
// time. TMDB only has air dates: an episode airs at 00:00 UTC that day.
func (p *Platform) Schedule(ctx context.Context, start, end int64) ([]*AiringEpisode, error) {
	coll, err := p.Collection(ctx, TypeTV, false)
	if err != nil {
		return nil, err
	}
	type job struct {
		media  *Media
		season int
	}
	var jobs []job
	for _, e := range coll.Entries() {
		if e.Status == "COMPLETED" || e.Status == "DROPPED" {
			continue
		}
		for _, s := range airingSeasons(e.Media, start, end) {
			jobs = append(jobs, job{e.Media, s})
		}
	}
	// The copy kept is of the same seasons: a show put on the list (or
	// finished, dropped) since shows up (or goes) at once, not an hour later.
	h := sha1.New()
	for _, j := range jobs {
		fmt.Fprintf(h, "%d/%d,", j.media.ID, j.season)
	}
	key := fmt.Sprintf("schedule:%d:%d:%x", start/3600, end/3600, h.Sum(nil)[:8])
	var out []*AiringEpisode
	if p.db.GetCache(key, &out) {
		return out, nil
	}

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
		next = make(chan job)
	)
	now := time.Now().Unix()
	out = []*AiringEpisode{}
	for range min(batchWorkers, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range next {
				s, err := p.Season(ctx, j.media.ID, j.season)
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
					mu.Unlock()
					continue
				}
				for _, ep := range s.Episodes {
					day, err := time.Parse(time.DateOnly, ep.AirDate)
					if err != nil {
						continue
					}
					at := day.Unix()
					if at < start || at >= end {
						continue
					}
					out = append(out, &AiringEpisode{
						AiringAt: int(at), TimeUntilAiring: int(at - now), Episode: ep.Key, Title: ep.Title, Media: j.media,
					})
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		next <- j
	}
	close(next)
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AiringAt != out[j].AiringAt {
			return out[i].AiringAt < out[j].AiringAt
		}
		if a, b := out[i].Media.PreferredTitle(), out[j].Media.PreferredTitle(); a != b {
			return a < b
		}
		return out[i].Episode < out[j].Episode
	})
	if err := errors.Join(errs...); err != nil {
		// A season failed: prefer an older complete copy, else show what
		// came, but never cache an incomplete schedule.
		var old []*AiringEpisode
		if p.db.GetStaleCache(key, &old) {
			return old, nil
		}
		if len(out) == 0 {
			return nil, err
		}
		return out, nil
	}
	p.db.SetCache(key, out, scheduleTTL)
	return out, nil
}

// airingSeasons are the seasons of a show that may have episodes airing in
// [start, end): the season of its latest episode and of its next one, and
// its last season (whose dates may be announced since the show was saved).
// None for a show that ended before start.
func airingSeasons(m *Media, start, end int64) []int {
	if m == nil || !m.IsShow() {
		return nil
	}
	if m.Status == "FINISHED" || m.Status == "CANCELLED" {
		if y := m.EndDate.Year; y != nil {
			ended := time.Date(*y, time.Month(deref(m.EndDate.Month, 12)), deref(m.EndDate.Day, 31), 0, 0, 0, 0, time.UTC)
			if ended.Unix() < start {
				return nil
			}
		}
	}
	seen := map[int]bool{}
	var out []int
	add := func(s int) {
		if s > 0 && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if m.LatestEpisode > 0 {
		s, _ := SplitKey(m.LatestEpisode)
		add(s)
	}
	if n := m.NextAiringEpisode; n != nil && n.Episode > 0 {
		s, _ := SplitKey(n.Episode)
		add(s)
	}
	last := 0
	for _, s := range m.Seasons {
		if s.Number <= last {
			continue
		}
		// A season starting after the range has nothing in it.
		if day, err := time.Parse(time.DateOnly, s.AirDate); err == nil && day.Unix() >= end {
			continue
		}
		last = s.Number
	}
	add(last)
	return out
}

func deref(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
