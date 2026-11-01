package util

import (
	"errors"
	"net"
	"testing"
)

// The guard sees the address a connection is made to, once resolved: every
// form of this computer's and the LAN's addresses is refused there.
func TestCheckDialAddress(t *testing.T) {
	for addr, private := range map[string]bool{
		"93.184.216.34:443":                false,
		"[2606:2800:220:1::248]:443":       false,
		"[64:ff9b::5db8:d822]:80":          false, // NAT64 of a public address
		"127.0.0.1:80":                     true,
		"10.1.2.3:80":                      true,
		"192.168.1.1:80":                   true,
		"172.16.0.1:80":                    true,
		"169.254.169.254:80":               true,
		"100.64.0.1:80":                    true,
		"0.0.0.0:80":                       true,
		"0.1.2.3:80":                       true,
		"[::]:80":                          true,
		"[::1]:80":                         true,
		"[::ffff:127.0.0.1]:80":            true,
		"[::ffff:192.168.1.1]:80":          true,
		"[::127.0.0.1]:80":                 true, // IPv4-compatible
		"[fc00::1]:80":                     true,
		"[fd12:3456::1]:80":                true,
		"[fe80::1]:80":                     true,
		"[fe80::1%eth0]:80":                true, // link-local with its interface
		"[fe80::1%12]:80":                  true, // (Windows names it by number)
		"[ff02::1%eth0]:80":                true,
		"[64:ff9b::c0a8:101]:80":           true, // NAT64 of 192.168.1.1
		"[64:ff9b:1::a00:1]:80":            true, // local-use NAT64
		"[64:ff9b::7f00:1%eth0]:80":        true,
		"[2001:db8::1%eth0]:80":            false,
		"[fe80::1%25eth0]:80":              true,
		"[FE80::abcd:1234:5678:9%en0]:443": true,
	} {
		err := checkDialAddress(addr)
		if got := errors.Is(err, ErrPrivateAddress); got != private {
			t.Errorf("checkDialAddress(%q) = %v, want private %v", addr, err, private)
		}
	}
	if !IsPrivateIP(net.ParseIP("fe80::1")) || IsPrivateIP(net.ParseIP("8.8.8.8")) {
		t.Error("IsPrivateIP")
	}
}
