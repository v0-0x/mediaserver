package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/history"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/share"
)

// sharingServer is an KumoBox with library sharing on, serving as a real one
// does with devices on the network: on every address, at the port its
// settings say, which it tells the others (its beacons, its requests).
// Where multicast works (Linux), the test KumoBoxes hear each other's beacons
// and may go to the address one says; it must answer there. Its URL is on
// 127.0.0.1.
func sharingServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := newTestServer(t)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(s.middleware(s.mux))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	srv.URL = fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	t.Cleanup(srv.Close)
	s.app.Share.Start()
	cfg := s.app.Settings.Get()
	cfg.Sharing.Enabled = true
	cfg.Server.Port = srv.Listener.Addr().(*net.TCPAddr).Port
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s, srv
}

// peerIn finds the other test KumoBox in what s lists. On a network with
// multicast (GitHub's Linux and macOS machines), other test KumoBoxes can be
// listed too.
func peerIn(s, other *Server) (share.PeerView, bool) {
	id := other.app.Share.ID()
	for _, p := range s.app.Share.Status().Peers {
		if p.ID == id {
			return p, true
		}
	}
	return share.PeerView{}, false
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// addEpisode puts a file in s's library.
func addEpisode(t *testing.T, s *Server, mediaID, episode int) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Show - 01.mkv")
	data := bytes.Repeat([]byte("kumobox-video-"), 2000)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &library.LocalFile{Path: path, Dir: dir, Name: filepath.Base(path), Size: int64(len(data)), MediaID: mediaID, Episode: episode, Kind: "main"}
	if err := s.app.Files.Save(f); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestLibrarySharing(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	path, data := addEpisode(t, host, 21, 1)
	hostID, guestID := host.app.Share.ID(), guest.app.Share.ID()

	// The guest adds the host by its address (no multicast needed).
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	// The host hears of the guest when it asks, and shares nothing yet.
	eventually(t, "the host to list the guest", func() bool {
		p, ok := peerIn(host, guest)
		return ok && p.ID == guestID && !p.Allowed
	})
	if libs := guest.app.Share.Libraries(); len(libs) != 0 {
		t.Fatalf("shared before the host allowed it: %+v", libs)
	}
	remote := share.RemotePath(hostID, path)
	if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {remote}}.Encode(), local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a file not shared: got %d", w.Code)
	}

	// The host shares its library with the guest.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID, local, `{"allowed":true}`, nil); w.Code != http.StatusOK {
		t.Fatalf("allow: %d %s", w.Code, w.Body)
	}
	var libs []share.SharedLibrary
	eventually(t, "the guest to get the host's library", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil) // asks again
		w := do(guest, "GET", "/api/sharing/libraries", local, "", nil)
		_ = json.Unmarshal(w.Body.Bytes(), &libs)
		return len(libs) == 1 && len(libs[0].Files) == 1
	})
	f := libs[0].Files[0]
	if f.Path != remote || f.Host != hostID || f.HostName == "" || f.MediaID != 21 || f.Episode != 1 || f.Dir != "" {
		t.Fatalf("shared file: %+v", f)
	}

	// The guest's player gets the file through the guest, ranges too.
	r := httptest.NewRequest("GET", "http://127.0.0.1:43311/api/local/file?"+url.Values{"path": {remote}}.Encode(), nil)
	r.RemoteAddr = local
	r.Header.Set("Range", "bytes=11-21")
	w := httptest.NewRecorder()
	guest.middleware(guest.mux).ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), data[11:22]) {
		t.Fatalf("range through the guest: %d %q", w.Code, w.Body.Bytes())
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "html") {
		t.Errorf("content type %q", ct)
	}

	// Only the files the host shares: not one it ignores, nor one not
	// matched to an anime, though in its library.
	for _, f := range []*library.LocalFile{{MediaID: 21, Episode: 2, Ignored: true}, {MediaID: 0}} {
		p, _ := addEpisode(t, host, 0, 0)
		f.Path, f.Dir, f.Name, f.Kind = p, filepath.Dir(p), filepath.Base(p), "main"
		if err := host.app.Files.Save(f); err != nil {
			t.Fatal(err)
		}
		if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {share.RemotePath(hostID, p)}}.Encode(), local, "", nil); w.Code == http.StatusOK || w.Code == http.StatusPartialContent {
			t.Errorf("a file the host doesn't share was read: %+v", f)
		}
	}

	// A request the guest made can't be sent again by someone who saw it.
	req, _ := http.NewRequest("GET", hostSrv.URL+"/api/peer/local/file?"+url.Values{"path": {path}}.Encode(), nil)
	if err := guest.app.Share.Sign(req, hostID, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{http.StatusOK, http.StatusUnauthorized} {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("request sent %d time(s): %d", i+1, resp.StatusCode)
		}
		if i == 0 && resp.Header.Get(share.HeaderReply) == "" {
			t.Error("the host's answer doesn't prove it's the host")
		}
	}
	// Another KumoBox is never this computer, whatever it may do.
	trusted := false
	host.mux.HandleFunc("GET /api/peer/zz-trusted", shared(func(w http.ResponseWriter, r *http.Request) { trusted = isTrusted(r) }))
	req, _ = http.NewRequest("GET", hostSrv.URL+"/api/peer/zz-trusted", nil)
	if err := guest.app.Share.Sign(req, hostID, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK || trusted {
		t.Errorf("a peer's request is trusted as this computer's: %v %v", resp, err)
	}

	// Watching on the guest is the guest's: its history (and its list,
	// Letterboxd or not), never the host's.
	if w := do(guest, "POST", "/api/playback/progress", local, `{"mediaId":21,"episode":1,"position":300,"duration":1420,"source":"local"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", w.Code, w.Body)
	}
	if e := guest.app.History.Get(21, 1); e == nil || e.Position != 300 {
		t.Errorf("the guest's history: %+v", e)
	}
	if h := host.app.History.Recent(10); len(h) != 0 {
		t.Errorf("the host's history changed: %+v", h)
	}
	if st := host.app.Player.Status(); st != nil {
		t.Errorf("the host's player changed: %+v", st)
	}

	// Remembered after a restart of the host's sharing.
	host.app.Share.Stop()
	host.app.Share.Start()
	if p, ok := peerIn(host, guest); !ok || !p.Allowed {
		t.Fatalf("the host forgot it shares with the guest: %+v", host.app.Share.Status().Peers)
	}

	// The host stops sharing: the guest's library goes.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID, local, `{"allowed":false}`, nil); w.Code != http.StatusOK {
		t.Fatalf("disallow: %d", w.Code)
	}
	eventually(t, "the guest to lose the host's library", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		w := do(guest, "GET", "/api/sharing/libraries", local, "", nil)
		_ = json.Unmarshal(w.Body.Bytes(), &libs)
		return len(libs) == 0
	})
	if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {remote}}.Encode(), local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("after sharing stopped: got %d", w.Code)
	}
}

func TestPeerRequestsChecked(t *testing.T) {
	const lan, public = "192.168.1.30:5000", "8.8.8.8:5000"
	host, hostSrv := sharingServer(t)
	path, _ := addEpisode(t, host, 21, 1)
	hostID := host.app.Share.ID()
	var who share.Hello
	_ = json.Unmarshal(do(host, "GET", "/api/peer/whoami", lan, "", nil).Body.Bytes(), &who)
	if who.ID != hostID || who.Key == "" {
		t.Fatalf("whoami: %+v", who)
	}
	if w := do(host, "GET", "/api/peer/whoami", public, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("from the internet: %d", w.Code)
	}
	// Sharing on doesn't open the rest to the network.
	if w := do(host, "GET", "/api/status", lan, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("the app from the network with LAN access off: %d", w.Code)
	}
	if w := do(host, "GET", "/api/peer/files", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", w.Code)
	}
	if w := do(host, "GET", "/api/peer/whoami", lan, "", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); w.Code != http.StatusForbidden {
		t.Errorf("from a website: %d", w.Code)
	}

	// Another KumoBox, which the host doesn't share with: it may ask, not take.
	other, _ := sharingServer(t)
	signed := func(target string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", hostSrv.URL+target, nil)
		if err := other.app.Share.Sign(req, hostID, who.Key); err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := signed("/api/peer/hello")
	var hello share.Hello
	_ = json.NewDecoder(resp.Body).Decode(&hello)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || hello.Shares || hello.Files != "" {
		t.Errorf("hello from an KumoBox not allowed: %d %+v", resp.StatusCode, hello)
	}
	for _, target := range []string{"/api/peer/files", "/api/peer/local/file?" + url.Values{"path": {path}}.Encode()} {
		resp := signed(target)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s from an KumoBox not allowed: %d", target, resp.StatusCode)
		}
	}
	// A token for another host is refused.
	req, _ := http.NewRequest("GET", hostSrv.URL+"/api/peer/hello", nil)
	_ = other.app.Share.Sign(req, other.app.Share.ID(), other.app.Share.Whoami().Key)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("a token made for another KumoBox: %d", resp.StatusCode)
		}
	}

	// Allowed: files, and only the library's.
	if err := host.app.Share.SetAllowed(other.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	resp = signed("/api/peer/files")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Show - 01.mkv") {
		t.Errorf("files when allowed: %d %s", resp.StatusCode, body)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	resp = signed("/api/peer/local/file?" + url.Values{"path": {outside}}.Encode())
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "secret") {
		t.Errorf("a file outside the library: %d %s", resp.StatusCode, body)
	}

	// Sharing off: nothing answers.
	cfg := host.app.Settings.Get()
	cfg.Sharing.Enabled = false
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(host, "GET", "/api/peer/whoami", lan, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("whoami with sharing off: %d", w.Code)
	}
}

func TestSharingSettingsOnlyFromThisComputer(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	const lan = "192.168.1.30:5000"
	if w := do(s, "POST", "/api/sharing/peers/abc", lan, `{"allowed":true}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("allowing from the network: %d", w.Code)
	}
	if w := do(s, "PUT", "/api/settings", lan, `{"sharing":{"enabled":true,"name":"x"}}`, nil); w.Code != http.StatusOK {
		t.Fatalf("settings from the network: %d %s", w.Code, w.Body)
	}
	if got := s.app.Settings.Get().Sharing; got.Enabled || got.Name != "" {
		t.Errorf("the network turned sharing on: %+v", got)
	}
}

// Two KumoBoxes on the same Letterboxd account keep their watch history in step:
// where an episode was stopped on one, it goes on on the other. Not with
// another account, nor with an KumoBox that doesn't share with this one.
func TestHistoryFollowsTheAccount(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	user := func(s *Server, id int) { s.app.Share.User = func() int { return id } }
	user(host, 42)
	user(guest, 42)
	if err := host.app.History.Save(history.Entry{MediaID: 21, Episode: 3, Position: 600, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	synced := func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil) // asks again
		e := guest.app.History.Get(21, 3)
		return e != nil && e.Position == 600
	}

	// Not shared with: nothing comes over.
	time.Sleep(500 * time.Millisecond)
	if synced() {
		t.Fatal("history came from an KumoBox that doesn't share with this one")
	}
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the history to come over", synced)

	// Watching the host's files on the guest: the host's Continue watching
	// has it as it happens (the host only shares one way, nothing asked).
	if err := guest.app.History.Save(history.Entry{MediaID: 21, Episode: 5, Position: 400, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for e := host.app.History.Get(21, 5); e == nil || e.Position != 400; e = host.app.History.Get(21, 5) {
		if time.Now().After(deadline) {
			t.Fatal("the position watched on the guest didn't reach the host")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Another Letterboxd account on the guest: newer positions stay home.
	user(guest, 43)
	if err := host.app.History.Save(history.Entry{MediaID: 21, Episode: 4, Position: 300, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		time.Sleep(300 * time.Millisecond)
	}
	if e := guest.app.History.Get(21, 4); e != nil {
		t.Errorf("history went to another Letterboxd account: %+v", e)
	}
	if err := guest.app.History.Save(history.Entry{MediaID: 21, Episode: 6, Position: 200, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if e := host.app.History.Get(21, 6); e != nil {
		t.Errorf("a position went to another Letterboxd account: %+v", e)
	}
}

// A host lets an KumoBox download onto it: it becomes where that KumoBox's
// downloads go, and they're sent there.
func TestDownloadsToHost(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	gid, hid := guest.app.Share.ID(), host.app.Share.ID()
	asked := func(ok func() bool) func() bool {
		return func() bool { do(guest, "POST", "/api/sharing/refresh", local, "{}", nil); return ok() }
	}
	if err := host.app.Share.SetAllowed(gid, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the library", asked(func() bool { return len(guest.app.Share.Libraries()) == 1 }))
	if to := guest.app.Settings.Get().Sharing.DownloadTo; to != "" {
		t.Fatalf("downloads go to %q before the host allowed it", to)
	}
	// Not allowed: an KumoBox can't download onto the host by itself.
	body := `{"mediaId":21,"uris":["magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"]}`
	req, _ := http.NewRequest("POST", hostSrv.URL+"/api/peer/downloads/torrent", strings.NewReader(body))
	if err := guest.app.Share.Sign(req, hid, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("download without permission: %v %v", resp, err)
	}

	yes := true
	if err := host.app.Share.SetGrants(gid, share.Grants{Downloads: &yes}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "downloads to go to the host", asked(func() bool { return guest.app.Settings.Get().Sharing.DownloadTo == hid }))
	// Not more than so many at once.
	uris := make([]string, 51)
	for i := range uris {
		uris[i] = fmt.Sprintf("magnet:?xt=urn:btih:%040d", i)
	}
	many, _ := json.Marshal(map[string]any{"mediaId": 21, "uris": uris})
	req, _ = http.NewRequest("POST", hostSrv.URL+"/api/peer/downloads/torrent", bytes.NewReader(many))
	if err := guest.app.Share.Sign(req, hid, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("51 torrents at once: %v %v", resp, err)
	}

	// A torrent added on the guest goes to the host's torrent client (none
	// here: its answer says so, from the host).
	w := do(guest, "POST", "/api/torrent-client/add", local, `{"magnet":"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567","mediaId":0}`, nil)
	// The host's library, by its ID: on a network with multicast, other
	// test KumoBoxes can be listed too (see peerIn).
	hostName := ""
	for _, l := range guest.app.Share.Libraries() {
		if l.Host.ID == hid {
			hostName = l.Host.Name
		}
	}
	if w.Code == http.StatusOK || hostName == "" || !strings.HasPrefix(errorOf(w), hostName+": ") {
		t.Fatalf("not sent to the host %q: %d %s", hostName, w.Code, w.Body)
	}
	// Its torrents are the host's: the client, the list, the actions.
	var st struct {
		Host   string `json:"host"`
		Client string `json:"client"`
	}
	if w := do(guest, "GET", "/api/torrent-client/status", local, "", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil || st.Host != hostName {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "POST", "/api/torrent-client/action", local, `{"hashes":["ab"],"action":"pause"}`, nil); !strings.HasPrefix(errorOf(w), hostName+": ") {
		t.Fatalf("action not sent to the host: %d %s", w.Code, w.Body)
	}
	// Only for an KumoBox allowed to download there.
	other, _ := sharingServer(t)
	req, _ = http.NewRequest("GET", hostSrv.URL+"/api/peer/torrents/list", nil)
	if err := other.app.Share.Sign(req, hid, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode == http.StatusOK {
		t.Fatalf("another KumoBox listed the host's torrents: %v %v", resp, err)
	}

	// Its Downloads page shows the host's downloads; another KumoBox can't
	// read them.
	var dl struct {
		Host  string            `json:"host"`
		Items []json.RawMessage `json:"items"`
		Error string            `json:"error"`
	}
	// By its name now: another test KumoBox with the same computer name
	// (other, above) has the host's name show its ID too.
	for _, l := range guest.app.Share.Libraries() {
		if l.Host.ID == hid {
			hostName = l.Host.Name
		}
	}
	if w := do(guest, "GET", "/api/downloads/host", local, "", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &dl) != nil || dl.Host != hostName || dl.Error != "" {
		t.Fatalf("host downloads: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "POST", "/api/downloads/host/nope/cancel", local, "{}", nil); w.Code != http.StatusOK {
		t.Fatalf("cancel on the host: %d %s", w.Code, w.Body)
	}
	req, _ = http.NewRequest("GET", hostSrv.URL+"/api/peer/downloads/list", nil)
	if err := other.app.Share.Sign(req, hid, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode == http.StatusOK {
		t.Fatalf("another KumoBox listed the host's downloads: %v %v", resp, err)
	}

	// Not allowed any more: saved here again.
	no := false
	if err := host.app.Share.SetGrants(gid, share.Grants{Downloads: &no}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "downloads to stay here", asked(func() bool { return guest.app.Settings.Get().Sharing.DownloadTo == "" }))
	st.Host = ""
	if w := do(guest, "GET", "/api/torrent-client/status", local, "", nil); json.Unmarshal(w.Body.Bytes(), &st) != nil || st.Host != "" {
		t.Fatalf("status still the host's: %s", w.Body)
	}
	if w := do(guest, "GET", "/api/downloads/host", local, "", nil); json.Unmarshal(w.Body.Bytes(), &dl) != nil || dl.Host != "" {
		t.Fatalf("host downloads after: %s", w.Body)
	}
	// Its own client again, the body read there.
	if w := do(guest, "POST", "/api/torrent-client/action", local, `{"hashes":["ab"],"action":"pause"}`, nil); strings.Contains(errorOf(w), "JSON") || strings.HasPrefix(errorOf(w), hostName) {
		t.Fatalf("action: %d %s", w.Code, w.Body)
	}
}

func errorOf(w *httptest.ResponseRecorder) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	return e.Error
}

// Home's Sync: two paired KumoBoxes on the same AniList account take each
// other's newer positions now, both ways, also ones the usual exchange
// missed (saved while the other was away).
func TestSyncNow(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, guestSrv := sharingServer(t)
	for _, s := range []*Server{host, guest} {
		s.app.Share.User = func() int { return 42 }
	}
	// Where the guest answers, as it tells the host.
	u, _ := url.Parse(guestSrv.URL)
	cfg := guest.app.Settings.Get()
	cfg.Server.Port, _ = strconv.Atoi(u.Port())
	if _, err := guest.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the library", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		return len(guest.app.Share.Libraries()) == 1
	})
	// Saved on each while the other didn't hear of it.
	now := time.Now().Unix()
	if _, err := host.app.History.Merge(history.Entry{MediaID: 21, Episode: 5, Position: 300, Duration: 1420, Source: "local", UpdatedAt: now - 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.app.History.Merge(history.Entry{MediaID: 21, Episode: 6, Position: 700, Duration: 1420, Source: "local", UpdatedAt: now - 50}); err != nil {
		t.Fatal(err)
	}
	var st struct {
		Paired []string `json:"paired"`
	}
	if w := do(host, "GET", "/api/status", local, "", nil); json.Unmarshal(w.Body.Bytes(), &st) != nil || len(st.Paired) != 1 {
		t.Fatalf("the host's paired KumoBoxes: %s", w.Body)
	}

	var res struct {
		Devices     []string `json:"devices"`
		Unreachable []string `json:"unreachable"`
		Updated     int      `json:"updated"`
	}
	w := do(host, "POST", "/api/sharing/sync", local, "{}", nil)
	// Updated: 1, the guest's position; or 0 when the guest's own exchange
	// (it asks every few seconds and sends its positions) brought it first.
	// Either way both have both positions after (below).
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Devices) != 1 || len(res.Unreachable) != 0 || res.Updated > 1 {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	if e := host.app.History.Get(21, 6); e == nil || e.Position != 700 {
		t.Errorf("the guest's position didn't come to the host: %+v", e)
	}
	if e := guest.app.History.Get(21, 5); e == nil || e.Position != 300 {
		t.Errorf("the host's position didn't go to the guest: %+v", e)
	}
	// And from the guest's side.
	if _, err := host.app.History.Merge(history.Entry{MediaID: 21, Episode: 7, Position: 90, Duration: 1420, Source: "local", UpdatedAt: now - 10}); err != nil {
		t.Fatal(err)
	}
	if w := do(guest, "POST", "/api/sharing/sync", local, "{}", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil || res.Updated != 1 {
		t.Fatalf("sync from the guest: %d %s", w.Code, w.Body)
	}
	if e := guest.app.History.Get(21, 7); e == nil || e.Position != 90 {
		t.Errorf("the guest's sync didn't take the host's position: %+v", e)
	}

	// An KumoBox that isn't paired gets no history.
	other, _ := sharingServer(t)
	other.app.Share.User = func() int { return 42 }
	req, _ := http.NewRequest("GET", hostSrv.URL+"/api/peer/history?since=0", nil)
	if err := other.app.Share.Sign(req, host.app.Share.ID(), host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("an unpaired KumoBox read the history: %v %v", resp, err)
	}
}

// What the usual exchange missed comes over later by itself: a position
// saved on the guest before the host shared with it, one the host took
// late from a third KumoBox (with an older time), and the last one saved as
// the guest quits.
func TestHistoryCatchesUp(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	for _, s := range []*Server{host, guest} {
		s.app.Share.User = func() int { return 42 }
	}
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	asked := func(ok func() bool) func() bool {
		return func() bool { do(guest, "POST", "/api/sharing/refresh", local, "{}", nil); return ok() }
	}
	has := func(s *Server, ep int, pos float64) func() bool {
		return func() bool { e := s.app.History.Get(21, ep); return e != nil && e.Position == pos }
	}

	// Saved on the guest while the host didn't share with it: not sent then.
	if err := guest.app.History.Save(history.Entry{MediaID: 21, Episode: 8, Position: 500, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if has(host, 8, 500)() {
		t.Fatal("sent to an KumoBox that doesn't share with this one")
	}
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the missed position to reach the host", asked(has(host, 8, 500)))

	// Taken by the host from another KumoBox after the guest had asked, with
	// an hour-old time: the guest still gets it.
	if _, err := host.app.History.Merge(history.Entry{MediaID: 21, Episode: 9, Position: 250, Duration: 1420, Source: "local", UpdatedAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the late position to reach the guest", asked(has(guest, 9, 250)))

	// Saved as the guest quits: sent before it stops, not 2 seconds later.
	if err := guest.app.History.Save(history.Entry{MediaID: 21, Episode: 10, Position: 610, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	guest.app.Share.Stop()
	if !has(host, 10, 610)() {
		t.Fatal("the last position didn't go before the guest stopped")
	}
}

// Forgetting a host leaves nothing of it: downloads don't go there any
// more. Not sharing with an KumoBox any more drops what came with it, which
// sharing again doesn't give back.
func TestForgetAndUnshareDropGrants(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	gid, hid := guest.app.Share.ID(), host.app.Share.ID()
	yes := true
	if err := host.app.Share.SetGrants(gid, share.Grants{Allowed: &yes, Downloads: &yes}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "downloads to go to the host", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		return guest.app.Settings.Get().Sharing.DownloadTo == hid
	})
	guest.app.Share.Forget(hid)
	if to := guest.app.Settings.Get().Sharing.DownloadTo; to != "" {
		t.Errorf("downloads still go to a forgotten KumoBox: %q", to)
	}

	if err := host.app.Share.SetAllowed(gid, false); err != nil {
		t.Fatal(err)
	}
	if err := host.app.Share.SetAllowed(gid, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range host.app.Share.Status().Peers {
		if p.ID == gid && p.Downloads {
			t.Errorf("shared again with what came before: %+v", p)
		}
	}
}

// The player's notes (what went wrong) go to the log here, and for a
// shared file to its host's too, where it can be looked into; from an KumoBox
// the host doesn't share with, nothing.
func TestPlayerNoteReachesTheHost(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	// After the servers: they log to their own Logs page too.
	var mu sync.Mutex
	var logged bytes.Buffer
	log.SetOutput(lockedWriter{&mu, &logged})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	seen := func(s string) bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(logged.String(), s) }
	path, _ := addEpisode(t, host, 21, 1)
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	remote := share.RemotePath(host.app.Share.ID(), path)

	// Not shared with yet: only the guest's log.
	note := func(path, text string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"path": path, "text": text})
		return do(guest, "POST", "/api/playback/note", local, string(b), nil)
	}
	if w := note(remote, "first\nnote"); w.Code != http.StatusOK {
		t.Fatalf("note: %d %s", w.Code, w.Body)
	}
	eventually(t, "the note in the guest's log", func() bool { return seen("player: first note") })
	time.Sleep(time.Second)
	if seen("sharing: the player on") {
		t.Fatal("an KumoBox the host doesn't share with wrote to its log")
	}

	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the library", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		return len(guest.app.Share.Libraries()) == 1
	})
	time.Sleep(2100 * time.Millisecond) // one note every 2 seconds
	note(remote, "no picture")
	eventually(t, "the note in the host's log", func() bool { return seen(": no picture") && seen("sharing: the player on") })
	// Not more than one every 2 seconds.
	note("", "too soon")
	time.Sleep(300 * time.Millisecond)
	if seen("too soon") {
		t.Error("notes aren't limited")
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// A position saved on the TV that didn't reach the computer (it was off):
// the computer takes it by itself (here, as the app opens again), without
// Sync; not when that's turned off. Within a second or two: the TV's own
// questions (every 30s) don't come in between.
func TestAutoSyncWhenBack(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, guestSrv := sharingServer(t)
	for _, s := range []*Server{host, guest} {
		s.app.Share.User = func() int { return 42 }
	}
	var mu sync.Mutex
	refreshed := 0
	host.app.Share.OnWatchedElsewhere = func([]history.Entry) { mu.Lock(); refreshed++; mu.Unlock() }
	u, _ := url.Parse(guestSrv.URL)
	cfg := guest.app.Settings.Get()
	cfg.Server.Port, _ = strconv.Atoi(u.Port())
	if _, err := guest.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the library", func() bool {
		do(guest, "POST", "/api/sharing/refresh", local, "{}", nil)
		return len(guest.app.Share.Libraries()) == 1
	})
	time.Sleep(500 * time.Millisecond)
	// Saved on the TV, not sent (as when the computer was off).
	if _, err := guest.app.History.Merge(history.Entry{MediaID: 21, Episode: 9, Position: 1400, Duration: 1420, Source: "local", UpdatedAt: time.Now().Unix() - 30}); err != nil {
		t.Fatal(err)
	}
	has := func() bool { e := host.app.History.Get(21, 9); return e != nil && e.Position == 1400 }

	// Turned off: nothing by itself.
	cfg = host.app.Settings.Get()
	cfg.Sharing.AutoSync = false
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(host, "POST", "/api/sharing/autosync", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("autosync: %d %s", w.Code, w.Body)
	}
	time.Sleep(1500 * time.Millisecond)
	if has() {
		t.Fatal("synced while turned off")
	}

	cfg.Sharing.AutoSync = true
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	do(host, "POST", "/api/sharing/autosync", local, "", nil)
	deadline := time.Now().Add(5 * time.Second)
	for !has() {
		if time.Now().After(deadline) {
			t.Fatal("the TV's position didn't reach the computer")
		}
		time.Sleep(100 * time.Millisecond)
	}
	eventually(t, "the episode finished on the TV to move the list on", func() bool { mu.Lock(); defer mu.Unlock(); return refreshed > 0 })
	// Not again a moment later.
	if _, err := guest.app.History.Merge(history.Entry{MediaID: 21, Episode: 10, Position: 300, Duration: 1420, Source: "local", UpdatedAt: time.Now().Unix() - 20}); err != nil {
		t.Fatal(err)
	}
	do(host, "POST", "/api/sharing/autosync", local, "", nil)
	time.Sleep(1500 * time.Millisecond)
	if e := host.app.History.Get(21, 10); e != nil {
		t.Errorf("synced again at once: %+v", e)
	}
}
