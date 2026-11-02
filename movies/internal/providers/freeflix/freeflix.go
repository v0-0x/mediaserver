// Package freeflix holds the movie and show resolvers of FreeFlix, the
// command-line app that streams movies, shows and anime
// (github.com/freedy237/freeflix-cli; its "GoldenMS" source), ported:
// Vidlink, Hexa, Xpass, Mapple and Videasy. Each looks a movie or an
// episode up by its TMDB id (Videasy by its title as well), and each is a
// provider of its own, turned on, made the default and tracked apart.
//
// Vidlink, Hexa and Videasy encrypt requests or answers with keys their
// web players work out: like FreeFlix, Moviko has enc-dec.app's API
// (DecryptURL) do it. Xpass and Mapple need no one else.
package freeflix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// DefaultDecryptURL is enc-dec.app's API.
const DefaultDecryptURL = "https://enc-dec.app/api"

const (
	requestTimeout = 15 * time.Second
	maxResponse    = 4 << 20
	// parallel bounds the requests a provider makes at once.
	parallel = 4
)

// Site is one of FreeFlix's resolvers.
type Site struct {
	ID   string
	Name string
	// DefaultURL is the site's address.
	DefaultURL string
	// Decrypts is set on the sites that go through enc-dec.app.
	Decrypts bool

	streams func(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error)
}

// Sites are the resolvers, in FreeFlix's order.
var Sites = []*Site{vidlinkSite, hexaSite, xpassSite, mappleSite, videasySite}

// SiteByID returns the resolver id names, nil when there's none.
func SiteByID(id string) *Site {
	for _, s := range Sites {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Provider streams from one of the resolvers.
type Provider struct {
	Site *Site
	// BaseURL is the site's address (Site.DefaultURL when empty): sites
	// move to mirrors from time to time.
	BaseURL string
	// DecryptURL is enc-dec.app's API (DefaultDecryptURL when empty).
	DecryptURL string
	// HTTP is the client used (one with Chrome's TLS fingerprint, which
	// can't reach the LAN, when nil).
	HTTP *http.Client
}

// New returns the provider of site; baseURL replaces its address when not
// empty.
func New(site *Site, baseURL string) *Provider { return &Provider{Site: site, BaseURL: baseURL} }

func (p *Provider) ID() string                     { return p.Site.ID }
func (p *Provider) Name() string                   { return p.Site.Name }
func (p *Provider) Kind() string                   { return providers.KindBuiltin }
func (p *Provider) Supports(mediaType string) bool { return true }

// Streams asks the site for the movie or episode.
func (p *Provider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if r.TMDBID <= 0 {
		return nil, providers.Skip(fmt.Errorf("%s needs the title's TMDB id", p.Site.Name))
	}
	return p.Site.streams(p, ctx, r)
}

// base is the site's address: a host alone gets https.
func (p *Provider) base() string {
	return baseURL(p.BaseURL, p.Site.DefaultURL)
}

func (p *Provider) decryptBase() string {
	return baseURL(p.DecryptURL, DefaultDecryptURL)
}

func baseURL(configured, def string) string {
	b := strings.TrimSpace(configured)
	if b == "" {
		return def
	}
	if !strings.Contains(b, "://") {
		b = "https://" + b
	}
	return strings.TrimRight(b, "/")
}

// chrome is a client with Chrome's TLS fingerprint, as FreeFlix's
// curl_cffi sessions: the sites sit behind Cloudflare, which turns Go's
// own away. Like util.HTTP, it can't reach this computer or the LAN.
var chrome = sync.OnceValue(func() *http.Client {
	c := req.C().ImpersonateChrome().SetTimeout(30 * time.Second)
	c.SetDial(util.PublicDialContext(15 * time.Second))
	return c.GetClient()
})

func (p *Provider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return chrome()
}

// fetch makes a request and returns the answer's body. body, when not nil,
// is sent as JSON.
func (p *Provider) fetch(ctx context.Context, method, url string, headers map[string]string, body any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxResponse {
		return nil, errors.New("answer too large")
	}
	return b, nil
}

func (p *Provider) fetchJSON(ctx context.Context, method, url string, headers map[string]string, body, out any) error {
	b, err := p.fetch(ctx, method, url, headers, body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("answer isn't JSON")
	}
	return nil
}

// decrypt has enc-dec.app's endpoint (enc-vidlink, dec-hexa...) work out
// something: a GET with query, or a POST of body. It returns the result.
func (p *Provider) decrypt(ctx context.Context, endpoint, query string, body any, out any) error {
	u := p.decryptBase() + "/" + endpoint
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	} else if query != "" {
		u += "?" + query
	}
	var v struct {
		Result json.RawMessage `json:"result"`
	}
	if err := p.fetchJSON(ctx, method, u, nil, body, &v); err != nil {
		return fmt.Errorf("enc-dec.app: %w", err)
	}
	if len(v.Result) == 0 || string(v.Result) == "null" || json.Unmarshal(v.Result, out) != nil {
		return errors.New("enc-dec.app: no result")
	}
	return nil
}

// streamType guesses a link's type: HLS when it says so, else what the
// file name says.
func streamType(u, declared string) string {
	switch strings.ToLower(declared) {
	case "hls", "m3u8":
		return providers.StreamM3U8
	case "mp4":
		return providers.StreamMP4
	}
	if strings.Contains(strings.ToLower(u), ".m3u8") {
		return providers.StreamM3U8
	}
	return providers.GuessType(u)
}

// subtitle reads a subtitle ({"url", "language"|"lang"|"label"}).
type subtitle struct {
	URL      string `json:"url"`
	File     string `json:"file"`
	Language string `json:"language"`
	Lang     string `json:"lang"`
	Label    string `json:"label"`
}

func subtitles(list []subtitle) []providers.Subtitle {
	var out []providers.Subtitle
	for _, s := range list {
		u := util.FirstNonEmpty(s.URL, s.File)
		if !isWebLink(u) {
			continue
		}
		out = append(out, providers.Subtitle{URL: u, Language: util.FirstNonEmpty(s.Language, s.Lang, s.Label), Label: s.Label})
	}
	return out
}

func isWebLink(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// absolute makes a link the site gave relative to base absolute.
func absolute(base, link string) string {
	if isWebLink(link) {
		return link
	}
	if strings.HasPrefix(link, "//") {
		return "https:" + link
	}
	return base + "/" + strings.TrimLeft(link, "/")
}

// each runs fn on every item, parallel at a time, and returns what they
// found in the items' order. When nothing is found, the first error is
// returned (nil when every item just had nothing).
func each[T any](ctx context.Context, items []T, fn func(context.Context, T) ([]providers.Stream, error)) ([]providers.Stream, error) {
	found := make([][]providers.Stream, len(items))
	errs := make([]error, len(items))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			found[i], errs[i] = fn(ctx, it)
		}()
	}
	wg.Wait()
	var out []providers.Stream
	for _, s := range found {
		out = append(out, s...)
	}
	if len(out) > 0 {
		return out, nil
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// kind is the request's media type as the sites name it.
func kind(r providers.Request) string {
	if r.IsTV() {
		return "tv"
	}
	return "movie"
}
