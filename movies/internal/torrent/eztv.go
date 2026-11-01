package torrent

// EZTV
//
// EZTV releases TV episodes and lists a show's torrents, newest first, by
// the show's IMDb id (without "tt"):
//
//	GET <base>/api/get-torrents?imdb_id=0903747&limit=100&page=1
//	{"imdb_id":"0903747","torrents_count":512,"limit":100,"page":1,"torrents":[
//	  {"id":1,"hash":"…","filename":"Breaking.Bad.S05E16.720p.HDTV.x264-IMMERSE[eztv].mkv",
//	   "episode_url":"https://eztvx.to/ep/…","torrent_url":"https://zoink.ch/torrent/….torrent",
//	   "magnet_url":"magnet:?xt=urn:btih:…","title":"Breaking Bad S05E16 720p HDTV x264-IMMERSE EZTV",
//	   "imdb_id":"0903747","season":"5","episode":"16","seeds":120,"peers":10,
//	   "date_released_unix":1380000000,"size_bytes":"1234567890"}]}
//
// Numbers come as strings or numbers depending on the field and the day,
// hence looseInt. "torrents" is missing when the show has none. The API
// can't search text, so EZTV only finds shows whose IMDb id is known. The
// site moves between domains (eztv.re, eztvx.to...): TorrentSource.URL
// points at the current one.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// eztvPageSize is the API's largest page.
const eztvPageSize = 100

// EZTV searches eztvx.to (shows only, by IMDb id).
type EZTV struct {
	Base string // default https://eztvx.to
	HTTP *http.Client
	// MaxPages bounds the pages of a show read for one search (default 5,
	// the 500 newest torrents).
	MaxPages int
}

func (e *EZTV) ID() string   { return "eztv" }
func (e *EZTV) Name() string { return "EZTV" }

func (e *EZTV) Supports(mediaType string) bool { return mediaType == MediaTV }

func (e *EZTV) base() string { return srcBase(e.Base, "https://eztvx.to", "/api") }

type eztvAnswer struct {
	TorrentsCount looseInt      `json:"torrents_count"`
	Limit         looseInt      `json:"limit"`
	Page          looseInt      `json:"page"`
	Torrents      []eztvTorrent `json:"torrents"`
}

type eztvTorrent struct {
	Hash             string      `json:"hash"`
	Filename         string      `json:"filename"`
	Title            string      `json:"title"`
	EpisodeURL       string      `json:"episode_url"`
	TorrentURL       string      `json:"torrent_url"`
	MagnetURL        string      `json:"magnet_url"`
	IMDbID           looseString `json:"imdb_id"`
	Season           looseInt    `json:"season"`
	Episode          looseInt    `json:"episode"`
	Seeds            looseInt    `json:"seeds"`
	Peers            looseInt    `json:"peers"`
	DateReleasedUnix looseInt    `json:"date_released_unix"`
	SizeBytes        looseInt    `json:"size_bytes"`
}

var reOnlyIMDb = regexp.MustCompile(`(?i)^tt\d{5,10}$`)

// Search finds a show's torrents when the query is its IMDb id (tt…):
// EZTV can't search text.
func (e *EZTV) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	query = strings.TrimSpace(query)
	if !reOnlyIMDb.MatchString(query) {
		return nil, fmt.Errorf("%w: EZTV only finds shows by IMDb id", ErrUnsupported)
	}
	return e.collect(ctx, query, -1, func(*SearchResult) bool { return true })
}

func (e *EZTV) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Type != MediaTV {
		return nil, ErrUnsupported
	}
	if q.IMDbID == "" {
		return nil, fmt.Errorf("%w: EZTV needs the show's IMDb id", ErrUnsupported)
	}
	if q.Query != "" {
		return e.collect(ctx, q.IMDbID, -1, func(r *SearchResult) bool { return queryWords(r.Name, q.Query) })
	}
	// Torrents come newest first: once a whole page is older than the
	// episode (or season) asked for, the next pages, older still, won't
	// have it.
	oldest := -1
	if q.Season > 0 || q.Episode > 0 {
		oldest = catalog.EpisodeKey(q.Season, q.Episode)
	}
	return e.collect(ctx, q.IMDbID, oldest, func(r *SearchResult) bool { return matchesMedia(r, q) })
}

// collect reads the pages of a show's torrents and keeps some, stopping
// after a page all older than the episode key oldest (-1: read on).
func (e *EZTV) collect(ctx context.Context, imdb string, oldest int, keep func(*SearchResult) bool) ([]*SearchResult, error) {
	pages := e.MaxPages
	if pages <= 0 {
		pages = 5
	}
	out := []*SearchResult{}
	for page := 1; page <= pages; page++ {
		a, err := e.page(ctx, imdb, page)
		if err != nil {
			if page > 1 {
				break // keep what the first pages found
			}
			return nil, err
		}
		older := len(a.Torrents) > 0
		for _, t := range a.Torrents {
			r := e.result(t)
			if r == nil {
				continue
			}
			if s, ep := int(t.Season), int(t.Episode); s <= 0 || catalog.EpisodeKey(s, ep) >= oldest {
				older = false
			}
			if keep(r) {
				out = append(out, r)
			}
		}
		if len(a.Torrents) < eztvPageSize || int(a.TorrentsCount) <= page*eztvPageSize || (oldest >= 0 && older) {
			break
		}
	}
	return out, nil
}

func (e *EZTV) page(ctx context.Context, imdb string, page int) (*eztvAnswer, error) {
	v := url.Values{"imdb_id": {imdbDigits(imdb)}, "limit": {strconv.Itoa(eztvPageSize)}, "page": {strconv.Itoa(page)}}
	var a eztvAnswer
	if err := srcGetJSON(ctx, srcClient(e.HTTP), e.base()+"/api/get-torrents?"+v.Encode(), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (e *EZTV) result(t eztvTorrent) *SearchResult {
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t.Title), " EZTV"))
	if name == "" {
		name = ncExtension.ReplaceAllString(strings.TrimSpace(t.Filename), "")
	}
	hash := canonicalHash(t.Hash)
	if hash == "" {
		hash = hashFromMagnet(t.MagnetURL)
	}
	if name == "" || (hash == "" && t.TorrentURL == "") {
		return nil
	}
	r := newResult(e.ID(), name)
	r.Link = t.EpisodeURL
	r.DownloadURL = t.TorrentURL
	r.InfoHash = hash
	r.MagnetLink = t.MagnetURL
	if r.MagnetLink == "" && hash != "" {
		r.MagnetLink = MagnetFromHash(hash, name)
	}
	r.Seeders, r.Leechers = int(t.Seeds), int(t.Peers)
	r.Size = int64(t.SizeBytes)
	r.Date = unixDate(int64(t.DateReleasedUnix))
	// The API's season and episode count when the name has none.
	if f := checkName(name); f.Episode < 0 && !f.Pack && t.Season > 0 && t.Episode > 0 {
		r.EpisodeNumber = catalog.EpisodeKey(int(t.Season), int(t.Episode))
	}
	annotate(r)
	return r
}

func (e *EZTV) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if m := magnetResult(r, nil); m != "" {
		return m, nil
	}
	if r.DownloadURL != "" {
		return r.DownloadURL, nil
	}
	return "", fmt.Errorf("no magnet link for %s", r.Name)
}
