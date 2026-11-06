package anilist

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// The copy here comes at once while AniList is slow: it's asked in the
// background, once, and the new list is in afterwards.
func TestCollectionSoonServesTheCopyWhileAniListIsSlow(t *testing.T) {
	var asked atomic.Int32
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		asked.Add(1)
		time.Sleep(1500 * time.Millisecond) // a slow AniList
		reply(w, http.StatusOK, data(map[string]any{"MediaListCollection": testCollection(listEntry(101, 1, "CURRENT", 3))}))
	})
	setLoggedIn(p, 7)
	seed(p, "ANIME", testCollection(listEntry(101, 1, "CURRENT", 1)))
	p.mu.Lock()
	p.fetchedAt["ANIME"] = time.Now().Add(-time.Hour) // the copy is old
	p.mu.Unlock()

	start := time.Now()
	c, refreshing, err := p.CollectionSoon(context.Background(), "ANIME", time.Minute)
	if err != nil || c == nil || !refreshing {
		t.Fatalf("CollectionSoon: %v, refreshing %v", err, refreshing)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("CollectionSoon waited %v for AniList", time.Since(start))
	}
	if got := c.Entries()[0].Progress; got != 1 {
		t.Fatalf("the copy here says progress %d, want 1", got)
	}
	// Asked again at once: the same fetch, not another.
	if _, again, _ := p.CollectionSoon(context.Background(), "ANIME", time.Minute); !again {
		t.Error("a second call isn't refreshing")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, refreshing, _ = p.CollectionSoon(context.Background(), "ANIME", time.Minute)
		if !refreshing && c.Entries()[0].Progress == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new list didn't come: refreshing %v, progress %d", refreshing, c.Entries()[0].Progress)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("AniList was asked %d times, want once", n)
	}
}
