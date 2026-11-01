package api

import (
	"encoding/json"
	"github.com/v0-0x/mediaserver/anime/internal/awake"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/app"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir()) // the runtime dir on Windows
	// Not t.TempDir: a database write still finishing as the app shuts down
	// can add a file (SQLite's -wal) while the folder is removed, so its
	// removal is tried again (after Shutdown, registered below).
	dir, err := os.MkdirTemp("", "aniko-test-")
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
	return New(a, fstest.MapFS{"index.html": {Data: []byte("<html>aniko</html>")}})
}

func do(s *Server, method, path, remote string, body string, mod func(r *http.Request)) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "http://127.0.0.1:43211"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, "http://127.0.0.1:43211"+path, nil)
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
	shell := func(r *http.Request) { r.Header.Set("X-Kumo-Shell", s.app.ShellToken) }
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
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "http://127.0.0.1:43211", "Sec-Fetch-Site", "same-origin")); w.Code == http.StatusForbidden {
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
	r := httptest.NewRequest("PUT", "http://127.0.0.1:43211/api/settings", strings.NewReader(`{"ui":{"accentColor":"#000000"}}`))
	r.RemoteAddr = local
	r.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("text/plain JSON body: got %d", rec.Code)
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

// The names a request may reach this computer by: its own, not one a
// website controls (DNS rebinding), even a home network's or Tailscale's.
func TestHostNames(t *testing.T) {
	was := machineName
	machineName = func() string { return "mypc" }
	t.Cleanup(func() { machineName = was })
	for host, ok := range map[string]bool{
		"127.0.0.1:43211": true, "[::1]:43211": true, "192.168.1.20": true, "localhost": true,
		"mypc": true, "MyPC.local": true, "mypc.lan:43211": true, "mypc.home.arpa": true, "mypc.tail1234.ts.net": true, "mypc.local.": true,
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

// Devices on the LAN can't install or set up extensions (code that runs
// here), read their settings, or choose where they come from.
func TestExtensionsOnlyFromThisComputer(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/extensions/install", `{"manifestURI":"https://evil.example.com/m.json"}`},
		{"POST", "/api/extensions/x/enable", `{"enabled":true}`},
		{"POST", "/api/extensions/x/config", `{}`},
		{"POST", "/api/extensions/x/grant", `{}`},
		{"POST", "/api/extensions/x/update", `{}`},
		{"DELETE", "/api/extensions/x", ""},
	} {
		if w := do(s, c.method, c.path, lan, c.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s %s from the LAN: %d", c.method, c.path, w.Code)
		}
	}
	evil := s.app.Settings.Get()
	evil.Extensions.MarketplaceURL = "https://evil.example.com/market.json"
	evil.Anilist.ClientID = "666"
	raw, _ := json.Marshal(evil)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d", w.Code)
	}
	if got := s.app.Settings.Get(); got.Extensions.MarketplaceURL == evil.Extensions.MarketplaceURL || got.Anilist.ClientID == "666" {
		t.Errorf("a LAN device changed where extensions or logins go: %+v %+v", got.Extensions, got.Anilist)
	}
}

// Guessing the server password: an address that got it wrong 5 times in a
// row waits, the others don't.
func TestPasswordGuessing(t *testing.T) {
	var g loginGuard
	now := time.Now()
	for range maxWrong {
		if g.wait("192.168.1.30", now) > 0 {
			t.Fatal("waits before 5 wrong passwords")
		}
		g.failed("192.168.1.30", now)
	}
	if g.wait("192.168.1.30", now) == 0 {
		t.Error("no wait after 5 wrong passwords")
	}
	if g.wait("192.168.1.31", now) > 0 {
		t.Error("another address waits")
	}
	if g.wait("192.168.1.30", now.Add(2*time.Minute)) > 0 {
		t.Error("still waiting after the first minute")
	}
	g.failed("192.168.1.30", now.Add(2*time.Minute))
	if d := g.wait("192.168.1.30", now.Add(2*time.Minute)); d < 2*time.Minute-time.Second {
		t.Errorf("the next wait is %v, not longer", d)
	}
	g.passed("192.168.1.30")
	if g.wait("192.168.1.30", now.Add(2*time.Minute)) > 0 {
		t.Error("still waiting after the right password")
	}
}

// On a device where other apps reach 127.0.0.1 (Android), only the app's
// window, with its token, is answered there, whatever the web UI setting.
func TestAppOnly(t *testing.T) {
	s := newTestServer(t)
	s.AppOnly = true
	local := "127.0.0.1:5000"
	if w := do(s, "GET", "/api/settings", local, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("another app: %d", w.Code)
	}
	app := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "kumo_shell", Value: s.app.ShellToken}) }
	if w := do(s, "GET", "/api/settings", local, "", app); w.Code != http.StatusOK {
		t.Errorf("the app's window: %d", w.Code)
	}
}

// A torrent action names its torrents: with none, the clients would act on
// all of them (and "remove and delete files" delete every download).
func TestTorrentActionNeedsHashes(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	for _, body := range []string{`{"action":"removeData"}`, `{"action":"removeData","hashes":[]}`, `{"action":"removeData","hashes":["all"]}`, `{"action":"pause","hashes":["1"]}`} {
		if w := do(s, "POST", "/api/torrent-client/action", local, body, nil); w.Code != http.StatusBadRequest || !strings.Contains(errorOf(w), "torrent") {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

// The AniList account is the computer's: a device on the network can't log
// it out.
func TestLogoutOnlyHere(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "POST", "/api/auth/logout", "192.168.1.30:5000", "", nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "log out on the computer") {
		t.Fatalf("logout from the network: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/auth/logout", "127.0.0.1:5000", "", nil); w.Code != http.StatusOK {
		t.Fatalf("logout here: %d %s", w.Code, w.Body)
	}
}

// A device on the network playing this computer's file keeps it awake
// (until a while after); this computer's own player doesn't need to.
func TestStreamingKeepsAwake(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	starts := 0
	s.app.Awake = awake.WithFuncs(func() bool { return s.app.Settings.Get().Sharing.KeepAwake }, func() error { mu.Lock(); starts++; mu.Unlock(); return nil }, func() {})
	path, _ := addEpisode(t, s, 21, 1)
	get := func(from string) int {
		w := do(s, "GET", "/api/local/file?"+url.Values{"path": {path}}.Encode(), from, "", nil)
		return w.Code
	}
	if code := get("127.0.0.1:5000"); code != http.StatusOK {
		t.Fatalf("here: %d", code)
	}
	if starts != 0 || s.app.Awake.Active() {
		t.Fatal("kept awake for this computer's own player")
	}
	if code := get("192.168.1.30:5000"); code != http.StatusOK {
		t.Fatalf("from the network: %d", code)
	}
	mu.Lock()
	n := starts
	mu.Unlock()
	if n != 1 || !s.app.Awake.Active() {
		t.Fatalf("not kept awake for a device on the network: %d", n)
	}
	var st struct {
		KeptAwake bool `json:"keptAwake"`
	}
	if w := do(s, "GET", "/api/status", "127.0.0.1:5000", "", nil); json.Unmarshal(w.Body.Bytes(), &st) != nil || !st.KeptAwake {
		t.Errorf("status: %s", w.Body)
	}
	s.app.Awake.Close()
	// Turned off in Settings: not at all.
	cfg = s.app.Settings.Get()
	cfg.Sharing.KeepAwake = false
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	get("192.168.1.30:5000")
	if mu.Lock(); starts != 1 {
		t.Error("kept awake while turned off")
	}
	mu.Unlock()
}
