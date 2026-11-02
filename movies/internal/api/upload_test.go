package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/share"
	fileup "github.com/v0-0x/mediaserver/movies/internal/upload"
)

// A Moviko sends files to one that takes its downloads, a piece at a time,
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
		return func() bool { do(guest, "POST", "/api/sharing/refresh", local, "{}", nil); return ok() }
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
	data := bytes.Repeat([]byte("moviko!!"), (folders.Chunk+4096)/8)
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

// mediaserver's movies side takes files from its signed-in browser itself.
func TestUploadHere(t *testing.T) {
	s := newTestServer(t)
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir = lib
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s.Owner = func(*http.Request) bool { return false }
	w := do(s, "GET", "/api/upload/targets", "127.0.0.1:5000", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"here":true`) {
		t.Fatalf("targets: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/upload?"+uploadQuery("", 0, "Show/S01E01.mkv", 0, 5), "127.0.0.1:5000", "hello", nil); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if b, err := os.ReadFile(filepath.Join(lib, "Show", "S01E01.mkv")); err != nil || string(b) != "hello" {
		t.Fatalf("the file: %q %v", b, err)
	}
}

// A piece sent again (its answer was lost) is answered with where the file
// stands, over the same kind of connection a browser uses: refused unread,
// the browser saw only a network error and couldn't carry on.
func TestUploadPieceSentAgain(t *testing.T) {
	s := newTestServer(t)
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir = lib
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s.Owner = func(*http.Request) bool { return false }
	srv := httptest.NewServer(s.middleware(s.mux))
	defer srv.Close()
	data := bytes.Repeat([]byte("x"), fileup.Chunk+1000)
	post := func(offset, end int) (int, map[string]any) {
		body := &countingReader{r: bytes.NewReader(data[offset:end])}
		req, _ := http.NewRequest("POST", srv.URL+"/api/upload?"+uploadQuery("", 0, "Film/Film.mkv", int64(offset), int64(len(data))), body)
		req.ContentLength = int64(end - offset)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("the connection broke: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		// Answered after the whole piece was taken (a browser gives up on
		// an answer that comes before).
		if body.n != int64(end-offset) {
			t.Fatalf("answered %d after %d of %d bytes", resp.StatusCode, body.n, end-offset)
		}
		return resp.StatusCode, out
	}
	if code, out := post(0, fileup.Chunk); code != http.StatusOK {
		t.Fatalf("first piece: %d %v", code, out)
	}
	code, out := post(0, fileup.Chunk)
	if code != http.StatusConflict || out["have"] != float64(fileup.Chunk) {
		t.Fatalf("the piece again: %d %v", code, out)
	}
	if code, out := post(fileup.Chunk, len(data)); code != http.StatusOK || out["done"] != true {
		t.Fatalf("the rest: %d %v", code, out)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
