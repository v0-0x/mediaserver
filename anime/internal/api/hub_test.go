package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/v0-0x/mediaserver/anime/internal/app"
)

func newHubServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a, err := app.New(t.TempDir(), app.Options{Hub: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	return New(a, fstest.MapFS{"index.html": {Data: []byte("<html>aniko</html>")}})
}

// A hub's dashboard is its owner's: before the password is set (with the
// setup code), a browser on the network sees only that it's a hub; after,
// one signed in with it may do what the hub's own computer may.
func TestHubOwner(t *testing.T) {
	s := newHubServer(t)
	lan := "192.168.1.20:5000"
	cfg := s.app.Settings.Get()
	if !cfg.Sharing.Enabled || !cfg.Server.AllowLAN || !cfg.Server.WebUI {
		t.Fatalf("a hub shares with the network: %+v %+v", cfg.Sharing, cfg.Server)
	}

	w := do(s, "GET", "/api/status", lan, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"setup":true`) || strings.Contains(w.Body.String(), `"settings"`) {
		t.Fatalf("status before setup: %d %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/hub/overview", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("overview before setup: %d", w.Code)
	}
	if w := do(s, "GET", "/api/settings", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("settings before setup: %d", w.Code)
	}
	if w := do(s, "POST", "/api/hub/setup", lan, `{"code":"0000-0000","password":"hunter22"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", w.Code)
	}
	code := s.app.HubStore.SetupCode()
	if w := do(s, "POST", "/api/hub/setup", lan, `{"code":"`+code+`","password":"abc"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d", w.Code)
	}
	// Typed with a space instead of the dash: the same code.
	w = do(s, "POST", "/api/hub/setup", lan, `{"code":"`+strings.Replace(code, "-", " ", 1)+`","password":"hunter22"}`, nil)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	signedIn := func(r *http.Request) { r.AddCookie(cookie) }

	if w := do(s, "POST", "/api/hub/setup", lan, `{"code":"`+code+`","password":"another1"}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("setup again: %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("status not signed in: %d", w.Code)
	}
	w = do(s, "GET", "/api/status", lan, "", signedIn)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"client":"owner"`) || !strings.Contains(w.Body.String(), `"setup":false`) {
		t.Fatalf("status signed in: %d %s", w.Code, w.Body.String()[:120])
	}
	w = do(s, "GET", "/api/hub/overview", lan, "", signedIn)
	var o hubOverview
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &o) != nil || o.System.Cores == 0 || o.Devices == nil {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String()[:200])
	}
	// The owner may set what a browser on the network may not.
	next := s.app.Settings.Get()
	next.Library.Dir = "/srv/anime"
	raw, _ := json.Marshal(next)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), signedIn); w.Code != http.StatusOK {
		t.Fatalf("owner settings: %d", w.Code)
	}
	if got := s.app.Settings.Get().Library.Dir; got != "/srv/anime" {
		t.Fatalf("the owner's library folder wasn't kept: %q", got)
	}
}
