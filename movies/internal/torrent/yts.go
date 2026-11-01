package torrent

// YTS
//
// YTS releases small encodes of movies (720p, 1080p, 2160p, 3D) and lists
// them through a JSON API:
//
//	GET <base>/api/v2/list_movies.json?query_term=<IMDb id or title>&limit=50
//	{"status":"ok","status_message":"Query was successful",
//	 "data":{"movie_count":1,"limit":50,"page_number":1,"movies":[
//	   {"url":"https://yts.mx/movies/the-matrix-1999","imdb_code":"tt0133093",
//	    "title":"The Matrix","title_long":"The Matrix (1999)","year":1999,
//	    "torrents":[{"url":"https://yts.mx/torrent/download/<HASH>","hash":"<HASH>",
//	      "quality":"1080p","type":"bluray","video_codec":"x264","seeds":100,"peers":20,
//	      "size":"2.07 GB","size_bytes":2222611251,"date_uploaded_unix":1446320871}]}]}}
//
// "movies" is missing when nothing matches. query_term matches titles,
// IMDb codes and cast. The site moves between domains (yts.mx, yts.lt...):
// TorrentSource.URL points at the current one.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ytsTrackers are the trackers of YTS's own magnet links.
var ytsTrackers = []string{
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.openbittorrent.com:80",
	"udp://tracker.coppersurfer.tk:6969",
	"udp://glotorrents.pw:6969/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://torrent.gresille.org:80/announce",
	"udp://p4p.arenabg.com:1337",
	"udp://tracker.leechers-paradise.org:6969",
}

// YTS searches yts.mx (movies only).
type YTS struct {
	Base string // default https://yts.mx
	HTTP *http.Client
}

func (y *YTS) ID() string   { return "yts" }
func (y *YTS) Name() string { return "YTS" }

func (y *YTS) Supports(mediaType string) bool { return mediaType == MediaMovie || mediaType == "" }

func (y *YTS) base() string { return srcBase(y.Base, "https://yts.mx", "/api/v2") }

type ytsAnswer struct {
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	// data is an object, or [] with an error status.
	Data json.RawMessage `json:"data"`
}

type ytsData struct {
	MovieCount looseInt   `json:"movie_count"`
	Movies     []ytsMovie `json:"movies"`
}

type ytsMovie struct {
	URL       string       `json:"url"`
	IMDbCode  string       `json:"imdb_code"`
	Title     string       `json:"title"`
	TitleLong string       `json:"title_long"`
	Year      looseInt     `json:"year"`
	Torrents  []ytsTorrent `json:"torrents"`
}

type ytsTorrent struct {
	URL              string   `json:"url"`
	Hash             string   `json:"hash"`
	Quality          string   `json:"quality"`
	Type             string   `json:"type"`
	VideoCodec       string   `json:"video_codec"`
	Seeds            looseInt `json:"seeds"`
	Peers            looseInt `json:"peers"`
	Size             string   `json:"size"`
	SizeBytes        looseInt `json:"size_bytes"`
	DateUploadedUnix looseInt `json:"date_uploaded_unix"`
}

func (y *YTS) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	movies, err := y.list(ctx, query)
	if err != nil {
		return nil, err
	}
	return y.results(movies), nil
}

func (y *YTS) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Type == MediaTV {
		return nil, ErrUnsupported
	}
	if q.Query != "" {
		return y.Search(ctx, q.Query)
	}
	if q.IMDbID != "" {
		// Every YTS movie has its IMDb id: one YTS doesn't know by id, it
		// doesn't have (a title search would find other movies).
		movies, err := y.list(ctx, q.IMDbID)
		if err != nil {
			return nil, err
		}
		return y.results(y.keep(movies, q)), nil
	}
	var firstErr error
	for _, t := range q.titles() {
		movies, err := y.list(ctx, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if res := y.results(y.keep(movies, q)); len(res) > 0 {
			return res, nil
		}
	}
	return []*SearchResult{}, firstErr
}

// keep returns the movies that are q's: the same IMDb id, else the same
// year (±1, release years vary by country).
func (y *YTS) keep(movies []ytsMovie, q MediaQuery) []ytsMovie {
	var out []ytsMovie
	for _, m := range movies {
		switch {
		case q.IMDbID != "" && m.IMDbCode != "":
			if !sameIMDb(m.IMDbCode, q.IMDbID) {
				continue
			}
		case q.Year > 0 && m.Year > 0:
			if d := int(m.Year) - q.Year; d < -1 || d > 1 {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func (y *YTS) list(ctx context.Context, term string) ([]ytsMovie, error) {
	v := url.Values{"query_term": {term}, "limit": {"50"}}
	var a ytsAnswer
	if err := srcGetJSON(ctx, srcClient(y.HTTP), y.base()+"/api/v2/list_movies.json?"+v.Encode(), &a); err != nil {
		return nil, err
	}
	if a.Status != "" && !strings.EqualFold(a.Status, "ok") {
		return nil, fmt.Errorf("YTS answered: %s", firstNonBlank(a.StatusMessage, a.Status))
	}
	var d ytsData
	if t := bytes.TrimSpace(a.Data); len(t) > 0 && t[0] == '{' {
		if err := json.Unmarshal(t, &d); err != nil {
			return nil, fmt.Errorf("unexpected answer from the site (%v)", err)
		}
	}
	return d.Movies, nil
}

// results lists the torrents of movies, named like YTS's own files:
// "The Matrix (1999) [1080p] [BluRay] [YTS]".
func (y *YTS) results(movies []ytsMovie) []*SearchResult {
	out := []*SearchResult{}
	for _, m := range movies {
		title := firstNonBlank(m.TitleLong, m.Title)
		if title == "" {
			continue
		}
		for _, t := range m.Torrents {
			hash := canonicalHash(t.Hash)
			if hash == "" {
				continue
			}
			name := title + " [" + t.Quality + "]"
			if typ := ytsType(t.Type); typ != "" {
				name += " [" + typ + "]"
			}
			if strings.EqualFold(t.VideoCodec, "x265") {
				name += " [x265]" // there are x264 and x265 copies of a quality
			}
			name += " [YTS]"
			r := newResult(y.ID(), name)
			r.Link = m.URL
			r.DownloadURL = t.URL
			r.InfoHash = hash
			r.MagnetLink = magnetWith(hash, name, ytsTrackers)
			r.Seeders, r.Leechers = int(t.Seeds), int(t.Peers)
			r.Size = int64(t.SizeBytes)
			if r.Size <= 0 {
				r.FormattedSize = t.Size
			}
			r.Date = unixDate(int64(t.DateUploadedUnix))
			r.Resolution = ncResolution(strings.ToLower(t.Quality))
			r.ReleaseGroup = "YTS"
			annotate(r)
			out = append(out, r)
		}
	}
	return out
}

func ytsType(t string) string {
	switch strings.ToLower(t) {
	case "bluray":
		return "BluRay"
	case "web":
		return "WEB"
	}
	return t
}

func (y *YTS) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if m := magnetResult(r, ytsTrackers); m != "" {
		return m, nil
	}
	if r.DownloadURL != "" {
		return r.DownloadURL, nil
	}
	return "", fmt.Errorf("no magnet link for %s", r.Name)
}
