package share

import (
	"net"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// Home asks about an KumoBox new on the network (one sharing with this one
// isn't new) until it's shared with or the answer is "not now" (kept), and
// about a paired one on another version.
func TestPrompts(t *testing.T) {
	old := config.AppVersion
	config.AppVersion = "1.0.140"
	t.Cleanup(func() { config.AppVersion = old })

	s := hostWithSharing(t)
	now := time.Now()
	tv, older, newer, same, dev := newGuest(t), newGuest(t), newGuest(t), newGuest(t), newGuest(t)
	s.mu.Lock()
	s.running = &run{wake: make(chan struct{}, 1)}
	tvKey, _ := parsePublicKey(tv.ID, tv.PublicKey())
	s.peers[tv.ID] = &peer{id: tv.ID, pub: tvKey, name: "Fire TV", platform: "android", version: "1.0.140", seen: now, addr: net.ParseIP("192.168.1.30"), port: 43311}
	s.peers[older.ID] = &peer{id: older.ID, name: "Laptop", version: "1.0.120", allowed: true, seen: now}
	s.peers[newer.ID] = &peer{id: newer.ID, name: "Desk", version: "1.0.150", shares: true, seen: now}
	s.peers[same.ID] = &peer{id: same.ID, name: "Same", version: "1.0.140", allowed: true, seen: now}
	s.peers[dev.ID] = &peer{id: dev.ID, name: "Dev", version: "1.0.0", allowed: true, seen: now}
	s.mu.Unlock()

	kinds := func() map[string]string {
		out := map[string]string{}
		for _, p := range s.Prompts() {
			out[p.Name] = p.Kind
		}
		return out
	}
	got := kinds()
	want := map[string]string{"Fire TV": "new", "Laptop": "older", "Desk": "newer"}
	if len(got) != len(want) {
		t.Fatalf("prompts %v, want %v", got, want)
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("%s: %q, want %q (all: %v)", n, got[n], k, got)
		}
	}

	// Not now: not asked again, also after a restart.
	no := true
	if err := s.SetGrants(tv.ID, Grants{Declined: &no}); err != nil {
		t.Fatal(err)
	}
	if k := kinds()["Fire TV"]; k != "" {
		t.Errorf("asked again after not now: %q", k)
	}
	s.mu.Lock()
	s.peers = map[string]*peer{}
	s.load()
	p := s.peers[tv.ID]
	s.mu.Unlock()
	if p == nil || !p.declined || p.platform != "android" {
		t.Fatalf("not now not kept: %+v", p)
	}
	// Shared with after all: the not now is over.
	yes := true
	if err := s.SetGrants(tv.ID, Grants{Allowed: &yes}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	declined := s.peers[tv.ID].declined
	s.mu.Unlock()
	if declined {
		t.Error("still declined once shared with")
	}

	// Gone from the network: not asked about.
	s.mu.Lock()
	for _, p := range s.peers {
		p.seen = time.Time{}
	}
	s.mu.Unlock()
	if got := s.Prompts(); len(got) != 0 {
		t.Errorf("prompts about KumoBoxes not around: %+v", got)
	}

	// Sharing off: nothing.
	s.mu.Lock()
	s.running = nil
	s.mu.Unlock()
	if got := s.Prompts(); got != nil {
		t.Errorf("prompts with sharing off: %+v", got)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.0.9", "1.0.10", -1, true},
		{"1.0.10", "1.0.9", 1, true},
		{"1.0.5", "1.0.5", 0, true},
		{"1.0.0", "1.0.5", 0, false},
		{"", "1.0.5", 0, false},
		{"1.0.x", "1.0.5", 0, false},
	} {
		if got, ok := compareVersions(c.a, c.b); got != c.want || ok != c.ok {
			t.Errorf("%s vs %s: %d %v", c.a, c.b, got, ok)
		}
	}
}
