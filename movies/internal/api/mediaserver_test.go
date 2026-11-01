package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// fakeJellyfin is a Jellyfin server on this computer with The Dark Knight
// (one 1080p MP4), refusing requests without its API key.
type fakeJellyfin struct {
	*httptest.Server
	mu sync.Mutex
	// tokens are the tokens each path was asked with.
	tokens map[string]string
}

const jellyfinKey = "jf-api-key"

func newFakeJellyfin(t *testing.T) *fakeJellyfin {
	t.Helper()
	f := &fakeJellyfin{tokens: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f.mu.Lock()
		f.tokens[r.URL.Path] = auth
		f.mu.Unlock()
		if auth != `MediaBrowser Token="`+jellyfinKey+`"` {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/System/Info":
			_, _ = io.WriteString(w, `{"ServerName":"Living room","Version":"10.10.3"}`)
		case r.URL.Path == "/Users/Me":
			http.Error(w, "an API key has no user", http.StatusBadRequest)
		case r.URL.Path == "/Items" && r.URL.Query().Get("Ids") == "dk":
			_, _ = io.WriteString(w, `{"Items":[{"Id":"dk","Name":"The Dark Knight","ProviderIds":{"Tmdb":"155"},
				"MediaSources":[{"Id":"src","Path":"/movies/The Dark Knight (2008).mp4","Container":"mp4","Size":3000,
				"MediaStreams":[{"Type":"Video","Codec":"h264","Width":1920,"Height":1080},{"Type":"Audio","Codec":"aac","Channels":2}]}]}]}`)
		case r.URL.Path == "/Items":
			_, _ = io.WriteString(w, `{"Items":[{"Id":"dk","Name":"The Dark Knight","Type":"Movie","ProviderIds":{"Tmdb":"155","Imdb":"tt0468569"}}]}`)
		case r.URL.Path == "/Videos/dk/stream":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "video bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeJellyfin) tokenFor(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens[path]
}

func addJellyfin(t *testing.T, s *Server, f *fakeJellyfin) {
	t.Helper()
	cfg := s.app.Settings.Get()
	cfg.Providers.MediaServers = []config.MediaServer{{Kind: "jellyfin", Name: "Living room", URL: f.URL + "/", Token: jellyfinKey}}
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// A media server's movies stream like any provider's, without its token in
// the links; the stream proxy (a server on this computer: allowed, as it's
// configured) adds the token for its videos only.
func TestMediaServerStreams(t *testing.T) {
	s := newProvidersServer(t)
	jf := newFakeJellyfin(t)
	addJellyfin(t, s, jf)
	if got := s.app.Settings.Get().Providers.MediaServers[0].URL; got != jf.URL {
		t.Fatalf("saved URL %q", got)
	}

	var res streamsResponse
	decodeBody(t, do(s, "GET", "/api/providers/streams?mediaId=155", lan, "", nil), &res)
	if len(res.Streams) != 1 || res.Streams[0].Provider != "jellyfin:"+strings.TrimPrefix(jf.URL, "http://") || res.Streams[0].ProviderName != "Living room" ||
		res.Streams[0].Quality != "1080p" || res.Streams[0].Kind != stream.KindDirect {
		t.Fatalf("streams: %+v", res)
	}
	link := res.Streams[0].URL
	if strings.Contains(link, jellyfinKey) || len(res.Streams[0].Headers) != 0 {
		t.Fatalf("the token is in the stream: %+v", res.Streams[0])
	}

	// Played in the app (from the LAN too): the sources carry no token.
	w := do(s, "POST", "/api/providers/play", lan, jsonBody(t, map[string]any{"mediaId": 155, "player": "builtin"}), nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), jellyfinKey) {
		t.Fatalf("play: %d %s", w.Code, w.Body.String())
	}

	proxy := func(target string) *httptest.ResponseRecorder {
		return do(s, "GET", stream.ProxyURL(target, nil), lan, "", nil)
	}
	if w := proxy(link); w.Code != http.StatusOK || w.Body.String() != "video bytes" {
		t.Fatalf("proxied video: %d %s", w.Code, w.Body.String())
	}
	if got := jf.tokenFor("/Videos/dk/stream"); got != `MediaBrowser Token="`+jellyfinKey+`"` {
		t.Fatalf("the proxy sent %q", got)
	}
	// The server's API through the proxy: no token.
	if w := proxy(jf.URL + "/System/Info"); w.Code != http.StatusUnauthorized || jf.tokenFor("/System/Info") != "" {
		t.Fatalf("proxied API: %d, sent %q", w.Code, jf.tokenFor("/System/Info"))
	}
}

// The tokens stay on this computer: devices on the LAN don't see them, and
// neither change the servers nor test them.
func TestMediaServerSettings(t *testing.T) {
	s := newProvidersServer(t)
	jf := newFakeJellyfin(t)
	addJellyfin(t, s, jf)

	var cfg config.Settings
	decodeBody(t, do(s, "GET", "/api/settings", lan, "", nil), &cfg)
	if len(cfg.Providers.MediaServers) != 1 || cfg.Providers.MediaServers[0].Token != "" || cfg.Providers.MediaServers[0].URL != jf.URL {
		t.Fatalf("LAN settings: %+v", cfg.Providers.MediaServers)
	}
	if s.app.Settings.Get().Providers.MediaServers[0].Token != jellyfinKey {
		t.Fatal("redacting changed the saved settings")
	}
	decodeBody(t, do(s, "GET", "/api/settings", local, "", nil), &cfg)
	if cfg.Providers.MediaServers[0].Token != jellyfinKey {
		t.Fatalf("local settings: %+v", cfg.Providers.MediaServers)
	}
	// A LAN device saving the settings doesn't change them.
	cfg.Providers.MediaServers = []config.MediaServer{{Kind: "plex", URL: "http://192.168.1.9:32400", Token: "x"}}
	if w := do(s, "PUT", "/api/settings", lan, jsonBody(t, cfg), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	}
	if got := s.app.Settings.Get().Providers.MediaServers; len(got) != 1 || got[0].Token != jellyfinKey {
		t.Fatalf("changed from the LAN: %+v", got)
	}

	body := map[string]any{"kind": "jellyfin", "url": jf.URL, "token": jellyfinKey}
	if w := do(s, "POST", "/api/providers/mediaservers/test", lan, jsonBody(t, body), nil); w.Code != http.StatusForbidden {
		t.Fatalf("test from the LAN: %d", w.Code)
	}
	var info struct{ Name, Version, UserID string }
	decodeBody(t, do(s, "POST", "/api/providers/mediaservers/test", local, jsonBody(t, body), nil), &info)
	if info.Name != "Living room" || info.Version != "10.10.3" || info.UserID != "" {
		t.Fatalf("test: %+v", info)
	}
	// Without the token: the saved one at that address.
	decodeBody(t, do(s, "POST", "/api/providers/mediaservers/test", local, jsonBody(t, map[string]any{"kind": "jellyfin", "url": jf.URL + "/"}), nil), &info)
	if info.Name != "Living room" {
		t.Fatalf("test with the saved token: %+v", info)
	}
	w := do(s, "POST", "/api/providers/mediaservers/test", local, jsonBody(t, map[string]any{"kind": "jellyfin", "url": jf.URL, "token": "wrong"}), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "refused the token") {
		t.Fatalf("wrong token: %d %s", w.Code, w.Body.String())
	}

	// Listed with the providers, first.
	var infos []struct{ ID, Kind string }
	decodeBody(t, do(s, "GET", "/api/providers", lan, "", nil), &infos)
	u, _ := url.Parse(jf.URL)
	if len(infos) == 0 || infos[0].ID != "jellyfin:"+u.Host || infos[0].Kind != "mediaserver" {
		t.Fatalf("providers: %+v", infos)
	}
}
