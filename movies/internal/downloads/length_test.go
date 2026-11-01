package downloads

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

func TestParsePlaylist(t *testing.T) {
	media := "\ufeff#EXTM3U\r\n#EXT-X-VERSION:3\r\n#EXT-X-TARGETDURATION:10\r\n#EXTINF:10.0,\r\nseg0.jpg\r\n#EXTINF:9.5, a title\r\nseg1.jpg\r\n#EXTINF:4,\r\nseg2.jpg\r\n#EXT-X-ENDLIST\r\n"
	if total, variant, ok := parsePlaylist(media); !ok || total != 23.5 || variant != "" {
		t.Errorf("media playlist = %v, %q, %v; want 23.5, \"\", true", total, variant, ok)
	}
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1920x1080\nhi/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nlo/index.m3u8\n"
	if total, variant, ok := parsePlaylist(master); !ok || total != 0 || variant != "hi/index.m3u8" {
		t.Errorf("master playlist = %v, %q, %v; want 0, hi/index.m3u8, true", total, variant, ok)
	}
	for _, body := range []string{"", "<html>Forbidden</html>", "\x00\x00\x00 ftypisom"} {
		if _, _, ok := parsePlaylist(body); ok {
			t.Errorf("%q taken for a playlist", body)
		}
	}
}

func TestClock(t *testing.T) {
	for s, want := range map[float64]string{0: "0:00", 59.6: "1:00", 605: "10:05", 3725: "1:02:05", -3: "0:00"} {
		if got := clock(s); got != want {
			t.Errorf("clock(%v) = %q, want %q", s, got, want)
		}
	}
}

// An ffmpeg download of an HLS stream gets its percentage and time left
// from the playlist, with no ffprobe (the hub's case, 2026-10-30: 0% on
// every download).
func TestFfmpegProgressFromPlaylist(t *testing.T) {
	var referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ep/master.m3u8":
			referer = r.Header.Get("Referer")
			w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n1080/index.m3u8\n"))
		case "/ep/1080/index.m3u8":
			w.Write([]byte("#EXTM3U\n#EXTINF:400,\na.jpg\n#EXTINF:400,\nb.jpg\n#EXTINF:400,\nc.jpg\n#EXT-X-ENDLIST\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	allow(t, srv.URL)
	old := playlistClient
	playlistClient = srv.Client()
	t.Cleanup(func() { playlistClient = old })

	// 10 of the 20 minutes, at 10x: a minute left.
	ffmpeg := writeScript(t, t.TempDir(), "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
echo 'speed=10x'
sleep 0.8
echo 'out_time_us=600000000'
echo 'progress=continue'
printf 'video' > "$last"
`)
	m, _ := newTestManager(t, func(c *config.Settings) { c.Transcode.FfmpegPath = ffmpeg })
	rec := record(t, m.hub)
	it, err := m.Enqueue(Item{MediaID: 1, Episode: 1, Title: "Playlist", Mode: "sub", Source: "stream", URL: srv.URL + "/ep/master.m3u8", Referrer: "https://example.org/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFinal(t, m, it.ID, 15*time.Second); got.Status != StatusCompleted {
		t.Fatalf("status = %s (%s), want completed", got.Status, got.Error)
	}
	if referer != "https://example.org/" {
		t.Errorf("playlist asked for with Referer %q", referer)
	}
	var seen bool
	for _, ev := range rec.downloadEvents() {
		if ev.Payload.ID == it.ID && ev.Payload.Status == StatusDownloading && ev.Payload.Progress == 0.5 {
			seen = true
			if ev.Payload.ETA != "1:00" || ev.Payload.Done != "" {
				t.Errorf("at 50%%: eta %q, done %q; want 1:00 and nothing", ev.Payload.ETA, ev.Payload.Done)
			}
		}
	}
	if !seen {
		t.Error("no progress event at 50%")
	}
}

// When the length can't be found (no playlist, no ffprobe), it says how
// much video is downloaded instead of a percentage stuck at 0.
func TestFfmpegProgressWithoutLength(t *testing.T) {
	ffmpeg := writeScript(t, t.TempDir(), "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
echo 'out_time_us=605000000'
echo 'speed=12.5x'
echo 'progress=continue'
printf 'video' > "$last"
`)
	m, _ := newTestManager(t, func(c *config.Settings) { c.Transcode.FfmpegPath = ffmpeg })
	rec := record(t, m.hub)
	it := enqueue(t, m, "Unknown")
	if got := waitFinal(t, m, it.ID, 15*time.Second); got.Status != StatusCompleted || got.Done != "" || got.Progress != 1 {
		t.Fatalf("finished as %s, done %q, progress %v; want completed, nothing, 1", got.Status, got.Done, got.Progress)
	}
	var seen bool
	for _, ev := range rec.downloadEvents() {
		seen = seen || (ev.Payload.ID == it.ID && ev.Payload.Status == StatusDownloading && ev.Payload.Done == "10:05" && ev.Payload.Progress == 0)
	}
	if !seen {
		t.Error("no progress event saying 10:05 downloaded")
	}
}
