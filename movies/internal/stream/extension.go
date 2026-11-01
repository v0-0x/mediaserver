package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Online streaming extensions
//
// KumoBox/Seanime-style "onlinestream-provider" extensions don't take ids:
// they search a site by title, list the episodes of the entry found
// (numbered from the show's first one), then give the video sources of an
// episode on each of their servers. ExtensionProvider turns one into a
// providers.Provider: the best match for the title (and year) is kept in
// the database (and can be picked by hand when it's wrong), its episode
// list for an hour.

// OnlineStreamExtension is what ExtensionProvider uses of an online
// streaming extension (*extensions.OnlineStreamProvider).
type OnlineStreamExtension interface {
	ID() string
	Name() string
	Settings(ctx context.Context) extensions.OSSettings
	Search(ctx context.Context, media *catalog.Media, query string, dub bool) ([]extensions.OSSearchResult, error)
	FindEpisodes(ctx context.Context, id string) ([]extensions.OSEpisode, error)
	FindEpisodeServer(ctx context.Context, ep extensions.OSEpisode, server string) (*extensions.OSServer, error)
}

// ExtensionPrefix starts the provider ids of extensions ("ext:<extension id>").
const ExtensionPrefix = "ext:"

// A match scoring less is taken for another title.
const goodMatch = 0.6

// ExtensionProvider is an online streaming extension seen as a provider.
type ExtensionProvider struct {
	ext   OnlineStreamExtension
	db    *db.DB
	media MediaFunc
	// Dub tells whether the dubbed version of a title is wanted (its
	// remembered choice; nil: never).
	Dub func(mediaID int) bool
}

// NewExtensionProvider returns the provider of an extension. media looks
// titles up (their episodes are numbered from the show's first one);
// matches are kept in d.
func NewExtensionProvider(d *db.DB, ext OnlineStreamExtension, media MediaFunc) *ExtensionProvider {
	return &ExtensionProvider{ext: ext, db: d, media: media}
}

func (p *ExtensionProvider) ID() string   { return ExtensionPrefix + p.ext.ID() }
func (p *ExtensionProvider) Name() string { return p.ext.Name() }
func (p *ExtensionProvider) Kind() string { return providers.KindExtension }

// Icon is the extension's icon.
func (p *ExtensionProvider) Icon() string {
	if ic, ok := p.ext.(interface{ Icon() string }); ok {
		return ic.Icon()
	}
	return ""
}

// ExtensionID is the extension's id.
func (p *ExtensionProvider) ExtensionID() string { return p.ext.ID() }

// Supports both: extensions don't say.
func (p *ExtensionProvider) Supports(mediaType string) bool {
	return mediaType == providers.TypeMovie || mediaType == providers.TypeTV
}

// mapping is the extension's title for a movie or show.
type mapping struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"` // 1 when picked by hand
	// Manual is set on titles picked by hand.
	Manual bool `json:"manual,omitempty"`
}

type episodesCache struct {
	Mapping  *mapping               `json:"mapping"`
	Episodes []extensions.OSEpisode `json:"episodes"`
}

func modeOf(dub bool) string {
	if dub {
		return "dub"
	}
	return "sub"
}

func (p *ExtensionProvider) dub(mediaID int) bool { return p.Dub != nil && p.Dub(mediaID) }

func (p *ExtensionProvider) cacheKey(mediaID int, dub bool) string {
	return fmt.Sprintf("os-eps:%s:%d:%s", p.ext.ID(), mediaID, modeOf(dub))
}

func (p *ExtensionProvider) loadMapping(mediaID int, dub bool) *mapping {
	var raw string
	if err := p.db.QueryRow(`SELECT value FROM stream_mappings WHERE provider = ? AND media_id = ? AND mode = ?`, p.ext.ID(), mediaID, modeOf(dub)).Scan(&raw); err != nil {
		return nil
	}
	var m mapping
	if json.Unmarshal([]byte(raw), &m) != nil || m.ID == "" {
		return nil
	}
	return &m
}

func (p *ExtensionProvider) saveMapping(mediaID int, dub bool, m *mapping) error {
	raw, _ := json.Marshal(m)
	_, err := p.db.Write(`INSERT INTO stream_mappings(provider, media_id, mode, value) VALUES(?, ?, ?, ?)
		ON CONFLICT(provider, media_id, mode) DO UPDATE SET value = excluded.value`, p.ext.ID(), mediaID, modeOf(dub), string(raw))
	return err
}

// CurrentMatch is the title used for a movie or show, nil when none was
// looked for yet.
func (p *ExtensionProvider) CurrentMatch(mediaID int) *Match {
	m := p.loadMapping(mediaID, p.dub(mediaID))
	if m == nil {
		return nil
	}
	return &Match{Provider: p.ID(), Name: p.Name(), ID: m.ID, Title: m.Title, Score: m.Score, Manual: m.Manual}
}

// SetMatch uses the extension's title id for a movie or show from now on.
func (p *ExtensionProvider) SetMatch(mediaID int, id, title string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("no title picked")
	}
	dub := p.dub(mediaID)
	if err := p.saveMapping(mediaID, dub, &mapping{ID: id, Title: title, Score: 1, Manual: true}); err != nil {
		return err
	}
	p.db.DeleteCachePrefix(p.cacheKey(mediaID, dub))
	return nil
}

// SearchTitles searches the extension (to pick the title by hand).
func (p *ExtensionProvider) SearchTitles(ctx context.Context, query string, mediaID int) ([]SearchResult, error) {
	res, err := p.ext.Search(ctx, nil, query, p.dub(mediaID))
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(res))
	for _, r := range res {
		out = append(out, SearchResult{ID: r.ID, Title: r.Title, SubOrDub: r.SubOrDub})
	}
	return out, nil
}

// episodes finds the extension's title for media (the one kept, or the
// best search result) and lists its episodes.
func (p *ExtensionProvider) episodes(ctx context.Context, media *catalog.Media, dub bool) (*episodesCache, error) {
	key := p.cacheKey(media.ID, dub)
	var c episodesCache
	if p.db.GetCache(key, &c) && c.Mapping != nil {
		return &c, nil
	}
	m := p.loadMapping(media.ID, dub)
	if m == nil {
		var err error
		if m, err = p.match(ctx, media, dub); err != nil {
			return nil, err
		}
		if m == nil {
			return &episodesCache{}, nil
		}
		_ = p.saveMapping(media.ID, dub, m)
	}
	eps, err := p.ext.FindEpisodes(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(eps, func(i, j int) bool { return eps[i].Number < eps[j].Number })
	c = episodesCache{Mapping: m, Episodes: eps}
	if len(eps) > 0 { // don't remember a temporary failure for an hour
		p.db.SetCache(key, &c, time.Hour)
	}
	return &c, nil
}

// match searches the extension with the title's names and picks the most
// similar result (its year helps, when the extension gives it in the title).
func (p *ExtensionProvider) match(ctx context.Context, media *catalog.Media, dub bool) (*mapping, error) {
	var queries []string
	for _, t := range []string{media.Title.English, media.Title.Romaji, media.Title.Native, media.PreferredTitle()} {
		if t = strings.TrimSpace(t); t != "" && !containsFold(queries, t) {
			queries = append(queries, t)
		}
	}
	if len(queries) > 2 {
		queries = queries[:2]
	}
	seen := map[string]bool{}
	var all []extensions.OSSearchResult
	var firstErr error
	for _, q := range queries {
		res, err := p.ext.Search(ctx, media, q, dub)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range res {
			if !seen[r.ID] {
				seen[r.ID] = true
				all = append(all, r)
			}
		}
	}
	if len(all) == 0 {
		return nil, firstErr
	}
	year := fmt.Sprint(media.Year())
	var best *mapping
	for _, r := range all {
		score := 0.0
		for _, t := range media.AllTitles() {
			score = math.Max(score, util.Similarity(t, r.Title))
			if media.Year() > 0 {
				score = math.Max(score, util.Similarity(t+" "+year, r.Title))
			}
		}
		if dub && r.SubOrDub == "sub" {
			score -= 0.1
		}
		if best == nil || score > best.Score {
			best = &mapping{ID: r.ID, Title: r.Title, Score: score}
		}
	}
	return best, nil
}

// Streams finds the movie or episode on the extension and returns the video
// sources of every server.
func (p *ExtensionProvider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	media, err := p.mediaOf(ctx, r)
	if err != nil {
		return nil, err
	}
	dub := p.dub(media.ID)
	c, err := p.episodes(ctx, media, dub)
	if err != nil {
		return nil, err
	}
	name := media.PreferredTitle()
	if c.Mapping == nil {
		return nil, fmt.Errorf("found nothing like %q (search it by hand)", name)
	}
	if c.Mapping.Score < goodMatch {
		return nil, fmt.Errorf("no good match for %q, the closest is %q (pick the right one by hand)", name, c.Mapping.Title)
	}
	key := catalog.MovieEpisode
	if r.IsTV() {
		key = catalog.EpisodeKey(r.Season, r.Episode)
	}
	ep := findEpisode(c.Episodes, ProviderEpisode(media, key))
	if ep == nil && r.IsTV() && r.Episode > 0 && c.Mapping.Manual {
		// Some sites list each season apart: the title picked by hand may
		// be the season. (A title found by searching is the show's, which
		// may lack the latest episodes: its episode with that number would
		// be one of an earlier season.)
		ep = findEpisode(c.Episodes, float64(r.Episode))
	}
	if ep == nil {
		what := "the movie"
		if r.IsTV() {
			what = catalog.FormatKey(key)
		}
		return nil, fmt.Errorf("%q has no %s here", c.Mapping.Title, what)
	}
	servers := p.ext.Settings(ctx).EpisodeServers
	if len(servers) == 0 {
		servers = []string{"default"}
	}
	type result struct {
		srv *extensions.OSServer
		err error
	}
	results := make([]result, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv string) {
			defer wg.Done()
			s, err := p.ext.FindEpisodeServer(ctx, *ep, srv)
			results[i] = result{s, err}
		}(i, srv)
	}
	wg.Wait()
	var out []providers.Stream
	var errs []string
	for i, res := range results {
		if res.err != nil {
			errs = append(errs, servers[i]+": "+res.err.Error())
			continue
		}
		if res.srv == nil {
			continue
		}
		out = append(out, serverStreams(c.Mapping.Title, res.srv, dub)...)
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return out, nil
}

// mediaOf is the movie or show of a request (rebuilt from the request when
// it can't be looked up).
func (p *ExtensionProvider) mediaOf(ctx context.Context, r providers.Request) (*catalog.Media, error) {
	if p.media != nil {
		if m, err := p.media(ctx, r.MediaID); err == nil && m != nil {
			return m, nil
		}
	}
	if r.Title == "" {
		return nil, errors.New("unknown movie or show")
	}
	m := &catalog.Media{ID: r.MediaID, TMDBID: r.TMDBID, IMDbID: r.IMDbID, Type: catalog.TypeMovie, Title: catalog.Title{English: r.Title, Native: r.OriginalTitle, UserPreferred: r.Title}}
	if r.IsTV() {
		m.Type = catalog.TypeTV
	}
	if r.Year > 0 {
		y := r.Year
		m.SeasonYear = &y
	}
	return m, nil
}

func findEpisode(eps []extensions.OSEpisode, n float64) *extensions.OSEpisode {
	for i := range eps {
		if eps[i].Number == n {
			return &eps[i]
		}
	}
	return nil
}

// serverStreams turns a server's video sources into streams.
func serverStreams(title string, srv *extensions.OSServer, dub bool) []providers.Stream {
	var out []providers.Stream
	for _, vs := range srv.VideoSources {
		s := providers.Stream{URL: vs.URL, Quality: providers.QualityFromText(vs.Quality), FileIdx: -1}
		if s.Quality == "" {
			s.Quality = providers.QualityFromText(vs.Label)
		}
		parts := []string{title}
		if dub {
			parts[0] += " (dub)"
		}
		for _, x := range []string{srv.Server, vs.Label, vs.Quality} {
			if x = strings.TrimSpace(x); x != "" && x != "default" && !containsFold(parts, x) {
				parts = append(parts, x)
			}
		}
		s.Title = strings.Join(parts, " · ")
		switch t := strings.ToLower(vs.Type); t {
		case "m3u8", "hls":
			s.Type = providers.StreamM3U8
		case "mp4", "mkv", "webm":
			s.Type = t
		}
		if len(srv.Headers) > 0 {
			s.Headers = map[string]string{}
			for k, v := range srv.Headers {
				s.Headers[k] = v
			}
		}
		for _, sub := range vs.Subtitles {
			// mpv loads these as it starts (--sub-file): web links only,
			// as from every other provider, never a path on this computer.
			if isWebLink(sub.URL) {
				s.Subtitles = append(s.Subtitles, providers.Subtitle{URL: sub.URL, Language: sub.Language})
			}
		}
		out = append(out, s)
	}
	return out
}

// isWebLink reports whether raw is an http(s) address with a host.
func isWebLink(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
