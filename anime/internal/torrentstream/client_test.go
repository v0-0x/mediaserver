//go:build !android

package torrentstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// seed seeds a torrent of one file of data from another client on this
// computer, and returns its magnet link (its address in it).
func seed(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := tempDir(t)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	var mi metainfo.MetaInfo
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.Seed = true
	cfg.NoDHT = true
	cfg.DisableIPv6 = true
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	tor, err := cl.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := tor.VerifyData(); err != nil {
		t.Fatal(err)
	}
	h := mi.HashInfoBytes()
	return mi.Magnet(&h, &info).String() + fmt.Sprintf("&x.pe=127.0.0.1:%d", cl.LocalPort())
}

// A torrent plays as it downloads: by magnet link, from its peer, the file
// served whole or by byte ranges (a seek), as it is.
func TestStreamFromAPeer(t *testing.T) {
	data := make([]byte, 3<<20+12345)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	magnet := seed(t, "Test.Movie.2020.1080p.mkv", data)
	e := New(filepath.Join(tempDir(t), "cache"))
	t.Cleanup(e.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := e.Open(ctx, magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "Test.Movie.2020.1080p.mkv" || st.Size != int64(len(data)) {
		t.Fatalf("status %+v", st)
	}
	srv := httptest.NewServer(e.Handler(st.ID))
	defer srv.Close()

	// A seek: a range from the middle first.
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	req.Header.Set("Range", "bytes=2000000-2099999")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[2000000:2100000]) {
		t.Fatalf("range: %d, %d bytes", resp.StatusCode, len(got))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/x-matroska" {
		t.Fatalf("type %q", ct)
	}
	// All of it.
	resp, err = http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("the whole file: %d of %d bytes, equal %v", len(got), len(data), bytes.Equal(got, data))
	}
	// From the loopback address too (ffmpeg's, mpv's).
	loop, err := e.URL(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get(loop); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback: %v %v", resp, err)
	} else {
		got, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Equal(got, data) {
			t.Fatal("loopback: not the file")
		}
	}
	if st, _ := e.Status(st.ID); st.Done != int64(len(data)) {
		t.Fatalf("status after: %+v", st)
	}
	// The same torrent again: the same session.
	again, err := e.Open(ctx, magnet, nil)
	if err != nil || again.ID != st.ID {
		t.Fatalf("again: %+v %v", again, err)
	}
	// Stopped: gone, with its files.
	e.Stop(st.ID)
	if _, ok := e.Status(st.ID); ok {
		t.Fatal("still there after Stop")
	}
	resp, _ = http.Get(srv.URL)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("served after Stop: %d", resp.StatusCode)
	}
	// Removed. (Not checked on Windows: the BitTorrent library may keep a
	// file open there until the program ends, and New empties the cache
	// when the app starts again.)
	for i := 0; runtime.GOOS != "windows"; i++ {
		entries, _ := os.ReadDir(e.dir)
		if len(entries) == 0 {
			break
		}
		if i == 100 {
			t.Fatalf("files left in the cache: %v", entries)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestMagnet(t *testing.T) {
	m, err := Magnet("", "0123456789abcdef0123456789abcdef01234567", "A Movie")
	if err != nil || !strings.HasPrefix(m, "magnet:?xt=urn:btih:0123456789abcdef") || !strings.Contains(m, "dn=A+Movie") || !strings.Contains(m, "&tr=") {
		t.Fatalf("from a hash: %q %v", m, err)
	}
	if _, err := Magnet("", "nothex", ""); err == nil {
		t.Fatal("a bad hash was taken")
	}
	own := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=udp%3A%2F%2Fexample%3A1"
	if m, _ := Magnet(own, "", ""); m != own {
		t.Fatalf("a magnet with its trackers changed: %q", m)
	}
	if _, err := Magnet("https://example.com/x.torrent", "", ""); err == nil {
		t.Fatal("not a magnet, taken")
	}
}

// tempDir is a temporary folder removed after the test as well as it can
// be: on Windows, a file a client has just closed can't be removed at
// once (t.TempDir would fail the test).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "torrentstream-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50 && os.RemoveAll(dir) != nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}
