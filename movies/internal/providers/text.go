package providers

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Sources describe themselves with free text ("Torrentio\n4k",
// "Movie.2160p.mkv\n👤 12 💾 5.3 GB ⚙️ YTS", "1080p · 1.4GB"...): these
// helpers read the useful bits out of it.

var (
	reResolution = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(4320|2160|1440|1080|720|576|540|480|360|240)\s*[pi](?:$|[^a-z])`)
	reBareRes    = regexp.MustCompile(`^(4320|2160|1440|1080|720|576|540|480|360|240)$`)
	re8K         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])8k(?:$|[^a-z0-9])`)
	re4K         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:4k|uhd|ultra\s*hd)(?:$|[^a-z0-9])`)
	reQHD        = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:2k|qhd)(?:$|[^a-z0-9])`)
	reFHD        = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:fhd|full\s*hd)(?:$|[^a-z0-9])`)
	reHD         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])hd(?:$|[^a-z0-9])`)
	reSD         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])sd(?:$|[^a-z0-9])`)
	reDimensions = regexp.MustCompile(`(?:^|[^0-9])\d{3,4}\s*[x×]\s*(\d{3,4})(?:$|[^0-9])`)

	reSize    = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(tib|tb|gib|gb|mib|mb|kib|kb)(?:$|[^a-z])`)
	reSeeders = regexp.MustCompile(`(?i)(?:👤|👥|🌱|⬆️|⬆|🔼|seeders?|seeds?)\s*[:=]?\s*(\d+)`)
)

// QualityFromText finds the video quality in s: "1080p", "720p", "4K" or
// "UHD" (2160p), "1920x1080"... It returns "" when s says nothing about it.
func QualityFromText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if m := reResolution.FindStringSubmatch(s); m != nil {
		return m[1] + "p"
	}
	if m := reBareRes.FindStringSubmatch(s); m != nil {
		return m[1] + "p"
	}
	switch {
	case re8K.MatchString(s):
		return "4320p"
	case re4K.MatchString(s):
		return "2160p"
	case reQHD.MatchString(s):
		return "1440p"
	case reFHD.MatchString(s):
		return "1080p"
	}
	if m := reDimensions.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		return QualityFromHeight(h)
	}
	switch {
	case reHD.MatchString(s):
		return "720p"
	case reSD.MatchString(s):
		return "480p"
	}
	return ""
}

// QualityFromHeight maps a video height in pixels to the usual quality
// name (a 1920x800 scope movie is still "1080p").
func QualityFromHeight(h int) string {
	switch {
	case h <= 0:
		return ""
	case h >= 3000:
		return "4320p"
	case h >= 1600:
		return "2160p"
	case h >= 1300:
		return "1440p"
	case h >= 780:
		return "1080p"
	case h >= 600:
		return "720p"
	case h >= 530:
		return "576p"
	case h >= 400:
		return "480p"
	case h >= 300:
		return "360p"
	}
	return "240p"
}

// qualityRank orders qualities: higher is better, 0 for unknown.
func qualityRank(q string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(q), "p"))
	if err != nil {
		return 0
	}
	return n
}

// SizeFromText finds a file size in s ("💾 1.2 GB", "1.4GB", "700 MiB",
// "1,4 GB"...) and returns it in bytes (binary multiples, as the sources
// compute them), or 0.
func SizeFromText(s string) int64 {
	m := reSize.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	num := m[1]
	if i := strings.IndexByte(num, ','); i >= 0 && len(num)-i-1 == 3 {
		num = strings.ReplaceAll(num, ",", "") // 1,400 MB
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", "."), 64)
	if err != nil || n < 0 {
		return 0
	}
	mult := float64(1)
	switch strings.ToLower(m[2]) {
	case "tb", "tib":
		mult = 1 << 40
	case "gb", "gib":
		mult = 1 << 30
	case "mb", "mib":
		mult = 1 << 20
	case "kb", "kib":
		mult = 1 << 10
	}
	return int64(n * mult)
}

// SeedersFromText finds a seeder count in s ("👤 123", "Seeders: 45"), or
// returns 0.
func SeedersFromText(s string) int {
	m := reSeeders.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// GuessType guesses a stream's type from its URL (or file name).
func GuessType(raw string) string {
	p := raw
	// A file name may hold '#' or '?': only URLs are parsed.
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			p = u.Path
		}
	}
	p = strings.ToLower(p)
	switch ext := path.Ext(p); {
	case ext == ".m3u8" || ext == ".m3u" || strings.Contains(p, "m3u8"):
		return StreamM3U8
	case ext == ".mp4" || ext == ".m4v" || ext == ".mov":
		return StreamMP4
	case ext == ".mkv":
		return StreamMKV
	case ext == ".webm":
		return StreamWebM
	}
	return StreamUnknown
}
