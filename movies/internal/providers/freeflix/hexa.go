package freeflix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Hexa answers encrypted with a key of the caller's choosing, given a
// token enc-dec.app hands out; enc-dec.app decrypts the answer: HLS
// playlists from several servers.
var hexaSite = &Site{ID: "hexa", Name: "Hexa", DefaultURL: "https://themoviedb.hexa.su", Decrypts: true, streams: hexaStreams}

// hexaReferer is Hexa's web player, which its CDNs want as Referer.
const hexaReferer = "https://hexa.su/"

func hexaStreams(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	base := p.base()
	u := fmt.Sprintf("%s/api/tmdb/movie/%d/images", base, r.TMDBID)
	if r.IsTV() {
		u = fmt.Sprintf("%s/api/tmdb/tv/%d/season/%d/episode/%d/images", base, r.TMDBID, r.Season, r.Episode)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(b)
	var tok struct {
		Token string `json:"token"`
	}
	if err := p.decrypt(ctx, "enc-hexa", "", nil, &tok); err != nil {
		return nil, err
	}
	if tok.Token == "" {
		return nil, errors.New("enc-dec.app: no token")
	}
	enc, err := p.fetch(ctx, http.MethodGet, u, map[string]string{
		"Accept":             "text/plain",
		"X-Api-Key":          key,
		"X-Fingerprint-Lite": "e9136c41504646444",
		"X-Cap-Token":        tok.Token,
		"Referer":            hexaReferer,
	}, nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Sources []struct {
			Server string `json:"server"`
			URL    string `json:"url"`
		} `json:"sources"`
	}
	if err := p.decrypt(ctx, "dec-hexa", "", map[string]string{"text": strings.TrimSpace(string(enc)), "key": key}, &res); err != nil {
		return nil, err
	}
	var out []providers.Stream
	for _, s := range res.Sources {
		if !isWebLink(s.URL) {
			continue
		}
		title := "Hexa"
		if s.Server != "" {
			title += " · " + strings.ToUpper(s.Server)
		}
		out = append(out, providers.Stream{
			Title: title, URL: s.URL, Type: streamType(s.URL, "hls"), Headers: map[string]string{"Referer": hexaReferer},
		})
	}
	return out, nil
}
