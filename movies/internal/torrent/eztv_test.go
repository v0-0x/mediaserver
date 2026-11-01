package torrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestEZTV(t *testing.T) {
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/get-torrents" || r.URL.Query().Get("imdb_id") != "0903747" {
			_, _ = w.Write([]byte(`{"imdb_id":"0","torrents_count":0,"limit":100,"page":1}`))
			return
		}
		_, _ = w.Write(srcFixture(t, "eztv_breakingbad.json"))
	})
	e := &EZTV{Base: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	res, err := e.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", IMDbID: "tt0903747", Season: 1, Episode: 2})
	if err != nil {
		t.Fatal(err)
	}
	q := srv.requests()[0].URL.Query()
	if q.Get("imdb_id") != "0903747" || q.Get("limit") != "100" || q.Get("page") != "1" || len(srv.requests()) != 1 {
		t.Fatalf("query %v (%d requests)", q, len(srv.requests()))
	}
	var names []string
	for _, r := range res {
		names = append(names, fmt.Sprintf("%s|%d|%v", r.Name, r.EpisodeNumber, r.IsBatch))
	}
	want := []string{
		"Breaking Bad S01E02 720p HDTV x264-CTU|1002|false",
		"Breaking Bad - Cat's in the Bag|1002|false", // the API's season and episode
		"Breaking Bad S01 1080p BluRay x265-GRP|-1|true",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Fatalf("results:\n%s", strings.Join(names, "\n"))
	}
	r := res[0]
	if r.Provider != "eztv" || r.InfoHash != "b0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9" || r.Seeders != 33 || r.Leechers != 4 || r.Size != 734003200 ||
		r.Link != "https://eztvx.to/ep/2002/breaking-bad-s01e02-720p-hdtv-x264-ctu/" || !strings.HasSuffix(r.DownloadURL, ".torrent") ||
		!strings.HasPrefix(r.MagnetLink, "magnet:?xt=urn:btih:b0b1") || r.Resolution != "720p" || r.ReleaseGroup != "CTU" || r.Date != "2008-01-22T11:06:40Z" {
		t.Fatalf("first: %+v", r)
	}
	// No magnet in the API: made from the hash.
	if res[1].MagnetLink == "" || res[1].Seeders != 7 || res[1].Size != 367001600 {
		t.Fatalf("second: %+v", res[1])
	}
	if m, err := e.Magnet(ctx, res[1]); err != nil || m != res[1].MagnetLink {
		t.Fatalf("magnet %q %v", m, err)
	}

	// Free text over the show's torrents.
	res, err = e.SearchMedia(ctx, MediaQuery{Type: MediaTV, IMDbID: "tt0903747", Query: "s05e16 1080p"})
	if err != nil || len(res) != 1 || res[0].EpisodeNumber != 5016 {
		t.Fatalf("query: %+v %v", res, err)
	}
	if res, err := e.Search(ctx, "tt0903747"); err != nil || len(res) != 5 {
		t.Fatalf("search by id: %d %v", len(res), err)
	}
	for _, q := range []MediaQuery{{Type: MediaTV, Title: "Breaking Bad"}, {Type: MediaMovie, IMDbID: "tt0133093"}} {
		if _, err := e.SearchMedia(ctx, q); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%+v: %v", q, err)
		}
	}
	if _, err := e.Search(ctx, "breaking bad"); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "IMDb") {
		t.Fatalf("text: %v", err)
	}
	if e.Supports(MediaMovie) || e.Supports("") || !e.Supports(MediaTV) {
		t.Fatal("Supports")
	}
}

// eztvPages serves a show of 250 torrents, newest first: 5 seasons of 50
// episodes.
func eztvPages(t *testing.T) *srcRecorder {
	return newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var torrents []map[string]any
		for i := (page - 1) * 100; i < page*100 && i < 250; i++ {
			season, ep := 5-i/50, 50-i%50
			torrents = append(torrents, map[string]any{
				"hash":     fmt.Sprintf("%040x", i+1),
				"title":    fmt.Sprintf("Long Show S%02dE%02d 720p HDTV x264-GRP EZTV", season, ep),
				"season":   strconv.Itoa(season),
				"episode":  strconv.Itoa(ep),
				"seeds":    10,
				"imdb_id":  "1234567",
				"filename": "x.mkv",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"imdb_id": "1234567", "torrents_count": 250, "limit": 100, "page": page, "torrents": torrents})
	})
}

func TestEZTVPages(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		q              MediaQuery
		pages, results int
	}{
		{MediaQuery{Season: 4, Episode: 10}, 2, 1}, // S04E10 is the 91st torrent; page 2 starts in season 3
		{MediaQuery{Season: 1, Episode: 1}, 3, 1},  // the oldest one, on the last page
		{MediaQuery{Season: 5}, 2, 0},              // page 1 ends in season 4: older still on page 2
		{MediaQuery{}, 3, 0},                       // the whole show: no pack, all pages read
	} {
		srv := eztvPages(t)
		e := &EZTV{Base: srv.URL, HTTP: srv.Client()}
		q := tc.q
		q.Type, q.Title, q.IMDbID = MediaTV, "Long Show", "tt1234567"
		res, err := e.SearchMedia(ctx, q)
		if err != nil || len(res) != tc.results || len(srv.requests()) != tc.pages {
			t.Errorf("%+v: %d results, %d pages, %v", tc.q, len(res), len(srv.requests()), err)
		}
	}
	srv := eztvPages(t)
	e := &EZTV{Base: srv.URL, HTTP: srv.Client(), MaxPages: 1}
	if _, err := e.SearchMedia(ctx, MediaQuery{Type: MediaTV, IMDbID: "tt1234567", Season: 1, Episode: 1}); err != nil || len(srv.requests()) != 1 {
		t.Fatalf("MaxPages: %d pages, %v", len(srv.requests()), err)
	}
}
