package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/torrent"
)

// fakeSource is a torrent search source that remembers what it's asked.
type fakeSource struct {
	id    string
	types []string
	res   []*torrent.SearchResult
	err   error
	mu    sync.Mutex
	asked []torrent.MediaQuery
}

func (f *fakeSource) ID() string   { return f.id }
func (f *fakeSource) Name() string { return strings.ToUpper(f.id) }
func (f *fakeSource) Supports(t string) bool {
	for _, x := range f.types {
		if x == t {
			return true
		}
	}
	return false
}
func (f *fakeSource) Search(ctx context.Context, q string) ([]*torrent.SearchResult, error) {
	return f.SearchMedia(ctx, torrent.MediaQuery{Query: q})
}
func (f *fakeSource) SearchMedia(_ context.Context, q torrent.MediaQuery) ([]*torrent.SearchResult, error) {
	f.mu.Lock()
	f.asked = append(f.asked, q)
	f.mu.Unlock()
	var out []*torrent.SearchResult
	for _, r := range f.res {
		c := *r
		out = append(out, &c)
	}
	return out, f.err
}
func (f *fakeSource) Magnet(_ context.Context, r *torrent.SearchResult) (string, error) {
	return "magnet:?xt=urn:btih:" + r.InfoHash, nil
}

func (f *fakeSource) last() torrent.MediaQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[len(f.asked)-1]
}

// fakeClient is a torrent client that remembers what it's given.
type fakeClient struct {
	mu   sync.Mutex
	adds map[string][]string // save path → URIs
}

func (c *fakeClient) Name() string                                    { return "fake" }
func (c *fakeClient) Version(context.Context) (string, error)         { return "1", nil }
func (c *fakeClient) List(context.Context) ([]torrent.Torrent, error) { return nil, nil }
func (c *fakeClient) Pause(context.Context, []string) error           { return nil }
func (c *fakeClient) Resume(context.Context, []string) error          { return nil }
func (c *fakeClient) Remove(context.Context, []string, bool) error    { return nil }
func (c *fakeClient) Add(_ context.Context, uris []string, save string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adds == nil {
		c.adds = map[string][]string{}
	}
	c.adds[save] = append(c.adds[save], uris...)
	return nil
}

const (
	hash1 = "1111111111111111111111111111111111111111"
	hash2 = "2222222222222222222222222222222222222222"
	hash3 = "3333333333333333333333333333333333333333"
)

func TestTorrentSearch(t *testing.T) {
	s := newCatalogServer(t)
	all := &fakeSource{id: "all-kinds", types: []string{torrent.MediaMovie, torrent.MediaTV, ""}, res: []*torrent.SearchResult{
		{Name: "The.Dark.Knight.2008.1080p.BluRay.x264-GRP", InfoHash: hash1, Seeders: 50},
		{Name: "Breaking.Bad.S02E05.720p.HDTV.x264-CTU", InfoHash: hash2, Seeders: 10},
	}}
	broken := &fakeSource{id: "broken", types: []string{torrent.MediaMovie}, err: errors.New("down")}
	s.app.Torrents.UseSources(all, broken)
	local := "127.0.0.1:5000"
	search := func(body string) (out torrentSearchResponse) {
		t.Helper()
		decodeBody(t, do(s, "POST", "/api/torrents/search", local, body, nil), &out)
		return out
	}

	if w := do(s, "POST", "/api/torrents/search", local, `{"query":"  "}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty search: %d", w.Code)
	}
	if w := do(s, "POST", "/api/torrents/search", local, `{"query":"x","provider":"nope"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown source: %d", w.Code)
	}

	// Free text: nothing is filtered.
	out := search(`{"query":"dark knight"}`)
	if len(out.Results) != 2 || out.Errors == nil || all.last().Query != "dark knight" {
		t.Fatalf("free text: %+v %+v", out, all.last())
	}

	// A movie: its titles and ids; the other sources' errors come along.
	out = search(`{"mediaId":155,"resolution":"1080","provider":"all"}`)
	q := all.last()
	if q.Title != "The Dark Knight" || q.Year != 2008 || q.IMDbID != "tt0468569" || q.TMDBID != 155 || q.Type != torrent.MediaMovie || q.Resolution != "1080p" || q.Media == nil {
		t.Fatalf("movie query %+v", q)
	}
	if len(out.Results) != 1 || out.Results[0].InfoHash != hash1 || !reflect.DeepEqual(out.Errors, []string{"BROKEN: down"}) {
		t.Fatalf("movie: %+v", out)
	}

	// A show's episode, season pack and whole show.
	show := strconv.Itoa(catalog.ShowID(1396))
	for _, tc := range []struct {
		body            string
		season, episode int
	}{
		{`{"mediaId":` + show + `,"episode":2005}`, 2, 5},
		{`{"mediaId":` + show + `,"season":3,"batch":true,"episode":2005}`, 3, 0},
		{`{"mediaId":` + show + `,"season":3}`, 3, 0},
		{`{"mediaId":` + show + `,"batch":true}`, 0, 0},
	} {
		out = search(tc.body)
		q = all.last()
		if q.Type != torrent.MediaTV || q.Title != "Breaking Bad" || q.IMDbID != "tt0903747" || q.TVDBID != 81189 || q.Season != tc.season || q.Episode != tc.episode {
			t.Fatalf("%s: %+v", tc.body, q)
		}
		if len(out.Errors) != 0 {
			t.Fatalf("%s: a movie source was asked: %v", tc.body, out.Errors)
		}
	}
	// One source: filtered the same way (a name that says nothing stays,
	// after the episode itself).
	all.res = append(all.res, &torrent.SearchResult{Name: "Breaking.Bad.S02E06.720p.HDTV.x264-CTU", InfoHash: hash3, Seeders: 99})
	if out := search(`{"mediaId":` + show + `,"episode":2005,"provider":"all-kinds"}`); len(out.Results) != 2 || out.Results[0].EpisodeNumber != 2005 || len(out.Errors) != 0 {
		t.Fatalf("one source: %+v", out.Results)
	}

	var providers []torrent.ProviderInfo
	decodeBody(t, do(s, "GET", "/api/torrents/providers", local, "", nil), &providers)
	if len(providers) != 2 || providers[0].ID != "all-kinds" || !reflect.DeepEqual(providers[1].Supports, []string{"movie"}) || providers[1].Kind != "builtin" {
		t.Fatalf("providers %+v", providers)
	}
}

func TestTorrentDownload(t *testing.T) {
	s := newCatalogServer(t)
	src := &fakeSource{id: "src", types: []string{torrent.MediaMovie, torrent.MediaTV}}
	client := &fakeClient{}
	s.app.Torrents.UseSources(src)
	s.app.Torrents.UseClient(client)
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir, cfg.Torrent.CreateSubfolder = lib, true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	local := "127.0.0.1:5000"
	// The titles' pages, as the title pages load them.
	for _, id := range []int{155, catalog.ShowID(1396)} {
		if w := do(s, "GET", "/api/media/"+strconv.Itoa(id), local, "", nil); w.Code != http.StatusOK {
			t.Fatalf("media %d: %d %s", id, w.Code, w.Body.String())
		}
	}

	body, _ := json.Marshal(map[string]any{"mediaId": catalog.ShowID(1396), "season": 2, "results": []*torrent.SearchResult{
		{Provider: "src", Name: "Breaking.Bad.S02E05.720p.HDTV.x264-CTU", InfoHash: hash1, EpisodeNumber: 2005},
		{Provider: "src", Name: "Breaking Bad S03 1080p BluRay", InfoHash: hash2, IsBatch: true, EpisodeNumber: -1},
		{Provider: "src", Name: "Breaking Bad 1080p", InfoHash: hash3, EpisodeNumber: -1},
	}})
	var out struct {
		Added     int      `json:"added"`
		SavePaths []string `json:"savePaths"`
	}
	decodeBody(t, do(s, "POST", "/api/torrents/download", local, string(body), nil), &out)
	show := filepath.Join(lib, "Breaking Bad (2008)")
	want := map[string][]string{
		filepath.Join(show, "Season 02"): {"magnet:?xt=urn:btih:" + hash1, "magnet:?xt=urn:btih:" + hash3},
		filepath.Join(show, "Season 03"): {"magnet:?xt=urn:btih:" + hash2},
	}
	if out.Added != 3 || len(out.SavePaths) != 2 || !reflect.DeepEqual(client.adds, want) {
		t.Fatalf("show: %+v\n%v", out, client.adds)
	}

	// A movie goes to its folder, or to the one its files are in.
	client.adds = nil
	body, _ = json.Marshal(map[string]any{"mediaId": 155, "results": []*torrent.SearchResult{{Provider: "src", Name: "The.Dark.Knight.2008.1080p", InfoHash: hash1}}})
	decodeBody(t, do(s, "POST", "/api/torrents/download", local, string(body), nil), &out)
	if _, ok := client.adds[filepath.Join(lib, "The Dark Knight (2008)")]; !ok {
		t.Fatalf("movie: %v", client.adds)
	}
	own := filepath.Join(lib, "Batman", "TDK [BD]")
	if err := s.app.Files.Save(&library.LocalFile{Path: filepath.Join(own, "tdk.mkv"), Dir: own, Name: "tdk.mkv", MediaID: 155, Episode: 1, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	client.adds = nil
	decodeBody(t, do(s, "POST", "/api/torrents/download", local, string(body), nil), &out)
	if _, ok := client.adds[own]; !ok {
		t.Fatalf("movie with a folder: %v", client.adds)
	}

	if w := do(s, "POST", "/api/torrents/download", local, `{"mediaId":155,"results":[]}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("nothing: %d", w.Code)
	}
}

// Devices on the LAN don't pick folders for the auto downloader.
func TestAutoDownloaderRules(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	local, lan := "127.0.0.1:5000", "192.168.1.20:5000"
	show := strconv.Itoa(catalog.ShowID(1396))
	if w := do(s, "POST", "/api/autodownloader/rules", local, `{"mediaId":155}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a movie's rule: %d", w.Code)
	}
	var rule torrent.Rule
	decodeBody(t, do(s, "POST", "/api/autodownloader/rules", local, `{"mediaId":`+show+`,"title":"Breaking Bad","enabled":true,"destination":"/mnt/tv","resolution":"1080p"}`, nil), &rule)
	if rule.ID == 0 || rule.Destination != "/mnt/tv" || rule.Resolution != "1080" {
		t.Fatalf("saved %+v", rule)
	}
	id := strconv.Itoa(rule.ID)
	decodeBody(t, do(s, "POST", "/api/autodownloader/rules", lan, `{"id":`+id+`,"mediaId":`+show+`,"title":"Breaking Bad","destination":"/home/u/.ssh"}`, nil), &rule)
	if rule.Destination != "/mnt/tv" || rule.Enabled {
		t.Fatalf("LAN update %+v", rule)
	}
	decodeBody(t, do(s, "POST", "/api/autodownloader/rules", lan, `{"mediaId":`+show+`,"title":"Breaking Bad","destination":"/etc"}`, nil), &rule)
	if rule.Destination != "" {
		t.Fatalf("LAN rule %+v", rule)
	}
	var rules []torrent.Rule
	decodeBody(t, do(s, "GET", "/api/autodownloader/rules", local, "", nil), &rules)
	if len(rules) != 2 {
		t.Fatalf("rules %+v", rules)
	}
	var rep torrent.RunReport
	decodeBody(t, do(s, "POST", "/api/autodownloader/run", local, "{}", nil), &rep)
	if rep.Added != 0 || rep.Errors == nil {
		t.Fatalf("run %+v", rep)
	}
	var items []torrent.AutoItem
	decodeBody(t, do(s, "GET", "/api/autodownloader/items", local, "", nil), &items)
	if items == nil || len(items) != 0 {
		t.Fatalf("items %+v", items)
	}
	if w := do(s, "DELETE", "/api/autodownloader/rules/"+id, local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("delete %d", w.Code)
	}
	if w := do(s, "GET", "/api/autodownloader/rules/"+id+"/wanted", local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("wanted of a deleted rule: %d", w.Code)
	}
}
