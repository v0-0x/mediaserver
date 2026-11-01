package providers

import (
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strings"
)

// DefaultTrackers are the public trackers added to a magnet link when its
// source lists none (DHT finds peers too, trackers just get there faster).
var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://explodie.org:6969/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://tracker.qu.ax:6969/announce",
}

// NormalizeInfoHash returns h as 40 lowercase hex characters (it may be
// given in hex or base32), or "" when it isn't an info hash.
func NormalizeInfoHash(h string) string {
	h = strings.TrimSpace(h)
	switch len(h) {
	case 40:
		if _, err := hex.DecodeString(h); err == nil {
			return strings.ToLower(h)
		}
	case 32:
		if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h)); err == nil && len(b) == 20 {
			return hex.EncodeToString(b)
		}
	}
	return ""
}

// MagnetURI builds a magnet link for an info hash, with a display name and
// trackers (DefaultTrackers when none is given).
func MagnetURI(infoHash, name string, trackers []string) string {
	h := NormalizeInfoHash(infoHash)
	if h == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(h)
	if name = strings.TrimSpace(name); name != "" {
		b.WriteString("&dn=")
		b.WriteString(url.QueryEscape(name))
	}
	if len(trackers) == 0 {
		trackers = DefaultTrackers
	}
	seen := map[string]bool{}
	for _, tr := range trackers {
		tr = strings.TrimSpace(tr)
		if tr == "" || seen[tr] {
			continue
		}
		seen[tr] = true
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

// InfoHashFromMagnet returns the (hex) info hash of a magnet link, or "".
func InfoHashFromMagnet(magnet string) string {
	u, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		if len(xt) > 9 && strings.EqualFold(xt[:9], "urn:btih:") {
			return NormalizeInfoHash(xt[9:])
		}
	}
	return ""
}
