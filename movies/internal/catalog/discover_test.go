package catalog

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// listPage is a TMDB page of one title, named after the path that gave it.
func listPage(path, kind string) map[string]any {
	item := map[string]any{"id": len(path), "media_type": kind, "popularity": 10, "release_date": "2001-01-01", "first_air_date": "2001-01-01"}
	if kind == "tv" {
		item["name"] = path
	} else {
		item["title"] = path
	}
	return map[string]any{"page": 1, "total_pages": 3, "total_results": 60, "results": []any{item,
		map[string]any{"id": 9, "media_type": "person", "name": "Someone"}}}
}

func TestDiscover(t *testing.T) {
	var failing atomic.Bool
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		path := r.URL.Path
		if strings.HasPrefix(path, "/genre/") {
			return false
		}
		if failing.Load() && path == "/tv/top_rated" {
			reply(w, http.StatusInternalServerError, map[string]any{})
			return true
		}
		kind := "movie"
		if strings.HasPrefix(path, "/tv/") || path == "/trending/all/week" {
			kind = "tv"
		}
		reply(w, http.StatusOK, listPage(path, kind))
		return true
	})
	p, settings := newTestPlatformSettings(t, f, func(s *config.Settings) { s.TMDB.APIKey, s.TMDB.Region = testKey, "GB" })
	ctx := context.Background()

	d, err := p.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]*Media{
		"/trending/all/week": d.Trending, "/movie/popular": d.PopularMovies, "/tv/popular": d.PopularShows,
		"/movie/top_rated": d.TopRatedMovies, "/tv/top_rated": d.TopRatedShows, "/movie/now_playing": d.NowPlaying,
		"/movie/upcoming": d.Upcoming, "/tv/on_the_air": d.OnTheAir,
	}
	for path, row := range rows {
		if len(row) != 1 || row[0].PreferredTitle() != path {
			t.Errorf("%s: %+v", path, row)
		}
	}
	if d.OnTheAir[0].Type != TypeTV || d.Upcoming[0].Type != TypeMovie || d.Region != "GB" || d.FetchedAt == 0 {
		t.Errorf("discover: %+v", d)
	}
	if r := f.last("/movie/now_playing"); r.URL.Query().Get("region") != "GB" {
		t.Errorf("now playing asked for region %q", r.URL.Query().Get("region"))
	}

	// Saved: shown again without asking TMDB.
	before := f.total()
	if _, err := p.Discover(ctx); err != nil || f.total() != before {
		t.Errorf("not saved: %v, %d requests", err, f.total()-before)
	}
	// Another region: fetched again; a row failing, the older page is kept.
	s := settings.Get()
	s.TMDB.Region = "FR"
	if _, err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	failing.Store(true)
	if d, err := p.Discover(ctx); err != nil || d.Region != "FR" || len(d.TopRatedShows) != 0 || len(d.Trending) != 1 {
		t.Errorf("FR with a row missing: %+v %v", d, err)
	}
	// What came isn't saved incomplete: asked again.
	failing.Store(false)
	before = f.count("/trending/all/week")
	if d, err := p.WarmDiscover(ctx); err != nil || d.Region != "FR" || len(d.TopRatedShows) != 1 || f.count("/trending/all/week") != before+1 {
		t.Fatalf("FR: %+v %v", d, err)
	}
}

func TestSearch(t *testing.T) {
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/discover/") || strings.HasPrefix(r.URL.Path, "/trending/") {
			reply(w, http.StatusOK, listPage(r.URL.Path, strings.Split(r.URL.Path, "/")[2]))
			return true
		}
		return false
	})
	p := newTestPlatform(t, f)
	ctx := context.Background()

	// A text search for both: people left out.
	res, err := p.Search(ctx, SearchParams{Search: "breaking bad"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Media) != 2 || res.Media[0].ID != ShowID(1396) || res.Media[1].ID != MovieID(559969) || res.PageInfo.Total != 3 {
		t.Fatalf("multi search: %+v", res)
	}
	if q := f.last("/search/multi").URL.Query(); q.Get("query") != "breaking bad" || q.Get("include_adult") != "false" || q.Get("page") != "1" {
		t.Errorf("query %v", q)
	}
	// Cached.
	if _, err := p.Search(ctx, SearchParams{Search: "breaking bad"}); err != nil || f.count("/search/multi") != 1 {
		t.Errorf("not cached: %v, %d", err, f.count("/search/multi"))
	}
	if res, err := p.Search(ctx, SearchParams{Search: "breaking bad", Year: 2019}); err != nil || len(res.Media) != 1 || res.Media[0].ID != 559969 {
		t.Errorf("multi search for 2019: %+v %v", res, err)
	}
	if _, err := p.Search(ctx, SearchParams{Search: "inception", Type: "MOVIE", Year: 2010}); err != nil {
		t.Fatal(err)
	}
	if q := f.last("/search/movie").URL.Query(); q.Get("year") != "2010" {
		t.Errorf("movie search: %v", q)
	}

	// Filters: discover, for both kinds. TV has no "Action" genre: no TV
	// request then.
	res, err = p.Search(ctx, SearchParams{Genres: []string{"action", "Crime"}, Year: 2008, Language: "EN", MinScore: 75, Sort: []string{"SCORE_DESC"}, Page: 2, WatchProviders: []int{8, 337}})
	if err != nil {
		t.Fatal(err)
	}
	q := f.last("/discover/movie").URL.Query()
	want := map[string]string{"with_genres": "28,80", "primary_release_year": "2008", "with_original_language": "en", "vote_average.gte": "7.5",
		"vote_count.gte": "50", "sort_by": "vote_average.desc", "page": "2", "with_watch_providers": "8|337", "watch_region": "US"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("discover %s = %q, want %q", k, q.Get(k), v)
		}
	}
	if f.count("/discover/tv") != 0 || len(res.Media) != 1 || res.Media[0].Type != TypeMovie {
		t.Errorf("TV asked for a movie genre: %d, %+v", f.count("/discover/tv"), res.Media)
	}

	// Both kinds merged, in order.
	res, err = p.Search(ctx, SearchParams{Genres: []string{"Drama"}, Status: "RELEASING", Sort: []string{"START_DATE_DESC"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Media) != 2 || res.PageInfo.Total != 120 || res.PageInfo.LastPage != 3 || !res.PageInfo.HasNextPage {
		t.Errorf("merged: %+v", res.PageInfo)
	}
	if q := f.last("/discover/tv").URL.Query(); q.Get("with_status") != "0" || q.Get("sort_by") != "first_air_date.desc" || q.Get("with_genres") != "18" {
		t.Errorf("tv discover: %v", q)
	}

	// Trending, without filters.
	if res, err := p.Search(ctx, SearchParams{Type: "TV", Sort: []string{"TRENDING_DESC"}}); err != nil || f.count("/trending/tv/week") != 1 || len(res.Media) != 1 {
		t.Errorf("trending: %v %v", res, err)
	}

	// Ids: those titles, in that order.
	res, err = p.Search(ctx, SearchParams{IDs: []int{ShowID(1396), 155}})
	if err != nil || len(res.Media) != 2 || res.Media[0].ID != ShowID(1396) || res.Media[1].ID != 155 {
		t.Fatalf("ids: %+v %v", res, err)
	}
}

func TestDiscoverSort(t *testing.T) {
	for _, c := range []struct{ kind, sort, want string }{
		{TypeMovie, "", "popularity.desc"},
		{TypeMovie, "POPULARITY_DESC", "popularity.desc"},
		{TypeTV, "SCORE_DESC", "vote_average.desc"},
		{TypeMovie, "START_DATE", "primary_release_date.asc"},
		{TypeTV, "START_DATE_DESC", "first_air_date.desc"},
		{TypeMovie, "TITLE", "title.asc"},
		{TypeTV, "TITLE", "name.asc"},
	} {
		if got := discoverSort(c.kind, c.sort); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.kind, c.sort, got, c.want)
		}
	}
}

func TestGenres(t *testing.T) {
	p := newTestPlatform(t, newFakeTMDB(t, nil))
	g, err := p.Genres(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 19 movie genres and 16 TV ones, 8 of them in both.
	if len(g) != 27 || g[0] != "Action" || g[1] != "Action & Adventure" || g[len(g)-1] != "Western" {
		t.Fatalf("genres (%d): %v", len(g), g)
	}
}

func TestFindMovie(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	ctx := context.Background()
	m, err := p.FindMovie(ctx, "Inception", 2010)
	if err != nil || m.ID != 27205 || *m.Duration != 148 {
		t.Fatalf("Inception (2010): %+v %v", m, err)
	}
	if q := f.last("/search/movie").URL.Query(); q.Get("query") != "Inception" || q.Get("primary_release_year") != "2010" {
		t.Errorf("search %v", q)
	}
	// Remembered.
	searches := f.count("/search/movie")
	if m, err := p.FindMovie(ctx, "inception", 2010); err != nil || m.ID != 27205 || f.count("/search/movie") != searches {
		t.Errorf("again: %v %v, %d searches", m, err, f.count("/search/movie")-searches)
	}
	// A year off: still found; two off: another film.
	if m, err := p.FindMovie(ctx, "Inception", 2011); err != nil || m.ID != 27205 {
		t.Errorf("a year off: %v %v", m, err)
	}
	if _, err := p.FindMovie(ctx, "Inception", 2013); err != ErrNotFound {
		t.Errorf("two years off: %v", err)
	}
	if _, err := p.FindMovie(ctx, "Some Other Film", 2010); err != ErrNotFound {
		t.Errorf("not alike: %v", err)
	}
	if m, err := p.FindMovie(ctx, "Inception: The Cobol Job", 0); err != nil || m.ID != 64956 {
		t.Errorf("no year: %v %v", m, err)
	}
}

func TestBestMatch(t *testing.T) {
	mk := func(id int, title string, year int) *Media {
		m := testMovie(id)
		m.Title.English, m.Title.UserPreferred = title, title
		m.SeasonYear = &year
		return m
	}
	found := []*Media{mk(1, "The Thing", 1982), mk(2, "The Thing", 2011), mk(3, "The Thing from Another World", 1951)}
	for _, c := range []struct {
		title string
		year  int
		want  int
	}{
		{"The Thing", 2011, 2},
		{"The Thing", 1982, 1},
		{"The Thing", 1983, 1},
		{"The Thing", 0, 1},
		{"The Thing From Another World", 1951, 3},
		{"The Thing", 1995, 0},
	} {
		got := 0
		if m := bestMatch(found, c.title, c.year); m != nil {
			got = m.TMDBID
		}
		if got != c.want {
			t.Errorf("%s (%d): %d, want %d", c.title, c.year, got, c.want)
		}
	}
}

func TestFindByIMDb(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	ctx := context.Background()
	m, err := p.FindByIMDb(ctx, "tt0903747")
	if err != nil || m.ID != ShowID(1396) || m.LastEpisode() != 5016 {
		t.Fatalf("got %+v %v", m, err)
	}
	if q := f.last("/find/tt0903747").URL.Query(); q.Get("external_source") != "imdb_id" {
		t.Errorf("query %v", q)
	}
	if _, err := p.FindByIMDb(ctx, "tt0903747"); err != nil || f.count("/find/tt0903747") != 1 {
		t.Errorf("not remembered: %v", err)
	}
	if _, err := p.FindByIMDb(ctx, "0903747"); err == nil {
		t.Error("not an IMDb id")
	}
	if _, err := p.FindByIMDb(ctx, "tt404"); err == nil {
		t.Error(fmt.Sprint("found a missing id: ", err))
	}
}

func TestMergePages(t *testing.T) {
	a, b, c := testMovie(1), testShow(2, "", 1), testMovie(3)
	a.Popularity, b.Popularity, c.Popularity = 5, 50, 20
	a.Title.UserPreferred, b.Title.UserPreferred, c.Title.UserPreferred = "b", "c", "a"
	pages := []*MediaPage{{Media: []*Media{a, c}}, {Media: []*Media{b}}}
	ids := func(p *MediaPage) []int {
		var out []int
		for _, m := range p.Media {
			out = append(out, m.TMDBID)
		}
		return out
	}
	if got := ids(mergePages(pages, "POPULARITY_DESC")); !reflect.DeepEqual(got, []int{2, 3, 1}) {
		t.Errorf("by popularity: %v", got)
	}
	if got := ids(mergePages(pages, "TITLE")); !reflect.DeepEqual(got, []int{3, 1, 2}) {
		t.Errorf("by title: %v", got)
	}
}
