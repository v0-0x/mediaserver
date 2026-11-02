package stream

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// The proxy lets the in-app player load streams that need special headers
// (Referer/Origin) or lack CORS. HLS playlists are rewritten so every
// segment/key/sub-playlist also goes through the proxy. It refuses to
// connect to private addresses so it can't be used to reach the LAN.

var proxyClient = &http.Client{
	Timeout: 0, // streams can be long
	Transport: &http.Transport{
		Proxy:                 util.PublicProxy,
		DialContext:           util.PublicDialContext(15 * time.Second),
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   16,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// LocalHosts, when set, lists the addresses on this computer or the LAN the
// proxy may connect to all the same (as HostKey gives them): those of the
// Stremio addons the user installed on purpose as local ones, whose streams
// usually come from the addon's own address. Every other private address
// stays refused.
var LocalHosts func() []string

// localProxyClient reaches those addresses, and follows redirects only
// among them.
var localProxyClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   16,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return errors.New("too many redirects")
		}
		if !allowedLocal(req.URL) {
			return errors.New("redirected away from the local addon's address")
		}
		return nil
	},
}

// Credentials, when set, gives the headers authenticating a request for a
// stream's link with the media server it's on (the token of a Jellyfin,
// Emby or Plex server of Settings › Providers), nil for any other link.
// Moviko adds them when it fetches the link itself (proxy, relay) or has
// mpv or a downloader fetch it on this computer: they're never handed to
// the browser, nor put in links.
var Credentials func(u *url.URL) map[string]string

// credentialsFor is Credentials' answer for u (nil when unset).
func credentialsFor(u *url.URL) map[string]string {
	if Credentials == nil || u == nil {
		return nil
	}
	return Credentials(u)
}

// WithCredentials returns a stream's headers with the credentials its link
// needs added (for mpv and the downloaders, which fetch it themselves).
// headers is left alone.
func WithCredentials(link string, headers map[string]string) map[string]string {
	u, err := url.Parse(CleanLink(link))
	if err != nil {
		return headers
	}
	creds := credentialsFor(u)
	if len(creds) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers)+len(creds))
	for k, v := range headers {
		out[k] = v
	}
	for k, v := range creds {
		out[k] = v
	}
	return out
}

// HostKey is a URL's host and port, the port filled in from the scheme, to
// compare addresses.
func HostKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// allowedLocal reports whether u is at one of LocalHosts.
func allowedLocal(u *url.URL) bool {
	if LocalHosts == nil || u == nil {
		return false
	}
	k := HostKey(u)
	for _, h := range LocalHosts() {
		if h == k {
			return true
		}
	}
	return false
}

// ProxyURL builds the URL the browser should use for a remote resource.
func ProxyURL(target string, headers map[string]string) string {
	q := url.Values{}
	q.Set("u", base64.RawURLEncoding.EncodeToString([]byte(target)))
	if len(headers) > 0 {
		raw, _ := json.Marshal(headers)
		q.Set("h", base64.RawURLEncoding.EncodeToString(raw))
	}
	return "/api/proxy?" + q.Encode()
}

// A tag's URI, quoted or not (hls.js takes both).
var reURIAttr = regexp.MustCompile(`URI=(?:"([^"]*)"|([^,"\s]+))`)

// sameSiteHeaders are the headers a playlist's segments on another host
// get: what a CDN may check, never a login (Authorization, Cookie…), which
// is for the playlist's own host.
var sameSiteHeaders = map[string]bool{"referer": true, "origin": true, "user-agent": true}

// IsDocumentRequest reports a navigation or frame load. Proxied and cached
// remote content is only ever loaded as media/images/XHR by the app, never
// opened as a page on Moviko's origin.
func IsDocumentRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame", "embed", "object":
		return true
	}
	return false
}

// SafeContentType passes through media, image, playlist and subtitle types
// and turns anything a browser could run as a page (HTML, SVG, XML, JS…)
// into a plain download.
func SafeContentType(ct string) string {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	if mt == "" || strings.Contains(mt, "html") || strings.Contains(mt, "xml") || strings.Contains(mt, "svg") || strings.Contains(mt, "script") {
		return "application/octet-stream"
	}
	switch {
	case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "image/"):
		return ct
	case mt == "application/vnd.apple.mpegurl", mt == "application/x-mpegurl", mt == "application/mp4",
		mt == "text/vtt", mt == "text/plain", mt == "application/octet-stream":
		return ct
	}
	return "application/octet-stream"
}

// ServeProxy implements GET /api/proxy.
func ServeProxy(w http.ResponseWriter, r *http.Request) {
	// Remote content must never run as a page on Moviko's origin.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if IsDocumentRequest(r) {
		http.Error(w, "not a page", http.StatusForbidden)
		return
	}
	rawU, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("u"))
	if err != nil {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	target := CleanLink(string(rawU))
	tu, err := url.Parse(target)
	if err != nil || (tu.Scheme != "http" && tu.Scheme != "https") {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	local := allowedLocal(tu)
	if strings.EqualFold(tu.Hostname(), "localhost") && !local {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	headers := map[string]string{}
	if h := r.URL.Query().Get("h"); h != "" {
		if raw, err := base64.RawURLEncoding.DecodeString(h); err == nil {
			_ = json.Unmarshal(raw, &headers)
		}
	}
	format := r.URL.Query().Get("fmt")

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", util.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range credentialsFor(tu) {
		req.Header.Set(k, v)
	}
	if rg := r.Header.Get("Range"); rg != "" && format == "" {
		req.Header.Set("Range", rg)
	}
	client := proxyClient
	if local {
		client = localProxyClient
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ctype := resp.Header.Get("Content-Type")
	path := strings.ToLower(tu.Path)
	if resp.StatusCode >= 400 && (format != "" || strings.HasSuffix(path, ".m3u8") || strings.Contains(strings.ToLower(ctype), "mpegurl")) {
		// Pass errors on as errors: an HTML error page rewritten into a
		// "playlist" or "subtitle" would hide the real cause (an expired
		// link, a missing Referer…) behind a parse error.
		http.Error(w, "upstream: "+resp.Status, resp.StatusCode)
		return
	}
	isPlaylist := strings.Contains(strings.ToLower(ctype), "mpegurl") || strings.HasSuffix(path, ".m3u8")
	if !isPlaylist && resp.StatusCode < 400 && format == "" {
		// Some hosts serve playlists as text/plain or octet-stream (also
		// gzipped or chunked, so the length is often unknown).
		peek := bufio.NewReader(resp.Body)
		head, _ := peek.Peek(7)
		if string(head) == "#EXTM3U" {
			isPlaylist = true
		}
		resp.Body = io.NopCloser(peek)
	}

	w.Header().Set("Cache-Control", "no-store")

	switch {
	case isPlaylist:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		base := resp.Request.URL
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write(rewritePlaylist(body, base, headers))
	case format == "vtt":
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write(ToVTT(body, path))
	default:
		for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", SafeContentType(ctype))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func rewritePlaylist(body []byte, base *url.URL, headers map[string]string) []byte {
	abs := func(ref string) string {
		u, err := base.Parse(strings.TrimSpace(ref))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return ""
		}
		h := headers
		if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
			h = map[string]string{}
			for k, v := range headers {
				if sameSiteHeaders[strings.ToLower(k)] {
					h[k] = v
				}
			}
		}
		return ProxyURL(u.String(), h)
	}
	// A lone CR ends a line too, for hls.js: a URL after one must be seen.
	body = bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			out.WriteString(line)
		case strings.HasPrefix(trimmed, "#"):
			out.WriteString(reURIAttr.ReplaceAllStringFunc(line, func(m string) string {
				sub := reURIAttr.FindStringSubmatch(m)
				return `URI="` + abs(sub[1]+sub[2]) + `"`
			}))
		default:
			out.WriteString(abs(trimmed))
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// ---------------------------------------------------------------------------
// Subtitle conversion (SRT/ASS -> WebVTT) for the in-app player.

var (
	reSrtTime = regexp.MustCompile(`\b(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\b`)
	reAssTag  = regexp.MustCompile(`\{[^}]*\}`)
)

// ToVTT converts subtitles to WebVTT based on their content/extension:
// WebVTT, SubRip (SRT) and ASS/SSA, in UTF-8, UTF-16 or (old SubRip files
// mostly) Windows-1252, gzipped or not.
func ToVTT(body []byte, name string) []byte {
	text := strings.ReplaceAll(subtitleText(body), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	switch {
	case strings.HasPrefix(strings.TrimSpace(text), "WEBVTT"):
		return []byte(text)
	case strings.Contains(text, "[Script Info]") || strings.HasSuffix(name, ".ass") || strings.HasSuffix(name, ".ssa"):
		return assToVTT(text)
	default:
		return srtToVTT(text)
	}
}

// srtToVTT turns SubRip into WebVTT: the times of its cues get a dot
// before their milliseconds, two digits of hours and three of
// milliseconds (SubRip files are written anyhow).
func srtToVTT(text string) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "-->") {
			line = reSrtTime.ReplaceAllStringFunc(line, func(t string) string {
				m := reSrtTime.FindStringSubmatch(t)
				return fmt.Sprintf("%s:%s:%s.%s", ("0" + m[1])[len(m[1])-1:], m[2], m[3], (m[4] + "00")[:3])
			})
		}
		out.WriteString(line + "\n")
	}
	return []byte(out.String())
}

// subtitleText is a subtitle file's text in UTF-8: gunzipped, from UTF-16
// (by its byte order mark), and from Windows-1252 when it isn't UTF-8.
func subtitleText(body []byte) string {
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			if b, err := io.ReadAll(io.LimitReader(zr, 16<<20)); err == nil {
				body = b
			}
		}
	}
	switch {
	case bytes.HasPrefix(body, []byte{0xff, 0xfe}):
		return utf16Text(body[2:], binary.LittleEndian)
	case bytes.HasPrefix(body, []byte{0xfe, 0xff}):
		return utf16Text(body[2:], binary.BigEndian)
	}
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if utf8.Valid(body) {
		return string(body)
	}
	var b strings.Builder
	for _, c := range body {
		if c >= 0x80 && c < 0xa0 {
			b.WriteRune(cp1252[c-0x80])
		} else {
			b.WriteRune(rune(c)) // Latin-1
		}
	}
	return b.String()
}

func utf16Text(b []byte, order binary.ByteOrder) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, order.Uint16(b[i:]))
	}
	return string(utf16.Decode(units))
}

// cp1252 are the characters of Windows-1252's bytes 0x80 to 0x9f (the
// others are Latin-1's).
var cp1252 = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8d, 'Ž', 0x8f,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9d, 'ž', 'Ÿ',
}

func assTime(t string) string {
	// h:mm:ss.cc -> hh:mm:ss.mmm
	parts := strings.Split(strings.TrimSpace(t), ":")
	if len(parts) != 3 {
		return "00:00:00.000"
	}
	h := parts[0]
	if len(h) < 2 {
		h = "0" + h
	}
	sec := parts[2]
	if i := strings.IndexByte(sec, '.'); i >= 0 {
		cs := sec[i+1:]
		for len(cs) < 3 {
			cs += "0"
		}
		sec = sec[:i] + "." + cs[:3]
	} else {
		sec += ".000"
	}
	return h + ":" + parts[1] + ":" + sec
}

func assToVTT(text string) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	format := []string{}
	inEvents := false
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") {
			inEvents = strings.EqualFold(l, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}
		if strings.HasPrefix(l, "Format:") {
			for _, f := range strings.Split(strings.TrimPrefix(l, "Format:"), ",") {
				format = append(format, strings.ToLower(strings.TrimSpace(f)))
			}
			continue
		}
		if !strings.HasPrefix(l, "Dialogue:") || len(format) == 0 {
			continue
		}
		fields := strings.SplitN(strings.TrimPrefix(l, "Dialogue:"), ",", len(format))
		if len(fields) < len(format) {
			continue
		}
		get := func(name string) string {
			for i, f := range format {
				if f == name {
					return strings.TrimSpace(fields[i])
				}
			}
			return ""
		}
		txt := fields[len(format)-1]
		txt = reAssTag.ReplaceAllString(txt, "")
		txt = strings.NewReplacer(`\N`, "\n", `\n`, "\n", `\h`, " ").Replace(txt)
		if strings.TrimSpace(txt) == "" {
			continue
		}
		out.WriteString(assTime(get("start")) + " --> " + assTime(get("end")) + "\n" + strings.TrimSpace(txt) + "\n\n")
	}
	return []byte(out.String())
}
