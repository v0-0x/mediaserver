package providers

import (
	"sort"
	"strconv"
	"strings"
)

// Rank returns the streams sorted best first (the input is left alone):
//
//  1. direct links, then torrents, then YouTube videos, then web pages;
//  2. the preferred quality ("1080p", "4K"... "" for none) first;
//  3. then higher qualities first (unknown last);
//  4. then links a browser plays as is first;
//  5. then the most seeded.
//
// Streams that tie keep their order (the providers' own ranking).
func Rank(streams []Stream, preferredQuality string) []Stream {
	out := append([]Stream(nil), streams...)
	pref := qualityRank(QualityFromText(preferredQuality))
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ta, tb := tier(a), tier(b); ta != tb {
			return ta < tb
		}
		qa, qb := qualityRank(a.Quality), qualityRank(b.Quality)
		if pref > 0 {
			if pa, pb := qa == pref, qb == pref; pa != pb {
				return pa
			}
		}
		if qa != qb {
			return qa > qb
		}
		if a.NotWebReady != b.NotWebReady {
			return !a.NotWebReady
		}
		return a.Seeders > b.Seeders
	})
	return out
}

// tier is how a stream plays: 0 direct link, 1 torrent, 2 YouTube, 3 web
// page, 4 nothing usable.
func tier(s Stream) int {
	switch {
	case s.Direct():
		return 0
	case s.InfoHash != "" || s.Magnet != "":
		return 1
	case s.YouTubeID != "":
		return 2
	case s.ExternalURL != "":
		return 3
	}
	return 4
}

// Playable reports whether s has anything to play or open.
func Playable(s Stream) bool { return tier(s) < 4 }

// Dedupe drops the streams already seen earlier in the list: same link,
// same torrent file or same video.
func Dedupe(streams []Stream) []Stream {
	seen := map[string]bool{}
	out := make([]Stream, 0, len(streams))
	for _, s := range streams {
		k := dedupeKey(s)
		if k != "" && seen[k] {
			continue
		}
		if k != "" {
			seen[k] = true
		}
		out = append(out, s)
	}
	return out
}

func dedupeKey(s Stream) string {
	switch {
	case s.Direct():
		return "url:" + s.URL
	case s.InfoHash != "":
		return "bt:" + strings.ToLower(s.InfoHash) + ":" + strconv.Itoa(s.FileIdx)
	case s.Magnet != "":
		return "magnet:" + s.Magnet
	case s.YouTubeID != "":
		return "yt:" + s.YouTubeID
	case s.ExternalURL != "":
		return "ext:" + s.ExternalURL
	}
	return ""
}
