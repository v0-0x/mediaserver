package torrent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func ytsServer(t *testing.T, answers map[string]string) *srcRecorder {
	return newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/list_movies.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		name, ok := answers[r.URL.Query().Get("query_term")]
		if !ok {
			name = "yts_empty.json"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(srcFixture(t, name))
	})
}

func TestYTS(t *testing.T) {
	srv := ytsServer(t, map[string]string{"tt0133093": "yts_matrix.json", "Dune": "yts_dune.json"})
	y := &YTS{Base: srv.URL + "/", HTTP: srv.Client()}
	ctx := context.Background()

	res, err := y.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "The Matrix", Year: 1999, IMDbID: "0133093"})
	if err != nil || len(res) != 4 {
		t.Fatalf("%d results, %v", len(res), err)
	}
	q := srv.requests()[0].URL.Query()
	if q.Get("query_term") != "tt0133093" || q.Get("limit") != "50" {
		t.Fatalf("query %v", q)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Name)
	}
	want := "The Matrix (1999) [720p] [BluRay] [YTS]|The Matrix (1999) [1080p] [BluRay] [YTS]|The Matrix (1999) [1080p] [BluRay] [x265] [YTS]|The Matrix (1999) [2160p] [WEB] [x265] [YTS]"
	if strings.Join(names, "|") != want {
		t.Fatalf("names %q", names)
	}
	r := res[1]
	if r.Provider != "yts" || r.InfoHash != "1d5b2e1d63e8de4f6c3a5f1a1a3c1b0d6f8e9a02" || r.Seeders != 650 || r.Leechers != 74 ||
		r.Size != 2222611251 || r.FormattedSize != "2.1 GiB" || r.Resolution != "1080p" || r.ReleaseGroup != "YTS" ||
		r.Link != "https://yts.mx/movies/the-matrix-1999" || r.DownloadURL != "https://yts.mx/torrent/download/1D5B2E1D63E8DE4F6C3A5F1A1A3C1B0D6F8E9A02" ||
		r.Date != "2015-10-31T19:47:55Z" || r.EpisodeNumber != -1 {
		t.Fatalf("1080p: %+v", r)
	}
	mag, err := url.ParseQuery(strings.TrimPrefix(r.MagnetLink, "magnet:?"))
	if err != nil || mag.Get("xt") != "urn:btih:"+r.InfoHash || mag.Get("dn") != r.Name || !hasFold(mag["tr"], "udp://open.demonii.com:1337/announce") || !hasFold(mag["tr"], Trackers[0]) {
		t.Fatalf("magnet %s", r.MagnetLink)
	}
	if res[2].Seeders != 120 || res[2].Size != 2126008811 || res[3].Resolution != "2160p" {
		t.Fatalf("string numbers / 2160p: %+v %+v", res[2], res[3])
	}
	if m, err := y.Magnet(ctx, &SearchResult{InfoHash: r.InfoHash, Name: "x"}); err != nil || !strings.Contains(m, "open.demonii.com") {
		t.Fatalf("Magnet: %q %v", m, err)
	}

	// Without an id: the title, keeping the movie of the right year.
	res, err = y.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Dune", AltTitles: []string{"Dune: Part One"}, Year: 2021})
	if err != nil || len(res) != 1 || res[0].Name != "Dune (2021) [1080p] [WEB] [YTS]" {
		t.Fatalf("by title: %+v %v", res, err)
	}
	// An id YTS doesn't know is a movie it doesn't have.
	n := len(srv.requests())
	if res, err := y.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Dune", Year: 2021, IMDbID: "tt9999999"}); err != nil || len(res) != 0 || len(srv.requests()) != n+1 {
		t.Fatalf("unknown id: %+v %v", res, err)
	}
	// Free text keeps everything.
	if res, err := y.Search(ctx, "Dune"); err != nil || len(res) != 2 {
		t.Fatalf("free text: %d %v", len(res), err)
	}
	if _, err := y.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Dune"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("tv: %v", err)
	}
	if res, err := y.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Nothing"}); err != nil || len(res) != 0 {
		t.Fatalf("nothing: %v %v", res, err)
	}
}

func TestYTSErrors(t *testing.T) {
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query_term") {
		case "blocked":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(srcFixture(t, "cloudflare.html"))
		default:
			_, _ = w.Write([]byte(`{"status":"error","status_message":"Invalid parameter","data":[]}`))
		}
	})
	y := &YTS{Base: srv.URL, HTTP: srv.Client()}
	if _, err := y.Search(context.Background(), "blocked"); !errors.Is(err, ErrWebPage) {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := y.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "Invalid parameter") {
		t.Fatalf("status: %v", err)
	}
	if (&YTS{}).base() != "https://yts.mx" || (&YTS{Base: "https://yts.lt/api/v2/"}).base() != "https://yts.lt" {
		t.Fatal("base")
	}
}
