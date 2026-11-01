package freeflix

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Vidlink takes the TMDB id encrypted (by enc-dec.app) and answers an HLS
// playlist, or MP4 files in several qualities with their subtitles.
var vidlinkSite = &Site{ID: "vidlink", Name: "Vidlink", DefaultURL: "https://vidlink.pro", Decrypts: true, streams: vidlinkStreams}

func vidlinkStreams(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	var enc string
	if err := p.decrypt(ctx, "enc-vidlink", "text="+url.QueryEscape(strconv.Itoa(r.TMDBID)), nil, &enc); err != nil {
		return nil, err
	}
	enc = strings.TrimSpace(enc)
	if enc == "" {
		return nil, fmt.Errorf("enc-dec.app: no result")
	}
	base := p.base()
	u := base + "/api/b/movie/" + enc
	if r.IsTV() {
		u = fmt.Sprintf("%s/api/b/tv/%s/%d/%d", base, enc, r.Season, r.Episode)
	}
	headers := map[string]string{"Referer": base + "/", "Origin": base}
	var v struct {
		Stream struct {
			Playlist  string `json:"playlist"`
			Qualities map[string]struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"qualities"`
			Captions []subtitle `json:"captions"`
		} `json:"stream"`
	}
	if err := p.fetchJSON(ctx, http.MethodGet, u, headers, nil, &v); err != nil {
		return nil, err
	}
	subs := subtitles(v.Stream.Captions)
	if isWebLink(v.Stream.Playlist) {
		return []providers.Stream{{
			Title: "Vidlink", URL: v.Stream.Playlist, Type: providers.StreamM3U8, Headers: maps.Clone(headers), Subtitles: subs,
		}}, nil
	}
	// The files, best first.
	type file struct {
		height int
		providers.Stream
	}
	var files []file
	for q, f := range v.Stream.Qualities {
		if !isWebLink(f.URL) {
			continue
		}
		h, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(q), "p"))
		quality := providers.QualityFromHeight(h)
		title := "Vidlink"
		if quality != "" {
			title += " · " + quality
		}
		files = append(files, file{h, providers.Stream{
			Title: title, URL: f.URL, Type: streamType(f.URL, f.Type), Quality: quality, Headers: maps.Clone(headers), Subtitles: subs,
		}})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].height != files[j].height {
			return files[i].height > files[j].height
		}
		return files[i].URL < files[j].URL
	})
	var out []providers.Stream
	for _, f := range files {
		out = append(out, f.Stream)
	}
	return out, nil
}
