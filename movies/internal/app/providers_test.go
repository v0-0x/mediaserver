package app

import (
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers/stremio"
)

// Addon collections are served while fresh, then forgotten (not kept in
// memory until they're asked for again); a collection downloaded again
// isn't dropped by the expiry of the copy it replaced.
func TestCollectionCacheExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var expire []func()
	c := newCollectionCache()
	c.now = func() time.Time { return now }
	c.afterFunc = func(d time.Duration, f func()) {
		if d != collectionTTL {
			t.Errorf("expires after %s", d)
		}
		expire = append(expire, f)
	}
	const url = "https://addons.test/collection.json"
	addons := func(name string) []stremio.Addon { return []stremio.Addon{{TransportURL: name}} }

	if _, ok := c.get(url); ok {
		t.Fatal("hit on an empty cache")
	}
	c.put(url, addons("first"))
	if got, ok := c.get(url); !ok || got[0].TransportURL != "first" {
		t.Fatalf("get = %v %v", got, ok)
	}
	now = now.Add(collectionTTL + time.Second)
	if _, ok := c.get(url); ok {
		t.Error("stale collection served")
	}

	// Downloaded again before the first copy's expiry runs.
	c.put(url, addons("second"))
	expire[0]()
	if got, ok := c.get(url); !ok || got[0].TransportURL != "second" {
		t.Fatalf("the new copy was dropped: %v %v", got, ok)
	}
	expire[1]()
	if len(c.entries) != 0 {
		t.Errorf("entries kept after expiry: %d", len(c.entries))
	}
}
