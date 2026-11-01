package torrent

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func apibayServer(t *testing.T) *srcRecorder {
	return newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/q.php" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// apibay answers JSON as text/html.
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		switch r.URL.Query().Get("q") {
		case "tt0903747":
			_, _ = w.Write(srcFixture(t, "apibay_imdb.json"))
		case "Breaking Bad S01E02":
			_, _ = w.Write(srcFixture(t, "apibay_text.json"))
		default:
			_, _ = w.Write(srcFixture(t, "apibay_none.json"))
		}
	})
}

func TestPirateBay(t *testing.T) {
	srv := apibayServer(t)
	p := &PirateBay{Base: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	// By IMDb id: the episode of season 5 is there.
	res, err := p.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", IMDbID: "tt0903747", Season: 5, Episode: 16})
	if err != nil || len(res) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	q := srv.requests()[0].URL.Query()
	if q.Get("q") != "tt0903747" || q.Get("cat") != "200" || len(srv.requests()) != 1 {
		t.Fatalf("query %v", q)
	}
	r := res[0]
	if r.Provider != "piratebay" || r.Name != "Breaking.Bad.S05E16.1080p.BluRay.x264-ROVERS" || r.InfoHash != "a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9" ||
		r.Seeders != 312 || r.Leechers != 14 || r.Size != 2147483648 || !r.Trusted || r.EpisodeNumber != 5016 ||
		r.Link != "https://thepiratebay.org/description.php?id=68910422" || r.Date != "2013-11-21T02:13:20Z" ||
		!strings.Contains(r.MagnetLink, "tracker.bittor.pw") || !strings.HasPrefix(r.MagnetLink, "magnet:?xt=urn:btih:a0b1") {
		t.Fatalf("episode: %+v", r)
	}
	if !res[1].IsBatch || !res[1].Trusted { // the complete series
		t.Fatalf("pack: %+v", res[1])
	}

	// Season 1's episode isn't among the id's rows (only the complete
	// series is): the text search finds it.
	res, err = p.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", IMDbID: "tt0903747", Season: 1, Episode: 2})
	if err != nil || len(res) != 3 || !res[0].IsBatch || res[1].Name != "Breaking.Bad.S01E02.720p.HDTV.x264-CTU" || res[2].Resolution != "1080p" {
		t.Fatalf("text: %+v %v", res, err)
	}
	reqs := srv.requests()
	if last := reqs[len(reqs)-1].URL.Query().Get("q"); last != "Breaking Bad S01E02" {
		t.Fatalf("text query %q", last)
	}

	// A movie search drops the TV rows; free text keeps all.
	res, err = p.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "El Camino", IMDbID: "tt0903747"})
	if err != nil || len(res) != 1 || res[0].Name != "El Camino A Breaking Bad Movie 2019 1080p WEBRip x264-RARBG" {
		t.Fatalf("movie: %+v %v", res, err)
	}
	if res, err := p.Search(ctx, "tt0903747"); err != nil || len(res) != 4 {
		t.Fatalf("free text: %d %v", len(res), err)
	}
	// "No results returned" is no result.
	if res, err := p.Search(ctx, "nothing at all"); err != nil || res == nil || len(res) != 0 {
		t.Fatalf("none: %+v %v", res, err)
	}
	if res, err := p.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Unknown", Year: 2001}); err != nil || len(res) != 0 {
		t.Fatalf("unknown: %+v %v", res, err)
	}
	if m, err := p.Magnet(ctx, &SearchResult{InfoHash: "A0B1C2D3E4F5A6B7C8D9E0F1A2B3C4D5E6F7A8B9", Name: "x"}); err != nil || !strings.HasPrefix(m, "magnet:?xt=urn:btih:a0b1") {
		t.Fatalf("magnet %q %v", m, err)
	}
}
