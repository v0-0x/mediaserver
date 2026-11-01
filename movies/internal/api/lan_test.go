package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/extensions"
)

// refusedURL is an address on this computer: KumoBox never downloads
// extensions, collections or repositories from there, so the requests of
// these tests that this computer may make fail without any network.
const refusedURL = "http://127.0.0.1:1/manifest.json"

// Devices on the LAN use providers, extensions and plugins; only this
// computer installs, sets up, turns on or off, orders, updates and removes
// them (and reads their logs).
func TestLANDoesntManageProviders(t *testing.T) {
	s := newProvidersServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/extensions/install", `{"manifestURI":"` + refusedURL + `"}`},
		{"POST", "/api/extensions/some.ext/enable", `{"enabled":true}`},
		{"POST", "/api/extensions/some.ext/config", `{"values":{"apiKey":"mine"}}`},
		{"POST", "/api/extensions/some.ext/update", ""},
		{"POST", "/api/extensions/some.ext/grant", ""},
		{"DELETE", "/api/extensions/some.ext", ""},
		{"GET", "/api/extensions/some.ext/logs", ""},
		{"GET", "/api/extensions/updates", ""},
		{"POST", "/api/extensions/marketplace/refresh", `{"url":"` + refusedURL + `"}`},
		{"POST", "/api/providers/stremio/addons", `{"url":"` + refusedURL + `"}`},
		{"DELETE", "/api/providers/stremio/addons?key=stremio:x", ""},
		{"PUT", "/api/providers/stremio/addons/enabled", `{"key":"stremio:x","enabled":false}`},
		{"PUT", "/api/providers/stremio/addons/order", `{"keys":["stremio:x"]}`},
		{"POST", "/api/providers/stremio/refresh", ""},
		{"POST", "/api/providers/stremio/catalog/refresh", `{"url":"` + refusedURL + `"}`},
		{"POST", "/api/providers/nuvio/repos", `{"url":"` + refusedURL + `"}`},
		{"DELETE", "/api/providers/nuvio/repos?url=" + url.QueryEscape(refusedURL), ""},
		{"POST", "/api/providers/nuvio/install", `{"repo":"` + refusedURL + `","id":"x"}`},
		{"POST", "/api/providers/nuvio/install-all", `{"repo":"` + refusedURL + `"}`},
	} {
		if w := do(s, tc.method, tc.path, lan, tc.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s %s from the LAN: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		// This computer may, refused at most for what the request is
		// about (nothing installed by that name, an address on this
		// computer...).
		if w := do(s, tc.method, tc.path, local, tc.body, nil); w.Code == http.StatusForbidden {
			t.Errorf("%s %s on this computer: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// What's installed is still theirs to see and use.
	for _, path := range []string{"/api/extensions", "/api/providers", "/api/providers/stremio/addons", "/api/plugins/ui"} {
		if w := do(s, "GET", path, lan, "", nil); w.Code != http.StatusOK {
			t.Errorf("GET %s from the LAN: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// Devices on the LAN see the marketplace, addon collections and Nuvio
// repositories of the settings, no others: they can't make KumoBox
// download (and keep in its cache) whatever they like.
func TestLANReadsConfiguredSourcesOnly(t *testing.T) {
	s := newProvidersServer(t)
	market, coll := "http://127.0.0.1:1/market.json", "http://127.0.0.1:1/collection.json"
	cfg := s.app.Settings.Get()
	cfg.Extensions.MarketplaceURL = market
	cfg.Providers.StremioCatalogs = []string{coll}
	cfg.Providers.NuvioRepos = []string{refusedURL}
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	other := url.QueryEscape("http://127.0.0.2:1/other.json")
	for _, tc := range []struct {
		path  string
		lanOK bool
	}{
		{"/api/extensions/marketplace", true},
		{"/api/extensions/marketplace?url=" + url.QueryEscape(market), true},
		{"/api/extensions/marketplace?url=" + other, false},
		{"/api/providers/stremio/catalog", true},
		{"/api/providers/stremio/catalog?url=" + url.QueryEscape(coll), true},
		{"/api/providers/stremio/catalog?url=" + other, false},
		{"/api/providers/nuvio/repo?url=" + url.QueryEscape(refusedURL), true},
		{"/api/providers/nuvio/repo?url=" + other, false},
	} {
		if w := do(s, "GET", tc.path, lan, "", nil); (w.Code == http.StatusForbidden) == tc.lanOK {
			t.Errorf("GET %s from the LAN: %d %s", tc.path, w.Code, w.Body.String())
		}
		if w := do(s, "GET", tc.path, local, "", nil); w.Code == http.StatusForbidden {
			t.Errorf("GET %s on this computer: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

// The values of an extension's settings (API keys, accounts) stay on this
// computer, which is where they're set.
func TestLANExtensionSettingsHidden(t *testing.T) {
	list := []extensions.Info{{
		Manifest:   &extensions.Manifest{ID: "some.ext"},
		UserConfig: extensions.SavedUserConfig{Version: 2, Values: map[string]string{"apiKey": "mine"}},
	}}
	for _, tc := range []struct {
		kind   clientKind
		hidden bool
	}{{clientShell, false}, {clientLocal, false}, {clientLAN, true}} {
		r := httptest.NewRequest("GET", "/api/extensions", nil).WithContext(context.WithValue(context.Background(), ctxKey{}, tc.kind))
		got := extensionsFor(r, list)
		if hidden := got[0].UserConfig.Values == nil; hidden != tc.hidden || got[0].UserConfig.Version != 2 {
			t.Errorf("client %d: %+v", tc.kind, got[0].UserConfig)
		}
	}
	if list[0].UserConfig.Values["apiKey"] != "mine" {
		t.Fatal("hiding the values changed the extension list")
	}
}

// GET requests only read: fetching a title or the providers' streams again,
// or asking the other KumoBoxes again, is a POST, which other websites
// can't send. Devices on the LAN may still, as they browse and play.
func TestRefreshesArePosts(t *testing.T) {
	s, tmdbHits := newCountingCatalogServer(t)
	cfg := s.app.Settings.Get()
	cfg.Providers.Archive, cfg.Server.AllowLAN = false, true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/media/155", "/api/media/155?refresh=1", "/api/media/155/refresh"} {
		do(s, "GET", path, local, "", nil)
	}
	if n := tmdbHits("/movie/155"); n != 1 {
		t.Fatalf("TMDB asked %d times for the title by GET requests", n)
	}
	var entry struct{ Media struct{ ID int } }
	decodeBody(t, do(s, "POST", "/api/media/155/refresh", lan, "{}", nil), &entry)
	if n := tmdbHits("/movie/155"); n != 2 || entry.Media.ID != 155 {
		t.Fatalf("refresh: TMDB asked %d times, %+v", n, entry)
	}

	addon := installTestAddon(t, s)
	asked := func() int {
		addon.mu.Lock()
		defer addon.mu.Unlock()
		n := 0
		for _, p := range addon.paths {
			if p == "/stream/movie/tt0468569.json" {
				n++
			}
		}
		return n
	}
	for _, path := range []string{"/api/providers/streams?mediaId=155", "/api/providers/streams?mediaId=155&refresh=1"} {
		if w := do(s, "GET", path, local, "", nil); w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if n := asked(); n != 1 {
		t.Fatalf("the addon was asked %d times by GET requests", n)
	}
	var res streamsResponse
	decodeBody(t, do(s, "POST", "/api/providers/streams/refresh", lan, jsonBody(t, map[string]any{"mediaId": 155}), nil), &res)
	if n := asked(); n != 2 || len(res.Streams) != 3 {
		t.Fatalf("refresh: asked %d times, %d streams", n, len(res.Streams))
	}

	// The refreshes have no GET form.
	for _, path := range []string{"/api/providers/streams/refresh", "/api/sharing/refresh", "/api/extensions/marketplace/refresh", "/api/providers/stremio/catalog/refresh"} {
		if w := do(s, "GET", path, local, "", nil); w.Code == http.StatusOK {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
	if w := do(s, "POST", "/api/sharing/refresh", lan, "{}", nil); w.Code != http.StatusOK {
		t.Errorf("sharing refresh from the LAN: %d %s", w.Code, w.Body.String())
	}
}

// Every method but GET and HEAD (which only read) has to come from
// KumoBox's own pages, for this computer's browsers and the LAN's alike:
// another origin, "null" (sandboxed frames, file: pages), a cross-site
// fetch or a host name another site could point here are refused.
func TestNonGetRequestsChecked(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	header := func(k, v string) func(r *http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, remote := range []string{local, lan} {
			for _, tc := range []struct {
				name    string
				mod     func(r *http.Request)
				refused bool
			}{
				{"same origin", header("Origin", "http://127.0.0.1:43311"), false},
				{"no origin (not a browser)", nil, false},
				{"another site", header("Origin", "https://evil.example"), true},
				{"another port", header("Origin", "http://127.0.0.1:8080"), true},
				{"null origin", header("Origin", "null"), true},
				{"cross-site fetch", header("Sec-Fetch-Site", "cross-site"), true},
				{"same-site fetch", header("Sec-Fetch-Site", "same-site"), true},
				{"another host name", func(r *http.Request) { r.Host = "evil.example.com" }, true},
			} {
				// No such route: what isn't refused is a 404.
				w := do(s, method, "/api/nothing-here", remote, "{}", tc.mod)
				if refused := w.Code == http.StatusForbidden; refused != tc.refused {
					t.Errorf("%s from %s, %s: %d %s", method, remote, tc.name, w.Code, w.Body.String())
				}
			}
		}
	}
}
