package torrent

// Torrent search sources for movies and shows
//
// Moviko finds torrents of a movie, an episode, a season or a whole show
// on public sites (YTS, EZTV, The Pirate Bay, Torrents-CSV, the Internet
// Archive) and on the user's own indexers (Jackett, Prowlarr, any Torznab
// feed). A Source knows how to ask one of them; SearchAll asks them all at
// once, drops what clearly isn't what was asked for, and lists each torrent
// once.

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Media types of MediaQuery.Type and Source.Supports.
const (
	MediaMovie = "movie"
	MediaTV    = "tv"
)

// MediaQuery is what to search torrents for: a movie, or an episode, a
// season or the whole of a show.
type MediaQuery struct {
	Title     string   `json:"title"`
	AltTitles []string `json:"altTitles,omitempty"` // original or other-language titles
	Year      int      `json:"year,omitempty"`
	IMDbID    string   `json:"imdbId,omitempty"` // tt0133093
	TMDBID    int      `json:"tmdbId,omitempty"`
	TVDBID    int      `json:"tvdbId,omitempty"`
	Type      string   `json:"type"` // MediaMovie or MediaTV
	// Season and Episode of a show: Episode 0 asks for a season pack,
	// Season 0 and Episode 0 for the whole show. Specials are season 0
	// (S00E03 is Season 0, Episode 3).
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
	// Resolution ranks results: "2160", "1080", "720" or "" (any).
	Resolution string `json:"resolution,omitempty"`
	// Query is free text the user typed: it replaces the searches made
	// from the title and ids, and nothing is filtered out.
	Query string `json:"query,omitempty"`
	// Media is the catalog entry the query was made from, when there is
	// one: extension providers get it whole (sources only use the fields
	// above).
	Media *catalog.Media `json:"-"`
}

// Source searches one site or indexer for torrents.
type Source interface {
	ID() string
	Name() string
	// Supports reports whether the source finds mediaType (MediaMovie or
	// MediaTV); "" asks whether it can search free text.
	Supports(mediaType string) bool
	// Search looks for free text.
	Search(ctx context.Context, query string) ([]*SearchResult, error)
	// SearchMedia looks for a movie or (part of) a show, by its ids when
	// the site knows them, else by its title.
	SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error)
	// Magnet returns what to give the torrent client for a result: a
	// magnet link, or the address of a .torrent file.
	Magnet(ctx context.Context, r *SearchResult) (string, error)
}

var (
	_ Source = (*Torznab)(nil)
	_ Source = (*YTS)(nil)
	_ Source = (*EZTV)(nil)
	_ Source = (*TorrentsCSV)(nil)
	_ Source = (*PirateBay)(nil)
	_ Source = (*InternetArchive)(nil)
)

// ErrUnsupported is returned by a source asked for something it can't look
// for (a show on YTS, free text on EZTV...). SearchAll skips it quietly.
var ErrUnsupported = errors.New("this source can't search for that")

// ErrWebPage means a site answered a search with a web page: a Cloudflare
// challenge, a block page, a parked domain or an error page.
var ErrWebPage = errors.New("the site returned a web page instead of results (blocked or down) — try a mirror URL in Settings")

// SourceTimeout bounds each source's search in SearchAll.
var SourceTimeout = 25 * time.Second

// BuildSources returns the enabled search sources of cfg: the Torznab
// indexers first (the user set them up, and they reach the most trackers),
// then YTS (movies), EZTV (shows), Torrents-CSV, The Pirate Bay and the
// Internet Archive.
//
// client is used for every request; nil means util.HTTP for the public
// sites (it refuses to connect to the LAN) and a plain client for the
// Torznab indexers, which usually run on this computer or the LAN.
func BuildSources(cfg config.TorrentSourceSettings, client *http.Client) []Source {
	var out []Source
	used := map[string]bool{}
	for _, idx := range cfg.Torznab {
		if !idx.Enabled || strings.TrimSpace(idx.URL) == "" {
			continue
		}
		t := NewTorznab(idx, client)
		id := t.id
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", t.id, n)
		}
		t.id = id
		used[id] = true
		out = append(out, t)
	}
	if cfg.YTS.Enabled {
		out = append(out, &YTS{Base: cfg.YTS.URL, HTTP: client})
	}
	if cfg.EZTV.Enabled {
		out = append(out, &EZTV{Base: cfg.EZTV.URL, HTTP: client})
	}
	if cfg.TorrentsCSV.Enabled {
		out = append(out, &TorrentsCSV{Base: cfg.TorrentsCSV.URL, HTTP: client})
	}
	if cfg.PirateBay.Enabled {
		out = append(out, &PirateBay{Base: cfg.PirateBay.URL, HTTP: client})
	}
	if cfg.Archive.Enabled {
		out = append(out, &InternetArchive{Base: cfg.Archive.URL, HTTP: client})
	}
	return out
}

// SourceByID returns the source with that id, or nil.
func SourceByID(sources []Source, id string) Source {
	for _, s := range sources {
		if s != nil && s.ID() == id {
			return s
		}
	}
	return nil
}

// SearchAll asks every source that supports q's media type at once, each
// for at most SourceTimeout. It drops results that clearly aren't what q
// asks for (another episode or season, a movie of another year), lists
// each torrent once (the best-seeded copy, with the ids of every source
// that found it in Provider, comma-separated) and sorts them: torrents with
// seeders first, then those of q's resolution, then by seeders.
//
// The errors are those of the sources that failed, prefixed with their
// names; results come back even when some sources fail.
func SearchAll(ctx context.Context, sources []Source, q MediaQuery) ([]*SearchResult, []error) {
	q = q.normalized()
	timeout := SourceTimeout
	type answer struct {
		i   int
		res []*SearchResult
		err error
	}
	answers := make([]*answer, len(sources))
	asked := make([]bool, len(sources))
	ch := make(chan answer, len(sources))
	waiting := 0
	for i, s := range sources {
		if s == nil || !s.Supports(q.Type) {
			continue
		}
		asked[i] = true
		waiting++
		go func(i int, s Source) {
			a := answer{i: i}
			defer func() {
				if v := recover(); v != nil {
					a.res, a.err = nil, fmt.Errorf("internal error: %v", v)
				}
				ch <- a
			}()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			a.res, a.err = s.SearchMedia(cctx, q)
			if a.err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				a.err = fmt.Errorf("no answer within %s", timeout)
			}
		}(i, s)
	}
	// A source that ignores its context doesn't hold the others up.
	deadline := time.NewTimer(timeout + min(2*time.Second, timeout))
	defer deadline.Stop()
wait:
	for ; waiting > 0; waiting-- {
		select {
		case a := <-ch:
			answers[a.i] = &a
		case <-deadline.C:
			break wait
		}
	}

	var errs []error
	var all []*SearchResult
	for i, s := range sources {
		if !asked[i] {
			continue
		}
		a := answers[i]
		if a == nil {
			errs = append(errs, fmt.Errorf("%s: no answer within %s", s.Name(), timeout))
			continue
		}
		if a.err != nil {
			if !errors.Is(a.err, ErrUnsupported) {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name(), a.err))
			}
			continue
		}
		for _, r := range a.res {
			if r == nil {
				continue
			}
			if r.Provider == "" {
				r.Provider = s.ID()
			}
			annotate(r)
			if q.Query == "" && !matchesMedia(r, q) {
				continue
			}
			all = append(all, r)
		}
	}
	out := mergeResults(all)
	sortResults(out, q)
	return out, errs
}

// ProviderIDs are the ids of the sources that found r: SearchAll lists them
// all in Provider, comma-separated, the one whose copy was kept first.
func ProviderIDs(r *SearchResult) []string {
	var ids []string
	for _, id := range strings.Split(r.Provider, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// MagnetFor returns what to give the torrent client for r (a magnet link or
// a .torrent address), asking the sources that found it, then falling back
// on what r holds.
func MagnetFor(ctx context.Context, sources []Source, r *SearchResult) (string, error) {
	var lastErr error
	for _, id := range ProviderIDs(r) {
		if s := SourceByID(sources, id); s != nil {
			m, err := s.Magnet(ctx, r)
			if err == nil && m != "" {
				return m, nil
			}
			lastErr = err
		}
	}
	switch {
	case r.MagnetLink != "":
		return r.MagnetLink, nil
	case canonicalHash(r.InfoHash) != "":
		return MagnetFromHash(canonicalHash(r.InfoHash), r.Name), nil
	case r.DownloadURL != "":
		return r.DownloadURL, nil
	case lastErr != nil:
		return "", lastErr
	}
	return "", errors.New("this result has no magnet link or torrent file")
}

// ---------------------------------------------------------------------------
// Queries

// normalized tidies q up: canonical type, IMDb id and resolution.
func (q MediaQuery) normalized() MediaQuery {
	q.Title = strings.TrimSpace(q.Title)
	q.Query = strings.TrimSpace(q.Query)
	switch strings.ToLower(strings.TrimSpace(q.Type)) {
	case "movie", "movies", "film":
		q.Type = MediaMovie
	case "tv", "show", "shows", "series":
		q.Type = MediaTV
	default:
		q.Type = ""
	}
	if q.Type == "" && q.Query == "" {
		q.Query = q.Title
	}
	q.IMDbID = normalizeIMDb(q.IMDbID)
	q.Resolution = wantedResolution(q.Resolution)
	q.Season = max(q.Season, 0)
	q.Episode = max(q.Episode, 0)
	return q
}

// titles are the titles to search by text: the main one, then the others,
// at most three.
func (q MediaQuery) titles() []string {
	var out []string
	for _, t := range append([]string{q.Title}, q.AltTitles...) {
		t = plainTitle(t)
		if t != "" && !hasFold(out, t) {
			out = append(out, t)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

// textFor is the text search for q with the title t: "Title 1999" for a
// movie, "Title S01E02" / "Title S01" / "Title" for a show.
func (q MediaQuery) textFor(t string) string {
	switch q.Type {
	case MediaMovie:
		if q.Year > 0 {
			return fmt.Sprintf("%s %d", t, q.Year)
		}
	case MediaTV:
		if tag := q.episodeTag(); tag != "" {
			return t + " " + tag
		}
	}
	return t
}

// episodeTag is how releases name what q asks for: S01E02, S01, or "" for
// a whole show.
func (q MediaQuery) episodeTag() string {
	switch {
	case q.Episode > 0:
		return fmt.Sprintf("S%02dE%02d", q.Season, q.Episode)
	case q.Season > 0:
		return fmt.Sprintf("S%02d", q.Season)
	}
	return ""
}

var (
	reIMDb      = regexp.MustCompile(`(?i)(?:^|tt|/)(\d{5,10})\b`)
	reNonSearch = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// normalizeIMDb returns an IMDb id as tt followed by at least 7 digits, or
// "". It takes "tt0133093", "0133093", "133093" or an imdb.com address.
func normalizeIMDb(id string) string {
	m := reIMDb.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return ""
	}
	d := strings.TrimLeft(m[1], "0")
	if d == "" {
		return ""
	}
	for len(d) < 7 {
		d = "0" + d
	}
	return "tt" + d
}

// imdbDigits is an IMDb id without its "tt" (0133093), as Newznab feeds and
// EZTV take it.
func imdbDigits(id string) string {
	return strings.TrimPrefix(normalizeIMDb(id), "tt")
}

// sameIMDb reports whether two IMDb ids, written any way, are the same.
func sameIMDb(a, b string) bool {
	a, b = normalizeIMDb(a), normalizeIMDb(b)
	return a != "" && a == b
}

// plainTitle turns a title into search words: sites match words, and
// punctuation either confuses their search or isn't in release names
// ("Ocean's Eleven" is "Oceans.Eleven", "Spider-Man: No Way Home" is
// "Spider-Man.No.Way.Home" or "Spider.Man.No.Way.Home").
func plainTitle(t string) string {
	t = strings.NewReplacer("'", "", "’", "", "`", "").Replace(t)
	return strings.Join(strings.Fields(reNonSearch.ReplaceAllString(t, " ")), " ")
}

// wantedResolution reads MediaQuery.Resolution ("1080", "1080p", "4K") the
// way results name it ("1080p"), or "" for any.
func wantedResolution(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "any", "best":
		return ""
	case "4k", "uhd":
		return "2160p"
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "p"), "i")
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s + "p"
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// queryWords reports whether name contains every word of query, for sites
// that can't search text themselves.
func queryWords(name, query string) bool {
	n := " " + strings.ToLower(plainTitle(name)) + " "
	for _, w := range strings.Fields(strings.ToLower(plainTitle(query))) {
		if !strings.Contains(n, " "+w+" ") {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Results

// mergeResults lists each torrent once: the copy with the most seeders,
// completed with what the others know, and the ids of every source that
// found it.
func mergeResults(results []*SearchResult) []*SearchResult {
	out := make([]*SearchResult, 0, len(results))
	at := map[string]int{}
	for _, r := range results {
		k := resultKey(r)
		i, dup := at[k]
		if !dup {
			at[k] = len(out)
			out = append(out, r)
			continue
		}
		kept, other := out[i], r
		if other.Seeders > kept.Seeders {
			kept, other = other, kept
		}
		fillResult(kept, other)
		kept.Provider = joinProviders(kept.Provider, other.Provider)
		out[i] = kept
	}
	return out
}

// resultKey identifies a torrent across sources: its info hash, else its
// file's address, else its name and size.
func resultKey(r *SearchResult) string {
	if h := canonicalHash(r.InfoHash); h != "" {
		return "h:" + h
	}
	if h := hashFromMagnet(r.MagnetLink); h != "" {
		return "h:" + h
	}
	if r.DownloadURL != "" {
		return "u:" + r.DownloadURL
	}
	if r.Link != "" {
		return "u:" + r.Link
	}
	return "n:" + strings.ToLower(strings.TrimSpace(r.Name)) + "|" + strconv.FormatInt(r.Size, 10)
}

// fillResult completes dst with what src knows and dst doesn't.
func fillResult(dst, src *SearchResult) {
	fill := func(d *string, s string) {
		if *d == "" {
			*d = s
		}
	}
	fill(&dst.MagnetLink, src.MagnetLink)
	fill(&dst.InfoHash, src.InfoHash)
	fill(&dst.DownloadURL, src.DownloadURL)
	fill(&dst.Link, src.Link)
	fill(&dst.Date, src.Date)
	fill(&dst.Resolution, src.Resolution)
	fill(&dst.ReleaseGroup, src.ReleaseGroup)
	if dst.Size <= 0 && src.Size > 0 {
		dst.Size, dst.FormattedSize = src.Size, src.FormattedSize
	}
	if dst.Leechers == 0 {
		dst.Leechers = src.Leechers
	}
	dst.DownloadCount = max(dst.DownloadCount, src.DownloadCount)
	if dst.EpisodeNumber < 0 && !dst.IsBatch {
		dst.EpisodeNumber = src.EpisodeNumber
	}
	dst.Trusted = dst.Trusted || src.Trusted
}

func joinProviders(a, b string) string {
	ids := ProviderIDs(&SearchResult{Provider: a})
	for _, id := range ProviderIDs(&SearchResult{Provider: b}) {
		if !hasFold(ids, id) {
			ids = append(ids, id)
		}
	}
	return strings.Join(ids, ",")
}

// sortResults puts torrents with seeders first (a dead torrent of the right
// resolution helps nobody; the Internet Archive's, whose seeders aren't
// known, come after the others), then those that are exactly what was asked
// for (see fit), then those of the wanted resolution, then the best-seeded.
func sortResults(results []*SearchResult, q MediaQuery) {
	fits := make(map[*SearchResult]int, len(results))
	for _, r := range results {
		fits[r] = fit(r, q)
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if (a.Seeders > 0) != (b.Seeders > 0) {
			return a.Seeders > 0
		}
		if fa, fb := fits[a], fits[b]; fa != fb {
			return fa < fb
		}
		if q.Resolution != "" {
			if am, bm := a.Resolution == q.Resolution, b.Resolution == q.Resolution; am != bm {
				return am
			}
		}
		return a.Seeders > b.Seeders
	})
}

// fit ranks how exactly a result is what was asked for, 0 being the best:
// for an episode, the episode itself before a pack holding it; for a
// season, that season's pack before packs of several seasons or of the
// whole show. Movies and free-text searches all fit alike.
func fit(r *SearchResult, q MediaQuery) int {
	if q.Query != "" || q.Type != MediaTV {
		return 0
	}
	f := checkName(r.Name)
	switch {
	case q.Episode > 0:
		if f.singleEpisode() {
			return 0
		}
		if f.Episode >= 0 {
			return 1 // a range of episodes
		}
		if f.SeasonFrom == f.SeasonTo && f.SeasonFrom > 0 {
			return 2 // the season's pack
		}
		return 3
	case q.Season > 0:
		if f.Pack && f.Episode < 0 && f.SeasonFrom == q.Season && f.SeasonTo == q.Season {
			return 0
		}
		return 1
	}
	return 0
}

// newResult starts a result of a source.
func newResult(provider, name string) *SearchResult {
	return &SearchResult{Provider: provider, Name: strings.TrimSpace(name), EpisodeNumber: -1}
}

// unixDate formats a Unix time for SearchResult.Date ("" for none).
func unixDate(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// magnetResult returns r's magnet link, or one made from its info hash.
func magnetResult(r *SearchResult, trackers []string) string {
	if r.MagnetLink != "" {
		return r.MagnetLink
	}
	if h := canonicalHash(r.InfoHash); h != "" {
		return magnetWith(h, r.Name, trackers)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Info hashes and magnets

// canonicalHash returns a v1 info hash as 40 lowercase hex digits, from hex
// or base32 (some magnets use it), or "" when h isn't one. All zeros
// (apibay's "no results" row) isn't one either.
func canonicalHash(h string) string {
	h = strings.TrimSpace(h)
	switch len(h) {
	case 40:
		b, err := hex.DecodeString(h)
		if err != nil || isZero(b) {
			return ""
		}
		return strings.ToLower(h)
	case 32:
		b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h))
		if err != nil || len(b) != 20 || isZero(b) {
			return ""
		}
		return hex.EncodeToString(b)
	}
	return ""
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// hashFromMagnet returns the info hash of a magnet link, or "".
func hashFromMagnet(m string) string {
	lower := strings.ToLower(m)
	if !strings.HasPrefix(lower, "magnet:") {
		return ""
	}
	i := strings.Index(lower, "urn:btih:")
	if i < 0 {
		return ""
	}
	h := m[i+len("urn:btih:"):]
	if j := strings.IndexAny(h, "&#"); j >= 0 {
		h = h[:j]
	}
	return canonicalHash(h)
}

// magnetWith is MagnetFromHash with the trackers a site's own magnets
// carry on top of the usual ones.
func magnetWith(hash, name string, extra []string) string {
	m := MagnetFromHash(hash, name)
	have := map[string]bool{}
	for _, t := range Trackers {
		have[t] = true
	}
	for _, t := range extra {
		if !have[t] {
			have[t] = true
			m += "&tr=" + url.QueryEscape(t)
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// HTTP

// maxResponse bounds a search answer: real ones are a few hundred KB.
const maxResponse = 8 << 20

// srcClient is the client a source uses: its own, else the shared one.
func srcClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return util.HTTP
}

// srcBase is a source's address: the configured one (a mirror), tidied up,
// else the default.
func srcBase(configured, def string, suffixes ...string) string {
	b := strings.TrimSpace(configured)
	if b == "" {
		return def
	}
	if !strings.Contains(b, "://") {
		b = "https://" + b
	}
	b = strings.TrimRight(b, "/")
	for _, s := range suffixes {
		b = strings.TrimRight(strings.TrimSuffix(b, s), "/")
	}
	return b
}

// srcResponse is an answer read by srcFetch.
type srcResponse struct {
	status int
	header http.Header
	body   []byte
}

// srcFetch GETs u: the status, headers and body, at most limit bytes of it.
// Errors never show API keys.
func srcFetch(ctx context.Context, client *http.Client, u, accept string, limit int64) (*srcResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("bad address %s", redactURL(u))
	}
	req.Header.Set("User-Agent", util.UserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, redactError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, redactError(err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the answer is too large (over %s)", humanSize(limit))
	}
	return &srcResponse{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// srcGet GETs a search answer: the body of a successful answer, or an error
// that tells the user what's wrong.
func srcGet(ctx context.Context, client *http.Client, u, accept string) ([]byte, error) {
	resp, err := srcFetch(ctx, client, u, accept, maxResponse)
	if err != nil {
		return nil, err
	}
	if looksLikeWebPage(resp.header.Get("Content-Type"), resp.body) {
		return nil, ErrWebPage
	}
	if resp.status >= 400 {
		return nil, statusError(resp.status)
	}
	return resp.body, nil
}

// srcGetJSON GETs a JSON answer into out.
func srcGetJSON(ctx context.Context, client *http.Client, u string, out any) error {
	b, err := srcGet(ctx, client, u, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("unexpected answer from the site (%v)", err)
	}
	return nil
}

// statusError explains an HTTP error status.
func statusError(status int) error {
	text := fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("the site limits how often it can be searched (%s) — try again in a minute", text)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("the site refused the search (%s) — it may block this network; try a mirror URL in Settings", text)
	case status == http.StatusNotFound:
		return fmt.Errorf("there is no search at this address (%s) — check the URL in Settings", text)
	case status >= 500:
		return fmt.Errorf("the site is having problems (%s) — try again later or a mirror URL in Settings", text)
	}
	return fmt.Errorf("the site answered %s", text)
}

// looksLikeWebPage reports whether an answer is an HTML page rather than
// the JSON or XML asked for. Some APIs send JSON as text/html, so the body
// decides first.
func looksLikeWebPage(contentType string, body []byte) bool {
	b := bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(b) == 0 || b[0] == '{' || b[0] == '[' {
		return false
	}
	head := bytes.ToLower(b[:min(len(b), 1024)])
	if bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html")) {
		return true
	}
	if bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<rss")) {
		return false
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	return bytes.Contains(head, []byte("<html")) || bytes.Contains(head, []byte("<body"))
}

var reSecretParam = regexp.MustCompile(`(?i)\b((?:jackett_)?api_?key|passkey|token|key)=[^&#\s"]*`)

// redactURL hides the API keys and passkeys of an address.
func redactURL(u string) string {
	return reSecretParam.ReplaceAllString(u, "$1=REDACTED")
}

// redactError hides the API keys in a request error, which quotes the
// address.
func redactError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = redactURL(ue.URL)
	}
	return err
}

// hintError is an error with a message of its own that still matches its
// base with errors.Is.
type hintError struct {
	base error
	msg  string
}

func (e *hintError) Error() string { return e.msg }
func (e *hintError) Unwrap() error { return e.base }

// ---------------------------------------------------------------------------
// Lenient JSON

// looseInt reads a JSON number that sites send as a number, a string
// ("123"), "", null or a one-element array. Anything else reads as 0: one
// odd field shouldn't lose a whole search.
type looseInt int64

func (n *looseInt) UnmarshalJSON(b []byte) error {
	*n = 0
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var list []looseInt
		if json.Unmarshal(b, &list) == nil && len(list) > 0 {
			*n = list[0]
		}
		return nil
	}
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		*n = looseInt(v)
	} else if f, err := strconv.ParseFloat(s, 64); err == nil {
		*n = looseInt(f)
	} else if m := reLeadingInt.FindString(s); m != "" {
		v, _ := strconv.ParseInt(m, 10, 64)
		*n = looseInt(v)
	}
	return nil
}

var reLeadingInt = regexp.MustCompile(`^\d+`)

// looseString reads a JSON string that may come as a number or a list
// (the first item).
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	*s = ""
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
	case b[0] == '"':
		var v string
		if json.Unmarshal(b, &v) == nil {
			*s = looseString(v)
		}
	case b[0] == '[':
		var list []looseString
		if json.Unmarshal(b, &list) == nil && len(list) > 0 {
			*s = list[0]
		}
	case b[0] == '{':
	default:
		*s = looseString(b)
	}
	return nil
}
