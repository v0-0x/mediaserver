package catalog

import (
	"context"
	"errors"
	"log"
	"net/url"
	"sync"
	"time"
)

// Discover is the Discover page: what's trending this week, the popular and
// top rated movies and shows, the movies in theaters and coming soon, and
// the shows on the air.
type Discover struct {
	Trending       []*Media `json:"trending"`
	PopularMovies  []*Media `json:"popularMovies"`
	PopularShows   []*Media `json:"popularShows"`
	TopRatedMovies []*Media `json:"topRatedMovies"`
	TopRatedShows  []*Media `json:"topRatedShows"`
	NowPlaying     []*Media `json:"nowPlaying"`
	Upcoming       []*Media `json:"upcoming"`
	OnTheAir       []*Media `json:"onTheAir"`
	// Language and Region are the settings it was fetched with: a copy
	// fetched with others isn't shown.
	Language string `json:"language"`
	Region   string `json:"region"`
	// FetchedAt is when TMDB gave it (unix seconds).
	FetchedAt int64 `json:"fetchedAt"`
}

const (
	// discoverFresh: a copy this young is shown as is.
	discoverFresh = 30 * time.Minute
	// discoverKeep: an older copy is shown right away while a fresh one is
	// fetched, for up to this long (also what's shown when TMDB can't be
	// reached).
	discoverKeep = 7 * 24 * time.Hour

	discoverKey = "discover"
)

// Discover returns the Discover page. Only the first one waits for TMDB:
// after that a copy is always there, and an old one is refreshed in the
// background.
func (p *Platform) Discover(ctx context.Context) (*Discover, error) {
	if d := p.savedDiscover(time.Now()); d != nil {
		if time.Since(time.Unix(d.FetchedAt, 0)) >= discoverFresh {
			p.refreshDiscover()
		}
		return d, nil
	}
	return p.fetchDiscover(ctx)
}

// WarmDiscover fetches the Discover page when there's no fresh copy, so that
// it opens at once.
func (p *Platform) WarmDiscover(ctx context.Context) (*Discover, error) {
	if d := p.savedDiscover(time.Now()); d != nil && time.Since(time.Unix(d.FetchedAt, 0)) < discoverFresh {
		return d, nil
	}
	return p.fetchDiscover(ctx)
}

// savedDiscover is the saved copy for the current language and region,
// unless it's older than discoverKeep.
func (p *Platform) savedDiscover(now time.Time) *Discover {
	var d Discover
	cfg := p.cfg().TMDB
	if !p.db.GetStaleCache(discoverKey, &d) || d.FetchedAt <= 0 || d.Language != cfg.Language || d.Region != cfg.Region {
		return nil
	}
	if now.Sub(time.Unix(d.FetchedAt, 0)) >= discoverKeep {
		return nil
	}
	return &d
}

// refreshDiscover fetches a fresh copy in the background, one at a time.
func (p *Platform) refreshDiscover() {
	if !p.discovering.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.discovering.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := p.fetchDiscover(ctx); err != nil {
			log.Printf("discover: %v", err)
		}
	}()
}

// fetchDiscover asks TMDB for every row at once and saves the page when
// they all came. Callers arriving meanwhile get the same result.
func (p *Platform) fetchDiscover(ctx context.Context) (*Discover, error) {
	p.discoverMu.Lock()
	defer p.discoverMu.Unlock()
	// Fetched by the caller this one waited for.
	if d := p.savedDiscover(time.Now()); d != nil && time.Since(time.Unix(d.FetchedAt, 0)) < discoverFresh {
		return d, nil
	}
	if !p.client.Configured() {
		return nil, ErrNoAPIKey
	}
	now := time.Now()
	cfg := p.cfg().TMDB
	d := &Discover{Language: cfg.Language, Region: cfg.Region}
	region := url.Values{"region": {cfg.Region}}
	rows := []struct {
		out  *[]*Media
		path string
		kind string
		q    url.Values
	}{
		{&d.Trending, "/trending/all/week", "", nil},
		{&d.PopularMovies, "/movie/popular", TypeMovie, region},
		{&d.PopularShows, "/tv/popular", TypeTV, nil},
		{&d.TopRatedMovies, "/movie/top_rated", TypeMovie, nil},
		{&d.TopRatedShows, "/tv/top_rated", TypeTV, nil},
		{&d.NowPlaying, "/movie/now_playing", TypeMovie, region},
		{&d.Upcoming, "/movie/upcoming", TypeMovie, region},
		{&d.OnTheAir, "/tv/on_the_air", TypeTV, nil},
	}
	mp := p.mapper(ctx, true)
	errs := make([]error, len(rows))
	var wg sync.WaitGroup
	for i, row := range rows {
		*row.out = []*Media{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Straight from TMDB: the page is saved whole, never with rows
			// older than the rest.
			res, err := p.client.page(ctx, row.path, cloneValues(row.q))
			if err != nil {
				errs[i] = err
				return
			}
			*row.out = p.pageMedia(mp, res, row.kind)
		}()
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err == nil {
		d.FetchedAt = now.Unix()
		p.db.SetCache(discoverKey, d, discoverKeep)
		return d, nil
	}
	// Not saved incomplete: rather an older complete copy, else what came.
	if old := p.savedDiscover(now); old != nil {
		return old, nil
	}
	if len(d.Trending) == 0 && len(d.PopularMovies) == 0 && len(d.PopularShows) == 0 {
		return nil, errs[firstErr(errs)]
	}
	return d, nil
}

func firstErr(errs []error) int {
	for i, err := range errs {
		if err != nil {
			return i
		}
	}
	return 0
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// pageMedia maps the movies and shows of a page, leaving out people, and
// adult titles unless they're shown. They aren't cached as the titles' lite
// copies: a list lacks what a title page has (a show's seasons, its
// status...), which the list, the library and progress need.
func (p *Platform) pageMedia(mp *mapping, res *tmdbPage, kind string) []*Media {
	showAdult := p.cfg().UI.ShowAdult
	out := []*Media{}
	for i := range res.Results {
		it := &res.Results[i]
		if it.MediaType == "person" || it.Adult && !showAdult {
			continue
		}
		k := kind
		if k == "" && it.MediaType == "" {
			continue // a mixed list must say what each one is
		}
		if m := mp.media(it, k); m != nil {
			out = append(out, m)
		}
	}
	return out
}
