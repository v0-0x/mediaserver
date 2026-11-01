package torrent

// The Pirate Bay
//
// thepiratebay.org's pages are drawn by JavaScript from a JSON API,
// apibay.org:
//
//	GET <base>/q.php?q=<words or IMDb id tt…>&cat=<category>
//	[{"id":"123","name":"The.Matrix.1999.1080p.BluRay.x264-SPARKS",
//	  "info_hash":"<HEX>","leechers":"12","seeders":"345","num_files":"3",
//	  "size":"1234567890","username":"user","added":"1446320871",
//	  "status":"vip","category":"207","imdb":"tt0133093"}]
//
// Every value is a string. Nothing found is one row with id "0" and an
// all-zero info hash ("No results returned"). A search returns at most 100
// rows. Searching "tt0133093" lists the torrents tagged with that IMDb id.
// Categories: 200 video (with all of its subcategories), 201 movies,
// 202 movie DVDR, 205 TV, 207 HD movies, 208 HD TV, 209 3D, 211 UHD movies,
// 212 UHD TV; KumoBox asks for 200 and drops the other kind's rows itself,
// so one search finds both SD and HD copies.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// pirateBayTrackers are those of The Pirate Bay's own magnet links.
var pirateBayTrackers = []string{
	"udp://tracker.opentrackr.org:1337",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://tracker.bittor.pw:1337/announce",
	"udp://public.popcorn-tracker.org:6969/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://exodus.desync.com:6969",
	"udp://open.demonii.com:1337/announce",
}

var (
	pirateBayMovieCats = map[int]bool{201: true, 202: true, 207: true, 209: true, 211: true}
	pirateBayTVCats    = map[int]bool{205: true, 208: true, 212: true}
)

// PirateBay searches The Pirate Bay through apibay.org.
type PirateBay struct {
	Base string // the API, default https://apibay.org
	// Site is where result pages link to (default https://thepiratebay.org).
	Site string
	HTTP *http.Client
}

func (p *PirateBay) ID() string   { return "piratebay" }
func (p *PirateBay) Name() string { return "The Pirate Bay" }

func (p *PirateBay) Supports(string) bool { return true }

func (p *PirateBay) base() string { return srcBase(p.Base, "https://apibay.org", "/q.php") }

type apibayRow struct {
	ID       looseInt    `json:"id"`
	Name     string      `json:"name"`
	InfoHash string      `json:"info_hash"`
	Leechers looseInt    `json:"leechers"`
	Seeders  looseInt    `json:"seeders"`
	NumFiles looseInt    `json:"num_files"`
	Size     looseInt    `json:"size"`
	Username string      `json:"username"`
	Added    looseInt    `json:"added"`
	Status   string      `json:"status"`
	Category looseInt    `json:"category"`
	IMDb     looseString `json:"imdb"`
}

func (p *PirateBay) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	return p.query(ctx, query, "")
}

func (p *PirateBay) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Query != "" {
		return p.query(ctx, q.Query, q.Type)
	}
	var firstErr error
	byID := []*SearchResult{}
	if q.IMDbID != "" {
		res, err := p.query(ctx, q.IMDbID, q.Type)
		if err != nil {
			firstErr = err
		}
		byID = keepMatching(res, q)
		// The 100 rows of a long show may not reach an older episode (only
		// packs that hold it): the text search can.
		if len(byID) > 0 && (q.Type != MediaTV || q.Episode == 0 || hasSingleEpisode(byID)) {
			return byID, nil
		}
	}
	for _, t := range q.titles() {
		res, err := p.query(ctx, q.textFor(t), q.Type)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if res = keepMatching(res, q); len(res) > 0 {
			return append(byID, res...), nil
		}
	}
	if len(byID) > 0 {
		return byID, nil
	}
	return byID, firstErr
}

func hasSingleEpisode(results []*SearchResult) bool {
	for _, r := range results {
		if r.EpisodeNumber > 0 && !r.IsBatch {
			return true
		}
	}
	return false
}

// keepMatching returns the results that may be what q asks for.
func keepMatching(results []*SearchResult, q MediaQuery) []*SearchResult {
	out := []*SearchResult{}
	for _, r := range results {
		if matchesMedia(r, q) {
			out = append(out, r)
		}
	}
	return out
}

// query searches the video category, keeping the rows of mediaType ("" for
// any).
func (p *PirateBay) query(ctx context.Context, text, mediaType string) ([]*SearchResult, error) {
	v := url.Values{"q": {text}, "cat": {"200"}}
	var rows []apibayRow
	if err := srcGetJSON(ctx, srcClient(p.HTTP), p.base()+"/q.php?"+v.Encode(), &rows); err != nil {
		return nil, err
	}
	site := srcBase(p.Site, "https://thepiratebay.org")
	out := []*SearchResult{}
	for _, row := range rows {
		hash := canonicalHash(row.InfoHash)
		if row.ID == 0 || hash == "" {
			continue // the "No results returned" row
		}
		cat := int(row.Category)
		if (mediaType == MediaMovie && pirateBayTVCats[cat]) || (mediaType == MediaTV && pirateBayMovieCats[cat]) {
			continue
		}
		r := newResult(p.ID(), row.Name)
		r.Link = site + "/description.php?id=" + strconv.FormatInt(int64(row.ID), 10)
		r.InfoHash = hash
		r.MagnetLink = magnetWith(hash, r.Name, pirateBayTrackers)
		r.Seeders, r.Leechers = int(row.Seeders), int(row.Leechers)
		r.Size = int64(row.Size)
		r.Date = unixDate(int64(row.Added))
		switch strings.ToLower(row.Status) {
		case "vip", "trusted", "helper", "moderator", "supermod", "admin":
			r.Trusted = true
		}
		annotate(r)
		out = append(out, r)
	}
	return out, nil
}

func (p *PirateBay) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if m := magnetResult(r, pirateBayTrackers); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("no magnet link for %s", r.Name)
}
