package stream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HLS through the relay
//
// ffmpeg reads HLS streams through the relay as well (downloads, and the
// in-app player converting a stream the browser can't play). Given a
// playlist, ffmpeg would fetch every variant, rendition, segment, key and
// init section it names by itself, from wherever they are: this computer
// and the LAN included. So the relay fetches each playlist itself, with
// the guarded client, and hands ffmpeg a rewritten copy in which every
// link ffmpeg would open is one of the relay's own (/h/<name>). The relay
// fetches those in turn, with the guarded client and the stream's headers:
//
//   - playlists are rewritten the same way, and must be HLS playlists;
//   - segments and init sections must be media, not a playlist or a
//     manifest naming more links (DASH, ffconcat, SDP: isManifest);
//   - keys are passed on as the few bytes they are.
//
// The names are signed with a key of this process, so they can't be made
// up, and the same link always gets the same name (live playlists are
// read again every few seconds). The playlists are read the way ffmpeg
// reads them (lines end at "\n", "\r" or a NUL byte; tag attributes are
// KEY=value lists whose quoted values take "\" escapes), and only what's
// rewritten, or can't name a link, is passed on.
//
// Of a master playlist, ffmpeg only gets one variant (and the renditions
// it uses): it would otherwise read them all.

const (
	hlsPlaylist = "p" // a playlist, served rewritten
	hlsMedia    = "m" // a segment or an init section
	hlsKey      = "k" // an AES-128 (or SAMPLE-AES) key
)

// The variant of a master playlist ffmpeg gets.
const (
	// pickBest: the highest bandwidth (downloads).
	pickBest = "best"
	// pickPlayable: the highest bandwidth among the variants browsers play
	// as they are (H.264 and AAC or MP3, by their CODECS), else among all
	// (the in-app player: when one plays as it is, the stream needn't be
	// converted).
	pickPlayable = "playable"
)

// relayTimeout bounds the look over an HLS stream's playlists that
// remoteInput may start.
const relayTimeout = 30 * time.Second

// relayKey signs the relay's names for the links of HLS playlists.
var relayKey = func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}()

type hlsLink struct{ root, kind, url string }

// hlsLinks are the links named in the playlists the relay served, by
// their relay names. The oldest are forgotten past hlsLinksMax (a live
// stream names new segments forever).
var hlsLinks = struct {
	sync.Mutex
	m     map[string]hlsLink
	order []string
}{m: map[string]hlsLink{}}

const hlsLinksMax = 100_000

// hlsName is the relay's name for a link named in the playlists of the
// HLS stream root, as kind.
func hlsName(root, kind, link string) string {
	mac := hmac.New(sha256.New, relayKey)
	mac.Write([]byte(root + "\x00" + kind + "\x00" + link))
	name := hex.EncodeToString(mac.Sum(nil)[:16])
	hlsLinks.Lock()
	defer hlsLinks.Unlock()
	if _, ok := hlsLinks.m[name]; !ok {
		hlsLinks.m[name] = hlsLink{root: root, kind: kind, url: link}
		hlsLinks.order = append(hlsLinks.order, name)
		if n := len(hlsLinks.order) - hlsLinksMax; n > 0 {
			for _, old := range hlsLinks.order[:n] {
				delete(hlsLinks.m, old)
			}
			hlsLinks.order = append([]string(nil), hlsLinks.order[n:]...)
		}
	}
	return name
}

func hlsLinkOf(name string) (hlsLink, bool) {
	hlsLinks.Lock()
	defer hlsLinks.Unlock()
	l, ok := hlsLinks.m[name]
	return l, ok
}

// RelayHLS names an HLS link for ffmpeg to download: the relay URL to give
// it as its input, and the options it must read it with (they go before
// its -i). Of a master playlist, it gets the best variant.
func RelayHLS(ctx context.Context, link string, headers map[string]string, ffmpegPath string) (string, []string, error) {
	path := register(link, headers, true, pickBest)
	src, _, ok := remoteSourceOf(path)
	if !ok {
		return "", nil, ErrRemoteGone
	}
	if err := scanOnce(ctx, src); err != nil {
		return "", nil, err
	}
	input, err := remoteInput(path)
	if err != nil {
		return "", nil, err
	}
	return input, relayInputArgs(input, ffmpegPath), nil
}

// serveHLSRelay serves /h/<name>: a link named in one of the relay's
// playlists.
func serveHLSRelay(w http.ResponseWriter, r *http.Request, name string) {
	l, ok := hlsLinkOf(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	src, _, ok := remoteSourceOf(RemotePrefix + l.root)
	if !ok || !src.hls {
		http.NotFound(w, r)
		return
	}
	if l.kind == hlsPlaylist {
		serveHLSPlaylist(w, r, l.root, src, l.url, "")
		return
	}
	serveHLSPart(w, r, src, l)
}

// serveHLSPlaylist serves the playlist at link, of the HLS stream root,
// rewritten (only the variant pick picks, of a master playlist).
func serveHLSPlaylist(w http.ResponseWriter, r *http.Request, root string, src *remoteSource, link, pick string) {
	body, base, err := fetchPlaylist(r.Context(), src, link)
	var out []byte
	if err == nil {
		addr, _ := startRelay()
		out, _, err = rewriteHLS(body, base, pick, func(kind, target string) string {
			return "http://" + addr + "/h/" + hlsName(root, kind, target)
		})
	}
	if err != nil {
		status := http.StatusBadGateway
		var se *statusError
		switch {
		case errors.As(err, &se):
			status = se.status
		case errors.Is(err, errNotPlaylist):
			status = http.StatusUnsupportedMediaType
		}
		log.Printf("stream relay: %s: %v", hostOf(link), err)
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, _ = w.Write(out)
	}
}

// serveHLSPart passes a segment, init section or key on, with its byte
// range. Segments that aren't media are refused, wherever their range
// starts: ffmpeg works out what each segment holds by itself.
func serveHLSPart(w http.ResponseWriter, r *http.Request, src *remoteSource, l hlsLink) {
	rng := ""
	if l.kind == hlsMedia {
		rng = r.Header.Get("Range")
	}
	resp, err := fetchRemote(r.Context(), r.Method, &remoteSource{url: l.url, headers: src.headers}, rng)
	if err != nil {
		log.Printf("stream relay: %s: %v", hostOf(l.url), err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("stream relay: %s answered %s", hostOf(l.url), resp.Status)
		http.Error(w, "upstream: "+resp.Status, resp.StatusCode)
		return
	}
	body := io.Reader(resp.Body)
	if l.kind == hlsKey {
		body = io.LimitReader(resp.Body, 1024) // a key is 16 bytes
	} else if r.Method == http.MethodGet {
		br := bufio.NewReader(resp.Body)
		head, _ := br.Peek(512)
		if isManifest("", head) {
			log.Printf("stream relay: %s answered with a playlist, not a segment", hostOf(l.url))
			http.Error(w, "a playlist, not a segment", http.StatusUnsupportedMediaType)
			return
		}
		body = br
	}
	// No type: ffmpeg tells what a segment holds by its bytes.
	w.Header().Set("Content-Type", "application/octet-stream")
	if l.kind == hlsMedia {
		for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
			if v := resp.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, body)
	}
}

var errNotPlaylist = errors.New("not an HLS playlist")

// statusError is a host's answer that isn't the playlist asked for.
type statusError struct {
	status int
	text   string
}

func (e *statusError) Error() string {
	return "the provider's link answered " + e.text
}

// fetchPlaylist fetches an HLS playlist of the stream src, and the address
// it came from in the end (its links are relative to it).
func fetchPlaylist(ctx context.Context, src *remoteSource, link string) ([]byte, *url.URL, error) {
	resp, err := fetchRemote(ctx, http.MethodGet, &remoteSource{url: link, headers: src.headers}, "")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, nil, &statusError{status: resp.StatusCode, text: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, nil, err
	}
	return body, resp.Request.URL, nil
}

// scanOnce looks the playlists of an HLS stream over for AES-128 keys,
// the first time it's asked.
func scanOnce(ctx context.Context, src *remoteSource) error {
	remotes.Lock()
	done := src.scanned
	remotes.Unlock()
	if done {
		return nil
	}
	crypto, err := scanHLS(ctx, src)
	if err != nil {
		return err
	}
	remotes.Lock()
	src.scanned, src.crypto = true, crypto
	remotes.Unlock()
	return nil
}

// scanHLS reports whether the segments ffmpeg will read of an HLS stream
// are AES-128 encrypted: its playlist has keys, or for a master playlist,
// those of the variant and renditions it gets.
func scanHLS(ctx context.Context, src *remoteSource) (bool, error) {
	info, err := playlistInfo(ctx, src, src.url, src.pick)
	if err != nil {
		return false, err
	}
	crypto := info.aes
	for i, link := range info.playlists {
		if crypto || i >= 6 {
			break
		}
		if sub, err := playlistInfo(ctx, src, link, ""); err == nil {
			crypto = sub.aes
		}
	}
	return crypto, nil
}

func playlistInfo(ctx context.Context, src *remoteSource, link, pick string) (hlsInfo, error) {
	body, base, err := fetchPlaylist(ctx, src, link)
	if err != nil {
		return hlsInfo{}, err
	}
	_, info, err := rewriteHLS(body, base, pick, func(string, string) string { return "" })
	if err != nil {
		return hlsInfo{}, errors.New("the provider's link isn't an HLS playlist")
	}
	return info, nil
}

// hlsInfo is what a playlist says that matters before ffmpeg reads it.
type hlsInfo struct {
	playlists []string // the variants and renditions of a master playlist
	aes       bool     // AES-128 keys
}

// The tags ffmpeg (or a later version, or the HLS spec) takes a link from,
// in their URI attribute, and what it is.
var hlsURITags = map[string]string{
	"#EXT-X-KEY":                hlsKey,
	"#EXT-X-SESSION-KEY":        hlsKey,
	"#EXT-X-MEDIA":              hlsPlaylist,
	"#EXT-X-I-FRAME-STREAM-INF": hlsPlaylist,
	"#EXT-X-RENDITION-REPORT":   hlsPlaylist,
	"#EXT-X-MAP":                hlsMedia,
	"#EXT-X-PART":               hlsMedia,
	"#EXT-X-PRELOAD-HINT":       hlsMedia,
	"#EXT-X-SESSION-DATA":       hlsMedia,
}

var reAttrName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// rewriteHLS rewrites a playlist that came from base for ffmpeg: every
// link ffmpeg would open becomes relay(kind, link). Tag lines that take
// links are written anew from their attributes; other tags that mention a
// URI are left out, and so are links that aren't web links. Of a master
// playlist, only the variant pick picks is kept ("": all of them).
func rewriteHLS(body []byte, base *url.URL, pick string, relay func(kind, link string) string) ([]byte, hlsInfo, error) {
	var info hlsInfo
	text := bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
	if !bytes.HasPrefix(text, []byte("#EXTM3U")) {
		return nil, info, errNotPlaylist
	}
	lines := splitLines(text)
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\n\v\f\r")
	}
	skip, groups := pickVariant(lines, pick)

	var out strings.Builder
	out.WriteString("#EXTM3U\n")
	variant := false
	for i, line := range lines {
		if i == 0 || line == "" || skip[i] {
			continue // #EXTM3U is written above
		}
		if strings.HasPrefix(line, "#") {
			name, attrs, _ := strings.Cut(line, ":")
			kind, takesLink := hlsURITags[name]
			switch {
			case takesLink:
				if groups != nil && name == "#EXT-X-MEDIA" && !groups[attrValue(attrs, "GROUP-ID")] {
					continue // a rendition the variant kept doesn't use
				}
				out.WriteString(rewriteTag(name, attrs, kind, base, relay, &info) + "\n")
			case name == "#EXT-X-STREAM-INF":
				// The next link is a variant's playlist.
				variant = true
				out.WriteString(line + "\n")
			case name == "#EXTINF" || !strings.Contains(strings.ToUpper(line), "URI"):
				out.WriteString(line + "\n")
			}
			continue
		}
		kind := hlsMedia
		if variant {
			kind = hlsPlaylist
		}
		variant = false
		target, ok := resolveRef(base, line)
		if !ok {
			continue
		}
		if kind == hlsPlaylist {
			info.playlists = append(info.playlists, target)
		}
		out.WriteString(relay(kind, target) + "\n")
	}
	return []byte(out.String()), info, nil
}

// splitLines splits a playlist into lines as ffmpeg does: at "\n", "\r"
// and NUL bytes.
func splitLines(b []byte) []string {
	return strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' || r == '\r' || r == 0 })
}

// pickVariant picks the variant of a master playlist ffmpeg gets: the
// lines of the others to leave out, and the groups of renditions the one
// kept uses (nil: no variant left out).
func pickVariant(lines []string, pick string) (map[int]bool, map[string]bool) {
	if pick == "" {
		return nil, nil
	}
	type variant struct {
		tag, uri  int
		bandwidth int64
		playable  bool
		groups    []string
	}
	var vs []variant
	cur := -1
	for i, line := range lines {
		switch {
		case i == 0 || line == "":
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs := strings.TrimPrefix(line, "#EXT-X-STREAM-INF:")
			v := variant{tag: i, uri: -1}
			v.bandwidth, _ = strconv.ParseInt(attrValue(attrs, "BANDWIDTH"), 10, 64)
			v.playable = playableCodecs(attrValue(attrs, "CODECS"))
			for _, g := range []string{"AUDIO", "VIDEO", "SUBTITLES", "CLOSED-CAPTIONS"} {
				if id := attrValue(attrs, g); id != "" {
					v.groups = append(v.groups, id)
				}
			}
			cur = len(vs)
			vs = append(vs, v)
		case strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF"):
		case !strings.HasPrefix(line, "#") && cur >= 0:
			vs[cur].uri, cur = i, -1
		}
	}
	keep := -1
	for _, playable := range []bool{true, false} {
		if pick != pickPlayable && playable {
			continue
		}
		for j, v := range vs {
			if v.uri >= 0 && (v.playable || !playable) && (keep < 0 || v.bandwidth > vs[keep].bandwidth) {
				keep = j
			}
		}
		if keep >= 0 {
			break
		}
	}
	if keep < 0 {
		return nil, nil
	}
	skip := map[int]bool{}
	for j, v := range vs {
		if j != keep {
			skip[v.tag] = true
			if v.uri >= 0 {
				skip[v.uri] = true
			}
		}
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF") {
			skip[i] = true
		}
	}
	groups := map[string]bool{}
	for _, g := range vs[keep].groups {
		groups[g] = true
	}
	return skip, groups
}

// playableCodecs reports whether a variant's CODECS are ones every
// browser plays: H.264 video, AAC or MP3 audio.
func playableCodecs(codecs string) bool {
	if strings.TrimSpace(codecs) == "" {
		return false
	}
	for _, c := range strings.Split(strings.ToLower(codecs), ",") {
		c = strings.TrimSpace(c)
		switch {
		case strings.HasPrefix(c, "avc1."), strings.HasPrefix(c, "avc3."),
			c == "mp4a.40.2", c == "mp4a.40.5", c == "mp4a.40.29", c == "mp4a.40.34", c == "mp4a.69", c == "mp4a.6b":
		default:
			return false
		}
	}
	return true
}

// attrValue is the value of a tag's attribute key ("" when it has none).
func attrValue(attrs, key string) string {
	v := ""
	for _, a := range parseAttrs(attrs) {
		if strings.EqualFold(a.key, key) {
			v = a.value // the last one counts, as for ffmpeg
		}
	}
	return v
}

// rewriteTag writes a tag's attributes anew, its URI replaced by the
// relay's link for it (left out when it isn't a web link), every value
// quoted.
func rewriteTag(name, attrs, kind string, base *url.URL, relay func(kind, link string) string, info *hlsInfo) string {
	var b strings.Builder
	b.WriteString(name + ":")
	n := 0
	for _, a := range parseAttrs(attrs) {
		if !reAttrName.MatchString(a.key) {
			continue
		}
		v := a.value
		switch {
		case strings.EqualFold(a.key, "URI"):
			target, ok := resolveRef(base, v)
			if !ok {
				continue
			}
			if name == "#EXT-X-MEDIA" {
				info.playlists = append(info.playlists, target)
			}
			v = relay(kind, target)
		case strings.EqualFold(a.key, "METHOD") && strings.EqualFold(v, "AES-128") && kind == hlsKey:
			info.aes = true
		}
		if n > 0 {
			b.WriteByte(',')
		}
		n++
		b.WriteString(a.key + `="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`)
	}
	return b.String()
}

type hlsAttr struct{ key, value string }

// parseAttrs reads a tag's attributes the way ffmpeg does
// (ff_parse_key_value): pairs separated by commas or spaces, a value
// either quoted, "\" escaping the next character, or running to the next
// comma or space.
func parseAttrs(s string) []hlsAttr {
	var out []hlsAttr
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' }
	i := 0
	for {
		for i < len(s) && (space(s[i]) || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			break
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := s[i : i+eq]
		i += eq + 1
		var v strings.Builder
		if i < len(s) && s[i] == '"' {
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					if i+1 >= len(s) {
						break
					}
					v.WriteByte(s[i+1])
					i += 2
					continue
				}
				v.WriteByte(s[i])
				i++
			}
			if i < len(s) && s[i] == '"' {
				i++
			}
		} else {
			for i < len(s) && !space(s[i]) && s[i] != ',' {
				v.WriteByte(s[i])
				i++
			}
		}
		out = append(out, hlsAttr{key, v.String()})
	}
	return out
}

// resolveRef is the web link a playlist's reference stands for.
func resolveRef(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	u, err := url.Parse(CleanLink(ref))
	if err != nil {
		return "", false
	}
	u = base.ResolveReference(u)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}
