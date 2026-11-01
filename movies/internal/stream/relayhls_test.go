package stream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// ffmpegLinks are the links ffmpeg would open in a playlist: its link
// lines and the URI attributes of its tags, read as ffmpeg reads them.
func ffmpegLinks(playlist string) []string {
	var out []string
	for i, line := range splitLines([]byte(playlist)) {
		line = strings.TrimRight(line, " \t\v\f")
		switch {
		case i == 0 || line == "":
		case strings.HasPrefix(line, "#"):
			_, attrs, _ := strings.Cut(line, ":")
			for _, a := range parseAttrs(attrs) {
				if strings.Contains(strings.ToUpper(a.key), "URI") {
					out = append(out, a.value)
				}
			}
		default:
			out = append(out, line)
		}
	}
	return out
}

// Every link ffmpeg would open in a rewritten playlist is the relay's,
// however the playlist hides one: lines ended by "\r" or a NUL byte (as
// ffmpeg ends them), unquoted or escaped URIs, links in other tags.
func TestRewriteHLS(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/hls/ep1/index.m3u8")
	var named []string
	relay := func(kind, link string) string {
		named = append(named, kind+" "+link)
		return fmt.Sprintf("http://relay/%d", len(named)-1)
	}
	in := "\xef\xbb\xbf#EXTM3U\r\n" +
		"#EXT-X-VERSION:3\n" +
		"#EXT-X-TARGETDURATION:4\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\",IV=0x0123\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=http://192.168.1.1/k\n" +
		"#EXT-X-MAP:URI=\"http:\\/\\/10.0.0.1\\/init.mp4\",BYTERANGE=\"720@0\"\n" +
		"#EXTINF:4,\rhttp://10.0.0.2/a.ts\n" +
		"#EXTINF:4,\x00http://10.0.0.3/b.ts\n" +
		"#EXT-X-DATERANGE:ID=\"ad\",X-ASSET-URI=\"http://10.0.0.4/\"\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"data:text/plain;base64,AAAA\"\n" +
		"#EXT-X-MAP:URI=\"file:///etc/passwd\"\n" +
		"#EXTINF:4,\n" +
		"  seg 3.ts  \n" +
		"#EXTINF:4,\n" +
		"rtp://10.0.0.5:5004\n" +
		"#EXT-X-ENDLIST\n"
	out, info, err := rewriteHLS([]byte(in), base, "", relay)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"k https://cdn.example/hls/ep1/key.bin",
		"k http://192.168.1.1/k",
		"m http://10.0.0.1/init.mp4",
		"m http://10.0.0.2/a.ts",
		"m http://10.0.0.3/b.ts",
		"m https://cdn.example/hls/ep1/seg%203.ts",
	}
	if strings.Join(named, "|") != strings.Join(want, "|") {
		t.Errorf("relayed\n %q\nwant\n %q", named, want)
	}
	if !info.aes {
		t.Error("AES-128 keys not seen")
	}
	text := string(out)
	if !strings.HasPrefix(text, "#EXTM3U\n") {
		t.Errorf("playlist starts with %.20q", text)
	}
	for _, l := range ffmpegLinks(text) {
		if !strings.HasPrefix(l, "http://relay/") {
			t.Errorf("ffmpeg would open %q:\n%s", l, text)
		}
	}
	for _, keep := range []string{"#EXT-X-TARGETDURATION:4", `IV="0x0123"`, `BYTERANGE="720@0"`, "#EXT-X-ENDLIST"} {
		if !strings.Contains(text, keep) {
			t.Errorf("rewritten playlist lacks %q:\n%s", keep, text)
		}
	}

	// Not a playlist.
	for _, body := range []string{"", "<MPD/>", "ffconcat version 1.0\nfile 'http://10.0.0.1/x'"} {
		if _, _, err := rewriteHLS([]byte(body), base, "", relay); err != errNotPlaylist {
			t.Errorf("%q: %v", body, err)
		}
	}
}

// Of a master playlist, ffmpeg gets one variant and the renditions it
// uses: the best one browsers play as it is for the in-app player, the
// best one for downloads.
func TestRewriteHLSPicksVariant(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/master.m3u8")
	in := `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aac",NAME="English",URI="audio/aac.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="ec3",NAME="English 5.1",URI="audio/ec3.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=800000,CODECS="avc1.4d401e,mp4a.40.2",AUDIO="aac"
low/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,CODECS="avc1.640028,mp4a.40.2",AUDIO="aac"
mid/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=9000000,CODECS="hvc1.2.4.L150.90,ec-3",AUDIO="ec3"
high/index.m3u8
#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=90000,URI="iframes.m3u8"
`
	for _, tc := range []struct {
		pick string
		want []string
	}{
		{pickPlayable, []string{"p https://cdn.example/audio/aac.m3u8", "p https://cdn.example/mid/index.m3u8"}},
		{pickBest, []string{"p https://cdn.example/audio/ec3.m3u8", "p https://cdn.example/high/index.m3u8"}},
		{"", []string{"p https://cdn.example/audio/aac.m3u8", "p https://cdn.example/audio/ec3.m3u8",
			"p https://cdn.example/low/index.m3u8", "p https://cdn.example/mid/index.m3u8", "p https://cdn.example/high/index.m3u8",
			"p https://cdn.example/iframes.m3u8"}},
	} {
		var named []string
		out, info, err := rewriteHLS([]byte(in), base, tc.pick, func(kind, link string) string {
			named = append(named, kind+" "+link)
			return "http://relay/" + strconv.Itoa(len(named))
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(named, "|") != strings.Join(tc.want, "|") {
			t.Errorf("pick %q: relayed %q, want %q\n%s", tc.pick, named, tc.want, out)
		}
		if n := strings.Count(string(out), "#EXT-X-STREAM-INF"); tc.pick != "" && n != 1 {
			t.Errorf("pick %q: %d variants left", tc.pick, n)
		}
		// The playlists to look over for keys: not the I-frame ones.
		if n := len(tc.want) - strings.Count(strings.Join(tc.want, " "), "iframes"); len(info.playlists) != n {
			t.Errorf("pick %q: playlists %q", tc.pick, info.playlists)
		}
	}
	// Without CODECS, the best one.
	plain := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\na.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nb.m3u8\n"
	out, _, _ := rewriteHLS([]byte(plain), base, pickPlayable, func(kind, link string) string { return link })
	if !strings.Contains(string(out), "b.m3u8") || strings.Contains(string(out), "a.m3u8") {
		t.Errorf("without codecs:\n%s", out)
	}
}

// makeHLS encodes an 8-second HLS stream (2-second MPEG-TS segments, H.264
// and AAC) into dir with the extra ffmpeg options.
func makeHLS(t *testing.T, dir string, extra ...string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=24", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "8", "-c:v", "libx264", "-g", "48", "-c:a", "aac", "-shortest",
		"-f", "hls", "-hls_time", "2", "-hls_playlist_type", "vod", "-hls_segment_filename", filepath.Join(dir, "seg%d.ts")}
	args = append(append(args, extra...), filepath.Join(dir, "index.m3u8"))
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Skipf("can't make an HLS stream: %v %s", err, out)
	}
}

// copyHLS has ffmpeg copy what it reads from input (with the input
// options args) into an MP4 file, as downloads do, and returns how long
// the copy is.
func copyHLS(t *testing.T, input string, args []string) (float64, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "copy.mp4")
	a := append([]string{"-y", "-nostdin", "-loglevel", "error"}, args...)
	a = append(a, "-i", input, "-map", "0:v?", "-map", "0:a?", "-c", "copy", out)
	if b, err := exec.Command("ffmpeg", a...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%v: %s", err, b)
	}
	b, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

// HLS streams play and download through the relay: a plain one, an
// AES-128 encrypted one (with the crypto protocol, only then), and a
// master playlist naming both.
func TestHLSRelay(t *testing.T) {
	dir := t.TempDir()
	makeHLS(t, filepath.Join(dir, "plain"))
	keyInfo := filepath.Join(dir, "keyinfo")
	if err := os.WriteFile(filepath.Join(dir, "enc.key"), []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyInfo, []byte("key.bin\n"+filepath.Join(dir, "enc.key")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeHLS(t, filepath.Join(dir, "enc"), "-hls_key_info_file", keyInfo)
	var sawReferer atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "https://site.example/" {
			sawReferer.Store(true)
		}
		switch r.URL.Path {
		case "/enc/key.bin":
			http.ServeFile(w, r, filepath.Join(dir, "enc.key"))
		case "/master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=500000\nplain/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=900000\nenc/index.m3u8\n")
		default:
			http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
		}
	}))
	defer provider.Close()
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)
	u, _ := url.Parse(provider.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} } // stands for a public host

	for _, tc := range []struct {
		name   string
		crypto bool
	}{{"/plain/index.m3u8", false}, {"/enc/index.m3u8", true}, {"/master.m3u8", true}} {
		input, args, err := RelayHLS(context.Background(), provider.URL+tc.name, map[string]string{"Referer": "https://site.example/"}, "ffmpeg")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		a := strings.Join(args, " ")
		if want := "-protocol_whitelist http,tcp -f hls"; tc.crypto {
			want = "-protocol_whitelist http,tcp,crypto -f hls"
			if !strings.HasPrefix(a, want) {
				t.Errorf("%s: input options %q, want %q", tc.name, a, want)
			}
		} else if !strings.HasPrefix(a, want) {
			t.Errorf("%s: input options %q, want %q", tc.name, a, want)
		}
		if d, err := copyHLS(t, input, args); err != nil || d < 7.5 || d > 8.5 {
			t.Errorf("%s: copied %vs (%v), want 8", tc.name, d, err)
		}
	}
	if !sawReferer.Load() {
		t.Error("the stream's headers weren't sent")
	}

	// The in-app player probes it like any online stream.
	l, _ := newFfmpegLocal(t, "libx264")
	p, err := l.Probe(context.Background(), RegisterRemoteHLS(provider.URL+"/master.m3u8", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Video) != 1 || p.Video[0].Codec != "h264" || len(p.Audio) != 1 || p.Audio[0].Codec != "aac" || p.Duration < 7.5 {
		t.Errorf("probe: %+v", p)
	}

	// Names the relay never gave.
	addr, _ := startRelay()
	for _, path := range []string{"/h/0123456789abcdef0123456789abcdef", "/h/", "/h/../r/x"} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}
}

// Whatever an HLS stream's playlists name (keys, init sections, segments,
// renditions, variants, redirects, segments that are playlists in
// disguise, manifests of another kind), ffmpeg reading it through the
// relay never reaches the LAN.
func TestHLSRelayNeverReachesLAN(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	l, _ := newFfmpegLocal(t, "libx264")
	var lanHits atomic.Int32
	lan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lanHits.Add(1)
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:2,\nseg.ts\n#EXT-X-ENDLIST\n")
	}))
	defer lan.Close()
	dir := t.TempDir()
	makeHLS(t, dir)
	seg, err := os.ReadFile(filepath.Join(dir, "seg0.ts"))
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		media := func(lines ...string) {
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n"+strings.Join(lines, "\n")+"\n#EXTINF:2,\nseg0.ts\n#EXT-X-ENDLIST\n")
		}
		switch r.URL.Path {
		case "/seg0.ts":
			_, _ = w.Write(seg)
		case "/key.m3u8":
			media(`#EXT-X-KEY:METHOD=AES-128,URI="` + lan.URL + `/key"`)
		case "/key-unquoted.m3u8":
			media(`#EXT-X-KEY:METHOD=AES-128,URI=` + lan.URL + `/key`)
		case "/map.m3u8":
			media(`#EXT-X-MAP:URI="` + strings.ReplaceAll(lan.URL, "/", `\/`) + `/init.mp4"`)
		case "/segments.m3u8":
			media("#EXTINF:2,\r"+lan.URL+"/a.ts", "#EXTINF:2,\x00"+lan.URL+"/b.ts", "#EXTINF:2,", lan.URL+"/c.ts")
		case "/disguised.m3u8":
			media("#EXTINF:2,", "/fake.ts")
		case "/fake.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:2,\n%s/x.ts\n#EXT-X-ENDLIST\n", lan.URL)
		case "/master.m3u8":
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"x\",URI=\"%s/audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"a\"\n%s/v.m3u8\n", lan.URL, lan.URL)
		case "/redirect.m3u8":
			http.Redirect(w, r, lan.URL+"/x.m3u8", http.StatusFound)
		case "/dash.m3u8":
			fmt.Fprintf(w, `<?xml version="1.0"?><MPD><BaseURL>%s/</BaseURL></MPD>`, lan.URL)
		case "/variant-dash.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/dash.m3u8\n")
		}
	}))
	defer provider.Close()
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)
	u, _ := url.Parse(provider.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} } // stands for a public host

	for _, name := range []string{"/key.m3u8", "/key-unquoted.m3u8", "/map.m3u8", "/segments.m3u8", "/disguised.m3u8",
		"/master.m3u8", "/redirect.m3u8", "/dash.m3u8", "/variant-dash.m3u8"} {
		_, _ = l.Probe(context.Background(), RegisterRemoteHLS(provider.URL+name, nil))
		if input, args, err := RelayHLS(context.Background(), provider.URL+name, nil, "ffmpeg"); err == nil {
			_, _ = copyHLS(t, input, args)
		}
		if n := lanHits.Load(); n > 0 {
			t.Fatalf("%s: ffmpeg reached a LAN address %d times", name, n)
		}
	}
	// A segment that's a playlist isn't passed on: only the real one is
	// copied.
	input, args, err := RelayHLS(context.Background(), provider.URL+"/disguised.m3u8", nil, "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	if d, err := copyHLS(t, input, args); err != nil || d > 2.5 {
		t.Errorf("copied %vs (%v), want the one real segment", d, err)
	}
}

// An online HLS stream plays as it is when the browser decodes it, and
// is converted like any other online stream when it doesn't: HEVC video,
// AC-3 or E-AC-3 audio.
func TestRemoteHLSConverts(t *testing.T) {
	l, _ := newFfmpegLocal(t, "libx264")
	dir := t.TempDir()
	makeHLS(t, filepath.Join(dir, "plain"))
	makeHLS(t, filepath.Join(dir, "ac3"), "-c:a", "ac3")
	makeHLS(t, filepath.Join(dir, "eac3"), "-c:a", "eac3")
	makeHLS(t, filepath.Join(dir, "hevc"), "-c:v", "libx265", "-tag:v", "hvc1", "-x265-params", "log-level=error")
	provider := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer provider.Close()
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)
	u, _ := url.Parse(provider.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} } // stands for a public host

	for _, tc := range []struct {
		name   string
		caps   Caps
		method string
		video  string // the converted stream's video codec
	}{
		{"plain", nil, "direct", ""},
		{"ac3", nil, "transcode", "h264"},
		{"eac3", nil, "transcode", "h264"},
		{"hevc", nil, "transcode", "h264"},
		{"hevc", CapsOf([]string{"hevc"}), "direct", ""},
	} {
		path := RegisterRemoteHLS(provider.URL+"/"+tc.name+"/index.m3u8", nil)
		p, err := l.Probe(context.Background(), path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		l.Decide(p, tc.caps)
		if p.Container != "hls" || p.Method != tc.method {
			t.Errorf("%s: %s %s (%s), want %s", tc.name, p.Container, p.Method, p.Reason, tc.method)
		}
		if tc.method == "direct" {
			continue
		}
		// Converted from 4 seconds in: H.264 and AAC in fragmented MP4.
		start, err := l.SeekPoint(context.Background(), path, 4, 0, "", tc.caps, false)
		if err != nil {
			t.Fatalf("%s: seek point: %v", tc.name, err)
		}
		w := httptest.NewRecorder()
		l.ServeTranscode(w, httptest.NewRequest("GET", "/api/local/transcode", nil), path, start, 0, "", tc.caps)
		if w.Code != http.StatusOK || w.Body.Len() < 1000 {
			t.Fatalf("%s: transcode %d, %d bytes", tc.name, w.Code, w.Body.Len())
		}
		f := filepath.Join(t.TempDir(), "out.mp4")
		if err := os.WriteFile(f, w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", f).Output()
		if got := strings.Fields(string(out)); strings.Join(got, ",") != tc.video+",aac" {
			t.Errorf("%s: converted to %q, want %s and aac", tc.name, got, tc.video)
		}
		if d := probeDuration(t, w.Body.Bytes()); d < 3 || d > 8.5-start {
			t.Errorf("%s: converted from %vs: %vs long", tc.name, start, d)
		}
	}

	// Safari, iPhones and iPads get the conversion as HLS sessions.
	h := NewHLS(l, filepath.Join(t.TempDir(), "hls"))
	t.Cleanup(h.Close)
	s, err := h.Start(context.Background(), HLSRequest{Path: RegisterRemoteHLS(provider.URL+"/eac3/index.m3u8", nil), Client: "a"})
	if err != nil || s.Method != "transcode" {
		t.Fatalf("HLS session: %+v, %v", s, err)
	}
}
