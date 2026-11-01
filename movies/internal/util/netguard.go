package util

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateAddress is returned when something tries to reach the LAN.
var ErrPrivateAddress = errors.New("connections to local/private network addresses are not allowed")

// IsPrivateIP reports loopback, private, link-local, CGNAT and similar ranges,
// also as IPv6 addresses carrying an IPv4 one (mapped, compatible, NAT64).
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT/Tailscale 100.64.0.0/10, "this network" 0.0.0.0/8.
		return v4[0] == 100 && v4[1]&0xc0 == 64 || v4[0] == 0
	}
	if len(ip) != net.IPv6len {
		return false
	}
	switch {
	case isZero(ip[:12]):
		// IPv4-compatible ::a.b.c.d.
		return IsPrivateIP(net.IP(ip[12:16]))
	case isZero(ip[4:12]) && ip[0] == 0 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b:
		// NAT64 64:ff9b::a.b.c.d reaches a.b.c.d.
		return IsPrivateIP(net.IP(ip[12:16]))
	case ip[0] == 0 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b && ip[4] == 0 && ip[5] == 1:
		// The local-use NAT64 prefix 64:ff9b:1::/48 is this network's own.
		return true
	}
	return false
}

// IsLocalHostname reports whether a URL's host names this computer or the
// LAN by itself: a private address, localhost, a single-label name ("nas")
// or a local domain (.local, .lan, .home.arpa...). A public name that
// resolves to the LAN is only caught when connecting (PublicDialContext).
func IsLocalHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return true
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // an IPv6 zone
		host = host[:i]
	}
	if ip := net.ParseIP(host); ip != nil {
		return IsPrivateIP(ip)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".intranet", ".corp"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// proxyHosts are the configured HTTP proxies (allowed even when local).
func proxyHosts() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			host := u.Host
			if u.Port() == "" {
				host = net.JoinHostPort(u.Hostname(), "80")
			}
			out[strings.ToLower(host)] = true
		}
	}
	return out
}

// PublicDialContext dials only public addresses (the configured HTTP proxy
// excepted), checked after DNS resolution so rebinding tricks don't work.
func PublicDialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	proxies := proxyHosts()
	plain := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return checkDialAddress(address)
		},
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxies[strings.ToLower(addr)] {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// checkDialAddress refuses the resolved address a connection is about to
// be made to ("ip:port") when it's this computer or the LAN. An IPv6
// address may come with its interface ("[fe80::1%eth0]:80", which a URL
// can name): it's judged without it, and an address that can't be read
// is refused.
func checkDialAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil || IsPrivateIP(ip) {
		return ErrPrivateAddress
	}
	return nil
}

// PublicProxy is http.ProxyFromEnvironment for the clients that may reach
// only public addresses: through a proxy, the proxy connects to the host, not
// this computer, so PublicDialContext never sees it; the host is checked
// here instead (each request, redirects too).
func PublicProxy(req *http.Request) (*url.URL, error) {
	p, err := http.ProxyFromEnvironment(req)
	if err != nil || p == nil {
		return p, err
	}
	if err := CheckPublicHost(req.Context(), req.URL.Hostname()); err != nil {
		return nil, err
	}
	// A request to a proxy names its Host as where to go, when it has one.
	if req.Host != "" {
		host := req.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if err := CheckPublicHost(req.Context(), host); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// CheckPublicHost fails for a host that is, or resolves to, a local or
// private address. A name this computer can't resolve passes: the proxy
// resolves it.
func CheckPublicHost(ctx context.Context, host string) error {
	host = strings.Trim(strings.ToLower(host), "[]")
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return ErrPrivateAddress
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ErrPrivateAddress
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if IsPrivateIP(ip.IP) {
			return ErrPrivateAddress
		}
	}
	return nil
}
