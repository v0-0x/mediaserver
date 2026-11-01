package torrent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// The info hashes of the .torrent fixtures, as computed by the Python
// script that wrote them (SHA-1 of the bencoded info dictionary).
const (
	sampleTorrentHash  = "03deeeffc3e025f3c77a1cc7ec3b1adaae70d08c"
	privateTorrentHash = "1963f03940a591b845b31d3221f4141eae00b019"
)

// torznabServer is a fake Prowlarr: caps answers t=caps, feeds answer the
// searches (by t), and /3/download, /7/download serve torrents.
func torznabServer(t *testing.T, caps string, feeds map[string]string) *srcRecorder {
	var srv *srcRecorder
	srv = newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		host := srv.URL
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			switch r.URL.Query().Get("link") {
			case "abc":
				_, _ = w.Write(srcFixture(t, "sample.torrent"))
			case "priv":
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(srcFixture(t, "private.torrent"))
			case "magnet":
				http.Redirect(w, r, "magnet:?xt=urn:btih:"+hashA+"&dn=Redirected", http.StatusFound)
			case "gone":
				w.WriteHeader(http.StatusNotFound)
			case "denied":
				_, _ = w.Write(srcFixture(t, "torznab_error.xml"))
			}
		case r.URL.Query().Get("apikey") != "secret":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(srcFixture(t, "torznab_error.xml"))
		case r.URL.Query().Get("t") == "caps":
			_, _ = w.Write(srcFixture(t, caps))
		default:
			name := feeds[r.URL.Query().Get("t")]
			if name == "" {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`))
				return
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(string(srcFixture(t, name)), "{{HOST}}", host)))
		}
	})
	return srv
}

func newTestTorznab(srv *srcRecorder, key string) *Torznab {
	return NewTorznab(config.TorznabIndexer{Name: "Prowlarr", URL: srv.URL + "/3/api", APIKey: key, Enabled: true}, srv.Client())
}

func queriesOf(srv *srcRecorder) []url.Values {
	var out []url.Values
	for _, r := range srv.requests() {
		out = append(out, r.URL.Query())
	}
	return out
}

func TestTorznabMovie(t *testing.T) {
	srv := torznabServer(t, "torznab_caps.xml", map[string]string{"movie": "torznab_movie.xml"})
	tz := newTestTorznab(srv, "secret")
	q := MediaQuery{Type: MediaMovie, Title: "The Matrix", Year: 1999, IMDbID: "tt0133093", TMDBID: 603}
	res, err := tz.SearchMedia(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	qs := queriesOf(srv)
	if len(qs) != 2 || qs[0].Get("t") != "caps" {
		t.Fatalf("requests: %v", qs)
	}
	s := qs[1]
	if s.Get("t") != "movie" || s.Get("imdbid") != "0133093" || s.Get("cat") != "2000" || s.Get("apikey") != "secret" || s.Get("q") != "" || s.Get("tmdbid") != "" {
		t.Fatalf("search: %v", s)
	}
	if srv.requests()[1].URL.Path != "/3/api" {
		t.Fatalf("path %s", srv.requests()[1].URL.Path)
	}
	// The Reloaded is another IMDb title.
	if len(res) != 3 {
		t.Fatalf("%d results: %+v", len(res), res)
	}
	r := res[0]
	if r.Provider != "torznab-prowlarr" || r.Name != "The.Matrix.1999.1080p.BluRay.x264-SPARKS" || r.Seeders != 150 || r.Leechers != 20 ||
		r.Size != 10234567890 || r.DownloadCount != 4242 || r.InfoHash != "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678" ||
		!strings.HasPrefix(r.MagnetLink, "magnet:?xt=urn:btih:A1B2") || r.Link != "https://1337x.example/torrent/1111/The-Matrix-1999-1080p/" ||
		!strings.HasPrefix(r.DownloadURL, srv.URL+"/3/download?") || r.Date != "2015-03-14T09:26:53Z" ||
		r.Resolution != "1080p" || r.ReleaseGroup != "SPARKS" || r.EpisodeNumber != -1 || r.FormattedSize != "9.5 GiB" {
		t.Fatalf("first: %+v", r)
	}
	r = res[1]
	if r.MagnetLink != "" || r.InfoHash != "" || r.Seeders != 35 || r.Leechers != 1 || r.Resolution != "2160p" || r.Link != "https://private.example.net/torrents.php?id=987" {
		t.Fatalf("private: %+v", r)
	}
	r = res[2]
	if !strings.HasPrefix(r.MagnetLink, "magnet:") || r.InfoHash != "c1b2c3d4e5f60718293a4b5c6d7e8f9012345678" || r.DownloadURL != "" ||
		r.Size != 733000000 || r.Link != "https://tpb.example/description.php?id=5555" || r.ReleaseGroup != "YIFY" || r.Date != "2012-01-03T10:00:00Z" {
		t.Fatalf("magnet enclosure: %+v", r)
	}

	// The capabilities are remembered.
	if _, err := tz.SearchMedia(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	caps := 0
	for _, v := range queriesOf(srv) {
		if v.Get("t") == "caps" {
			caps++
		}
	}
	if caps != 1 {
		t.Fatalf("caps asked %d times", caps)
	}
}

func TestTorznabTV(t *testing.T) {
	srv := torznabServer(t, "torznab_caps.xml", map[string]string{"tvsearch": "torznab_tv.xml"})
	tz := newTestTorznab(srv, "secret")
	ctx := context.Background()

	res, err := tz.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", IMDbID: "tt0903747", TVDBID: 81189, Season: 1, Episode: 2})
	if err != nil || len(res) != 3 {
		t.Fatalf("%d results, %v", len(res), err)
	}
	s := queriesOf(srv)[1]
	if s.Get("t") != "tvsearch" || s.Get("imdbid") != "0903747" || s.Get("tvdbid") != "" || s.Get("season") != "1" || s.Get("ep") != "2" || s.Get("cat") != "5000" {
		t.Fatalf("search: %v", s)
	}
	if res[0].EpisodeNumber != 1002 || res[0].IsBatch || res[1].EpisodeNumber != -1 || !res[1].IsBatch {
		t.Fatalf("episodes: %+v / %+v", res[0], res[1])
	}
	// No magnet nor enclosure: the link is the download.
	if res[2].DownloadURL == "" || res[2].Link != "https://eztvx.to/ep/102/" {
		t.Fatalf("link only: %+v", res[2])
	}

	// A season pack: no ep; without an IMDb id, the TVDB id.
	if _, err := tz.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", TVDBID: 81189, Season: 3}); err != nil {
		t.Fatal(err)
	}
	qs := queriesOf(srv)
	s = qs[len(qs)-1]
	if s.Get("tvdbid") != "81189" || s.Get("season") != "3" || s.Has("ep") || s.Has("q") {
		t.Fatalf("season search: %v", s)
	}

	// Free text overrides.
	if _, err := tz.SearchMedia(ctx, MediaQuery{Type: MediaTV, Title: "Breaking Bad", Query: "breaking bad french", Season: 1}); err != nil {
		t.Fatal(err)
	}
	qs = queriesOf(srv)
	s = qs[len(qs)-1]
	if s.Get("t") != "search" || s.Get("q") != "breaking bad french" || s.Get("cat") != "5000" || s.Has("season") {
		t.Fatalf("free text: %v", s)
	}
}

func TestTorznabFallsBackOnText(t *testing.T) {
	// A feed that only searches text.
	srv := torznabServer(t, "torznab_caps_basic.xml", map[string]string{"search": "torznab_movie.xml"})
	tz := newTestTorznab(srv, "secret")
	res, err := tz.SearchMedia(context.Background(), MediaQuery{Type: MediaMovie, Title: "The Matrix", Year: 1999, IMDbID: "tt0133093"})
	if err != nil || len(res) != 3 {
		t.Fatalf("%d results, %v", len(res), err)
	}
	s := queriesOf(srv)[1]
	if s.Get("t") != "search" || s.Get("q") != "The Matrix 1999" || s.Get("cat") != "2000" {
		t.Fatalf("search: %v", s)
	}

	// A feed that searches by id but finds nothing that way.
	srv = torznabServer(t, "torznab_caps.xml", map[string]string{"search": "torznab_tv.xml"})
	tz = newTestTorznab(srv, "secret")
	res, err = tz.SearchMedia(context.Background(), MediaQuery{Type: MediaTV, Title: "Breaking Bad", IMDbID: "tt0903747", Season: 1, Episode: 2})
	if err != nil || len(res) != 3 {
		t.Fatalf("%d results, %v", len(res), err)
	}
	qs := queriesOf(srv)
	if len(qs) != 3 || qs[1].Get("t") != "tvsearch" || qs[2].Get("t") != "search" || qs[2].Get("q") != "Breaking Bad S01E02" {
		t.Fatalf("requests: %v", qs)
	}
}

func TestTorznabErrors(t *testing.T) {
	srv := torznabServer(t, "torznab_caps.xml", nil)
	tz := newTestTorznab(srv, "wrong")
	_, err := tz.SearchMedia(context.Background(), MediaQuery{Type: MediaMovie, Title: "The Matrix", IMDbID: "tt0133093"})
	var te *TorznabError
	if !errors.As(err, &te) || !te.IsAuth() || te.Code != 100 || !strings.Contains(err.Error(), "refused the API key (Invalid API Key)") {
		t.Fatalf("bad key: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the key shows: %v", err)
	}
	// Searches of free text don't need the capabilities.
	if _, err := tz.Search(context.Background(), "x"); !errors.As(err, &te) {
		t.Fatalf("search: %v", err)
	}

	page := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(srcFixture(t, "cloudflare.html"))
	})
	tz = NewTorznab(config.TorznabIndexer{URL: page.URL + "/", APIKey: "k"}, page.Client())
	_, err = tz.SearchMedia(context.Background(), MediaQuery{Type: MediaMovie, Title: "The Matrix"})
	if !errors.Is(err, ErrWebPage) || !strings.Contains(err.Error(), "end in /api") {
		t.Fatalf("web page: %v", err)
	}
	if page.requests()[0].URL.Path != "/api" {
		t.Fatalf("path: %s", page.requests()[0].URL.Path)
	}

	for code, want := range map[int]string{500: "request limit", 910: "disabled", 201: "error 201: Incorrect parameter"} {
		e := &TorznabError{Code: code, Description: "Incorrect parameter"}
		if !strings.Contains(e.Error(), want) {
			t.Errorf("%d: %v", code, e)
		}
	}
	if torznabErrorIn([]byte(`<?xml version="1.0"?><rss><channel><description>error</description></channel></rss>`)) != nil {
		t.Error("a feed taken for an error")
	}
}

func TestNewTorznab(t *testing.T) {
	for _, tc := range []struct {
		url, key, endpoint, apiKey, name, id string
	}{
		{"http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab/", "k", "http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab/api", "k", "Torznab (127.0.0.1:9117)", "torznab-127-0-0-1-9117"},
		{"localhost:9696/1/api?apikey=fromurl&extra=1", "", "http://localhost:9696/1/api", "fromurl", "Torznab (localhost:9696)", "torznab-localhost-9696"},
		{"https://indexer.example/api.php", "k2", "https://indexer.example/api.php", "k2", "Torznab (indexer.example)", "torznab-indexer-example"},
	} {
		tz := NewTorznab(config.TorznabIndexer{URL: tc.url, APIKey: tc.key}, nil)
		if tz.endpoint != tc.endpoint || tz.apiKey != tc.apiKey || tz.Name() != tc.name || tz.ID() != tc.id {
			t.Errorf("%s: %q %q %q %q", tc.url, tz.endpoint, tz.apiKey, tz.Name(), tz.ID())
		}
	}
	tz := NewTorznab(config.TorznabIndexer{URL: "localhost:9696/1/api?apikey=fromurl&extra=1"}, nil)
	if u := tz.address(url.Values{"t": {"caps"}}); u != "http://localhost:9696/1/api?apikey=fromurl&extra=1&t=caps" {
		t.Errorf("address %s", u)
	}
	if tz.client() != torznabHTTP {
		t.Error("default client")
	}
}

func TestTorznabMagnet(t *testing.T) {
	srv := torznabServer(t, "torznab_caps.xml", nil)
	tz := newTestTorznab(srv, "secret")
	ctx := context.Background()
	dl := func(link string) string { return srv.URL + "/3/download?apikey=secret&link=" + link }

	// The feed's magnet.
	if m, err := tz.Magnet(ctx, &SearchResult{MagnetLink: "magnet:?xt=urn:btih:" + hashA, DownloadURL: dl("abc")}); err != nil || m != "magnet:?xt=urn:btih:"+hashA {
		t.Fatalf("%q %v", m, err)
	}
	// From the .torrent, with its trackers and the usual ones.
	m, err := tz.Magnet(ctx, &SearchResult{Name: "x", DownloadURL: dl("abc"), InfoHash: hashB})
	if err != nil || !strings.HasPrefix(m, "magnet:?xt=urn:btih:"+sampleTorrentHash+"&") ||
		!strings.Contains(m, "dn=Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP") ||
		!strings.Contains(m, url.QueryEscape("https://tracker.example.org/announce")) || !strings.Contains(m, url.QueryEscape(Trackers[0])) ||
		strings.Count(m, url.QueryEscape("udp://tracker.opentrackr.org:1337/announce")) != 1 {
		t.Fatalf("torrent: %q %v", m, err)
	}
	// A private torrent keeps its own tracker only.
	m, err = tz.Magnet(ctx, &SearchResult{DownloadURL: dl("priv")})
	if err != nil || !strings.HasPrefix(m, "magnet:?xt=urn:btih:"+privateTorrentHash+"&") ||
		!strings.Contains(m, url.QueryEscape("https://private.example.net/announce/0123456789abcdef0123456789abcdef")) || strings.Contains(m, "opentrackr") {
		t.Fatalf("private: %q %v", m, err)
	}
	// Indexers that only have a magnet redirect to it.
	if m, err := tz.Magnet(ctx, &SearchResult{DownloadURL: dl("magnet")}); err != nil || m != "magnet:?xt=urn:btih:"+hashA+"&dn=Redirected" {
		t.Fatalf("redirect: %q %v", m, err)
	}
	// A failed download falls back on the info hash, or fails.
	if m, err := tz.Magnet(ctx, &SearchResult{Name: "x", DownloadURL: dl("gone"), InfoHash: strings.ToUpper(hashC)}); err != nil || !strings.HasPrefix(m, "magnet:?xt=urn:btih:"+hashC) {
		t.Fatalf("fallback: %q %v", m, err)
	}
	if _, err := tz.Magnet(ctx, &SearchResult{DownloadURL: dl("gone")}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("gone: %v", err)
	}
	if _, err := tz.Magnet(ctx, &SearchResult{DownloadURL: dl("denied")}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("denied: %v", err)
	}
	// Other hosts' addresses go to the torrent client as they are.
	before := len(srv.requests())
	if m, err := tz.Magnet(ctx, &SearchResult{DownloadURL: "http://192.168.1.10/secret.torrent"}); err != nil || m != "http://192.168.1.10/secret.torrent" {
		t.Fatalf("other host: %q %v", m, err)
	}
	if len(srv.requests()) != before {
		t.Fatal("fetched another host's address")
	}
	if _, err := tz.Magnet(ctx, &SearchResult{}); err == nil {
		t.Fatal("nothing to download")
	}
}

func TestBencode(t *testing.T) {
	meta, err := readTorrent(srcFixture(t, "sample.torrent"))
	if err != nil || meta.InfoHash != sampleTorrentHash || meta.Name != "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP" || meta.Private ||
		len(meta.Trackers) != 3 || meta.Trackers[0] != "udp://tracker.opentrackr.org:1337/announce" || meta.Trackers[1] != "https://tracker.example.org/announce" {
		t.Fatalf("%+v %v", meta, err)
	}
	meta, err = readTorrent(srcFixture(t, "private.torrent"))
	if err != nil || meta.InfoHash != privateTorrentHash || !meta.Private || len(meta.Trackers) != 1 {
		t.Fatalf("%+v %v", meta, err)
	}

	v, err := bdecode([]byte("d3:cow3:moo4:spaml1:a1:be1:ni-42ee"))
	if err != nil || v.kind != 'd' || string(v.dict["cow"].str) != "moo" || len(v.dict["spam"].list) != 2 || v.dict["n"].num != -42 || v.dict[""].kind != 0 {
		t.Fatalf("%+v %v", v, err)
	}
	if v, _ := bdecode([]byte("i-42e")); v.num != -42 {
		t.Fatalf("int %+v", v)
	}
	for _, bad := range []string{
		"", "i42", "ie", "i4x2e", "5:abc", "-1:a", "99999999999:a", "l", "li1e", "d3:cowe", "di1ei2ee", "x",
		strings.Repeat("l", 100) + strings.Repeat("e", 100),
	} {
		if _, err := bdecode([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	for _, bad := range []string{"le", "d4:infoi1ee", "d4:infol1:aee", "<html>"} {
		if _, err := readTorrent([]byte(bad)); err == nil {
			t.Errorf("%q: read as a torrent", bad)
		}
	}
	if _, err := readTorrent([]byte("d4:infod12:meta versioni2e4:name1:x9:file treedeee")); !errors.Is(err, errV2Only) {
		t.Errorf("v2: %v", err)
	}
}

// The indexer may be on the LAN and send the .torrent from elsewhere, but
// not redirect its download to another address on this computer or the
// LAN.
func TestTorznabTorrentRedirectStaysOffTheLAN(t *testing.T) {
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.1.50:8080/admin.torrent", http.StatusFound)
	})
	tz := newTestTorznab(srv, "secret")
	if _, err := tz.torrentMagnet(context.Background(), srv.URL+"/dl/1"); err == nil || !strings.Contains(err.Error(), "local address") {
		t.Fatalf("redirected to the LAN: %v", err)
	}
	for host, local := range map[string]bool{"192.168.1.50": true, "localhost": true, "nas": true, "box.lan": true, "[fe80::1%eth0]": true, "tracker.example.org": false, "1.1.1.1": false} {
		if got := util.IsLocalHostname(strings.Trim(host, "[]")); got != local {
			t.Errorf("IsLocalHostname(%q) = %v", host, got)
		}
	}
}
