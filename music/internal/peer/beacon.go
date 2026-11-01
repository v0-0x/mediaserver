package peer

import (
	"encoding/json"
	"net"
	"strings"

	"golang.org/x/net/ipv4"
)

// The music side is found on the home network as the other sides are: by a
// small message (a beacon) sent every few seconds to the multicast group
// every side uses, on its own port (anime 43212, movies & TV 43312). Utako
// listens there (src/hub/discovery.rs).
const DiscoveryPort = 43412

var group = net.IPv4(239, 255, 75, 77)

// Beacon is the message: who sends it, and where its server is.
type Beacon struct {
	Protocol int    `json:"kumo"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Key      string `json:"key"`
	Version  string `json:"version"`
	Platform string `json:"platform,omitempty"`
	Role     string `json:"role,omitempty"`
}

// Announcer sends the beacon.
type Announcer struct {
	conn net.PacketConn
	pc   *ipv4.PacketConn
}

// NewAnnouncer opens a socket to send beacons from (on any port: the music
// side listens for none).
func NewAnnouncer() (*Announcer, error) {
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastTTL(1)         // this network only
	_ = pc.SetMulticastLoopback(true) // a Utako on this computer
	return &Announcer{conn: conn, pc: pc}, nil
}

// Announce sends b on every interface the home network is reached through.
func (a *Announcer) Announce(b Beacon) {
	data, _ := json.Marshal(b)
	dst := &net.UDPAddr{IP: group, Port: DiscoveryPort}
	for _, ifi := range interfaces() {
		if err := a.pc.SetMulticastInterface(&ifi); err != nil {
			continue
		}
		_, _ = a.pc.WriteTo(data, nil, dst)
	}
}

// Close stops it.
func (a *Announcer) Close() { _ = a.conn.Close() }

// interfaces are the ones other computers can reach this one through.
func interfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 || virtual(ifi.Name) {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && IsLocalNetwork(ipn.IP) {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

// virtual reports a network of containers or virtual machines on this
// computer: no device is there.
func virtual(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vboxnet", "vmnet", "podman", "cni", "flannel", "lxcbr", "lxdbr", "tailscale", "zt"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// IsLocalNetwork reports addresses of the home network (or this computer,
// or Tailscale's): the only ones the music side talks to.
func IsLocalNetwork(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64
}
