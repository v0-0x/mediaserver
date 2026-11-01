package freeflix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Videasy (the API of Cineby's player) finds a title by its title and ids
// on several servers, each answering encrypted; enc-dec.app decrypts the
// answers: HLS playlists or files, with subtitles.
var videasySite = &Site{ID: "videasy", Name: "Videasy", DefaultURL: "https://api.videasy.net", Decrypts: true, streams: videasyStreams}

// videasyReferer is the player its CDNs want as Origin and Referer.
const videasyReferer = "https://cineby.gd"

// videasyServers are Videasy's servers, in FreeFlix's order.
var videasyServers = []string{"myflixerzupcloud", "1movies", "moviebox", "primewire", "m4uhd", "hdmovie", "cdn", "primesrcme"}

func videasyStreams(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		return nil, providers.Skip(errors.New("Videasy needs the title's name"))
	}
	base := p.base()
	// The title is encoded twice, as Videasy's player does.
	q := "title=" + pyQuote(pyQuote(title)) + "&mediaType=" + kind(r)
	if r.Year > 0 {
		q += "&year=" + strconv.Itoa(r.Year)
	}
	q += "&tmdbId=" + strconv.Itoa(r.TMDBID)
	if r.IMDbID != "" {
		q += "&imdbId=" + pyQuote(r.IMDbID)
	}
	if r.IsTV() {
		q += fmt.Sprintf("&seasonId=%d&episodeId=%d", r.Season, r.Episode)
	}
	headers := map[string]string{"Accept": "*/*", "Origin": videasyReferer, "Referer": videasyReferer + "/"}
	return each(ctx, videasyServers, func(ctx context.Context, server string) ([]providers.Stream, error) {
		enc, err := p.fetch(ctx, http.MethodGet, base+"/"+server+"/sources-with-title?"+q, headers, nil)
		if err != nil {
			return nil, err
		}
		var res struct {
			Sources []struct {
				Quality string `json:"quality"`
				URL     string `json:"url"`
			} `json:"sources"`
			Subtitles []subtitle `json:"subtitles"`
		}
		if err := p.decrypt(ctx, "dec-videasy", "", map[string]any{"text": strings.TrimSpace(string(enc)), "id": r.TMDBID}, &res); err != nil {
			return nil, err
		}
		subs := subtitles(res.Subtitles)
		var out []providers.Stream
		for _, s := range res.Sources {
			if !isWebLink(s.URL) {
				continue
			}
			t := "Videasy · " + strings.ToUpper(server)
			quality := providers.QualityFromText(s.Quality)
			if quality != "" {
				t += " · " + quality
			}
			out = append(out, providers.Stream{
				Title: t, URL: s.URL, Type: streamType(s.URL, ""), Quality: quality, Subtitles: subs,
				Headers: map[string]string{"Origin": videasyReferer, "Referer": videasyReferer + "/"},
			})
		}
		return out, nil
	})
}

// pyQuote percent-encodes s as Python's urllib.parse.quote does: all but
// letters, digits, "_.-~" and "/".
func pyQuote(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("_.-~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}
