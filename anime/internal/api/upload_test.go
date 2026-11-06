package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// A Aniko sends files to one that takes its downloads, a piece at a time,
// into the folder picked; only when it's allowed, and only into it.
func TestUploadToHost(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	lib := t.TempDir()
	cfg := host.app.Settings.Get()
	cfg.Library.Dir = lib
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	gid, hid := guest.app.Share.ID(), host.app.Share.ID()
	asked := func(ok func() bool) func() bool {
		return func() bool { do(guest, "GET", "/api/sharing?refresh=1", local, "", nil); return ok() }
	}
	targets := func() []uploadTarget {
		var out []uploadTarget
		w := do(guest, "GET", "/api/upload/targets", local, "", nil)
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if err := host.app.Share.SetAllowed(gid, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the library", asked(func() bool { return len(guest.app.Share.Libraries()) == 1 }))
	// Shared with, not allowed to download there: no uploads either.
	if got := targets(); len(got) != 0 {
		t.Fatalf("targets before it's allowed: %+v", got)
	}
	if w := do(guest, "POST", "/api/upload?"+uploadQuery(hid, 0, "a.mkv", 0, 1), local, "x", nil); w.Code != http.StatusForbidden {
		t.Fatalf("upload before it's allowed: %d %s", w.Code, w.Body)
	}
	yes := true
	if err := host.app.Share.SetGrants(gid, share.Grants{Downloads: &yes}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the host to take files", asked(func() bool { got := targets(); return len(got) == 1 && got[0].ID == hid }))

	var folders uploadFolders
	if w := do(guest, "GET", "/api/upload?target="+hid, local, "", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &folders) != nil || len(folders.Folders) != 1 || folders.Chunk == 0 {
		t.Fatalf("folders: %d %s", w.Code, w.Body)
	}
	// A file in two pieces, into a folder of its own.
	data := bytes.Repeat([]byte("kumobox!"), (folders.Chunk+4096)/8)
	rel := "Movie (2020)/Movie (2020).mkv"
	send := func(offset, end int) *httpResult {
		w := do(guest, "POST", "/api/upload?"+uploadQuery(hid, 0, rel, int64(offset), int64(len(data))), local, string(data[offset:end]), nil)
		return &httpResult{w.Code, w.Body.String()}
	}
	if r := send(0, folders.Chunk); r.code != http.StatusOK || !strings.Contains(r.body, `"done":false`) {
		t.Fatalf("first piece: %+v", r)
	}
	// Sent again: told where it stands.
	if r := send(0, 10); r.code != http.StatusConflict || !strings.Contains(r.body, `"have":`) {
		t.Fatalf("a piece sent again: %+v", r)
	}
	if r := send(folders.Chunk, len(data)); r.code != http.StatusOK || !strings.Contains(r.body, `"done":true`) {
		t.Fatalf("last piece: %+v", r)
	}
	got, err := os.ReadFile(filepath.Join(lib, "Movie (2020)", "Movie (2020).mkv"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the file on the host isn't what was sent: %v", err)
	}
	// Out of its folder: refused by the host.
	if w := do(guest, "POST", "/api/upload?"+uploadQuery(hid, 0, "../out.mkv", 0, 1), local, "x", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("out of the folder: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "POST", "/api/upload?"+uploadQuery(hid, 3, "b.mkv", 0, 1), local, "x", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a folder that isn't there: %d %s", w.Code, w.Body)
	}
	// Only this computer's user sends files; and the guest isn't a server,
	// so it takes none from the browser.
	if w := do(guest, "POST", "/api/upload?"+uploadQuery(hid, 0, "c.mkv", 0, 1), "192.168.1.20:5000", "x", nil); w.Code != http.StatusForbidden {
		t.Fatalf("from the network: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "POST", "/api/upload?"+uploadQuery("", 0, "c.mkv", 0, 1), local, "x", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("here, not a server: %d %s", w.Code, w.Body)
	}
}

type httpResult struct {
	code int
	body string
}

// mediaserver's anime side (a hub) takes files from its dashboard itself,
// and notes them in its audit log.
func TestUploadHere(t *testing.T) {
	s := newHubServer(t)
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir = lib
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/api/upload/targets", "127.0.0.1:5000", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"here":true`) {
		t.Fatalf("targets: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/upload?"+uploadQuery("", 0, "Show/Show - 01.mkv", 0, 5), "127.0.0.1:5000", "hello", nil); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if b, err := os.ReadFile(filepath.Join(lib, "Show", "Show - 01.mkv")); err != nil || string(b) != "hello" {
		t.Fatalf("the file: %q %v", b, err)
	}
	found := false
	for _, e := range s.app.HubStore.Activity() {
		if e.Kind == "upload" && strings.Contains(e.Text, "Show - 01.mkv") {
			found = true
		}
	}
	if !found {
		t.Fatalf("not in the audit log: %+v", s.app.HubStore.Activity())
	}
}

// The other sides' uploads go in the same audit log, saying which side.
func TestLogUploadFromOtherSides(t *testing.T) {
	s := newHubServer(t)
	r := httptest.NewRequest("POST", "/api/upload", nil)
	r.RemoteAddr = "192.168.1.20:5000"
	s.LogUpload(r, "Movies", "Laptop", "Film (2020)/Film.mkv", "/srv/movies/Film (2020)/Film.mkv", 42)
	s.LogUpload(r, "Music", "", "Band/Song.flac", "/srv/music/Band/Song.flac", 7)
	var got []string
	for _, e := range s.app.HubStore.Activity() {
		if e.Kind == "upload" {
			got = append(got, fmt.Sprintf("%s|%s|%s|%s|%d|%d", e.Device, e.By, e.IP, e.Text, e.Size, len(e.Files)))
		}
	}
	want := map[string]bool{
		"Laptop|||uploaded Film (2020)/Film.mkv to Movies|42|1":  true,
		"|hub|192.168.1.20|uploaded Band/Song.flac to Music|7|1": true,
	}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("audit log: %q", got)
	}
}
