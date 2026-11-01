package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fakeAddons serves several addons, each under its own path prefix, plus a
// collection. Requests are recorded.
type fakeAddons struct {
	mu        sync.Mutex
	manifests map[string]map[string]any // prefix ("" for the root addon) → manifest
	requests  []string
}

func newFakeAddons() *fakeAddons {
	streamAddon := func(id, name string, extra map[string]any) map[string]any {
		m := map[string]any{
			"id": id, "version": "1.0.0", "name": name, "description": name,
			"resources": []any{"catalog", "stream"}, "types": []string{"movie", "series"},
			"idPrefixes": []string{"tt"}, "catalogs": []any{},
			"behaviorHints": map[string]any{"p2p": true},
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	return &fakeAddons{manifests: map[string]map[string]any{
		"":            streamAddon("org.test.streams", "Test Streams", map[string]any{"logo": "https://addons.test/logo.png"}),
		"cfg=1080p":   streamAddon("org.test.streams", "Test Streams (1080p)", nil),
		"tmdbonly":    {"id": "org.test.tmdb", "version": "1", "name": "TMDB Only", "resources": []any{map[string]any{"name": "stream", "types": []string{"movie", "series"}, "idPrefixes": []string{"tmdb:"}}}, "types": []string{"movie"}},
		"subs":        {"id": "org.test.subs", "version": "1", "name": "Test Subtitles", "resources": []any{map[string]any{"name": "subtitles", "types": []string{"movie", "series"}, "idPrefixes": []string{"tt"}}}, "types": []string{"movie", "series"}},
		"catalogonly": {"id": "org.test.catalog", "version": "1", "name": "Catalog", "resources": []any{"catalog", "meta"}, "types": []string{"movie"}},
		"adult":       streamAddon("org.test.adult", "Adult", map[string]any{"behaviorHints": map[string]any{"adult": true}}),
		"needscfg":    streamAddon("org.test.cfg", "Needs Config", map[string]any{"behaviorHints": map[string]any{"configurable": true, "configurationRequired": true}}),
		"anime":       {"id": "org.test.anime", "version": "1", "name": "Anime", "resources": []any{"stream"}, "types": []string{"anime"}},
	}}
}

func (f *fakeAddons) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.EscapedPath())
	f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "collection.json" {
		http.ServeFile(w, r, filepath.Join("testdata", "collection.json"))
		return
	}
	prefix, rest := "", p
	for k := range f.manifests {
		if k != "" && strings.HasPrefix(p, k+"/") {
			prefix, rest = k, strings.TrimPrefix(p, k+"/")
		}
	}
	// needscfg.test is an addon that must be configured: its configured
	// URLs (/<config>/manifest.json) serve the same manifest.
	if strings.HasPrefix(r.Host, "needscfg.") {
		prefix = "needscfg"
		if i := strings.LastIndex(p, "/"); i >= 0 {
			rest = p[i+1:]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case rest == "manifest.json":
		f.mu.Lock()
		m := f.manifests[prefix]
		raw, _ := json.Marshal(m)
		f.mu.Unlock()
		_, _ = w.Write(raw)
	case rest == "stream/movie/tt0133093.json" && (prefix == "" || prefix == "cfg=1080p"):
		http.ServeFile(w, r, filepath.Join("testdata", "streams_movie.json"))
	case rest == "stream/series/tt0944947:1:2.json":
		_, _ = w.Write([]byte(`{"streams":[{"name":"S","title":"Game.of.Thrones.S01E02.720p.mkv","infoHash":"cc8255ecdc7ca55fb0bbf81323d87062db1f6d1c","fileIdx":1}]}`))
	case prefix == "tmdbonly" && rest == "stream/movie/tmdb:603.json":
		_, _ = w.Write([]byte(`{"streams":[{"name":"T 1080p","url":"https://cdn.example.org/603.mp4"}]}`))
	case prefix == "subs" && strings.HasPrefix(rest, "subtitles/movie/tt0133093"):
		_, _ = w.Write([]byte(`{"subtitles":[{"id":"a","url":"https://subs.example.org/a.srt","lang":"eng","label":"English [CC]"},{"id":"b","url":"not a url","lang":"fre"}]}`))
	case prefix == "subs" && strings.HasPrefix(rest, "subtitles/series/"):
		_, _ = w.Write([]byte(`{"subtitles":[]}`))
	case strings.HasPrefix(rest, "stream/"):
		_, _ = w.Write([]byte(`{"streams":[]}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAddons) lastRequest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return ""
	}
	return f.requests[len(f.requests)-1]
}

// testClient routes every connection to the fake server, so the addons can
// have public-looking host names (addons.test): the local-address guard
// only lets loopback URLs through when the user allows them.
func testClient(t *testing.T) (*Client, *fakeAddons, *httptest.Server) {
	t.Helper()
	fa := newFakeAddons()
	srv := httptest.NewServer(fa)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	public := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	return &Client{HTTP: public, Local: srv.Client()}, fa, srv
}

func newTestStore(t *testing.T, c *Client) (*Store, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := NewStore(d, c)
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://torrentio.strem.fun/manifest.json", "https://torrentio.strem.fun/manifest.json"},
		{"https://torrentio.strem.fun", "https://torrentio.strem.fun/manifest.json"},
		{" https://torrentio.strem.fun/ ", "https://torrentio.strem.fun/manifest.json"},
		{"stremio://torrentio.strem.fun/providers=yts|eztv/manifest.json", "https://torrentio.strem.fun/providers=yts|eztv/manifest.json"},
		{"STREMIO://addon.test/manifest.json#x", "https://addon.test/manifest.json"},
		{"HTTPS://addon.test/%7B%22a%22%3A1%7D/manifest.json?x=1", "https://addon.test/%7B%22a%22%3A1%7D/manifest.json"},
		{"http://192.168.1.5:7000/manifest.json/", "http://192.168.1.5:7000/manifest.json"},
	} {
		got, err := NormalizeURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "ftp://addon.test/manifest.json", "file:///etc/passwd", "javascript:alert(1)", "addon.test/manifest.json", "https://"} {
		if got, err := NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) = %q, want an error", bad, got)
		}
	}
}

func TestIsLocalURL(t *testing.T) {
	for in, want := range map[string]bool{
		"http://127.0.0.1:7000/manifest.json":     true,
		"http://localhost:11470/manifest.json":    true,
		"http://192.168.1.10/manifest.json":       true,
		"http://10.0.0.2/manifest.json":           true,
		"http://[::1]:7000/manifest.json":         true,
		"http://nas:7000/manifest.json":           true,
		"http://media.local/manifest.json":        true,
		"http://box.home.arpa/manifest.json":      true,
		"https://torrentio.strem.fun/m.json":      false,
		"https://8.8.8.8/manifest.json":           false,
		"https://addon.example.org/manifest.json": false,
	} {
		if got := IsLocalURL(in); got != want {
			t.Errorf("IsLocalURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestManifestResources(t *testing.T) {
	var m Manifest
	raw := `{"id":"a","name":"A","types":["movie","series"],"idPrefixes":["tt"],
		"resources":["catalog",{"name":"stream","types":["movie"],"idPrefixes":["tt","tmdb:"]},"subtitles"]}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if ok, p := m.Serves(ResourceStream, TypeMovie); !ok || len(p) != 2 {
		t.Errorf("stream/movie = %v %v", ok, p)
	}
	if ok, _ := m.Serves(ResourceStream, TypeSeries); ok {
		t.Error("stream/series served")
	}
	if ok, p := m.Serves(ResourceSubtitles, TypeSeries); !ok || len(p) != 1 || p[0] != "tt" {
		t.Errorf("subtitles/series = %v %v (the manifest's types and prefixes apply)", ok, p)
	}
	if !m.Useful() {
		t.Error("not useful")
	}
	// The string form survives a round trip.
	out, _ := json.Marshal(m.Resources)
	if !strings.HasPrefix(string(out), `["catalog",{"name":"stream"`) || !strings.HasSuffix(string(out), `"subtitles"]`) {
		t.Errorf("resources = %s", out)
	}
	var bad Manifest
	if err := json.Unmarshal([]byte(`{"id":"a","name":"A","resources":[42]}`), &bad); err == nil {
		t.Error("a numeric resource was accepted")
	}
}

func TestVideoID(t *testing.T) {
	mk := func(prefixes ...string) Manifest {
		return Manifest{ID: "x", Name: "X", Types: []string{"movie", "series"}, IDPrefixes: prefixes, Resources: []Resource{{Name: "stream", short: true}}}
	}
	movie := providers.Request{Type: providers.TypeMovie, TMDBID: 603, IMDbID: "tt0133093"}
	episode := providers.Request{Type: providers.TypeTV, TMDBID: 1399, IMDbID: "tt0944947", Season: 1, Episode: 2}
	noIMDb := providers.Request{Type: providers.TypeMovie, TMDBID: 99}
	for _, tc := range []struct {
		m       Manifest
		r       providers.Request
		want    string
		wantErr error
	}{
		{mk("tt"), movie, "tt0133093", nil},
		{mk("tt"), episode, "tt0944947:1:2", nil},
		{mk(), movie, "tt0133093", nil},
		{mk("tmdb:"), movie, "tmdb:603", nil},
		{mk("tmdb:"), episode, "tmdb:1399:1:2", nil},
		{mk("tt", "tmdb:"), noIMDb, "tmdb:99", nil},
		{mk(), noIMDb, "tmdb:99", nil},
		{mk("tt"), noIMDb, "", ErrNoIMDbID},
	} {
		got, err := tc.m.VideoID(ResourceStream, tc.r)
		if got != tc.want || !errors.Is(err, tc.wantErr) {
			t.Errorf("VideoID(%v, %+v) = %q, %v; want %q, %v", tc.m.IDPrefixes, tc.r, got, err, tc.want, tc.wantErr)
		}
	}
	if _, err := mk("kitsu:").VideoID(ResourceStream, movie); err == nil {
		t.Error("no error for an addon that takes other ids")
	}
}

func TestStreams(t *testing.T) {
	c, fa, _ := testClient(t)
	a, err := c.FetchManifest(context.Background(), "http://addons.test/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if a.TransportURL != "http://addons.test/manifest.json" || a.Manifest.Name != "Test Streams" || !a.Supported || a.Local {
		t.Errorf("addon = %+v", a)
	}
	streams, err := NewProvider(c, a).Streams(context.Background(), providers.Request{Type: providers.TypeMovie, TMDBID: 603, IMDbID: "tt0133093", Title: "The Matrix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fa.lastRequest(); got != "/stream/movie/tt0133093.json" {
		t.Errorf("request = %s", got)
	}
	if len(streams) != 7 {
		for _, s := range streams {
			t.Logf("%+v", s)
		}
		t.Fatalf("got %d streams, want 7", len(streams))
	}
	for _, s := range streams {
		if s.Provider != "stremio:org.test.streams" || s.ProviderName != "Test Streams" {
			t.Errorf("provider of %q = %s/%s", s.Title, s.Provider, s.ProviderName)
		}
	}

	tor := streams[0]
	if tor.InfoHash != "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c" || tor.FileIdx != 0 || tor.Quality != "2160p" ||
		tor.Seeders != 152 || tor.Size != int64(18.5*(1<<30)) || tor.URL != "" || tor.Type != providers.StreamMKV ||
		tor.Filename != "The.Matrix.1999.2160p.UHD.BluRay.x265-GROUP.mkv" {
		t.Errorf("torrent = %+v", tor)
	}
	if !strings.Contains(tor.Magnet, "tr=udp%3A%2F%2Ftracker.example.org%3A1337%2Fannounce") || strings.Contains(tor.Magnet, "opentrackr") ||
		!strings.Contains(tor.Magnet, "dn=The.Matrix.1999.2160p.UHD.BluRay.x265-GROUP.mkv") {
		t.Errorf("magnet = %s", tor.Magnet)
	}

	direct := streams[1]
	if direct.URL != "https://cdn.example.org/files/The.Matrix.1999.1080p.mp4" || direct.Type != providers.StreamMP4 ||
		direct.Quality != "1080p" || direct.Size != 2254857830 || !direct.NotWebReady || direct.FileIdx != -1 ||
		direct.Filename != "The.Matrix.1999.1080p.mp4" || direct.Seeders != 12 || !strings.HasPrefix(direct.Title, "The Matrix 1999 1080p WEB") {
		t.Errorf("direct = %+v", direct)
	}
	if direct.Headers["Referer"] != "https://example.org/" || direct.Headers["User-Agent"] != "Stremio" || direct.Headers["X-Num"] != "5" {
		t.Errorf("headers = %v", direct.Headers)
	}
	if len(direct.Subtitles) != 1 || direct.Subtitles[0].Language != "eng" {
		t.Errorf("subtitles = %+v", direct.Subtitles)
	}

	hls := streams[2]
	if hls.Type != providers.StreamM3U8 || hls.Quality != "720p" || hls.Title != "hls" {
		t.Errorf("hls = %+v", hls)
	}
	if yt := streams[3]; yt.YouTubeID != "vKQi3bBA1y8" || yt.Title != "Trailer" {
		t.Errorf("youtube = %+v", yt)
	}
	if ext := streams[4]; ext.ExternalURL != "https://www.netflix.com/title/20557937" {
		t.Errorf("external = %+v", ext)
	}
	if mag := streams[5]; mag.InfoHash != "aa8255ecdc7ca55fb0bbf81323d87062db1f6d1c" || !strings.HasPrefix(mag.Magnet, "magnet:?xt=urn:btih:aa8255") || mag.FileIdx != -1 {
		t.Errorf("magnet stream = %+v", mag)
	}
	if lenient := streams[6]; lenient.FileIdx != 3 || lenient.Size != 734003200 || lenient.Quality != "480p" || !strings.Contains(lenient.Magnet, "opentrackr") {
		t.Errorf("lenient = %+v", lenient)
	}

	// Episodes use "<imdb>:<season>:<episode>".
	eps, err := NewProvider(c, a).Streams(context.Background(), providers.Request{Type: providers.TypeTV, TMDBID: 1399, IMDbID: "tt0944947", Season: 1, Episode: 2})
	if err != nil || len(eps) != 1 || eps[0].Quality != "720p" || eps[0].FileIdx != 1 {
		t.Fatalf("episode streams = %+v, %v", eps, err)
	}
	if got := fa.lastRequest(); got != "/stream/series/tt0944947:1:2.json" {
		t.Errorf("request = %s", got)
	}
	// No IMDb id: a clear error.
	if _, err := NewProvider(c, a).Streams(context.Background(), providers.Request{Type: providers.TypeMovie, TMDBID: 5}); !errors.Is(err, ErrNoIMDbID) {
		t.Errorf("err = %v", err)
	}
}

func TestTMDBOnlyAddon(t *testing.T) {
	c, fa, _ := testClient(t)
	a, err := c.FetchManifest(context.Background(), "http://addons.test/tmdbonly/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	p := NewProvider(c, a)
	if !p.Supports(providers.TypeMovie) || !p.Supports(providers.TypeTV) {
		t.Error("the resource's types are ignored")
	}
	streams, err := p.Streams(context.Background(), providers.Request{Type: providers.TypeMovie, TMDBID: 603, IMDbID: "tt0133093"})
	if err != nil || len(streams) != 1 || streams[0].Quality != "1080p" {
		t.Fatalf("streams = %+v, %v", streams, err)
	}
	if got := fa.lastRequest(); got != "/tmdbonly/stream/movie/tmdb:603.json" {
		t.Errorf("request = %s", got)
	}
}

func TestSubtitles(t *testing.T) {
	c, fa, _ := testClient(t)
	a, err := c.FetchManifest(context.Background(), "http://addons.test/subs")
	if err != nil {
		t.Fatal(err)
	}
	p := NewProvider(c, a)
	if p.Supports(providers.TypeMovie) || !p.SupportsSubtitles(providers.TypeMovie) {
		t.Error("wrong resources")
	}
	subs, err := p.Subtitles(context.Background(), providers.Request{Type: providers.TypeMovie, IMDbID: "tt0133093"})
	if err != nil || len(subs) != 1 || subs[0].Language != "eng" || subs[0].Label != "English [CC]" {
		t.Fatalf("subs = %+v, %v", subs, err)
	}
	if _, err := c.SubtitlesFor(context.Background(), a, TypeMovie, "tt0133093", map[string]string{"filename": "The Matrix.mkv", "videoSize": "123"}); err != nil {
		t.Fatal(err)
	}
	if got := fa.lastRequest(); got != "/subs/subtitles/movie/tt0133093/filename=The%20Matrix.mkv&videoSize=123.json" {
		t.Errorf("request = %s", got)
	}

	// For the very file playing: its size and hash, as Stremio sends them
	// (an episode as tt…:season:episode).
	for _, tc := range []struct {
		r    providers.Request
		v    providers.Video
		want string
	}{
		{providers.Request{Type: providers.TypeMovie, IMDbID: "tt0133093"}, providers.Video{Size: 1234567890, Hash: "8e245d9679d31e12", Filename: "The Matrix.mkv"},
			"/subs/subtitles/movie/tt0133093/filename=The%20Matrix.mkv&videoHash=8e245d9679d31e12&videoSize=1234567890.json"},
		{providers.Request{Type: providers.TypeTV, IMDbID: "tt0944947", Season: 1, Episode: 2}, providers.Video{Size: 42},
			"/subs/subtitles/series/tt0944947:1:2/videoSize=42.json"},
		{providers.Request{Type: providers.TypeMovie, IMDbID: "tt0133093"}, providers.Video{},
			"/subs/subtitles/movie/tt0133093.json"},
	} {
		if _, err := p.VideoSubtitles(context.Background(), tc.r, tc.v); err != nil {
			t.Fatal(err)
		}
		if got := fa.lastRequest(); got != tc.want {
			t.Errorf("request = %s, want %s", got, tc.want)
		}
	}
	var _ providers.VideoSubtitleProvider = p
}

func TestFetchCollection(t *testing.T) {
	c, _, _ := testClient(t)
	list, err := c.FetchCollection(context.Background(), "http://addons.test/collection.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d addons: %+v", len(list), list)
	}
	if list[0].Manifest.ID != "com.linvo.cinemeta" || list[0].Supported || !list[0].Flags.Official || !list[0].Flags.Protected {
		t.Errorf("cinemeta = %+v", list[0])
	}
	if !list[1].Supported || list[1].TransportURL != "http://addons.test/manifest.json" {
		t.Errorf("streams = %+v", list[1])
	}
	if list[2].TransportURL != "https://opensubtitles.test/manifest.json" || !list[2].Supported {
		t.Errorf("subs = %+v", list[2])
	}
	if _, err := c.FetchCollection(context.Background(), "http://addons.test/manifest.json"); err == nil {
		t.Error("a manifest was taken for a collection")
	}
}

func TestLocalAddonsNeedConsent(t *testing.T) {
	c, _, srv := testClient(t)
	s, _ := newTestStore(t, c)
	local := srv.URL + "/manifest.json"
	if _, err := c.FetchManifest(context.Background(), local); !errors.Is(err, ErrLocalAddon) {
		t.Errorf("FetchManifest(local) err = %v", err)
	}
	if _, err := s.Install(context.Background(), local, InstallOptions{}); !errors.Is(err, ErrLocalAddon) {
		t.Errorf("Install(local) err = %v", err)
	}
	a, err := s.Install(context.Background(), local, InstallOptions{AllowLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Local {
		t.Error("not marked local")
	}
	// Installed on purpose, it works.
	streams, errs := providers.Aggregate(context.Background(), s.Providers(), providers.Request{Type: providers.TypeMovie, IMDbID: "tt0133093"}, 5*time.Second)
	if len(errs) != 0 || len(streams) != 7 {
		t.Errorf("streams = %d, errs = %v", len(streams), errs)
	}
}

func TestStoreInstallRules(t *testing.T) {
	c, _, _ := testClient(t)
	s, _ := newTestStore(t, c)
	ctx := context.Background()
	if _, err := s.Install(ctx, "http://addons.test/catalogonly/manifest.json", InstallOptions{}); !errors.Is(err, ErrUnsupportedAddon) {
		t.Errorf("catalog addon: %v", err)
	}
	if _, err := s.Install(ctx, "http://addons.test/anime/manifest.json", InstallOptions{}); !errors.Is(err, ErrUnsupportedAddon) {
		t.Errorf("anime addon: %v", err)
	}
	if _, err := s.Install(ctx, "http://addons.test/adult/manifest.json", InstallOptions{}); !errors.Is(err, ErrAdultAddon) {
		t.Errorf("adult addon: %v", err)
	}
	if _, err := s.Install(ctx, "http://addons.test/adult/manifest.json", InstallOptions{AllowAdult: true}); err != nil {
		t.Errorf("allowed adult addon: %v", err)
	}
	var cfgErr *ConfigurationError
	if _, err := s.Install(ctx, "http://needscfg.test/manifest.json", InstallOptions{}); !errors.As(err, &cfgErr) || cfgErr.ConfigureURL != "http://needscfg.test/configure" {
		t.Errorf("unconfigured addon: %v", err)
	}
	if _, err := s.Install(ctx, "http://needscfg.test/token=abc/manifest.json", InstallOptions{}); err != nil {
		t.Errorf("configured addon: %v", err)
	}
	if _, err := s.Install(ctx, "ftp://addons.test/manifest.json", InstallOptions{}); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("ftp: %v", err)
	}
	if _, err := s.Install(ctx, "http://addons.test/missing/manifest.json", InstallOptions{}); err == nil {
		t.Error("missing addon installed")
	}
}

func TestStore(t *testing.T) {
	c, fa, _ := testClient(t)
	s, d := newTestStore(t, c)
	ctx := context.Background()
	changes := 0
	s.OnChange = func() { changes++ }

	for _, u := range []string{"http://addons.test/manifest.json", "http://addons.test/subs/manifest.json", "http://addons.test/tmdbonly"} {
		if _, err := s.Install(ctx, u, InstallOptions{}); err != nil {
			t.Fatalf("install %s: %v", u, err)
		}
	}
	names := func(list []Addon) string {
		var out []string
		for _, a := range list {
			out = append(out, a.Manifest.Name)
		}
		return strings.Join(out, "|")
	}
	if got := names(s.List()); got != "Test Streams|Test Subtitles|TMDB Only" {
		t.Errorf("list = %s", got)
	}
	if len(s.Providers()) != 2 || len(s.SubtitleProviders()) != 1 {
		t.Errorf("providers = %d, subtitle providers = %d", len(s.Providers()), len(s.SubtitleProviders()))
	}
	if p := s.Providers()[0]; p.ID() != "stremio:org.test.streams" || p.Kind() != providers.KindStremio || providers.Describe(p).Icon != "https://addons.test/logo.png" {
		t.Errorf("provider = %+v", providers.Describe(p))
	}

	// Another configuration of an installed addon replaces it, in place.
	if _, err := s.Install(ctx, "http://addons.test/cfg=1080p/manifest.json", InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := names(s.List()); got != "Test Streams (1080p)|Test Subtitles|TMDB Only" {
		t.Errorf("after reconfiguring: %s", got)
	}

	if err := s.Reorder([]string{"stremio:org.test.tmdb", "http://addons.test/subs"}); err != nil {
		t.Fatal(err)
	}
	if got := names(s.List()); got != "TMDB Only|Test Subtitles|Test Streams (1080p)" {
		t.Errorf("after reorder: %s", got)
	}
	if err := s.SetEnabled("org.test.tmdb", false); err != nil {
		t.Fatal(err)
	}
	if len(s.Providers()) != 1 {
		t.Errorf("a disabled addon is still a provider")
	}
	if err := s.Uninstall("http://addons.test/subs/manifest.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("http://addons.test/subs/manifest.json"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second uninstall: %v", err)
	}

	// Refresh picks manifest changes up.
	fa.mu.Lock()
	fa.manifests["tmdbonly"]["name"] = "TMDB Only v2"
	fa.mu.Unlock()
	if errs := s.Refresh(ctx); len(errs) != 0 {
		t.Errorf("refresh errors: %v", errs)
	}

	// Everything survives a restart.
	s2, err := NewStore(d, c)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if got := names(list); got != "TMDB Only v2|Test Streams (1080p)" {
		t.Errorf("after restart: %s", got)
	}
	if list[0].Enabled || !list[1].Enabled || !list[1].Installed || list[1].InstalledAt == 0 {
		t.Errorf("state = %+v", list)
	}
	if changes == 0 {
		t.Error("OnChange never called")
	}

	// Collections show what's installed.
	coll, err := s2.Collection(ctx, "http://addons.test/collection.json")
	if err != nil {
		t.Fatal(err)
	}
	if coll[0].Installed || !coll[1].Installed || coll[2].Installed {
		t.Errorf("collection = %+v", coll)
	}
}
