package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Finding the TMDB title behind a title and year (Letterboxd's films, file
// and release names) or an IMDb id. A title found is remembered for a month,
// so that syncing the same diary again costs no search.

const (
	findTTL = 30 * 24 * time.Hour
	// minFindSimilarity is how alike a title found must be to the one
	// looked for.
	minFindSimilarity = 0.85
)

// FindMovie returns the TMDB movie best matching a title and its release
// year (0: unknown). A year one off still matches (release dates differ
// between countries); ErrNotFound when nothing matches well enough.
func (p *Platform) FindMovie(ctx context.Context, title string, year int) (*Media, error) {
	return p.findTitle(ctx, TypeMovie, title, year)
}

// FindShow is FindMovie for shows (year: of the first episode).
func (p *Platform) FindShow(ctx context.Context, title string, year int) (*Media, error) {
	return p.findTitle(ctx, TypeTV, title, year)
}

func (p *Platform) findTitle(ctx context.Context, kind, title string, year int) (*Media, error) {
	title = strings.TrimSpace(title)
	if util.NormalizeTitle(title) == "" {
		return nil, ErrNotFound
	}
	key := fmt.Sprintf("find:%s:%s:%d", kind, util.NormalizeTitle(title), year)
	var id int
	if p.db.GetCache(key, &id) {
		if id == 0 {
			return nil, ErrNotFound
		}
		return p.MediaLite(ctx, id)
	}
	mp := p.mapper(ctx, false)
	search := func(withYear bool) ([]*Media, error) {
		q := url.Values{"query": {title}, "include_adult": {strconv.FormatBool(p.cfg().UI.ShowAdult)}}
		path := "/search/movie"
		yearParam := "primary_release_year"
		if kind == TypeTV {
			path, yearParam = "/search/tv", "first_air_date_year"
		}
		if withYear {
			q.Set(yearParam, strconv.Itoa(year))
		}
		res, err := p.client.page(ctx, path, q)
		if err != nil {
			return nil, err
		}
		var out []*Media
		for i := range res.Results {
			if m := mp.media(&res.Results[i], kind); m != nil {
				out = append(out, m)
			}
		}
		return out, nil
	}
	var best *Media
	var err error
	if year > 0 {
		var found []*Media
		if found, err = search(true); err == nil {
			best = bestMatch(found, title, year)
		}
	}
	if best == nil && err == nil {
		var found []*Media
		if found, err = search(false); err == nil {
			best = bestMatch(found, title, year)
		}
	}
	if err != nil {
		return nil, err
	}
	if best == nil {
		p.db.SetCache(key, 0, 24*time.Hour) // TMDB may add it: ask again tomorrow
		return nil, ErrNotFound
	}
	p.db.SetCache(key, best.ID, findTTL)
	if m, err := p.MediaLite(ctx, best.ID); err == nil {
		return m, nil
	}
	return best, nil
}

// bestMatch picks among search results the one whose title (localized or
// original) is the most like title, of the year when one is given (or one
// off); nil when none is alike enough.
func bestMatch(found []*Media, title string, year int) *Media {
	var best *Media
	bestScore := 0.0
	for i, m := range found {
		if year > 0 {
			if y := m.Year(); y > 0 && (y < year-1 || y > year+1) {
				continue
			}
		}
		s := 0.0
		for _, t := range m.AllTitles() {
			s = max(s, util.Similarity(title, t))
		}
		if s < minFindSimilarity {
			continue
		}
		if year > 0 && m.Year() == year {
			s += 0.05
		}
		// TMDB ranks by relevance and popularity: earlier wins a tie.
		s -= float64(i) * 0.001
		if best == nil || s > bestScore {
			best, bestScore = m, s
		}
	}
	return best
}

// FindByIMDb returns the movie (or show) with an IMDb id ("tt0111161").
func (p *Platform) FindByIMDb(ctx context.Context, imdbID string) (*Media, error) {
	imdbID = strings.TrimSpace(imdbID)
	if !strings.HasPrefix(imdbID, "tt") {
		return nil, errors.New("not an IMDb id: " + imdbID)
	}
	key := "find:imdb:" + imdbID
	var id int
	if p.db.GetCache(key, &id) {
		if id == 0 {
			return nil, ErrNotFound
		}
		return p.MediaLite(ctx, id)
	}
	res, err := p.client.find(ctx, imdbID)
	if err != nil {
		return nil, err
	}
	mp := p.mapper(ctx, false)
	var m *Media
	switch {
	case len(res.MovieResults) > 0:
		m = mp.media(&res.MovieResults[0], TypeMovie)
	case len(res.TVResults) > 0:
		m = mp.media(&res.TVResults[0], TypeTV)
	}
	if m == nil {
		p.db.SetCache(key, 0, 24*time.Hour)
		return nil, ErrNotFound
	}
	p.db.SetCache(key, m.ID, findTTL)
	if full, err := p.MediaLite(ctx, m.ID); err == nil {
		return full, nil
	}
	return m, nil
}
