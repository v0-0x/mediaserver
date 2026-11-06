package share

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// hostFor puts a host behind srv in guest's peers, as one that shares.
func hostFor(t *testing.T, guest, host *Service, srv *httptest.Server) *peer {
	t.Helper()
	addr := srv.Listener.Addr().(*net.TCPAddr)
	p := &peer{id: host.id.ID, name: "Pi", addr: addr.IP, port: addr.Port, shares: true}
	p.pub, _ = parsePublicKey(host.id.ID, host.id.PublicKey())
	guest.mu.Lock()
	guest.peers[p.id] = p
	guest.mu.Unlock()
	return p
}

// A host that answers but whose list of files doesn't come (a server busy
// with a scan) is still online: what this Aniko has of its files stays, and
// the list is asked for again soon.
func TestHostOnlineWhenItsFilesAreLate(t *testing.T) {
	host := hostWithSharing(t)
	guest := hostWithSharing(t)
	var filesOK atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := host.Verify(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/peer/hello":
			_ = json.NewEncoder(w).Encode(Hello{ID: host.id.ID, Name: "Pi", Key: host.id.PublicKey(), Shares: true, Files: "v2"})
		case "/api/peer/files":
			if !filesOK.Load() {
				http.Error(w, "busy", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode([]*library.LocalFile{{Path: "/anime/x.mkv", MediaID: 1, Episode: 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := hostFor(t, guest, host, srv)
	// What it had of the host, from before.
	old := []*library.LocalFile{{Path: "kumo://" + host.id.ID + "/anime/old.mkv", MediaID: 1, Episode: 1}}
	guest.mu.Lock()
	p.files, p.filesVer, p.seen = old, "v1", time.Now().Add(-time.Hour)
	guest.mu.Unlock()

	guest.ask(t.Context(), p)
	guest.mu.Lock()
	online, fails, stale, files := p.online(time.Now()), p.fails, p.filesStale, len(p.files)
	guest.mu.Unlock()
	if !online || fails != 0 {
		t.Fatalf("a host whose files are late: online %v, fails %d", online, fails)
	}
	if !stale || files != 1 {
		t.Fatalf("its files: stale %v, %d kept (want the old one kept)", stale, files)
	}

	// Asked again soon, not in askEvery.
	guest.mu.Lock()
	p.asked = time.Now().Add(-11 * time.Second)
	guest.mu.Unlock()
	filesOK.Store(true)
	guest.askHosts(t.Context())
	deadline := time.Now().Add(5 * time.Second)
	for {
		guest.mu.Lock()
		stale, ver := p.filesStale, p.filesVer
		guest.mu.Unlock()
		if !stale && ver == "v2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the files weren't asked for again: stale %v, version %q", stale, ver)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// One ask that fails doesn't make a host offline; the next one does.
func TestOneFailedAskTolerated(t *testing.T) {
	host := hostWithSharing(t)
	guest := hostWithSharing(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	p := hostFor(t, guest, host, srv)
	guest.mu.Lock()
	p.seen = time.Now().Add(-40 * time.Second)
	guest.mu.Unlock()
	guest.ask(t.Context(), p)
	guest.mu.Lock()
	online1, fails1 := p.online(time.Now()), p.fails
	guest.mu.Unlock()
	if fails1 != 1 || !online1 {
		t.Fatalf("after one failed ask: fails %d, online %v (want 1, true)", fails1, online1)
	}
	guest.ask(t.Context(), p)
	guest.mu.Lock()
	online2, fails2 := p.online(time.Now()), p.fails
	guest.mu.Unlock()
	if fails2 != 2 || online2 {
		t.Fatalf("after two failed asks: fails %d, online %v (want 2, false)", fails2, online2)
	}
}

// Any signed answer from a host (a file read, a position pushed) counts as
// hearing from it: it stays online while it's used, between asks.
func TestSignedAnswerCountsAsHeard(t *testing.T) {
	host := hostWithSharing(t)
	guest := hostWithSharing(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := host.Verify(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	p := hostFor(t, guest, host, srv)
	guest.mu.Lock()
	p.seen = time.Now().Add(-time.Hour)
	guest.mu.Unlock()
	var out map[string]any
	if err := guest.getJSON(t.Context(), p, "/api/peer/local/probe", &out); err != nil {
		t.Fatal(err)
	}
	guest.mu.Lock()
	defer guest.mu.Unlock()
	if !p.online(time.Now()) {
		t.Fatal("a host that just answered a request isn't online")
	}
}

// Settings asking every few seconds doesn't have the hosts asked again
// every few seconds: Refresh is taken at most every refreshEvery, RefreshNow
// always.
func TestRefreshRateLimited(t *testing.T) {
	guest := hostWithSharing(t)
	p := &peer{id: "host1", shares: true, asked: time.Now().Add(-10 * time.Second)}
	guest.mu.Lock()
	guest.peers[p.id] = p
	guest.mu.Unlock()
	guest.Refresh()
	guest.mu.Lock()
	first := p.asked.IsZero()
	p.asked = time.Now().Add(-10 * time.Second)
	guest.mu.Unlock()
	if !first {
		t.Fatal("the first Refresh didn't ask")
	}
	guest.Refresh()
	guest.mu.Lock()
	second := p.asked.IsZero()
	guest.mu.Unlock()
	if second {
		t.Fatal("a Refresh right after the first asked again")
	}
	guest.RefreshNow()
	guest.mu.Lock()
	now := p.asked.IsZero()
	guest.mu.Unlock()
	if !now {
		t.Fatal("RefreshNow didn't ask")
	}
}

// A host that answers but was never heard announcing itself is said so (its
// beacons don't get through: a firewall), after a minute.
func TestHostWithoutBeaconsSaidSo(t *testing.T) {
	guest := hostWithSharing(t)
	now := time.Now()
	p := &peer{id: "host1", name: "PC", shares: true, seen: now, answeredAt: now.Add(-2 * time.Minute)}
	guest.mu.Lock()
	v := guest.view(p, now)
	p.beaconAt = now
	v2 := guest.view(p, now)
	guest.mu.Unlock()
	if !v.NoBeacons {
		t.Error("a host answering for two minutes with no beacon: not said")
	}
	if v2.NoBeacons {
		t.Error("a host whose beacon came: said to have none")
	}
}
