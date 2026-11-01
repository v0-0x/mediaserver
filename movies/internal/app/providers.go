package app

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/downloads"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/providers/archive"
	"github.com/v0-0x/mediaserver/movies/internal/providers/cinejoy"
	"github.com/v0-0x/mediaserver/movies/internal/providers/freeflix"
	"github.com/v0-0x/mediaserver/movies/internal/providers/mediaserver"
	"github.com/v0-0x/mediaserver/movies/internal/providers/stremio"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// Streaming providers
//
// Movies and episodes are streamed from, in this order: the user's own
// Jellyfin, Emby and Plex servers, the built-in Internet Archive provider
// and the built-in streaming sites of the luffy and FreeFlix apps
// (Settings › Providers), the Stremio addons the user installed (in their
// order), the Nuvio providers and the online streaming extensions. The
// stream service asks the enabled ones; the Providers page lists them all.

// providersUpdated tells the UI the provider list changed.
const providersUpdated = "providers-updated"

// ProviderInfo describes a streaming provider for the UI.
type ProviderInfo struct {
	providers.Info
	Enabled bool `json:"enabled"`
	// Extension is the extension id of Nuvio providers and extensions
	// (their logs, settings and removal go through the extension API).
	Extension string `json:"extension,omitempty"`
}

// collectionTTL is how long an addon collection is kept.
const collectionTTL = 30 * time.Minute

// collectionCache keeps the addon collections downloaded lately. A
// collection holds hundreds of manifests (megabytes): it's forgotten once
// stale rather than kept until it's asked for again.
type collectionCache struct {
	// Replaced in tests.
	now       func() time.Time
	afterFunc func(time.Duration, func())

	mu      sync.Mutex
	entries map[string]collectionEntry
}

type collectionEntry struct {
	addons []stremio.Addon
	at     time.Time
}

func newCollectionCache() *collectionCache {
	return &collectionCache{
		now:       time.Now,
		afterFunc: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		entries:   map[string]collectionEntry{},
	}
}

// get returns the addons of the collection at url, when they're fresh.
func (c *collectionCache) get(url string) ([]stremio.Addon, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[url]
	if !ok || c.now().Sub(e.at) > collectionTTL {
		return nil, false
	}
	return e.addons, true
}

// put keeps the addons of the collection at url for collectionTTL.
func (c *collectionCache) put(url string, addons []stremio.Addon) {
	at := c.now()
	c.mu.Lock()
	c.entries[url] = collectionEntry{addons: addons, at: at}
	c.mu.Unlock()
	c.afterFunc(collectionTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if e, ok := c.entries[url]; ok && e.at.Equal(at) { // not downloaded again since
			delete(c.entries, url)
		}
	})
}

// streamOptions are the settings the stream service reads.
func streamOptions(s *config.Store) func() stream.Options {
	return func() stream.Options {
		cfg := s.Get()
		return stream.Options{
			PreferredQuality: cfg.Providers.PreferredQuality,
			Timeout:          time.Duration(cfg.Providers.TimeoutSeconds) * time.Second,
			DefaultProvider:  cfg.OnlineStream.DefaultProvider,
		}
	}
}

// wireProviders opens the Stremio addon store and hands the providers to
// the stream service and the downloads.
func (a *App) wireProviders() error {
	store, err := stremio.NewStore(a.DB, stremio.NewClient())
	if err != nil {
		return err
	}
	a.Stremio = store
	a.collections = newCollectionCache()
	store.OnChange = func() {
		a.Stream.Invalidate()
		a.Hub.Publish(providersUpdated, nil)
	}
	// The in-app player may stream from the addons installed as local ones,
	// and from the user's media servers (often on the LAN).
	stream.LocalHosts = func() []string {
		var out []string
		for _, ad := range store.List() {
			if u, err := url.Parse(ad.TransportURL); err == nil && ad.Local && ad.Enabled {
				out = append(out, stream.HostKey(u))
			}
		}
		return append(out, mediaserver.Hosts(a.mediaServers())...)
	}
	// The media servers' tokens are added when KumoBox fetches their
	// videos, never put in the links (mediaserver/credentials.go).
	stream.Credentials = func(u *url.URL) map[string]string { return mediaserver.Credentials(a.mediaServers(), u) }
	a.Settings.OnChange(func(old, cur config.Settings) {
		// The sites' addresses too: their streams are found again.
		if !reflect.DeepEqual(old.Providers.MediaServers, cur.Providers.MediaServers) || old.Providers.Sites != cur.Providers.Sites {
			a.Stream.Invalidate()
			a.Hub.Publish(providersUpdated, nil)
		}
	})
	a.Stream.Providers = a.StreamProviders
	a.Stream.SubtitleProviders = a.Stremio.SubtitleProviders
	// How each provider's searches go (providerhealth.go), and the version
	// of each show that played last (streamversion.go).
	a.healthStore = newHealthStore(a.DB)
	a.Health = a.healthStore.health
	a.Stream.Health = a.Health
	a.versions = newStreamVersions(a.DB)
	a.Stream.Versions = a.versions.Get
	a.Downloads.Resolvers["stream"] = a.resolveStreamDownload
	return nil
}

// mediaServers are the media servers of Settings › Providers.
func (a *App) mediaServers() []mediaserver.Config {
	var out []mediaserver.Config
	for _, s := range a.Settings.Get().Providers.MediaServers {
		out = append(out, mediaserver.Config{Kind: s.Kind, Name: s.Name, URL: s.URL, Token: s.Token, UserID: s.UserID})
	}
	return out
}

// mediaServerProviders are the providers of the user's media servers.
func (a *App) mediaServerProviders() []providers.Provider {
	var out []providers.Provider
	for _, c := range a.mediaServers() {
		if p, err := mediaserver.New(c); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// streamSite is a built-in streaming site, and whether it's on.
type streamSite struct {
	providers.Provider
	enabled bool
}

// streamSites are the built-in streaming sites, in the order they're
// asked: luffy's, then FreeFlix's.
func (a *App) streamSites() []streamSite {
	cfg := a.Settings.Get().Providers.Sites
	out := []streamSite{{cinejoy.New(cfg.Cinejoy.URL), cfg.Cinejoy.Enabled}}
	for _, site := range freeflix.Sites {
		c, _ := cfg.Site(site.ID)
		p := freeflix.New(site, c.URL)
		p.DecryptURL = cfg.DecryptURL
		out = append(out, streamSite{p, c.Enabled})
	}
	return out
}

// StreamProviders returns the enabled streaming providers, in order: the
// user's own media servers first.
func (a *App) StreamProviders() []providers.Provider {
	out := a.mediaServerProviders()
	if a.Settings.Get().Providers.Archive {
		out = append(out, archive.New())
	}
	for _, s := range a.streamSites() {
		if s.enabled {
			out = append(out, s.Provider)
		}
	}
	if a.Stremio != nil {
		out = append(out, a.Stremio.Providers()...)
	}
	out = append(out, a.Extensions.NuvioProviders()...)
	for _, ext := range a.Extensions.OnlineStreamProviders() {
		p := stream.NewExtensionProvider(a.DB, ext, a.Platform.MediaLite)
		p.Dub = a.wantsDub
		out = append(out, p)
	}
	return out
}

// wantsDub is the dubbed-or-original choice of a title (its track prefs),
// else the default one.
func (a *App) wantsDub(mediaID int) bool {
	if p := a.Player.Tracks.Get(mediaID); p != nil && (p.StreamMode == "sub" || p.StreamMode == "dub") {
		return p.StreamMode == "dub"
	}
	return a.Settings.Get().OnlineStream.PreferDub
}

// ProviderInfos lists every streaming provider, disabled ones too, in the
// order they're asked.
func (a *App) ProviderInfos() []ProviderInfo {
	var out []ProviderInfo
	for _, p := range a.mediaServerProviders() {
		out = append(out, ProviderInfo{Info: providers.Describe(p), Enabled: true})
	}
	out = append(out, ProviderInfo{Info: providers.Describe(archive.New()), Enabled: a.Settings.Get().Providers.Archive})
	for _, s := range a.streamSites() {
		out = append(out, ProviderInfo{Info: providers.Describe(s.Provider), Enabled: s.enabled})
	}
	if a.Stremio != nil {
		for _, addon := range a.Stremio.List() {
			p := stremio.NewProvider(a.Stremio.Client(), addon)
			if p.Supports(providers.TypeMovie) || p.Supports(providers.TypeTV) {
				out = append(out, ProviderInfo{Info: providers.Describe(p), Enabled: addon.Enabled})
			}
		}
	}
	var nuvio, exts []ProviderInfo
	for _, e := range a.Extensions.List() {
		if e.Manifest == nil {
			continue
		}
		m := e.Manifest
		switch m.Type {
		case extensions.TypeNuvioProvider:
			types := []string{providers.TypeMovie, providers.TypeTV}
			if m.Nuvio != nil && len(m.Nuvio.SupportedTypes) > 0 {
				types = m.Nuvio.SupportedTypes
			}
			nuvio = append(nuvio, ProviderInfo{
				Info:    providers.Info{ID: "nuvio:" + strings.TrimPrefix(m.ID, "nuvio."), Name: m.Name, Kind: providers.KindNuvio, Icon: m.Icon, Types: types},
				Enabled: e.Enabled, Extension: m.ID,
			})
		case extensions.TypeOnlineStream:
			exts = append(exts, ProviderInfo{
				Info:    providers.Info{ID: stream.ExtensionPrefix + m.ID, Name: m.Name, Kind: providers.KindExtension, Icon: m.Icon, Types: []string{providers.TypeMovie, providers.TypeTV}},
				Enabled: e.Enabled, Extension: m.ID,
			})
		}
	}
	return append(append(out, nuvio...), exts...)
}

// StremioCollection downloads an addon collection (kept half an hour) and
// marks the addons installed.
func (a *App) StremioCollection(ctx context.Context, url string, refresh bool) ([]stremio.Addon, error) {
	addons, ok := a.collections.get(url)
	if !ok || refresh {
		list, err := a.Stremio.Client().FetchCollection(ctx, url)
		if err != nil {
			return nil, err
		}
		a.collections.put(url, list)
		addons = list
	}
	return a.Stremio.MarkInstalled(append([]stremio.Addon(nil), addons...)), nil
}

// resolveStreamDownload finds a fresh link for a queued download: from the
// same provider, in the quality closest to the one picked.
func (a *App) resolveStreamDownload(ctx context.Context, it *downloads.Item) (*downloads.Resolved, error) {
	media, err := a.Platform.MediaLite(ctx, it.MediaID)
	if err != nil {
		return nil, err
	}
	s, res, err := a.Stream.Resolve(ctx, media, it.Episode, it.Provider, it.Quality)
	if err != nil {
		return nil, err
	}
	r := &downloads.Resolved{URL: stream.CleanLink(s.URL)}
	switch s.Type {
	case providers.StreamMKV, providers.StreamWebM:
		r.Container = s.Type // kept as it is
	case providers.StreamMP4, providers.StreamM3U8:
		r.Container = "mp4"
	}
	r.Headers, r.Referrer = stream.SplitReferrer(stream.WithCredentials(s.URL, s.Headers))
	// One subtitle file goes along: the stream's own, else an addon's, in
	// the preferred language.
	subs := append(append([]providers.Subtitle(nil), s.Subtitles...), res.Subtitles...)
	if sorted := stream.SortSubtitles(subs, a.Settings.Get().Playback.PreferredSubLang, 1); len(sorted) > 0 {
		r.SubURL = sorted[0].URL
	}
	return r, nil
}

// DownloadTitle is the name a download of media is saved under: a movie's
// title with its year (as libraries name movie folders), a show's title.
func DownloadTitle(media *catalog.Media) string {
	if !media.IsShow() && media.Year() > 0 {
		return fmt.Sprintf("%s (%d)", media.PreferredTitle(), media.Year())
	}
	return media.PreferredTitle()
}
