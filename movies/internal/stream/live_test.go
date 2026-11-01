package stream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// liveLog collects a search's progress.
type liveLog struct {
	mu      sync.Mutex
	asked   []Asked
	answers []Answer
}

func (l *liveLog) progress() *Progress {
	return &Progress{
		Asking: func(a []Asked) { l.mu.Lock(); l.asked = a; l.mu.Unlock() },
		Answer: func(a Answer) { l.mu.Lock(); l.answers = append(l.answers, a); l.mu.Unlock() },
	}
}

func (l *liveLog) order() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var ids []string
	for _, a := range l.answers {
		ids = append(ids, a.Provider)
	}
	return strings.Join(ids, ",")
}

// Each provider's answer comes as it answers, the fastest first; the result
// at the end is ranked as Streams ranks it. Asked again a moment later, the
// answers kept come at once.
func TestStreamsLive(t *testing.T) {
	slow := &fakeProvider{id: "slow", name: "Slow", delay: 120 * time.Millisecond, streams: []providers.Stream{{Title: "slow 4k", URL: "https://slow.example/4k.mp4", Quality: "2160p"}}}
	fast := &fakeProvider{id: "fast", name: "Fast", streams: []providers.Stream{
		{Title: "fast 720", URL: "https://fast.example/720.mp4", Quality: "720p"},
		{Title: "dup of slow", URL: "https://slow.example/4k.mp4"},
	}}
	bad := &fakeProvider{id: "bad", name: "Bad", delay: 40 * time.Millisecond, err: errors.New("site down")}
	s := newTestService(Options{}, slow, fast, bad)
	ctx := context.Background()

	var log liveLog
	res, err := s.StreamsLive(ctx, movie(), 1, "", false, log.progress())
	if err != nil {
		t.Fatal(err)
	}
	if len(log.asked) != 3 || log.asked[0].Provider != "slow" || log.asked[1].Name != "Fast" {
		t.Fatalf("asked %+v", log.asked)
	}
	if got := log.order(); got != "fast,bad,slow" {
		t.Fatalf("answers came as %s", got)
	}
	f, b := log.answers[0], log.answers[1]
	if len(f.Streams) != 2 || f.Streams[0].ID == "" || f.Streams[0].Kind != KindDirect || f.Cached || b.Error != "site down" || b.Ms < 30 {
		t.Fatalf("answers %+v / %+v", f, b)
	}
	// The whole result: ranked, the duplicate (slow comes first in the
	// providers' order) dropped.
	if len(res.Streams) != 2 || res.Streams[0].Title != "slow 4k" || res.Streams[1].Title != "fast 720" || len(res.Errors) != 1 {
		t.Fatalf("result %+v", res)
	}
	// Each stream's id is the one the list ends with: picked while the
	// search goes on, it's found.
	if f.Streams[0].ID != res.Streams[1].ID {
		t.Errorf("ids differ: %s / %s", f.Streams[0].ID, res.Streams[1].ID)
	}

	// Kept: the answers come at once, cached, each provider with what was
	// kept of its streams.
	var again liveLog
	if _, err := s.StreamsLive(ctx, movie(), 1, "", false, again.progress()); err != nil {
		t.Fatal(err)
	}
	if slow.calls.Load() != 1 || again.order() != "fast,bad,slow" {
		t.Fatalf("asked %d times, answers %s", slow.calls.Load(), again.order())
	}
	for _, a := range again.answers {
		if !a.Cached {
			t.Errorf("not marked cached: %+v", a)
		}
	}
	if len(again.answers[0].Streams) != 1 || again.answers[0].Streams[0].Title != "fast 720" || len(again.answers[2].Streams) != 1 || again.answers[1].Error != "site down" {
		t.Errorf("replayed %+v", again.answers)
	}
}

// A request joining a search under way gets the answers that came before
// it, then the others as they come; the providers are asked once.
func TestStreamsLiveJoinsSearch(t *testing.T) {
	fast := &fakeProvider{id: "fast", name: "Fast", streams: []providers.Stream{{URL: "https://fast.example/1.mp4"}}}
	slow := &fakeProvider{id: "slow", name: "Slow", delay: 200 * time.Millisecond, streams: []providers.Stream{{URL: "https://slow.example/1.mp4"}}}
	s := newTestService(Options{}, fast, slow)
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Streams(ctx, movie(), 1, "", false); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(60 * time.Millisecond) // fast answered, slow still looking
	var log liveLog
	res, err := s.StreamsLive(ctx, movie(), 1, "", false, log.progress())
	<-done
	if err != nil || len(res.Streams) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := log.order(); got != "fast,slow" || fast.calls.Load() != 1 || slow.calls.Load() != 1 {
		t.Fatalf("answers %s, asked %d/%d times", got, fast.calls.Load(), slow.calls.Load())
	}
}

// A provider failing again and again is listed last, and the automatic
// picks leave it out (without waiting for it), until it answers again.
func TestDemotedProviders(t *testing.T) {
	flaky := &fakeProvider{id: "flaky", name: "Flaky", err: errors.New("site down")}
	good := &fakeProvider{id: "good", name: "Good", streams: []providers.Stream{{Title: "good 720", URL: "https://good.example/720.mp4", Quality: "720p"}}}
	s := newTestService(Options{}, flaky, good)
	s.Health = providers.NewHealth()
	ctx := context.Background()
	for i := 0; i < providers.DemoteAfter; i++ {
		if _, err := s.Streams(ctx, movie(), 1, "", true); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Health.Demoted("flaky") {
		t.Fatalf("not demoted: %+v", s.Health.Get("flaky"))
	}

	// Back with a better stream, but still demoted when the pick is made:
	// the pick doesn't ask it.
	flaky.err = nil
	flaky.streams = []providers.Stream{{Title: "flaky 4k", URL: "https://flaky.example/4k.mp4", Quality: "2160p"}}
	calls := flaky.calls.Load()
	best, _, err := s.Best(ctx, movie(), 1, "")
	if err != nil || best.Title != "good 720" || flaky.calls.Load() != calls {
		t.Fatalf("best = %+v %v (flaky asked %d times)", best, err, flaky.calls.Load()-calls)
	}
	// Watch online asks it, listed last; it answered, so it's back.
	var log liveLog
	res, err := s.StreamsLive(ctx, movie(), 1, "", true, log.progress())
	if err != nil {
		t.Fatal(err)
	}
	if len(log.asked) != 2 || log.asked[1].Provider != "flaky" || !log.asked[1].Demoted || log.asked[0].Demoted {
		t.Fatalf("asked %+v", log.asked)
	}
	if s.Health.Demoted("flaky") || res.Streams[0].Title != "flaky 4k" {
		t.Fatalf("after a success: %+v, streams %+v", s.Health.Get("flaky"), res.Streams)
	}
	// The pick takes it again, from the links just found.
	calls = flaky.calls.Load()
	if best, _, err := s.Best(ctx, movie(), 1, ""); err != nil || best.Title != "flaky 4k" || flaky.calls.Load() != calls {
		t.Fatalf("best = %+v %v (asked %d times)", best, err, flaky.calls.Load()-calls)
	}

	// Named, a demoted provider is asked all the same.
	for i := 0; i < providers.DemoteAfter; i++ {
		s.Health.Record("flaky", errors.New("down"), time.Second)
	}
	s.Invalidate()
	if best, _, err := s.Best(ctx, movie(), 1, "flaky"); err != nil || best.Title != "flaky 4k" {
		t.Fatalf("best of flaky = %+v %v", best, err)
	}
}

// A demoted provider's streams kept from before come after the others',
// whatever their quality.
func TestDemotedStreamsLast(t *testing.T) {
	a := &fakeProvider{id: "a", name: "A", streams: []providers.Stream{{Title: "a 4k", URL: "https://a.example/4k.mp4", Quality: "2160p"}}}
	b := &fakeProvider{id: "b", name: "B", streams: []providers.Stream{{Title: "b 720", URL: "https://b.example/720.mp4", Quality: "720p"}}}
	s := newTestService(Options{}, a, b)
	s.Health = providers.NewHealth()
	ctx := context.Background()
	if _, err := s.Streams(ctx, movie(), 1, "", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < providers.DemoteAfter; i++ {
		s.Health.Record("a", errors.New("down"), time.Second)
	}
	var log liveLog
	res, err := s.StreamsLive(ctx, movie(), 1, "", false, log.progress())
	if err != nil || res.Streams[0].Title != "b 720" || res.Streams[1].Title != "a 4k" {
		t.Fatalf("%+v %v", res.Streams, err)
	}
	if len(log.asked) != 2 || log.asked[1].Provider != "a" || !log.asked[1].Demoted || len(log.answers) != 2 || !log.answers[0].Cached {
		t.Errorf("kept result: asked %+v, answers %+v", log.asked, log.answers)
	}
}

// The version of a show that played last is looked for first: its
// provider's links most like it (release group, source, quality), in Watch
// online and the automatic picks; auto play next's own version wins.
func TestVersions(t *testing.T) {
	a := &fakeProvider{id: "a", name: "A", streams: []providers.Stream{
		{Title: "Breaking.Bad.S01E02.2160p.WEB-DL.x265-AAA", URL: "https://a.example/4k.mkv", Quality: "2160p"},
		{Title: "Breaking.Bad.S01E02.1080p.BluRay.x264-BBB", URL: "https://a.example/1080-bbb.mkv", Quality: "1080p"},
		{Title: "Breaking.Bad.S01E02.720p.WEB-DL.x264-CCC", URL: "https://a.example/720-ccc.mp4", Quality: "720p"},
	}}
	b := &fakeProvider{id: "b", name: "B", streams: []providers.Stream{{Title: "b 4k", URL: "https://b.example/4k.mp4", Quality: "2160p"}}}
	s := newTestService(Options{}, b, a)
	var last *Version
	s.Versions = func(mediaID int) *Version {
		if mediaID != show().ID {
			t.Errorf("version of %d asked", mediaID)
		}
		return last
	}
	ctx := context.Background()

	res, err := s.Streams(ctx, show(), 1002, "", false)
	if err != nil || res.Preferred != nil || res.Streams[0].Title != "b 4k" {
		t.Fatalf("no version: %+v %v", res.Streams, err)
	}

	v := NewVersion("a", "1080p", "Breaking.Bad.S01E01.1080p.BluRay.x264-BBB.mkv", false)
	if v.Quality != "1080p" || v.Source != "BluRay" || v.Group != "BBB" {
		t.Fatalf("version = %+v", v)
	}
	last = &v
	res, _ = s.Streams(ctx, show(), 1002, "", false)
	if res.Preferred == nil || res.Streams[0].URL != "https://a.example/1080-bbb.mkv" || !res.Streams[0].Preferred || res.Streams[1].Preferred ||
		res.Streams[1].Provider != "a" || res.Streams[3].Title != "b 4k" {
		t.Fatalf("with a version: %+v", res.Streams)
	}
	// One provider asked: listed as it ranks (the version still wins the
	// automatic picks).
	if res, _ := s.Streams(ctx, show(), 1002, "a", false); res.Preferred != nil || res.Streams[0].Quality != "2160p" {
		t.Fatalf("one provider: %+v", res.Streams)
	}
	for _, provider := range []string{"", "a"} {
		if best, _, err := s.Best(ctx, show(), 1002, provider); err != nil || best.URL != "https://a.example/1080-bbb.mkv" {
			t.Errorf("best of %q = %+v %v", provider, best, err)
		}
	}
	// Another provider named: its own best.
	if best, _, _ := s.Best(ctx, show(), 1002, "b"); best.Title != "b 4k" {
		t.Errorf("best of b = %+v", best)
	}
	// Auto play next's version (the episode that just played) wins.
	web := NewVersion("a", "720p", "Breaking.Bad.S01E01.720p.WEB-DL.x264-CCC", false)
	if best, _, _ := s.BestFor(ctx, show(), 1002, Pick{Provider: "a", Version: &web}); best.URL != "https://a.example/720-ccc.mp4" {
		t.Errorf("best for the 720p version = %+v", best)
	}
	// Movies aren't concerned.
	if res, _ := s.Streams(ctx, movie(), 1, "", false); res.Preferred != nil {
		t.Errorf("a movie got a version: %+v", res.Preferred)
	}
}

// Test asks the provider again, past what was kept, and records its
// health.
func TestServiceTest(t *testing.T) {
	p := &fakeProvider{id: "p", name: "P", streams: []providers.Stream{{URL: "https://p.example/x.mp4"}, {InfoHash: strings.Repeat("ab", 20)}}}
	s := newTestService(Options{}, p)
	s.Health = providers.NewHealth()
	req := RequestFor(movie(), 1)
	for i := 0; i < 2; i++ {
		a := s.Test(context.Background(), p, req)
		if a.Provider != "p" || len(a.Streams) != 2 || a.Error != "" {
			t.Fatalf("answer %+v", a)
		}
	}
	if p.calls.Load() != 2 || s.Health.Get("p").Successes != 2 {
		t.Fatalf("asked %d times, health %+v", p.calls.Load(), s.Health.Get("p"))
	}
	p.err = errors.New("down")
	if a := s.Test(context.Background(), p, req); a.Error != "down" || s.Health.Get("p").ConsecutiveFailures != 1 {
		t.Fatalf("failure: %+v %+v", a, s.Health.Get("p"))
	}
}
