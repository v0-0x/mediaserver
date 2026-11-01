package archive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fakeArchive serves the search and metadata fixtures of testdata/.
type fakeArchive struct {
	mu      sync.Mutex
	queries []string
}

func (f *fakeArchive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/advancedsearch.php":
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.RawQuery)
		f.mu.Unlock()
		if !strings.Contains(r.URL.Query().Get("q"), "Night of the Living Dead") {
			_, _ = w.Write([]byte(`{"response":{"numFound":0,"docs":[]}}`))
			return
		}
		http.ServeFile(w, r, filepath.Join("testdata", "search.json"))
	case strings.HasPrefix(r.URL.Path, "/metadata/"):
		id := strings.TrimPrefix(r.URL.Path, "/metadata/")
		p := filepath.Join("testdata", "metadata_"+id+".json")
		if _, err := os.Stat(p); err != nil {
			// archive.org answers {} for unknown items.
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.ServeFile(w, r, p)
	default:
		http.NotFound(w, r)
	}
}

func newTestProvider(t *testing.T) (*Provider, *fakeArchive) {
	t.Helper()
	fa := &fakeArchive{}
	srv := httptest.NewServer(fa)
	t.Cleanup(srv.Close)
	p := New()
	p.BaseURL, p.HTTP = srv.URL, srv.Client()
	return p, fa
}

func TestSearchRanksMatches(t *testing.T) {
	p, fa := newTestProvider(t)
	items, err := p.Search(context.Background(), "Night of the Living Dead", 1968)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.Identifier)
	}
	want := "night_of_the_living_dead|random_upload_notld|no_year_notld"
	if strings.Join(ids, "|") != want {
		t.Errorf("items = %v, want %s", ids, want)
	}
	if len(fa.queries) != 1 {
		t.Fatalf("queries = %v", fa.queries)
	}
	q := fa.queries[0]
	for _, part := range []string{
		"q=title%3A%28%22Night+of+the+Living+Dead%22%29+AND+mediatype%3A%28movies%29",
		"fl%5B%5D=identifier", "fl%5B%5D=title", "fl%5B%5D=year", "fl%5B%5D=downloads",
		"rows=20", "output=json",
	} {
		if !strings.Contains(q, part) {
			t.Errorf("query %q lacks %q", q, part)
		}
	}
}

func TestStreams(t *testing.T) {
	p, _ := newTestProvider(t)
	if p.Supports(providers.TypeTV) || !p.Supports(providers.TypeMovie) {
		t.Error("the archive provider is for movies only")
	}
	streams, err := p.Streams(context.Background(), providers.Request{
		Type: providers.TypeMovie, Title: "Night of the Living Dead", Year: 1968, TMDBID: 10331,
	})
	if err != nil {
		t.Fatal(err)
	}
	type want struct{ file, typ, quality string }
	wants := []want{
		{"night_of_the_living_dead.ia.mp4", providers.StreamMP4, "480p"},
		{"Night%20of%20the%20Living%20Dead%20%231.mkv", providers.StreamMKV, "1080p"},
		{"night_of_the_living_dead_512kb.mp4", providers.StreamMP4, "240p"},
		{"night_of_the_living_dead.ogv", providers.StreamUnknown, ""},
		{"Night.of.the.Living.Dead.1968.720p.webm", providers.StreamWebM, "720p"},
	}
	if len(streams) != len(wants) {
		for _, s := range streams {
			t.Logf("%s", s.URL)
		}
		t.Fatalf("got %d streams, want %d", len(streams), len(wants))
	}
	for i, w := range wants {
		s := streams[i]
		if !strings.HasSuffix(s.URL, "/"+w.file) || s.Type != w.typ || s.Quality != w.quality {
			t.Errorf("stream %d = %s %s %q, want %s %s %q", i, s.URL, s.Type, s.Quality, w.file, w.typ, w.quality)
		}
		if s.Provider != ID || s.ProviderName != Name || s.FileIdx != -1 {
			t.Errorf("stream %d = %+v", i, s)
		}
	}
	first := streams[0]
	if !strings.HasPrefix(first.URL, p.BaseURL+"/download/night_of_the_living_dead/") {
		t.Errorf("URL = %s", first.URL)
	}
	if first.Size != 1006632960 || first.NotWebReady || !strings.Contains(first.Title, "Night of the Living Dead (1968)") {
		t.Errorf("first = %+v", first)
	}
	if !strings.Contains(streams[1].URL, "/download/night_of_the_living_dead/HD/") || !streams[1].NotWebReady {
		t.Errorf("mkv = %+v", streams[1])
	}
	if len(first.Subtitles) != 2 || first.Subtitles[0].Language != "en" || first.Subtitles[1].Language != "fre" ||
		!strings.HasSuffix(first.Subtitles[0].URL, "/night_of_the_living_dead.en.srt") {
		t.Errorf("subtitles = %+v", first.Subtitles)
	}
	for _, s := range streams {
		if strings.Contains(s.URL, "trailer") || strings.Contains(s.URL, "notld.mp4") {
			t.Errorf("unexpected stream %s", s.URL)
		}
	}

	// Nothing for shows, and nothing found is no error.
	if s, err := p.Streams(context.Background(), providers.Request{Type: providers.TypeTV, Title: "Night of the Living Dead"}); err != nil || s != nil {
		t.Errorf("tv = %v, %v", s, err)
	}
	if s, err := p.Streams(context.Background(), providers.Request{Type: providers.TypeMovie, Title: "Unknown Film", Year: 2020}); err != nil || len(s) != 0 {
		t.Errorf("unknown = %v, %v", s, err)
	}
}

func TestOriginalTitleFallback(t *testing.T) {
	p, fa := newTestProvider(t)
	streams, err := p.Streams(context.Background(), providers.Request{
		Type: providers.TypeMovie, Title: "La Nuit des morts-vivants", OriginalTitle: "Night of the Living Dead", Year: 1968,
	})
	if err != nil || len(streams) == 0 {
		t.Fatalf("streams = %v, %v", streams, err)
	}
	if len(fa.queries) != 2 {
		t.Errorf("queries = %v", fa.queries)
	}
}

func TestRestrictedItem(t *testing.T) {
	p, _ := newTestProvider(t)
	if _, err := p.ItemStreams(context.Background(), "random_upload_notld"); err == nil || !strings.Contains(err.Error(), "borrowed") {
		t.Errorf("err = %v", err)
	}
	if _, err := p.ItemStreams(context.Background(), "missing"); err == nil {
		t.Error("no error for a missing item")
	}
	if _, err := p.ItemStreams(context.Background(), "../etc"); err == nil {
		t.Error("no error for an invalid identifier")
	}
}

func TestTorrent(t *testing.T) {
	p, _ := newTestProvider(t)
	btih, u, err := p.Torrent(context.Background(), "night_of_the_living_dead")
	if err != nil {
		t.Fatal(err)
	}
	if btih != "c8295ce630f2064f08440db1534e4992cfe4862a" {
		t.Errorf("btih = %q", btih)
	}
	if u != p.BaseURL+"/download/night_of_the_living_dead/night_of_the_living_dead_archive.torrent" {
		t.Errorf("torrent URL = %q", u)
	}
	if _, _, err := p.Torrent(context.Background(), "no_year_notld"); err == nil {
		t.Error("no error for an item without torrent")
	}
}

func TestSubtitleLanguage(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Film.en.srt", "en"},
		{"Film_eng.srt", "eng"},
		{"Film - English.vtt", "en"},
		{"English.srt", "en"},
		{"Film_The_End.srt", ""},
		{"Film.srt", ""},
	} {
		if got := subtitleLanguage(tc.in); got != tc.want {
			t.Errorf("subtitleLanguage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
