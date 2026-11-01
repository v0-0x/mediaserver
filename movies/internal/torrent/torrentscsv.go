package torrent

// Torrents-CSV
//
// torrents-csv.com is an open database of torrents (names, sizes and
// seeders scraped from trackers), searchable by words:
//
//	GET <base>/service/search?q=<words>&size=50
//	{"torrents":[{"rowid":1,"infohash":"…","name":"…","size_bytes":123,
//	  "created_unix":1600000000,"seeders":12,"leechers":3,"completed":40,
//	  "scraped_date":1700000000}],"next":1}
//
// Older servers (and self-hosted copies) answer the bare array instead of
// {"torrents": …}. It knows no IMDb ids: KumoBox searches "Title 1999" or
// "Title S01E02".

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// TorrentsCSV searches torrents-csv.com.
type TorrentsCSV struct {
	Base string // default https://torrents-csv.com
	HTTP *http.Client
}

func (c *TorrentsCSV) ID() string   { return "torrentscsv" }
func (c *TorrentsCSV) Name() string { return "Torrents-CSV" }

func (c *TorrentsCSV) Supports(string) bool { return true }

func (c *TorrentsCSV) base() string {
	return srcBase(c.Base, "https://torrents-csv.com", "/service/search", "/service")
}

type torrentsCSVRow struct {
	InfoHash    string   `json:"infohash"`
	Name        string   `json:"name"`
	SizeBytes   looseInt `json:"size_bytes"`
	CreatedUnix looseInt `json:"created_unix"`
	Seeders     looseInt `json:"seeders"`
	Leechers    looseInt `json:"leechers"`
	Completed   looseInt `json:"completed"`
}

func (c *TorrentsCSV) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	v := url.Values{"q": {query}, "size": {"50"}}
	b, err := srcGet(ctx, srcClient(c.HTTP), c.base()+"/service/search?"+v.Encode(), "application/json")
	if err != nil {
		return nil, err
	}
	var rows []torrentsCSVRow
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '[' {
		err = json.Unmarshal(t, &rows)
	} else {
		var page struct {
			Torrents []torrentsCSVRow `json:"torrents"`
		}
		err = json.Unmarshal(t, &page)
		rows = page.Torrents
	}
	if err != nil {
		return nil, fmt.Errorf("unexpected answer from the site (%v)", err)
	}
	out := []*SearchResult{}
	for _, row := range rows {
		hash := canonicalHash(row.InfoHash)
		if hash == "" || row.Name == "" {
			continue
		}
		r := newResult(c.ID(), row.Name)
		r.Link = c.base() + "/search?q=" + url.QueryEscape(row.Name)
		r.InfoHash = hash
		r.MagnetLink = MagnetFromHash(hash, r.Name)
		r.Seeders, r.Leechers = int(row.Seeders), int(row.Leechers)
		r.DownloadCount = int(row.Completed)
		r.Size = int64(row.SizeBytes)
		r.Date = unixDate(int64(row.CreatedUnix))
		annotate(r)
		out = append(out, r)
	}
	return out, nil
}

func (c *TorrentsCSV) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Query != "" {
		return c.Search(ctx, q.Query)
	}
	var firstErr error
	for _, t := range q.titles() {
		res, err := c.Search(ctx, q.textFor(t))
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

func (c *TorrentsCSV) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if m := magnetResult(r, nil); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("no magnet link for %s", r.Name)
}
