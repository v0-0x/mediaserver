package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

const (
	maxManifestSize   = 2 << 20
	maxResponseSize   = 8 << 20
	maxCollectionSize = 8 << 20
	requestTimeout    = 30 * time.Second
)

// Errors of the client and the store.
var (
	ErrInvalidURL = errors.New("an addon URL starts with https:// (or stremio://) and usually ends with /manifest.json")
	// ErrLocalAddon is returned for addons on this computer or the LAN when
	// the user didn't explicitly ask to install one.
	ErrLocalAddon = errors.New("this addon runs on this computer or your local network: confirm to install it anyway")
)

// Addon is an addon: where it is (its transport URL, ".../manifest.json")
// and its manifest; in a collection, its flags; once installed, its state.
type Addon struct {
	TransportURL  string   `json:"transportUrl"`
	TransportName string   `json:"transportName,omitempty"`
	Manifest      Manifest `json:"manifest"`
	Flags         Flags    `json:"flags"`

	// Installed addons only.
	Installed   bool  `json:"installed"`
	Enabled     bool  `json:"enabled"`
	Position    int   `json:"position"`
	InstalledAt int64 `json:"installedAt,omitempty"`

	// Local addons run on this computer or the LAN (Moviko only talks to
	// them once the user installed them on purpose).
	Local bool `json:"local"`
	// Supported addons serve streams or subtitles of movies or series.
	Supported bool `json:"supported"`

	// raw is the manifest as the addon sent it (stored as is).
	raw json.RawMessage
}

// Flags are a collection's marks on an addon.
type Flags struct {
	Official  bool `json:"official,omitempty"`
	Protected bool `json:"protected,omitempty"`
}

// BaseURL is the addon's base URL: resources are under it.
func (a Addon) BaseURL() string { return strings.TrimSuffix(a.TransportURL, "/manifest.json") }

// NormalizeURL turns what the user pasted (a manifest URL, an addon's base
// URL, a stremio:// link) into the addon's transport URL
// (https://host/[config/]manifest.json).
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if len(s) >= 10 && strings.EqualFold(s[:10], "stremio://") {
		s = "https://" + s[10:]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/manifest.json")
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ErrInvalidURL
	}
	return scheme + s[len(u.Scheme):] + "/manifest.json", nil
}

// IsLocalURL reports whether rawURL points to this computer or the LAN: a
// private or loopback address, localhost, a single-label host name ("nas")
// or a local domain (.local, .lan, .home.arpa, .internal...).
func IsLocalURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return util.IsPrivateIP(ip)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".intranet", ".corp"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// Client talks to addons.
type Client struct {
	// HTTP reaches public addons. util.HTTP by default: it refuses to
	// connect to the LAN, even when a public host name resolves there.
	HTTP *http.Client
	// Local reaches the addons the user installed from this computer or
	// the LAN.
	Local *http.Client
}

// DefaultClient is the client of the package-level functions.
var DefaultClient = NewClient()

// NewClient returns a client with the default HTTP clients.
func NewClient() *Client {
	return &Client{HTTP: util.HTTP, Local: localHTTP()}
}

// localHTTP is the client for addons on this computer or the LAN: no
// address guard, and no proxy (it couldn't reach the LAN anyway).
var localHTTP = sync.OnceValue(func() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: requestTimeout,
		},
	}
})

func (c *Client) httpClient(local bool) *http.Client {
	if local {
		if c.Local != nil {
			return c.Local
		}
		return localHTTP()
	}
	if c.HTTP != nil {
		return c.HTTP
	}
	return util.HTTP
}

// get fetches rawURL (with the local client when local is set, after
// checking the URL isn't local otherwise) and decodes its JSON into out.
func (c *Client) get(ctx context.Context, rawURL string, local bool, limit int64, out any) error {
	if !local && IsLocalURL(rawURL) {
		return ErrLocalAddon
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient(local).Do(req)
	if err != nil {
		if errors.Is(err, util.ErrPrivateAddress) {
			return ErrLocalAddon
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("the addon answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("the addon's answer is too large (over %d MB)", limit>>20)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("the addon's answer is not valid JSON: %w", err)
	}
	return nil
}

// FetchManifest downloads an addon's manifest. rawURL is a manifest URL, a
// base URL or a stremio:// link; addons on this computer or the LAN are
// refused (see Store.Install).
func (c *Client) FetchManifest(ctx context.Context, rawURL string) (Addon, error) {
	return c.fetchManifest(ctx, rawURL, false)
}

func (c *Client) fetchManifest(ctx context.Context, rawURL string, allowLocal bool) (Addon, error) {
	transport, err := NormalizeURL(rawURL)
	if err != nil {
		return Addon{}, err
	}
	local := IsLocalURL(transport)
	if local && !allowLocal {
		return Addon{}, ErrLocalAddon
	}
	var raw json.RawMessage
	if err := c.get(ctx, transport, local, maxManifestSize, &raw); err != nil {
		return Addon{}, fmt.Errorf("could not read the addon's manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Addon{}, fmt.Errorf("invalid addon manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Addon{}, err
	}
	return Addon{TransportURL: transport, Manifest: m, Local: local, Supported: m.Useful(), raw: raw}, nil
}

// FetchManifest downloads an addon's manifest with DefaultClient.
func FetchManifest(ctx context.Context, rawURL string) (Addon, error) {
	return DefaultClient.FetchManifest(ctx, rawURL)
}

// resourceURL is <base>/<resource>/<type>/<id>[/<extra>].json.
func resourceURL(a Addon, resource, typ, id string, extra map[string]string) string {
	u := a.BaseURL() + "/" + resource + "/" + url.PathEscape(typ) + "/" + url.PathEscape(id)
	if e := encodeExtra(extra); e != "" {
		u += "/" + e
	}
	return u + ".json"
}

// encodeExtra writes extra arguments the way Stremio does
// (encodeURIComponent'd query string, keys sorted).
func encodeExtra(extra map[string]string) string {
	keys := make([]string, 0, len(extra))
	for k, v := range extra {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, encodeComponent(k)+"="+encodeComponent(extra[k]))
	}
	return strings.Join(parts, "&")
}

func encodeComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// Streams asks an addon for the streams of a video: typ is "movie" or
// "series", id "tt0133093" or "tt0944947:1:2" (season 1, episode 2).
func (c *Client) Streams(ctx context.Context, a Addon, typ, id string) ([]providers.Stream, error) {
	var resp struct {
		Streams []json.RawMessage `json:"streams"`
	}
	if err := c.get(ctx, resourceURL(a, ResourceStream, typ, id, nil), a.Local, maxResponseSize, &resp); err != nil {
		return nil, err
	}
	out := make([]providers.Stream, 0, len(resp.Streams))
	for _, raw := range resp.Streams {
		if s, ok := parseStream(raw, a); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// Streams asks an addon for streams with DefaultClient.
func Streams(ctx context.Context, a Addon, typ, id string) ([]providers.Stream, error) {
	return DefaultClient.Streams(ctx, a, typ, id)
}

// Subtitles asks a subtitles addon for the subtitles of a video.
func (c *Client) Subtitles(ctx context.Context, a Addon, typ, id string) ([]providers.Subtitle, error) {
	return c.SubtitlesFor(ctx, a, typ, id, nil)
}

// SubtitlesFor is Subtitles with extra arguments: "filename", "videoSize"
// (bytes) and "videoHash" (OpenSubtitles hash) help addons find subtitles
// matching the very file.
func (c *Client) SubtitlesFor(ctx context.Context, a Addon, typ, id string, extra map[string]string) ([]providers.Subtitle, error) {
	var resp struct {
		Subtitles []json.RawMessage `json:"subtitles"`
	}
	if err := c.get(ctx, resourceURL(a, ResourceSubtitles, typ, id, extra), a.Local, maxResponseSize, &resp); err != nil {
		return nil, err
	}
	out := make([]providers.Subtitle, 0, len(resp.Subtitles))
	for _, raw := range resp.Subtitles {
		var w wireSubtitle
		if json.Unmarshal(raw, &w) != nil {
			continue
		}
		if s, ok := w.subtitle(); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// Subtitles asks an addon for subtitles with DefaultClient.
func Subtitles(ctx context.Context, a Addon, typ, id string) ([]providers.Subtitle, error) {
	return DefaultClient.Subtitles(ctx, a, typ, id)
}

// FetchCollection downloads an addon collection: a JSON array of
// {transportUrl, transportName, manifest, flags} (like Stremio's
// https://api.strem.io/addonscollection.json), or an addon catalog answer
// ({"addons": [...]}). Entries that make no sense are skipped.
func (c *Client) FetchCollection(ctx context.Context, rawURL string) ([]Addon, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("a collection URL starts with https://")
	}
	var raw json.RawMessage
	if err := c.get(ctx, u.String(), false, maxCollectionSize, &raw); err != nil {
		return nil, fmt.Errorf("could not read the addon collection: %w", err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		var wrapped struct {
			Addons []json.RawMessage `json:"addons"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 != nil || wrapped.Addons == nil {
			return nil, errors.New("this is not an addon collection")
		}
		entries = wrapped.Addons
	}
	return parseCollection(entries), nil
}

// parseCollection reads the entries of an addon collection
// ({transportUrl, transportName, manifest, flags}), skipping those that
// make no sense and the second of two at the same address.
func parseCollection(entries []json.RawMessage) []Addon {
	out := make([]Addon, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		var entry struct {
			TransportURL  string          `json:"transportUrl"`
			TransportName string          `json:"transportName"`
			Manifest      json.RawMessage `json:"manifest"`
			Flags         Flags           `json:"flags"`
		}
		if json.Unmarshal(e, &entry) != nil {
			continue
		}
		transport, err := NormalizeURL(entry.TransportURL)
		if err != nil || seen[transport] {
			continue
		}
		var m Manifest
		if json.Unmarshal(entry.Manifest, &m) != nil || m.Validate() != nil {
			continue
		}
		seen[transport] = true
		out = append(out, Addon{
			TransportURL: transport, TransportName: entry.TransportName, Manifest: m, Flags: entry.Flags,
			Local: IsLocalURL(transport), Supported: m.Useful(), raw: entry.Manifest,
		})
	}
	return out
}

// FetchCollection downloads an addon collection with DefaultClient.
func FetchCollection(ctx context.Context, rawURL string) ([]Addon, error) {
	return DefaultClient.FetchCollection(ctx, rawURL)
}
