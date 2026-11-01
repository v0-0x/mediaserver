package stream

import "strings"

// CleanLink makes a provider's link a valid URL without changing what it
// asks for, as browsers, mpv and ffmpeg do: bytes a URL can't hold as they
// are (spaces, quotes, non-ASCII...) are percent-encoded, a "%" that
// doesn't start an escape becomes "%25", and everything else stays as
// written. Go's own parsing would otherwise rewrite some links into others
// ("Show%2FSeason 1/" asked for "Show/Season%201/", another path) or send
// a space in the request line; scrapers build links like that all the
// time, and hosts answer them with 404s. The fragment is dropped.
func CleanLink(link string) string {
	link = strings.TrimSpace(link)
	if i := strings.IndexByte(link, '#'); i >= 0 {
		link = link[:i]
	}
	// Leave the scheme and the host alone.
	start := 0
	if i := strings.Index(link, "://"); i >= 0 {
		start = i + 3
		if j := strings.IndexAny(link[start:], "/?"); j >= 0 {
			start += j
		} else {
			return link
		}
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(link) + 16)
	b.WriteString(link[:start])
	for i := start; i < len(link); i++ {
		c := link[i]
		switch {
		case c == '%' && i+2 < len(link) && isHex(link[i+1]) && isHex(link[i+2]):
			b.WriteByte(c)
		case c == '%', c <= ' ', c >= 0x7f, strings.IndexByte("\"<>\\^`{|}", c) >= 0:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
