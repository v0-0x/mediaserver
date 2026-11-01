package stremio

import (
	"context"
	"strconv"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// ProviderID is the provider id of the addon whose manifest id is addonID.
func ProviderID(addonID string) string { return "stremio:" + addonID }

// AddonProvider is an installed addon seen as a streaming provider (and a
// subtitle provider when it serves subtitles).
type AddonProvider struct {
	client *Client
	addon  Addon
}

// NewProvider returns the provider of an addon (c nil: DefaultClient).
func NewProvider(c *Client, a Addon) *AddonProvider {
	if c == nil {
		c = DefaultClient
	}
	return &AddonProvider{client: c, addon: a}
}

func (p *AddonProvider) ID() string   { return ProviderID(p.addon.Manifest.ID) }
func (p *AddonProvider) Name() string { return p.addon.Manifest.Name }
func (p *AddonProvider) Kind() string { return providers.KindStremio }
func (p *AddonProvider) Icon() string { return p.addon.Manifest.Logo }

// Addon is the addon behind the provider.
func (p *AddonProvider) Addon() Addon { return p.addon }

// Supports reports whether the addon serves streams of mediaType.
func (p *AddonProvider) Supports(mediaType string) bool {
	ok, _ := p.addon.Manifest.Serves(ResourceStream, Type(mediaType))
	return ok
}

// SupportsSubtitles reports whether the addon serves subtitles of
// mediaType.
func (p *AddonProvider) SupportsSubtitles(mediaType string) bool {
	ok, _ := p.addon.Manifest.Serves(ResourceSubtitles, Type(mediaType))
	return ok
}

// Streams asks the addon for the streams of the movie or episode. Addons
// identify titles by IMDb id (or TMDB id for the few that take it):
// ErrNoIMDbID is returned when r has none they understand.
func (p *AddonProvider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if !p.Supports(r.Type) {
		return nil, nil
	}
	id, err := p.addon.Manifest.VideoID(ResourceStream, r)
	if err != nil {
		// The addon can't be asked about this title: not a failure of the
		// addon (its health stays as it is).
		return nil, providers.Skip(err)
	}
	return p.client.Streams(ctx, p.addon, Type(r.Type), id)
}

// Subtitles asks the addon for the subtitles of the movie or episode
// (nothing when it doesn't serve subtitles).
func (p *AddonProvider) Subtitles(ctx context.Context, r providers.Request) ([]providers.Subtitle, error) {
	if !p.SupportsSubtitles(r.Type) {
		return nil, nil
	}
	id, err := p.addon.Manifest.VideoID(ResourceSubtitles, r)
	if err != nil {
		return nil, err
	}
	return p.client.Subtitles(ctx, p.addon, Type(r.Type), id)
}

// VideoSubtitles is Subtitles for the very video file v: its size, hash
// and name go along as Stremio sends them (videoSize, videoHash,
// filename), for addons that match subtitles with the file (OpenSubtitles).
func (p *AddonProvider) VideoSubtitles(ctx context.Context, r providers.Request, v providers.Video) ([]providers.Subtitle, error) {
	if !p.SupportsSubtitles(r.Type) {
		return nil, nil
	}
	id, err := p.addon.Manifest.VideoID(ResourceSubtitles, r)
	if err != nil {
		return nil, err
	}
	extra := map[string]string{"videoHash": v.Hash, "filename": v.Filename}
	if v.Size > 0 {
		extra["videoSize"] = strconv.FormatInt(v.Size, 10)
	}
	return p.client.SubtitlesFor(ctx, p.addon, Type(r.Type), id, extra)
}
