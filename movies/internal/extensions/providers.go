package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/torrent"
)

// ProviderMedia is the media shape providers receive (hibike's Media), with
// the TMDB and IMDb ids of the movie or show and its type (MOVIE or TV) on
// top.
type ProviderMedia struct {
	ID                   int        `json:"id"`
	IDMal                *int       `json:"idMal,omitempty"`
	TmdbID               int        `json:"tmdbId,omitempty"`
	ImdbID               string     `json:"imdbId,omitempty"`
	Type                 string     `json:"type,omitempty"`
	Status               string     `json:"status,omitempty"`
	Format               string     `json:"format,omitempty"`
	EnglishTitle         *string    `json:"englishTitle,omitempty"`
	RomajiTitle          string     `json:"romajiTitle,omitempty"`
	EpisodeCount         int        `json:"episodeCount"`
	AbsoluteSeasonOffset int        `json:"absoluteSeasonOffset"`
	Synonyms             []string   `json:"synonyms"`
	IsAdult              bool       `json:"isAdult"`
	StartDate            *FuzzyDate `json:"startDate,omitempty"`
}

type FuzzyDate struct {
	Year  int  `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

func ToProviderMedia(m *catalog.Media) ProviderMedia {
	if m == nil {
		return ProviderMedia{Synonyms: []string{}, Status: "NOT_YET_RELEASED", Format: "TV", EpisodeCount: -1}
	}
	pm := ProviderMedia{
		ID: m.ID, TmdbID: m.TMDBID, ImdbID: m.IMDbID, Type: m.Type, Status: m.Status, Format: m.Format,
		RomajiTitle: m.Title.Romaji, EpisodeCount: -1, Synonyms: m.Synonyms, IsAdult: m.IsAdult,
	}
	if pm.RomajiTitle == "" {
		pm.RomajiTitle = m.PreferredTitle()
	}
	if pm.Synonyms == nil {
		pm.Synonyms = []string{}
	}
	if pm.Status == "" {
		pm.Status = "NOT_YET_RELEASED"
	}
	if pm.Format == "" {
		pm.Format = "TV"
	}
	if m.Title.English != "" {
		e := m.Title.English
		pm.EnglishTitle = &e
	}
	if n := m.TotalEpisodes(); n > 0 {
		pm.EpisodeCount = n
	}
	if m.StartDate.Year != nil {
		pm.StartDate = &FuzzyDate{Year: *m.StartDate.Year, Month: m.StartDate.Month, Day: m.StartDate.Day}
	}
	return pm
}

// ---------------------------------------------------------------------------
// Torrent providers

type TorrentProvider struct {
	ext *Loaded
}

// ID is the extension's. The torrent manager adds a suffix to one that
// clashes with a built-in source's.
func (p *TorrentProvider) ID() string   { return p.ext.Manifest.ID }
func (p *TorrentProvider) Name() string { return p.ext.Manifest.Name }

type animeTorrent struct {
	Name          string   `json:"name"`
	Date          string   `json:"date"`
	Size          float64  `json:"size"`
	FormattedSize string   `json:"formattedSize"`
	Seeders       float64  `json:"seeders"`
	Leechers      float64  `json:"leechers"`
	DownloadCount float64  `json:"downloadCount"`
	Link          string   `json:"link"`
	DownloadURL   string   `json:"downloadUrl"`
	MagnetLink    string   `json:"magnetLink"`
	InfoHash      string   `json:"infoHash"`
	Resolution    string   `json:"resolution"`
	IsBatch       bool     `json:"isBatch"`
	EpisodeNumber *float64 `json:"episodeNumber"`
	ReleaseGroup  string   `json:"releaseGroup"`
	IsBestRelease bool     `json:"isBestRelease"`
	Confirmed     bool     `json:"confirmed"`
}

func (p *TorrentProvider) convert(raw json.RawMessage) ([]*torrent.SearchResult, error) {
	var list []animeTorrent
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s returned unexpected data: %w", p.Name(), err)
	}
	out := make([]*torrent.SearchResult, 0, len(list))
	for _, t := range list {
		r := &torrent.SearchResult{
			Provider: p.ID(), Name: t.Name, Date: t.Date, Size: int64(t.Size), FormattedSize: t.FormattedSize,
			Seeders: int(t.Seeders), Leechers: int(t.Leechers), DownloadCount: int(t.DownloadCount), Link: t.Link,
			DownloadURL: t.DownloadURL, MagnetLink: t.MagnetLink, InfoHash: t.InfoHash, Resolution: t.Resolution,
			IsBatch: t.IsBatch, ReleaseGroup: t.ReleaseGroup, IsBestRelease: t.IsBestRelease, Confirmed: t.Confirmed,
			EpisodeNumber: -1,
		}
		if t.EpisodeNumber != nil {
			r.EpisodeNumber = int(*t.EpisodeNumber)
		}
		torrent.Enrich(r)
		out = append(out, r)
	}
	return out, nil
}

func (p *TorrentProvider) Search(ctx context.Context, query string) ([]*torrent.SearchResult, error) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	raw, err := rt.CallProvider(ctx, "search", map[string]any{"media": ToProviderMedia(nil), "query": query})
	if err != nil {
		return nil, err
	}
	return p.convert(raw)
}

func (p *TorrentProvider) settings(ctx context.Context) (canSmart bool) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return false
	}
	raw, err := rt.CallProvider(ctx, "getSettings")
	if err != nil {
		return false
	}
	var s struct {
		CanSmartSearch bool `json:"canSmartSearch"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.CanSmartSearch
}

func (p *TorrentProvider) SmartSearch(ctx context.Context, q torrent.SmartQuery) ([]*torrent.SearchResult, error) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	media := ToProviderMedia(q.Media)
	// The episode as providers number them: in its season (the season and
	// the Moviko key come along).
	season, episode := 0, 0
	if q.Media != nil && q.Media.IsShow() && q.Episode > 0 {
		season, episode = catalog.SplitKey(q.Episode)
	}
	if q.Query != "" || !p.settings(ctx) {
		query := q.Query
		if query == "" && q.Media != nil {
			query = q.Media.EnglishOrRomaji()
			if !q.Batch && episode > 0 {
				query = fmt.Sprintf("%s %s", query, catalog.FormatKey(q.Episode))
			}
		}
		raw, err := rt.CallProvider(ctx, "search", map[string]any{"media": media, "query": query})
		if err != nil {
			return nil, err
		}
		return p.convert(raw)
	}
	opts := map[string]any{
		"media": media, "query": "", "batch": q.Batch, "episodeNumber": episode, "seasonNumber": season, "episodeKey": q.Episode,
		"resolution": strings.TrimSuffix(q.Resolution, "p"), "anidbAID": 0, "anidbEID": 0, "bestReleases": false,
	}
	raw, err := rt.CallProvider(ctx, "smartSearch", opts)
	if err != nil {
		return nil, err
	}
	return p.convert(raw)
}

func (p *TorrentProvider) Magnet(ctx context.Context, r *torrent.SearchResult) (string, error) {
	if r.MagnetLink != "" {
		return r.MagnetLink, nil
	}
	rt, err := p.ext.Runtime()
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"name": r.Name, "date": r.Date, "size": r.Size, "formattedSize": r.FormattedSize, "seeders": r.Seeders,
		"leechers": r.Leechers, "downloadCount": r.DownloadCount, "link": r.Link, "downloadUrl": r.DownloadURL,
		"magnetLink": r.MagnetLink, "infoHash": r.InfoHash, "resolution": r.Resolution, "isBatch": r.IsBatch,
		"episodeNumber": r.EpisodeNumber, "releaseGroup": r.ReleaseGroup, "isBestRelease": r.IsBestRelease, "confirmed": r.Confirmed,
	}
	raw, err := rt.CallProvider(ctx, "getTorrentMagnetLink", payload)
	if err == nil {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s, nil
		}
	}
	if r.InfoHash != "" {
		return torrent.MagnetFromHash(r.InfoHash, r.Name), nil
	}
	raw, err2 := rt.CallProvider(ctx, "getTorrentInfoHash", payload)
	if err2 == nil {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return torrent.MagnetFromHash(s, r.Name), nil
		}
	}
	if r.DownloadURL != "" {
		return r.DownloadURL, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s could not provide a magnet link", p.Name())
}

// ---------------------------------------------------------------------------
// Online streaming providers

type OSSearchResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	SubOrDub string `json:"subOrDub"`
}

type OSEpisode struct {
	ID       string  `json:"id"`
	Number   float64 `json:"number"`
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Provider string  `json:"provider,omitempty"`
}

type OSSubtitle struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Language  string `json:"language"`
	IsDefault bool   `json:"isDefault"`
}

type OSVideoSource struct {
	URL       string       `json:"url"`
	Type      string       `json:"type"`
	Quality   string       `json:"quality"`
	Label     string       `json:"label,omitempty"`
	Subtitles []OSSubtitle `json:"subtitles"`
}

type OSServer struct {
	Server       string            `json:"server"`
	Headers      map[string]string `json:"headers"`
	VideoSources []OSVideoSource   `json:"videoSources"`
	Provider     string            `json:"provider,omitempty"`
}

type OSSettings struct {
	EpisodeServers []string `json:"episodeServers"`
	SupportsDub    bool     `json:"supportsDub"`
}

type OnlineStreamProvider struct {
	ext *Loaded
}

func (p *OnlineStreamProvider) ID() string   { return p.ext.Manifest.ID }
func (p *OnlineStreamProvider) Name() string { return p.ext.Manifest.Name }
func (p *OnlineStreamProvider) Icon() string { return p.ext.Manifest.Icon }

func (p *OnlineStreamProvider) Settings(ctx context.Context) OSSettings {
	s := OSSettings{EpisodeServers: []string{}}
	rt, err := p.ext.Runtime()
	if err != nil {
		return s
	}
	if raw, err := rt.CallProvider(ctx, "getSettings"); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if len(s.EpisodeServers) == 0 && rt.HasMethod("getEpisodeServers") {
		if raw, err := rt.CallProvider(ctx, "getEpisodeServers"); err == nil {
			_ = json.Unmarshal(raw, &s.EpisodeServers)
		}
	}
	if len(s.EpisodeServers) == 0 {
		s.EpisodeServers = []string{"default"}
	}
	return s
}

func (p *OnlineStreamProvider) Search(ctx context.Context, media *catalog.Media, query string, dub bool) ([]OSSearchResult, error) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	opts := map[string]any{"media": ToProviderMedia(media), "query": query, "dub": dub}
	if media != nil && media.Year() > 0 {
		opts["year"] = media.Year()
	}
	raw, err := rt.CallProvider(ctx, "search", opts)
	if err != nil {
		return nil, err
	}
	var out []OSSearchResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s returned unexpected search data: %w", p.Name(), err)
	}
	return out, nil
}

func (p *OnlineStreamProvider) FindEpisodes(ctx context.Context, id string) ([]OSEpisode, error) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	raw, err := rt.CallProvider(ctx, "findEpisodes", id)
	if err != nil {
		return nil, err
	}
	var out []OSEpisode
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s returned unexpected episode data: %w", p.Name(), err)
	}
	for i := range out {
		out[i].Provider = p.ID()
	}
	return out, nil
}

func (p *OnlineStreamProvider) FindEpisodeServer(ctx context.Context, ep OSEpisode, server string) (*OSServer, error) {
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	ep.Provider = p.ID()
	raw, err := rt.CallProvider(ctx, "findEpisodeServer", ep, server)
	if err != nil {
		return nil, err
	}
	var out OSServer
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s returned unexpected server data: %w", p.Name(), err)
	}
	out.Provider = p.ID()
	if out.Server == "" {
		out.Server = server
	}
	return &out, nil
}
