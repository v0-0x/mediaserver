package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
)

const testTMDBKey = "0123456789abcdef0123456789abcdef"

// newCatalogServer is a test server whose TMDB is a stand-in serving the
// catalog package's fixtures (/movie/155 is movie_155.json), with a key set.
func newCatalogServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newCountingCatalogServer(t)
	return s
}

// newCountingCatalogServer is newCatalogServer, and how many times its TMDB
// was asked for a path (e.g. "/movie/155").
func newCountingCatalogServer(t *testing.T) (*Server, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		name := strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "_") + ".json"
		raw, err := os.ReadFile(filepath.Join("..", "catalog", "testdata", name))
		if err != nil || r.URL.Query().Get("api_key") != testTMDBKey {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(tmdb.Close)
	t.Setenv("MOVIKO_TMDB_URL", tmdb.URL)
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.TMDB.APIKey = testTMDBKey
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
}

// Without a TMDB key the app runs and says so; Letterboxd isn't connected.
func TestStatusCatalog(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	var st map[string]any
	decodeBody(t, do(s, "GET", "/api/status", local, "", nil), &st)
	if st["tmdbConfigured"] != false || st["letterboxd"] != nil || st["loggedIn"] != false {
		t.Fatalf("status: tmdbConfigured=%v letterboxd=%v loggedIn=%v", st["tmdbConfigured"], st["letterboxd"], st["loggedIn"])
	}
	if _, ok := st["anilistAuthUrl"]; ok {
		t.Error("status still has AniList's login URL")
	}
	if w := do(s, "GET", "/api/media/155", local, "", nil); w.Code != http.StatusPreconditionFailed || !strings.Contains(w.Body.String(), "TMDB") {
		t.Errorf("a title without a key: %d %s", w.Code, w.Body.String())
	}

	if err := s.app.Platform.SetViewer(&catalog.Viewer{ID: 7, Name: "dave"}); err != nil {
		t.Fatal(err)
	}
	cfg := s.app.Settings.Get()
	cfg.TMDB.APIKey = testTMDBKey
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	decodeBody(t, do(s, "GET", "/api/status", local, "", nil), &st)
	if lb, _ := st["letterboxd"].(map[string]any); st["tmdbConfigured"] != true || lb["name"] != "dave" || st["loggedIn"] != true {
		t.Fatalf("status: tmdbConfigured=%v letterboxd=%v", st["tmdbConfigured"], st["letterboxd"])
	}
}

// The renamed routes: title pages, seasons, genres, the list and its
// episodes. Kumo's anime and manga routes are gone.
func TestMediaRoutes(t *testing.T) {
	s := newCatalogServer(t)
	local := "127.0.0.1:5000"

	var entry struct {
		Media struct {
			ID    int `json:"id"`
			Title struct {
				English string `json:"english"`
			} `json:"title"`
		} `json:"media"`
		Episodes []struct {
			Key int `json:"key"`
		} `json:"episodes"`
		Mappings struct {
			ImdbID string `json:"imdbId"`
		} `json:"mappings"`
	}
	decodeBody(t, do(s, "GET", "/api/media/155", local, "", nil), &entry)
	if entry.Media.ID != 155 || entry.Media.Title.English != "The Dark Knight" || len(entry.Episodes) != 1 || entry.Episodes[0].Key != 1 || entry.Mappings.ImdbID != "tt0468569" {
		t.Fatalf("title page: %+v", entry)
	}

	show := catalog.ShowID(1396)
	var season catalog.SeasonDetail
	decodeBody(t, do(s, "GET", "/api/media/"+strconv.Itoa(show)+"/season/1", local, "", nil), &season)
	if len(season.Episodes) != 7 || season.Episodes[0].Key != 1001 {
		t.Fatalf("season: %+v", season)
	}
	if w := do(s, "GET", "/api/media/155/season/1", local, "", nil); w.Code != http.StatusBadRequest {
		t.Errorf("a movie's season: %d", w.Code)
	}
	// What TMDB doesn't have is a 404 (the UI doesn't ask again), not a
	// server error.
	for _, path := range []string{"/api/media/999999", "/api/media/" + strconv.Itoa(show) + "/season/9"} {
		if w := do(s, "GET", path, local, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", path, w.Code, w.Body.String())
		}
	}

	var genres []string
	decodeBody(t, do(s, "GET", "/api/catalog/genres", local, "", nil), &genres)
	if len(genres) != 27 {
		t.Errorf("genres: %v", genres)
	}

	// On the watchlist, watched, off the list.
	if w := do(s, "POST", "/api/media/155/entry", local, `{"status":"PLANNING","score":80}`, nil); w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	var marked map[string]any
	decodeBody(t, do(s, "POST", "/api/media/155/episode", local, `{"episode":1,"watched":true}`, nil), &marked)
	if marked["status"] != "COMPLETED" || marked["progress"] != 1.0 {
		t.Fatalf("watched: %v", marked)
	}
	var list catalog.Collection
	decodeBody(t, do(s, "GET", "/api/catalog/list?type=MOVIE", local, "", nil), &list)
	if e := list.Find(155); e == nil || e.Status != "COMPLETED" || e.Score != 80 {
		t.Fatalf("list: %+v", list)
	}
	var view struct {
		Lists []struct {
			Status string `json:"status"`
		} `json:"lists"`
	}
	decodeBody(t, do(s, "GET", "/api/media/collection?type=TV", local, "", nil), &view)
	if len(view.Lists) != 0 {
		t.Errorf("shows only: %+v", view)
	}
	decodeBody(t, do(s, "GET", "/api/media/collection", local, "", nil), &view)
	if len(view.Lists) != 1 || view.Lists[0].Status != "COMPLETED" {
		t.Errorf("collection: %+v", view)
	}
	var episodes struct {
		Episodes []struct {
			Watched bool `json:"watched"`
		} `json:"episodes"`
	}
	decodeBody(t, do(s, "GET", "/api/media/155/episodes", local, "", nil), &episodes)
	if len(episodes.Episodes) != 1 || !episodes.Episodes[0].Watched {
		t.Errorf("episodes: %+v", episodes)
	}
	if w := do(s, "DELETE", "/api/media/155/entry", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	decodeBody(t, do(s, "GET", "/api/catalog/list", local, "", nil), &list)
	if list.Find(155) != nil {
		t.Error("still on the list")
	}

	// What TMDB doesn't have is a 404 (the UI doesn't ask again).
	for _, path := range []string{"/api/media/999999", "/api/media/1000999999", "/api/media/1000001396/season/9"} {
		if w := do(s, "GET", path, local, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", path, w.Code, w.Body.String())
		}
	}

	for _, path := range []string{"/api/anime/155", "/api/anilist/discover", "/api/manga/collection", "/api/anicli/status", "/api/onlinestream/providers", "/api/providers/providers"} {
		if w := do(s, "GET", path, local, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want gone", path, w.Code)
		}
	}
	if w := do(s, "GET", "/api/providers", local, "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"archive"`) {
		t.Errorf("providers: %d %s", w.Code, w.Body.String())
	}
}

// A title or season TMDB doesn't have is a 404 (which the UI doesn't ask
// for again), with TMDB's reason.
func TestNotOnTMDB(t *testing.T) {
	s := newCatalogServer(t)
	local := "127.0.0.1:5000"
	for _, path := range []string{"/api/media/99999999", "/api/media/" + strconv.Itoa(catalog.ShowID(1396)) + "/season/42"} {
		if w := do(s, "GET", path, local, "", nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "TMDB") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// Devices on the LAN never see the API keys, and can't change them.
func TestLANSettingsHideKeys(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	cfg.TMDB.APIKey = testTMDBKey
	cfg.Letterboxd.APIClientSecret = "lb-secret"
	cfg.TorrentSources.Torznab = []config.TorznabIndexer{{Name: "Prowlarr", URL: "http://127.0.0.1:9696/1/api", APIKey: "prowlarr-secret", Enabled: true}}
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	w := do(s, "GET", "/api/settings", lan, "", nil)
	for _, secret := range []string{testTMDBKey, "lb-secret", "prowlarr-secret"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("LAN device can read %q", secret)
		}
	}
	if w := do(s, "PUT", "/api/settings", lan, w.Body.String(), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	}
	got := s.app.Settings.Get()
	if got.TMDB.APIKey != testTMDBKey || got.Letterboxd.APIClientSecret != "lb-secret" || got.TorrentSources.Torznab[0].APIKey != "prowlarr-secret" {
		t.Fatalf("a LAN save lost the keys: %+v %+v", got.TMDB, got.TorrentSources.Torznab)
	}
	if w := do(s, "GET", "/api/settings", "127.0.0.1:5000", "", nil); !strings.Contains(w.Body.String(), testTMDBKey) {
		t.Error("this computer can't see the TMDB key it set")
	}
}
