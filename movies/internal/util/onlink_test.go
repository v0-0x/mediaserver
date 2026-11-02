package util

import (
	"net"
	"testing"
	"time"
)

// Another address on one of this computer's own subnets is home; addresses
// elsewhere on the internet aren't.
func TestOnLink(t *testing.T) {
	if OnLink(net.ParseIP("8.8.8.8")) || OnLink(net.ParseIP("2001:4860:4860::8888")) || OnLink(nil) {
		t.Fatal("an internet address counted as home")
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() {
			continue
		}
		ones, bits := n.Mask.Size()
		if ones == bits || (bits == 32 && ones < 8) || (bits == 128 && ones < 48) {
			continue
		}
		// The next address on the same subnet.
		other := make(net.IP, len(n.IP))
		copy(other, n.IP)
		other[len(other)-1] ^= 1
		if n.Contains(other) && !OnLink(other) {
			t.Fatalf("%s is on %s but didn't count", other, n)
		}
	}
}

// A Mac or a phone on a home network's IPv6 (a 2001:… address from the
// provider, not a "private" one) counts once this computer is on that
// network; the same kind of address elsewhere doesn't.
func TestOnLinkIPv6Home(t *testing.T) {
	_, home, _ := net.ParseCIDR("2001:db8:88::1/64")
	onLink.Lock()
	saved, savedAt := onLink.nets, onLink.at
	onLink.nets, onLink.at = []*net.IPNet{home}, time.Now()
	onLink.Unlock()
	defer func() {
		onLink.Lock()
		onLink.nets, onLink.at = saved, savedAt
		onLink.Unlock()
	}()
	if !OnLink(net.ParseIP("2001:db8:88::abcd:1234")) {
		t.Fatal("a device on the home IPv6 network didn't count")
	}
	for _, ip := range []string{"2001:db8:89::2", "2a00:1450:4001:80b::200e", "8.8.8.8"} {
		if OnLink(net.ParseIP(ip)) {
			t.Fatalf("%s counted as home", ip)
		}
	}
}
