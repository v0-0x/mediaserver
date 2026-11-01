// Package cinejoy is the Cinejoy provider of luffy, the command-line app
// that streams movies and shows (github.com/DemonKingSwarn/luffy), ported:
// movies and episodes are looked up by their TMDB id on Cinejoy's resolver,
// which answers HLS playlists (and MP4 files) from several servers.
//
// The resolver only takes requests sealed by Cinejoy's own encoder
// (encoder.go). KumoBox downloads it from the resolver, checks it's the
// very build pinned in EncoderSHA256 and runs it in wazero, where it can't
// reach anything but its own memory.
//
// Like luffy, KumoBox asks the servers proven to work, all at once, and
// only offers a server's stream once its playlist and first segment load.
package cinejoy

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

const (
	ID   = "cinejoy"
	Name = "Cinejoy"

	// DefaultAPIURL is the resolver; DefaultSiteURL the site, which its
	// CDNs want as Origin and Referer.
	DefaultAPIURL  = "https://api.shegu.st"
	DefaultSiteURL = "https://cinejoy.to"

	httpTimeout   = 15 * time.Second
	serverTimeout = 25 * time.Second
	// grace is how long the other servers are waited for once one has
	// answered with a stream that plays.
	grace      = 3 * time.Second
	catalogTTL = 5 * time.Minute

	apiLimit      = 4 << 20
	playlistLimit = 4 << 20
	// segmentProbe is how much of a stream's first segment (or file) is
	// read to see that it's video.
	segmentProbe = 64 << 10
	// maxNested bounds the embeds asked about when a server answers with
	// embeds rather than streams.
	maxNested = 2
)

// VerifiedServers are the resolver's servers known to give streams that
// play (as in luffy): the others it lists stay unused until checked.
var VerifiedServers = []string{"Lisbon", "Nebula", "Solara"}

// Provider is the Cinejoy provider. The zero value works.
type Provider struct {
	// APIURL is the resolver's address (DefaultAPIURL when empty), SiteURL
	// the site's (DefaultSiteURL when empty).
	APIURL  string
	SiteURL string
	// HTTP is the client used (util.HTTP, which can't reach the LAN, when
	// nil).
	HTTP *http.Client
	// EncoderSHA256 is the encoder build accepted (EncoderSHA256 when
	// empty; tests run their own).
	EncoderSHA256 string
	// Grace is how long the other servers are waited for once one answered
	// (3 s when 0).
	Grace time.Duration
}

// New returns the Cinejoy provider; apiURL replaces the resolver's address
// when not empty.
func New(apiURL string) *Provider { return &Provider{APIURL: apiURL} }

func (p *Provider) ID() string                     { return ID }
func (p *Provider) Name() string                   { return Name }
func (p *Provider) Kind() string                   { return providers.KindBuiltin }
func (p *Provider) Supports(mediaType string) bool { return true }

// Streams asks the resolver for the movie or episode, on every verified
// server at once.
func (p *Provider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if r.TMDBID <= 0 {
		return nil, providers.Skip(errors.New("Cinejoy needs the title's TMDB id"))
	}
	enc, err := p.encoder(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := p.servers(ctx)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, errors.New("none of its servers is up")
	}
	kind, params := request(r)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		i       int
		streams []providers.Stream
		err     error
	}
	results := make(chan result, len(servers))
	for i, name := range servers {
		go func() {
			sctx, scancel := context.WithTimeout(ctx, serverTimeout)
			defer scancel()
			streams, err := p.server(sctx, enc, name, kind, params)
			results <- result{i, streams, err}
		}()
	}
	found := make([][]providers.Stream, len(servers))
	errs := make([]error, len(servers))
	var graceC <-chan time.Time
wait:
	for pending := len(servers); pending > 0; {
		select {
		case res := <-results:
			pending--
			found[res.i], errs[res.i] = res.streams, res.err
			if res.err == nil && len(res.streams) > 0 && graceC == nil {
				t := time.NewTimer(p.grace())
				defer t.Stop()
				graceC = t.C
			}
		case <-graceC:
			break wait
		case <-ctx.Done():
			break wait
		}
	}
	var out []providers.Stream
	for _, s := range found {
		out = append(out, s...)
	}
	if len(out) > 0 {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var msgs []string
	for i, err := range errs {
		if err != nil && !errors.Is(err, errNotFound) {
			msgs = append(msgs, servers[i]+": "+err.Error())
		}
	}
	if len(msgs) == 0 {
		return nil, nil // not on Cinejoy
	}
	return nil, fmt.Errorf("no stream that plays (%s)", strings.Join(msgs, "; "))
}

// errNotFound is a server not having the title.
var errNotFound = errors.New("not found")

// request is the resolver request for r: the kind of title and what
// identifies it.
func request(r providers.Request) (kind string, params map[string]any) {
	params = map[string]any{"tmdb": strconv.Itoa(r.TMDBID)}
	kind = "movie"
	if r.IsTV() {
		kind = "series"
		params["season"] = strconv.Itoa(r.Season)
		params["episode"] = strconv.Itoa(r.Episode)
	}
	if t := strings.TrimSpace(r.Title); t != "" {
		params["title"] = t
	}
	if r.Year > 0 {
		params["year"] = strconv.Itoa(r.Year)
	}
	return kind, params
}

// server finds the stream of one server: the first of its answers that
// plays.
func (p *Provider) server(ctx context.Context, enc *encoder, name, kind string, params map[string]any) ([]providers.Stream, error) {
	path := "/" + name + "/" + kind
	data, err := p.resolve(ctx, enc, path, params)
	if err != nil {
		return nil, err
	}
	entries := parseEntries(data)
	var cands []candidate
	for _, e := range entries {
		if c, ok := e.candidate(); ok {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		// Embeds rather than streams: each is asked for in turn.
		for _, e := range entries {
			embed, ok := e.embed()
			if !ok || e.Playlist == "" {
				continue
			}
			nested := make(map[string]any, len(params)+1)
			for k, v := range params {
				nested[k] = v
			}
			nested["embed"] = embed
			if d, err := p.resolve(ctx, enc, path, nested); err == nil {
				for _, ne := range parseEntries(d) {
					if c, ok := ne.candidate(); ok {
						cands = append(cands, c)
					}
				}
			}
			if len(cands) >= maxNested {
				break
			}
		}
	}
	if len(cands) == 0 {
		return nil, errNotFound
	}
	var firstErr error
	for i, c := range cands {
		if i >= maxNested {
			break
		}
		streams, err := p.check(ctx, name, c)
		if err == nil {
			return streams, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// candidate is a server's answer: an HLS playlist, or MP4 files in several
// qualities (best first).
type candidate struct {
	playlist  string
	files     []file
	subtitles []providers.Subtitle
}

type file struct {
	quality string
	url     string
}

// check loads c's playlist and first segment (or the best file's first
// bytes) and returns its streams when it's video.
func (p *Provider) check(ctx context.Context, server string, c candidate) ([]providers.Stream, error) {
	title := Name + " · " + server
	if c.playlist != "" {
		quality, err := p.probeHLS(ctx, c.playlist)
		if err != nil {
			return nil, err
		}
		return []providers.Stream{{
			Title: title, URL: c.playlist, Type: providers.StreamM3U8, Quality: quality,
			Headers: p.headers(), Subtitles: c.subtitles,
		}}, nil
	}
	if err := p.probeVideo(ctx, c.files[0].url, true); err != nil {
		return nil, err
	}
	var out []providers.Stream
	for _, f := range c.files {
		t := title
		if f.quality != "" {
			t += " · " + f.quality
		}
		out = append(out, providers.Stream{
			Title: t, URL: f.url, Type: providers.StreamMP4, Quality: f.quality,
			Headers: p.headers(), Subtitles: c.subtitles,
		})
	}
	return out, nil
}

// headers are the headers Cinejoy's CDNs want.
func (p *Provider) headers() map[string]string {
	site := p.site()
	return map[string]string{"Origin": site, "Referer": site + "/"}
}

// resolve sends a sealed request for path and returns the answer's data.
func (p *Provider) resolve(ctx context.Context, enc *encoder, path string, params map[string]any) (json.RawMessage, error) {
	s, err := enc.seal(ctx, path, params)
	if err != nil {
		return nil, err
	}
	wire, err := p.fetch(ctx, http.MethodPost, p.api()+"/g", s.body, apiLimit, false)
	if err != nil {
		return nil, err
	}
	env, err := decrypt(s, wire)
	if err != nil {
		return nil, err
	}
	switch {
	case env.Status == http.StatusNotFound:
		return nil, errNotFound
	case env.Status < 200 || env.Status >= 300:
		return nil, fmt.Errorf("resolver status %d", env.Status)
	}
	return env.Data, nil
}

// envelope is the resolver's decrypted answer.
type envelope struct {
	Status int             `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// decrypt opens the resolver's answer to s: AES-256-GCM with the key s
// came with, the IV first.
func decrypt(s *sealed, wire []byte) (envelope, error) {
	var env envelope
	if len(wire) < 12+16 {
		return env, errors.New("resolver answer too short")
	}
	block, err := aes.NewCipher(s.responseKey)
	if err != nil {
		return env, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return env, err
	}
	plain, err := gcm.Open(nil, wire[:12], wire[12:], s.aad())
	if err != nil {
		return env, errors.New("resolver answer doesn't authenticate")
	}
	if err := json.Unmarshal(plain, &env); err != nil {
		return env, errors.New("resolver answer isn't JSON")
	}
	return env, nil
}

// entry is a stream of an answer.
type entry struct {
	ID        json.RawMessage         `json:"id"`
	Type      string                  `json:"type"`
	Playlist  string                  `json:"playlist"`
	Qualities map[string]entryQuality `json:"qualities"`
	Captions  []json.RawMessage       `json:"captions"`
}

type entryQuality struct {
	URL string `json:"url"`
}

func parseEntries(data json.RawMessage) []entry {
	var v struct {
		Stream []entry `json:"stream"`
	}
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	return v.Stream
}

// candidate is what e plays: an https playlist, or files.
func (e entry) candidate() (candidate, bool) {
	c := candidate{subtitles: captions(e.Captions)}
	if strings.HasPrefix(e.Playlist, "https://") {
		c.playlist = e.Playlist
		return c, true
	}
	if e.Type != "file" {
		return c, false
	}
	type q struct {
		height int
		file
	}
	var qs []q
	for k, v := range e.Qualities {
		if !strings.HasPrefix(v.URL, "https://") {
			continue
		}
		h, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(k), "p"))
		qs = append(qs, q{h, file{quality: providers.QualityFromHeight(h), url: v.URL}})
	}
	sort.Slice(qs, func(i, j int) bool {
		if qs[i].height != qs[j].height {
			return qs[i].height > qs[j].height
		}
		return qs[i].url < qs[j].url
	})
	for _, x := range qs {
		c.files = append(c.files, x.file)
	}
	return c, len(c.files) > 0
}

// embed is e's id, when it's one a nested request can be made with.
func (e entry) embed() (any, bool) {
	var v any
	if len(e.ID) == 0 || json.Unmarshal(e.ID, &v) != nil {
		return nil, false
	}
	switch v.(type) {
	case string, float64:
		return v, true
	}
	return nil, false
}

// captions reads an answer's subtitles ({"url", "language"...}).
func captions(raw []json.RawMessage) []providers.Subtitle {
	var out []providers.Subtitle
	for _, r := range raw {
		var c struct {
			URL      string `json:"url"`
			File     string `json:"file"`
			Language string `json:"language"`
			Lang     string `json:"lang"`
			Label    string `json:"label"`
		}
		if json.Unmarshal(r, &c) != nil {
			continue
		}
		u := util.FirstNonEmpty(c.URL, c.File)
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			continue
		}
		out = append(out, providers.Subtitle{URL: u, Language: util.FirstNonEmpty(c.Language, c.Lang, c.Label), Label: c.Label})
	}
	return out
}

// probeHLS follows the playlist at raw to its first segment and checks
// it's video. It returns the best quality the playlist offers ("" when it
// doesn't say).
func (p *Provider) probeHLS(ctx context.Context, raw string) (string, error) {
	quality := ""
	current := raw
	for depth := 0; depth < 3; depth++ {
		body, err := p.fetch(ctx, http.MethodGet, current, nil, playlistLimit, true)
		if err != nil {
			return "", err
		}
		text := strings.TrimPrefix(strings.TrimSpace(string(body)), "\ufeff")
		if !strings.HasPrefix(text, "#EXTM3U") {
			return "", errors.New("not an HLS playlist")
		}
		first := ""
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				first = line
				break
			}
		}
		if first == "" {
			return "", errors.New("empty playlist")
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", err
		}
		ref, err := url.Parse(first)
		if err != nil {
			return "", err
		}
		next := base.ResolveReference(ref).String()
		if !strings.Contains(text, "#EXT-X-STREAM-INF") {
			return quality, p.probeVideo(ctx, next, false)
		}
		if quality == "" {
			quality = bestQuality(text)
		}
		current = next
	}
	return "", errors.New("playlists nested too deep")
}

// bestQuality is the best quality a master playlist offers.
func bestQuality(master string) string {
	best := 0
	for _, line := range strings.Split(master, "\n") {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		i := strings.Index(line, "RESOLUTION=")
		if i < 0 {
			continue
		}
		res := line[i+len("RESOLUTION="):]
		if j := strings.IndexByte(res, ','); j >= 0 {
			res = res[:j]
		}
		if _, h, ok := strings.Cut(res, "x"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n > best {
				best = n
			}
		}
	}
	return providers.QualityFromHeight(best)
}

// probeVideo reads the first bytes at raw and checks they're not a web
// page or an error. ranged asks for those bytes only (files).
func (p *Provider) probeVideo(ctx context.Context, raw string, ranged bool) error {
	b, err := p.get(ctx, raw, segmentProbe, ranged)
	if err != nil {
		return err
	}
	if len(b) < 188 { // one MPEG-TS packet
		return errors.New("no video at the stream's link")
	}
	head := strings.ToLower(strings.TrimSpace(string(b[:min(len(b), 100)])))
	for _, prefix := range []string{"<!doctype", "<html", "{", "["} {
		if strings.HasPrefix(head, prefix) {
			return errors.New("no video at the stream's link")
		}
	}
	return nil
}

func (p *Provider) get(ctx context.Context, raw string, limit int64, ranged bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := p.request(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	if ranged {
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", limit-1))
	}
	return p.do(req, limit, true)
}

// fetch makes a request to the resolver or a CDN. Only the CDNs may
// redirect.
func (p *Provider) fetch(ctx context.Context, method, raw string, body []byte, limit int64, redirects bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := p.request(ctx, method, raw, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	return p.do(req, limit, redirects)
}

func (p *Provider) request(ctx context.Context, method, raw string, body []byte) (*http.Request, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("not a web link")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	for k, v := range p.headers() {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (p *Provider) do(req *http.Request, limit int64, redirects bool) ([]byte, error) {
	c := *p.client()
	if !redirects {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// encoder returns the compiled encoder, downloading it the first time.
func (p *Provider) encoder(ctx context.Context) (*encoder, error) {
	sum := p.EncoderSHA256
	if sum == "" {
		sum = EncoderSHA256
	}
	if e := cachedEncoder(sum); e != nil {
		return e, nil
	}
	wasm, err := p.fetch(ctx, http.MethodGet, p.api()+"/crush.wasm", nil, encoderMaxSize, false)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	return loadEncoder(ctx, wasm, sum)
}

// Server lists by resolver address, kept catalogTTL.
var (
	catalogsMu sync.Mutex
	catalogs   = map[string]catalog{}
)

type catalog struct {
	servers []string
	at      time.Time
}

// servers are the verified servers that are up, in VerifiedServers' order.
func (p *Provider) servers(ctx context.Context) ([]string, error) {
	api := p.api()
	catalogsMu.Lock()
	c, ok := catalogs[api]
	catalogsMu.Unlock()
	if ok && time.Since(c.at) < catalogTTL {
		return c.servers, nil
	}
	body, err := p.fetch(ctx, http.MethodGet, api+"/servers", nil, 1<<20, false)
	if err != nil {
		return nil, fmt.Errorf("servers: %w", err)
	}
	var v struct {
		Servers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, errors.New("servers: not JSON")
	}
	up := map[string]bool{}
	for _, s := range v.Servers {
		if strings.EqualFold(s.Status, "ok") {
			up[s.Name] = true
		}
	}
	var list []string
	for _, name := range VerifiedServers {
		if up[name] {
			list = append(list, name)
		}
	}
	catalogsMu.Lock()
	catalogs[api] = catalog{servers: list, at: time.Now()}
	catalogsMu.Unlock()
	return list, nil
}

// api is the resolver's address: a host alone gets https.
func (p *Provider) api() string {
	a := strings.TrimSpace(p.APIURL)
	if a == "" {
		return DefaultAPIURL
	}
	if !strings.Contains(a, "://") {
		a = "https://" + a
	}
	return strings.TrimRight(a, "/")
}

func (p *Provider) site() string {
	if p.SiteURL == "" {
		return DefaultSiteURL
	}
	return strings.TrimRight(p.SiteURL, "/")
}

func (p *Provider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return util.HTTP
}

func (p *Provider) grace() time.Duration {
	if p.Grace > 0 {
		return p.Grace
	}
	return grace
}
