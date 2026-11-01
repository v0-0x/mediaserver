package torrent

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestInternetArchive(t *testing.T) {
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/advancedsearch.php" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.Contains(strings.ToLower(r.URL.Query().Get("q")), "living") {
			_, _ = w.Write(srcFixture(t, "iarchive_search.json"))
			return
		}
		_, _ = w.Write([]byte(`{"responseHeader":{"status":0},"response":{"numFound":0,"start":0,"docs":[]}}`))
	})
	a := &InternetArchive{Base: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	res, err := a.SearchMedia(ctx, MediaQuery{Type: MediaMovie, Title: "Night of the Living Dead", Year: 1968})
	if err != nil {
		t.Fatal(err)
	}
	q := srv.requests()[0].URL.Query()
	if q.Get("q") != "title:(Night AND of AND the AND Living AND Dead) AND mediatype:(movies)" || q.Get("output") != "json" ||
		strings.Join(q["fl[]"], ",") != "identifier,title,year,downloads,btih,item_size" || q.Get("sort[]") != "downloads desc" {
		t.Fatalf("query %v", q)
	}
	// The 1990 trailer is of another year; the colorized copy has none.
	if len(res) != 2 {
		t.Fatalf("%+v", res)
	}
	r := res[0]
	if r.Provider != "archive" || r.Name != "Night of the Living Dead (1968)" || r.Link != srv.URL+"/details/night_of_the_living_dead" ||
		r.DownloadURL != srv.URL+"/download/night_of_the_living_dead/night_of_the_living_dead_archive.torrent" ||
		r.InfoHash != "9c5d2e1d63e8de4f6c3a5f1a1a3c1b0d6f8e9a10" || r.Seeders != 0 || r.DownloadCount != 2534211 || r.Size != 3456789012 {
		t.Fatalf("first: %+v", r)
	}
	mag, err := url.ParseQuery(strings.TrimPrefix(r.MagnetLink, "magnet:?"))
	if err != nil || mag.Get("ws") != srv.URL+"/download/" || !hasFold(mag["tr"], "http://bt1.archive.org:6969/announce") {
		t.Fatalf("magnet %s", r.MagnetLink)
	}
	if res[1].Size != 1234567890 || res[1].Name != "Night of the Living Dead - colorized" {
		t.Fatalf("second: %+v", res[1])
	}
	// The torrent client gets the .torrent, whose web seeds need no peer.
	if m, err := a.Magnet(ctx, r); err != nil || m != r.DownloadURL {
		t.Fatalf("magnet: %q %v", m, err)
	}

	// Free text: no year to filter by; the list title is read.
	res, err = a.Search(ctx, "night of the living dead")
	if err != nil || len(res) != 3 || res[1].Name != "Night of the Living Dead (1990 remake trailer)" || res[1].MagnetLink != "" {
		t.Fatalf("search: %+v %v", res, err)
	}
	if res, err := a.Search(ctx, "?!"); err != nil || len(res) != 0 {
		t.Fatalf("no words: %v %v", res, err)
	}
	if (&InternetArchive{}).base() != "https://archive.org" {
		t.Fatal("base")
	}
}

// Every source together, through SearchAll, against their fixtures.
func TestSearchAllSources(t *testing.T) {
	yts := ytsServer(t, map[string]string{"tt0133093": "yts_matrix.json"})
	tz := torznabServer(t, "torznab_caps.xml", map[string]string{"movie": "torznab_movie.xml"})
	sources := []Source{
		newTestTorznab(tz, "secret"),
		&YTS{Base: yts.URL, HTTP: yts.Client()},
		&EZTV{Base: "http://127.0.0.1:1", HTTP: yts.Client()}, // shows only: not asked
	}
	res, errs := SearchAll(context.Background(), sources, MediaQuery{Type: MediaMovie, Title: "The Matrix", Year: 1999, IMDbID: "tt0133093", Resolution: "2160"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(res) != 7 || res[0].Resolution != "2160p" || res[1].Resolution != "2160p" || res[2].Resolution == "2160p" {
		for _, r := range res {
			t.Log(r.Provider, r.Name, r.Seeders, r.Resolution)
		}
		t.Fatalf("%d results", len(res))
	}
	if res[0].Seeders != 300 || res[0].Provider != "yts" || res[1].Provider != "torznab-prowlarr" {
		t.Fatalf("order: %+v / %+v", res[0], res[1])
	}
}
