package torrent

import (
	"fmt"
	"net/url"
)

// SearchResult is a torrent found by a search source (sources.go) or an
// extension's torrent provider (extsource.go).
type SearchResult struct {
	// Provider is the id of the source that found it; SearchAll lists every
	// source that found the same torrent, comma-separated (ProviderIDs).
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	Link          string `json:"link"` // the torrent's page
	DownloadURL   string `json:"downloadUrl"`
	MagnetLink    string `json:"magnetLink"`
	InfoHash      string `json:"infoHash"`
	Size          int64  `json:"size"`
	FormattedSize string `json:"formattedSize"`
	Seeders       int    `json:"seeders"`
	Leechers      int    `json:"leechers"`
	DownloadCount int    `json:"downloadCount"`
	Date          string `json:"date"` // RFC 3339
	Resolution    string `json:"resolution"`
	ReleaseGroup  string `json:"releaseGroup"`
	// EpisodeNumber is the key (season*1000+episode) of the one episode
	// the torrent holds, -1 when it isn't one episode or it's unknown.
	EpisodeNumber int `json:"episodeNumber"`
	// IsBatch marks packs: several episodes, a season, a whole show.
	IsBatch       bool `json:"isBatch"`
	IsBestRelease bool `json:"isBestRelease"`
	Confirmed     bool `json:"confirmed"`
	Trusted       bool `json:"trusted"`
	Remake        bool `json:"remake"`
	Dub           bool `json:"dub"`
}

// Trackers are added to the magnet links KumoBox makes from info hashes.
var Trackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://open.demonii.com:1337/announce",
}

// MagnetFromHash makes a magnet link from an info hash, with Trackers.
func MagnetFromHash(hash, name string) string {
	v := url.Values{}
	v.Set("dn", name)
	for _, t := range Trackers {
		v.Add("tr", t)
	}
	return "magnet:?xt=urn:btih:" + hash + "&" + v.Encode()
}

// Enrich fills what a result's name tells and its provider didn't say:
// resolution, release group, episode key, whether it's a pack, the size
// written out.
func Enrich(r *SearchResult) { annotate(r) }

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
