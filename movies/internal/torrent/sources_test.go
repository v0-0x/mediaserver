package torrent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// srcFixture reads a file of testdata.
func srcFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// srcRecorder is a test server that answers with handler and remembers the
// requests it got.
type srcRecorder struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newSrcRecorder(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *srcRecorder {
	t.Helper()
	rec := &srcRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r)
		rec.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (rec *srcRecorder) requests() []*http.Request {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]*http.Request(nil), rec.reqs...)
}

// fakeSource is a Source that answers what it's told.
type fakeSource struct {
	id      string
	types   []string // "" (free text), MediaMovie, MediaTV
	res     []*SearchResult
	err     error
	block   bool // waits for its context to end
	stuck   bool // ignores its context
	mu      sync.Mutex
	queries []MediaQuery
}

func (f *fakeSource) ID() string   { return f.id }
func (f *fakeSource) Name() string { return strings.ToUpper(f.id) }
func (f *fakeSource) Supports(t string) bool {
	for _, x := range f.types {
		if x == t {
			return true
		}
	}
	return false
}
func (f *fakeSource) Search(ctx context.Context, q string) ([]*SearchResult, error) {
	return f.SearchMedia(ctx, MediaQuery{Query: q})
}
func (f *fakeSource) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.stuck {
		time.Sleep(3 * time.Second)
	}
	// Fresh copies: SearchAll changes the results it merges.
	var out []*SearchResult
	for _, r := range f.res {
		c := *r
		out = append(out, &c)
	}
	return out, f.err
}
func (f *fakeSource) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "magnet:?from=" + f.id, nil
}

const (
	hashA = "0123456789abcdef0123456789abcdef01234567"
	hashB = "1123456789abcdef0123456789abcdef01234567"
	hashC = "2123456789abcdef0123456789abcdef01234567"
	hashD = "3123456789abcdef0123456789abcdef01234567"
)

func TestCheckName(t *testing.T) {
	for _, tc := range []struct {
		name           string
		season, ep, to int
		pack           bool
		from, upto     int
		res, group     string
		years          []int
	}{
		{"Breaking.Bad.S05E16.1080p.BluRay.x264-ROVERS[eztv].mkv", 5, 16, 16, false, 0, 0, "1080p", "ROVERS", nil},
		{"Breaking Bad S01E02 720p HDTV x264-CTU", 1, 2, 2, false, 0, 0, "720p", "CTU", nil},
		{"The.Office.US.S02E01E02.720p.WEB-DL.DD5.1.H.264-NTb", 2, 1, 2, true, 2, 2, "720p", "NTb", nil},
		{"Show.S03E01-E04.1080p.WEB.h264-GRP", 3, 1, 4, true, 3, 3, "1080p", "GRP", nil},
		{"Show S03E01-04 1080p", 3, 1, 4, true, 3, 3, "1080p", "", nil},
		{"Doctor Who 2005 S00E03 The Runaway Bride 720p", 0, 3, 3, false, 0, 0, "720p", "", []int{2005}},
		{"Friends 1x02 The One with the Sonogram", 1, 2, 2, false, 0, 0, "", "", nil},
		{"Friends Season 10 Episode 17 The Last One", 10, 17, 17, false, 0, 0, "", "", nil},
		{"Breaking.Bad.S01.1080p.BluRay.x265-GRP", -1, -1, -1, true, 1, 1, "1080p", "GRP", nil},
		{"Breaking Bad Season 1 Complete 1080p BluRay x265 10bit-GRP", -1, -1, -1, true, 1, 1, "1080p", "GRP", nil},
		{"Breaking Bad Season 1-5 Complete 1080p BluRay x265 HEVC-GRP", -1, -1, -1, true, 1, 5, "1080p", "GRP", nil},
		{"The.Sopranos.S01-S06.COMPLETE.720p.BluRay.x264", -1, -1, -1, true, 1, 6, "720p", "", nil},
		{"Seinfeld - The Complete Series (1989-1998) [1080p]", -1, -1, -1, true, 0, 0, "1080p", "", []int{1989, 1998}},
		{"The.Matrix.1999.1080p.BluRay.x264-SPARKS", -1, -1, -1, false, 0, 0, "1080p", "SPARKS", []int{1999}},
		{"The Matrix (1999) [2160p] [WEB] [x265] [YTS]", -1, -1, -1, false, 0, 0, "2160p", "", []int{1999}},
		{"Blade.Runner.2049.2017.UHD.BluRay.x265-TERMINAL", -1, -1, -1, false, 0, 0, "2160p", "TERMINAL", []int{2017}},
		{"1917.2019.1080p.WEB-DL.DD5.1.H264-FGT", -1, -1, -1, false, 0, 0, "1080p", "FGT", []int{2019}},
		{"2001 A Space Odyssey 1968 4K", -1, -1, -1, false, 0, 0, "2160p", "", []int{1968}},
		{"Spider-Man (2002) [1080p] [YTS]", -1, -1, -1, false, 0, 0, "1080p", "", []int{2002}},
		{"Ocean's Eleven 2001 720p BrRip x264 - YIFY", -1, -1, -1, false, 0, 0, "720p", "YIFY", []int{2001}},
		{"Movie.2019.1080p.WEB-DL", -1, -1, -1, false, 0, 0, "1080p", "", []int{2019}},
		{"[SubsPlease] Show - 05 (1080p) [ABCD1234].mkv", -1, -1, -1, false, 0, 0, "1080p", "SubsPlease", nil},
	} {
		f := checkName(tc.name)
		if f.Season != tc.season || f.Episode != tc.ep || f.EpisodeTo != tc.to || f.Pack != tc.pack ||
			f.SeasonFrom != tc.from || f.SeasonTo != tc.upto || f.Resolution != tc.res || f.Group != tc.group ||
			!reflect.DeepEqual(f.Years, tc.years) {
			t.Errorf("%q: got %+v", tc.name, f)
		}
	}
}

func TestAnnotate(t *testing.T) {
	r := &SearchResult{Name: "Breaking.Bad.S02E05.720p.HDTV.x264-CTU", Size: 1 << 30}
	annotate(r)
	if r.EpisodeNumber != 2005 || r.IsBatch || r.Resolution != "720p" || r.ReleaseGroup != "CTU" || r.FormattedSize != "1.0 GiB" {
		t.Fatalf("episode: %+v", r)
	}
	r = &SearchResult{Name: "Breaking Bad S02 720p", EpisodeNumber: -1}
	annotate(r)
	if r.EpisodeNumber != -1 || !r.IsBatch {
		t.Fatalf("season pack: %+v", r)
	}
	// A key the source knew stays, unless the name says it's a pack.
	r = &SearchResult{Name: "Breaking Bad - Cat's in the Bag", EpisodeNumber: 1002}
	annotate(r)
	if r.EpisodeNumber != 1002 || r.IsBatch {
		t.Fatalf("source key: %+v", r)
	}
	r = &SearchResult{Name: "Show S01E01-E03", EpisodeNumber: 1001}
	annotate(r)
	if r.EpisodeNumber != -1 || !r.IsBatch {
		t.Fatalf("range: %+v", r)
	}
	r = &SearchResult{Name: "The Matrix 1999", Resolution: "2160p", ReleaseGroup: "YTS"}
	annotate(r)
	if r.EpisodeNumber != -1 || r.Resolution != "2160p" || r.ReleaseGroup != "YTS" {
		t.Fatalf("movie: %+v", r)
	}
}

func TestMatchesMedia(t *testing.T) {
	ep := MediaQuery{Type: MediaTV, Title: "Breaking Bad", Season: 1, Episode: 2}
	season := MediaQuery{Type: MediaTV, Title: "Breaking Bad", Season: 1}
	show := MediaQuery{Type: MediaTV, Title: "Breaking Bad"}
	special := MediaQuery{Type: MediaTV, Title: "Doctor Who", Season: 0, Episode: 3}
	movie := MediaQuery{Type: MediaMovie, Title: "The Matrix", Year: 1999}
	br := MediaQuery{Type: MediaMovie, Title: "Blade Runner 2049", Year: 2017}
	for _, tc := range []struct {
		q    MediaQuery
		name string
		key  int
		want bool
	}{
		{ep, "Breaking.Bad.S01E02.720p", -1, true},
		{ep, "Breaking.Bad.S01E03.720p", -1, false},
		{ep, "Breaking.Bad.S02E02.720p", -1, false},
		{ep, "Breaking Bad 1x02", -1, true},
		{ep, "Breaking.Bad.S01E01-E03.720p", -1, true},
		{ep, "Breaking.Bad.S01E03-E05.720p", -1, false},
		{ep, "Breaking.Bad.S01.1080p", -1, true},
		{ep, "Breaking.Bad.S02.1080p", -1, false},
		{ep, "Breaking Bad Seasons 1-5 Complete", -1, true},
		{ep, "Breaking Bad Complete Series", -1, true},
		{ep, "Breaking Bad 720p", -1, true}, // says nothing
		{ep, "Breaking Bad - Cat's in the Bag", 1002, true},
		{ep, "Breaking Bad - And the Bag's in the River", 1003, false},
		{season, "Breaking.Bad.S01.1080p", -1, true},
		{season, "Breaking.Bad.S01E02.1080p", -1, false},
		{season, "Breaking.Bad.S01E01-E07.1080p", -1, true},
		{season, "Breaking.Bad.S02.1080p", -1, false},
		{season, "Breaking Bad S01-S05 1080p", -1, true},
		{show, "Breaking Bad S01-S05 1080p", -1, true},
		{show, "Breaking.Bad.S03.1080p", -1, true},
		{show, "Breaking.Bad.S03E01.1080p", -1, false},
		{special, "Doctor.Who.2005.S00E03.720p", -1, true},
		{special, "Doctor.Who.2005.S01E03.720p", -1, false},
		{special, "Doctor.Who.2005.S01.720p", -1, false},
		{movie, "The.Matrix.1999.1080p", -1, true},
		{movie, "The Matrix (2000) 1080p", -1, true}, // released a year later somewhere
		{movie, "The.Matrix.Resurrections.2021.1080p", -1, false},
		{movie, "The Matrix 1080p", -1, true},
		{movie, "The.Matrix.S01E01.1080p", -1, false},
		{br, "Blade.Runner.2049.2017.1080p", -1, true},
		{br, "Blade.Runner.2049.1080p", -1, true},
		{br, "Blade.Runner.1982.Final.Cut.1080p", -1, false},
		{MediaQuery{Type: MediaMovie, Title: "1917", Year: 2019}, "1917.1080p.BluRay", -1, true},
		{MediaQuery{Query: "anything"}, "Some.Show.S01E01", -1, true},
	} {
		r := &SearchResult{Name: tc.name, EpisodeNumber: tc.key}
		if got := matchesMedia(r, tc.q.normalized()); got != tc.want {
			t.Errorf("%+v / %q: %v, want %v", tc.q, tc.name, got, tc.want)
		}
	}
}

func TestNormalizeIMDbAndHashes(t *testing.T) {
	for in, want := range map[string]string{
		"tt0133093": "tt0133093", "0133093": "tt0133093", "133093": "tt0133093", "TT0903747": "tt0903747",
		"https://www.imdb.com/title/tt10872600/": "tt10872600", "": "", "tt": "", "nm0000206x": "",
	} {
		if got := normalizeIMDb(in); got != want {
			t.Errorf("normalizeIMDb(%q) = %q, want %q", in, got, want)
		}
	}
	if imdbDigits("tt0133093") != "0133093" || !sameIMDb("0133093", "tt0133093") || sameIMDb("", "") {
		t.Error("imdbDigits/sameIMDb")
	}
	for in, want := range map[string]string{
		"A1B2C3D4E5F60718293A4B5C6D7E8F9012345678": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
		"0000000000000000000000000000000000000000": "",
		"MFRGGZDFMZTWQ2LKNNWG23TPOBYXE43U":         "6162636465666768696a6b6c6d6e6f7071727374", // base32
		"xyz":                                      "",
	} {
		if got := canonicalHash(in); got != want {
			t.Errorf("canonicalHash(%q) = %q, want %q", in, got, want)
		}
	}
	if h := hashFromMagnet("magnet:?xt=urn:btih:MFRGGZDFMZTWQ2LKNNWG23TPOBYXE43U&dn=x"); h != "6162636465666768696a6b6c6d6e6f7071727374" {
		t.Errorf("hashFromMagnet: %q", h)
	}
	if h := hashFromMagnet("https://example.org/?xt=urn:btih:" + hashA); h != "" {
		t.Errorf("not a magnet: %q", h)
	}
	m := magnetWith(hashA, "Movie", []string{Trackers[1], "udp://extra.example:80/announce"})
	if strings.Count(m, "open.stealth.si") != 1 || !strings.Contains(m, "&tr=udp%3A%2F%2Fextra.example%3A80%2Fannounce") || !strings.HasPrefix(m, "magnet:?xt=urn:btih:"+hashA+"&") {
		t.Errorf("magnetWith: %s", m)
	}
}

func TestSearchAllMergesFiltersAndSorts(t *testing.T) {
	a := &fakeSource{id: "a", types: []string{MediaTV, MediaMovie, ""}, res: []*SearchResult{
		{Name: "Show.S01E02.720p.HDTV.x264-CTU", InfoHash: strings.ToUpper(hashA), Seeders: 10, Size: 100},
		{Name: "Show.S01E03.1080p.WEB.h264-GRP", InfoHash: hashB, Seeders: 500},                      // another episode
		{Name: "Show.S01.1080p.BluRay.x265-GRP", InfoHash: hashC, Seeders: 50},                       // the season
		{Name: "Show.S01E02.1080p.WEB.h264-NTb", InfoHash: hashD, Seeders: 0},                        // dead
		{Name: "Show.S02.720p.BluRay.x264-DEMAND", MagnetLink: "magnet:?xt=urn:btih:" + hashD + "x"}, // another season
	}}
	b := &fakeSource{id: "b", types: []string{MediaTV}, res: []*SearchResult{
		// The same torrent as a's first, better seeded here, with a magnet.
		{Name: "Show S01E02 720p HDTV x264-CTU", MagnetLink: "magnet:?xt=urn:btih:" + hashA, Seeders: 40, Date: "2024-01-01T00:00:00Z"},
		{Name: "Show.S01E02.1080p.WEB.h264-ETHEL", DownloadURL: "https://indexer.example/dl/1", Seeders: 20, Resolution: "1080p"},
	}}
	movies := &fakeSource{id: "movies", types: []string{MediaMovie}, res: []*SearchResult{{Name: "x", InfoHash: hashC}}}
	failing := &fakeSource{id: "down", types: []string{MediaTV}, err: errors.New("boom")}
	unsupported := &fakeSource{id: "nope", types: []string{MediaTV}, err: ErrUnsupported}

	q := MediaQuery{Type: "TV", Title: "Show", Season: 1, Episode: 2, Resolution: "1080"}
	res, errs := SearchAll(context.Background(), []Source{a, b, movies, failing, unsupported}, q)
	if len(errs) != 1 || errs[0].Error() != "DOWN: boom" {
		t.Fatalf("errors: %v", errs)
	}
	if len(movies.queries) != 0 {
		t.Fatal("a movie source was asked for a show")
	}
	if got := a.queries[0]; got.Type != MediaTV || got.Resolution != "1080p" {
		t.Fatalf("query not normalized: %+v", got)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Name+"|"+r.Provider)
	}
	want := []string{
		"Show.S01E02.1080p.WEB.h264-ETHEL|b", // the episode, 1080p
		"Show S01E02 720p HDTV x264-CTU|b,a", // the episode, merged, b's copy kept
		"Show.S01.1080p.BluRay.x265-GRP|a",   // the season's pack holds it too
		"Show.S01E02.1080p.WEB.h264-NTb|a",   // no seeders: last
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("results:\n%s\nwant\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
	merged := res[1]
	if merged.Seeders != 40 || merged.InfoHash != strings.ToUpper(hashA) || merged.Size != 100 || merged.EpisodeNumber != 1002 ||
		merged.ReleaseGroup != "CTU" || merged.Date == "" || !reflect.DeepEqual(ProviderIDs(merged), []string{"b", "a"}) {
		t.Fatalf("merged: %+v", merged)
	}
	if res[2].EpisodeNumber != -1 || !res[2].IsBatch {
		t.Fatalf("pack: %+v", res[2])
	}

	// Free text: nothing is filtered, only free-text sources are asked.
	res, errs = SearchAll(context.Background(), []Source{a, b}, MediaQuery{Query: "show"})
	if len(errs) != 0 || len(res) != 5 || len(b.queries) != 1 {
		t.Fatalf("free text: %d results, %v, b asked %d times", len(res), errs, len(b.queries))
	}
}

// Asked for a season, its own pack comes before bigger packs, however
// well seeded.
func TestSearchAllSeasonPackFirst(t *testing.T) {
	src := &fakeSource{id: "a", types: []string{MediaTV}, res: []*SearchResult{
		{Name: "Show.Complete.Series.1080p.BluRay-GRP", InfoHash: hashA, Seeders: 900},
		{Name: "Show.S01-S03.1080p.WEB-GRP", InfoHash: hashB, Seeders: 400},
		{Name: "Show.S02.1080p.WEB-GRP", InfoHash: hashC, Seeders: 30},
	}}
	res, _ := SearchAll(context.Background(), []Source{src}, MediaQuery{Type: MediaTV, Title: "Show", Season: 2})
	if len(res) != 3 || res[0].InfoHash != hashC || res[1].InfoHash != hashA || res[2].InfoHash != hashB {
		var names []string
		for _, r := range res {
			names = append(names, r.Name)
		}
		t.Fatalf("order: %v", names)
	}
}

func TestSearchAllMovieYear(t *testing.T) {
	src := &fakeSource{id: "a", types: []string{MediaMovie}, res: []*SearchResult{
		{Name: "Dune.2021.1080p.WEB-DL", InfoHash: hashA, Seeders: 100},
		{Name: "Dune.1984.1080p.BluRay", InfoHash: hashB, Seeders: 300},
		{Name: "Dune Part Two 2024 2160p", InfoHash: hashC, Seeders: 900},
		{Name: "Dune 1080p", InfoHash: hashD, Seeders: 5},
	}}
	res, _ := SearchAll(context.Background(), []Source{src}, MediaQuery{Type: MediaMovie, Title: "Dune", Year: 2021})
	if len(res) != 2 || res[0].InfoHash != hashA || res[1].InfoHash != hashD {
		t.Fatalf("%+v", res)
	}
}

func TestSearchAllTimeouts(t *testing.T) {
	old := SourceTimeout
	SourceTimeout = 100 * time.Millisecond
	t.Cleanup(func() { SourceTimeout = old })
	ok := &fakeSource{id: "ok", types: []string{MediaMovie}, res: []*SearchResult{{Name: "Movie 2020", InfoHash: hashA, Seeders: 1}}}
	slow := &fakeSource{id: "slow", types: []string{MediaMovie}, block: true}
	stuck := &fakeSource{id: "stuck", types: []string{MediaMovie}, stuck: true}
	start := time.Now()
	res, errs := SearchAll(context.Background(), []Source{ok, slow, stuck}, MediaQuery{Type: MediaMovie, Title: "Movie"})
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if len(res) != 1 || len(errs) != 2 {
		t.Fatalf("%d results, errors %v", len(res), errs)
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "no answer within 100ms") {
			t.Errorf("error: %v", err)
		}
	}
}

func TestMagnetFor(t *testing.T) {
	a := &fakeSource{id: "a"}
	broken := &fakeSource{id: "broken", err: errors.New("down")}
	ctx := context.Background()
	if m, err := MagnetFor(ctx, []Source{a, broken}, &SearchResult{Provider: "broken,a"}); err != nil || m != "magnet:?from=a" {
		t.Fatalf("%q %v", m, err)
	}
	if m, _ := MagnetFor(ctx, nil, &SearchResult{Provider: "gone", InfoHash: hashA, Name: "x"}); !strings.HasPrefix(m, "magnet:?xt=urn:btih:"+hashA) {
		t.Fatalf("from hash: %q", m)
	}
	if m, _ := MagnetFor(ctx, nil, &SearchResult{DownloadURL: "https://x.example/a.torrent"}); m != "https://x.example/a.torrent" {
		t.Fatalf("url: %q", m)
	}
	if _, err := MagnetFor(ctx, []Source{broken}, &SearchResult{Provider: "broken"}); err == nil || err.Error() != "down" {
		t.Fatalf("error: %v", err)
	}
}

func TestBuildSources(t *testing.T) {
	cfg := config.TorrentSourceSettings{
		Torznab: []config.TorznabIndexer{
			{Name: "Prowlarr 1337x", URL: "http://127.0.0.1:9696/3/api", APIKey: "k", Enabled: true},
			{Name: "Off", URL: "http://127.0.0.1:9696/4/api", Enabled: false},
			{Name: "Prowlarr 1337x", URL: "http://127.0.0.1:9696/5/", Enabled: true},
			{Name: "", URL: "", Enabled: true},
		},
		YTS:         config.TorrentSource{Enabled: true, URL: "yts.lt/"},
		EZTV:        config.TorrentSource{Enabled: true},
		PirateBay:   config.TorrentSource{Enabled: true},
		TorrentsCSV: config.TorrentSource{Enabled: false},
		Archive:     config.TorrentSource{Enabled: true},
	}
	var ids []string
	for _, s := range BuildSources(cfg, nil) {
		ids = append(ids, s.ID())
	}
	want := []string{"torznab-prowlarr-1337x", "torznab-prowlarr-1337x-2", "yts", "eztv", "piratebay", "archive"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("%v", ids)
	}
	srcs := BuildSources(cfg, nil)
	if y := srcs[2].(*YTS); y.base() != "https://yts.lt" {
		t.Fatalf("yts base %q", y.base())
	}
	if tz := srcs[1].(*Torznab); tz.endpoint != "http://127.0.0.1:9696/5/api" {
		t.Fatalf("torznab endpoint %q", tz.endpoint)
	}
	if SourceByID(srcs, "eztv") == nil || SourceByID(srcs, "torrentscsv") != nil {
		t.Fatal("SourceByID")
	}
	if len(BuildSources(config.TorrentSourceSettings{}, nil)) != 0 {
		t.Fatal("nothing enabled")
	}
}

func TestSrcGet(t *testing.T) {
	page := srcFixture(t, "cloudflare.html")
	srv := newSrcRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/challenge":
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(page)
		case "/html200":
			_, _ = w.Write(page)
		case "/json-as-html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/limited":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
		case "/big":
			_, _ = w.Write(make([]byte, maxResponse+10))
		case "/text":
			_, _ = w.Write([]byte("Service temporarily unavailable"))
		}
	})
	ctx := context.Background()
	c := srv.Client()
	for _, p := range []string{"/challenge", "/html200"} {
		_, err := srcGet(ctx, c, srv.URL+p, "application/json")
		if !errors.Is(err, ErrWebPage) || err.Error() != "the site returned a web page instead of results (blocked or down) — try a mirror URL in Settings" {
			t.Errorf("%s: %v", p, err)
		}
	}
	var out struct{ OK bool }
	if err := srcGetJSON(ctx, c, srv.URL+"/json-as-html", &out); err != nil || !out.OK {
		t.Errorf("json as html: %v", err)
	}
	if _, err := srcGet(ctx, c, srv.URL+"/limited", ""); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("429: %v", err)
	}
	if _, err := srcGet(ctx, c, srv.URL+"/big", ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("big: %v", err)
	}
	if err := srcGetJSON(ctx, c, srv.URL+"/text", &out); err == nil || !strings.Contains(err.Error(), "unexpected answer") {
		t.Errorf("text: %v", err)
	}
	if ua := srv.requests()[0].Header.Get("User-Agent"); ua == "" || strings.HasPrefix(ua, "Go-http-client") {
		t.Errorf("user agent %q", ua)
	}
	// Errors never show API keys.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := srcGet(cctx, c, srv.URL+"/x?apikey=SECRET&t=search&jackett_apikey=SECRET2", "")
	if err == nil || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("cancelled: %v", err)
	}
}

func TestLooseJSON(t *testing.T) {
	var v struct {
		A, B, C, D, E, F looseInt
		S, T, U          looseString
	}
	err := json.Unmarshal([]byte(`{"A":12,"B":"34","C":"","D":null,"E":["1968"],"F":"1968-10-01","S":"x","T":["first","second"],"U":42}`), &v)
	if err != nil || v.A != 12 || v.B != 34 || v.C != 0 || v.D != 0 || v.E != 1968 || v.F != 1968 || v.S != "x" || v.T != "first" || v.U != "42" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestWantedResolution(t *testing.T) {
	for in, want := range map[string]string{"1080": "1080p", "1080p": "1080p", "4K": "2160p", "2160": "2160p", "": "", "best": "", "hd": ""} {
		if got := wantedResolution(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestMediaQueryText(t *testing.T) {
	q := MediaQuery{Type: MediaTV, Title: "Grey's Anatomy", AltTitles: []string{"grey's anatomy", "Anatomía de Grey", "A", "B"}, Season: 2, Episode: 5}.normalized()
	if got := q.titles(); !reflect.DeepEqual(got, []string{"Greys Anatomy", "Anatomía de Grey", "A"}) {
		t.Fatalf("titles %q", got)
	}
	if got := q.textFor("Greys Anatomy"); got != "Greys Anatomy S02E05" {
		t.Fatalf("text %q", got)
	}
	q.Episode = 0
	if got := q.textFor("X"); got != "X S02" {
		t.Fatalf("season %q", got)
	}
	m := MediaQuery{Type: "MOVIE", Title: "Spider-Man: No Way Home", Year: 2021}.normalized()
	if got := m.textFor(m.titles()[0]); got != "Spider Man No Way Home 2021" {
		t.Fatalf("movie %q", got)
	}
	if !queryWords("Breaking.Bad.S01E02.720p", "breaking bad s01e02") || queryWords("Breaking.Bad.S01E02", "bad s01e03") {
		t.Fatal("queryWords")
	}
}
