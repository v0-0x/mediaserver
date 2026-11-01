package torrent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestTorrentsCSV(t *testing.T) {
	answer := "torrentscsv_new.json"
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("q") {
		case "Night of the Living Dead 1968", "night of the living dead":
			_, _ = w.Write(srcFixture(t, answer))
		case "blocked":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(srcFixture(t, "cloudflare.html"))
		default:
			_, _ = w.Write([]byte(`{"torrents":[],"next":null}`))
		}
	})
	c := &TorrentsCSV{Base: srv.URL + "/service", HTTP: srv.Client()}
	ctx := context.Background()

	res, err := c.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Night of the Living Dead", Year: 1968})
	if err != nil {
		t.Fatal(err)
	}
	q := srv.requests()[0].URL.Query()
	if q.Get("q") != "Night of the Living Dead 1968" || q.Get("size") != "50" {
		t.Fatalf("query %v", q)
	}
	// The 1990 remake is another movie.
	if len(res) != 2 || res[0].Seeders != 44 || res[1].Resolution != "2160p" {
		t.Fatalf("%+v", res)
	}
	r := res[0]
	if r.Provider != "torrentscsv" || r.InfoHash != "6b5d2e1d63e8de4f6c3a5f1a1a3c1b0d6f8e9a07" || r.Leechers != 6 || r.DownloadCount != 1200 ||
		r.Size != 8123456789 || r.Date != "2019-02-12T19:33:20Z" || r.ReleaseGroup != "GRP" || !strings.HasPrefix(r.MagnetLink, "magnet:?xt=urn:btih:6b5d") ||
		!strings.HasPrefix(r.Link, srv.URL+"/search?q=") {
		t.Fatalf("first: %+v", r)
	}

	// The bare array of older servers.
	answer = "torrentscsv_old.json"
	res, err = c.Search(ctx, "night of the living dead")
	if err != nil || len(res) != 1 || res[0].Seeders != 44 {
		t.Fatalf("old: %+v %v", res, err)
	}
	if _, err := c.Search(ctx, "blocked"); !errors.Is(err, ErrWebPage) {
		t.Fatalf("blocked: %v", err)
	}
	if res, err := c.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Unknown Show", Season: 1}); err != nil || len(res) != 0 {
		t.Fatalf("nothing: %+v %v", res, err)
	}
	if got := srv.requests()[len(srv.requests())-1].URL.Query().Get("q"); got != "Unknown Show S01" {
		t.Fatalf("tv query %q", got)
	}
}
