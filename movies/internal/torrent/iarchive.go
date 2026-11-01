package torrent

// Internet Archive
//
// archive.org keeps thousands of public-domain films and TV episodes, each
// item with a torrent (archive.org seeds it over HTTP: the .torrent lists
// its files' addresses as web seeds). Items are found with the advanced
// search:
//
//	GET <base>/advancedsearch.php?q=title:(night AND of AND the AND living AND dead) AND mediatype:(movies)
//	    &fl[]=identifier&fl[]=title&fl[]=year&fl[]=downloads&fl[]=btih&fl[]=item_size
//	    &rows=50&sort[]=downloads desc&output=json
//	{"responseHeader":{…},"response":{"numFound":2,"start":0,"docs":[
//	  {"identifier":"night_of_the_living_dead","title":"Night of the Living Dead",
//	   "year":"1968","downloads":2500000,"btih":"…","item_size":3456789012}]}}
//
// year comes as a string or a number, title sometimes as a list. An item's
// torrent is <base>/download/<id>/<id>_archive.torrent. Seeders aren't
// known (0): the web seeds serve every item.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// archiveTrackers are archive.org's own.
var archiveTrackers = []string{
	"http://bt1.archive.org:6969/announce",
	"http://bt2.archive.org:6969/announce",
}

// InternetArchive searches archive.org's films and TV.
type InternetArchive struct {
	Base string // default https://archive.org
	HTTP *http.Client
}

func (a *InternetArchive) ID() string   { return "archive" }
func (a *InternetArchive) Name() string { return "Internet Archive" }

func (a *InternetArchive) Supports(string) bool { return true }

func (a *InternetArchive) base() string {
	return srcBase(a.Base, "https://archive.org", "/advancedsearch.php")
}

type archiveAnswer struct {
	Response struct {
		NumFound looseInt     `json:"numFound"`
		Docs     []archiveDoc `json:"docs"`
	} `json:"response"`
	Error string `json:"error"`
}

type archiveDoc struct {
	Identifier string      `json:"identifier"`
	Title      looseString `json:"title"`
	Year       looseInt    `json:"year"`
	Downloads  looseInt    `json:"downloads"`
	BTIH       looseString `json:"btih"`
	ItemSize   looseInt    `json:"item_size"`
}

// Search looks for free text in titles.
func (a *InternetArchive) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	return a.search(ctx, query)
}

func (a *InternetArchive) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Query != "" {
		return a.search(ctx, q.Query)
	}
	var firstErr error
	for _, t := range q.titles() {
		res, err := a.search(ctx, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if res = keepMatching(res, q); len(res) > 0 {
			return res, nil
		}
	}
	return []*SearchResult{}, firstErr
}

func (a *InternetArchive) search(ctx context.Context, title string) ([]*SearchResult, error) {
	words := luceneWords(title)
	if words == "" {
		return []*SearchResult{}, nil
	}
	v := url.Values{
		"q":      {"title:(" + words + ") AND mediatype:(movies)"},
		"fl[]":   {"identifier", "title", "year", "downloads", "btih", "item_size"},
		"rows":   {"50"},
		"page":   {"1"},
		"sort[]": {"downloads desc"},
		"output": {"json"},
	}
	var ans archiveAnswer
	if err := srcGetJSON(ctx, srcClient(a.HTTP), a.base()+"/advancedsearch.php?"+v.Encode(), &ans); err != nil {
		return nil, err
	}
	if ans.Error != "" {
		return nil, fmt.Errorf("the Internet Archive answered: %s", ans.Error)
	}
	base := a.base()
	out := []*SearchResult{}
	for _, d := range ans.Response.Docs {
		id := strings.TrimSpace(d.Identifier)
		title := strings.TrimSpace(string(d.Title))
		if id == "" || strings.ContainsAny(id, "/?#") {
			continue
		}
		name := firstNonBlank(title, id)
		if y := strconv.Itoa(int(d.Year)); d.Year > 0 && !strings.Contains(name, y) {
			name += " (" + y + ")"
		}
		r := newResult(a.ID(), name)
		r.Link = base + "/details/" + url.PathEscape(id)
		r.DownloadURL = base + "/download/" + url.PathEscape(id) + "/" + url.PathEscape(id+"_archive.torrent")
		r.DownloadCount = int(d.Downloads)
		r.Size = int64(d.ItemSize)
		if hash := canonicalHash(string(d.BTIH)); hash != "" {
			r.InfoHash = hash
			// The web seed lets a client that knows the files download
			// them from archive.org.
			r.MagnetLink = magnetWith(hash, id, archiveTrackers) + "&ws=" + url.QueryEscape(base+"/download/")
		}
		annotate(r)
		out = append(out, r)
	}
	return out, nil
}

// luceneWords keeps the words of a title for the archive's Lucene query
// (whose syntax uses most punctuation), all required: the archive's
// default is any of them.
func luceneWords(s string) string {
	words := strings.Fields(plainTitle(s))
	for i, w := range words {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "TO":
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " AND ")
}

// Magnet returns the item's .torrent: its web seeds (archive.org's HTTP
// servers) download it with no peer at all, which a magnet link can't do
// (a client needs a peer to get the torrent's file list first).
func (a *InternetArchive) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if r.DownloadURL != "" {
		return r.DownloadURL, nil
	}
	if m := magnetResult(r, archiveTrackers); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("no torrent for %s", r.Name)
}
