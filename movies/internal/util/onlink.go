package util

import (
	"net"
	"sync"
	"time"
)

// OnLink reports an address on one of the networks this computer is on (its
// interfaces' own subnets): another device at home, whatever kind of address
// it uses. IPv6 home networks give devices addresses that aren't "private"
// (2001:… from the internet provider), which phones and Macs often use to
// reach a .local name; they're home all the same. An address from outside
// can't be on these networks. Subnets wider than /8 (IPv4) or /48 (IPv6)
// don't count.
func OnLink(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	for _, n := range localNets() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

var onLink struct {
	sync.Mutex
	nets []*net.IPNet
	at   time.Time
}

// localNets are this computer's subnets, looked up again every 10 seconds
// (a Wi-Fi that comes and goes, an address that changes).
func localNets() []*net.IPNet {
	onLink.Lock()
	defer onLink.Unlock()
	if time.Since(onLink.at) < 10*time.Second {
		return onLink.nets
	}
	var out []*net.IPNet
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() {
			continue
		}
		ones, bits := n.Mask.Size()
		if (bits == 32 && ones < 8) || (bits == 128 && ones < 48) {
			continue
		}
		out = append(out, n)
	}
	onLink.nets, onLink.at = out, time.Now()
	return out
}
