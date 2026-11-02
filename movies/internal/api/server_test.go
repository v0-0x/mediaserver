package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/update"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir()) // the runtime dir on Windows
	// Tests never reach the real TMDB (a key set in a test makes the app
	// fetch the Discover page in the background): without a stand-in, it's
	// an address nothing answers at.
	if os.Getenv("MOVIKO_TMDB_URL") == "" {
		dead := httptest.NewServer(http.NotFoundHandler())
		dead.Close()
		t.Setenv("MOVIKO_TMDB_URL", dead.URL)
	}
	// Not t.TempDir: a database write still finishing as the app shuts down
	// can add a file (SQLite's -wal) while the folder is removed, so its
	// removal is tried again (after Shutdown, registered below).
	dir, err := os.MkdirTemp("", "moviko-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 20 && os.RemoveAll(dir) != nil; i++ {
			time.Sleep(50 * time.Millisecond)
		}
	})
	a, err := app.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	return New(a, fstest.MapFS{"index.html": {Data: []byte("<html>moviko</html>")}})
}

func do(s *Server, method, path, remote string, body string, mod func(r *http.Request)) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "http://127.0.0.1:43311"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, "http://127.0.0.1:43311"+path, nil)
		if method == http.MethodPost {
			// As the app sends them (api.post).
			r.Header.Set("Content-Type", "application/json")
		}
	}
	r.RemoteAddr = remote
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(w, r)
	return w
}

func TestClosedNetwork(t *testing.T) {
	s := newTestServer(t)
	local, lan, public := "127.0.0.1:5000", "192.168.1.20:5000", "8.8.8.8:5000"

	if w := do(s, "GET", "/api/status", public, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("public client: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("LAN client with LAN disabled: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("local client: got %d", w.Code)
	}
	// DNS rebinding: a public hostname pointing at 127.0.0.1
	if w := do(s, "GET", "/api/status", local, "", func(r *http.Request) { r.Host = "evil.example.com" }); w.Code != http.StatusForbidden {
		t.Fatalf("rebinding host: got %d", w.Code)
	}
	// CSRF from another website
	if w := do(s, "POST", "/api/library/scan", local, "{}", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.com") }); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: got %d", w.Code)
	}

	// Web UI off: browsers are refused, the desktop window (shell token) isn't.
	cfg := s.app.Settings.Get()
	cfg.Server.WebUI = false
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/api/status", local, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("browser with web UI off: got %d", w.Code)
	}
	shell := func(r *http.Request) { r.Header.Set("X-KumoBox-Shell", s.app.ShellToken) }
	if w := do(s, "GET", "/api/status", local, "", shell); w.Code != http.StatusOK {
		t.Fatalf("desktop window: got %d", w.Code)
	}

	// LAN with password
	cfg.Server.WebUI = true
	cfg.Server.AllowLAN = true
	cfg.Server.Password = "hunter2"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/api/status", public, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("public client with LAN on: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("LAN without password: got %d", w.Code)
	}
	if w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"wrong"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", w.Code)
	}
	w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"hunter2"}`, nil)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
		t.Fatalf("login: got %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }
	w = do(s, "GET", "/api/status", lan, "", withCookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"client":"lan"`) {
		t.Fatalf("LAN with cookie: %d %s", w.Code, w.Body.String()[:80])
	}
	// LAN devices can't change which programs run on this PC.
	next := s.app.Settings.Get()
	next.Mpv.Path = "/bin/sh"
	next.UI.AccentColor = "#22c3a6"
	raw, _ := json.Marshal(next)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), withCookie); w.Code != http.StatusOK {
		t.Fatalf("LAN settings save: %d", w.Code)
	}
	got := s.app.Settings.Get()
	if got.Mpv.Path == "/bin/sh" || got.UI.AccentColor != "#22c3a6" {
		t.Fatalf("LAN settings filter: mpv=%q accent=%q", got.Mpv.Path, got.UI.AccentColor)
	}
	if w := do(s, "GET", "/api/fs/dirs?path=/", lan, "", withCookie); w.Code != http.StatusForbidden {
		t.Fatalf("LAN dir browsing: got %d", w.Code)
	}
}

func TestCrossSiteRequests(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	set := func(kv ...string) func(r *http.Request) {
		return func(r *http.Request) {
			for i := 0; i+1 < len(kv); i += 2 {
				r.Header.Set(kv[i], kv[i+1])
			}
		}
	}

	// Our own pages work.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "http://127.0.0.1:43311", "Sec-Fetch-Site", "same-origin")); w.Code == http.StatusForbidden {
		t.Fatalf("same-origin POST refused: %s", w.Body.String())
	}
	// Sandboxed frames / file: pages send Origin: null.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "null")); w.Code != http.StatusForbidden {
		t.Fatalf("Origin null POST: got %d", w.Code)
	}
	// Another local web app (different port) is a different origin.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "http://127.0.0.1:8080")); w.Code != http.StatusForbidden {
		t.Fatalf("other-port POST: got %d", w.Code)
	}
	// Websites can't load API resources or navigate to them.
	if w := do(s, "GET", "/api/settings", local, "", set("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site GET: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/proxy?u=aHR0cHM6Ly9leGFtcGxlLmNvbS8", local, "", set("Sec-Fetch-Site", "same-site", "Sec-Fetch-Dest", "document")); w.Code != http.StatusForbidden {
		t.Fatalf("same-site proxy navigation: got %d", w.Code)
	}
	// ...but may link to the app itself.
	if w := do(s, "GET", "/", local, "", set("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Dest", "document")); w.Code != http.StatusOK {
		t.Fatalf("cross-site link to the app: got %d", w.Code)
	}
	// Proxied/cached remote content is never opened as a page on our origin.
	w := do(s, "GET", "/api/proxy?u=aHR0cHM6Ly9leGFtcGxlLmNvbS8", local, "", set("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "document"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("proxy navigation: got %d csp=%q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	// A JSON body sent as text/plain (no CORS preflight) is refused.
	r := httptest.NewRequest("PUT", "http://127.0.0.1:43311/api/settings", strings.NewReader(`{"ui":{"accentColor":"#000000"}}`))
	r.RemoteAddr = local
	r.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("text/plain JSON body: got %d", rec.Code)
	}
}

// Connections from this computer only take names that always mean this
// computer: a local network name (.local, .lan, a single label, a
// tailnet's .ts.net...) could be pointed at 127.0.0.1 by another device and
// make its page one of ours. Devices of the network may still use them.
func TestLoopbackHostNames(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	withHost := func(host string) func(r *http.Request) {
		return func(r *http.Request) {
			r.Host = host
			r.Header.Set("Origin", "http://"+host)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}
	}
	for _, host := range []string{"evil.local:43311", "attacker:43311", "box.lan:43311", "pc.tail1234.ts.net", "a.home.arpa:43311", "LOCALHOST.:43311"} {
		next := s.app.Settings.Get()
		next.Mpv.Path = "/tmp/not-mpv"
		raw, _ := json.Marshal(next)
		if w := do(s, "PUT", "/api/settings", "127.0.0.1:5000", string(raw), withHost(host)); w.Code != http.StatusForbidden {
			t.Errorf("local connection with Host %s: got %d", host, w.Code)
		}
		if got := s.app.Settings.Get().Mpv.Path; got == "/tmp/not-mpv" {
			t.Fatalf("Host %s changed the settings", host)
		}
	}
	for _, host := range []string{"127.0.0.1:43311", "localhost:43311", "[::1]:43311", "127.0.1.1:43311"} {
		if w := do(s, "GET", "/api/status", "127.0.0.1:5000", "", withHost(host)); w.Code != http.StatusOK {
			t.Errorf("local connection with Host %s: got %d", host, w.Code)
		}
	}
	if name, _ := os.Hostname(); name != "" {
		name, _, _ = strings.Cut(name, ".")
		if w := do(s, "GET", "/api/status", "127.0.0.1:5000", "", withHost(name+":43311")); w.Code != http.StatusOK {
			t.Errorf("local connection with this computer's name %s: got %d", name, w.Code)
		}
	}
	// Devices of the network open Moviko by the computer's local name.
	was := machineName
	machineName = func() string { return "mypc" }
	t.Cleanup(func() { machineName = was })
	for _, host := range []string{"mypc.local:43311", "mypc:43311", "mypc.tail1234.ts.net:43311"} {
		if w := do(s, "GET", "/api/status", "192.168.1.20:5000", "", withHost(host)); w.Code != http.StatusOK {
			t.Errorf("LAN device with Host %s: got %d", host, w.Code)
		}
	}
}

// Password attempts are answered one at a time: attempts sent side by side
// don't each get their own second, they wait or are told to come back.
func TestServerLoginOneAttemptAtATime(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, "hunter2"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"wrong"}`, nil)
			mu.Lock()
			codes[w.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[http.StatusUnauthorized] > 2 || codes[http.StatusTooManyRequests] < 18 {
		t.Fatalf("20 attempts at once were all checked: %v", codes)
	}
	// Once they're answered, the right password gets in.
	if w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"hunter2"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("login after the attempts: %d %s", w.Code, w.Body.String())
	}
	if passwordEq("hunter2", "hunter") || !passwordEq("hunter2", "hunter2") {
		t.Fatal("passwordEq")
	}
}

// A request the browsers' middleware didn't see (another Moviko's) isn't
// this computer's.
func TestUnclassifiedRequestNotTrusted(t *testing.T) {
	if isTrusted(httptest.NewRequest("GET", "/api/peer/files", nil)) {
		t.Fatal("a request with no client kind is trusted")
	}
}

// A plugin's permissions (files it may read and write, commands it may
// run) are granted on this computer, as plugins are installed there.
func TestPluginGrantOnlyFromThisComputer(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "POST", "/api/extensions/some-plugin/grant", "192.168.1.20:5000", "", nil); w.Code != http.StatusForbidden {
		t.Errorf("LAN grant: %d %s", w.Code, w.Body.String())
	}
	// From this computer it's only refused for not being installed.
	if w := do(s, "POST", "/api/extensions/some-plugin/grant", "127.0.0.1:5000", "", nil); w.Code == http.StatusForbidden || !strings.Contains(w.Body.String(), "not installed") {
		t.Errorf("local grant: %d %s", w.Code, w.Body.String())
	}
}

// Devices on the LAN can't change where torrents are searched (a Torznab
// indexer may be on the LAN, a mirror could be their own server, getting
// the API keys) nor where provider code and addon collections come from;
// the indexers' API keys stay hidden from them.
func TestLANCantChangeSourcesOrProviders(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	cfg.TorrentSources.Torznab = []config.TorznabIndexer{{Name: "Prowlarr", URL: "http://127.0.0.1:9696/1/api", APIKey: "indexer-key", Enabled: true}}
	cfg.Providers.NuvioRepos = []string{"https://repo.example/manifest.json"}
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	evil := s.app.Settings.Get()
	evil.TorrentSources.Torznab = []config.TorznabIndexer{{Name: "Mine", URL: "http://192.168.1.20:9696/api", Enabled: true}}
	evil.TorrentSources.EZTV.URL = "http://192.168.1.20:8080"
	evil.Providers.StremioCatalogs = []string{"https://evil.example/collection.json"}
	evil.Providers.NuvioRepos = []string{"https://evil.example/manifest.json"}
	raw, _ := json.Marshal(evil)
	w := do(s, "PUT", "/api/settings", lan, string(raw), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "indexer-key") {
		t.Fatal("LAN device received the indexer's API key")
	}
	got := s.app.Settings.Get()
	if len(got.TorrentSources.Torznab) != 1 || got.TorrentSources.Torznab[0].URL != "http://127.0.0.1:9696/1/api" || got.TorrentSources.Torznab[0].APIKey != "indexer-key" ||
		got.TorrentSources.EZTV.URL != "" || fmt.Sprint(got.Providers.StremioCatalogs) != fmt.Sprint([]string{config.DefaultStremioCatalog}) ||
		fmt.Sprint(got.Providers.NuvioRepos) != "[https://repo.example/manifest.json]" {
		t.Fatalf("LAN device changed the sources or providers: %+v %+v", got.TorrentSources, got.Providers)
	}
	if w := do(s, "GET", "/api/settings", lan, "", nil); strings.Contains(w.Body.String(), "indexer-key") {
		t.Fatal("LAN device can read the indexer's API key")
	}
	// Nor keep or forget Nuvio repositories.
	if w := do(s, "POST", "/api/providers/nuvio/repos", lan, `{"url":"https://evil.example/manifest.json"}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("LAN keeps a Nuvio repository: %d", w.Code)
	}
	if w := do(s, "DELETE", "/api/providers/nuvio/repos?url="+url.QueryEscape("https://repo.example/manifest.json"), lan, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("LAN forgets a Nuvio repository: %d", w.Code)
	}
	if got := s.app.Settings.Get().Providers.NuvioRepos; fmt.Sprint(got) != "[https://repo.example/manifest.json]" {
		t.Errorf("repositories %v", got)
	}
}

// The key a Torznab feed's URL carries is hidden from devices on the LAN
// like the indexer's API key field, and they can't change the extension
// marketplace (where this computer installs and updates extensions from).
func TestLANKeysInURLsAndMarketplace(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	feed := "http://127.0.0.1:9117/api/v2.0/indexers/all/results/torznab/api?apikey=indexer-key&cat=2000&Passkey=tracker-pass"
	cfg.TorrentSources.Torznab = []config.TorznabIndexer{{Name: "Jackett", URL: feed, Enabled: true}}
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	for _, path := range []string{"/api/settings", "/api/status"} {
		body := do(s, "GET", path, lan, "", nil).Body.String()
		if strings.Contains(body, "indexer-key") || strings.Contains(body, "tracker-pass") || !strings.Contains(body, "cat=2000") {
			t.Errorf("%s shows the LAN device %s", path, body[strings.Index(body, `"torznab"`):][:200])
		}
	}
	if !strings.Contains(do(s, "GET", "/api/settings", "127.0.0.1:5000", "", nil).Body.String(), "indexer-key") {
		t.Error("this computer doesn't see the feed's key")
	}

	evil := s.app.Settings.Get()
	evil.Extensions.MarketplaceURL = "https://evil.example/market.json"
	evil.Extensions.AutoUpdate = true
	evil.UI.AccentColor = "#22c3a6"
	raw, _ := json.Marshal(settingsFor(httptest.NewRequest("GET", "/", nil).WithContext(context.WithValue(context.Background(), ctxKey{}, clientLAN)), evil))
	if w := do(s, "PUT", "/api/settings", lan, string(raw), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	}
	got := s.app.Settings.Get()
	if got.Extensions.MarketplaceURL != config.DefaultMarketplaceURL || got.Extensions.AutoUpdate || got.UI.AccentColor != "#22c3a6" {
		t.Errorf("LAN save: extensions %+v, accent %q", got.Extensions, got.UI.AccentColor)
	}
	// Saving the settings it was shown keeps the feed's key.
	if got.TorrentSources.Torznab[0].URL != feed {
		t.Errorf("feed URL after a LAN save: %q", got.TorrentSources.Torznab[0].URL)
	}
}

// Settings slices must not be shared with the live settings: decoding a
// LAN device's request used to write its extraDirs straight into them.
func TestSettingsSlicesNotShared(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Library.ExtraDirs = []string{"/srv/anime-a", "/srv/anime-b"}
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	cfg.Qbittorrent.Password = "secret"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	evil := s.app.Settings.Get()
	evil.Library.ExtraDirs = []string{"/home/u/.ssh", "/etc"}
	evil.Qbittorrent.Host = "203.0.113.9"
	raw, _ := json.Marshal(evil)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	} else if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("LAN device received the qBittorrent password")
	}
	got := s.app.Settings.Get()
	if got.Library.ExtraDirs[0] != "/srv/anime-a" || got.Library.ExtraDirs[1] != "/srv/anime-b" || got.Qbittorrent.Host == "203.0.113.9" {
		t.Fatalf("LAN device changed protected settings: %q %q", got.Library.ExtraDirs, got.Qbittorrent.Host)
	}
	if w := do(s, "GET", "/api/settings", lan, "", nil); strings.Contains(w.Body.String(), "secret") {
		t.Fatal("LAN device can read the qBittorrent password")
	}
	// A copy from Get can be changed freely.
	c := s.app.Settings.Get()
	c.Library.ExtraDirs[0] = "/changed"
	if s.app.Settings.Get().Library.ExtraDirs[0] != "/srv/anime-a" {
		t.Fatal("Get returned a slice shared with the live settings")
	}
}

// Devices on the LAN don't read this computer's server log, nor the
// update's build log and commands from the event stream.
func TestLANLogsAndUpdateEvents(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	if w := do(s, "GET", "/api/logs", lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("LAN logs: %d", w.Code)
	}
	if w := do(s, "GET", "/api/logs", "127.0.0.1:5000", "", nil); w.Code != http.StatusOK {
		t.Fatalf("local logs: %d", w.Code)
	}

	msg, _ := json.Marshal(events.Event{Type: events.UpdateStatus, Payload: update.Status{
		Available: true, CanApply: true, ManualCommand: "sudo pacman -U /home/me/moviko.pkg.tar.zst", Log: []string{"building in /home/me/..."},
	}})
	// A request the middleware didn't classify counts as the LAN's.
	r := httptest.NewRequest("GET", "/api/events", nil)
	out := string(eventFor(r, msg))
	if strings.Contains(out, "pacman") || strings.Contains(out, "/home/me") || !strings.Contains(out, `"available":true`) {
		t.Fatalf("LAN update event: %s", out)
	}
	if other := []byte(`{"type":"collection-updated"}`); string(eventFor(r, other)) != string(other) {
		t.Fatal("other events changed")
	}
}

// TestUIFramingAndCSP checks the web UI page can't be framed by other sites
// (clickjacking) and carries a Content-Security-Policy that still permits the
// resources the app and its plugin webviews rely on.
func TestUIFramingAndCSP(t *testing.T) {
	s := newTestServer(t)
	w := do(s, "GET", "/", "127.0.0.1:5000", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("serving the UI: got %d", w.Code)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("the UI page has no Content-Security-Policy")
	}
	// Clickjacking and injection-narrowing directives must be present and strict.
	for _, want := range []string{"frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	// ...without blocking what the app needs: inline styles/scripts (plugin
	// css nodes and the sandboxed plugin webviews that inherit this policy),
	// remote media/images, and frames for the trailer and plugin webviews.
	for _, want := range []string{"'unsafe-inline'", "media-src", "frame-src", "blob:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP too strict, missing %q: %s", want, csp)
		}
	}
}

// The names a request may reach this computer by: its own, not one a
// website controls (DNS rebinding), even a home network's or Tailscale's.
func TestHostNames(t *testing.T) {
	was := machineName
	machineName = func() string { return "mypc" }
	t.Cleanup(func() { machineName = was })
	for host, ok := range map[string]bool{
		"127.0.0.1:43311": true, "[::1]:43311": true, "192.168.1.20": true, "localhost": true,
		"mypc": true, "MyPC.local": true, "mypc.lan:43311": true, "mypc.home.arpa": true, "mypc.tail1234.ts.net": true, "mypc.local.": true,
		"attacker": false, "evil.ts.net": false, "evil.tail1234.ts.net": false, "nas.lan": false, "mypc.evil.com": false,
		"evil.example.com": false, "mypc.a.b.ts.net": false, "localhost.evil.com": false,
	} {
		if hostAllowed(host) != ok {
			t.Errorf("%q: allowed %v", host, !ok)
		}
	}
}

// Other websites can't make this computer do things, even through a
// browser that doesn't say where a request comes from; the app can.
func TestNoCrossSitePosts(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	form := func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }
	if w := do(s, "POST", "/api/downloads/clear", local, "x", form); w.Code != http.StatusForbidden {
		t.Errorf("a form post with no origin: %d", w.Code)
	}
	if w := do(s, "POST", "/api/downloads/clear", local, "x", func(r *http.Request) { form(r); r.Header.Set("Sec-Fetch-Site", "same-origin") }); w.Code != http.StatusOK {
		t.Errorf("the app's own: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/downloads/clear", local, "{}", nil); w.Code != http.StatusOK {
		t.Errorf("JSON: %d %s", w.Code, w.Body)
	}
	// Nor show its pages in a frame.
	if w := do(s, "GET", "/settings", local, "", nil); w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("page headers: %v", w.Header())
	}
}
