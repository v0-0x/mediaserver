package downloads

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// matroska is a video file's worth of bytes that starts like a Matroska
// file.
func matroska(t *testing.T, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	copy(b, []byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81, 0x01, 0x42, 0x82, 0x88, 'm', 'a', 't', 'r', 'o', 's', 'k', 'a'})
	return b
}

// A video file is downloaded in Go with the stream's headers, its
// progress and speed reported on the way, and named after what it holds
// (a Matroska file the provider calls an MP4 is saved as .mkv).
func TestFileDownload(t *testing.T) {
	data := matroska(t, 2<<20)
	var sawReferer atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawReferer.Store(r.Header.Get("Referer") == "https://site.example/")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		// Slowly enough for the progress to be reported.
		for i := 0; i < len(data); i += len(data) / 8 {
			_, _ = w.Write(data[i:min(i+len(data)/8, len(data))])
			w.(http.Flusher).Flush()
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer srv.Close()
	allow(t, srv.URL)
	m, lib := newTestManager(t, nil)
	rec := record(t, m.hub)
	it := enqueueResolved(t, m, "Film", &Resolved{URL: srv.URL + "/film.mp4", Referrer: "https://site.example/", Container: "mp4"})
	got := waitFinal(t, m, it.ID, 15*time.Second)
	want := filepath.Join(lib, "Film", "Film.mkv")
	if got.Status != StatusCompleted || got.Output != want {
		t.Fatalf("%s %q (%s), want completed into %q", got.Status, got.Output, got.Error, want)
	}
	if b, err := os.ReadFile(want); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("downloaded %d bytes (%v), want %d", len(b), err, len(data))
	}
	if names := dirEntries(t, filepath.Join(lib, "Film")); strings.Join(names, ",") != "Film.mkv" {
		t.Errorf("title folder holds %v", names)
	}
	if f, err := m.files.Get(want); err != nil || f.MediaID != 1 {
		t.Errorf("library entry %+v, %v", f, err)
	}
	if !sawReferer.Load() {
		t.Error("the Referer wasn't sent")
	}
	var sawProgress bool
	for _, ev := range rec.downloadEvents() {
		p := ev.Payload
		sawProgress = sawProgress || (p.ID == it.ID && p.Status == StatusDownloading && p.Progress > 0 && p.Progress < 1 && strings.HasSuffix(p.Speed, "/s"))
	}
	if !sawProgress {
		t.Error("no progress with a speed")
	}
}

// A download whose connection breaks picks up where it stopped, with a
// byte range from there (If-Range: the same file), or starts over when
// the host doesn't take ranges or the file changed.
func TestFileDownloadResumes(t *testing.T) {
	data := matroska(t, 1<<20)
	other := matroska(t, 1<<20)
	for _, tc := range []struct {
		name      string
		ranges    bool // the host takes byte ranges
		changed   bool // the file is another one after the break
		wantRange bool // the second request asks for a range
		want      []byte
	}{
		{"range", true, false, true, data},
		{"no ranges", false, false, true, data},
		{"changed", true, true, true, other},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var asked []string
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				n := calls
				asked = append(asked, r.Header.Get("Range")+" "+r.Header.Get("If-Range"))
				mu.Unlock()
				if n == 1 {
					// Half the file, then the connection breaks.
					w.Header().Set("ETag", `"v1"`)
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
					_, _ = w.Write(data[:len(data)/2])
					w.(http.Flusher).Flush()
					panic(http.ErrAbortHandler)
				}
				body, etag := data, `"v1"`
				if tc.changed {
					body, etag = other, `"v2"`
				}
				w.Header().Set("ETag", etag)
				if !tc.ranges {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					_, _ = w.Write(body)
					return
				}
				http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(body))
			}))
			defer srv.Close()
			allow(t, srv.URL)
			m, lib := newTestManager(t, nil)
			it := enqueueResolved(t, m, "Film", &Resolved{URL: srv.URL + "/film.mkv"})
			got := waitFinal(t, m, it.ID, 15*time.Second)
			if got.Status != StatusCompleted {
				t.Fatalf("%s (%s), want completed", got.Status, got.Error)
			}
			if b, _ := os.ReadFile(filepath.Join(lib, "Film", "Film.mkv")); !bytes.Equal(b, tc.want) {
				t.Fatalf("downloaded %d bytes, not the file", len(b))
			}
			mu.Lock()
			defer mu.Unlock()
			if len(asked) != 2 {
				t.Fatalf("asked %q, want 2 requests", asked)
			}
			if want := fmt.Sprintf("bytes=%d- \"v1\"", len(data)/2); tc.wantRange && asked[1] != want {
				t.Errorf("second request %q, want %q", asked[1], want)
			}
		})
	}
}

// Canceling a file download stops it at once and leaves nothing behind.
func TestCancelFileDownload(t *testing.T) {
	data := matroska(t, 1<<20)
	stop := make(chan struct{})
	defer close(stop)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data[:1024])
		w.(http.Flusher).Flush()
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	allow(t, srv.URL)
	m, lib := newTestManager(t, nil)
	it := enqueueResolved(t, m, "Halt", &Resolved{URL: srv.URL + "/film.mkv"})
	waitFile(t, filepath.Join(lib, "Halt", ".kumobox-download-"+it.ID, "Halt.mkv"))
	m.Cancel(it.ID)
	waitIdleWithin(t, m, 5*time.Second)
	if got := find(m, it.ID); got.Status != StatusCanceled {
		t.Fatalf("status = %s (%s), want canceled", got.Status, got.Error)
	}
	if names := dirEntries(t, filepath.Join(lib, "Halt")); len(names) != 0 {
		t.Fatalf("canceled download left %v behind", names)
	}
}

// makeHLS encodes an 8-second HLS stream (2-second segments, H.264 and
// AAC) into dir.
func makeHLS(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=24", "-f", "lavfi", "-i", "sine",
		"-t", "8", "-c:v", "libx264", "-g", "48", "-c:a", "aac", "-shortest", "-f", "hls", "-hls_time", "2", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.ts"), filepath.Join(dir, "index.m3u8")).CombinedOutput()
	if err != nil {
		t.Skipf("can't make an HLS stream: %v %s", err, out)
	}
}

// realFfmpeg points the downloads at the installed ffmpeg and ffprobe.
func realFfmpeg(t *testing.T) func(*config.Settings) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	return func(c *config.Settings) { c.Transcode.FfmpegPath, c.Transcode.FfprobePath = ffmpeg, ffprobe }
}

// An HLS stream (here a master playlist) is downloaded by ffmpeg through
// the stream relay, into an MP4 file.
func TestHLSDownload(t *testing.T) {
	mutate := realFfmpeg(t)
	dir := t.TempDir()
	makeHLS(t, filepath.Join(dir, "v"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=300000,CODECS=\"avc1.64000c,mp4a.40.2\"\nv/index.m3u8\n")
			return
		}
		http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
	}))
	defer srv.Close()
	allow(t, srv.URL)
	m, lib := newTestManager(t, mutate)
	it := enqueueResolved(t, m, "Stream", &Resolved{URL: srv.URL + "/master.m3u8", Container: "mp4"})
	got := waitFinal(t, m, it.ID, 60*time.Second)
	want := filepath.Join(lib, "Stream", "Stream.mp4")
	if got.Status != StatusCompleted || got.Output != want {
		t.Fatalf("%s %q (%s)", got.Status, got.Output, got.Error)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", want).Output()
	if d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err != nil || d < 7.5 || d > 8.5 {
		t.Errorf("downloaded %vs (%v), want 8", d, err)
	}
}

// Whatever a provider's link leads to (a redirect, a playlist naming
// segments, keys or variants elsewhere, a web page), a download never
// reaches this computer or the LAN: here a LAN server that must never be
// asked anything.
func TestDownloadsNeverReachLAN(t *testing.T) {
	mutate := realFfmpeg(t)
	var lanHits atomic.Int32
	lan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lanHits.Add(1)
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:2,\nseg.ts\n#EXT-X-ENDLIST\n")
	}))
	defer lan.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect.mkv":
			http.Redirect(w, r, lan.URL+"/film.mkv", http.StatusFound)
		case "/segments.m3u8":
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\n%s/a.ts\n#EXTINF:2,\r%s/b.ts\n#EXT-X-ENDLIST\n", lan.URL, lan.URL)
		case "/key.m3u8":
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-KEY:METHOD=AES-128,URI=%s/key\n#EXTINF:2,\nseg.ts\n#EXT-X-ENDLIST\n", lan.URL)
		case "/master.m3u8":
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n%s/v.m3u8\n", lan.URL)
		case "/dash.mpd":
			fmt.Fprintf(w, `<?xml version="1.0"?><MPD><BaseURL>%s/</BaseURL></MPD>`, lan.URL)
		}
	}))
	defer provider.Close()
	allow(t, provider.URL)

	bin := t.TempDir()
	ran := filepath.Join(bin, "ran")
	writeScript(t, bin, "yt-dlp", "echo ran > '"+ran+"'\nexit 1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, res := range []*Resolved{
		{URL: lan.URL + "/film.mkv"},
		{URL: provider.URL + "/redirect.mkv"},
		{URL: provider.URL + "/segments.m3u8"},
		{URL: provider.URL + "/key.m3u8"},
		{URL: provider.URL + "/master.m3u8"},
		{URL: provider.URL + "/dash.mpd"},
		{URL: lan.URL + "/watch", Page: true},
		{URL: "http://nas.lan/watch", Page: true},
		{URL: "http://192.168.1.20/watch", Page: true},
		{URL: "https://rebound.example/watch", Page: true},
	} {
		m, _ := newTestManager(t, func(c *config.Settings) {
			mutate(c)
			c.Downloads.Downloader = "auto"
		})
		it := enqueueResolved(t, m, "Film", res)
		got := waitFinal(t, m, it.ID, 30*time.Second)
		t.Logf("%s: %s", res.URL, got.Error)
		if got.Status != StatusFailed {
			t.Errorf("%s: %s, want failed", res.URL, got.Status)
		}
		if n := lanHits.Load(); n > 0 {
			t.Fatalf("%s: reached the LAN %d times", res.URL, n)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("yt-dlp ran for a page on the LAN")
	}
}
