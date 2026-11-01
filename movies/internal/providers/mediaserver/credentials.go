package mediaserver

import (
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Tokens
//
// The links of a server's videos carry no token: they're listed to every
// device, handed to the browser and to mpv. KumoBox adds the token when it
// fetches one of them itself (stream proxy, relay, mpv, downloads), and
// only for the server's video paths: the stream proxy fetches what devices
// on the LAN ask it to, and must not become a way to call the server's API
// with the user's token.

var (
	// Jellyfin and Emby: /Videos/{id}/stream (Emby also under /emby).
	reJellyfinVideo = regexp.MustCompile(`(?i)^(/emby)?/videos/[^/]+/stream(\.[a-z0-9]+)?$`)
	// Plex: /library/parts/{id}/{timestamp}/file.mkv
	rePlexPart = regexp.MustCompile(`^/library/parts/\d+/\d+/[^/]+$`)
)

// Credentials returns the headers authenticating a request for u with the
// server of servers it's on, when u is one of that server's video links;
// nil for any other link.
func Credentials(servers []Config, u *url.URL) map[string]string {
	if u == nil {
		return nil
	}
	// No ".." or doubled slashes: the server could resolve them to
	// another of its paths.
	if u.Path == "" || path.Clean(u.Path) != u.Path || strings.Contains(u.Path, "..") {
		return nil
	}
	for _, c := range servers {
		b, err := c.base()
		if err != nil || !strings.EqualFold(b.Scheme, u.Scheme) || hostKey(b) != hostKey(u) {
			continue
		}
		prefix := strings.TrimRight(b.Path, "/")
		if len(u.Path) < len(prefix) || !strings.EqualFold(u.Path[:len(prefix)], prefix) {
			continue
		}
		if videoPath(c.Kind, u.Path[len(prefix):]) {
			return authHeaders(c)
		}
	}
	return nil
}

// videoPath reports whether rel (a path under the server's address) is one
// of a server's video files.
func videoPath(kind, rel string) bool {
	if kind == Plex {
		return rePlexPart.MatchString(rel)
	}
	return reJellyfinVideo.MatchString(rel)
}

// Hosts are the servers' addresses as "host:port" (the port filled in from
// the scheme): the stream proxy and relay may reach them although they're
// on this computer or the LAN.
func Hosts(servers []Config) []string {
	var out []string
	for _, c := range servers {
		if b, err := c.base(); err == nil {
			out = append(out, hostKey(b))
		}
	}
	return out
}

// hostKey is a URL's host and port, the port filled in from the scheme
// (as stream.HostKey has it).
func hostKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
