package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/providers/stremio"
)

// Importing a Stremio account (this computer only): its addons on this
// computer or the LAN aren't installed without a confirmation, and the
// password and session key are kept nowhere.
func TestStremioAccountImport(t *testing.T) {
	s := newProvidersServer(t)
	addon := newFakeAddon(t)
	var logins atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.URL.Path == "/api/login" && body["password"] == "hunter2":
			logins.Add(1)
			_, _ = io.WriteString(w, `{"result":{"authKey":"sess-key-42"}}`)
		case r.URL.Path == "/api/login":
			_, _ = io.WriteString(w, `{"error":{"message":"Wrong passphrase","code":3}}`)
		case r.URL.Path == "/api/addonCollectionGet" && body["authKey"] == "sess-key-42":
			_, _ = io.WriteString(w, `{"result":{"addons":[{"transportUrl":"`+addon.URL+`/manifest.json","manifest":{"id":"test.addon","version":"1.0.0","name":"Test Addon","resources":["stream"],"types":["movie"]}}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	t.Setenv("KUMOBOX_STREMIO_API_URL", api.URL)

	creds := jsonBody(t, map[string]any{"email": "me@example.com", "password": "hunter2"})
	if w := do(s, "POST", "/api/providers/stremio/import", lan, creds, nil); w.Code != http.StatusForbidden || logins.Load() != 0 {
		t.Fatalf("from the LAN: %d (%d sign-ins)", w.Code, logins.Load())
	}
	w := do(s, "POST", "/api/providers/stremio/import", local, jsonBody(t, map[string]any{"email": "me@example.com", "password": "nope"}), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Wrong passphrase") {
		t.Fatalf("wrong password: %d %s", w.Code, w.Body.String())
	}
	var res stremio.ImportResult
	decodeBody(t, do(s, "POST", "/api/providers/stremio/import", local, creds, nil), &res)
	if len(res.Added) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "local network") || res.Skipped[0].Name != "Test Addon" {
		t.Fatalf("import: %+v", res)
	}
	if len(s.app.Stremio.List()) != 0 {
		t.Fatal("a local addon was installed without confirmation")
	}
	var leaks int
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM kv WHERE value LIKE '%hunter2%' OR value LIKE '%sess-key-42%'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("the password or session key was saved (%d, %v)", leaks, err)
	}
}
