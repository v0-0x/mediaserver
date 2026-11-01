package peer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The vectors Utako's tests check its own copy of the protocol against
// (src/hub/testdata/mediaserver-vectors.json, made by the anime side's
// identity.go): the music side must take what Utako sends.
type vectors struct {
	Body          string              `json:"body"`
	GuestID       string              `json:"guest_id"`
	GuestKey      string              `json:"guest_key"`
	GuestPrivate  string              `json:"guest_private"`
	HostID        string              `json:"host_id"`
	HostKey       string              `json:"host_key"`
	HostPrivate   string              `json:"host_private"`
	Headers       map[string][]string `json:"headers"`
	TicketHeaders map[string][]string `json:"ticket_headers"`
	Reply         string              `json:"reply"`
	Sealed        string              `json:"sealed"`
}

func load(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/utako-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestUtakosRequestsAreTaken(t *testing.T) {
	v := load(t)
	host, err := FromPrivate(v.HostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if host.ID != v.HostID || host.PublicKey() != v.HostKey {
		t.Fatalf("host %s %s", host.ID, host.PublicKey())
	}
	at := time.Unix(1793500000, 0)

	// The signed request: POST /api/peer/hub/prefs?x=1&y=two%20words.
	r := httptest.NewRequest("POST", "/api/peer/hub/prefs?x=1&y=two%20words", strings.NewReader(v.Body))
	r.Header = http.Header(v.Headers)
	c, sig, err := host.Verify(r, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != v.GuestID || c.Name != "Utako on iPhone, 2" || c.User != 7 || c.Ticket {
		t.Fatalf("caller %+v", c)
	}
	reply, err := sig.Reply("Sun, 01 Nov 2026 12:00:00 GMT")
	if err != nil || reply != v.Reply {
		t.Fatalf("reply %s, want %s (%v)", reply, v.Reply, err)
	}
	// The same again: refused.
	r = httptest.NewRequest("POST", "/api/peer/hub/prefs?x=1&y=two%20words", strings.NewReader(v.Body))
	r.Header = http.Header(v.Headers)
	if _, _, err := host.Verify(r, at.Add(time.Minute)); err != ErrReplay {
		t.Fatalf("sent again: %v", err)
	}
	// Another body: refused.
	r = httptest.NewRequest("POST", "/api/peer/hub/prefs?x=1&y=two%20words", strings.NewReader(`{"a":2}`))
	r.Header = http.Header(v.Headers)
	if _, _, err := host.Verify(r, at.Add(time.Minute)); err == nil {
		t.Fatal("another body taken")
	}

	// A ticket, hours later, again and again; not for another file.
	for i := 0; i < 2; i++ {
		r = httptest.NewRequest("GET", "/api/peer/local/file?path=%2Fmusic%2Fa%20b.flac", nil)
		r.Header = http.Header(v.TicketHeaders).Clone()
		r.Header.Set("Range", "bytes=10-")
		c, _, err := host.Verify(r, at.Add(3*time.Hour))
		if err != nil || !c.Ticket {
			t.Fatalf("ticket: %v %+v", err, c)
		}
	}
	r = httptest.NewRequest("GET", "/api/peer/local/file?path=%2Fmusic%2Fother.flac", nil)
	r.Header = http.Header(v.TicketHeaders).Clone()
	if _, _, err := host.Verify(r, at); err == nil {
		t.Fatal("a ticket opened another file")
	}
	r = httptest.NewRequest("GET", "/api/peer/local/file?path=%2Fmusic%2Fa%20b.flac", nil)
	r.Header = http.Header(v.TicketHeaders).Clone()
	if _, _, err := host.Verify(r, at.Add(13*time.Hour)); err != ErrClock {
		t.Fatalf("an old ticket: %v", err)
	}

	// What the anime side sealed for Utako opens with the same key here.
	guest, _ := FromPrivate(v.GuestPrivate)
	pub, err := ParsePublicKey(guest.ID, guest.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := host.OpenFrom(pub, guest.ID, host.ID, v.Sealed)
	if err != nil || string(plain) != "secret login" {
		t.Fatalf("opened %q (%v)", plain, err)
	}
}

func TestSealedBothWays(t *testing.T) {
	dir := t.TempDir()
	server, err := Load(filepath.Join(dir, "id.json"))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Load(filepath.Join(dir, "id.json"))
	if again.ID != server.ID {
		t.Fatal("the identity isn't kept")
	}
	if st, _ := os.Stat(filepath.Join(dir, "id.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	device, _ := Load(filepath.Join(dir, "device.json"))
	devicePub, _ := ParsePublicKey(device.ID, device.PublicKey())
	serverPub, _ := ParsePublicKey(server.ID, server.PublicKey())
	// Utako seals for the server: guest = the server, host = the device.
	sealed, err := device.SealFor(serverPub, server.ID, device.ID, []byte("settings"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := server.OpenFrom(devicePub, server.ID, device.ID, sealed)
	if err != nil || string(plain) != "settings" {
		t.Fatalf("%q %v", plain, err)
	}
	// The server seals for the device: guest = the device, host = the server.
	back, _ := server.SealFor(devicePub, device.ID, server.ID, []byte("settings"))
	plain, err = device.OpenFrom(serverPub, device.ID, server.ID, back)
	if err != nil || string(plain) != "settings" {
		t.Fatalf("%q %v", plain, err)
	}
}
