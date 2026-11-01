package stream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Online streams in the in-app player
//
// Providers mostly link to files browsers can't play as they are: Matroska,
// HEVC video, AC-3/DTS/TrueHD audio. The in-app player converts those like
// library files (remux or transcode with ffmpeg, see local.go and hls.go),
// with the stream's link as ffmpeg's input instead of a file.
//
// ffmpeg doesn't fetch the link itself: the player is handed a "remote:"
// path naming the link (RegisterRemote), and ffmpeg reads it from a relay
// on this computer's loopback address, which fetches it with the stream
// proxy's client (its headers, byte ranges for seeking, and above all the
// same refusal to reach this computer or the LAN, redirects included).
// HLS links (RegisterRemoteHLS) go through the relay too, their playlists
// rewritten so that ffmpeg reads everything they name from it as well
// (relayhls.go). Downloads use the same client and relay (Fetch,
// RelayHLS).

// RemotePrefix starts the paths of online streams.
const RemotePrefix = "remote:"

// IsRemoteInput reports whether path names an online stream.
func IsRemoteInput(path string) bool { return strings.HasPrefix(path, RemotePrefix) }

const (
	remoteKeep = 12 * time.Hour // a link unused this long is forgotten
	remoteMax  = 256
)

type remoteSource struct {
	url     string
	headers map[string]string
	used    time.Time
	// hls: the link is an HLS playlist, served rewritten (relayhls.go),
	// with the variant pick picks when it's a master playlist. Once
	// scanned, crypto says whether its playlists have AES-128 keys (ffmpeg
	// then needs its crypto protocol).
	hls, scanned, crypto bool
	pick                 string
	// handler serves the stream on this computer instead of a link (a
	// torrent being played: RegisterRemoteHandler); key names it.
	handler http.Handler
	key     string
}

var remotes = struct {
	sync.Mutex
	m map[string]*remoteSource
}{m: map[string]*remoteSource{}}

// RegisterRemote names a stream's link (and the headers it needs) for the
// in-app player's conversion endpoints: the "remote:" path it returns is
// as good as a library file's path there. The name is unguessable, so
// nobody can have KumoBox fetch other links through it.
func RegisterRemote(link string, headers map[string]string) string {
	return register(link, headers, false, "")
}

// RegisterRemoteHLS is RegisterRemote for an HLS playlist's link: ffmpeg
// reads it, and everything it names, through the relay (relayhls.go). Of
// a master playlist, ffmpeg gets the best variant browsers play as it is
// when there's one (pickPlayable), else the best one.
func RegisterRemoteHLS(link string, headers map[string]string) string {
	return register(link, headers, true, pickPlayable)
}

// RegisterRemoteHandler names a stream served on this computer (a torrent
// being played, by key) for the conversion endpoints, like RegisterRemote:
// ffmpeg reads it from the relay, which has h serve it.
func RegisterRemoteHandler(key string, h http.Handler) string {
	remotes.Lock()
	defer remotes.Unlock()
	now := time.Now()
	for id, s := range remotes.m {
		if s.handler != nil && s.key == key {
			s.handler, s.used = h, now
			return RemotePrefix + id
		}
	}
	forgetLocked(now)
	id := newSessionID()
	remotes.m[id] = &remoteSource{handler: h, key: key, used: now}
	return RemotePrefix + id
}

func register(link string, headers map[string]string, hls bool, pick string) string {
	remotes.Lock()
	defer remotes.Unlock()
	now := time.Now()
	for id, s := range remotes.m {
		if s.handler == nil && s.url == link && s.hls == hls && s.pick == pick && sameHeaders(s.headers, headers) {
			s.used = now
			return RemotePrefix + id
		}
	}
	forgetLocked(now)
	h := make(map[string]string, len(headers))
	for k, v := range headers {
		h[k] = v
	}
	id := newSessionID()
	remotes.m[id] = &remoteSource{url: link, headers: h, used: now, hls: hls, pick: pick}
	return RemotePrefix + id
}

// forgetLocked forgets the stale links, then the least recently used ones,
// to make room for one.
func forgetLocked(now time.Time) {
	for id, s := range remotes.m {
		if now.Sub(s.used) > remoteKeep {
			delete(remotes.m, id)
		}
	}
	for len(remotes.m) >= remoteMax {
		var oldest string
		for id, s := range remotes.m {
			if oldest == "" || s.used.Before(remotes.m[oldest].used) {
				oldest = id
			}
		}
		delete(remotes.m, oldest)
	}
}

func sameHeaders(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func remoteSourceOf(path string) (*remoteSource, string, bool) {
	id := strings.TrimPrefix(path, RemotePrefix)
	remotes.Lock()
	defer remotes.Unlock()
	s, ok := remotes.m[id]
	if !ok {
		return nil, "", false
	}
	s.used = time.Now()
	return s, id, true
}

// ErrRemoteGone: the player asked for a stream KumoBox no longer knows
// (forgotten, or KumoBox restarted): it asks the provider again.
var ErrRemoteGone = errors.New("this stream has expired, play it again")

// remoteInput is the relay URL ffmpeg reads an online stream from. An HLS
// link's playlists are looked over first (once: see relayInputArgs).
func remoteInput(path string) (string, error) {
	src, id, ok := remoteSourceOf(path)
	if !ok {
		return "", ErrRemoteGone
	}
	addr, err := startRelay()
	if err != nil {
		return "", err
	}
	if src.hls {
		ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
		defer cancel()
		if err := scanOnce(ctx, src); err != nil {
			return "", err
		}
	}
	return "http://" + addr + "/r/" + id, nil
}

// relayedSource is the stream a relay URL (remoteInput's) stands for, nil
// for any other input.
func relayedSource(input string) *remoteSource {
	addr, err := startRelay()
	if err != nil {
		return nil
	}
	id, ok := strings.CutPrefix(input, "http://"+addr+"/r/")
	if !ok {
		return nil
	}
	src, _, _ := remoteSourceOf(RemotePrefix + id)
	return src
}

var relay struct {
	once sync.Once
	addr string
	err  error
}

// startRelay starts the relay once, on a free port of the loopback address.
func startRelay() (string, error) {
	relay.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			relay.err = err
			return
		}
		relay.addr = ln.Addr().String()
		srv := &http.Server{Handler: http.HandlerFunc(serveRelay), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("stream relay: %v", err)
			}
		}()
	})
	return relay.addr, relay.err
}

// serveRelay passes one request of ffmpeg (a GET, maybe of a byte range) on
// to the stream's link, or to what its HLS playlists name (/h/...).
func serveRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/h/"); ok {
		serveHLSRelay(w, r, rest)
		return
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/r/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	src, _, ok := remoteSourceOf(RemotePrefix + id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if src.handler != nil {
		src.handler.ServeHTTP(w, r)
		return
	}
	if src.hls {
		serveHLSPlaylist(w, r, id, src, src.url, src.pick)
		return
	}
	resp, err := fetchRemote(r.Context(), r.Method, src, r.Header.Get("Range"))
	if err != nil {
		// Only the host: the error names the link, which may carry an
		// addon's settings or keys.
		msg := providers.ErrorText(err)
		log.Printf("stream relay: %s: %s", hostOf(src.url), msg)
		http.Error(w, msg, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("stream relay: %s answered %s", hostOf(src.url), resp.Status)
	}
	// ffmpeg reads the links in playlists and manifests (HLS, DASH...) by
	// itself, not through the relay: they could name this computer or the
	// LAN. Only media files are passed on (an HLS link is registered as
	// one, and its playlists are served rewritten: relayhls.go).
	body := io.Reader(resp.Body)
	if resp.StatusCode < 300 {
		var head []byte
		if r.Method == http.MethodGet && fromStart(resp) {
			br := bufio.NewReader(resp.Body)
			head, _ = br.Peek(512)
			body = br
		}
		if isManifest(resp.Header.Get("Content-Type"), head) {
			log.Printf("stream relay: %s answered with a playlist, not a video file", hostOf(src.url))
			http.Error(w, "a playlist, not a video file", http.StatusUnsupportedMediaType)
			return
		}
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, body)
	}
}

// fromStart reports an answer that starts at the file's first byte.
func fromStart(resp *http.Response) bool {
	return resp.StatusCode == http.StatusOK ||
		resp.StatusCode == http.StatusPartialContent && strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-")
}

// isManifest reports a playlist or manifest (HLS, DASH, Smooth Streaming,
// IMF, ffconcat, SDP...) by its type or its first bytes (no media
// container starts with text like those).
func isManifest(ctype string, head []byte) bool {
	ct := strings.ToLower(ctype)
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "dash+xml") || strings.Contains(ct, "vnd.ms-sstr") || strings.Contains(ct, "sdp") {
		return true
	}
	head = bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")), " \t\r\n")
	for _, p := range []string{"#EXTM3U", "<", "ffconcat", "v=0"} {
		if len(head) >= len(p) && bytes.EqualFold(head[:len(p)], []byte(p)) {
			return true
		}
	}
	return false
}

// What a link answers with (LinkKind).
const (
	LinkFile     = "file"     // a video file, or anything else
	LinkHLS      = "hls"      // an HLS playlist
	LinkPage     = "page"     // a web page
	LinkManifest = "manifest" // another kind of playlist or manifest (DASH...)
)

// LinkKind tells what a link answered with, by its type and first bytes.
func LinkKind(ctype string, head []byte) string {
	ct := strings.ToLower(ctype)
	h := bytes.ToLower(bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")), " \t\r\n"))
	switch {
	case strings.Contains(ct, "mpegurl") || bytes.HasPrefix(h, []byte("#extm3u")):
		return LinkHLS
	case strings.Contains(ct, "text/html") || strings.Contains(ct, "xhtml") || bytes.HasPrefix(h, []byte("<!doctype html")) || bytes.HasPrefix(h, []byte("<html")):
		return LinkPage
	case isManifest(ctype, head):
		return LinkManifest
	}
	return LinkFile
}

// relayInputArgs are ffmpeg's and ffprobe's options for an input read
// from the relay: plain HTTP to it only, so nothing in the stream (an SDP
// description, a demuxer opening other resources) has them open files,
// HTTPS or RTP/UDP addresses themselves. An HLS link is read as HLS, with
// the crypto protocol when its segments are AES-128 encrypted (it only
// decrypts what comes from the relay, the keys included), and with any
// segment names (see HLSExtensionPicky).
func relayInputArgs(input, ffmpegPath string) []string {
	args := []string{"-protocol_whitelist", "http,tcp"}
	src := relayedSource(input)
	if src == nil || !src.hls {
		return args
	}
	remotes.Lock()
	crypto := src.crypto
	remotes.Unlock()
	if crypto {
		args[1] += ",crypto"
	}
	args = append(args, "-f", "hls")
	if HLSExtensionPicky(ffmpegPath) {
		args = append(args, "-extension_picky", "0")
	}
	return args
}

var picky sync.Map // ffmpeg's path -> whether it's picky

// HLSExtensionPicky reports whether ffmpeg's HLS demuxer refuses segments
// with unusual names unless told otherwise (ffmpeg 7.1 and later):
// providers name them anyhow (.jpg, .html...), and the relay's names have
// no extension at all. Older versions don't know the option.
func HLSExtensionPicky(ffmpegPath string) bool {
	if v, ok := picky.Load(ffmpegPath); ok {
		return v.(bool)
	}
	out, _ := exec.Command(ffmpegPath, "-hide_banner", "-h", "demuxer=hls").CombinedOutput()
	ok := strings.Contains(string(out), "extension_picky")
	picky.Store(ffmpegPath, ok)
	return ok
}

// hostOf is a link's host, for the logs (links often carry tokens).
func hostOf(link string) string {
	if u, err := url.Parse(CleanLink(link)); err == nil {
		return u.Host
	}
	return "?"
}

// Fetch requests a stream's link (GET, a byte range when rng is set) the
// way the stream proxy does: with its headers, and never from this
// computer or the LAN, redirects included, but at the address of a local
// addon (LocalHosts). Downloads fetch video files with it.
func Fetch(ctx context.Context, link string, headers map[string]string, rng string) (*http.Response, error) {
	return fetchRemote(ctx, http.MethodGet, &remoteSource{url: link, headers: headers}, rng)
}

// fetchRemote requests a stream's link the way the stream proxy does.
func fetchRemote(ctx context.Context, method string, src *remoteSource, rng string) (*http.Response, error) {
	if src.handler != nil {
		return fetchHandler(ctx, method, src.handler, rng)
	}
	link := CleanLink(src.url)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("not a web link")
	}
	local := allowedLocal(u)
	if strings.EqualFold(u.Hostname(), "localhost") && !local {
		return nil, errors.New("forbidden")
	}
	req, err := http.NewRequestWithContext(ctx, method, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	for k, v := range src.headers {
		req.Header.Set(k, v)
	}
	for k, v := range credentialsFor(u) {
		req.Header.Set(k, v)
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	client := proxyClient
	if local {
		client = localProxyClient
	}
	return client.Do(req)
}

// fetchHandler asks a stream served on this computer (RegisterRemoteHandler)
// for a byte range of it, as fetchRemote asks a link: a range only (the
// answer is held in memory).
func fetchHandler(ctx context.Context, method string, h http.Handler, rng string) (*http.Response, error) {
	if rng == "" && method != http.MethodHead {
		return nil, errors.New("a range of it only")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://relay/", nil)
	if err != nil {
		return nil, err
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// RemoteURL is the relay URL a "remote:" path is read from on this
// computer: mpv plays a torrent being played from it.
func RemoteURL(path string) (string, error) { return remoteInput(path) }
