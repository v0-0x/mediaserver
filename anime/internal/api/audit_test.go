package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// signedInHub is a hub whose password is set, and how to be signed in to
// its dashboard.
func signedInHub(t *testing.T) (*Server, func(*http.Request)) {
	t.Helper()
	s := newHubServer(t)
	w := do(s, "POST", "/api/hub/setup", "192.168.1.20:5000", `{"code":"`+s.app.HubStore.SetupCode()+`","password":"hunter22"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	return s, func(r *http.Request) { r.AddCookie(cookie) }
}

func auditOf(t *testing.T, s *Server, query string, signedIn func(*http.Request)) []anikohub.Event {
	t.Helper()
	w := do(s, "GET", "/api/hub/audit"+query, "192.168.1.20:5000", "", signedIn)
	var res struct {
		Events []anikohub.Event `json:"events"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil {
		t.Fatalf("audit%s: %d %s", query, w.Code, w.Body.String())
	}
	return res.Events
}

// A hub deletes for good: the files and their subtitles gone, noted in the
// audit log with who and what, from where Download again queues the
// episodes (not the opening) onto the hub.
func TestHubDeleteAndDownloadAgain(t *testing.T) {
	s, signedIn := signedInHub(t)
	lan := "192.168.1.20:5000"
	root := t.TempDir()
	next := s.app.Settings.Get()
	next.Library.Dir = root
	raw, _ := json.Marshal(next)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), signedIn); w.Code != http.StatusOK {
		t.Fatalf("settings: %d", w.Code)
	}
	s.app.DB.SetCache("media-lite:21", anilist.Media{ID: 21, Title: anilist.Title{English: "One Piece", Romaji: "One Piece"}, Format: "TV"}, time.Hour)
	add := func(name string, ep int, kind string) string {
		p := filepath.Join(root, "One Piece", name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, 1000), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.app.Files.Save(&library.LocalFile{Path: p, Dir: filepath.Dir(p), Name: name, Size: 1000, MediaID: 21, Episode: ep, Kind: kind}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b, op := add("One Piece - 1051.mkv", 1051, "main"), add("One Piece - 1052.mkv", 1052, "main"), add("One Piece - NCOP1.mkv", 0, "nc")
	sub := filepath.Join(root, "One Piece", "One Piece - 1051.en.ass")
	_ = os.WriteFile(sub, []byte("subs"), 0o644)
	body := fmt.Sprintf(`{"paths":[%q,%q,%q]}`, a, b, op)

	if w := do(s, "POST", "/api/library/delete", lan, body, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("not signed in: %d", w.Code)
	}
	w := do(s, "POST", "/api/library/delete", lan, body, signedIn)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":3`) || !strings.Contains(w.Body.String(), `"freed":3000`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	for _, p := range []string{a, b, op, sub} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there", p)
		}
	}
	if items := s.app.Trash.Items(); len(items) != 0 {
		t.Errorf("in the trash: %+v", items)
	}

	lib := auditOf(t, s, "?group=library", signedIn)
	if len(lib) == 0 || lib[0].Kind != "delete" {
		t.Fatalf("library events: %+v", lib)
	}
	d := lib[0]
	if d.By != "dashboard" || d.IP != "192.168.1.20" || d.Title != "One Piece" || len(d.Files) != 3 || d.Size != 3000 || d.Again == nil || fmt.Sprint(d.Again.Episodes) != "[1051 1052]" || d.Again.Dub {
		t.Fatalf("the deletion: %+v (again %+v)", d, d.Again)
	}
	// The folder added before is there too.
	if f := auditOf(t, s, "?group=library&q=added+folder", signedIn); len(f) != 1 || !strings.Contains(f[0].Text, root) || f[0].Kind != "folders" {
		t.Errorf("the folder: %+v", f)
	}

	w = do(s, "POST", fmt.Sprintf("/api/hub/audit/%d/again", d.ID), lan, `{"dub":true}`, signedIn)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"queued":2`) {
		t.Fatalf("again: %d %s", w.Code, w.Body.String())
	}
	got := map[int]string{}
	for _, it := range s.app.Downloads.List() {
		if it.MediaID == 21 {
			got[it.Episode] = it.Mode
		}
	}
	if len(got) != 2 || got[1051] != "dub" || got[1052] != "dub" {
		t.Errorf("queued: %v", got)
	}
	if e := s.app.HubStore.AuditEvent(d.ID); e.Again.Queued == 0 || !e.Again.Dub {
		t.Errorf("not marked queued: %+v", e.Again)
	}
	// (The downloads may have failed already, here without ani-cli, and be
	// noted after it.)
	var again *anikohub.Event
	for _, e := range auditOf(t, s, "?group=downloads", signedIn) {
		if e.Kind == "again" {
			again = &e
		}
	}
	if again == nil || again.Text != "started downloading again (dubbed): 2 episodes of" || again.Title != "One Piece" || again.By != "dashboard" {
		t.Errorf("downloading again: %+v", again)
	}
	// Nothing to download again: only an opening.
	ncop := add("One Piece - NCOP2.mkv", 0, "nc")
	_ = do(s, "POST", "/api/library/delete", lan, fmt.Sprintf(`{"paths":[%q]}`, ncop), signedIn)
	last := auditOf(t, s, "?group=library", signedIn)[0]
	if last.Again != nil {
		t.Errorf("an opening to download again: %+v", last.Again)
	}
	if w := do(s, "POST", fmt.Sprintf("/api/hub/audit/%d/again", last.ID), lan, `{}`, signedIn); w.Code != http.StatusBadRequest {
		t.Errorf("again with nothing: %d", w.Code)
	}
	if w := do(s, "POST", "/api/library/trash", lan, body, signedIn); w.Code != http.StatusBadRequest {
		t.Errorf("the hub's trash: %d", w.Code)
	}
}

// Sign-ins, wrong passwords and every change from the dashboard are in the
// audit log (refused ones too); what the devices do isn't asked of it here.
// The server's log is kept on disk and read back.
func TestHubAuditLog(t *testing.T) {
	s, signedIn := signedInHub(t)
	lan := "192.168.1.20:5000"
	_ = do(s, "POST", "/api/auth/server-login", "192.168.1.40:5000", `{"password":"wrong"}`, nil)
	_ = do(s, "POST", "/api/auth/server-login", lan, `{"password":"hunter22"}`, nil)
	_ = do(s, "POST", "/api/library/scan", lan, `{"full":true}`, signedIn)
	_ = do(s, "POST", "/api/library/match", lan, `{"paths":["/nope.mkv"],"mediaId":0}`, signedIn)

	dash := auditOf(t, s, "?group=dashboard", signedIn)
	var texts []string
	for _, e := range dash {
		texts = append(texts, e.Kind+": "+e.IP+" "+e.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{
		"signin: 192.168.1.20 set the dashboard's password (the first time, with the setup code)",
		"signin-failed: 192.168.1.40 tried a wrong password",
		"signin: 192.168.1.20 signed in to the dashboard",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	lib := auditOf(t, s, "?group=library", signedIn)
	if len(lib) < 2 || lib[0].Kind != "library" || !lib[0].Failed || lib[0].Detail != "refused: mediaId and paths are required" {
		t.Errorf("the refused match: %+v", lib)
	}
	if lib[1].Kind != "scan" || lib[1].Text != "scanned everything again" || lib[1].By != "dashboard" {
		t.Errorf("the scan: %+v", lib[1])
	}
	// Searches and playing aren't changes.
	_ = do(s, "POST", "/api/anilist/search", lan, `{"query":"x"}`, signedIn)
	if all := auditOf(t, s, "?q=search", signedIn); len(all) != 0 {
		t.Errorf("a search noted: %+v", all)
	}
	if w := do(s, "GET", "/api/hub/audit?group=nope", lan, "", signedIn); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown filter: %d", w.Code)
	}
	if w := do(s, "GET", "/api/hub/audit", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("not signed in: %d", w.Code)
	}

	log.Printf("an audit test line, found again")
	w := do(s, "GET", "/api/hub/log?q=audit+test+line", lan, "", signedIn)
	var res struct {
		Lines  []string `json:"lines"`
		OnDisk bool     `json:"onDisk"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil || !res.OnDisk || len(res.Lines) != 1 {
		t.Fatalf("log: %d %s", w.Code, w.Body.String())
	}
	w = do(s, "GET", "/api/hub/log/download", lan, "", signedIn)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "an audit test line") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %.200s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/hub/log/download", lan, "", nil); w.Code == http.StatusOK {
		t.Error("the log for someone not signed in")
	}
}

func TestPathValue(t *testing.T) {
	if got := pathValue("POST /api/hub/devices/{id}", "/api/hub/devices/abc123", "id"); got != "abc123" {
		t.Errorf("got %q", got)
	}
	if got := pathValue("POST /api/sharing/peers/{id}/parental", "/api/sharing/peers/x9/parental", "id"); got != "x9" {
		t.Errorf("got %q", got)
	}
	if got := pathValue("PUT /api/settings", "/api/settings", "id"); got != "" {
		t.Errorf("got %q", got)
	}
}

// Not a hub: nothing is deleted for good.
func TestDeleteOnlyOnAHub(t *testing.T) {
	s := newTestServer(t)
	if w := do(s, "POST", "/api/library/delete", "127.0.0.1:5000", `{"paths":["/x.mkv"]}`, nil); w.Code != http.StatusNotFound {
		t.Errorf("delete on a computer: %d", w.Code)
	}
}
