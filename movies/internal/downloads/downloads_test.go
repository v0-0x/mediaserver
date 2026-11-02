package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// testURL is a small HLS stream any test may download (the segment it
// names isn't there: ffmpeg is faked where it's used).
var testURL string

// pageURL is a web page on the internet (lookupIP says so), for yt-dlp.
const pageURL = "https://videos.example/watch?v=1"

// allowed are the addresses on this computer the downloads may reach,
// standing for public hosts (the stream package's LocalHosts).
var allowed struct {
	sync.Mutex
	hosts []string
}

// allow lets downloads reach a test server for the rest of the test.
func allow(t *testing.T, raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	allowed.Lock()
	allowed.hosts = append(allowed.hosts, stream.HostKey(u))
	allowed.Unlock()
	t.Cleanup(func() {
		allowed.Lock()
		defer allowed.Unlock()
		for i, h := range allowed.hosts {
			if h == stream.HostKey(u) {
				allowed.hosts = append(allowed.hosts[:i], allowed.hosts[i+1:]...)
				break
			}
		}
	})
}

func TestMain(m *testing.M) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg0.ts\n#EXT-X-ENDLIST\n")
	}))
	testURL = srv.URL + "/ep.m3u8"
	u, _ := url.Parse(srv.URL)
	stream.LocalHosts = func() []string {
		allowed.Lock()
		defer allowed.Unlock()
		return append([]string{stream.HostKey(u)}, allowed.hosts...)
	}
	reconnectDelay = func(int) time.Duration { return 10 * time.Millisecond }
	lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "videos.example":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		case "rebound.example":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("192.168.1.20")}}, nil
		}
		return nil, errors.New("no such host")
	}
	code := m.Run()
	srv.Close()
	os.Exit(code)
}

func newTestManager(t *testing.T, mutate func(*config.Settings)) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	cfg.Library.Dir = filepath.Join(dir, "library")
	cfg.Downloads.Downloader = "ffmpeg"
	cfg.Transcode.FfmpegPath = filepath.Join(dir, "missing-ffmpeg")
	cfg.Transcode.FfprobePath = filepath.Join(dir, "missing-ffprobe")
	if mutate != nil {
		mutate(&cfg)
	}
	if _, err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewManager(s, d, events.NewHub(), library.NewStore(d)), cfg.Library.Dir
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in programs are shell scripts")
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func enqueue(t *testing.T, m *Manager, title string) *Item {
	t.Helper()
	it, err := m.Enqueue(Item{MediaID: 1, Episode: 1, Title: title, Source: "stream", URL: testURL})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// enqueueResolved queues a download whose link is res.
func enqueueResolved(t *testing.T, m *Manager, title string, res *Resolved) *Item {
	t.Helper()
	m.Resolvers["test"] = func(context.Context, *Item) (*Resolved, error) { return res, nil }
	it, err := m.Enqueue(Item{MediaID: 1, Episode: 1, Title: title, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// enqueuePage queues the download of a web page's video (yt-dlp).
func enqueuePage(t *testing.T, m *Manager, title string) *Item {
	return enqueueResolved(t, m, title, &Resolved{URL: pageURL, Page: true})
}

func find(m *Manager, id string) *Item {
	for _, it := range m.List() {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func waitFinal(t *testing.T, m *Manager, id string, timeout time.Duration) Item {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if it := find(m, id); it != nil && (it.Status == StatusCompleted || it.Status == StatusFailed || it.Status == StatusCanceled) {
			return *it
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("download %s did not finish within %s", id, timeout)
	return Item{}
}

// waitIdle waits until no run is left, canceled ones included.
func waitIdle(t *testing.T, m *Manager) {
	t.Helper()
	waitIdleWithin(t, m, 10*time.Second)
}

func waitIdleWithin(t *testing.T, m *Manager, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.runs)
		m.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runs did not stop within %s", timeout)
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s was never written", path)
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// dirEntries lists a directory (nil if it doesn't exist).
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func dbRows(t *testing.T, m *Manager, id string) int {
	t.Helper()
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM downloads WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type event struct {
	Type    string `json:"type"`
	Payload struct {
		ID       string  `json:"id"`
		Status   string  `json:"status"`
		Progress float64 `json:"progress"`
		Speed    string  `json:"speed"`
		ETA      string  `json:"eta"`
		Done     string  `json:"done"`
		Removed  bool    `json:"removed"`
		Cleared  bool    `json:"cleared"`
	} `json:"payload"`
}

type recorder struct {
	mu     sync.Mutex
	events []event
}

func record(t *testing.T, hub *events.Hub) *recorder {
	ch, unsubscribe := hub.Subscribe()
	rec := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for raw := range ch {
			var ev event
			_ = json.Unmarshal(raw, &ev)
			rec.mu.Lock()
			rec.events = append(rec.events, ev)
			rec.mu.Unlock()
		}
	}()
	t.Cleanup(func() { unsubscribe(); <-done })
	return rec
}

func (r *recorder) downloadEvents() []event {
	time.Sleep(50 * time.Millisecond) // let the collector catch up
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event
	for _, ev := range r.events {
		if ev.Type == events.DownloadProgress {
			out = append(out, ev)
		}
	}
	return out
}

// A stream with lots of errors makes ffmpeg write megabytes to stderr: the
// download must not stall once the pipe is full.
func TestFfmpegStderrFloodDoesNotStall(t *testing.T) {
	bin := t.TempDir()
	ffmpeg := writeScript(t, bin, "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
yes 'Error while decoding stream #0:0: Invalid data found when processing input' | head -n 30000 >&2
echo 'out_time_us=5000000'
echo 'speed=2.5x'
echo 'progress=end'
printf 'video' > "$last"
`)
	ffprobe := writeScript(t, bin, "ffprobe", "echo 10.0\n")
	m, lib := newTestManager(t, func(c *config.Settings) {
		c.Transcode.FfmpegPath, c.Transcode.FfprobePath = ffmpeg, ffprobe
	})
	rec := record(t, m.hub)
	it := enqueue(t, m, "Flood")
	got := waitFinal(t, m, it.ID, 15*time.Second)
	if got.Status != StatusCompleted {
		t.Fatalf("status = %s (%s), want completed", got.Status, got.Error)
	}
	want := filepath.Join(lib, "Flood", "Flood.mp4")
	if got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "video" {
		t.Fatalf("downloaded file = %q, %v", b, err)
	}
	if names := dirEntries(t, filepath.Dir(want)); len(names) != 1 {
		t.Fatalf("title folder holds %v, want only the video", names)
	}
	if f, err := m.files.Get(want); err != nil || f.MediaID != 1 || f.Episode != 1 {
		t.Fatalf("library entry = %+v, %v", f, err)
	}
	var sawProgress bool
	for _, ev := range rec.downloadEvents() {
		sawProgress = sawProgress || (ev.Payload.ID == it.ID && ev.Payload.Status == StatusDownloading && ev.Payload.Progress == 0.5)
	}
	if !sawProgress {
		t.Error("no progress event at 50%")
	}
}

// Movies' direct links often have AC-3, E-AC-3 or Opus audio, which
// ffmpeg won't copy through the AAC-only aac_adtstoasc filter: it must not
// be asked for (the MP4 muxer adds it by itself for AAC).
func TestFfmpegCopiesAnyAudio(t *testing.T) {
	ffmpeg := writeScript(t, t.TempDir(), "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
case "$*" in *aac_adtstoasc*)
	echo "[aac_adtstoasc @ 0x5599] Codec 'ac3' (86019) is not supported by the bitstream filter 'aac_adtstoasc'. Supported codecs are: aac (86018)" >&2
	echo 'Error opening output files: Invalid argument' >&2
	exit 234 ;;
esac
for last; do :; done
printf 'video' > "$last"
`)
	m, _ := newTestManager(t, func(c *config.Settings) { c.Transcode.FfmpegPath = ffmpeg })
	it := enqueue(t, m, "Surround")
	if got := waitFinal(t, m, it.ID, 15*time.Second); got.Status != StatusCompleted {
		t.Fatalf("status = %s (%q), want completed", got.Status, got.Error)
	}
}

// The error shown is ffmpeg's last stderr line, even after megabytes of
// noise, and the half-written file doesn't stay in the library.
func TestFfmpegFailureReportsLastErrorAndCleansUp(t *testing.T) {
	ffmpeg := writeScript(t, t.TempDir(), "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
printf 'half a video' > "$last"
yes 'Error while decoding stream #0:0: Invalid data found when processing input' | head -n 30000 >&2
echo '[https @ 0x5599] HTTP error 403 Forbidden' >&2
exit 1
`)
	m, lib := newTestManager(t, func(c *config.Settings) { c.Transcode.FfmpegPath = ffmpeg })
	it := enqueue(t, m, "Broken")
	got := waitFinal(t, m, it.ID, 15*time.Second)
	if got.Status != StatusFailed || got.Error != "ffmpeg: [https @ 0x5599] HTTP error 403 Forbidden" {
		t.Fatalf("got %s (%q), want failed with the last stderr line", got.Status, got.Error)
	}
	if names := dirEntries(t, filepath.Join(lib, "Broken")); len(names) != 0 {
		t.Fatalf("failed download left %v behind", names)
	}
}

func TestYtdlpStderrFlood(t *testing.T) {
	const head = `out=
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; esac
	shift
done
yes 'WARNING: [generic] Falling back on generic information extractor' | head -n 30000 >&2
echo 'MOVIKO| 42.0%|1.50MiB/s|00:07'
printf 'partial' > "$out.part"
printf 'fragment' > "$out.part-Frag1"
`
	t.Run("success", func(t *testing.T) {
		bin := t.TempDir()
		writeScript(t, bin, "yt-dlp", head+`printf 'video' > "$out"
`)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		m, lib := newTestManager(t, func(c *config.Settings) { c.Downloads.Downloader = "yt-dlp" })
		rec := record(t, m.hub)
		it := enqueuePage(t, m, "Chatty")
		got := waitFinal(t, m, it.ID, 15*time.Second)
		if got.Status != StatusCompleted {
			t.Fatalf("status = %s (%s), want completed", got.Status, got.Error)
		}
		if names := dirEntries(t, filepath.Join(lib, "Chatty")); strings.Join(names, ",") != "Chatty.mp4" {
			t.Fatalf("title folder holds %v, want only the video", names)
		}
		var sawProgress bool
		for _, ev := range rec.downloadEvents() {
			sawProgress = sawProgress || (ev.Payload.ID == it.ID && ev.Payload.Progress == 0.42 && ev.Payload.Speed == "1.50MiB/s")
		}
		if !sawProgress {
			t.Error("no progress event with 42% at 1.50MiB/s")
		}
	})
	t.Run("failure", func(t *testing.T) {
		bin := t.TempDir()
		writeScript(t, bin, "yt-dlp", head+`echo 'ERROR: [generic] Unable to download webpage: HTTP Error 404: Not Found' >&2
echo 'trailing noise' >&2
exit 1
`)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		m, lib := newTestManager(t, func(c *config.Settings) { c.Downloads.Downloader = "yt-dlp" })
		it := enqueuePage(t, m, "Gone")
		got := waitFinal(t, m, it.ID, 15*time.Second)
		if got.Status != StatusFailed || got.Error != "ERROR: [generic] Unable to download webpage: HTTP Error 404: Not Found" {
			t.Fatalf("got %s (%q), want failed with the ERROR line", got.Status, got.Error)
		}
		if names := dirEntries(t, filepath.Join(lib, "Gone")); len(names) != 0 {
			t.Fatalf("failed download left %v behind (.part files?)", names)
		}
	})
}

// Canceling stops the real downloader process right away (yt-dlp through an
// interrupt, well before it would be killed) and leaves no partial files.
func TestCancelStopsDownloader(t *testing.T) {
	for _, tc := range []struct{ name, partial, script string }{
		{"ffmpeg", ".mp4", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
printf 'half a video' > "$last"
exec sleep 30
`},
		{"yt-dlp", ".mp4.part", `out=
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; esac
	shift
done
trap 'echo "ERROR: Interrupted by user" >&2; exit 1' INT
printf 'partial' > "$out.part"
while :; do sleep 0.05; done
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := writeScript(t, bin, tc.name, tc.script)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			m, lib := newTestManager(t, func(c *config.Settings) {
				c.Downloads.Downloader, c.Transcode.FfmpegPath = tc.name, script
			})
			var it *Item
			if tc.name == "yt-dlp" {
				it = enqueuePage(t, m, "Halt")
			} else {
				it = enqueue(t, m, "Halt")
			}
			waitFile(t, filepath.Join(lib, "Halt", ".kumobox-download-"+it.ID, "Halt"+tc.partial))
			m.Cancel(it.ID)
			waitIdleWithin(t, m, 5*time.Second)
			if got := find(m, it.ID); got.Status != StatusCanceled {
				t.Fatalf("status = %s (%s), want canceled", got.Status, got.Error)
			}
			if names := dirEntries(t, filepath.Join(lib, "Halt")); len(names) != 0 {
				t.Fatalf("canceled download left %v behind", names)
			}
		})
	}
}

// Removing (or clearing) a download while its run is still stopping must not
// let that run write the item back to the database or the UI.
func TestRemoveDuringRunDoesNotResurrect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(m *Manager, id string)
		marker func(ev event, id string) bool
	}{
		{"remove", func(m *Manager, id string) { m.Remove(id) }, func(ev event, id string) bool { return ev.Payload.Removed && ev.Payload.ID == id }},
		{"cancel and clear", func(m *Manager, id string) { m.Cancel(id); m.ClearFinished() }, func(ev event, _ string) bool { return ev.Payload.Cleared }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lib := newTestManager(t, nil)
			rec := record(t, m.hub)
			started, release := make(chan struct{}, 1), make(chan struct{})
			m.fetch = func(r *run, _ config.Settings, _ *Resolved, out string) error {
				_ = os.WriteFile(out+".part", []byte("partial"), 0o644)
				started <- struct{}{}
				<-r.ctx.Done()
				<-release // slow to stop
				return r.ctx.Err()
			}
			it := enqueue(t, m, "Gone")
			waitSignal(t, started, "the download to start")
			tc.remove(m, it.ID)
			close(release)
			waitIdle(t, m)

			if find(m, it.ID) != nil {
				t.Fatal("item is listed again")
			}
			if n := dbRows(t, m, it.ID); n != 0 {
				t.Fatalf("database still has %d row(s) for the item", n)
			}
			evs := rec.downloadEvents()
			marker := -1
			for i, ev := range evs {
				if tc.marker(ev, it.ID) {
					marker = i
				}
			}
			if marker < 0 {
				t.Fatal("no removal event")
			}
			for _, ev := range evs[marker+1:] {
				if ev.Payload.ID == it.ID {
					t.Fatalf("item published after its removal: %+v", ev.Payload)
				}
			}
			if names := dirEntries(t, filepath.Join(lib, "Gone")); len(names) != 0 {
				t.Fatalf("canceled download left %v behind", names)
			}
			restarted := NewManager(m.settings, m.db, events.NewHub(), m.files)
			if find(restarted, it.ID) != nil {
				t.Fatal("item came back after a restart")
			}
		})
	}
}

func TestCancelRunningDownload(t *testing.T) {
	m, _ := newTestManager(t, nil)
	started := make(chan struct{}, 1)
	m.fetch = func(r *run, _ config.Settings, _ *Resolved, _ string) error {
		started <- struct{}{}
		<-r.ctx.Done()
		return r.ctx.Err()
	}
	it := enqueue(t, m, "Stop")
	waitSignal(t, started, "the download to start")
	m.Cancel(it.ID)
	waitIdle(t, m)
	got := find(m, it.ID)
	if got == nil || got.Status != StatusCanceled || got.Error != "" {
		t.Fatalf("got %+v, want canceled without error", got)
	}
	var raw string
	if err := m.db.QueryRow(`SELECT data FROM downloads WHERE id = ?`, it.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored Item
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.Status != StatusCanceled {
		t.Fatalf("stored status = %q (%v), want canceled", stored.Status, err)
	}
}

// Retrying right after a cancel must run the download again once the
// canceled run has stopped, and never both at once.
func TestCancelThenRetryWaitsForOldRun(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var calls, running, maxRunning atomic.Int32
	started, release := make(chan struct{}, 4), make(chan struct{})
	m.fetch = func(r *run, _ config.Settings, _ *Resolved, out string) error {
		n := calls.Add(1)
		cur := running.Add(1)
		defer running.Add(-1)
		for old := maxRunning.Load(); cur > old && !maxRunning.CompareAndSwap(old, cur); old = maxRunning.Load() {
		}
		started <- struct{}{}
		if n == 1 {
			<-r.ctx.Done()
			<-release // slow to stop
			return r.ctx.Err()
		}
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	it := enqueue(t, m, "Again")
	waitSignal(t, started, "the first run")
	m.Cancel(it.ID)
	if err := m.Retry(it.ID); err != nil {
		t.Fatal(err)
	}
	if got := find(m, it.ID); got.Status != StatusQueued {
		t.Fatalf("status after retry = %s, want queued", got.Status)
	}
	select {
	case <-started:
		t.Fatal("the retry started while the canceled run was still stopping")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	got := waitFinal(t, m, it.ID, 10*time.Second)
	if got.Status != StatusCompleted || got.Error != "" {
		t.Fatalf("got %s (%q), want completed", got.Status, got.Error)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("downloader ran %d times, want 2", n)
	}
	if n := maxRunning.Load(); n != 1 {
		t.Fatalf("%d runs overlapped", n)
	}
	if _, err := os.Stat(got.Output); err != nil {
		t.Fatal(err)
	}
}

// Downloads interrupted by a shutdown are queued again, but only start once
// Start says the resolvers are wired up.
func TestRestoredDownloadsWaitForStart(t *testing.T) {
	old, _ := newTestManager(t, nil)
	raw, _ := json.Marshal(Item{ID: "restored", MediaID: 7, Episode: 3, Title: "Resume", Mode: "sub", Source: "stream",
		URL: testURL, Status: StatusDownloading, Progress: 0.4, CreatedAt: 1})
	if _, err := old.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, 1)`, "restored", string(raw)); err != nil {
		t.Fatal(err)
	}
	// A download saved by an older version, with its title as animeTitle.
	if _, err := old.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, 2)`, "older",
		`{"id":"older","mediaId":8,"episode":1,"animeTitle":"Old Film (1999)","status":"completed","createdAt":2}`); err != nil {
		t.Fatal(err)
	}
	m := NewManager(old.settings, old.db, old.hub, old.files)
	if got := find(m, "older"); got == nil || got.Title != "Old Film (1999)" {
		t.Errorf("older download: %+v", got)
	}
	if b, _ := json.Marshal(find(m, "older")); !strings.Contains(string(b), `"title":"Old Film (1999)"`) {
		t.Errorf("older download sent as %s", b)
	}
	var calls atomic.Int32
	m.fetch = func(_ *run, _ config.Settings, _ *Resolved, out string) error {
		calls.Add(1)
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	time.Sleep(100 * time.Millisecond)
	if got := find(m, "restored"); got == nil || got.Status != StatusQueued || got.Progress != 0 || calls.Load() != 0 {
		t.Fatalf("before Start: %+v (downloader calls: %d), want queued and untouched", got, calls.Load())
	}
	m.Start()
	if got := waitFinal(t, m, "restored", 10*time.Second); got.Status != StatusCompleted {
		t.Fatalf("got %s (%q), want completed", got.Status, got.Error)
	}
}

// A download's title is "title" to the UI; a row an earlier build saved
// with Kumo's name for it ("animeTitle") keeps its title.
func TestItemTitle(t *testing.T) {
	raw, _ := json.Marshal(Item{ID: "x", Title: "Heat (1995)"})
	if !strings.Contains(string(raw), `"title":"Heat (1995)"`) || strings.Contains(string(raw), "anime") {
		t.Errorf("JSON: %s", raw)
	}
	old, _ := newTestManager(t, nil)
	if _, err := old.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, 1)`, "old",
		`{"id":"old","mediaId":949,"animeTitle":"Heat (1995)","status":"completed","createdAt":1}`); err != nil {
		t.Fatal(err)
	}
	m := NewManager(old.settings, old.db, old.hub, old.files)
	if got := find(m, "old"); got == nil || got.Title != "Heat (1995)" {
		t.Fatalf("restored: %+v", got)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(l string) { lines = append(lines, l) }}
	long := strings.Repeat("x", maxLine+10)
	for _, chunk := range []string{"out_time", "_us=1\nspeed=", "2x\r\n\nfra", "me=3\r", long[:100], long[100:] + "\n", "tail"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	w.flush()
	want := []string{"out_time_us=1", "speed=2x", "frame=3", long[:maxLine], "tail"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %.80q, want %.80q", lines, want)
	}
}

// Downloads go into a folder per movie or show: the one that already holds
// its files, else a new one named after it — never loose in the
// library folder.
func TestDownloadFolder(t *testing.T) {
	m, lib := newTestManager(t, nil)
	cfg := m.settings.Get()
	lib = filepath.Clean(lib)

	dir, err := m.folderFor(cfg, 2904, "Code Geass: Hangyaku no Lelouch R2")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != lib {
		t.Fatalf("new title: %q, want a folder inside %q", dir, lib)
	}

	// The user's own folder for it is reused.
	own := filepath.Join(lib, "Code Geass Hangyaku no Lelouch R2")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.files.Save(&library.LocalFile{Path: filepath.Join(own, "ep01.mkv"), Dir: own, Name: "ep01.mkv", MediaID: 2904, Episode: 1, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	if dir, _ := m.folderFor(cfg, 2904, "Code Geass: Hangyaku no Lelouch R2"); dir != own {
		t.Fatalf("existing folder: got %q, want %q", dir, own)
	}

	// Files loose in the library folder itself don't count.
	if err := m.files.Save(&library.LocalFile{Path: filepath.Join(lib, "loose.mkv"), Dir: lib, Name: "loose.mkv", MediaID: 21, Episode: 1, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	if dir, _ := m.folderFor(cfg, 21, "One Piece"); dir != filepath.Join(lib, "One Piece") {
		t.Fatalf("loose files: got %q", dir)
	}
	if dir, _ := m.folderFor(cfg, 99, "  "); filepath.Base(dir) != "Title 99" {
		t.Fatalf("no title: got %q", dir)
	}
}

// A download keeps its stream's container: Matroska and WebM streams are
// saved as .mkv and .webm, HLS and MP4 streams (and streams of an unknown
// type) as .mp4.
func TestDownloadContainer(t *testing.T) {
	m, lib := newTestManager(t, nil)
	m.fetch = func(_ *run, _ config.Settings, _ *Resolved, out string) error {
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	resolved := map[int]*Resolved{
		1: {URL: "https://cdn.example/ep.m3u8"},
		2: {URL: "https://cdn.example/play?id=2", Container: "mkv"},
		3: {URL: "https://cdn.example/Show.S01E03.WEBM?token=x"},
		4: {URL: "https://cdn.example/play?id=4"},
	}
	m.Resolvers["test"] = func(_ context.Context, it *Item) (*Resolved, error) {
		return resolved[it.Episode%1000], nil
	}
	for _, c := range []struct {
		episode int
		url     string
		want    string
	}{
		{1001, "", "Show - S01E01.mp4"},
		{1002, "", "Show - S01E02.mkv"},
		{1003, "", "Show - S01E03.webm"},
		{1004, "", "Show - S01E04.mp4"},
		{1005, "https://cdn.example/files/Show.S01E05.mkv", "Show - S01E05.mkv"},
	} {
		it, err := m.Enqueue(Item{MediaID: catalog.ShowID(1), Episode: c.episode, Title: "Show", Source: "test", URL: c.url})
		if err != nil {
			t.Fatal(err)
		}
		got := waitFinal(t, m, it.ID, 15*time.Second)
		want := filepath.Join(lib, "Show", "Season 01", c.want)
		if got.Status != StatusCompleted || got.Output != want {
			t.Errorf("S01E%02d: %s %q (%s), want %q", c.episode%1000, got.Status, got.Output, got.Error, want)
		}
	}
}

// ffmpeg copies the streams of HLS links into the container of the file:
// the MP4 options only go with an MP4 file, and nothing that only suits
// AAC audio (WebM has Opus or Vorbis); yt-dlp merges the videos of web
// pages into that container too. ffmpeg reads the stream from the relay,
// with nothing but plain HTTP to it.
func TestDownloaderArgsFollowContainer(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	script := `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
echo "$*" >> '` + argsFile + `'
out=
for last; do out="$last"; done
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; esac
	shift
done
printf 'video' > "$out"
`
	for _, name := range []string{"ffmpeg", "yt-dlp"} {
		writeScript(t, bin, name, script)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, c := range []struct {
		downloader, container string
		want, not             []string
	}{
		{"ffmpeg", "mp4", []string{"-protocol_whitelist http,tcp -f hls -i http://127.0.0.1:", "-c copy", "-movflags +faststart"}, []string{"aac_adtstoasc", testURL}},
		{"ffmpeg", "webm", []string{"-c copy"}, []string{"aac_adtstoasc", "-movflags"}},
		{"ffmpeg", "mkv", []string{"-c copy"}, []string{"aac_adtstoasc", "-movflags"}},
		// The link after "--": yt-dlp never reads it as an option.
		{"yt-dlp", "mkv", []string{"--merge-output-format mkv", " -- " + pageURL}, []string{"--merge-output-format mp4"}},
		{"yt-dlp", "mp4", []string{"--merge-output-format mp4", " -- " + pageURL}, nil},
	} {
		_ = os.Remove(argsFile)
		m, _ := newTestManager(t, func(cfg *config.Settings) {
			cfg.Downloads.Downloader, cfg.Transcode.FfmpegPath = c.downloader, filepath.Join(bin, "ffmpeg")
		})
		m.Resolvers["test"] = func(context.Context, *Item) (*Resolved, error) {
			if c.downloader == "yt-dlp" {
				return &Resolved{URL: pageURL, Container: c.container, Page: true}, nil
			}
			return &Resolved{URL: testURL, Container: c.container}, nil
		}
		it, err := m.Enqueue(Item{MediaID: 1, Episode: 1, Title: "Film", Source: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if got := waitFinal(t, m, it.ID, 15*time.Second); got.Status != StatusCompleted || !strings.HasSuffix(got.Output, "."+c.container) {
			t.Errorf("%s %s: %s %q (%s)", c.downloader, c.container, got.Status, got.Output, got.Error)
		}
		b, _ := os.ReadFile(argsFile)
		args := string(b)
		for _, w := range c.want {
			if !strings.Contains(args, w) {
				t.Errorf("%s %s: %q lacks %q", c.downloader, c.container, args, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(args, n) {
				t.Errorf("%s %s: %q has %q", c.downloader, c.container, args, n)
			}
		}
	}
}

// Finished downloads are named the way Plex, Jellyfin and the library read
// them — "Movie (Year)/Movie (Year).mp4", "Show (Year)/Season 02/Show -
// S02E05.mp4" — with names valid on every OS, in the show's folder when
// it has one, and are matched at once with what was downloaded.
func TestDownloadNames(t *testing.T) {
	m, lib := newTestManager(t, nil)
	titles := map[int]*catalog.Media{}
	add := func(id int, kind, title string, year int) {
		mm := &catalog.Media{ID: id, Type: kind, SeasonYear: &year}
		mm.Title.English, mm.Title.UserPreferred = title, title
		titles[id] = mm
	}
	add(603, catalog.TypeMovie, "The Matrix", 1999)
	add(catalog.ShowID(1396), catalog.TypeTV, "Breaking Bad", 2008)
	add(catalog.ShowID(85949), catalog.TypeTV, "Star Trek: Picard", 2020)
	m.Lookup = func(_ context.Context, id int) (*catalog.Media, error) {
		if mm := titles[id]; mm != nil {
			return mm, nil
		}
		return nil, catalog.ErrNotFound
	}
	m.fetch = func(_ *run, _ config.Settings, _ *Resolved, out string) error {
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	// Picard's episodes are already in a folder of the user's.
	own := filepath.Join(lib, "Picard")
	ownFile := filepath.Join(own, "Season 01", "Picard.S01E01.mkv")
	if err := os.MkdirAll(filepath.Dir(ownFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.files.Save(&library.LocalFile{Path: ownFile, Dir: filepath.Dir(ownFile), Name: filepath.Base(ownFile), MediaID: catalog.ShowID(85949), Episode: 1001, Kind: "main",
		Parsed: library.Parse(ownFile, []string{lib})}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		mediaID, episode int
		title            string
		want             string
		kind             string
	}{
		{603, 1, "Matrix", "The Matrix (1999)/The Matrix (1999).mp4", "main"},
		{catalog.ShowID(1396), 2005, "BB", "Breaking Bad (2008)/Season 02/Breaking Bad - S02E05.mp4", "main"},
		{catalog.ShowID(1396), 3, "BB", "Breaking Bad (2008)/Specials/Breaking Bad - S00E03.mp4", "special"},
		{catalog.ShowID(85949), 1002, "Picard", "Picard/Season 01/Star Trek： Picard - S01E02.mp4", "main"},
		{catalog.ShowID(77), 1001, "Not: On TMDB?", "Not： On TMDB/Season 01/Not： On TMDB - S01E01.mp4", "main"},
	} {
		it, err := m.Enqueue(Item{MediaID: c.mediaID, Episode: c.episode, Title: c.title, Source: "stream", URL: testURL})
		if err != nil {
			t.Fatal(err)
		}
		got := waitFinal(t, m, it.ID, 15*time.Second)
		want := filepath.Join(lib, filepath.FromSlash(c.want))
		if got.Status != StatusCompleted || got.Output != want {
			t.Errorf("%s: %s %q (%s), want %q", c.title, got.Status, got.Output, got.Error, want)
			continue
		}
		f, err := m.files.Get(want)
		if err != nil || f.MediaID != c.mediaID || f.Episode != c.episode || f.Kind != c.kind || !f.Locked {
			t.Errorf("%s: library entry %+v, %v", c.title, f, err)
		}
	}
	// The library reads the names back as what they are.
	f, _ := m.files.Get(filepath.Join(lib, "Breaking Bad (2008)", "Season 02", "Breaking Bad - S02E05.mp4"))
	if p := f.Parsed; p.Title != "Breaking Bad" || p.Year != 2008 || p.Season != 2 || p.Episode != 5 {
		t.Errorf("parsed back: %+v", p)
	}
	f, _ = m.files.Get(filepath.Join(lib, "The Matrix (1999)", "The Matrix (1999).mp4"))
	if p := f.Parsed; p.Title != "The Matrix" || p.Year != 1999 || p.Show {
		t.Errorf("parsed back: %+v", p)
	}
}
