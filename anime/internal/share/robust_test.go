package share

import (
	"crypto/ecdh"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hostWithSharing is an Aniko with sharing on, to check requests made to it.
func hostWithSharing(t *testing.T) *Service {
	t.Helper()
	s := newService(t, openDB(t))
	cfg := s.settings.Get()
	cfg.Sharing.Enabled = true
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

func newGuest(t *testing.T) *Identity {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return newIdentity(priv)
}

// call has guest ask host (from addr), as signed requests reach it.
func call(t *testing.T, host *Service, guest *Identity, method, target, from string, ticket bool) (Caller, error) {
	t.Helper()
	pub, _ := parsePublicKey(host.id.ID, host.id.PublicKey())
	req := httptest.NewRequest(method, target, nil)
	if _, err := guest.sign(req, host.id.ID, pub, "TV", 43211, 0, time.Now(), ticket); err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = from
	return host.Verify(httptest.NewRecorder(), req)
}

// mpv's tickets are repeated for hours, maybe from another address: they
// don't move where the Aniko is reached.
func TestTicketsKeepTheAddress(t *testing.T) {
	host := hostWithSharing(t)
	guest := newGuest(t)
	if _, err := call(t, host, guest, "GET", "/api/peer/hello", "192.168.1.20:5000", false); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, host, guest, "GET", "/api/peer/local/file?path=x", "192.168.1.66:5000", true); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	p := host.peers[guest.ID]
	addr := p.addr.String()
	host.mu.Unlock()
	if addr != "192.168.1.20" {
		t.Errorf("a ticket moved the address to %s", addr)
	}
}

// Anyone on the network can make up Anikos: only a few unknown ones are
// kept, and none of them gets a large body read.
func TestStrangersAreKeptFew(t *testing.T) {
	host := hostWithSharing(t)
	for i := 0; i < maxStrangers+10; i++ {
		g := newGuest(t)
		host.heard(beacon{Aniko: protocol, ID: g.ID, Name: "x", Port: 43211, Key: g.PublicKey()}, net.ParseIP("192.168.1.30"))
	}
	host.mu.Lock()
	n := len(host.peers)
	host.mu.Unlock()
	if n > maxStrangers {
		t.Errorf("%d Anikos kept", n)
	}

	g := newGuest(t)
	pub, _ := parsePublicKey(host.id.ID, host.id.PublicKey())
	body := strings.Repeat("x", maxUnpairedBody+1)
	req := httptest.NewRequest("POST", "/api/peer/history", strings.NewReader(body))
	if _, err := g.sign(req, host.id.ID, pub, "TV", 43211, 0, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "192.168.1.31:5000"
	if _, err := host.Verify(httptest.NewRecorder(), req); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a large body from a stranger: %v", err)
	}
}

// A host that stopped answering (its address changed) is reached where its
// beacons now come from, at once, even one that proved itself lately or
// was added by hand.
func TestFailingHostFollowsItsBeacon(t *testing.T) {
	guest := hostWithSharing(t)
	host := newGuest(t)
	guest.mu.Lock()
	p := &peer{id: host.ID, addr: net.ParseIP("192.168.1.20"), port: 43211, shares: true, manual: "pc.lan:43211", verified: time.Now(), fails: 1, asked: time.Now().Add(-10 * time.Second)}
	p.pub, _ = parsePublicKey(host.ID, host.PublicKey())
	guest.peers[host.ID] = p
	guest.mu.Unlock()
	guest.heard(beacon{Aniko: protocol, ID: host.ID, Name: "PC", Port: 43211, Key: host.PublicKey()}, net.ParseIP("192.168.1.42"))
	guest.mu.Lock()
	addr, asked := p.addr.String(), p.asked
	guest.mu.Unlock()
	if addr != "192.168.1.42" || !asked.IsZero() {
		t.Errorf("address %s, asked %v", addr, asked)
	}
	// Answering there: one that proved itself lately keeps its address.
	guest.mu.Lock()
	p.fails, p.verified, p.manual = 0, time.Now(), ""
	guest.mu.Unlock()
	guest.heard(beacon{Aniko: protocol, ID: host.ID, Name: "PC", Port: 43211, Key: host.PublicKey()}, net.ParseIP("192.168.1.99"))
	guest.mu.Lock()
	addr = p.addr.String()
	guest.mu.Unlock()
	if addr != "192.168.1.42" {
		t.Errorf("a repeated beacon moved a host that answers to %s", addr)
	}
}

func TestVirtualNetworksLeftOut(t *testing.T) {
	for name, want := range map[string]bool{"docker0": true, "br-1a2b3c": true, "virbr0": true, "veth12ab": true, "vEthernet (WSL)": true, "eth0": false, "wlan0": false, "en0": false, "Wi-Fi": false, "br0": false} {
		if got := virtual(name); got != want {
			t.Errorf("virtual(%q) = %v", name, got)
		}
	}
}

// The host dates its answer when the request comes: the offset is taken
// against when it was sent, not when a slow answer came back.
func TestClockOffsetFromSlowAnswers(t *testing.T) {
	host := hostWithSharing(t)
	guest := hostWithSharing(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := host.Verify(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		time.Sleep(3 * time.Second) // a slow answer
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().(*net.TCPAddr)
	p := &peer{id: host.id.ID, addr: addr.IP, port: addr.Port, shares: true}
	p.pub, _ = parsePublicKey(host.id.ID, host.id.PublicKey())
	guest.mu.Lock()
	guest.peers[p.id] = p
	guest.mu.Unlock()
	// Sharing on, so the host takes the request (loopback counts as local).
	var out map[string]any
	if err := guest.getJSON(t.Context(), p, "/api/peer/hello", &out); err != nil {
		t.Fatal(err)
	}
	guest.mu.Lock()
	off := p.offset
	guest.mu.Unlock()
	if off < -time.Second || off > time.Second {
		t.Errorf("offset %v from a slow answer", off)
	}
}

// What the TV shows when its computer doesn't answer says why, in words,
// not the request (with a file's path in it).
func TestPlainErrors(t *testing.T) {
	c := &http.Client{Timeout: time.Second}
	_, err := c.Get("http://127.0.0.1:1/api/peer/local/file?path=%2Fhome%2Fme%2FAnime%2Fx.mkv")
	if got := plainError(err); strings.Contains(got, "/home") || strings.Contains(got, "http") {
		t.Errorf("plainError(%v) = %q", err, got)
	}
}
