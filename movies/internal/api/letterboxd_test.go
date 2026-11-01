package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/letterboxd"
)

// davevisFeed is the diary feed of the fake letterboxd.com: a movie TMDB
// knows (by its id) and one it doesn't.
const davevisFeed = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:letterboxd="https://letterboxd.com" xmlns:tmdb="https://themoviedb.org">
  <channel>
    <title>Letterboxd - Dave Vis</title>
    <link>https://letterboxd.com/davevis/</link>
    <item>
      <title>The Dark Knight, 2008 - ★★★★½</title>
      <link>https://letterboxd.com/davevis/film/the-dark-knight/</link>
      <guid isPermaLink="false">letterboxd-watch-1001</guid>
      <pubDate>Sun, 10 Mar 2024 21:14:05 +1300</pubDate>
      <letterboxd:watchedDate>2024-03-10</letterboxd:watchedDate>
      <letterboxd:rewatch>No</letterboxd:rewatch>
      <letterboxd:filmTitle>The Dark Knight</letterboxd:filmTitle>
      <letterboxd:filmYear>2008</letterboxd:filmYear>
      <letterboxd:memberRating>4.5</letterboxd:memberRating>
      <letterboxd:memberLike>Yes</letterboxd:memberLike>
      <tmdb:movieId>155</tmdb:movieId>
      <description><![CDATA[ <p>Watched on Sunday March 10, 2024.</p> ]]></description>
      <dc:creator>Dave Vis</dc:creator>
    </item>
    <item>
      <title>Nonexistent Movie, 1900</title>
      <link>https://letterboxd.com/davevis/film/nonexistent-movie/</link>
      <guid isPermaLink="false">letterboxd-watch-1002</guid>
      <pubDate>Sat, 09 Mar 2024 20:00:00 +1300</pubDate>
      <letterboxd:watchedDate>2024-03-09</letterboxd:watchedDate>
      <letterboxd:rewatch>No</letterboxd:rewatch>
      <letterboxd:filmTitle>Nonexistent Movie</letterboxd:filmTitle>
      <letterboxd:filmYear>1900</letterboxd:filmYear>
      <description><![CDATA[ <p>Watched on Saturday March 9, 2024.</p> ]]></description>
      <dc:creator>Dave Vis</dc:creator>
    </item>
  </channel>
</rss>`

// newLetterboxdServer is a catalog test server whose Letterboxd is a fake
// site with davevis's feed.
func newLetterboxdServer(t *testing.T) *Server {
	t.Helper()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/davevis/rss/" {
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = w.Write([]byte(davevisFeed))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(site.Close)
	s := newCatalogServer(t)
	s.app.Letterboxd.BaseURL = site.URL
	return s
}

const (
	local = "127.0.0.1:5000"
	lan   = "192.168.1.20:5000"
)

func allowLAN(t *testing.T, s *Server) {
	t.Helper()
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func lbStatusOf(t *testing.T, s *Server) letterboxd.Status {
	t.Helper()
	var st letterboxd.Status
	decodeBody(t, do(s, "GET", "/api/letterboxd/status", local, "", nil), &st)
	return st
}

func diaryOf(t *testing.T, s *Server, mediaID int) []catalog.DiaryEntry {
	t.Helper()
	var d []catalog.DiaryEntry
	decodeBody(t, do(s, "GET", "/api/letterboxd/diary?mediaId="+strconv.Itoa(mediaID), local, "", nil), &d)
	return d
}

func wantCode(t *testing.T, w *httptest.ResponseRecorder, code int, what string) {
	t.Helper()
	if w.Code != code {
		t.Errorf("%s: %d %s, want %d", what, w.Code, strings.TrimSpace(w.Body.String()), code)
	}
}

func TestLetterboxdConnect(t *testing.T) {
	s := newLetterboxdServer(t)

	wantCode(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":"not a name!"}`, nil), http.StatusBadRequest, "bad username")
	wantCode(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":"nobody"}`, nil), http.StatusNotFound, "no such member")
	wantCode(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":""}`, nil), http.StatusBadRequest, "no username")
	wantCode(t, do(s, "POST", "/api/letterboxd/sync", local, "", nil), http.StatusConflict, "sync while not connected")
	site := s.app.Letterboxd.BaseURL
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	s.app.Letterboxd.BaseURL = down.URL
	if w := do(s, "POST", "/api/letterboxd/connect", local, `{"username":"davevis"}`, nil); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "Couldn't reach Letterboxd") {
		t.Errorf("Letterboxd down: %d %s", w.Code, w.Body.String())
	}
	s.app.Letterboxd.BaseURL = site
	if lbStatusOf(t, s).Connected {
		t.Fatal("connected while Letterboxd was down")
	}

	var conn struct {
		letterboxd.Status
		Viewer *catalog.Viewer    `json:"viewer"`
		Result *letterboxd.Result `json:"result"`
	}
	decodeBody(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":"https://letterboxd.com/DaveVis/"}`, nil), &conn)
	if !conn.Connected || conn.Username != "davevis" || conn.DisplayName != "Dave Vis" || conn.LastSync == 0 || conn.LastError != "" ||
		conn.ProfileURL != "https://letterboxd.com/davevis/" || conn.Viewer == nil || conn.Viewer.Name != "Dave Vis" {
		t.Fatalf("connect: %+v viewer %+v", conn.Status, conn.Viewer)
	}
	want := &letterboxd.Result{Added: 1, Diary: 1, Unmatched: []string{"Nonexistent Movie (1900)"}}
	if !reflect.DeepEqual(conn.Result, want) {
		t.Errorf("first sync: %+v, want %+v", conn.Result, want)
	}

	// /api/status reports the member as before.
	var st map[string]any
	decodeBody(t, do(s, "GET", "/api/status", local, "", nil), &st)
	if lb, _ := st["letterboxd"].(map[string]any); lb["name"] != "Dave Vis" || st["loggedIn"] != true || st["user"] == nil {
		t.Errorf("status: letterboxd=%v loggedIn=%v", st["letterboxd"], st["loggedIn"])
	}

	// The diary and the list have the film.
	d := diaryOf(t, s, 155)
	if len(d) != 1 || d[0].Source != "letterboxd" || d[0].WatchedDate != "2024-03-10" || d[0].Rating != 90 || !d[0].Liked {
		t.Errorf("diary: %+v", d)
	}
	var list catalog.Collection
	decodeBody(t, do(s, "GET", "/api/catalog/list", local, "", nil), &list)
	if e := list.Find(155); e == nil || e.Status != "COMPLETED" || e.Score != 90 {
		t.Errorf("list entry: %+v", e)
	}

	// Syncing again brings nothing new.
	var res letterboxd.Result
	decodeBody(t, do(s, "POST", "/api/letterboxd/sync", local, "", nil), &res)
	if res.Added != 0 || res.Diary != 0 || len(res.Unmatched) != 1 {
		t.Errorf("second sync: %+v", res)
	}

	// Saving the settings from a page opened before the connection doesn't
	// disconnect the account.
	cfg := s.app.Settings.Get()
	cfg.Letterboxd.Username = ""
	cfg.Letterboxd.AutoSync = false
	raw, _ := json.Marshal(cfg)
	wantCode(t, do(s, "PUT", "/api/settings", local, string(raw), nil), http.StatusOK, "save settings")
	if got := s.app.Settings.Get().Letterboxd; got.Username != "davevis" || got.AutoSync {
		t.Errorf("settings saved: %+v", got)
	}

	var after letterboxd.Status
	decodeBody(t, do(s, "POST", "/api/letterboxd/disconnect", local, "", nil), &after)
	if after.Connected || after.Username != "" {
		t.Errorf("disconnected: %+v", after)
	}
	decodeBody(t, do(s, "GET", "/api/status", local, "", nil), &st)
	if st["letterboxd"] != nil || st["loggedIn"] != false {
		t.Errorf("status after disconnecting: %v", st["letterboxd"])
	}
	wantCode(t, do(s, "POST", "/api/letterboxd/sync", local, "", nil), http.StatusConflict, "sync after disconnecting")
	// What was imported stays.
	if d := diaryOf(t, s, 155); len(d) != 1 {
		t.Errorf("diary after disconnecting: %+v", d)
	}
}

// A diary entry read from the feed and deleted stays deleted while the feed
// still has it.
func TestLetterboxdDeletedEntryStaysDeleted(t *testing.T) {
	s := newLetterboxdServer(t)
	wantCode(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":"davevis"}`, nil), http.StatusOK, "connect")
	d := diaryOf(t, s, 155)
	if len(d) != 1 {
		t.Fatalf("imported: %+v", d)
	}
	wantCode(t, do(s, "DELETE", "/api/letterboxd/diary/"+strconv.FormatInt(d[0].ID, 10), local, "", nil), http.StatusOK, "delete")
	var res letterboxd.Result
	decodeBody(t, do(s, "POST", "/api/letterboxd/sync", local, "", nil), &res)
	if res.Diary != 0 || res.Added != 0 {
		t.Errorf("sync after deleting: %+v", res)
	}
	if d := diaryOf(t, s, 155); len(d) != 0 {
		t.Errorf("the deleted entry came back: %+v", d)
	}
}

// Devices on the LAN see the account and sync, but don't connect it, import
// files or sign in to the API.
func TestLetterboxdLAN(t *testing.T) {
	s := newLetterboxdServer(t)
	allowLAN(t, s)
	wantCode(t, do(s, "POST", "/api/letterboxd/connect", lan, `{"username":"davevis"}`, nil), http.StatusForbidden, "LAN connect")
	wantCode(t, do(s, "POST", "/api/letterboxd/connect", local, `{"username":"davevis"}`, nil), http.StatusOK, "connect")

	wantCode(t, do(s, "GET", "/api/letterboxd/status", lan, "", nil), http.StatusOK, "LAN status")
	wantCode(t, do(s, "GET", "/api/letterboxd/diary?mediaId=155", lan, "", nil), http.StatusOK, "LAN diary")
	wantCode(t, do(s, "POST", "/api/letterboxd/sync", lan, "", nil), http.StatusOK, "LAN sync")
	wantCode(t, do(s, "GET", "/api/letterboxd/export.csv", lan, "", nil), http.StatusOK, "LAN export")
	wantCode(t, do(s, "POST", "/api/letterboxd/disconnect", lan, "", nil), http.StatusForbidden, "LAN disconnect")
	wantCode(t, do(s, "POST", "/api/letterboxd/api-login", lan, `{"username":"davevis","password":"x"}`, nil), http.StatusForbidden, "LAN API sign-in")
	wantCode(t, do(s, "DELETE", "/api/letterboxd/api-login", lan, "", nil), http.StatusForbidden, "LAN API sign-out")
	body, ct := multipartBody(t, map[string]string{"diary.csv": diaryCSV})
	wantCode(t, upload(s, lan, body, ct), http.StatusForbidden, "LAN import")

	// Nor through the settings.
	cfg := s.app.Settings.Get()
	cfg.Letterboxd.Username = "someoneelse"
	raw, _ := json.Marshal(cfg)
	wantCode(t, do(s, "PUT", "/api/settings", lan, string(raw), nil), http.StatusOK, "LAN settings")
	if got := s.app.Settings.Get().Letterboxd.Username; got != "davevis" {
		t.Errorf("a LAN device changed the account to %q", got)
	}
	if !lbStatusOf(t, s).Connected {
		t.Error("disconnected from the LAN")
	}
}

const diaryCSV = "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Tags,Watched Date\n" +
	"2024-01-02,Inception,2010,https://boxd.it/abc1,4,,,2024-01-01\n" +
	"2024-01-03,Nonexistent Movie,1900,https://boxd.it/abc2,3,,,2024-01-03\n"

const likesCSV = "Date,Name,Year,Letterboxd URI\n2024-01-04,Inception,2010,https://boxd.it/film1\n"

// multipartBody is a form with files (name: content), in name order.
func multipartBody(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fw, err := mw.CreateFormFile("file", n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(files[n]))
	}
	_ = mw.WriteField("note", "not a file")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func upload(s *Server, remote string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:43311/api/letterboxd/import", body)
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(w, r)
	return w
}

func TestLetterboxdImport(t *testing.T) {
	s := newLetterboxdServer(t)

	type imported struct {
		letterboxd.Result
		Files []string `json:"files"`
	}
	var res imported
	body, ct := multipartBody(t, map[string]string{"diary.csv": diaryCSV, "films.csv": likesCSV})
	decodeBody(t, upload(s, local, body, ct), &res)
	if res.Added != 1 || res.Diary != 1 || !reflect.DeepEqual(res.Unmatched, []string{"Nonexistent Movie (1900)"}) ||
		!reflect.DeepEqual(res.Files, []string{"diary.csv", "likes/films.csv"}) {
		t.Fatalf("import: %+v", res)
	}
	var list catalog.Collection
	decodeBody(t, do(s, "GET", "/api/catalog/list", local, "", nil), &list)
	if e := list.Find(27205); e == nil || e.Status != "COMPLETED" || e.Score != 80 || !e.Liked {
		t.Errorf("imported entry: %+v", e)
	}
	if d := diaryOf(t, s, 27205); len(d) != 1 || d[0].WatchedDate != "2024-01-01" || d[0].Source != "letterboxd" {
		t.Errorf("imported diary: %+v", d)
	}

	// Again: nothing new.
	body, ct = multipartBody(t, map[string]string{"diary.csv": diaryCSV})
	decodeBody(t, upload(s, local, body, ct), &res)
	if res.Added != 0 || res.Diary != 0 {
		t.Errorf("second import: %+v", res)
	}

	body, ct = multipartBody(t, map[string]string{"notes.txt": "just some text\nwith lines\n"})
	if w := upload(s, local, body, ct); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "notes.txt") {
		t.Errorf("not an export: %d %s", w.Code, w.Body.String())
	}
	body, ct = multipartBody(t, nil)
	wantCode(t, upload(s, local, body, ct), http.StatusBadRequest, "no file")
	wantCode(t, do(s, "POST", "/api/letterboxd/import", local, `{"file":"diary.csv"}`, nil), http.StatusBadRequest, "JSON instead of files")
}

// What's watched here is logged once, exported as a CSV and marked as sent.
func TestLetterboxdExport(t *testing.T) {
	s := newLetterboxdServer(t)

	// Marked watched on its page: logged.
	wantCode(t, do(s, "POST", "/api/media/155/episode", local, `{"episode":1,"watched":true}`, nil), http.StatusOK, "mark watched")
	d := diaryOf(t, s, 155)
	if len(d) != 1 || d[0].Source != "kumobox" || d[0].WatchedDate != time.Now().Format("2006-01-02") || d[0].ExportedAt != 0 {
		t.Fatalf("logged: %+v", d)
	}
	// Edited afterwards (a rating): not logged again.
	wantCode(t, do(s, "POST", "/api/media/155/entry", local, `{"status":"COMPLETED","score":90}`, nil), http.StatusOK, "rate")
	if d := diaryOf(t, s, 155); len(d) != 1 {
		t.Fatalf("logged twice: %+v", d)
	}
	if st := lbStatusOf(t, s); st.Pending != 1 {
		t.Errorf("pending = %d", st.Pending)
	}

	w := do(s, "GET", "/api/letterboxd/export.csv?all=0", local, "", nil)
	wantName := "kumobox-letterboxd-" + time.Now().Format("2006-01-02") + ".csv"
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Header().Get("Content-Disposition"), wantName) ||
		w.Header().Get("X-Diary-Ids") != strconv.FormatInt(d[0].ID, 10) {
		t.Fatalf("export: %d %v", w.Code, w.Header())
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Title,Year,tmdbID") || !strings.HasPrefix(lines[1], "The Dark Knight,2008,155,tt0468569,"+time.Now().Format("2006-01-02")+",4.5,") {
		t.Errorf("CSV:\n%s", w.Body.String())
	}

	// "I imported it": the entries of the CSV are sent (unknown ids aren't
	// counted).
	var marked map[string]int
	decodeBody(t, do(s, "POST", "/api/letterboxd/mark-exported", local, fmt.Sprintf(`{"ids":[%d,9999]}`, d[0].ID), nil), &marked)
	if marked["marked"] != 1 || lbStatusOf(t, s).Pending != 0 {
		t.Errorf("marked %v, pending %d", marked, lbStatusOf(t, s).Pending)
	}
	if w := do(s, "GET", "/api/letterboxd/export.csv", local, "", nil); w.Header().Get("X-Diary-Count") != "0" {
		t.Errorf("new entries after marking: %v", w.Header())
	}
	if w := do(s, "GET", "/api/letterboxd/export.csv?all=1", local, "", nil); w.Header().Get("X-Diary-Count") != "1" {
		t.Errorf("all entries: %v", w.Header())
	}

	// Put on the Watched list by hand: logged; without ids, every entry
	// still to send is marked.
	wantCode(t, do(s, "POST", "/api/media/27205/entry", local, `{"status":"COMPLETED"}`, nil), http.StatusOK, "watched by hand")
	d = diaryOf(t, s, 27205)
	if len(d) != 1 || d[0].Source != "kumobox" {
		t.Fatalf("logged by hand: %+v", d)
	}
	decodeBody(t, do(s, "POST", "/api/letterboxd/mark-exported", local, "", nil), &marked)
	if marked["marked"] != 1 {
		t.Errorf("marked %v", marked)
	}
	decodeBody(t, do(s, "POST", "/api/letterboxd/mark-exported", local, "{}", nil), &marked)
	if marked["marked"] != 0 {
		t.Errorf("marked again %v", marked)
	}

	wantCode(t, do(s, "DELETE", "/api/letterboxd/diary/"+strconv.FormatInt(d[0].ID, 10), local, "", nil), http.StatusOK, "delete")
	if d := diaryOf(t, s, 27205); len(d) != 0 {
		t.Errorf("deleted entry still there: %+v", d)
	}
	wantCode(t, do(s, "DELETE", "/api/letterboxd/diary/abc", local, "", nil), http.StatusBadRequest, "delete a bad id")
	var all []catalog.DiaryEntry
	decodeBody(t, do(s, "GET", "/api/letterboxd/diary?limit=10", local, "", nil), &all)
	if len(all) != 1 || all[0].MediaID != 155 || all[0].Media == nil {
		t.Errorf("whole diary: %+v", all)
	}

	// The watchlist as a CSV.
	wantCode(t, do(s, "POST", "/api/media/27205/entry", local, `{"status":"PLANNING"}`, nil), http.StatusOK, "watchlist")
	w = do(s, "GET", "/api/letterboxd/watchlist.csv", local, "", nil)
	if w.Code != http.StatusOK || w.Header().Get("X-Film-Count") != "1" || !strings.Contains(w.Body.String(), "Inception,2010,27205,tt1375666") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "kumobox-watchlist-") {
		t.Errorf("watchlist: %d %v\n%s", w.Code, w.Header(), w.Body.String())
	}
}

// fakeLetterboxdAPI accepts davevis with the password "right" and logs
// diary entries.
func fakeLetterboxdAPI(t *testing.T, logged *[]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "kumobox" || r.Form.Get("password") != "right" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad credentials"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","refresh_token":"ref","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Bearer tok":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/me":
			_, _ = w.Write([]byte(`{"member":{"id":"m1","username":"davevis","displayName":"Dave Vis"}}`))
		case r.URL.Path == "/films":
			if r.URL.Query().Get("filmId") == "tmdb:155" {
				_, _ = w.Write([]byte(`{"items":[{"id":"2b0k","name":"The Dark Knight"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.URL.Path == "/log-entries" && r.Method == "POST":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*logged = append(*logged, body)
			_, _ = w.Write([]byte(`{"id":"e1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLetterboxdAPI(t *testing.T) {
	s := newLetterboxdServer(t)
	var logged []map[string]any
	s.app.Letterboxd.APIBaseURL = fakeLetterboxdAPI(t, &logged).URL

	wantCode(t, do(s, "POST", "/api/letterboxd/push", local, "", nil), http.StatusConflict, "push without API access")
	wantCode(t, do(s, "POST", "/api/letterboxd/api-login", local, `{"username":"davevis","password":"right"}`, nil), http.StatusConflict, "sign in without credentials")

	cfg := s.app.Settings.Get()
	cfg.Letterboxd.APIClientID, cfg.Letterboxd.APIClientSecret = "kumobox", "secret"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if st := lbStatusOf(t, s); !st.APIAvailable || st.APIConnected {
		t.Fatalf("status with credentials: %+v", st)
	}
	wantCode(t, do(s, "POST", "/api/letterboxd/api-login", local, `{"username":"davevis","password":"wrong"}`, nil), http.StatusUnauthorized, "wrong password")
	wantCode(t, do(s, "POST", "/api/letterboxd/api-login", local, `{"username":"davevis"}`, nil), http.StatusBadRequest, "no password")

	var in struct {
		letterboxd.Status
		Member *letterboxd.Member `json:"member"`
	}
	decodeBody(t, do(s, "POST", "/api/letterboxd/api-login", local, `{"username":"davevis","password":"right"}`, nil), &in)
	// Signing in connects the member's account when none is.
	if !in.APIConnected || in.APIMember != "davevis" || in.Member == nil || in.Member.DisplayName != "Dave Vis" || !in.Connected || in.Username != "davevis" {
		t.Fatalf("signed in: %+v member %+v", in.Status, in.Member)
	}

	wantCode(t, do(s, "POST", "/api/media/155/episode", local, `{"episode":1,"watched":true}`, nil), http.StatusOK, "mark watched")
	var res letterboxd.Result
	decodeBody(t, do(s, "POST", "/api/letterboxd/push", local, "", nil), &res)
	if res.Added != 1 || len(logged) != 1 || logged[0]["filmId"] != "2b0k" {
		t.Fatalf("push: %+v, logged %v", res, logged)
	}
	if st := lbStatusOf(t, s); st.Pending != 0 {
		t.Errorf("pending after push: %d", st.Pending)
	}

	var out letterboxd.Status
	decodeBody(t, do(s, "DELETE", "/api/letterboxd/api-login", local, "", nil), &out)
	if out.APIConnected || !out.Connected {
		t.Errorf("signed out: %+v", out)
	}
	wantCode(t, do(s, "POST", "/api/letterboxd/push", local, "", nil), http.StatusConflict, "push after signing out")
}
