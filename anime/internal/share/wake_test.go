package share

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMagicPacket(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	p := magicPacket(mac)
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatalf("packet %x", p)
	}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(p[6+i*6:12+i*6], mac) {
			t.Fatalf("copy %d of the address: %x", i, p[6+i*6:12+i*6])
		}
	}
	if got := cleanMACs([]string{"AA-BB-CC-DD-EE-FF", "nope", "00:11:22:33:44:55:66:77"}); strings.Join(got, ",") != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("cleanMACs: %v", got)
	}
}

// A host asleep is sent the packet (here where it was last: this
// computer), and asked until it answers; one that never said its cards
// can't be.
func TestWakeSendsThePacket(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	old := wakePorts
	wakePorts = []string{strconv.Itoa(conn.LocalAddr().(*net.UDPAddr).Port)}
	t.Cleanup(func() { wakePorts = old })

	s := hostWithSharing(t)
	host := newGuest(t)
	p := &peer{id: host.ID, name: "PC", shares: true, addr: net.ParseIP("127.0.0.1"), port: 43211, fails: 3, wakeMACs: []string{"aa:bb:cc:dd:ee:ff"}}
	s.mu.Lock()
	s.peers[p.id] = p
	s.running = &run{wake: make(chan struct{}, 1)}
	s.mu.Unlock()
	if got := s.Asleep(); len(got) != 1 || !got[0].CanWake || got[0].Waking || got[0].Name != "PC" {
		t.Fatalf("asleep: %+v", got)
	}
	if err := s.Wake(p.id); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 200)
	n, _, err := conn.ReadFrom(buf)
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	if err != nil || !bytes.Equal(buf[:n], magicPacket(mac)) {
		t.Fatalf("got %d bytes %x, %v", n, buf[:n], err)
	}
	if got := s.Asleep(); len(got) != 1 || !got[0].Waking {
		t.Errorf("not waking: %+v", got)
	}
	s.mu.Lock()
	asked := p.asked
	s.mu.Unlock()
	if !asked.IsZero() {
		t.Error("not asked again at once")
	}

	// A file of its: the same.
	if err := s.WakeFor(RemotePath(p.id, "/anime/x.mkv")); err != nil {
		t.Errorf("wake for a file: %v", err)
	}
	// Never told its cards.
	s.mu.Lock()
	p.wakeMACs = nil
	s.mu.Unlock()
	if err := s.Wake(p.id); err == nil || !strings.Contains(err.Error(), "update Aniko there") {
		t.Errorf("woke a host that never said how: %v", err)
	}
	if err := s.Wake("nobody"); err == nil {
		t.Error("woke an Aniko that doesn't share")
	}
}

// The cards a host says (Hello) are kept, and saved with it.
func TestWakeCardsKept(t *testing.T) {
	d := openDB(t)
	s := newService(t, d)
	g := newGuest(t)
	pub, _ := parsePublicKey(g.ID, g.PublicKey())
	s.mu.Lock()
	s.peers[g.ID] = &peer{id: g.ID, pub: pub, shares: true, wakeMACs: []string{"aa:bb:cc:dd:ee:ff"}}
	s.save()
	s.mu.Unlock()
	again := newService(t, d)
	again.mu.Lock()
	got := again.peers[g.ID].wakeMACs
	again.mu.Unlock()
	if strings.Join(got, ",") != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("after a restart: %v", got)
	}
}
