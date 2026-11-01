package api

import (
	"net/http"
	"strings"
	"testing"
)

// The dashboard's movies and TV pages reach the movies side through this
// one: only for its signed-in owner (or this computer), as the movies
// side's own computer, without the dashboard's cookie.
func TestMoviesRoute(t *testing.T) {
	s := newHubServer(t)
	var got *http.Request
	s.SetMovies(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		writeJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path})
	}))
	lan, local := "192.168.1.20:5000", "127.0.0.1:5000"

	// This computer.
	if w := do(s, "GET", "/api/movies/library/titles?host=x", local, "", nil); w.Code != http.StatusOK || got == nil {
		t.Fatalf("from this computer: %d %s", w.Code, w.Body)
	}
	if got.URL.Path != "/api/library/titles" || got.URL.RawQuery != "host=x" || !strings.HasPrefix(got.RemoteAddr, "127.0.0.1:") || got.Host != "localhost" {
		t.Fatalf("passed on as %s %s from %s to %s", got.URL.Path, got.URL.RawQuery, got.RemoteAddr, got.Host)
	}

	// The network: not before the password is set, nor without signing in.
	got = nil
	if w := do(s, "GET", "/api/movies/settings", lan, "", nil); w.Code == http.StatusOK || got != nil {
		t.Fatalf("before setup: %d", w.Code)
	}
	code := s.app.HubStore.SetupCode()
	w := do(s, "POST", "/api/hub/setup", lan, `{"code":"`+code+`","password":"hunter22"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	if w := do(s, "GET", "/api/movies/settings", lan, "", nil); w.Code == http.StatusOK || got != nil {
		t.Fatalf("not signed in: %d", w.Code)
	}
	w = do(s, "POST", "/api/movies/sharing/peers/abc", lan, `{"allowed":true}`, func(r *http.Request) {
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://"+r.Host)
	})
	if w.Code != http.StatusOK || got == nil || got.URL.Path != "/api/sharing/peers/abc" {
		t.Fatalf("signed in: %d %s", w.Code, w.Body)
	}
	if got.Header.Get("Cookie") != "" || got.Header.Get("Origin") != "" {
		t.Fatalf("passed on with the dashboard's cookie or origin: %v", got.Header)
	}
}
