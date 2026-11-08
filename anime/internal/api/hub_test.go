package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"fmt"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/app"
	"github.com/v0-0x/mediaserver/anime/internal/watchorder"
	"net"
	"net/http/httptest"
	"time"
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

// hubSharingServer is a hub on the network, as sharingServer makes a device.
func hubSharingServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := newHubServer(t)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(s.middleware(s.mux))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	srv.URL = fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	t.Cleanup(srv.Close)
	s.app.Share.Start()
	cfg := s.app.Settings.Get()
	cfg.Sharing.Enabled = true
	cfg.Server.Port = srv.Listener.Addr().(*net.TCPAddr).Port
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s, srv
}

// Two devices on no AniList account share one list through the hub, and
// their watch-order edits: an entry added on one shows on the other, a
// removal removes it there, an edited order comes over.
func TestListsFollowTheHub(t *testing.T) {
	const local = "127.0.0.1:5000"
	hub, hubSrv := hubSharingServer(t)
	a, _ := sharingServer(t)
	b, _ := sharingServer(t)
	addr := strings.TrimPrefix(hubSrv.URL, "http://")
	for _, d := range []*Server{a, b} {
		if w := do(d, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
			t.Fatalf("connect: %d %s", w.Code, w.Body)
		}
		eventually(t, "the hub to list the device", func() bool { _, ok := peerIn(hub, d); return ok })
		if err := hub.app.Share.SetAllowed(d.app.Share.ID(), true); err != nil {
			t.Fatal(err)
		}
	}
	asked := func(d *Server, ok func() bool) func() bool {
		return func() bool { do(d, "GET", "/api/sharing?refresh=1", local, "", nil); return ok() }
	}
	for _, d := range []*Server{a, b} {
		eventually(t, "the hub's library", asked(d, func() bool { return len(d.app.Share.Libraries()) == 1 }))
	}
	media := &anilist.Media{ID: 21, Type: "ANIME", Title: anilist.Title{Romaji: "One Piece"}}
	a.app.DB.SetCache("media-lite:21", media, time.Hour)
	st := "CURRENT"
	if err := a.app.Platform.UpdateEntry(t.Context(), anilist.EntryUpdate{MediaID: 21, Status: &st}); err != nil {
		t.Fatal(err)
	}
	has := func(d *Server, id int) func() bool {
		return func() bool {
			rows, _ := d.app.Platform.LocalRows()
			for _, r := range rows {
				if r.MediaID == id {
					return true
				}
			}
			return false
		}
	}
	eventually(t, "the entry to reach the hub", func() bool { return len(hub.app.HubStore.Blob(0, "lists").Items) == 1 })
	eventually(t, "the entry to reach the other device", asked(b, has(b, 21)))
	if ls := b.app.Share.Status().ListsSynced; ls == nil || ls.At == 0 || ls.With == "" {
		t.Fatalf("the other device doesn't say when it synced: %+v", ls)
	}
	// Removed on one: gone on the other (and remembered as a removal).
	if err := a.app.Platform.DeleteEntry(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the removal to reach the other device", asked(b, func() bool { return !has(b, 21)() }))
	// A watch-order edit on B comes to A.
	if w := do(b, "PUT", "/api/watch-order/100", local, `{"order":[102,101],"hidden":[101]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	eventually(t, "the edit to reach the other device", asked(a, func() bool {
		var e watchorder.Edit
		ok, _ := a.app.DB.GetKV(watchorder.EditKey(100), &e)
		return ok && len(e.Order) == 2 && e.Order[0] == 102
	}))
	// Turned off on a device: nothing more comes to it.
	cfg := a.app.Settings.Get()
	cfg.Sharing.SyncLists = false
	if _, err := a.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(b, "PUT", "/api/watch-order/300", local, `{"order":[301],"hidden":[]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	eventually(t, "the hub to have the second edit", func() bool { return len(hub.app.HubStore.Blob(0, "watch-order").Items) == 2 })
	do(a, "GET", "/api/sharing?refresh=1", local, "", nil)
	time.Sleep(300 * time.Millisecond)
	var e watchorder.Edit
	if ok, _ := a.app.DB.GetKV(watchorder.EditKey(300), &e); ok {
		t.Fatal("an edit came to a device that turned it off")
	}
}
