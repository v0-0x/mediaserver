package mediaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

const token = "s3cret-token"

// fakeServer is a Jellyfin/Emby or Plex server answering from testdata/.
// It refuses requests without the token, sent the way its kind expects.
type fakeServer struct {
	*httptest.Server
	mu    sync.Mutex
	asked []string
}

func (f *fakeServer) requests() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.asked, "\n")
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newFake(t *testing.T, kind, prefix string) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.asked = append(f.asked, r.URL.RequestURI())
		f.mu.Unlock()
		var authed bool
		switch kind {
		case Jellyfin:
			authed = r.Header.Get("Authorization") == `MediaBrowser Token="`+token+`"`
		case Emby:
			authed = r.Header.Get("X-Emby-Token") == token
		case Plex:
			authed = r.Header.Get("X-Plex-Token") == token
		}
		if !authed || r.URL.Query().Get("api_key") != "" || r.URL.Query().Get("X-Plex-Token") != "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if kind == Plex {
			servePlex(t, w, r, p, q)
			return
		}
		switch {
		case p == "/System/Info":
			_, _ = w.Write([]byte(`{"ServerName":"Home","Version":"10.10.3","Id":"x"}`))
		case p == "/Users/Me" && kind == Jellyfin:
			_, _ = w.Write([]byte(`{"Id":"u1","Name":"me"}`))
		case p == "/Users/u1":
			_, _ = w.Write([]byte(`{"Id":"u1","Name":"me"}`))
		case p == "/Items" || p == "/Users/u1/Items":
			switch {
			case q.Get("Ids") == "m1":
				_, _ = w.Write(fixture(t, "jellyfin/items-m1.json"))
			case q.Get("Ids") == "m2":
				_, _ = w.Write(fixture(t, "jellyfin/items-m2.json"))
			case q.Get("Recursive") != "true" || !strings.Contains(q.Get("Fields"), "ProviderIds"):
				http.Error(w, "bad listing", http.StatusBadRequest)
			case q.Get("IncludeItemTypes") == "Movie":
				_, _ = w.Write(fixture(t, "jellyfin/movies.json"))
			case q.Get("IncludeItemTypes") == "Series":
				_, _ = w.Write(fixture(t, "jellyfin/series.json"))
			default:
				_, _ = w.Write([]byte(`{"Items":[]}`))
			}
		case p == "/Shows/s1/Episodes" && q.Get("season") == "1":
			_, _ = w.Write(fixture(t, "jellyfin/episodes-s1.json"))
		case strings.HasPrefix(p, "/Shows/"):
			_, _ = w.Write([]byte(`{"Items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func servePlex(t *testing.T, w http.ResponseWriter, r *http.Request, p string, q url.Values) {
	switch {
	case p == "/":
		_, _ = w.Write([]byte(`{"MediaContainer":{"friendlyName":"Plexy","version":"1.41.0"}}`))
	case p == "/library/sections":
		_, _ = w.Write(fixture(t, "plex/sections.json"))
	case q.Get("includeGuids") != "1" && strings.HasSuffix(p, "/all"):
		http.Error(w, "no guids asked", http.StatusBadRequest)
	case p == "/library/sections/1/all" && q.Get("type") == "1":
		_, _ = w.Write(fixture(t, "plex/movies.json"))
	case p == "/library/sections/2/all" && q.Get("type") == "2":
		_, _ = w.Write(fixture(t, "plex/shows.json"))
	case p == "/library/metadata/100/children":
		_, _ = w.Write(fixture(t, "plex/seasons.json"))
	case p == "/library/metadata/101/children":
		_, _ = w.Write(fixture(t, "plex/season1.json"))
	case strings.HasPrefix(p, "/library/metadata/"):
		_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
	default:
		http.NotFound(w, r)
	}
}

func newProvider(t *testing.T, c Config) *Provider {
	t.Helper()
	p, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func movie(tmdb int, imdb string) providers.Request {
	return providers.Request{Type: providers.TypeMovie, TMDBID: tmdb, IMDbID: imdb, Title: "x"}
}

func episode(season, ep int) providers.Request {
	return providers.Request{Type: providers.TypeTV, TMDBID: 1396, IMDbID: "tt0903747", Season: season, Episode: ep}
}

func noToken(t *testing.T, streams []providers.Stream) {
	t.Helper()
	for _, s := range streams {
		if strings.Contains(s.URL, token) || len(s.Headers) > 0 {
			t.Errorf("the token is in a stream: %+v", s)
		}
	}
}

// Jellyfin and Emby: a movie by its TMDB id (its IMDb id when the server
// has no TMDB id for it), each media source a link to its file.
func TestJellyfinMovies(t *testing.T) {
	for _, tc := range []struct {
		kind, user, prefix, items string
	}{
		{Jellyfin, "", "", "/Items?"},
		{Emby, "u1", "/emby", "/emby/Users/u1/Items?"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			srv := newFake(t, tc.kind, tc.prefix)
			p := newProvider(t, Config{Kind: tc.kind, URL: srv.URL + tc.prefix + "/", Token: token, UserID: tc.user})
			ctx := context.Background()

			streams, err := p.Streams(ctx, movie(603, "tt0133093"))
			if err != nil || len(streams) != 2 {
				t.Fatalf("%+v %v", streams, err)
			}
			noToken(t, streams)
			uhd, hd := streams[0], streams[1]
			if uhd.URL != srv.URL+tc.prefix+"/Videos/m1/stream?MediaSourceId=ms4k&static=true" || uhd.Quality != "2160p" || uhd.Type != providers.StreamMKV ||
				!uhd.NotWebReady || uhd.Size != 40000000000 || uhd.Filename != "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP.mkv" ||
				uhd.Title != "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP.mkv\nHEVC · HDR · TrueHD 7.1 · MKV" {
				t.Errorf("4K: %+v", uhd)
			}
			if hd.Quality != "1080p" || hd.Type != providers.StreamMP4 || hd.NotWebReady || hd.Filename != "The Matrix (1999) - 1080p.mp4" {
				t.Errorf("1080p: %+v", hd)
			}
			if !strings.Contains(srv.requests(), tc.items) {
				t.Errorf("items not asked at %s:\n%s", tc.items, srv.requests())
			}

			// No TMDB id on the server: its IMDb id.
			streams, err = p.Streams(ctx, movie(10331, "tt0063350"))
			if err != nil || len(streams) != 1 || streams[0].Quality != "480p" || !streams[0].NotWebReady || streams[0].Type != providers.StreamUnknown {
				t.Fatalf("by IMDb id: %+v %v", streams, err)
			}
			// Not on the server.
			if streams, err := p.Streams(ctx, movie(1, "tt0000001")); err != nil || len(streams) != 0 {
				t.Fatalf("missing movie: %+v %v", streams, err)
			}
			if _, err := p.Streams(ctx, providers.Request{Type: providers.TypeMovie, Title: "x"}); !providers.IsSkip(err) {
				t.Fatalf("no ids: %v", err)
			}
		})
	}
}

// A show's episode: the series by its ids, then the season's episodes; a
// file holding several episodes is each one's.
func TestJellyfinEpisodes(t *testing.T) {
	srv := newFake(t, Jellyfin, "")
	p := newProvider(t, Config{Kind: Jellyfin, URL: srv.URL, Token: token})
	ctx := context.Background()
	for _, tc := range []struct {
		season, ep int
		want       string
	}{
		{1, 1, "Breaking.Bad.S01E01.720p.WEB-DL.mkv"},
		{1, 2, "Breaking.Bad.S01E02E03.1080p.mp4"},
		{1, 3, "Breaking.Bad.S01E02E03.1080p.mp4"},
		{1, 4, ""},
		{2, 1, ""},
	} {
		streams, err := p.Streams(ctx, episode(tc.season, tc.ep))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(streams) == 1 {
			got = streams[0].Filename
		}
		if got != tc.want || len(streams) > 1 {
			t.Errorf("S%02dE%02d: %+v, want %q", tc.season, tc.ep, streams, tc.want)
		}
	}
	if s, _ := p.Streams(ctx, episode(1, 1)); s[0].Quality != "720p" || !s[0].NotWebReady || s[0].URL != srv.URL+"/Videos/e1/stream?MediaSourceId=e1s&static=true" {
		t.Errorf("S01E01: %+v", s[0])
	}
	if !strings.Contains(srv.requests(), "/Shows/s1/Episodes?") {
		t.Errorf("episodes not asked:\n%s", srv.requests())
	}
}

// Plex: the libraries listed with their guids, a show's seasons and
// episodes, each media part a link.
func TestPlex(t *testing.T) {
	srv := newFake(t, Plex, "")
	p := newProvider(t, Config{Kind: Plex, Name: "Den", URL: srv.URL, Token: token})
	ctx := context.Background()
	if p.Name() != "Den" || p.Kind() != providers.KindMediaServer || !p.Supports(providers.TypeTV) {
		t.Fatalf("provider %s %s", p.Name(), p.Kind())
	}

	streams, err := p.Streams(ctx, movie(603, ""))
	if err != nil || len(streams) != 2 {
		t.Fatalf("%+v %v", streams, err)
	}
	noToken(t, streams)
	if s := streams[0]; s.URL != srv.URL+"/library/parts/1001/1600000000/file.mkv" || s.Quality != "2160p" || s.Type != providers.StreamMKV ||
		s.Filename != "The.Matrix.1999.2160p.WEB-DL.mkv" || s.Size != 20000000000 || !strings.Contains(s.Title, "HEVC · HDR · E-AC-3 5.1") {
		t.Errorf("4K: %+v", s)
	}
	if s := streams[1]; s.Quality != "720p" || s.Type != providers.StreamMP4 || s.NotWebReady {
		t.Errorf("720p: %+v", s)
	}
	if s, err := p.Streams(ctx, movie(10331, "tt0063350")); err != nil || len(s) != 1 || s[0].Quality != "480p" {
		t.Fatalf("by IMDb id: %+v %v", s, err)
	}
	if s, err := p.Streams(ctx, movie(5, "")); err != nil || len(s) != 0 {
		t.Fatalf("missing movie: %+v %v", s, err)
	}

	s, err := p.Streams(ctx, episode(1, 2))
	if err != nil || len(s) != 1 || s[0].URL != srv.URL+"/library/parts/2002/1600000101/file.mkv" || s[0].Quality != "720p" || !s[0].NotWebReady {
		t.Fatalf("S01E02: %+v %v", s, err)
	}
	if s, err := p.Streams(ctx, episode(2, 1)); err != nil || len(s) != 0 {
		t.Fatalf("S02E01: %+v %v", s, err)
	}
	if strings.Contains(srv.requests(), "/library/sections/3/") {
		t.Errorf("the music library was listed:\n%s", srv.requests())
	}
}

// The Test button: the server's name and version with the token, and the
// user searched; a wrong or missing token says so.
func TestCheck(t *testing.T) {
	ctx := context.Background()
	jf := newFake(t, Jellyfin, "")
	info, err := Check(ctx, Config{Kind: Jellyfin, URL: jf.URL, Token: token})
	if err != nil || info.Name != "Home" || info.Version != "10.10.3" || info.UserID != "u1" || info.UserName != "me" {
		t.Fatalf("jellyfin: %+v %v", info, err)
	}
	emby := newFake(t, Emby, "")
	if info, err := Check(ctx, Config{Kind: Emby, URL: emby.URL, Token: token}); err != nil || info.UserID != "" {
		t.Fatalf("emby API key: %+v %v", info, err)
	}
	if _, err := Check(ctx, Config{Kind: Emby, URL: emby.URL, Token: token, UserID: "nobody"}); err == nil {
		t.Fatal("an unknown user")
	}
	if _, err := Check(ctx, Config{Kind: Jellyfin, URL: jf.URL, Token: "wrong"}); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := Check(ctx, Config{Kind: Jellyfin, URL: jf.URL}); err == nil {
		t.Fatal("no token")
	}
	px := newFake(t, Plex, "")
	if info, err := Check(ctx, Config{Kind: Plex, URL: px.URL, Token: token}); err != nil || info.Name != "Plexy" || info.Libraries != 2 {
		t.Fatalf("plex: %+v %v", info, err)
	}
	if _, err := Check(ctx, Config{Kind: "kodi", URL: px.URL, Token: token}); err == nil {
		t.Fatal("unknown kind")
	}
	if _, err := Check(ctx, Config{Kind: Plex, URL: "ftp://x", Token: token}); err == nil {
		t.Fatal("not a web address")
	}
}

// The token goes with the servers' video links only: not their API, not
// another address, not a path that could lead elsewhere on the server.
func TestCredentials(t *testing.T) {
	servers := []Config{
		{Kind: Jellyfin, URL: "http://nas:8096", Token: "jf"},
		{Kind: Plex, URL: "https://plex.example:32400/", Token: "px"},
		{Kind: Emby, URL: "http://192.168.1.5:8096", Token: "em"},
		{Kind: Jellyfin, URL: "https://home.example/jellyfin", Token: "home"},
	}
	jf := map[string]string{"Authorization": `MediaBrowser Token="jf"`}
	for _, tc := range []struct {
		link string
		want map[string]string
	}{
		{"http://nas:8096/Videos/abc/stream?static=true&MediaSourceId=x", jf},
		{"http://NAS:8096/videos/abc/stream.mkv", jf},
		{"http://nas:8096/Users/u1/Items", nil},
		{"http://nas:8096/System/Info", nil},
		{"http://nas:8096/Videos/abc/stream/../../System/Info", nil},
		{"http://nas:8096/Videos/a/b/stream", nil},
		{"http://nas:8096//Videos/abc/stream", nil},
		{"http://other:8096/Videos/abc/stream", nil},
		{"https://nas:8096/Videos/abc/stream", nil},
		{"https://plex.example:32400/library/parts/1001/1600000000/file.mkv", map[string]string{"X-Plex-Token": "px"}},
		{"https://plex.example/library/parts/1001/1600000000/file.mkv", nil},
		{"https://plex.example:32400/library/sections", nil},
		{"https://plex.example:32400/library/parts/1/2/file.mkv/../../../../sections", nil},
		{"http://192.168.1.5:8096/emby/Videos/x/stream", map[string]string{"X-Emby-Token": "em"}},
		{"https://home.example/jellyfin/Videos/x/stream", map[string]string{"Authorization": `MediaBrowser Token="home"`}},
		{"https://home.example/Videos/x/stream", nil},
	} {
		u, _ := url.Parse(tc.link)
		got := Credentials(servers, u)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %v, want %v", tc.link, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: %v, want %v", tc.link, got, tc.want)
			}
		}
	}
	if hosts := strings.Join(Hosts(servers), ","); hosts != "nas:8096,plex.example:32400,192.168.1.5:8096,home.example:443" {
		t.Errorf("hosts = %s", hosts)
	}
}

func TestConfigNames(t *testing.T) {
	for _, tc := range []struct {
		c        Config
		id, name string
	}{
		{Config{Kind: Jellyfin, URL: "http://NAS:8096/"}, "jellyfin:nas:8096", "Jellyfin (nas)"},
		{Config{Kind: Plex, Name: "Den", URL: "https://plex.example:32400"}, "plex:plex.example:32400", "Den"},
		{Config{Kind: Emby, URL: "https://home.example/emby"}, "emby:home.example/emby", "Emby (home.example)"},
	} {
		if id, name := tc.c.ID(), tc.c.DisplayName(); id != tc.id || name != tc.name {
			t.Errorf("%+v: %q %q, want %q %q", tc.c, id, name, tc.id, tc.name)
		}
	}
}

func TestFileQuality(t *testing.T) {
	for _, tc := range []struct {
		f    fileInfo
		want string
	}{
		{fileInfo{width: 3840, height: 1600}, "2160p"},
		{fileInfo{width: 1920, height: 800}, "1080p"},
		{fileInfo{width: 1280, height: 534}, "720p"},
		{fileInfo{height: 576}, "576p"},
		{fileInfo{resolution: "4k"}, "2160p"},
		{fileInfo{resolution: "1080"}, "1080p"},
		{fileInfo{resolution: "sd"}, "480p"},
		{fileInfo{}, ""},
	} {
		if got := tc.f.quality(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.f, got, tc.want)
		}
	}
}
