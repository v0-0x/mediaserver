package stremio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testEmail    = "me@example.com"
	testPassword = "hunter2"
	testAuthKey  = "session-key-123"
)

// fakeStremioAPI is Stremio's API: a sign-in, then the account's addon
// collection (testdata/account_addons.json). Like the real one, it answers
// errors with 200 and {"error": {...}}.
func fakeStremioAPI(t *testing.T) *httptest.Server {
	t.Helper()
	collection, err := os.ReadFile(filepath.Join("testdata", "account_addons.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/login":
			if body["type"] != "Login" || body["email"] != testEmail || body["password"] != testPassword {
				_, _ = w.Write([]byte(`{"error":{"message":"Wrong passphrase","code":3}}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":{"authKey":"` + testAuthKey + `","user":{"email":"` + testEmail + `"}}}`))
		case "/api/addonCollectionGet":
			if body["type"] != "AddonCollectionGet" || body["authKey"] != testAuthKey || body["update"] != true {
				_, _ = w.Write([]byte(`{"error":{"message":"Session does not exist","code":1}}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":{"addons":` + string(collection) + `,"lastModified":"2026-01-01T00:00:00Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// An account's addons: those KumoBox can use are installed, the others
// left out with the reason.
func TestAccountImport(t *testing.T) {
	api := fakeStremioAPI(t)
	ctx := context.Background()
	if _, err := AccountAddons(ctx, api.URL, testEmail, "wrong"); err == nil || !strings.Contains(err.Error(), "Wrong passphrase") {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := AccountAddons(ctx, api.URL, " ", testPassword); err == nil {
		t.Fatal("no email")
	}
	addons, err := AccountAddons(ctx, api.URL, " "+testEmail+" ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(addons) != 9 || addons[0].Manifest.ID != "org.test.streams" || !addons[3].Local || addons[2].Supported {
		t.Fatalf("addons: %+v", addons)
	}

	c, _, _ := testClient(t)
	s, _ := newTestStore(t, c)
	if _, err := s.Install(ctx, "http://addons.test/tmdbonly/manifest.json", InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	res := s.Import(ctx, addons)
	var added []string
	for _, a := range res.Added {
		added = append(added, a.ID)
	}
	if strings.Join(added, ",") != "org.test.streams,org.test.subs" {
		t.Errorf("added %v", added)
	}
	reasons := map[string]string{}
	for _, a := range res.Skipped {
		reasons[a.TransportURL] = a.Reason
	}
	for u, want := range map[string]string{
		"http://addons.test/catalogonly/manifest.json":     skipUnsupported,
		"http://127.0.0.1:11470/local-addon/manifest.json": skipLocal,
		"http://addons.test/adult/manifest.json":           skipAdult,
		"http://needscfg.test/manifest.json":               skipConfigure,
		"http://addons.test/cfg=1080p/manifest.json":       skipOther,
		"http://addons.test/tmdbonly/manifest.json":        skipInstalled,
		"http://addons.test/missing/manifest.json":         "could not read the addon's manifest",
	} {
		if !strings.Contains(reasons[u], want) {
			t.Errorf("%s: %q, want %q", u, reasons[u], want)
		}
	}
	if len(res.Skipped) != 7 {
		t.Errorf("skipped %+v", res.Skipped)
	}
	list := s.List()
	if len(list) != 3 || list[1].TransportURL != "http://addons.test/manifest.json" || !list[1].Enabled {
		t.Fatalf("installed: %+v", list)
	}

	// Imported again: everything is there already.
	if again := s.Import(ctx, addons[:2]); len(again.Added) != 0 || again.Skipped[0].Reason != skipInstalled {
		t.Errorf("again: %+v", again)
	}
}

func TestAPIURL(t *testing.T) {
	t.Setenv("KUMOBOX_STREMIO_API_URL", "")
	if APIURL() != DefaultAPIURL {
		t.Errorf("default: %s", APIURL())
	}
	t.Setenv("KUMOBOX_STREMIO_API_URL", " http://127.0.0.1:9999/ ")
	if APIURL() != "http://127.0.0.1:9999" {
		t.Errorf("override: %s", APIURL())
	}
}
