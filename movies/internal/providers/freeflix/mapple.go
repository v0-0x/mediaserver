package freeflix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Mapple hands out a request token, then a stream link for each of its
// sources (an HLS playlist), which the token opens.
var mappleSite = &Site{ID: "mapple", Name: "Mapple", DefaultURL: "https://mapple.uk", streams: mappleStreams}

// mappleSources are Mapple's sources, in FreeFlix's order.
var mappleSources = []string{"mapple", "sakura", "oak", "willow", "cherry", "pines", "magnolia", "sequoia"}

var reMappleToken = regexp.MustCompile(`window\.__REQUEST_TOKEN__\s*=\s*["']([^"']+)["']`)

func mappleStreams(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	base := p.base()
	slug := ""
	watch := fmt.Sprintf("%s/watch/%s/%d", base, kind(r), r.TMDBID)
	if r.IsTV() {
		slug = fmt.Sprintf("%d-%d", r.Season, r.Episode)
		watch += "/" + slug
	}
	token, err := p.mappleToken(ctx, base, watch)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Referer": base + "/"}
	return each(ctx, mappleSources, func(ctx context.Context, source string) ([]providers.Stream, error) {
		var enc struct {
			URL string `json:"url"`
		}
		body := map[string]any{
			"data":     map[string]any{"mediaId": r.TMDBID, "mediaType": kind(r), "tv_slug": slug, "source": source},
			"endpoint": "stream-encrypted",
		}
		if err := p.fetchJSON(ctx, http.MethodPost, base+"/api/encrypt", headers, body, &enc); err != nil {
			return nil, err
		}
		if enc.URL == "" {
			return nil, nil
		}
		u := absolute(base, enc.URL)
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		var v struct {
			Success bool `json:"success"`
			Data    struct {
				StreamURL string `json:"stream_url"`
			} `json:"data"`
		}
		if err := p.fetchJSON(ctx, http.MethodGet, u+sep+"requestToken="+token, headers, nil, &v); err != nil {
			return nil, err
		}
		if !v.Success || !isWebLink(v.Data.StreamURL) {
			return nil, nil
		}
		return []providers.Stream{{
			Title: "Mapple · " + strings.ToUpper(source[:1]) + source[1:], URL: v.Data.StreamURL,
			Type: streamType(v.Data.StreamURL, "hls"), Headers: map[string]string{"Referer": base + "/"},
		}}, nil
	})
}

// mappleToken gets a request token, as Mapple's player does; older pages
// had it in the watch page itself.
func (p *Provider) mappleToken(ctx context.Context, base, watch string) (string, error) {
	var v struct {
		Token     string          `json:"token"`
		ExpiresAt json.RawMessage `json:"expiresAt"`
	}
	err := p.fetchJSON(ctx, http.MethodPost, base+"/api/request-token", map[string]string{
		"Referer": watch, "Origin": base, "Accept": "application/json",
	}, nil, &v)
	if err == nil && len(v.Token) >= 3 {
		var exp float64
		if json.Unmarshal(v.ExpiresAt, &exp) != nil || exp > float64(time.Now().UnixMilli()) {
			return v.Token, nil
		}
	}
	page, perr := p.fetch(ctx, http.MethodGet, watch, map[string]string{"Referer": base + "/"}, nil)
	if perr == nil {
		if m := reMappleToken.FindSubmatch(page); m != nil {
			return string(m[1]), nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	return "", errors.New("no request token")
}
