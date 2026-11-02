package stream

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fakeExtension is a Seanime-style site: a search, an episode list
// numbered from 1, and two servers.
type fakeExtension struct {
	results  []extensions.OSSearchResult
	episodes map[string]int // title id → episode count
	mu       sync.Mutex
	searches []string
	asked    []string // "id#number@server"
	failing  string   // a server that errors
}

func (f *fakeExtension) ID() string   { return "site" }
func (f *fakeExtension) Name() string { return "Some Site" }
func (f *fakeExtension) Icon() string { return "https://site.example/icon.png" }
func (f *fakeExtension) Settings(ctx context.Context) extensions.OSSettings {
	return extensions.OSSettings{EpisodeServers: []string{"alpha", "beta"}}
}

func (f *fakeExtension) Search(ctx context.Context, media *catalog.Media, query string, dub bool) ([]extensions.OSSearchResult, error) {
	f.mu.Lock()
	f.searches = append(f.searches, query)
	f.mu.Unlock()
	return f.results, nil
}

func (f *fakeExtension) FindEpisodes(ctx context.Context, id string) ([]extensions.OSEpisode, error) {
	n, ok := f.episodes[id]
	if !ok {
		return nil, fmt.Errorf("no title %s", id)
	}
	var out []extensions.OSEpisode
	for i := n; i >= 1; i-- { // the site lists them backwards
		out = append(out, extensions.OSEpisode{ID: fmt.Sprintf("%s-%d", id, i), Number: float64(i)})
	}
	return out, nil
}

func (f *fakeExtension) FindEpisodeServer(ctx context.Context, ep extensions.OSEpisode, server string) (*extensions.OSServer, error) {
	f.mu.Lock()
	f.asked = append(f.asked, fmt.Sprintf("%s#%v@%s", ep.ID, ep.Number, server))
	f.mu.Unlock()
	if server == f.failing {
		return nil, errors.New("server down")
	}
	return &extensions.OSServer{
		Server:  server,
		Headers: map[string]string{"Referer": "https://site.example/"},
		VideoSources: []extensions.OSVideoSource{
			{URL: "https://cdn.example/" + server + "/" + ep.ID + ".m3u8", Type: "m3u8", Quality: "1080p", Subtitles: []extensions.OSSubtitle{{URL: "https://cdn.example/en.vtt", Language: "English"}}},
			{URL: "https://cdn.example/" + server + "/" + ep.ID + ".mp4", Type: "mp4", Quality: "auto", Label: "720p"},
		},
	}, nil
}

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestExtensionProviderShow(t *testing.T) {
	ext := &fakeExtension{
		results:  []extensions.OSSearchResult{{ID: "bb-movie", Title: "El Camino: A Breaking Bad Movie"}, {ID: "bb", Title: "Breaking Bad"}},
		episodes: map[string]int{"bb": 62, "bb-movie": 1},
		failing:  "beta",
	}
	d := openTestDB(t)
	media := show()
	p := NewExtensionProvider(d, ext, func(ctx context.Context, id int) (*catalog.Media, error) { return media, nil })
	if p.ID() != "ext:site" || p.Kind() != providers.KindExtension || p.Icon() == "" || !p.Supports(providers.TypeMovie) || !p.Supports(providers.TypeTV) {
		t.Fatalf("provider: %s %s %q", p.ID(), p.Kind(), p.Icon())
	}
	// S02E03 is the 10th episode (7 in season 1).
	streams, err := p.Streams(context.Background(), RequestFor(media, 2003))
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams: %+v", streams)
	}
	s := streams[0]
	if s.URL != "https://cdn.example/alpha/bb-10.m3u8" || s.Type != providers.StreamM3U8 || s.Quality != "1080p" || s.Headers["Referer"] != "https://site.example/" ||
		len(s.Subtitles) != 1 || !strings.Contains(s.Title, "Breaking Bad") || !strings.Contains(s.Title, "alpha") {
		t.Fatalf("stream: %+v", s)
	}
	if streams[1].Quality != "720p" || streams[1].Type != providers.StreamMP4 {
		t.Fatalf("second stream: %+v", streams[1])
	}
	m := p.CurrentMatch(media.ID)
	if m == nil || m.ID != "bb" || m.Manual || m.Provider != "ext:site" {
		t.Fatalf("match: %+v", m)
	}

	// The match and the episode list are kept: no search the second time.
	searches := len(ext.searches)
	if _, err := p.Streams(context.Background(), RequestFor(media, 1001)); err != nil {
		t.Fatal(err)
	}
	if len(ext.searches) != searches {
		t.Errorf("searched again: %v", ext.searches)
	}

	// Picked by hand.
	if err := p.SetMatch(media.ID, "bb-movie", "El Camino"); err != nil {
		t.Fatal(err)
	}
	if m := p.CurrentMatch(media.ID); m == nil || m.ID != "bb-movie" || !m.Manual {
		t.Fatalf("manual match: %+v", m)
	}
	if _, err := p.Streams(context.Background(), RequestFor(media, 2003)); err == nil || !strings.Contains(err.Error(), "S02E03") {
		t.Fatalf("an episode the title doesn't have: %v", err)
	}
	res, err := p.SearchTitles(context.Background(), "breaking", media.ID)
	if err != nil || len(res) != 2 {
		t.Fatalf("search: %+v %v", res, err)
	}
}

// A site that has fewer episodes than the show (the latest season isn't
// there yet) has no episode past them: never one of an earlier season
// with that number in its season.
func TestExtensionProviderMissingEpisode(t *testing.T) {
	ext := &fakeExtension{results: []extensions.OSSearchResult{{ID: "bb", Title: "Breaking Bad"}}, episodes: map[string]int{"bb": 7}}
	media := show()
	p := NewExtensionProvider(openTestDB(t), ext, func(ctx context.Context, id int) (*catalog.Media, error) { return media, nil })
	streams, err := p.Streams(context.Background(), RequestFor(media, 2003))
	if err == nil || !strings.Contains(err.Error(), "S02E03") {
		t.Fatalf("S02E03 of a site with season 1 only: %v, %+v", err, streams)
	}
	// Picked by hand (a site listing each season apart), the season's own
	// number is tried.
	ext.episodes["bb-s2"] = 5
	if err := p.SetMatch(media.ID, "bb-s2", "Breaking Bad Season 2"); err != nil {
		t.Fatal(err)
	}
	if streams, err := p.Streams(context.Background(), RequestFor(media, 2003)); err != nil || len(streams) == 0 || !strings.Contains(streams[0].URL, "bb-s2-3.") {
		t.Fatalf("hand-picked season: %v, %+v", err, streams)
	}
}

func TestExtensionProviderMovieAndBadMatch(t *testing.T) {
	ext := &fakeExtension{
		results:  []extensions.OSSearchResult{{ID: "tdk", Title: "The Dark Knight (2008)"}},
		episodes: map[string]int{"tdk": 1},
	}
	d := openTestDB(t)
	p := NewExtensionProvider(d, ext, nil) // looked up from the request
	streams, err := p.Streams(context.Background(), RequestFor(movie(), 1))
	if err != nil || len(streams) != 4 {
		t.Fatalf("movie: %d streams, %v", len(streams), err)
	}

	other := &fakeExtension{results: []extensions.OSSearchResult{{ID: "x", Title: "Completely Unrelated"}}, episodes: map[string]int{"x": 3}}
	p = NewExtensionProvider(openTestDB(t), other, nil)
	if _, err := p.Streams(context.Background(), RequestFor(movie(), 1)); err == nil || !strings.Contains(err.Error(), "no good match") {
		t.Fatalf("bad match: %v", err)
	}
	none := &fakeExtension{}
	p = NewExtensionProvider(openTestDB(t), none, nil)
	if _, err := p.Streams(context.Background(), RequestFor(movie(), 1)); err == nil || !strings.Contains(err.Error(), "found nothing") {
		t.Fatalf("nothing found: %v", err)
	}
}

// Through the service: the manual match is reported and changing it
// forgets the streams found with the old one.
func TestServiceMatches(t *testing.T) {
	ext := &fakeExtension{
		results:  []extensions.OSSearchResult{{ID: "tdk", Title: "The Dark Knight"}, {ID: "tdk2", Title: "The Dark Knight Returns"}},
		episodes: map[string]int{"tdk": 1, "tdk2": 1},
	}
	p := NewExtensionProvider(openTestDB(t), ext, nil)
	other := &fakeProvider{id: "plain", name: "Plain"}
	s := newTestService(Options{}, p, other)
	res, err := s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil || len(res.Matches) != 1 || res.Matches[0].ID != "tdk" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := s.Matcher("plain"); err == nil {
		t.Fatal("a provider without matches")
	}
	if err := s.SetMatch("ext:site", movie().ID, "tdk2", "The Dark Knight Returns"); err != nil {
		t.Fatal(err)
	}
	res, err = s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil || len(res.Matches) != 1 || !res.Matches[0].Manual || !strings.Contains(res.Streams[0].URL, "tdk2") {
		t.Fatalf("after the manual match: %+v %v", res, err)
	}
}

// An extension's subtitles reach mpv's --sub-file: like every other
// provider's, only web links are kept, never paths on this computer.
func TestServerStreamsKeepsOnlyWebSubtitles(t *testing.T) {
	srv := &extensions.OSServer{Server: "s", VideoSources: []extensions.OSVideoSource{{
		URL: "https://cdn.example/v.m3u8",
		Subtitles: []extensions.OSSubtitle{
			{URL: "https://cdn.example/en.vtt", Language: "en"},
			{URL: "/tmp/local.srt", Language: "fr"},
			{URL: "not a link", Language: "de"},
		},
	}}}
	got := serverStreams("Title", srv, false)
	if len(got) != 1 {
		t.Fatalf("streams = %d, want 1", len(got))
	}
	if subs := got[0].Subtitles; len(subs) != 1 || subs[0].URL != "https://cdn.example/en.vtt" {
		t.Fatalf("subtitles = %+v, want only the https one", subs)
	}
}
