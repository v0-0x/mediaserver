package api

import (
	"net/http"
	"strings"
	"testing"
)

// mediaserver's movies side: a browser on the network signed in to the
// dashboard may do what this computer may; one that isn't is sent there.
// Other Moviko apps (/api/peer/) are checked as always, not by sign-in.
func TestOwnerSignIn(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, "moviko's own"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s.DashboardPort = 43211
	s.Owner = func(r *http.Request) bool {
		c, err := r.Cookie("kumo_session")
		return err == nil && c.Value == "signed-in"
	}
	lan, local := "192.168.1.20:5000", "127.0.0.1:5000"
	// This computer by its name, as the browser has it.
	host := machineName() + ".local"
	at := func(r *http.Request) { r.Host = host + ":43311" }
	signedIn := func(r *http.Request) {
		at(r)
		r.AddCookie(&http.Cookie{Name: "kumo_session", Value: "signed-in"})
	}

	w := do(s, "GET", "/", lan, "", at)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "http://"+host+":43211/" {
		t.Fatalf("page, not signed in: %d to %q", w.Code, w.Header().Get("Location"))
	}
	w = do(s, "GET", "/api/status", lan, "", at)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), host+":43211") {
		t.Fatalf("API, not signed in: %d %s", w.Code, w.Body)
	}
	// Moviko's own network password doesn't let one in.
	w = do(s, "POST", "/api/auth/server-login", lan, `{"password":"moviko's own"}`, at)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Moviko's password, not signed in: %d", w.Code)
	}

	w = do(s, "GET", "/api/status", lan, "", signedIn)
	if w.Code != http.StatusOK {
		t.Fatalf("signed in: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"client":"local"`) || !strings.Contains(w.Body.String(), `"dashboardPort":43211`) {
		t.Fatalf("signed in: not this computer's, or no dashboard: %s", w.Body)
	}
	// What only this computer may do.
	if w := do(s, "POST", "/api/update/check", lan, "{}", signedIn); w.Code != http.StatusOK {
		t.Fatalf("signed in, update check: %d %s", w.Code, w.Body)
	}
	// The computer itself, as always.
	if w := do(s, "GET", "/api/status", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("this computer: %d", w.Code)
	}
	// Another Moviko: its own checks (unsigned: refused by them, not sent
	// to the dashboard).
	w = do(s, "GET", "/api/peer/hello", lan, "", at)
	if w.Code == http.StatusSeeOther || strings.Contains(w.Body.String(), "dashboard") {
		t.Fatalf("a Moviko app was sent to the dashboard: %d %s", w.Code, w.Body)
	}
}
