package catalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Test helpers: a stand-in for TMDB serving the fixtures of testdata/, and a
// Platform backed by a temporary database.

func TestMain(m *testing.M) {
	retryWait = time.Millisecond
	os.Exit(m.Run())
}

const (
	testKey    = "0123456789abcdef0123456789abcdef"
	testImages = "https://img.test/t/p"
)

// fakeTMDB serves TMDB's API from testdata: /movie/155 answers
// movie_155.json, /tv/1396/season/1 tv_1396_season_1.json. handle, when
// set, may answer a request first (it returns true when it did). Requests
// without the test key are refused like TMDB does.
type fakeTMDB struct {
	t      *testing.T
	srv    *httptest.Server
	handle func(w http.ResponseWriter, r *http.Request) bool

	mu       sync.Mutex
	requests []*http.Request
}

func newFakeTMDB(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) *fakeTMDB {
	t.Helper()
	f := &fakeTMDB{t: t, handle: handle}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTMDB) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Clone(r.Context()))
	f.mu.Unlock()
	if r.URL.Query().Get("api_key") != testKey && r.Header.Get("Authorization") != "Bearer "+testKey {
		reply(w, http.StatusUnauthorized, map[string]any{"success": false, "status_code": 7, "status_message": "Invalid API key: You must be granted a valid key."})
		return
	}
	if f.handle != nil && f.handle(w, r) {
		return
	}
	name := strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "_") + ".json"
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		reply(w, http.StatusNotFound, map[string]any{"success": false, "status_code": 34, "status_message": "The resource you requested could not be found."})
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	_, _ = w.Write(raw)
}

// count is how many requests were made for a path.
func (f *fakeTMDB) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.URL.Path == path {
			n++
		}
	}
	return n
}

// total is how many requests were made.
func (f *fakeTMDB) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// last is the last request made for a path.
func (f *fakeTMDB) last(path string) *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].URL.Path == path {
			return f.requests[i]
		}
	}
	return nil
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// newTestPlatform returns a Platform on a fresh database, with the test key
// configured and f as TMDB. A nil f fails the test on any request.
func newTestPlatform(t *testing.T, f *fakeTMDB) *Platform {
	t.Helper()
	p, _ := newTestPlatformSettings(t, f, func(s *config.Settings) { s.TMDB.APIKey = testKey })
	return p
}

func newTestPlatformSettings(t *testing.T, f *fakeTMDB, edit func(*config.Settings)) (*Platform, *config.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		s := settings.Get()
		edit(&s)
		if _, err := settings.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPlatform(d, events.NewHub(), settings)
	p.client.images = testImages
	if f == nil {
		f = newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
			t.Errorf("unexpected TMDB request: %s", r.URL.Path)
			reply(w, http.StatusInternalServerError, map[string]any{})
			return true
		})
	}
	p.client.base = f.srv.URL
	return p, settings
}

// ---------------------------------------------------------------------------
// Test data

func ptr[T any](v T) *T { return &v }

// testShow is a show with seasons of these episode counts (specials not
// included), in a status.
func testShow(tmdbID int, status string, seasons ...int) *Media {
	m := &Media{ID: ShowID(tmdbID), TMDBID: tmdbID, Type: TypeTV, Format: TypeTV, Status: status,
		Title: Title{English: fmt.Sprintf("Show %d", tmdbID), UserPreferred: fmt.Sprintf("Show %d", tmdbID)}}
	total := 0
	for i, n := range seasons {
		m.Seasons = append(m.Seasons, Season{Number: i + 1, EpisodeCount: n})
		total += n
	}
	m.Episodes = &total
	return m
}

func testMovie(tmdbID int) *Media {
	return &Media{ID: MovieID(tmdbID), TMDBID: tmdbID, Type: TypeMovie, Format: TypeMovie, Status: "FINISHED", Episodes: ptr(1),
		Title: Title{English: fmt.Sprintf("Movie %d", tmdbID), UserPreferred: fmt.Sprintf("Movie %d", tmdbID)}}
}

// addLocal puts a title on the list, its copy fetched just now.
func addLocal(t *testing.T, p *Platform, m *Media, status string, progress int) {
	t.Helper()
	raw, _ := json.Marshal(m)
	now := time.Now().Unix()
	if _, err := p.db.Write(`INSERT INTO local_list(media_id, type, status, progress, media, media_updated, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		m.ID, KindOf(m.ID), status, progress, string(raw), now, now); err != nil {
		t.Fatal(err)
	}
	p.invalidate()
}

func readLocal(t *testing.T, p *Platform, mediaID int) *ListEntry {
	t.Helper()
	e, err := p.entry(mediaID)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		t.Fatalf("media %d isn't in the list", mediaID)
	}
	return e
}

// layout lists the media ids of each status list, in order.
func layout(c *Collection) map[string][]int {
	out := map[string][]int{}
	for _, l := range c.Lists {
		ids := []int{}
		for _, e := range l.Entries {
			ids = append(ids, e.MediaID)
		}
		out[l.Status] = ids
	}
	return out
}
