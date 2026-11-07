package extensions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The marketplace's plugins say which permission scopes they ask for that
// Aniko doesn't have (from their manifests, kept a day); providers and
// plugins with Aniko's scopes alone say nothing.
func TestMarketplaceMarksUnsupportedScopes(t *testing.T) {
	asked := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/needy.json", func(w http.ResponseWriter, r *http.Request) {
		asked++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "needy", "name": "Needy", "type": TypePlugin, "version": "1.0.0", "language": "javascript",
			"plugin": map[string]any{"version": "1", "permissions": map[string]any{"scopes": []string{"anilist", "playback", "system"}}},
		})
	})
	mux.HandleFunc("/fine.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fine", "name": "Fine", "type": TypePlugin, "version": "1.0.0", "language": "javascript",
			"plugin": map[string]any{"version": "1", "permissions": map[string]any{"scopes": []string{"anilist", "storage"}}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/market.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "needy", "name": "Needy", "type": TypePlugin, "manifestURI": srv.URL + "/needy.json"},
			{"id": "fine", "name": "Fine", "type": TypePlugin, "manifestURI": srv.URL + "/fine.json"},
			{"id": "src", "name": "A source", "type": TypeOnlineStream, "manifestURI": srv.URL + "/none.json"},
		})
	})
	m := newTestManager(t)
	// The test server is local, which Aniko's fetches refuse: plain ones here.
	plain := func(ctx context.Context, url string, _ map[string]string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	old := fetchManifest
	fetchManifest = plain
	t.Cleanup(func() { fetchManifest = old })
	for round := range 2 {
		market, err := plain(context.Background(), srv.URL+"/market.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		// The list itself as the marketplace fetch would keep it.
		var list []MarketEntry
		if err := json.Unmarshal(market, &list); err != nil {
			t.Fatal(err)
		}
		m.db.SetCache("marketplace:"+srv.URL+"/market.json", list, time.Hour)
		entries, err := m.Marketplace(context.Background(), srv.URL+"/market.json", false)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string][]string{}
		for _, e := range entries {
			got[e.ID] = e.Unsupported
		}
		if u := got["needy"]; len(u) != 2 || u[0] != "playback" || u[1] != "system" {
			t.Fatalf("round %d: needy's unsupported scopes %v, want [playback system]", round, u)
		}
		if len(got["fine"]) != 0 || len(got["src"]) != 0 {
			t.Fatalf("round %d: fine %v, src %v: want none", round, got["fine"], got["src"])
		}
	}
	if asked != 1 {
		t.Fatalf("the manifest was fetched %d times, want once (kept)", asked)
	}
}
