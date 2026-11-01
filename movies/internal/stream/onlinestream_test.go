package stream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fakeProvider answers with fixed streams (or an error), after a delay.
type fakeProvider struct {
	id, name string
	types    []string
	streams  []providers.Stream
	err      error
	delay    time.Duration
	// block makes Streams wait for its context to end (a provider that
	// never answers).
	block bool

	calls atomic.Int32
	mu    sync.Mutex
	last  providers.Request
}

func (p *fakeProvider) ID() string   { return p.id }
func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Kind() string { return providers.KindStremio }
func (p *fakeProvider) Supports(t string) bool {
	if p.types == nil {
		return true
	}
	for _, x := range p.types {
		if x == t {
			return true
		}
	}
	return false
}

func (p *fakeProvider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	p.calls.Add(1)
	p.mu.Lock()
	p.last = r
	p.mu.Unlock()
	if p.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return append([]providers.Stream(nil), p.streams...), p.err
}

func (p *fakeProvider) request() providers.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

type fakeSubs struct{ subs []providers.Subtitle }

func (f *fakeSubs) Subtitles(ctx context.Context, r providers.Request) ([]providers.Subtitle, error) {
	return f.subs, nil
}

func intp(n int) *int { return &n }

func movie() *catalog.Media {
	return &catalog.Media{
		ID: 155, TMDBID: 155, IMDbID: "tt0468569", Type: catalog.TypeMovie,
		Title:      catalog.Title{English: "The Dark Knight", UserPreferred: "The Dark Knight", Native: "The Dark Knight"},
		SeasonYear: intp(2008),
	}
}

func show() *catalog.Media {
	return &catalog.Media{
		ID: catalog.ShowID(1396), TMDBID: 1396, IMDbID: "tt0903747", Type: catalog.TypeTV,
		Title:      catalog.Title{English: "Breaking Bad", Native: "Breaking Bad", UserPreferred: "Breaking Bad"},
		SeasonYear: intp(2008),
		Seasons:    []catalog.Season{{Number: 0, EpisodeCount: 9}, {Number: 1, EpisodeCount: 7}, {Number: 2, EpisodeCount: 13}},
	}
}

func newTestService(opts Options, ps ...providers.Provider) *Service {
	s := NewService(func() Options { return opts })
	s.Providers = func() []providers.Provider { return ps }
	return s
}

func TestRequestFor(t *testing.T) {
	m := movie()
	m.Title.Native = "Le Chevalier noir"
	r := RequestFor(m, 0)
	want := providers.Request{MediaID: 155, TMDBID: 155, IMDbID: "tt0468569", Type: providers.TypeMovie, Title: "The Dark Knight", OriginalTitle: "Le Chevalier noir", Year: 2008}
	if r != want {
		t.Fatalf("movie: %+v, want %+v", r, want)
	}

	for _, tc := range []struct {
		key, season, episode int
	}{
		{2005, 2, 5}, // S02E05
		{1001, 1, 1},
		{3, 0, 3}, // the special S00E03
	} {
		r := RequestFor(show(), tc.key)
		if r.Type != providers.TypeTV || r.TMDBID != 1396 || r.IMDbID != "tt0903747" || r.MediaID != catalog.ShowID(1396) {
			t.Fatalf("show ids: %+v", r)
		}
		if r.Season != tc.season || r.Episode != tc.episode || r.OriginalTitle != "" || r.Year != 2008 {
			t.Errorf("key %d: S%d E%d (original %q), want S%d E%d", tc.key, r.Season, r.Episode, r.OriginalTitle, tc.season, tc.episode)
		}
	}
}

func TestStreamsRanksAndReportsErrors(t *testing.T) {
	a := &fakeProvider{id: "a", name: "A", streams: []providers.Stream{
		{Title: "a 720p", URL: "https://a.example/720.mp4", Quality: "720p"},
		{Title: "a torrent", InfoHash: strings.Repeat("ab", 20), Quality: "1080p", Seeders: 50},
	}}
	b := &fakeProvider{id: "b", name: "B", streams: []providers.Stream{
		{Title: "b mkv", URL: "https://b.example/1080.mkv", Quality: "1080p", NotWebReady: true},
		{Title: "b 1080p", URL: "https://b.example/1080.m3u8", Quality: "1080p"},
		{Title: "b 4k", URL: "https://b.example/2160.mp4", Quality: "2160p"},
		{Title: "nothing to play"},
	}}
	c := &fakeProvider{id: "c", name: "C", err: errors.New("site down")}
	tvOnly := &fakeProvider{id: "tv", name: "TV only", types: []string{providers.TypeTV}, streams: []providers.Stream{{URL: "https://tv.example/x.mp4"}}}
	s := newTestService(Options{PreferredQuality: "1080p"}, a, b, c, tvOnly)

	res, err := s.Streams(context.Background(), movie(), 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, st := range res.Streams {
		titles = append(titles, st.Title)
	}
	want := "b 1080p|b mkv|b 4k|a 720p|a torrent"
	if got := strings.Join(titles, "|"); got != want {
		t.Fatalf("order:\n got %s\nwant %s", got, want)
	}
	if res.Episode != catalog.MovieEpisode || strings.Join(res.Providers, ",") != "a,b,c" || tvOnly.calls.Load() != 0 {
		t.Errorf("episode %d, providers %v, tv-only asked %d times", res.Episode, res.Providers, tvOnly.calls.Load())
	}
	if len(res.Errors) != 1 || res.Errors[0].Provider != "c" || res.Errors[0].Name != "C" || res.Errors[0].Message != "site down" {
		t.Errorf("errors: %+v", res.Errors)
	}
	ids := map[string]bool{}
	for _, st := range res.Streams {
		if len(st.ID) != 16 || ids[st.ID] {
			t.Errorf("%s: id %q (duplicate: %v)", st.Title, st.ID, ids[st.ID])
		}
		ids[st.ID] = true
		if st.Provider == "" || st.ProviderName == "" {
			t.Errorf("%s: provider not filled in", st.Title)
		}
	}
	if k := res.Streams[4].Kind; k != KindTorrent || res.Streams[0].Kind != KindDirect {
		t.Errorf("kinds: %q, %q", res.Streams[0].Kind, k)
	}
	// Ids stay the same from one search to the next.
	again, _ := s.Streams(context.Background(), movie(), 0, "", true)
	if again.Streams[0].ID != res.Streams[0].ID || again.Streams[0].ID != StreamID(b.streams[1]) {
		// The provider fills in Provider before the id is computed.
		withProvider := b.streams[1]
		withProvider.Provider = "b"
		if again.Streams[0].ID != StreamID(withProvider) {
			t.Errorf("ids changed: %s, %s", res.Streams[0].ID, again.Streams[0].ID)
		}
	}
}

func TestStreamsDefaultProviderFirstAndFilter(t *testing.T) {
	a := &fakeProvider{id: "a", name: "A", streams: []providers.Stream{{Title: "a", URL: "https://a.example/1.mp4", Quality: "1080p"}}}
	b := &fakeProvider{id: "b", name: "B", streams: []providers.Stream{{Title: "b", URL: "https://b.example/1.mp4", Quality: "480p"}}}
	s := newTestService(Options{DefaultProvider: "b"}, a, b)
	res, err := s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil || len(res.Streams) != 2 || res.Streams[0].Provider != "b" {
		t.Fatalf("default provider first: %+v %v", res, err)
	}
	only, err := s.Streams(context.Background(), movie(), 1, "a", false)
	if err != nil || len(only.Streams) != 1 || only.Streams[0].Provider != "a" || strings.Join(only.Providers, ",") != "a" {
		t.Fatalf("one provider: %+v %v", only, err)
	}
	if _, err := s.Streams(context.Background(), movie(), 1, "nope", false); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unknown provider: %v", err)
	}
	tv := &fakeProvider{id: "tv", name: "Series", types: []string{providers.TypeTV}}
	s = newTestService(Options{}, tv)
	if _, err := s.Streams(context.Background(), movie(), 1, "tv", false); err == nil || !strings.Contains(err.Error(), "has no movies") {
		t.Fatalf("provider without movies: %v", err)
	}
	if _, err := s.Streams(context.Background(), movie(), 1, "", false); !errors.Is(err, ErrNoProviders) {
		t.Fatalf("no provider for movies: %v", err)
	}
}

func TestStreamsShowEpisode(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{{URL: "https://p.example/e.m3u8"}}}
	s := newTestService(Options{}, p)
	if _, err := s.Streams(context.Background(), show(), 0, "", false); err == nil {
		t.Fatal("a show without an episode")
	}
	if _, err := s.Streams(context.Background(), show(), 2000, "", false); err == nil {
		t.Fatal("episode 0 of season 2")
	}
	res, err := s.Streams(context.Background(), show(), 2005, "", false)
	if err != nil || res.Episode != 2005 {
		t.Fatalf("%+v %v", res, err)
	}
	if r := p.request(); r.Season != 2 || r.Episode != 5 || r.Type != providers.TypeTV || r.IMDbID != "tt0903747" {
		t.Fatalf("request: %+v", r)
	}
	if res.Streams[0].Type != providers.StreamM3U8 || res.Streams[0].FileIdx != -1 {
		t.Errorf("normalized: %+v", res.Streams[0])
	}
}

func TestStreamsTimeout(t *testing.T) {
	slow := &fakeProvider{id: "slow", name: "Slow", block: true}
	late := &fakeProvider{id: "late", name: "Late", delay: 5 * time.Second, streams: []providers.Stream{{URL: "https://late.example/x.mp4"}}}
	fast := &fakeProvider{id: "fast", name: "Fast", streams: []providers.Stream{{URL: "https://fast.example/x.mp4"}}}
	s := newTestService(Options{Timeout: 100 * time.Millisecond}, slow, late, fast)
	start := time.Now()
	res, err := s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if len(res.Streams) != 1 || res.Streams[0].Provider != "fast" {
		t.Fatalf("streams: %+v", res.Streams)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("errors: %+v", res.Errors)
	}
	for _, e := range res.Errors {
		if !strings.Contains(e.Message, "no answer within") {
			t.Errorf("%s: %q", e.Provider, e.Message)
		}
	}
}

// Results are dropped once expired, without waiting for another search.
func TestStreamsCacheExpires(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{{URL: "https://p.example/x.mp4"}}}
	s := newTestService(Options{}, p)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	var expire []func()
	s.afterFunc = func(d time.Duration, f func()) {
		if d != DefaultTTL {
			t.Errorf("expires after %s", d)
		}
		expire = append(expire, f)
	}
	if _, err := s.Streams(context.Background(), movie(), 1, "", false); err != nil {
		t.Fatal(err)
	}
	if len(expire) != 1 {
		t.Fatalf("%d expiries scheduled", len(expire))
	}
	// While fresh (asked for again since: the older search's expiry comes
	// first), it's kept.
	expire[0]()
	if len(s.cache) != 1 {
		t.Fatal("dropped before its time")
	}
	now = now.Add(DefaultTTL)
	expire[0]()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) != 0 {
		t.Errorf("%d results kept after they expired", len(s.cache))
	}
}

func TestStreamsCache(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{{URL: "https://p.example/x.mp4"}}}
	q := &fakeProvider{id: "q", name: "Q"}
	s := newTestService(Options{}, p, q)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := s.Streams(ctx, movie(), 1, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("asked %d times, want 1 (cached)", n)
	}
	// Another episode, another provider set: asked again.
	if _, err := s.Streams(ctx, show(), 1001, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Streams(ctx, movie(), 1, "p", false); err != nil {
		t.Fatal(err)
	}
	if n := p.calls.Load(); n != 3 {
		t.Fatalf("asked %d times, want 3", n)
	}
	if _, err := s.Streams(ctx, movie(), 1, "", true); err != nil || p.calls.Load() != 4 {
		t.Fatalf("refresh: asked %d times (%v)", p.calls.Load(), err)
	}
	now = now.Add(DefaultTTL + time.Second)
	if _, err := s.Streams(ctx, movie(), 1, "", false); err != nil || p.calls.Load() != 5 {
		t.Fatalf("expired: asked %d times (%v)", p.calls.Load(), err)
	}
	s.Invalidate()
	if _, err := s.Streams(ctx, movie(), 1, "", false); err != nil || p.calls.Load() != 6 {
		t.Fatalf("invalidated: asked %d times (%v)", p.calls.Load(), err)
	}

	// A total failure isn't kept.
	bad := &fakeProvider{id: "bad", name: "Bad", err: errors.New("offline")}
	s = newTestService(Options{}, bad)
	for i := 0; i < 2; i++ {
		res, err := s.Streams(ctx, movie(), 1, "", false)
		if err != nil || len(res.Errors) != 1 {
			t.Fatalf("%+v %v", res, err)
		}
	}
	if n := bad.calls.Load(); n != 2 {
		t.Fatalf("failure cached: asked %d times", n)
	}
}

func TestStreamsAskedOnceAtATime(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", delay: 150 * time.Millisecond, streams: []providers.Stream{{URL: "https://p.example/x.mp4"}}}
	s := newTestService(Options{}, p)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res, err := s.Streams(context.Background(), movie(), 1, "", false); err != nil || len(res.Streams) != 1 {
				t.Errorf("%+v %v", res, err)
			}
		}()
	}
	wg.Wait()
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("asked %d times at once", n)
	}
	// A request that gives up doesn't stop the search for the others.
	s = newTestService(Options{}, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Streams(ctx, movie(), 1, "", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled request: %v", err)
	}
	res, err := s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil || len(res.Streams) != 1 || p.calls.Load() != 2 {
		t.Fatalf("after a canceled request: %+v %v (asked %d times)", res, err, p.calls.Load())
	}
}

func TestStreamsSubtitles(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{{URL: "https://p.example/x.mp4"}}}
	s := newTestService(Options{}, p)
	s.SubtitleProviders = func() []providers.SubtitleProvider {
		return []providers.SubtitleProvider{&fakeSubs{subs: []providers.Subtitle{
			{URL: "https://subs.example/fr.srt", Language: "fre"},
			{URL: "https://subs.example/en.srt", Language: "eng"},
		}}}
	}
	res, err := s.Streams(context.Background(), movie(), 1, "", false)
	if err != nil || len(res.Subtitles) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	sorted := SortSubtitles(res.Subtitles, "", 1)
	if len(sorted) != 1 || sorted[0].Language != "eng" {
		t.Fatalf("English first: %+v", sorted)
	}
	if sorted := SortSubtitles(res.Subtitles, "fr,fre", 0); sorted[0].Language != "fre" || len(sorted) != 2 {
		t.Fatalf("French first: %+v", sorted)
	}
}

func TestFindBestResolve(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{
		{Title: "4k", URL: "https://p.example/2160.mp4", Quality: "2160p"},
		{Title: "1080", URL: "https://p.example/1080.mp4", Quality: "1080p"},
		{Title: "720", URL: "https://p.example/720.mp4", Quality: "720p"},
		{Title: "?", URL: "https://p.example/x.mp4"},
	}}
	tor := &fakeProvider{id: "t", name: "T", streams: []providers.Stream{{Title: "torrent", InfoHash: strings.Repeat("cd", 20)}}}
	s := newTestService(Options{}, p, tor)
	ctx := context.Background()
	res, err := s.Streams(ctx, movie(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := res.Streams[2]
	got, from, err := s.Find(ctx, movie(), 1, want.ID)
	if err != nil || got.ID != want.ID || got.URL != want.URL || from == nil || len(from.Streams) != len(res.Streams) {
		t.Fatalf("find: %+v %v", got, err)
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("find asked again (%d)", n)
	}
	if _, _, err := s.Find(ctx, movie(), 1, "0123456789abcdef"); !errors.Is(err, ErrStreamGone) {
		t.Fatalf("unknown id: %v", err)
	}

	best, _, err := s.Best(ctx, movie(), 1, "")
	if err != nil || best.Title != "4k" {
		t.Fatalf("best: %+v %v", best, err)
	}
	if _, _, err := s.Best(ctx, movie(), 1, "t"); err == nil || !strings.Contains(err.Error(), "no direct link") {
		t.Fatalf("best of a torrent provider: %v", err)
	}

	for _, tc := range []struct{ want, got string }{
		{"720p", "720"},
		{"1080", "1080"},
		{"best", "4k"},
		{"", "4k"},
		{"1440p", "1080"},
		{"480p", "720"},
		{"4K", "4k"},
	} {
		calls := p.calls.Load()
		st, _, err := s.Resolve(ctx, movie(), 1, "p", tc.want)
		if err != nil || st.Title != tc.got {
			t.Errorf("resolve %q: %q %v, want %q", tc.want, st.Title, err, tc.got)
		}
		if p.calls.Load() != calls+1 {
			t.Errorf("resolve %q didn't ask the provider again", tc.want)
		}
	}
	if _, _, err := s.Resolve(ctx, movie(), 1, "t", "1080p"); err == nil {
		t.Fatal("resolve: a download from a torrent provider")
	}
}

func TestEpisodeNumbers(t *testing.T) {
	m := show()
	for _, tc := range []struct {
		key int
		n   float64
	}{{1001, 1}, {1007, 7}, {2001, 8}, {2013, 20}} {
		if got := ProviderEpisode(m, tc.key); got != tc.n {
			t.Errorf("ProviderEpisode(%d) = %v, want %v", tc.key, got, tc.n)
		}
		if got := EpisodeKeyOf(m, tc.n); got != tc.key {
			t.Errorf("EpisodeKeyOf(%v) = %d, want %d", tc.n, got, tc.key)
		}
	}
	if ProviderEpisode(movie(), 1) != 1 || EpisodeKeyOf(movie(), 1) != catalog.MovieEpisode {
		t.Error("a movie is episode 1")
	}
}
