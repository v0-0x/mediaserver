package torrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// fakeProvider is an extension's torrent provider.
type fakeProvider struct {
	id    string
	res   []*SearchResult
	mu    sync.Mutex
	smart []SmartQuery
	text  []string
}

func (p *fakeProvider) ID() string   { return p.id }
func (p *fakeProvider) Name() string { return "Ext " + p.id }
func (p *fakeProvider) copies() []*SearchResult {
	var out []*SearchResult
	for _, r := range p.res {
		c := *r
		out = append(out, &c)
	}
	return out
}
func (p *fakeProvider) Search(_ context.Context, q string) ([]*SearchResult, error) {
	p.mu.Lock()
	p.text = append(p.text, q)
	p.mu.Unlock()
	return p.copies(), nil
}
func (p *fakeProvider) SmartSearch(_ context.Context, q SmartQuery) ([]*SearchResult, error) {
	p.mu.Lock()
	p.smart = append(p.smart, q)
	p.mu.Unlock()
	return p.copies(), nil
}
func (p *fakeProvider) Magnet(_ context.Context, r *SearchResult) (string, error) {
	return "magnet:?ext=" + p.id, nil
}

func TestExtensionSource(t *testing.T) {
	p := &fakeProvider{id: "ext", res: []*SearchResult{
		{Provider: "ext", Name: "Show 1080p - 05", EpisodeNumber: 5, InfoHash: hashA, Seeders: 9},           // episode 5 of the season asked
		{Provider: "ext", Name: "Show.S02E06.1080p.WEB-GRP", EpisodeNumber: 6, InfoHash: hashB, Seeders: 9}, // the name says better
		{Provider: "ext", Name: "Show S02 Batch", EpisodeNumber: 3, InfoHash: hashC, IsBatch: true},
	}}
	src := &extensionSource{id: "ext-ext", p: p}
	ctx := context.Background()
	show := &catalog.Media{ID: catalog.ShowID(9), Type: catalog.TypeTV, Title: catalog.Title{English: "Show"}}

	res, err := src.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Show", Season: 2, Episode: 5, Resolution: "1080", Media: show})
	if err != nil {
		t.Fatal(err)
	}
	var keys []int
	for _, r := range res {
		if r.Provider != "ext-ext" {
			t.Fatalf("provider %q", r.Provider)
		}
		keys = append(keys, r.EpisodeNumber)
	}
	if !reflect.DeepEqual(keys, []int{2005, 2006, -1}) {
		t.Fatalf("episode keys %v", keys)
	}
	if got := p.smart[0]; got.Media != show || got.Episode != 2005 || got.Batch || got.Resolution != "1080" || got.Query != "" {
		t.Fatalf("episode: %+v", got)
	}

	_, _ = src.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Show", Season: 3})
	if got := p.smart[1]; !got.Batch || got.Episode != 3000 || got.Media == nil || got.Media.ID != 0 || got.Media.Type != catalog.TypeTV || got.Media.Title.English != "Show" {
		t.Fatalf("season pack: %+v %+v", got, got.Media)
	}
	_, _ = src.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Show", TMDBID: 9})
	if got := p.smart[2]; !got.Batch || got.Episode != 0 || got.Media.ID != catalog.ShowID(9) {
		t.Fatalf("whole show: %+v", got)
	}
	_, _ = src.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Heat", Year: 1995, TMDBID: 949, AltTitles: []string{"Heat (orig)"}})
	if m := p.smart[3].Media; p.smart[3].Batch || m.ID != 949 || m.Type != catalog.TypeMovie || m.Year() != 1995 || m.Title.Native != "Heat (orig)" {
		t.Fatalf("movie: %+v %+v", p.smart[3], m)
	}
	_, _ = src.SearchMedia(ctx, MediaQuery{Query: "free words"})
	if len(p.text) != 1 || p.text[0] != "free words" || len(p.smart) != 4 {
		t.Fatalf("free text: %v", p.text)
	}
}

func TestManagerSources(t *testing.T) {
	at := newAutoTest(t)
	m := at.torrents
	m.UseSources() // the settings' sources
	s := m.settings.Get()
	s.TorrentSources = config.TorrentSourceSettings{
		Torznab: []config.TorznabIndexer{{Name: "Prowlarr", URL: "http://127.0.0.1:9696/1/api", Enabled: true}, {Name: "Off", URL: "http://x/api"}},
		YTS:     config.TorrentSource{Enabled: true},
		EZTV:    config.TorrentSource{Enabled: true},
	}
	if _, err := m.settings.Save(s); err != nil {
		t.Fatal(err)
	}
	m.ExtraProviders = func() []Provider {
		return []Provider{&fakeProvider{id: "yts"}, &fakeProvider{id: "mine"}, &fakeProvider{id: "yts"}, nil}
	}
	var got []string
	for _, p := range m.Providers() {
		got = append(got, p.ID+"|"+p.Kind+"|"+strings.Join(p.Supports, "+"))
	}
	want := []string{"torznab-prowlarr|torznab|movie+tv", "yts|builtin|movie", "eztv|builtin|tv", "yts-ext|extension|movie+tv", "mine|extension|movie+tv", "yts-ext2|extension|movie+tv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providers\n%v\nwant\n%v", got, want)
	}
	// The sources are kept while the settings don't change.
	first := m.Source("torznab-prowlarr")
	if m.Source("torznab-prowlarr") != first || m.Source("yts-ext") == nil {
		t.Fatal("sources built again")
	}
	s.TorrentSources.YTS.Enabled = false
	if _, err := m.settings.Save(s); err != nil {
		t.Fatal(err)
	}
	// The extension now has YTS's id to itself.
	if _, ext := m.Source("yts").(*extensionSource); !ext || m.Source("torznab-prowlarr") == first || m.Source("yts-ext") == nil {
		t.Fatal("sources not rebuilt")
	}
}

func TestManagerSearch(t *testing.T) {
	movies := &fakeSource{id: "movies", types: []string{MediaMovie, ""}, res: []*SearchResult{
		{Name: "Heat.1995.1080p.BluRay-GRP", InfoHash: hashA, Seeders: 10},
		{Name: "Heat.1995.2160p.UHD.BluRay-GRP", InfoHash: hashB, Seeders: 30},
		{Name: "Heat.1986.720p", InfoHash: hashC, Seeders: 99}, // another movie
	}}
	shows := &fakeSource{id: "shows", types: []string{MediaTV}}
	at := newAutoTest(t, movies, shows)
	ctx := context.Background()
	q := MediaQuery{Type: MediaMovie, Title: "Heat", Year: 1995, Resolution: "1080"}
	for _, provider := range []string{"", AllSources, "movies"} {
		res, errs, err := at.torrents.Search(ctx, provider, q)
		if err != nil || len(errs) != 0 || len(res) != 2 || res[0].InfoHash != hashA {
			t.Fatalf("%q: %+v %v %v", provider, res, errs, err)
		}
	}
	if _, _, err := at.torrents.Search(ctx, "shows", q); err == nil || !strings.Contains(err.Error(), "doesn't search movies") {
		t.Fatalf("a show source for a movie: %v", err)
	}
	if _, _, err := at.torrents.Search(ctx, "gone", q); err == nil {
		t.Fatal("an unknown source")
	}
	if m, err := at.torrents.Magnet(ctx, &SearchResult{Provider: "shows,movies"}); err != nil || m != "magnet:?from=shows" {
		t.Fatalf("magnet %q %v", m, err)
	}
}

// A blank executable used to index an empty slice: a panic in the auto
// downloader's goroutine, which took the whole app down.
func TestStartClientBlankExecutable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	at := newAutoTest(t)
	at.torrents.UseClient(nil)
	srv.Close() // not running
	s := at.torrents.settings.Get()
	s.Qbittorrent = cfgFor(t, srv)
	s.Qbittorrent.Executable = "   "
	if _, err := at.torrents.settings.Save(s); err != nil {
		t.Fatal(err)
	}
	err := at.torrents.StartClient(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no executable") {
		t.Fatalf("got %v", err)
	}
}
