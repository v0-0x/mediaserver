package torrent

// Torznab
//
// Torznab is the Newznab API for torrents, served by Jackett and Prowlarr
// for each of the hundreds of trackers they reach (public and private), and
// by a few sites themselves. Moviko asks a feed what it can search
// (t=caps), then searches movies by IMDb/TMDB id (t=movie), episodes by
// IMDb/TVDB/TMDB id, season and episode (t=tvsearch), and falls back on
// text (t=search) when the feed can't search by id or finds nothing that
// way. Spec: https://torznab.github.io/spec-1.3-draft/
//
// Assumptions (Jackett 0.2x, Prowlarr 1.x):
//   - the configured URL is the feed's API endpoint (…/api); Moviko adds
//     /api when it's missing, as Sonarr and Radarr do with their "API
//     path";
//   - ids go without "tt" (imdbid=0133093), as Newznab defines them; both
//     Jackett and Prowlarr also take them with it;
//   - items carry torznab:attr seeders, peers (seeders + leechers),
//     infohash, magneturl, size, grabs, imdbid, downloadvolumefactor; the
//     enclosure is the .torrent (or a magnet) and comments the details page;
//   - errors come as <error code="100" description="Invalid API Key"/>,
//     with any HTTP status.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Newznab categories.
const (
	torznabMovies = "2000"
	torznabTV     = "5000"
)

// maxTorrentFile bounds a downloaded .torrent (those of big season packs
// are a few MB).
const maxTorrentFile = 16 << 20

// torznabHTTP reaches the indexers when no client is given: Jackett and
// Prowlarr run on this computer or the LAN, which util.HTTP refuses.
var torznabHTTP = &http.Client{Timeout: 45 * time.Second}

// Torznab searches one Torznab or Newznab feed.
type Torznab struct {
	id, name string
	endpoint string     // the feed's address, without query
	extra    url.Values // query parameters the configured URL carries
	apiKey   string
	// HTTP is the client to use (nil: a plain one).
	HTTP *http.Client

	mu        sync.Mutex
	caps      *torznabCaps
	capsUntil time.Time
}

// NewTorznab returns the source of a configured indexer.
func NewTorznab(idx config.TorznabIndexer, client *http.Client) *Torznab {
	raw := strings.TrimSpace(idx.URL)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	t := &Torznab{HTTP: client, apiKey: strings.TrimSpace(idx.APIKey), extra: url.Values{}}
	u, err := url.Parse(raw)
	if err != nil {
		t.endpoint = raw
	} else {
		for k, vs := range u.Query() {
			switch strings.ToLower(k) {
			case "apikey":
				if t.apiKey == "" && len(vs) > 0 {
					t.apiKey = vs[0]
				}
			case "t", "q", "cat", "imdbid", "tvdbid", "tmdbid", "season", "ep", "limit", "offset", "extended":
			default:
				t.extra[k] = vs
			}
		}
		u.RawQuery, u.Fragment = "", ""
		p := strings.TrimRight(u.Path, "/")
		if !strings.HasSuffix(p, "/api") && !strings.HasSuffix(p, ".php") {
			p += "/api"
		}
		u.Path = p
		t.endpoint = u.String()
	}
	t.name = strings.TrimSpace(idx.Name)
	t.id = "torznab-" + slug(t.name)
	if t.name == "" {
		host := ""
		if u != nil {
			host = u.Host
		}
		t.name = "Torznab (" + firstNonBlank(host, "?") + ")"
		t.id = "torznab-" + slug(host)
	}
	return t
}

func (t *Torznab) ID() string   { return t.id }
func (t *Torznab) Name() string { return t.name }

func (t *Torznab) Supports(string) bool { return true }

func (t *Torznab) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return torznabHTTP
}

// Search looks for free text among movies and shows.
func (t *Torznab) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	return t.search(ctx, url.Values{"t": {"search"}, "q": {query}, "cat": {torznabMovies + "," + torznabTV}}, MediaQuery{})
}

func (t *Torznab) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Query != "" {
		v := url.Values{"t": {"search"}, "q": {q.Query}}
		switch q.Type {
		case MediaMovie:
			v.Set("cat", torznabMovies)
		case MediaTV:
			v.Set("cat", torznabTV)
		default:
			v.Set("cat", torznabMovies+","+torznabTV)
		}
		return t.search(ctx, v, MediaQuery{})
	}
	caps, err := t.capabilities(ctx)
	if err != nil {
		return nil, err
	}
	switch q.Type {
	case MediaMovie:
		return t.searchMovie(ctx, caps, q)
	case MediaTV:
		return t.searchTV(ctx, caps, q)
	}
	return nil, ErrUnsupported
}

func (t *Torznab) searchMovie(ctx context.Context, caps *torznabCaps, q MediaQuery) ([]*SearchResult, error) {
	if m := caps.MovieSearch; m.Available {
		v := url.Values{"t": {"movie"}, "cat": {torznabMovies}}
		byID := true
		switch {
		case q.IMDbID != "" && m.Params["imdbid"]:
			v.Set("imdbid", imdbDigits(q.IMDbID))
		case q.TMDBID > 0 && m.Params["tmdbid"]:
			v.Set("tmdbid", strconv.Itoa(q.TMDBID))
		case q.Title != "" && m.Params["q"]:
			byID = false
			if q.Year > 0 && m.Params["year"] {
				v.Set("q", plainTitle(q.Title))
				v.Set("year", strconv.Itoa(q.Year))
			} else {
				v.Set("q", q.textFor(plainTitle(q.Title)))
			}
		default:
			v = nil
		}
		if v != nil {
			res, err := t.search(ctx, v, q)
			// Trackers without ids for their torrents find nothing by id:
			// try the title then.
			if err != nil || len(res) > 0 || !byID {
				return res, err
			}
		}
	}
	return t.searchText(ctx, caps, q, torznabMovies)
}

func (t *Torznab) searchTV(ctx context.Context, caps *torznabCaps, q MediaQuery) ([]*SearchResult, error) {
	if m := caps.TVSearch; m.Available {
		v := url.Values{"t": {"tvsearch"}, "cat": {torznabTV}}
		byID := true
		switch {
		case q.IMDbID != "" && m.Params["imdbid"]:
			v.Set("imdbid", imdbDigits(q.IMDbID))
		case q.TVDBID > 0 && m.Params["tvdbid"]:
			v.Set("tvdbid", strconv.Itoa(q.TVDBID))
		case q.TMDBID > 0 && m.Params["tmdbid"]:
			v.Set("tmdbid", strconv.Itoa(q.TMDBID))
		case q.Title != "" && m.Params["q"]:
			byID = false
			v.Set("q", plainTitle(q.Title))
		default:
			v = nil
		}
		if v != nil {
			if q.Season > 0 || q.Episode > 0 {
				switch {
				case m.Params["season"]:
					v.Set("season", strconv.Itoa(q.Season))
					if q.Episode > 0 && m.Params["ep"] {
						v.Set("ep", strconv.Itoa(q.Episode))
					}
				case !byID:
					v.Set("q", q.textFor(v.Get("q")))
				}
				// Otherwise the feed lists all the show's torrents, and
				// SearchAll keeps those of the episode.
			}
			res, err := t.search(ctx, v, q)
			if err != nil || len(res) > 0 || !byID {
				return res, err
			}
		}
	}
	return t.searchText(ctx, caps, q, torznabTV)
}

// searchText searches the title as text ("Title 1999", "Title S01E02").
func (t *Torznab) searchText(ctx context.Context, caps *torznabCaps, q MediaQuery, cat string) ([]*SearchResult, error) {
	if !caps.Search.Available || q.Title == "" {
		return nil, nil
	}
	return t.search(ctx, url.Values{"t": {"search"}, "q": {q.textFor(plainTitle(q.Title))}, "cat": {cat}}, q)
}

// search runs a query and reads the feed. Results that the feed says are
// of another IMDb title than q's are dropped.
func (t *Torznab) search(ctx context.Context, v url.Values, q MediaQuery) ([]*SearchResult, error) {
	v.Set("extended", "1")
	if v.Get("limit") == "" {
		v.Set("limit", "100")
	}
	body, err := t.get(ctx, v)
	if err != nil {
		return nil, err
	}
	var feed torznabFeed
	if err := xmlUnmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("unexpected answer from the indexer (%v)", err)
	}
	out := []*SearchResult{}
	for _, it := range feed.Channel.Items {
		r, imdb := t.result(it)
		if r == nil {
			continue
		}
		if q.IMDbID != "" && imdb != "" && !sameIMDb(imdb, q.IMDbID) {
			continue
		}
		annotate(r)
		out = append(out, r)
	}
	return out, nil
}

// get calls the feed with the query v (plus the API key).
func (t *Torznab) get(ctx context.Context, v url.Values) ([]byte, error) {
	resp, err := srcFetch(ctx, t.client(), t.address(v), "application/rss+xml, application/xml, text/xml", maxResponse)
	if err != nil {
		return nil, err
	}
	if e := torznabErrorIn(resp.body); e != nil {
		return nil, e
	}
	if looksLikeWebPage(resp.header.Get("Content-Type"), resp.body) {
		return nil, &hintError{ErrWebPage, "the indexer returned a web page instead of a Torznab feed — check its URL in Settings (Jackett and Prowlarr feeds end in /api)"}
	}
	if resp.status == http.StatusUnauthorized || resp.status == http.StatusForbidden {
		return nil, &TorznabError{Code: 100, Description: fmt.Sprintf("HTTP %d", resp.status)}
	}
	if resp.status >= 400 {
		return nil, statusError(resp.status)
	}
	return resp.body, nil
}

func (t *Torznab) address(v url.Values) string {
	all := url.Values{}
	for k, vs := range t.extra {
		all[k] = vs
	}
	for k, vs := range v {
		all[k] = vs
	}
	if t.apiKey != "" {
		all.Set("apikey", t.apiKey)
	}
	return t.endpoint + "?" + all.Encode()
}

// result converts a feed item, and returns the IMDb id the feed gives it.
func (t *Torznab) result(it torznabItem) (*SearchResult, string) {
	if strings.TrimSpace(it.Title) == "" {
		return nil, ""
	}
	attrs := map[string]string{}
	for _, a := range it.Attrs {
		k := strings.ToLower(a.Name)
		if _, dup := attrs[k]; !dup {
			attrs[k] = strings.TrimSpace(a.Value)
		}
	}
	num := func(s string) int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
			n = int64(f)
		}
		return max(n, 0)
	}

	r := newResult(t.id, it.Title)
	r.Seeders = int(num(attrs["seeders"]))
	if l, ok := attrs["leechers"]; ok {
		r.Leechers = int(num(l))
	} else if p := int(num(attrs["peers"])); p > r.Seeders {
		r.Leechers = p - r.Seeders // peers counts the seeders too
	}
	r.Size = num(it.Size)
	if r.Size == 0 {
		r.Size = num(attrs["size"])
	}
	if r.Size == 0 {
		r.Size = num(it.Enclosure.Length)
	}
	r.DownloadCount = int(num(it.Grabs))
	if r.DownloadCount == 0 {
		r.DownloadCount = int(num(attrs["grabs"]))
	}
	// downloadvolumefactor 0 (freeleech) has no place in SearchResult yet.

	r.MagnetLink = attrs["magneturl"]
	for _, link := range []string{it.Enclosure.URL, it.Link} {
		link = strings.TrimSpace(link)
		switch {
		case link == "":
		case strings.HasPrefix(strings.ToLower(link), "magnet:"):
			if r.MagnetLink == "" {
				r.MagnetLink = link
			}
		case r.DownloadURL == "":
			r.DownloadURL = link
		}
	}
	r.InfoHash = canonicalHash(attrs["infohash"])
	if r.InfoHash == "" {
		r.InfoHash = hashFromMagnet(r.MagnetLink)
	}
	for _, page := range []string{it.Comments, it.GUID} {
		page = strings.TrimSpace(page)
		if isWebURL(page) && page != r.DownloadURL {
			r.Link = page
			break
		}
	}
	if r.Link == "" {
		r.Link = r.DownloadURL
	}
	if d := parseFeedDate(it.PubDate); d != "" {
		r.Date = d
	}
	return r, attrs["imdbid"]
}

// Magnet returns the result's magnet link. Without one, it downloads the
// .torrent from the indexer and makes the magnet from it, with its trackers
// (a private tracker's announce URL carries the user's passkey); a feed's
// info hash alone is a last resort, as it only works on public trackers.
func (t *Torznab) Magnet(ctx context.Context, r *SearchResult) (string, error) {
	if r.MagnetLink != "" {
		return r.MagnetLink, nil
	}
	hash := canonicalHash(r.InfoHash)
	if r.DownloadURL != "" {
		if !t.ownsURL(r.DownloadURL) {
			// Only addresses of the indexer are fetched from here (results
			// come back from the UI); the torrent client fetches others.
			return r.DownloadURL, nil
		}
		m, err := t.torrentMagnet(ctx, r.DownloadURL)
		if err == nil {
			return m, nil
		}
		if hash == "" {
			if errors.Is(err, errV2Only) {
				return r.DownloadURL, nil
			}
			return "", err
		}
	}
	if hash != "" {
		return MagnetFromHash(hash, r.Name), nil
	}
	return "", errors.New("the indexer gave no magnet link or torrent file for this result")
}

// ownsURL reports whether u is on the indexer's host.
func (t *Torznab) ownsURL(u string) bool {
	a, err1 := url.Parse(u)
	b, err2 := url.Parse(t.endpoint)
	return err1 == nil && err2 == nil && isWebURL(u) && strings.EqualFold(a.Host, b.Host)
}

// torrentMagnet downloads a .torrent through the indexer and returns its
// magnet link. Indexers that only know a torrent's magnet redirect to it.
func (t *Torznab) torrentMagnet(ctx context.Context, link string) (string, error) {
	c := *t.client()
	own := ""
	if u, err := url.Parse(link); err == nil {
		own = strings.ToLower(u.Hostname())
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		// The indexer may run on the LAN (Jackett, Prowlarr), and send the
		// file from a tracker's server; but a redirect elsewhere on this
		// computer or the LAN is refused.
		if h := strings.ToLower(req.URL.Hostname()); h != own && util.IsLocalHostname(h) {
			return errors.New("the indexer redirected the torrent download to a local address")
		}
		return nil
	}
	resp, err := srcFetch(ctx, &c, link, "application/x-bittorrent", maxTorrentFile)
	if err != nil {
		return "", err
	}
	if resp.status >= 300 && resp.status < 400 {
		if loc := resp.header.Get("Location"); strings.HasPrefix(strings.ToLower(loc), "magnet:") {
			return loc, nil
		}
	}
	if e := torznabErrorIn(resp.body); e != nil {
		return "", e
	}
	if looksLikeWebPage(resp.header.Get("Content-Type"), resp.body) {
		return "", &hintError{ErrWebPage, "the indexer returned a web page instead of the torrent file"}
	}
	if resp.status >= 400 {
		return "", fmt.Errorf("the indexer couldn't give the torrent file (HTTP %d %s)", resp.status, http.StatusText(resp.status))
	}
	if b := bytes.TrimSpace(resp.body); bytes.HasPrefix(bytes.ToLower(b), []byte("magnet:")) {
		return string(b), nil
	}
	return magnetFromTorrent(resp.body)
}

// ---------------------------------------------------------------------------
// Capabilities

// torznabCaps is what a feed can search.
type torznabCaps struct {
	Search, TVSearch, MovieSearch torznabMode
}

type torznabMode struct {
	Available bool
	Params    map[string]bool // q, imdbid, tvdbid, tmdbid, season, ep, year...
}

type torznabCapsXML struct {
	Searching struct {
		Search      torznabModeXML `xml:"search"`
		TVSearch    torznabModeXML `xml:"tv-search"`
		MovieSearch torznabModeXML `xml:"movie-search"`
	} `xml:"searching"`
}

type torznabModeXML struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

func (m torznabModeXML) mode() torznabMode {
	out := torznabMode{Available: strings.EqualFold(strings.TrimSpace(m.Available), "yes"), Params: map[string]bool{}}
	params := m.SupportedParams
	if strings.TrimSpace(params) == "" {
		params = "q"
	}
	for _, p := range strings.Split(params, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out.Params[p] = true
		}
	}
	return out
}

// basicCaps is what any feed does: search text.
func basicCaps() *torznabCaps {
	return &torznabCaps{Search: torznabMode{Available: true, Params: map[string]bool{"q": true}}}
}

// capabilities returns the feed's capabilities, asked once in a while. A
// feed that can't tell is searched by text; a refused API key is an error.
func (t *Torznab) capabilities(ctx context.Context) (*torznabCaps, error) {
	t.mu.Lock()
	if t.caps != nil && time.Now().Before(t.capsUntil) {
		c := t.caps
		t.mu.Unlock()
		return c, nil
	}
	t.mu.Unlock()

	caps, err := t.fetchCaps(ctx)
	ttl := 6 * time.Hour
	if err != nil {
		var te *TorznabError
		if (errors.As(err, &te) && te.IsAuth()) || ctx.Err() != nil {
			return nil, err
		}
		caps, ttl = basicCaps(), 5*time.Minute
	}
	t.mu.Lock()
	t.caps, t.capsUntil = caps, time.Now().Add(ttl)
	t.mu.Unlock()
	return caps, nil
}

func (t *Torznab) fetchCaps(ctx context.Context) (*torznabCaps, error) {
	body, err := t.get(ctx, url.Values{"t": {"caps"}})
	if err != nil {
		return nil, err
	}
	var x torznabCapsXML
	if err := xmlUnmarshal(body, &x); err != nil {
		return nil, err
	}
	caps := &torznabCaps{
		Search:      x.Searching.Search.mode(),
		TVSearch:    x.Searching.TVSearch.mode(),
		MovieSearch: x.Searching.MovieSearch.mode(),
	}
	if !caps.Search.Available && !caps.TVSearch.Available && !caps.MovieSearch.Available {
		caps.Search = basicCaps().Search // a feed without <searching> still searches
	}
	return caps, nil
}

// ---------------------------------------------------------------------------
// Feeds and errors

type torznabFeed struct {
	Channel struct {
		Items []torznabItem `xml:"item"`
	} `xml:"channel"`
}

type torznabItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Comments  string `xml:"comments"`
	PubDate   string `xml:"pubDate"`
	Size      string `xml:"size"`
	Grabs     string `xml:"grabs"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	// torznab:attr, or newznab:attr in Newznab feeds.
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

// TorznabError is an error a Torznab feed answered with.
type TorznabError struct {
	Code        int
	Description string
}

// IsAuth reports whether the feed refused the API key or the account.
func (e *TorznabError) IsAuth() bool { return e.Code >= 100 && e.Code <= 102 }

func (e *TorznabError) Error() string {
	desc := strings.TrimSpace(e.Description)
	switch {
	case e.IsAuth():
		return fmt.Sprintf("the indexer refused the API key (%s) — copy the API key from Jackett or Prowlarr into this indexer's settings", firstNonBlank(desc, "invalid API key"))
	case e.Code == 500 || e.Code == 501:
		return fmt.Sprintf("the indexer's request limit is reached (%s) — try again later", firstNonBlank(desc, "limit reached"))
	case e.Code == 910:
		return "the indexer's API is disabled"
	}
	return fmt.Sprintf("the indexer answered with error %d: %s", e.Code, firstNonBlank(desc, "unknown error"))
}

// torznabErrorIn returns the error a feed answered with, if its document
// is an <error/>.
func torznabErrorIn(body []byte) *TorznabError {
	head := body[:min(len(body), 2048)]
	if !bytes.Contains(head, []byte("<error")) {
		return nil
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "error" {
			return nil
		}
		e := &TorznabError{}
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "code":
				e.Code, _ = strconv.Atoi(strings.TrimSpace(a.Value))
			case "description":
				e.Description = a.Value
			}
		}
		return e
	}
}

// xmlUnmarshal reads a feed, whatever encoding it declares (they're UTF-8
// or ASCII in practice).
func xmlUnmarshal(body []byte, out any) error {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec.Decode(out)
}

// parseFeedDate reads an RSS date as RFC 3339, or "".
func parseFeedDate(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", time.RFC3339} {
		if d, err := time.Parse(layout, s); err == nil {
			return d.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func isWebURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// slug makes an id out of a name: "My Indexer!" is "my-indexer".
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "indexer"
	}
	return out
}

// ---------------------------------------------------------------------------
// .torrent files

// errV2Only: a BitTorrent v2-only torrent has no v1 info hash for a
// btih magnet.
var errV2Only = errors.New("this is a BitTorrent v2-only torrent")

var errBencode = errors.New("not a valid torrent file")

// bvalue is a decoded bencode value.
type bvalue struct {
	kind byte // 'i', 's', 'l' or 'd'
	num  int64
	str  []byte
	list []bvalue
	dict map[string]bvalue
	raw  []byte // the value's own encoding: the info hash is its SHA-1
}

// bdecode reads one bencoded value (a .torrent file is a dictionary).
func bdecode(b []byte) (bvalue, error) {
	v, _, err := bparse(b, 0)
	return v, err
}

func bparse(b []byte, depth int) (bvalue, int, error) {
	if depth > 64 || len(b) == 0 {
		return bvalue{}, 0, errBencode
	}
	switch c := b[0]; {
	case c == 'i':
		end := bytes.IndexByte(b, 'e')
		if end < 2 {
			return bvalue{}, 0, errBencode
		}
		n, err := strconv.ParseInt(string(b[1:end]), 10, 64)
		if err != nil {
			return bvalue{}, 0, errBencode
		}
		return bvalue{kind: 'i', num: n, raw: b[:end+1]}, end + 1, nil
	case c >= '0' && c <= '9':
		colon := bytes.IndexByte(b, ':')
		if colon < 1 || colon > 10 {
			return bvalue{}, 0, errBencode
		}
		n, err := strconv.Atoi(string(b[:colon]))
		if err != nil || n < 0 || n > len(b)-colon-1 {
			return bvalue{}, 0, errBencode
		}
		end := colon + 1 + n
		return bvalue{kind: 's', str: b[colon+1 : end], raw: b[:end]}, end, nil
	case c == 'l' || c == 'd':
		v := bvalue{kind: c}
		if c == 'd' {
			v.dict = map[string]bvalue{}
		}
		i := 1
		for {
			if i >= len(b) {
				return bvalue{}, 0, errBencode
			}
			if b[i] == 'e' {
				v.raw = b[:i+1]
				return v, i + 1, nil
			}
			item, n, err := bparse(b[i:], depth+1)
			if err != nil {
				return bvalue{}, 0, err
			}
			i += n
			if c == 'l' {
				v.list = append(v.list, item)
				continue
			}
			if item.kind != 's' {
				return bvalue{}, 0, errBencode
			}
			val, n, err := bparse(b[i:], depth+1)
			if err != nil {
				return bvalue{}, 0, err
			}
			i += n
			v.dict[string(item.str)] = val
		}
	}
	return bvalue{}, 0, errBencode
}

// torrentMeta is what a magnet link needs from a .torrent.
type torrentMeta struct {
	InfoHash string // hex SHA-1 of the bencoded info dictionary
	Name     string
	Trackers []string
	Private  bool
}

// readTorrent reads a .torrent file.
func readTorrent(data []byte) (torrentMeta, error) {
	root, err := bdecode(data)
	if err != nil || root.kind != 'd' {
		return torrentMeta{}, errBencode
	}
	info, ok := root.dict["info"]
	if !ok || info.kind != 'd' {
		return torrentMeta{}, errBencode
	}
	if _, ok := info.dict["pieces"]; !ok {
		if mv := info.dict["meta version"]; mv.kind == 'i' && mv.num == 2 {
			return torrentMeta{}, errV2Only
		}
	}
	sum := sha1.Sum(info.raw)
	m := torrentMeta{InfoHash: hex.EncodeToString(sum[:])}
	for _, k := range []string{"name.utf-8", "name"} {
		if n := info.dict[k]; n.kind == 's' && len(n.str) > 0 {
			m.Name = string(n.str)
			break
		}
	}
	m.Private = info.dict["private"].kind == 'i' && info.dict["private"].num == 1
	add := func(v bvalue) {
		if v.kind != 's' {
			return
		}
		tr := strings.TrimSpace(string(v.str))
		l := strings.ToLower(tr)
		if (isWebURL(tr) || strings.HasPrefix(l, "udp://") || strings.HasPrefix(l, "wss://")) && !hasFold(m.Trackers, tr) {
			m.Trackers = append(m.Trackers, tr)
		}
	}
	for _, tier := range root.dict["announce-list"].list {
		for _, tr := range tier.list {
			add(tr)
		}
	}
	add(root.dict["announce"])
	return m, nil
}

// magnetFromTorrent makes the magnet link of a .torrent file, with its own
// trackers, and for a public torrent the usual ones too.
func magnetFromTorrent(data []byte) (string, error) {
	m, err := readTorrent(data)
	if err != nil {
		return "", err
	}
	v := url.Values{}
	if m.Name != "" {
		v.Set("dn", m.Name)
	}
	trackers := m.Trackers
	if !m.Private {
		for _, tr := range Trackers {
			if !hasFold(trackers, tr) {
				trackers = append(trackers, tr)
			}
		}
	}
	for _, tr := range trackers {
		v.Add("tr", tr)
	}
	out := "magnet:?xt=urn:btih:" + m.InfoHash
	if len(v) > 0 {
		out += "&" + v.Encode()
	}
	return out, nil
}
