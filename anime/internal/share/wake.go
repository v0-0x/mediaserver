package share

import (
	"errors"
	"log"
	"net"
	"strings"
	"time"
)

// Waking a host that's asleep (Wake-on-LAN): its network card stays on and
// starts the computer when it gets a "magic packet" with its address. A host
// tells the Anikos it shares with its cards' addresses (Hello.Wake); they keep
// them, and send the packet when asked (or when a stream from it broke), then
// ask it every few seconds until it answers.

// How long a host is waited for after it's been sent the packet.
const wakeFor = 2 * time.Minute

// The ports the packet is usually listened for on.
var wakePorts = []string{"9", "7"}

// macs are this computer's network cards on the home network (not virtual
// ones): what wakes it.
func macs() []string {
	var out []string
	seen := map[string]bool{}
	for _, ifi := range interfaces() {
		if len(ifi.HardwareAddr) != 6 {
			continue
		}
		m := ifi.HardwareAddr.String()
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// magicPacket is the Wake-on-LAN packet for a card: 6 times 0xFF, then its
// address 16 times.
func magicPacket(mac net.HardwareAddr) []byte {
	b := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		b = append(b, 0xFF)
	}
	for i := 0; i < 16; i++ {
		b = append(b, mac...)
	}
	return b
}

// broadcasts are where the packet goes: everyone on this computer's networks
// (the host is on one of them), and the host's last address.
func broadcasts(last net.IP) []string {
	out := []string{"255.255.255.255"}
	for _, ifi := range interfaces() {
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			// Not all are networks (Windows gives some as addresses alone).
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || len(ipn.Mask) != 4 {
				continue
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^ipn.Mask[i]
			}
			out = append(out, b.String())
		}
	}
	if ip := last.To4(); ip != nil {
		out = append(out, ip.String())
	}
	return out
}

// sendWake sends the magic packet for each card, everywhere, to the ports
// it's usually listened for on (9 and 7).
func sendWake(cards []string, last net.IP) error {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()
	sent := 0
	var lastErr error
	for _, c := range cards {
		mac, err := net.ParseMAC(c)
		if err != nil || len(mac) != 6 {
			continue
		}
		pkt := magicPacket(mac)
		for _, to := range broadcasts(last) {
			for _, port := range wakePorts {
				addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(to, port))
				if err != nil {
					continue
				}
				if _, err := conn.WriteTo(pkt, addr); err != nil {
					lastErr = err
					continue
				}
				sent++
			}
		}
	}
	if sent == 0 {
		if lastErr == nil {
			lastErr = errors.New("no network card address to wake")
		}
		return lastErr
	}
	return nil
}

// waking: it was sent the packet a moment ago, and hasn't answered since.
func (p *peer) waking(now time.Time) bool { return now.Before(p.wakeUntil) }

// Wake wakes a host that shares its library with this Aniko, and asks it
// until it answers (for a while).
func (s *Service) Wake(id string) error {
	s.mu.Lock()
	p := s.peers[id]
	if p == nil || !p.shares {
		s.mu.Unlock()
		return errors.New("that Aniko doesn't share its library with this one")
	}
	cards, last, name := p.wakeMACs, p.addr, p.displayName()
	s.mu.Unlock()
	if len(cards) == 0 {
		return errors.New(name + " hasn't said how to wake it yet: update Aniko there, and leave it on until this device has heard from it once")
	}
	if err := sendWake(cards, last); err != nil {
		return errors.New("couldn't send the wake-up call: " + plainError(err))
	}
	s.mu.Lock()
	p.wakeUntil, p.asked = time.Now().Add(wakeFor), time.Time{}
	s.mu.Unlock()
	log.Printf("sharing: waking %s", name)
	s.wake()
	s.hub.Publish("sharing-updated", nil)
	// Not answered by then: the app stops saying it's waking.
	time.AfterFunc(wakeFor+time.Second, func() { s.hub.Publish("sharing-updated", nil) })
	// Some cards miss the first one, or the computer is between sleep and
	// awake: again a bit later, while it doesn't answer.
	go func() {
		for _, d := range []time.Duration{5 * time.Second, 15 * time.Second} {
			time.Sleep(d)
			s.mu.Lock()
			still := p.waking(time.Now()) && p.fails > 0
			s.mu.Unlock()
			if !still {
				return
			}
			_ = sendWake(cards, last)
		}
	}()
	return nil
}

// WakeFor wakes the host of a shared file (see Wake).
func (s *Service) WakeFor(path string) error {
	host, _, ok := ParsePath(path)
	if !ok {
		return errors.New("not a shared file")
	}
	return s.Wake(host)
}

// Sleeping is a host that shares its library with this Aniko and doesn't
// answer: asleep or off.
type Sleeping struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	CanWake bool   `json:"canWake"`
	Waking  bool   `json:"waking"`
}

// Asleep lists the hosts that share with this Aniko and don't answer (sharing
// on only).
func (s *Service) Asleep() []Sleeping {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Sleeping{}
	if s.running == nil {
		return out
	}
	for _, p := range s.peers {
		// Not before it was asked and didn't answer (twice: a blip isn't
		// sleep); or while it's being woken.
		if !p.shares || p.fails < 2 && !p.waking(now) {
			continue
		}
		out = append(out, Sleeping{ID: p.id, Name: p.displayName(), CanWake: len(p.wakeMACs) > 0, Waking: p.waking(now)})
	}
	return out
}

// cleanMACs keeps what a host sent as its cards' addresses that are ones.
func cleanMACs(in []string) []string {
	var out []string
	for _, m := range in {
		if mac, err := net.ParseMAC(strings.TrimSpace(m)); err == nil && len(mac) == 6 && len(out) < 8 {
			out = append(out, mac.String())
		}
	}
	return out
}
