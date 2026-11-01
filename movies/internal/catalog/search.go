package catalog

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Search page: a text search, or TMDB's discover filters (genres, year,
// original language, score, where to watch...), for movies, shows or both.

const (
	searchTTL = 30 * time.Minute
	// tmdbMaxPage is the last page TMDB serves (of 20 results: TMDB doesn't
	// let the page size change, PerPage only shortens a page).
	tmdbMaxPage = 500
)

// Search searches TMDB, with caching (and the cached copy, however old, when
// TMDB can't be reached).
func (p *Platform) Search(ctx context.Context, params SearchParams) (*MediaPage, error) {
	cfg := p.cfg()
	params.Page = min(max(params.Page, 1), tmdbMaxPage)
	params.Type = normalizeType(params.Type)
	adult := cfg.UI.ShowAdult && (params.IsAdult == nil || *params.IsAdult)
	raw, _ := json.Marshal(struct {
		SearchParams
		Language, Region string
		Adult            bool
	}{params, cfg.TMDB.Language, cfg.TMDB.Region, adult})
	sum := sha1.Sum(raw)
	key := "search:" + hex.EncodeToString(sum[:])
	var page MediaPage
	if p.db.GetCache(key, &page) {
		return &page, nil
	}
	res, err := p.search(ctx, params, adult)
	if err != nil {
		if p.db.GetStaleCache(key, &page) {
			return &page, nil
		}
		return nil, err
	}
	if params.PerPage > 0 && params.PerPage < len(res.Media) {
		res.Media = res.Media[:params.PerPage]
	}
	p.db.SetCache(key, res, searchTTL)
	return res, nil
}

func (p *Platform) search(ctx context.Context, params SearchParams, adult bool) (*MediaPage, error) {
	if len(params.IDs) > 0 {
		return p.byIDs(ctx, params.IDs)
	}
	if !p.client.Configured() {
		return nil, ErrNoAPIKey
	}
	mp := p.mapper(ctx, true)
	text := strings.TrimSpace(params.Search)
	switch {
	case text != "":
		return p.searchText(ctx, mp, params, text, adult)
	case len(params.Sort) > 0 && params.Sort[0] == "TRENDING_DESC" && !filtered(params):
		kind := "all"
		if params.Type != "" {
			kind = kindPath(params.Type)
		}
		res, err := p.client.page(ctx, "/trending/"+kind+"/week", url.Values{"page": {strconv.Itoa(params.Page)}})
		if err != nil {
			return nil, err
		}
		return p.toPage(mp, res, params.Type), nil
	}
	kinds := []string{TypeMovie, TypeTV}
	if params.Type != "" {
		kinds = []string{params.Type}
	}
	pages := make([]*MediaPage, len(kinds))
	errs := make([]error, len(kinds))
	var wg sync.WaitGroup
	for i, kind := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pages[i], errs[i] = p.discover(ctx, mp, kind, params, adult)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(pages) == 1 {
		return pages[0], nil
	}
	return mergePages(pages, sortOf(params)), nil
}

// filtered reports whether params filter the results (more than a sort).
func filtered(params SearchParams) bool {
	return len(params.Genres) > 0 || params.Year > 0 || params.Status != "" || params.Language != "" ||
		params.MinScore > 0 || len(params.WatchProviders) > 0
}

func sortOf(params SearchParams) string {
	if len(params.Sort) > 0 {
		return params.Sort[0]
	}
	return "POPULARITY_DESC"
}

// searchText searches titles: movies, shows, or both (TMDB's multi search).
func (p *Platform) searchText(ctx context.Context, mp *mapping, params SearchParams, text string, adult bool) (*MediaPage, error) {
	q := url.Values{"query": {text}, "page": {strconv.Itoa(params.Page)}, "include_adult": {strconv.FormatBool(adult)}}
	path := "/search/multi"
	switch params.Type {
	case TypeMovie:
		path = "/search/movie"
		if params.Year > 0 {
			q.Set("year", strconv.Itoa(params.Year))
		}
	case TypeTV:
		path = "/search/tv"
		if params.Year > 0 {
			q.Set("first_air_date_year", strconv.Itoa(params.Year))
		}
	}
	res, err := p.client.page(ctx, path, q)
	if err != nil {
		return nil, err
	}
	page := p.toPage(mp, res, params.Type)
	// The multi search can't filter by year itself.
	if params.Type == "" && params.Year > 0 {
		kept := []*Media{}
		for _, m := range page.Media {
			if m.Year() == params.Year {
				kept = append(kept, m)
			}
		}
		page.Media = kept
	}
	return page, nil
}

// discover runs TMDB's discover for one kind with the filters of params.
func (p *Platform) discover(ctx context.Context, mp *mapping, kind string, params SearchParams, adult bool) (*MediaPage, error) {
	cfg := p.cfg()
	q := url.Values{
		"page":          {strconv.Itoa(params.Page)},
		"include_adult": {strconv.FormatBool(adult)},
		"sort_by":       {discoverSort(kind, sortOf(params))},
	}
	if len(params.Genres) > 0 {
		names, err := p.client.genreNames(ctx, kind)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, g := range params.Genres {
			id, ok := genreID(names, g)
			if !ok {
				// Movies and shows have different genres ("Action" and
				// "Action & Adventure"): none of this kind has it.
				return &MediaPage{PageInfo: PageInfo{CurrentPage: params.Page}, Media: []*Media{}}, nil
			}
			ids = append(ids, strconv.Itoa(id))
		}
		q.Set("with_genres", strings.Join(ids, ","))
	}
	today := time.Now().Format(time.DateOnly)
	if kind == TypeMovie {
		if params.Year > 0 {
			q.Set("primary_release_year", strconv.Itoa(params.Year))
		}
		switch params.Status {
		case "NOT_YET_RELEASED":
			q.Set("primary_release_date.gte", today)
		case "FINISHED", "RELEASING":
			q.Set("primary_release_date.lte", today)
		}
	} else {
		if params.Year > 0 {
			q.Set("first_air_date_year", strconv.Itoa(params.Year))
		}
		// TMDB's show statuses: 0 Returning Series, 1 Planned, 2 In
		// Production, 3 Ended, 4 Canceled, 5 Pilot.
		switch params.Status {
		case "RELEASING":
			q.Set("with_status", "0")
		case "FINISHED":
			q.Set("with_status", "3")
		case "CANCELLED":
			q.Set("with_status", "4")
		case "NOT_YET_RELEASED":
			q.Set("with_status", "1|2|5")
		}
	}
	if params.Language != "" {
		q.Set("with_original_language", strings.ToLower(params.Language))
	}
	switch {
	case params.MinScore > 0:
		q.Set("vote_average.gte", strconv.FormatFloat(float64(params.MinScore)/10, 'f', 1, 64))
		q.Set("vote_count.gte", "50") // a few votes make any score
	case sortOf(params) == "SCORE_DESC":
		q.Set("vote_count.gte", "200")
	}
	if len(params.WatchProviders) > 0 {
		ids := make([]string, len(params.WatchProviders))
		for i, id := range params.WatchProviders {
			ids[i] = strconv.Itoa(id)
		}
		q.Set("with_watch_providers", strings.Join(ids, "|"))
		q.Set("watch_region", cfg.TMDB.Region)
	}
	res, err := p.client.page(ctx, "/discover/"+kindPath(kind), q)
	if err != nil {
		return nil, err
	}
	return p.toPage(mp, res, kind), nil
}

// discoverSort is TMDB's sort_by for a sort of the Search page.
func discoverSort(kind, sort string) string {
	date := "primary_release_date"
	title := "title"
	if kind == TypeTV {
		date, title = "first_air_date", "name"
	}
	switch sort {
	case "SCORE_DESC":
		return "vote_average.desc"
	case "START_DATE_DESC":
		return date + ".desc"
	case "START_DATE":
		return date + ".asc"
	case "TITLE":
		return title + ".asc"
	}
	return "popularity.desc"
}

func genreID(names map[int]string, name string) (int, bool) {
	for id, n := range names {
		if strings.EqualFold(n, strings.TrimSpace(name)) {
			return id, true
		}
	}
	return 0, false
}

// toPage maps a TMDB page.
func (p *Platform) toPage(mp *mapping, res *tmdbPage, kind string) *MediaPage {
	last := min(max(res.TotalPages, 1), tmdbMaxPage)
	page := max(res.Page, 1)
	return &MediaPage{
		PageInfo: PageInfo{Total: res.TotalResults, CurrentPage: page, LastPage: last, HasNextPage: page < last},
		Media:    p.pageMedia(mp, res, kind),
	}
}

// mergePages merges the movie and show pages of a search for both, in the
// order asked for.
func mergePages(pages []*MediaPage, order string) *MediaPage {
	out := &MediaPage{Media: []*Media{}}
	for _, pg := range pages {
		out.Media = append(out.Media, pg.Media...)
		out.PageInfo.Total += pg.PageInfo.Total
		out.PageInfo.CurrentPage = max(out.PageInfo.CurrentPage, pg.PageInfo.CurrentPage)
		out.PageInfo.LastPage = max(out.PageInfo.LastPage, pg.PageInfo.LastPage)
		out.PageInfo.HasNextPage = out.PageInfo.HasNextPage || pg.PageInfo.HasNextPage
	}
	less := func(a, b *Media) bool { return a.Popularity > b.Popularity }
	switch order {
	case "SCORE_DESC":
		less = func(a, b *Media) bool { return score(a) > score(b) }
	case "START_DATE_DESC":
		less = func(a, b *Media) bool { return formatDate(a.StartDate) > formatDate(b.StartDate) }
	case "START_DATE":
		less = func(a, b *Media) bool { return formatDate(a.StartDate) < formatDate(b.StartDate) }
	case "TITLE":
		less = func(a, b *Media) bool {
			return strings.ToLower(a.PreferredTitle()) < strings.ToLower(b.PreferredTitle())
		}
	}
	sort.SliceStable(out.Media, func(i, j int) bool { return less(out.Media[i], out.Media[j]) })
	return out
}

func score(m *Media) int {
	if m.AverageScore == nil {
		return 0
	}
	return *m.AverageScore
}

// byIDs is a page of the titles with these ids, in that order.
func (p *Platform) byIDs(ctx context.Context, ids []int) (*MediaPage, error) {
	found, err := p.MediaBatch(ctx, ids)
	if len(found) == 0 && err != nil {
		return nil, err
	}
	out := &MediaPage{Media: []*Media{}}
	for _, id := range ids {
		if m := found[id]; m != nil {
			out.Media = append(out.Media, m)
		}
	}
	out.PageInfo = PageInfo{Total: len(out.Media), CurrentPage: 1, LastPage: 1}
	return out, nil
}

// Genres lists the genres of movies and shows (in the metadata language),
// for the Search page's filter.
func (p *Platform) Genres(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, kind := range []string{TypeMovie, TypeTV} {
		names, err := p.client.genreNames(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !seen[strings.ToLower(n)] {
				seen[strings.ToLower(n)] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
