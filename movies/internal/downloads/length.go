package downloads

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// How long an episode is, for an ffmpeg download's percentage: ffmpeg
// says how much it has written, not how much there is.

// playlistClient fetches HLS playlists: the shared client for public
// services (tests swap it for one that reaches their own server).
var playlistClient = util.HTTP

// maxPlaylist caps a playlist read; a long episode's is a few hundred KB.
const maxPlaylist = 4 << 20

// hlsLength adds up the segment durations of the HLS playlist at res.URL
// (a master playlist's first variant). ffprobe isn't needed for it, and
// ffprobe can fail where ffmpeg doesn't: ffmpeg 7.1 refuses segments named
// like images unless told otherwise. 0 when it isn't a playlist or can't
// be read.
func hlsLength(ctx context.Context, res *Resolved) float64 {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := res.URL
	// A master playlist, then its variant.
	for range 2 {
		body, final, err := getPlaylist(ctx, u, res)
		if err != nil {
			return 0
		}
		total, variant, ok := parsePlaylist(body)
		if !ok || total > 0 || variant == "" {
			return total
		}
		ref, err := url.Parse(variant)
		if err != nil {
			return 0
		}
		u = final.ResolveReference(ref).String()
	}
	return 0
}

// getPlaylist fetches a playlist as ffmpeg would (same referrer, user agent
// and headers), and the address it came from after redirects.
func getPlaylist(ctx context.Context, u string, res *Resolved) (string, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", nil, err
	}
	for k, v := range res.Headers {
		if !strings.EqualFold(k, "referer") && !strings.EqualFold(k, "user-agent") {
			req.Header.Set(k, v)
		}
	}
	if res.Referrer != "" {
		req.Header.Set("Referer", res.Referrer)
	}
	req.Header.Set("User-Agent", util.UserAgent)
	resp, err := playlistClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("playlist: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylist))
	if err != nil {
		return "", nil, err
	}
	return string(b), resp.Request.URL, nil
}

// parsePlaylist reads an HLS playlist: the total of its segments'
// #EXTINF durations, or for a master playlist the first variant's address.
// ok is false when it isn't a playlist at all (an .mp4, an error page).
func parsePlaylist(body string) (total float64, variant string, ok bool) {
	sc := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(body, "\ufeff")))
	sc.Buffer(make([]byte, 0, 64<<10), maxPlaylist)
	first, nextIsVariant := true, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			if line != "#EXTM3U" {
				return 0, "", false
			}
			first = false
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			d, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if s, err := strconv.ParseFloat(strings.TrimSpace(d), 64); err == nil && s > 0 {
				total += s
			}
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF"):
			nextIsVariant = true
		case !strings.HasPrefix(line, "#") && nextIsVariant:
			if variant == "" {
				variant = line
			}
			nextIsVariant = false
		}
	}
	return total, variant, !first
}

// clock writes seconds as 4:05, or 1:02:05 from an hour.
func clock(s float64) string {
	t := max(int(s+0.5), 0)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
