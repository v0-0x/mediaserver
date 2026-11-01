package library

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/release"
)

// A stand-in for TMDB, serving a small catalog of movies and shows built
// by the tests (search, details, seasons, find by IMDb or TVDB id), and a Platform
// talking to it.

const fakeKey = "0123456789abcdef0123456789abcdef"

type fakeEpisode struct {
	title, airDate string
}

type fakeSeason struct {
	number   int
	airDate  string
	episodes []fakeEpisode
}

type fakeTitle struct {
	id         int
	title      string
	date       string // release or first air date
	imdb       string
	tvdb       int
	popularity float64
	seasons    []fakeSeason // shows
}

// season adds a season of n episodes (titled "Episode 1"...), the first
// airing on start and one a week after.
func (t *fakeTitle) season(number, n int, start string) *fakeTitle {
	s := fakeSeason{number: number, airDate: start}
	day, _ := time.Parse(time.DateOnly, start)
	for e := 1; e <= n; e++ {
		s.episodes = append(s.episodes, fakeEpisode{title: fmt.Sprintf("Episode %d", e), airDate: day.AddDate(0, 0, 7*(e-1)).Format(time.DateOnly)})
	}
	t.seasons = append(t.seasons, s)
	return t
}

// daily adds a season with an episode every weekday from start.
func (t *fakeTitle) daily(number, n int, start string) *fakeTitle {
	s := fakeSeason{number: number, airDate: start}
	day, _ := time.Parse(time.DateOnly, start)
	for len(s.episodes) < n {
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			s.episodes = append(s.episodes, fakeEpisode{title: "Show of " + day.Format(time.DateOnly), airDate: day.Format(time.DateOnly)})
		}
		day = day.AddDate(0, 0, 1)
	}
	t.seasons = append(t.seasons, s)
	return t
}

// specials adds season 0 with these titles.
func (t *fakeTitle) specials(start string, titles ...string) *fakeTitle {
	s := fakeSeason{number: 0, airDate: start}
	for _, title := range titles {
		s.episodes = append(s.episodes, fakeEpisode{title: title, airDate: start})
	}
	t.seasons = append([]fakeSeason{s}, t.seasons...)
	return t
}

type fakeTMDB struct {
	t      *testing.T
	srv    *httptest.Server
	movies []*fakeTitle
	shows  []*fakeTitle
	// down makes it answer every request with "too many requests".
	down atomic.Bool

	mu       sync.Mutex
	requests []string // paths, with the query
}

func newFakeTMDB(t *testing.T) *fakeTMDB {
	t.Helper()
	f := &fakeTMDB{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTMDB) movie(id int, title, date string) *fakeTitle {
	m := &fakeTitle{id: id, title: title, date: date, popularity: float64(100 - len(f.movies))}
	f.movies = append(f.movies, m)
	return m
}

func (f *fakeTMDB) show(id int, title, date string) *fakeTitle {
	s := &fakeTitle{id: id, title: title, date: date, popularity: float64(100 - len(f.shows))}
	f.shows = append(f.shows, s)
	return s
}

// count is how many requests had a path starting with prefix.
func (f *fakeTMDB) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeTMDB) reset() {
	f.mu.Lock()
	f.requests = nil
	f.mu.Unlock()
}

func fakeReply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeTMDB) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+"?"+q.Get("query")+q.Get("primary_release_year")+q.Get("first_air_date_year"))
	f.mu.Unlock()
	if f.down.Load() {
		w.Header().Set("Retry-After", "0")
		fakeReply(w, http.StatusTooManyRequests, map[string]any{"status_code": 25})
		return
	}
	if q.Get("api_key") != fakeKey {
		fakeReply(w, http.StatusUnauthorized, map[string]any{"status_code": 7})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	notFound := func() { fakeReply(w, http.StatusNotFound, map[string]any{"status_code": 34}) }
	switch {
	case len(parts) == 3 && parts[0] == "genre":
		fakeReply(w, 200, map[string]any{"genres": []any{}})
	case len(parts) == 2 && parts[0] == "search":
		var found []map[string]any
		switch parts[1] {
		case "movie":
			found = f.search(f.movies, q.Get("query"), q.Get("primary_release_year"), "movie", false)
		case "tv":
			found = f.search(f.shows, q.Get("query"), q.Get("first_air_date_year"), "tv", false)
		case "multi":
			found = append(f.search(f.movies, q.Get("query"), "", "movie", true), f.search(f.shows, q.Get("query"), "", "tv", true)...)
		}
		fakeReply(w, 200, map[string]any{"page": 1, "total_pages": 1, "total_results": len(found), "results": found})
	case len(parts) == 2 && parts[0] == "movie":
		if m := find(f.movies, parts[1]); m != nil {
			fakeReply(w, 200, m.json("movie", true))
			return
		}
		notFound()
	case len(parts) == 2 && parts[0] == "tv":
		if s := find(f.shows, parts[1]); s != nil {
			fakeReply(w, 200, s.json("tv", true))
			return
		}
		notFound()
	case len(parts) == 4 && parts[0] == "tv" && parts[2] == "season":
		s := find(f.shows, parts[1])
		n, _ := strconv.Atoi(parts[3])
		if s == nil {
			notFound()
			return
		}
		for _, se := range s.seasons {
			if se.number != n {
				continue
			}
			var eps []map[string]any
			for i, e := range se.episodes {
				eps = append(eps, map[string]any{"id": s.id*10000 + n*100 + i, "name": e.title, "air_date": e.airDate, "season_number": n, "episode_number": i + 1, "runtime": 42})
			}
			fakeReply(w, 200, map[string]any{"id": n, "season_number": n, "name": fmt.Sprintf("Season %d", n), "air_date": se.airDate, "episodes": eps})
			return
		}
		notFound()
	case len(parts) == 2 && parts[0] == "find":
		out := map[string]any{"movie_results": []any{}, "tv_results": []any{}}
		ids := func(t *fakeTitle) bool {
			if q.Get("external_source") == "tvdb_id" {
				return t.tvdb > 0 && strconv.Itoa(t.tvdb) == parts[1]
			}
			return t.imdb == parts[1]
		}
		for _, m := range f.movies {
			if ids(m) {
				out["movie_results"] = []any{m.json("movie", false)}
			}
		}
		for _, s := range f.shows {
			if ids(s) {
				out["tv_results"] = []any{s.json("tv", false)}
			}
		}
		fakeReply(w, 200, out)
	default:
		notFound()
	}
}

func find(list []*fakeTitle, id string) *fakeTitle {
	n, _ := strconv.Atoi(id)
	for _, t := range list {
		if t.id == n {
			return t
		}
	}
	return nil
}

// search finds the titles holding every word of the query (as TMDB does,
// more or less), of the year when one is given, most popular first.
func (f *fakeTMDB) search(list []*fakeTitle, query, year, kind string, typed bool) []map[string]any {
	words := strings.Fields(release.Normalize(query))
	var found []*fakeTitle
	for _, t := range list {
		title := " " + release.Normalize(t.title) + " "
		ok := len(words) > 0
		for _, w := range words {
			ok = ok && strings.Contains(title, " "+w+" ")
		}
		if ok && (year == "" || strings.HasPrefix(t.date, year)) {
			found = append(found, t)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].popularity > found[j].popularity })
	out := []map[string]any{}
	for _, t := range found {
		j := t.json(kind, false)
		if typed {
			j["media_type"] = kind
		}
		out = append(out, j)
	}
	return out
}

func (t *fakeTitle) json(kind string, details bool) map[string]any {
	out := map[string]any{"id": t.id, "popularity": t.popularity, "overview": "About " + t.title, "poster_path": fmt.Sprintf("/p%d.jpg", t.id)}
	if kind == "movie" {
		out["title"], out["original_title"], out["release_date"] = t.title, t.title, t.date
		if details {
			out["status"], out["runtime"], out["imdb_id"] = "Released", 120, t.imdb
		}
		return out
	}
	out["name"], out["original_name"], out["first_air_date"] = t.title, t.title, t.date
	if details {
		out["status"] = "Ended"
		var seasons []map[string]any
		total := 0
		var last map[string]any
		for _, s := range t.seasons {
			seasons = append(seasons, map[string]any{"season_number": s.number, "episode_count": len(s.episodes), "air_date": s.airDate, "name": fmt.Sprintf("Season %d", s.number)})
			if s.number > 0 && len(s.episodes) > 0 {
				total += len(s.episodes)
				last = map[string]any{"season_number": s.number, "episode_number": len(s.episodes), "air_date": s.episodes[len(s.episodes)-1].airDate}
			}
		}
		out["seasons"], out["number_of_episodes"], out["last_episode_to_air"] = seasons, total, last
		if t.imdb != "" {
			out["external_ids"] = map[string]any{"imdb_id": t.imdb}
		}
	}
	return out
}

// newTestPlatform is a Platform on a fresh database, with TMDB being f.
func newTestPlatform(t *testing.T, f *fakeTMDB) (*catalog.Platform, *db.DB, *config.Store) {
	t.Helper()
	t.Setenv("KUMOBOX_TMDB_URL", f.srv.URL)
	t.Setenv("KUMOBOX_TMDB_IMAGE_URL", "https://img.test/t/p")
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Get()
	cfg.TMDB.APIKey = fakeKey
	cfg.Library.AutoRefresh = false
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return catalog.NewPlatform(d, events.NewHub(), settings), d, settings
}

// newMatchingScanner is a scanner of the library folders dirs, matching
// with TMDB being f.
func newMatchingScanner(t *testing.T, f *fakeTMDB, dirs ...string) (*Scanner, *catalog.Platform) {
	t.Helper()
	p, d, settings := newTestPlatform(t, f)
	cfg := settings.Get()
	cfg.Library.Dir, cfg.Library.ExtraDirs = dirs[0], dirs[1:]
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewScanner(NewStore(d), p, settings, events.NewHub()), p
}

// tree makes empty video files at these paths below root.
func tree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		touch(t, filepath.Join(root, filepath.FromSlash(p)))
	}
}

// matches lists each indexed file (by path below its root, with slashes)
// as "media id/key/kind".
func matches(t *testing.T, s *Scanner, roots ...string) map[string]string {
	t.Helper()
	all, err := s.Store.All()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range all {
		rel := f.Path
		for _, r := range roots {
			if x, err := filepath.Rel(r, f.Path); err == nil && !strings.HasPrefix(x, "..") {
				rel = filepath.ToSlash(x)
			}
		}
		v := fmt.Sprintf("%d/%d/%s", f.MediaID, f.Episode, f.Kind)
		if f.EpisodeEnd > 0 {
			v += fmt.Sprintf("-%d", f.EpisodeEnd)
		}
		out[rel] = v
	}
	return out
}

func touchSized(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}
