package letterboxd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFeed(t *testing.T) {
	feed, err := ParseFeed(strings.NewReader(string(readFixture(t, "feed.xml"))))
	if err != nil {
		t.Fatal(err)
	}
	if feed.Username != "davevis" || feed.DisplayName != "Dave Vis" {
		t.Errorf("member = %q / %q", feed.Username, feed.DisplayName)
	}
	// 9 items, one of them a list.
	if len(feed.Items) != 8 {
		t.Fatalf("got %d items, want 8 (the list left out)", len(feed.Items))
	}
	for _, it := range feed.Items {
		if strings.HasPrefix(it.GUID, "letterboxd-list-") {
			t.Errorf("list item kept: %+v", it)
		}
	}

	m := feed.Items[0]
	nz := time.FixedZone("", 13*3600)
	want := DiaryItem{
		GUID:        "letterboxd-review-568371234",
		Link:        "https://letterboxd.com/davevis/film/the-matrix/1/",
		FilmTitle:   "The Matrix",
		FilmYear:    1999,
		TMDBMovieID: 603,
		WatchedDate: "2024-03-10",
		Rewatch:     true,
		Rating:      4.5,
		Liked:       true,
		Review:      "Still holds up. Neo's first jump never gets old & the lobby scene is perfect.\n\nSecond watch with friends.\nThey loved it.",
		Spoilers:    true,
		Poster:      "https://a.ltrbxd.com/resized/film-poster/5/1/5/1/8/51518-the-matrix-0-600-0-900-crop.jpg?v=fc7c366afe",
		Creator:     "Dave Vis",
		Title:       "The Matrix, 1999 - ★★★★½ (contains spoilers)",
		Published:   time.Date(2024, 3, 10, 21, 14, 5, 0, nz),
	}
	if !m.Published.Equal(want.Published) {
		t.Errorf("published = %v, want %v", m.Published, want.Published)
	}
	m.Published = want.Published
	if m != want {
		t.Errorf("first item:\n got %+v\nwant %+v", m, want)
	}

	byGUID := map[string]DiaryItem{}
	for _, it := range feed.Items {
		byGUID[it.GUID] = it
	}
	tests := []struct {
		guid        string
		rating      float64
		liked       bool
		review      string
		watchedDate string
		tmdb, tv    int
	}{
		{"letterboxd-review-567000001", 3.5, false, "Loud. Very loud.", "2024-03-05", 693134, 0},
		{"letterboxd-watch-566000002", 5, false, "", "2024-03-02", 496243, 0},
		{"letterboxd-watch-565000003", 0, false, "", "2024-02-20", 0, 87108},
		{"letterboxd-watch-564000004", 2, false, "", "2024-02-15", 0, 0},
		{"letterboxd-review-562000006", 3, false, "The highway chase, though.", "", 604, 0},
		{"letterboxd-watch-401000007", 4, true, "", "2020-05-01", 603, 0},
	}
	for _, tt := range tests {
		it, ok := byGUID[tt.guid]
		if !ok {
			t.Errorf("%s missing", tt.guid)
			continue
		}
		if it.Rating != tt.rating || it.Liked != tt.liked || it.Review != tt.review ||
			it.WatchedDate != tt.watchedDate || it.TMDBMovieID != tt.tmdb || it.TMDBTVID != tt.tv {
			t.Errorf("%s = %+v", tt.guid, it)
		}
		if it.Published.IsZero() {
			t.Errorf("%s: no pubDate", tt.guid)
		}
	}
	if p := byGUID["letterboxd-watch-401000007"].Published; p.Day() != 1 || p.Month() != time.May {
		t.Errorf("one-digit day pubDate = %v", p)
	}
}

func TestParseFeedTolerance(t *testing.T) {
	// Other namespace spellings, HTML entities outside CDATA, an escaped
	// description and no channel link.
	const feed = `<?xml version="1.0"?>
<rss version="2.0" xmlns:lb="http://letterboxd.com/" xmlns:t="http://www.themoviedb.org/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel><title>Letterboxd - Ana&nbsp;B</title>
<item>
  <title>Heat</title><link>https://letterboxd.com/ana_b/film/heat-1995/</link>
  <guid>letterboxd-watch-1</guid>
  <pubDate>2024-01-02T10:00:00Z</pubDate>
  <lb:watchedDate>2024-01-02</lb:watchedDate><lb:filmTitle>Heat</lb:filmTitle><lb:filmYear>1995</lb:filmYear>
  <lb:memberRating>-1</lb:memberRating><lb:rewatch>yes</lb:rewatch>
  <t:movieId>949</t:movieId>
  <description>&lt;p&gt;Rewatched on Tuesday January 2, 2024.&lt;/p&gt;</description>
</item>
</channel></rss>`
	f, err := ParseFeed(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	if f.DisplayName != "Ana B" || f.Username != "" {
		t.Errorf("member = %q / %q", f.Username, f.DisplayName)
	}
	if len(f.Items) != 1 {
		t.Fatalf("items = %d", len(f.Items))
	}
	it := f.Items[0]
	if it.TMDBMovieID != 949 || it.Rating != 0 || !it.Rewatch || it.Review != "" || it.WatchedDate != "2024-01-02" || it.Published.IsZero() {
		t.Errorf("item = %+v", it)
	}
}

func TestParseFeedRejectsHTML(t *testing.T) {
	if _, err := ParseFeed(strings.NewReader("<!DOCTYPE html><html><body>Not here</body></html>")); err == nil {
		t.Error("an HTML page parsed as a feed")
	}
}

func TestParseDescription(t *testing.T) {
	tests := []struct {
		name, in, review, poster string
		spoilers                 bool
	}{
		{"empty", "", "", "", false},
		{"watched only", `<p><img src="p.jpg"/></p> <p>Watched on Monday January 1, 2024.</p>`, "", "p.jpg", false},
		{"lists and quotes", `<p>Top:</p><ul><li>One</li><li>Two</li></ul><blockquote>Quoted</blockquote>`, "Top:\n\nOne\n\nTwo\n\nQuoted", "", false},
		{"bare text", `Just text &amp; more`, "Just text & more", "", false},
		{"spoilers", `<p><em>This review may contain spoilers.</em></p><p>He dies.</p>`, "He dies.", "", true},
		{"watched on in a longer review", `<p>Watched on Monday January 1, 2024.</p><p>Then again.</p>`, "Watched on Monday January 1, 2024.\n\nThen again.", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review, poster, spoilers := parseDescription(tt.in)
			if review != tt.review || poster != tt.poster || spoilers != tt.spoilers {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", review, poster, spoilers, tt.review, tt.poster, tt.spoilers)
			}
		})
	}
}

func TestFetchFeed(t *testing.T) {
	fixture := readFixture(t, "feed.xml")
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/davevis/rss/":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write(fixture)
		case "/busy/rss/":
			http.Error(w, "slow down", http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	feed, err := FetchFeed(ctx, srv.Client(), srv.URL, "DaveVis")
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 8 || feed.Username != "davevis" {
		t.Errorf("feed = %s, %d items", feed.Username, len(feed.Items))
	}
	if gotUA != "Moviko/"+config.AppVersion {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if _, err := FetchFeed(ctx, srv.Client(), srv.URL, "nobody"); !errors.Is(err, ErrNoSuchMember) {
		t.Errorf("unknown member: %v", err)
	}
	if _, err := FetchFeed(ctx, srv.Client(), srv.URL, "busy"); err == nil || errors.Is(err, ErrNoSuchMember) {
		t.Errorf("429: %v", err)
	}
	if _, err := FetchFeed(ctx, srv.Client(), srv.URL, "../etc"); !errors.Is(err, ErrBadUsername) {
		t.Errorf("bad username: %v", err)
	}
}

func TestParseUsername(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"dave", "dave", true},
		{" @Dave_V ", "Dave_V", true},
		{"letterboxd.com/dave", "dave", true},
		{"https://letterboxd.com/dave/films/diary/", "dave", true},
		{"https://letterboxd.com/dave?x=1", "dave", true},
		{"", "", false},
		{"dave vis", "", false},
		{"dave/../x", "", false},
		{"dävé", "", false},
	}
	for _, tt := range tests {
		got, err := ParseUsername(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseUsername(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestHelpers(t *testing.T) {
	if a, b := ViewerID("DaveVis"), ViewerID("davevis"); a != b || a <= 0 {
		t.Errorf("ViewerID: %d, %d", a, b)
	}
	if ViewerID("dave") == ViewerID("davevis") {
		t.Error("ViewerID collides")
	}
	for in, want := range map[string]string{
		"https://letterboxd.com/dave/film/the-matrix/":      "https://letterboxd.com/film/the-matrix/",
		"https://letterboxd.com/dave/film/the-matrix/2/":    "https://letterboxd.com/film/the-matrix/",
		"https://letterboxd.com/dave/list/best-of-nineties": "https://letterboxd.com/dave/list/best-of-nineties",
		"https://boxd.it/2a1m":                              "https://boxd.it/2a1m",
	} {
		if got := FilmURL(in); got != want {
			t.Errorf("FilmURL(%q) = %q", in, got)
		}
	}
	for stars, score := range map[float64]int{0: 0, 0.5: 10, 3.5: 70, 5: 100, 7: 100} {
		if got := StarsToScore(stars); got != score {
			t.Errorf("StarsToScore(%v) = %d", stars, got)
		}
	}
	for score, stars := range map[float64]float64{0: 0, 1: 0.5, 10: 0.5, 70: 3.5, 74: 3.5, 75: 4, 87: 4.5, 100: 5, 120: 5} {
		if got := ScoreToStars(score); got != stars {
			t.Errorf("ScoreToStars(%v) = %v", score, got)
		}
	}
	for in, want := range map[string]string{"2024-03-01": "2024-03-01", "2024-03-01 10:00": "2024-03-01", "2024/03/01": "2024-03-01", "2024-13-01": "", "": "", "soon": ""} {
		if got := normDate(in); got != want {
			t.Errorf("normDate(%q) = %q", in, got)
		}
	}
}
