// Package mediaserver streams from the user's own media servers: Jellyfin,
// Emby (which shares most of Jellyfin's API) and Plex. A movie or an
// episode is found by its TMDB id (its IMDb id when the server has no TMDB
// id for it), and each of its files becomes a direct link to the file as it
// is (no transcoding on the server): KumoBox's in-app player converts what
// the browser can't play, mpv plays everything.
//
// The server's token never goes in the links: the players and the browser
// get links without it, and KumoBox adds it (Credentials) when it fetches a
// video from the server itself (stream proxy, relay, mpv, downloads), for
// the servers' video paths only.
package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Server kinds.
const (
	Jellyfin = "jellyfin"
	Emby     = "emby"
	Plex     = "plex"
)

// Config is a media server as Settings › Providers has it.
type Config struct {
	Kind  string
	Name  string
	URL   string
	Token string
	// UserID is the Jellyfin/Emby user whose libraries are searched ("":
	// the token's own, every library for an API key).
	UserID string
}

// base is the server's address without a trailing slash, nil when it isn't
// a web address.
func (c Config) base() (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.URL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q is not a server address (http://host:port)", c.URL)
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// ID is the provider id of the server: its kind and address
// ("jellyfin:192.168.1.10:8096").
func (c Config) ID() string {
	u, err := c.base()
	if err != nil {
		return c.Kind + ":" + c.URL
	}
	return c.Kind + ":" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
}

// DisplayName is the server's name, or what it is and where.
func (c Config) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	kind := map[string]string{Jellyfin: "Jellyfin", Emby: "Emby", Plex: "Plex"}[c.Kind]
	if u, err := c.base(); err == nil {
		return kind + " (" + strings.ToLower(u.Hostname()) + ")"
	}
	return kind
}

// Provider is a media server seen as a streaming provider.
type Provider struct {
	cfg  Config
	http *http.Client
}

// New returns the provider of a server.
func New(c Config) (*Provider, error) {
	switch c.Kind {
	case Jellyfin, Emby, Plex:
	default:
		return nil, fmt.Errorf("unknown media server kind %q", c.Kind)
	}
	if _, err := c.base(); err != nil {
		return nil, err
	}
	return &Provider{cfg: c, http: httpClient()}, nil
}

func (p *Provider) ID() string   { return p.cfg.ID() }
func (p *Provider) Name() string { return p.cfg.DisplayName() }
func (p *Provider) Kind() string { return providers.KindMediaServer }
func (p *Provider) Supports(mediaType string) bool {
	return mediaType == providers.TypeMovie || mediaType == providers.TypeTV
}

// Streams finds the movie or episode on the server and returns its files.
func (p *Provider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if r.TMDBID <= 0 && r.IMDbID == "" {
		return nil, providers.Skip(errors.New("no TMDB or IMDb id to look the title up with"))
	}
	if p.cfg.Kind == Plex {
		return p.plexStreams(ctx, r)
	}
	return p.jellyfinStreams(ctx, r)
}

// Info is what the Test button says about a server.
type Info struct {
	// Name and Version are the server's.
	Name    string `json:"name"`
	Version string `json:"version"`
	// UserID and UserName are the Jellyfin/Emby user whose libraries are
	// searched (the one set, else the token's own; "" for an API key,
	// which sees every library).
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
	// Libraries counts the server's movie and show libraries (Plex).
	Libraries int `json:"libraries,omitempty"`
}

// Check connects to the server with its token: the Test button.
func Check(ctx context.Context, c Config) (Info, error) {
	p, err := New(c)
	if err != nil {
		return Info{}, err
	}
	if c.Token == "" {
		return Info{}, errors.New("the server's token is missing")
	}
	if c.Kind == Plex {
		return p.plexCheck(ctx)
	}
	return p.jellyfinCheck(ctx)
}

// ---------------------------------------------------------------------------
// HTTP

const (
	requestTimeout = 30 * time.Second
	// listLimit bounds a library listing (thousands of titles), itemLimit
	// the other answers.
	listLimit = 64 << 20
	itemLimit = 8 << 20
)

// httpClient reaches the server wherever the user runs it (this computer,
// the LAN or the internet: it's their own, set up on purpose), and follows
// redirects only on the server's own address.
var httpClient = sync.OnceValue(func() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: requestTimeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many redirects")
			}
			if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return errors.New("the server redirected to another address")
			}
			return nil
		},
	}
})

// link is the server's address followed by p (a path, maybe with a query).
func (p *Provider) link(rel string, q url.Values) string {
	u, _ := p.cfg.base()
	ref, err := url.Parse(rel)
	if err != nil {
		ref = &url.URL{Path: rel}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(ref.Path, "/")
	u.RawPath = ""
	if q != nil {
		u.RawQuery = q.Encode()
	} else {
		u.RawQuery = ref.RawQuery
	}
	return u.String()
}

// get fetches a path of the server's API (with its token) into out.
func (p *Provider) get(ctx context.Context, rel string, q url.Values, limit int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.link(rel, q), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range authHeaders(p.cfg) {
		req.Header.Set(k, v)
	}
	if p.cfg.Kind == Plex {
		req.Header.Set("X-Plex-Product", "KumoBox")
		req.Header.Set("X-Plex-Client-Identifier", "kumobox")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		// The error names the link: its host is enough (no token is in it,
		// but the path says what was asked).
		return errors.New(providers.ErrorText(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the server refused the token (%s)", resp.Status)
	case resp.StatusCode >= 400:
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("the server's answer is too large (over %d MB)", limit>>20)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("the server's answer is not valid JSON: %w", err)
	}
	return nil
}

// authHeaders are the headers carrying a server's token, as each kind
// expects it.
func authHeaders(c Config) map[string]string {
	if c.Token == "" {
		return nil
	}
	switch c.Kind {
	case Jellyfin:
		return map[string]string{"Authorization": `MediaBrowser Token="` + c.Token + `"`}
	case Emby:
		return map[string]string{"X-Emby-Token": c.Token}
	case Plex:
		return map[string]string{"X-Plex-Token": c.Token}
	}
	return nil
}

// fileName is the last element of a file's path on the server (Windows
// servers too).
func fileName(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" {
		return ""
	}
	return path.Base(p)
}
