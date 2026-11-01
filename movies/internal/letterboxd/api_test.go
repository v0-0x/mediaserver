package letterboxd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// fakeAPI is a Letterboxd API with one member (dave / pw) and a few films.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	valid     map[string]bool // access tokens accepted
	issued    int
	refreshes int
	logged    []map[string]any
	patched   map[string]map[string]any
	// films by "tmdb:603" / "imdb:tt..."; listed ones come from /films,
	// the others only from /film/<id>.
	listed map[string]Film
	direct map[string]Film
	broken bool // answer 500 to everything
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{
		t:       t,
		valid:   map[string]bool{},
		patched: map[string]map[string]any{},
		listed:  map[string]Film{"tmdb:603": {ID: "2a1m", Name: "The Matrix", ReleaseYear: 1999}},
		direct:  map[string]Film{"imdb:tt7366338": {ID: "tCh1", Name: "Chernobyl", ReleaseYear: 2019}},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeAPI) issue() map[string]any {
	f.issued++
	tok := "tok" + string(rune('0'+f.issued))
	f.valid[tok] = true
	return map[string]any{"access_token": tok, "token_type": "bearer", "refresh_token": "ref1", "expires_in": 3600}
}

func (f *fakeAPI) revoke() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.valid = map[string]bool{}
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken {
		writeJSON(w, 500, map[string]any{"message": "Oops"})
		return
	}
	if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "KumoBox/") {
		f.t.Errorf("User-Agent = %q", ua)
	}
	if r.URL.Path == "/auth/token" {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			f.t.Errorf("token request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		if r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("client_secret") != "secret" {
			writeJSON(w, 401, map[string]any{"error": "invalid_client"})
			return
		}
		switch r.PostForm.Get("grant_type") {
		case "password":
			if r.PostForm.Get("username") != "dave" || r.PostForm.Get("password") != "pw" {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant", "error_description": "Bad credentials"})
				return
			}
			writeJSON(w, 200, f.issue())
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != "ref1" {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			f.refreshes++
			tok := f.issue()
			delete(tok, "refresh_token")
			writeJSON(w, 200, tok)
		default:
			writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
		}
		return
	}
	if !f.valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
		writeJSON(w, 401, map[string]any{"type": "error", "code": "", "message": "Unauthorized"})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/me":
		writeJSON(w, 200, map[string]any{"member": map[string]any{
			"id": "abc", "username": "dave", "displayName": "Dave Vis",
			"avatar": map[string]any{"sizes": []any{
				map[string]any{"width": 144, "height": 144, "url": "https://a.ltrbxd.com/avatar-144.jpg"},
				map[string]any{"width": 1000, "height": 1000, "url": "https://a.ltrbxd.com/avatar-1000.jpg"},
				map[string]any{"width": 80, "height": 80, "url": "https://a.ltrbxd.com/avatar-80.jpg"},
			}},
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/films":
		var items []Film
		if film, ok := f.listed[r.URL.Query().Get("filmId")]; ok {
			items = append(items, film)
		}
		writeJSON(w, 200, map[string]any{"items": items, "next": ""})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/film/"):
		id := strings.TrimPrefix(r.URL.Path, "/film/")
		if film, ok := f.direct[id]; ok {
			writeJSON(w, 200, film)
			return
		}
		writeJSON(w, 404, map[string]any{"message": "No film"})
	case r.Method == http.MethodPost && r.URL.Path == "/log-entries":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("log entry body: %v", err)
		}
		f.logged = append(f.logged, body)
		writeJSON(w, 200, map[string]any{"id": "le" + string(rune('0'+len(f.logged)))})
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/film/") && strings.HasSuffix(r.URL.Path, "/me"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.patched[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/film/"), "/me")] = body
		writeJSON(w, 200, map[string]any{"data": body, "messages": []any{}})
	default:
		writeJSON(w, 404, map[string]any{"message": "Not found"})
	}
}

func TestAPIClient(t *testing.T) {
	api := newFakeAPI(t)
	ctx := context.Background()
	now := time.Date(2024, 3, 12, 9, 0, 0, 0, time.UTC)
	c := &APIClient{BaseURL: api.srv.URL, ClientID: "cid", ClientSecret: "secret", HTTP: api.srv.Client(), now: func() time.Time { return now }}

	if _, err := c.PasswordLogin(ctx, "dave", "wrong"); !errors.Is(err, ErrAPIUnauthorized) || !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("bad password: %v", err)
	}
	if _, err := (&APIClient{BaseURL: api.srv.URL}).PasswordLogin(ctx, "dave", "pw"); !errors.Is(err, ErrNoAPIAccess) {
		t.Errorf("no client credentials: %v", err)
	}
	if _, err := (&APIClient{BaseURL: api.srv.URL, ClientID: "cid", ClientSecret: "nope", HTTP: api.srv.Client()}).PasswordLogin(ctx, "dave", "pw"); !errors.Is(err, ErrAPIUnauthorized) {
		t.Errorf("bad client: %v", err)
	}
	tok, err := c.PasswordLogin(ctx, "dave", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "tok1" || tok.RefreshToken != "ref1" || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("token = %+v", tok)
	}
	if tok.Expired(now) || !tok.Expired(now.Add(time.Hour)) || !(*Token)(nil).Expired(now) || (&Token{AccessToken: "x"}).Expired(now) {
		t.Error("Expired is wrong")
	}

	if _, err := c.Me(ctx); !errors.Is(err, ErrNoAPIAccess) {
		t.Errorf("Me without a token: %v", err)
	}
	c.AccessToken = tok.AccessToken
	m, err := c.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (&Member{ID: "abc", Username: "dave", DisplayName: "Dave Vis", Avatar: "https://a.ltrbxd.com/avatar-1000.jpg"}); !reflect.DeepEqual(m, want) {
		t.Errorf("member = %+v", m)
	}

	if f, err := c.FilmByTMDB(ctx, 603); err != nil || f.ID != "2a1m" || f.ReleaseYear != 1999 {
		t.Errorf("FilmByTMDB = %+v, %v", f, err)
	}
	if f, err := c.FilmByIMDb(ctx, "tt7366338"); err != nil || f.ID != "tCh1" {
		t.Errorf("FilmByIMDb = %+v, %v", f, err)
	}
	if _, err := c.FilmByTMDB(ctx, 1); !errors.Is(err, ErrFilmNotFound) {
		t.Errorf("unknown film: %v", err)
	}
	if _, err := c.FilmByIMDb(ctx, " "); !errors.Is(err, ErrFilmNotFound) {
		t.Errorf("no IMDb id: %v", err)
	}

	id, err := c.LogEntry(ctx, LogEntryRequest{FilmID: "2a1m", Date: "2024-03-10", Rewatch: true, Rating: 4.4, Like: true, Review: "Still good.", Spoilers: true, Tags: []string{"cinema"}})
	if err != nil || id != "le1" {
		t.Fatalf("LogEntry = %q, %v", id, err)
	}
	if _, err := c.LogEntry(ctx, LogEntryRequest{FilmID: "2a1m"}); err != nil {
		t.Fatal(err)
	}
	wantLogged := []map[string]any{
		{"filmId": "2a1m", "like": true, "rating": 4.5, "tags": []any{"cinema"},
			"diaryDetails": map[string]any{"diaryDate": "2024-03-10", "rewatch": true},
			"review":       map[string]any{"text": "Still good.", "containsSpoilers": true}},
		{"filmId": "2a1m"},
	}
	if !reflect.DeepEqual(api.logged, wantLogged) {
		t.Errorf("logged = %#v", api.logged)
	}

	yes, rating := true, 4.0
	if err := c.SetFilmRelationship(ctx, "2a1m", RelationshipUpdate{Watched: &yes, Rating: &rating}); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"watched": true, "rating": 4.0}; !reflect.DeepEqual(api.patched["2a1m"], want) {
		t.Errorf("patched = %#v", api.patched)
	}

	fresh, err := c.Refresh(ctx, tok.RefreshToken)
	if err != nil || fresh.AccessToken != "tok2" || fresh.RefreshToken != "ref1" {
		t.Errorf("Refresh = %+v, %v", fresh, err)
	}
	if _, err := c.Refresh(ctx, "stale"); !errors.Is(err, ErrAPIUnauthorized) {
		t.Errorf("stale refresh: %v", err)
	}

	api.revoke()
	if _, err := c.Me(ctx); !errors.Is(err, ErrAPIUnauthorized) {
		t.Errorf("revoked token: %v", err)
	}
	api.broken = true
	_, err = c.FilmByTMDB(ctx, 603)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 500 || errors.Is(err, ErrAPIUnauthorized) || errors.Is(err, ErrFilmNotFound) || !strings.Contains(err.Error(), "Oops") {
		t.Errorf("server error: %v", err)
	}
}

func TestPush(t *testing.T) {
	ts := newTestSync(t)
	api := newFakeAPI(t)
	ctx := context.Background()
	ts.APIBaseURL = api.srv.URL

	if _, err := ts.Push(ctx); !errors.Is(err, ErrNoAPIAccess) {
		t.Errorf("push without API: %v", err)
	}
	if _, err := ts.APILogin(ctx, "dave", "pw"); !errors.Is(err, ErrNoAPIAccess) {
		t.Errorf("login without client credentials: %v", err)
	}
	ts.settings(t, func(l *config.LetterboxdSettings) { l.APIClientID = "cid"; l.APIClientSecret = "secret" })
	if _, err := ts.APILogin(ctx, "dave", "nope"); !errors.Is(err, ErrAPIUnauthorized) {
		t.Errorf("bad password: %v", err)
	}
	m, err := ts.APILogin(ctx, "dave", "pw")
	if err != nil || m.Username != "dave" {
		t.Fatalf("login = %+v, %v", m, err)
	}
	// No account was connected: the API's member is now.
	if u := ts.store.Get().Letterboxd.Username; u != "dave" {
		t.Errorf("username = %q", u)
	}
	if v := ts.cat.Viewer(); v == nil || v.ID != ViewerID("dave") || v.Name != "Dave Vis" || v.Avatar.Large != "https://a.ltrbxd.com/avatar-1000.jpg" {
		t.Errorf("viewer = %+v", v)
	}
	if st := ts.Status(ctx); !st.APIConnected || !st.APIAvailable || st.APIMember != "dave" {
		t.Errorf("status = %+v", st)
	}

	ts.cat.put(catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Score: 90, Liked: true})
	e1, _ := ts.LoggedWatched(ctx, matrix)
	e2, _, _ := ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: chernobyl, WatchedDate: "2024-03-01", Source: SourceKumoBox, Tags: "binge, , tv"})
	e3, _, _ := ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: 999, WatchedDate: "2024-03-02", Source: SourceKumoBox})
	ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: parasite, WatchedDate: "2024-03-02", Source: SourceLetterboxd, LetterboxdID: "x"})

	// The token was revoked: Push signs in again with the refresh token.
	api.revoke()
	res, err := ts.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (&Result{Added: 2, Skipped: 1, Unmatched: []string{"TMDB movie 999"}}); !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v", res)
	}
	if api.refreshes != 1 {
		t.Errorf("%d refreshes", api.refreshes)
	}
	wantLogged := []map[string]any{
		{"filmId": "2a1m", "like": true, "rating": 4.5, "diaryDetails": map[string]any{"diaryDate": "2024-03-12", "rewatch": false}},
		{"filmId": "tCh1", "tags": []any{"binge", "tv"}, "diaryDetails": map[string]any{"diaryDate": "2024-03-01", "rewatch": false}},
	}
	if !reflect.DeepEqual(api.logged, wantLogged) {
		t.Errorf("logged = %#v", api.logged)
	}
	pending, _ := ts.cat.PendingDiary(ctx)
	if len(pending) != 1 || pending[0].ID != e3.ID {
		t.Errorf("pending = %+v (sent %d, %d)", pending, e1.ID, e2.ID)
	}

	// The token expires: it's renewed before sending.
	*ts.clock = testNow.Add(2 * time.Hour)
	ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: matrix, WatchedDate: "2024-03-12", Source: SourceKumoBox, Rewatch: true})
	if res, err := ts.Push(ctx); err != nil || res.Added != 1 || api.refreshes != 2 {
		t.Errorf("push after expiry = %+v, %v, %d refreshes", res, err, api.refreshes)
	}

	// The server fails: the error is kept, the entries stay pending.
	api.broken = true
	ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: matrix, WatchedDate: "2024-03-13", Source: SourceKumoBox})
	if _, err := ts.Push(ctx); err == nil {
		t.Error("push to a broken server succeeded")
	}
	if st := ts.Status(ctx); st.LastError == "" || st.Pending != 2 {
		t.Errorf("status = %+v", st)
	}

	ts.APILogout()
	if st := ts.Status(ctx); st.APIConnected || !st.Connected {
		t.Errorf("after API logout: %+v", st)
	}
}

func TestPushSignInOver(t *testing.T) {
	ts := newTestSync(t)
	api := newFakeAPI(t)
	ctx := context.Background()
	ts.APIBaseURL = api.srv.URL
	ts.settings(t, func(l *config.LetterboxdSettings) { l.APIClientID = "cid"; l.APIClientSecret = "secret" })
	// A token that's expired, with a refresh token Letterboxd no longer takes.
	_ = ts.setKV(kvToken, Token{AccessToken: "old", RefreshToken: "stale", ExpiresAt: testNow.Add(-time.Hour)})
	ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: matrix, WatchedDate: "2024-03-01", Source: SourceKumoBox})
	if _, err := ts.Push(ctx); !errors.Is(err, ErrAPIUnauthorized) {
		t.Errorf("err = %v", err)
	}
	if st := ts.Status(ctx); st.APIConnected || st.Pending != 1 || st.LastError == "" {
		t.Errorf("status = %+v", st)
	}
	if _, err := ts.Push(ctx); !errors.Is(err, ErrNoAPIAccess) {
		t.Errorf("second push: %v", err)
	}
}

func TestBackgroundPush(t *testing.T) {
	ts := newTestSync(t)
	api := newFakeAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts.APIBaseURL = api.srv.URL
	ts.settings(t, func(l *config.LetterboxdSettings) {
		l.APIClientID, l.APIClientSecret, l.AutoSync = "cid", "secret", false
	})
	if _, err := ts.APILogin(ctx, "dave", "pw"); err != nil {
		t.Fatal(err)
	}
	ts.firstDelay, ts.tick = time.Hour, time.Hour
	ts.Start(ctx)
	// Logging a viewing sends it soon.
	if _, err := ts.LoggedWatched(ctx, matrix); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pending, _ := ts.cat.PendingDiary(ctx); len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not pushed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
