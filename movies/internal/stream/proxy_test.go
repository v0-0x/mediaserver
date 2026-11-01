package stream

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestSafeContentType(t *testing.T) {
	for in, want := range map[string]string{
		"video/mp4":                     "video/mp4",
		"application/vnd.apple.mpegurl": "application/vnd.apple.mpegurl",
		"image/jpeg":                    "image/jpeg",
		"text/vtt; charset=utf-8":       "text/vtt; charset=utf-8",
		"text/html; charset=utf-8":      "application/octet-stream",
		"image/svg+xml":                 "application/octet-stream",
		"application/xhtml+xml":         "application/octet-stream",
		"text/xml":                      "application/octet-stream",
		"application/javascript":        "application/octet-stream",
		"":                              "application/octet-stream",
		"application/x-shockwave-flash": "application/octet-stream",
		"video/mp2t":                    "video/mp2t",
		"application/octet-stream":      "application/octet-stream",
	} {
		if got := SafeContentType(in); got != want {
			t.Errorf("SafeContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewritePlaylist(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/hls/ep7/index.m3u8")
	in := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:4.0,\nseg-1.ts\n\n#EXTINF:4.0,\nhttps://other.example.com/seg-2.ts\n"
	out := string(rewritePlaylist([]byte(in), base, map[string]string{"Referer": "https://site.example/"}))
	for _, want := range []string{
		"URI=\"" + ProxyURL("https://cdn.example.com/hls/ep7/key.bin", map[string]string{"Referer": "https://site.example/"}) + "\"",
		ProxyURL("https://cdn.example.com/hls/ep7/seg-1.ts", map[string]string{"Referer": "https://site.example/"}),
		ProxyURL("https://other.example.com/seg-2.ts", map[string]string{"Referer": "https://site.example/"}),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten playlist lacks %q:\n%s", want, out)
		}
	}
}

// The proxy refuses this computer and the LAN, except the addresses of
// the addons installed as local ones (LocalHosts), and doesn't follow a
// redirect from those to another local address.
func TestProxyLocalHosts(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("elsewhere"))
	}))
	defer other.Close()
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/away" {
			http.Redirect(w, r, other.URL+"/x", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("video bytes"))
	}))
	defer addon.Close()
	get := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ServeProxy(w, httptest.NewRequest("GET", ProxyURL(target, nil), nil))
		return w
	}
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)

	LocalHosts = nil
	if w := get(addon.URL + "/stream.mp4"); w.Code == http.StatusOK {
		t.Fatalf("a local address without permission: %d %q", w.Code, w.Body.String())
	}
	u, _ := url.Parse(addon.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} }
	if w := get(addon.URL + "/stream.mp4"); w.Code != http.StatusOK || w.Body.String() != "video bytes" {
		t.Fatalf("the local addon: %d %q", w.Code, w.Body.String())
	}
	if w := get(other.URL + "/x"); w.Code == http.StatusOK {
		t.Fatalf("another local address: %d", w.Code)
	}
	if w := get(addon.URL + "/away"); w.Code == http.StatusOK {
		t.Fatalf("redirected to another local address: %d %q", w.Code, w.Body.String())
	}
	if HostKey(&url.URL{Scheme: "https", Host: "Box.LAN"}) != "box.lan:443" || HostKey(&url.URL{Scheme: "http", Host: "[::1]:7000"}) != "[::1]:7000" {
		t.Error("HostKey")
	}
}

// Subtitles come as WebVTT whatever they were: SubRip written anyhow
// (one-digit hours, short milliseconds, CRLF, a byte order mark), in
// Windows-1252 or UTF-16, gzipped; ASS; WebVTT as it is.
func TestToVTT(t *testing.T) {
	gz := func(s string) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return b.Bytes()
	}
	utf16le := func(s string) []byte {
		b := []byte{0xff, 0xfe}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	srt := "1\r\n00:00:01,500 --> 00:00:03,000\r\nHello there\r\n\r\n2\r\n0:00:04,5 --> 0:00:06,25\r\nBye\r\n"
	want := "WEBVTT\n\n1\n00:00:01.500 --> 00:00:03.000\nHello there\n\n2\n00:00:04.500 --> 00:00:06.250\nBye\n"
	for _, tc := range []struct {
		name string
		in   []byte
		want string // the WebVTT, or a part of it
	}{
		{"a.srt", []byte(srt), want},
		{"a.srt", append([]byte("\xef\xbb\xbf"), srt...), want},
		{"a.srt", gz(srt), want},
		{"a.srt", utf16le(srt), want},
		{"a.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\n\x93Caf\xe9\x94 \x96 cr\xe8me br\xfbl\xe9e\n"), "“Café” – crème brûlée"},
		{"a.ass", []byte("[Script Info]\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:01.50,0:00:03.00,Default,{\\i1}Hi{\\i0}\\Nthere\n"), "00:00:01.500 --> 00:00:03.000\nHi\nthere"},
		{"a.vtt", []byte("WEBVTT\n\n00:01.000 --> 00:02.000\nAs it is\n"), "WEBVTT\n\n00:01.000 --> 00:02.000\nAs it is\n"},
	} {
		got := string(ToVTT(tc.in, tc.name))
		if !strings.HasPrefix(got, "WEBVTT") || !strings.Contains(got, tc.want) {
			t.Errorf("%s %.20q:\n%s\nwant %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Every URL hls.js would load goes through the proxy (none left to load
// from KumoBox's own address), and a login header stays with its host.
func TestRewritePlaylistLeavesNothingOut(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/hls/index.m3u8")
	h := map[string]string{"Referer": "https://site.example/", "Authorization": "Bearer secret"}
	in := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=http://127.0.0.1:43311/api/settings\n#EXT-X-MAP:URI=http://192.168.1.1/init.mp4\n#EXTINF:4.0,\rhttp://127.0.0.1:43311/api/x\nseg.ts\nhttps://evil.example/seg.ts\njavascript:alert(1)\n"
	out := string(rewritePlaylist([]byte(in), base, h))
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#EXTM3U") || strings.HasPrefix(line, "#EXTINF") {
			continue
		}
		if !strings.Contains(line, "/api/proxy?") && line != "" {
			t.Errorf("not proxied: %q", line)
		}
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "javascript") {
		t.Errorf("a raw URL is left:\n%s", out)
	}
	if !strings.Contains(out, ProxyURL("https://cdn.example.com/hls/seg.ts", h)) {
		t.Errorf("the playlist's host lost its headers:\n%s", out)
	}
	if !strings.Contains(out, ProxyURL("https://evil.example/seg.ts", map[string]string{"Referer": "https://site.example/"})) {
		t.Errorf("another host got the login, or lost the Referer:\n%s", out)
	}
}
