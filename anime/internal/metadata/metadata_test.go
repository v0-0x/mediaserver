package metadata

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

func testService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return NewService(d)
}

// What's kept comes at once; an anime ani.zip couldn't be asked about a
// moment ago isn't asked about again right away (every page would).
func TestCachedAndMisses(t *testing.T) {
	s := testService(t)
	kept := &Anime{Episodes: map[string]*Episode{"1": {Key: "1", Number: 1, Title: "First"}}, EpisodeCount: 12}
	s.db.SetCache(cacheKey(7), kept, time.Hour)
	if a := s.Cached(7); a == nil || a.Get(1) == nil || a.Get(1).Title != "First" {
		t.Fatalf("Cached(7) = %+v, want what's kept", a)
	}
	if md := s.Cached(8).Get(1); md != nil {
		t.Errorf("Cached of an unknown anime: %+v, want nothing", md)
	}

	// Marked as missed: Get doesn't go to the network, and answers at once.
	s.db.SetCache(missKey(9), true, missFor)
	start := time.Now()
	a, err := s.Get(context.Background(), 9)
	if err == nil || a == nil || len(a.Episodes) != 0 {
		t.Errorf("Get of a missed anime: %+v, %v (want empty, an error)", a, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("Get of a missed anime took %v (went to the network?)", time.Since(start))
	}
	// A stale copy still stands in for it.
	s.db.SetCache(cacheKey(9), kept, time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	if a, _ := s.Get(context.Background(), 9); a == nil || a.Get(1) == nil {
		t.Errorf("Get of a missed anime with a stale copy: %+v, want the copy", a)
	}
	if a := s.Cached(9); a == nil || a.Get(1) == nil {
		t.Errorf("Cached of a missed anime with a stale copy: %+v, want the copy", a)
	}
}
