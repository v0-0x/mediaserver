package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/music/internal/peer"
)

// id3 is an MP3 file's start with ID3v2.4 tags (UTF-8 text frames) and a
// little of what would be the sound.
func id3(frames map[string]string) []byte {
	var body bytes.Buffer
	for _, id := range []string{"TIT2", "TPE1", "TALB", "TPE2", "TRCK", "TPOS"} {
		v, ok := frames[id]
		if !ok {
			continue
		}
		data := append([]byte{3}, v...)
		body.WriteString(id)
		body.Write(syncsafe(len(data)))
		body.Write([]byte{0, 0})
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("ID3")
	out.Write([]byte{4, 0, 0})
	out.Write(syncsafe(body.Len()))
	out.Write(body.Bytes())
	out.Write(bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x00}, 512))
	return out.Bytes()
}

func syncsafe(n int) []byte {
	return []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}

// flac is a FLAC file's start: its stream info, Vorbis comments and a
// cover.
func flac(comments []string, cover []byte) []byte {
	var out bytes.Buffer
	out.WriteString("fLaC")
	block := func(typ byte, last bool, data []byte) {
		if last {
			typ |= 0x80
		}
		out.WriteByte(typ)
		out.Write([]byte{byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))})
		out.Write(data)
	}
	info := make([]byte, 34)
	binary.BigEndian.PutUint16(info[0:], 4096)
	binary.BigEndian.PutUint16(info[2:], 4096)
	// 44100 Hz, 2 channels, 16 bits, 30 s.
	rate, total := uint64(44100), uint64(44100*30)
	packed := rate<<44 | 1<<41 | 15<<36 | total
	binary.BigEndian.PutUint64(info[10:], packed)
	block(0, false, info)
	var vc bytes.Buffer
	le := func(n int) { _ = binary.Write(&vc, binary.LittleEndian, uint32(n)) }
	le(len("test"))
	vc.WriteString("test")
	le(len(comments))
	for _, c := range comments {
		le(len(c))
		vc.WriteString(c)
	}
	block(4, cover == nil, vc.Bytes())
	if cover != nil {
		var pic bytes.Buffer
		be := func(n int) { _ = binary.Write(&pic, binary.BigEndian, uint32(n)) }
		be(3)
		be(len("image/png"))
		pic.WriteString("image/png")
		be(0)
		be(1)
		be(1)
		be(24)
		be(0)
		be(len(cover))
		pic.Write(cover)
		block(6, true, pic.Bytes())
	}
	out.Write(bytes.Repeat([]byte{0xff, 0xf8, 0x69, 0x08}, 256))
	return out.Bytes()
}

var png = []byte("\x89PNG\r\n\x1a\nnot really a picture")

// testServer is a music side with a library of three songs, its devices'
// port and the dashboard's handler.
type testServer struct {
	t        *testing.T
	s        *Server
	music    string
	peers    *httptest.Server
	dash     http.Handler
	hostPub  []byte
	hostID   string
	lastResp *http.Response
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	dir, music := t.TempDir(), t.TempDir()
	write := func(rel string, data []byte) {
		p := filepath.Join(music, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Band/First Album/01 Opening.mp3", id3(map[string]string{"TIT2": "Opening", "TPE1": "Band", "TALB": "First Album", "TRCK": "1/9"}))
	write("Band/First Album/02 Second.flac", flac([]string{"TITLE=Second", "ARTIST=Band", "ALBUM=First Album", "TRACKNUMBER=2", "DISCNUMBER=1"}, png))
	write("Loose/03 - Someone - A Song.mp3", bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x00}, 64))
	write("Loose/.hidden.mp3", []byte("x"))
	write("Loose/notes.txt", []byte("x"))
	write("Loose/cover.jpg", []byte("\xff\xd8\xff a jpeg"))

	s, err := New(Options{DataDir: dir, Version: "1.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.lib.AddFolder(music); err != nil {
		t.Fatal(err)
	}
	s.lib.Scan(context.Background())
	ts := httptest.NewServer(s.peerHandler())
	t.Cleanup(ts.Close)
	pub, _ := peer.ParsePublicKey(s.id.ID, s.id.PublicKey())
	return &testServer{t: t, s: s, music: music, peers: ts, dash: s.Handler(), hostPub: pub, hostID: s.id.ID}
}

// device is a Utako, as the server sees it.
func (ts *testServer) device(name string) *peer.Identity {
	id, err := peer.Load(filepath.Join(ts.t.TempDir(), "id.json"))
	if err != nil {
		ts.t.Fatal(err)
	}
	_ = name
	return id
}

// ask sends a signed request, as Utako does, and checks a 2xx answer is
// signed by the server.
func (ts *testServer) ask(dev *peer.Identity, name, method, uri string, body []byte, ticket bool, header ...string) (int, []byte) {
	ts.t.Helper()
	req, _ := http.NewRequest(method, ts.peers.URL+uri, bytes.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	nonce, err := dev.Sign(req, body, ts.hostID, ts.hostPub, name, time.Now(), ticket)
	if err != nil {
		ts.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 300 {
		if err := dev.CheckReply(ts.hostID, ts.hostPub, nonce, resp); err != nil {
			ts.t.Fatalf("%s %s: %v", method, uri, err)
		}
	}
	ts.lastResp = resp
	return resp.StatusCode, raw
}

// dashboard asks the dashboard's API, as the anime side passes it on.
func (ts *testServer) dashboard(method, uri string, body any) (int, []byte) {
	ts.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, uri, rd)
	r.RemoteAddr = "127.0.0.1:0"
	w := httptest.NewRecorder()
	ts.dash.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

type libraryAnswer struct {
	Version string `json:"version"`
	Tracks  []struct {
		Path, Title, Artist, Album string
		TrackNo                    int  `json:"trackNo"`
		DiscNo                     int  `json:"discNo"`
		HasArt                     bool `json:"hasArt"`
		AddedAt                    int64
	} `json:"tracks"`
}

func TestUtakoAndTheMusicSide(t *testing.T) {
	ts := newTestServer(t)
	dev := ts.device("Utako on test")
	const name = "Utako on test, with a comma"

	// Who's there, unsigned.
	resp, err := http.Get(ts.peers.URL + "/api/peer/whoami")
	if err != nil {
		t.Fatal(err)
	}
	var who map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&who)
	resp.Body.Close()
	if who["id"] != ts.hostID || who["role"] != "hub" || peer.Fingerprint(ts.hostPub) != who["id"] {
		t.Fatalf("whoami %v", who)
	}
	// Unsigned elsewhere: refused.
	if resp, _ := http.Get(ts.peers.URL + "/api/peer/hello"); resp.StatusCode != 401 {
		t.Fatalf("unsigned hello: %d", resp.StatusCode)
	}

	// Not let in yet: hello says so, the rest is refused.
	code, raw := ts.ask(dev, name, "GET", "/api/peer/hello", nil, false)
	var hello struct {
		ID      string `json:"id"`
		Shares  bool   `json:"shares"`
		Uploads bool   `json:"uploads"`
		Files   string `json:"files"`
	}
	if json.Unmarshal(raw, &hello); code != 200 || hello.Shares || hello.ID != ts.hostID {
		t.Fatalf("hello %d %s", code, raw)
	}
	if code, raw := ts.ask(dev, name, "GET", "/api/peer/music/library", nil, false); code != 403 || !strings.Contains(string(raw), name) {
		t.Fatalf("library before it's allowed: %d %s", code, raw)
	}

	// The dashboard sees it, and lets it in.
	code, raw = ts.dashboard("GET", "/api/devices", nil)
	var devs []deviceView
	if json.Unmarshal(raw, &devs); code != 200 || len(devs) != 1 || devs[0].Name != name || devs[0].ID != dev.ID || !devs[0].Online || devs[0].Allowed {
		t.Fatalf("devices %d %s", code, raw)
	}
	if code, raw := ts.dashboard("POST", "/api/devices/"+dev.ID, map[string]bool{"allowed": true}); code != 200 {
		t.Fatalf("allow: %d %s", code, raw)
	}
	_, raw = ts.ask(dev, name, "GET", "/api/peer/hello", nil, false)
	if json.Unmarshal(raw, &hello); !hello.Shares || hello.Uploads || hello.Files == "" {
		t.Fatalf("hello once allowed: %s", raw)
	}

	// The library, with its tags (and a guess from a file's name).
	code, raw = ts.ask(dev, name, "GET", "/api/peer/music/library", nil, false)
	var lib libraryAnswer
	if json.Unmarshal(raw, &lib); code != 200 || len(lib.Tracks) != 3 || lib.Version != hello.Files {
		t.Fatalf("library %d %s", code, raw)
	}
	byTitle := map[string]int{}
	for i, tr := range lib.Tracks {
		byTitle[tr.Title] = i
	}
	op, sec, song := lib.Tracks[byTitle["Opening"]], lib.Tracks[byTitle["Second"]], lib.Tracks[byTitle["A Song"]]
	if op.Artist != "Band" || op.Album != "First Album" || op.TrackNo != 1 || op.HasArt {
		t.Fatalf("mp3 %+v", op)
	}
	if sec.TrackNo != 2 || sec.DiscNo != 1 || !sec.HasArt || sec.AddedAt == 0 {
		t.Fatalf("flac %+v", sec)
	}
	if song.Artist != "Someone" || song.TrackNo != 3 || !song.HasArt {
		t.Fatalf("guessed %+v", song)
	}

	// A song with a ticket, from its start and from the middle, again and
	// again; the same ticket doesn't open another one.
	uri := "/api/peer/local/file?path=" + url.QueryEscape(sec.Path)
	req, _ := http.NewRequest("GET", ts.peers.URL+uri, nil)
	if _, err := dev.Sign(req, nil, ts.hostID, ts.hostPub, name, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	ticket := req.Header.Clone()
	whole, _ := os.ReadFile(sec.Path)
	for i, rng := range []string{"", "bytes=100-", "bytes=100-"} {
		req, _ := http.NewRequest("GET", ts.peers.URL+uri, nil)
		req.Header = ticket.Clone()
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want, status := whole, 200
		if rng != "" {
			want, status = whole[100:], 206
		}
		if resp.StatusCode != status || !bytes.Equal(got, want) || resp.Header.Get("Content-Type") != "audio/flac" {
			t.Fatalf("stream %d: %d %q", i, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	req, _ = http.NewRequest("GET", ts.peers.URL+"/api/peer/local/file?path="+url.QueryEscape(op.Path), nil)
	req.Header = ticket.Clone()
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("a ticket for another song: %d", resp.StatusCode)
	}
	// Only songs of the library.
	if code, _ := ts.ask(dev, name, "GET", "/api/peer/local/file?path="+url.QueryEscape(filepath.Join(ts.music, "Loose", "notes.txt")), nil, true); code != 404 {
		t.Fatalf("not a song: %d", code)
	}
	if code, _ := ts.ask(dev, name, "GET", "/api/peer/local/file?path=%2Fetc%2Fpasswd", nil, true); code != 404 {
		t.Fatalf("outside the library: %d", code)
	}
	// What it plays shows on the dashboard.
	_, raw = ts.dashboard("GET", "/api/status", nil)
	var st struct {
		Songs     int          `json:"songs"`
		Albums    int          `json:"albums"`
		Artists   int          `json:"artists"`
		Online    int          `json:"online"`
		Listening []deviceView `json:"listening"`
	}
	if json.Unmarshal(raw, &st); st.Songs != 3 || st.Albums != 1 || st.Artists != 2 || st.Online != 1 || len(st.Listening) != 1 || st.Listening[0].Playing.Title != "Second" {
		t.Fatalf("status %s", raw)
	}

	// Covers: in the file, by the file, none.
	if code, raw := ts.ask(dev, name, "GET", "/api/peer/music/art?path="+url.QueryEscape(sec.Path), nil, false); code != 200 || !bytes.Equal(raw, png) || ts.lastResp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("art in the file: %d", code)
	}
	if code, raw := ts.ask(dev, name, "GET", "/api/peer/music/art?path="+url.QueryEscape(song.Path), nil, false); code != 200 || !strings.Contains(string(raw), "a jpeg") {
		t.Fatalf("cover.jpg: %d", code)
	}
	if code, _ := ts.ask(dev, name, "GET", "/api/peer/music/art?path="+url.QueryEscape(op.Path), nil, false); code != 404 {
		t.Fatalf("no cover: %d", code)
	}

	// Settings: none yet; sent sealed for the server; given to another
	// device once it's let in, sealed for it.
	if code, _ := ts.ask(dev, name, "GET", "/api/peer/music/settings", nil, false); code != 404 {
		t.Fatalf("no settings yet: %d", code)
	}
	file := []byte(`{"version":1,"note":"é and commas, too"}`)
	sealed, _ := dev.SealFor(ts.hostPub, ts.hostID, dev.ID, file)
	body, _ := json.Marshal(map[string]any{"sealed": sealed, "updatedAt": 1761955200})
	if code, raw := ts.ask(dev, name, "POST", "/api/peer/music/settings", body, false, "Content-Type", "application/json"); code != 204 {
		t.Fatalf("send settings: %d %s", code, raw)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.s.opt.DataDir, "settings-kept.json")); len(b) == 0 {
		t.Fatal("not kept")
	} else if st, _ := os.Stat(filepath.Join(ts.s.opt.DataDir, "settings-kept.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("kept as %v", st.Mode())
	}
	phone := ts.device("phone")
	if code, _ := ts.ask(phone, "Utako on phone", "GET", "/api/peer/music/settings", nil, false); code != 403 {
		t.Fatalf("settings for a device not let in: %d", code)
	}
	ts.dashboard("POST", "/api/devices/"+phone.ID, map[string]bool{"allowed": true})
	code, raw = ts.ask(phone, "Utako on phone", "GET", "/api/peer/music/settings", nil, false)
	var got sealedBody
	if json.Unmarshal(raw, &got); code != 200 || got.UpdatedAt != 1761955200 {
		t.Fatalf("fetch settings: %d %s", code, raw)
	}
	if plain, err := phone.OpenFrom(ts.hostPub, phone.ID, ts.hostID, got.Sealed); err != nil || !bytes.Equal(plain, file) {
		t.Fatalf("opened %q %v", plain, err)
	}
	if _, err := dev.OpenFrom(ts.hostPub, dev.ID, ts.hostID, got.Sealed); err == nil {
		t.Fatal("sealed for one device, opened by another")
	}

	// Uploads: refused until the dashboard lets it upload, then a song in
	// two pieces (the first sent again is refused with where it stands).
	if code, raw := ts.ask(dev, name, "GET", "/api/peer/upload", nil, false); code != 403 || !strings.Contains(string(raw), "Let it upload here") {
		t.Fatalf("upload before it's let: %d %s", code, raw)
	}
	ts.dashboard("POST", "/api/devices/"+dev.ID, map[string]bool{"uploads": true})
	_, raw = ts.ask(dev, name, "GET", "/api/peer/hello", nil, false)
	if json.Unmarshal(raw, &hello); !hello.Uploads {
		t.Fatalf("hello: %s", raw)
	}
	code, raw = ts.ask(dev, name, "GET", "/api/peer/upload", nil, false)
	var folders struct {
		Folders []string `json:"folders"`
		Chunk   int      `json:"chunk"`
	}
	if json.Unmarshal(raw, &folders); code != 200 || len(folders.Folders) != 1 || folders.Chunk == 0 {
		t.Fatalf("upload folders %d %s", code, raw)
	}
	up := id3(map[string]string{"TIT2": "Uploaded", "TPE1": "New Band"})
	q := func(offset int) string {
		return fmt.Sprintf("/api/peer/upload?folder=0&path=%s&offset=%d&total=%d", url.QueryEscape("New Band/Uploaded.mp3"), offset, len(up))
	}
	if code, raw := ts.ask(dev, name, "POST", q(0), up[:300], false, "Content-Type", "application/octet-stream"); code != 200 || !strings.Contains(string(raw), `"have":300`) {
		t.Fatalf("first piece: %d %s", code, raw)
	}
	if code, raw := ts.ask(dev, name, "POST", q(0), up[:300], false, "Content-Type", "application/octet-stream"); code != 409 || !strings.Contains(string(raw), `"have":300`) {
		t.Fatalf("again: %d %s", code, raw)
	}
	if code, raw := ts.ask(dev, name, "POST", q(300), up[300:], false, "Content-Type", "application/octet-stream"); code != 200 || !strings.Contains(string(raw), `"done":true`) {
		t.Fatalf("last piece: %d %s", code, raw)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.music, "New Band", "Uploaded.mp3")); !bytes.Equal(b, up) {
		t.Fatal("the upload isn't whole")
	}
	ts.s.lib.Scan(context.Background())
	_, raw = ts.ask(dev, name, "GET", "/api/peer/hello", nil, false)
	before := hello.Files
	if json.Unmarshal(raw, &hello); hello.Files == before {
		t.Fatal("the library's version didn't change")
	}
	_, raw = ts.dashboard("GET", "/api/tracks?q=new+uploaded", nil)
	if !strings.Contains(string(raw), `"total":1`) || !strings.Contains(string(raw), "Uploaded") {
		t.Fatalf("search %s", raw)
	}

	// Let out again: nothing more, its uploads off.
	ts.dashboard("POST", "/api/devices/"+dev.ID, map[string]bool{"allowed": false})
	if code, _ := ts.ask(dev, name, "GET", "/api/peer/music/library", nil, false); code != 403 {
		t.Fatalf("library once let out: %d", code)
	}
	if d, _ := ts.s.devs.get(dev.ID); d.Uploads {
		t.Fatal("uploads stayed on")
	}
	// The ticket too.
	req, _ = http.NewRequest("GET", ts.peers.URL+uri, nil)
	req.Header = ticket.Clone()
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("a ticket once let out: %d", resp.StatusCode)
	}
}

func TestRefusals(t *testing.T) {
	ts := newTestServer(t)
	dev := ts.device("x")
	// A request sent again.
	req, _ := http.NewRequest("GET", ts.peers.URL+"/api/peer/hello", nil)
	_, _ = dev.Sign(req, nil, ts.hostID, ts.hostPub, "x", time.Now(), false)
	for i, want := range []int{200, 403} {
		again, _ := http.NewRequest("GET", ts.peers.URL+"/api/peer/hello", nil)
		again.Header = req.Header.Clone()
		resp, _ := http.DefaultClient.Do(again)
		if resp.StatusCode != want {
			t.Fatalf("%d: %d", i, resp.StatusCode)
		}
	}
	// From outside the home network.
	r := httptest.NewRequest("GET", "/api/peer/hello", nil)
	_, _ = dev.Sign(r, nil, ts.hostID, ts.hostPub, "x", time.Now(), false)
	r.RemoteAddr = "8.8.8.8:5000"
	w := httptest.NewRecorder()
	ts.s.peerHandler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("from the internet: %d", w.Code)
	}
	// A large body from a device not let in isn't even read.
	big := bytes.Repeat([]byte("x"), smallBody+1)
	if code, _ := ts.ask(dev, "x", "POST", "/api/peer/music/settings", big, false); code != 413 {
		t.Fatalf("large body: %d", code)
	}
	// The dashboard's API only from the program.
	r = httptest.NewRequest("GET", "/api/status", nil)
	r.RemoteAddr = "192.168.1.5:1234"
	w = httptest.NewRecorder()
	ts.dash.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("dashboard from the network: %d", w.Code)
	}
	// Too many strangers: the oldest is forgotten.
	for i := 0; i < maxStrangers+5; i++ {
		ts.ask(ts.device("s"), fmt.Sprint("s", i), "GET", "/api/peer/hello", nil, false)
	}
	if n := len(ts.s.devs.list()); n > maxStrangers {
		t.Fatalf("%d devices kept", n)
	}
	// A browser on the music side's port goes to the dashboard.
	ts.s.opt.DashboardPort = 43211
	r = httptest.NewRequest("GET", "/", nil)
	r.Host = "pi.local:43411"
	w = httptest.NewRecorder()
	ts.s.peerHandler().ServeHTTP(w, r)
	if w.Code != 302 || w.Header().Get("Location") != "http://pi.local:43211/#music" {
		t.Fatalf("browser: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestDashboardFolders(t *testing.T) {
	ts := newTestServer(t)
	sub := filepath.Join(ts.music, "Band")
	if code, raw := ts.dashboard("POST", "/api/folders", map[string]string{"path": sub}); code != 400 || !strings.Contains(string(raw), "already") {
		t.Fatalf("a folder inside one: %d %s", code, raw)
	}
	if code, _ := ts.dashboard("POST", "/api/folders", map[string]string{"path": "relative/path"}); code != 400 {
		t.Fatalf("relative: %d", code)
	}
	if code, raw := ts.dashboard("DELETE", "/api/folders?path="+url.QueryEscape(ts.music), nil); code != 200 || string(bytes.TrimSpace(raw)) != "[]" {
		t.Fatalf("remove: %d %s", code, raw)
	}
	if n, _, _ := ts.s.lib.Counts(); n != 0 {
		t.Fatalf("%d songs left", n)
	}
	other := t.TempDir()
	if code, raw := ts.dashboard("POST", "/api/folders", map[string]string{"path": other}); code != 200 || !strings.Contains(string(raw), other) {
		t.Fatalf("add: %d %s", code, raw)
	}
	// The dashboard's uploads go to this server.
	_, raw := ts.dashboard("GET", "/api/upload/targets", nil)
	if !strings.Contains(string(raw), `"here":true`) {
		t.Fatalf("targets %s", raw)
	}
	code, raw := ts.dashboard("POST", "/api/upload?folder=0&path=a.mp3&offset=0&total=3", nil)
	_ = code
	r := httptest.NewRequest("POST", "/api/upload?folder=0&path=a.mp3&offset=0&total=3", strings.NewReader("abc"))
	r.RemoteAddr = "127.0.0.1:0"
	w := httptest.NewRecorder()
	ts.dash.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatalf("upload from the dashboard: %d %s %s", w.Code, w.Body, raw)
	}
}
