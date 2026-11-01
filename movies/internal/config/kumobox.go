package config

import "strings"

// Settings KumoBox adds to Kumo's: the movie and show catalog (TMDB),
// Letterboxd, the streaming providers and the torrent search sources.

// DefaultStremioCatalog is the official Stremio addon collection: the
// addons the Providers page offers out of the box. Any addon can also be
// added by its manifest URL, and other collections listed in
// ProviderSettings.StremioCatalogs.
const DefaultStremioCatalog = "https://api.strem.io/addonscollection.json"

// TMDBSettings configure The Movie Database, where every movie and show's
// data comes from. APIKey is either a v3 API key (32 hex characters) or a v4
// "API Read Access Token" (a long token starting with "eyJ"): both are free
// from themoviedb.org › Settings › API.
type TMDBSettings struct {
	APIKey string `json:"apiKey"`
	// Language of titles and descriptions, e.g. en-US, fr-FR.
	Language string `json:"language"`
	// Region (ISO 3166-1, e.g. US) for release dates, certifications and
	// where to watch.
	Region string `json:"region"`
}

// LetterboxdSettings connect a Letterboxd account. With only a username
// KumoBox reads the member's public diary (RSS) and imports their watched
// films and ratings; what's watched in KumoBox is exported as a CSV that
// letterboxd.com/import takes. With API access (granted by Letterboxd on
// request) it logs straight to the account: APIClientID and
// APIClientSecret are the app's credentials, the member signs in from the
// UI and the tokens are kept in the database, not here.
type LetterboxdSettings struct {
	Username string `json:"username"`
	// AutoSync reads the diary every SyncMinutes.
	AutoSync    bool `json:"autoSync"`
	SyncMinutes int  `json:"syncMinutes"`
	// LogWatched adds a diary entry when a movie is watched to the end in
	// KumoBox (to export, or to send with API access).
	LogWatched bool `json:"logWatched"`
	// ImportWatchlist puts the films of the Letterboxd watchlist (from an
	// export) on the KumoBox watchlist.
	ImportWatchlist bool   `json:"importWatchlist"`
	APIClientID     string `json:"apiClientId"`
	APIClientSecret string `json:"apiClientSecret"`
}

// ProviderSettings configure where movies and episodes are streamed from:
// the built-in Internet Archive provider (public-domain films), the
// built-in streaming sites of the luffy and FreeFlix apps, Stremio addons,
// Nuvio provider repositories and KumoBox/Seanime extensions.
type ProviderSettings struct {
	// Archive enables the Internet Archive (archive.org) provider.
	Archive bool `json:"archive"`
	// Sites are the built-in streaming sites.
	Sites StreamSiteSettings `json:"sites"`
	// StremioCatalogs are addon collections the Providers page lists
	// (JSON arrays of addon descriptors, like Stremio's own).
	StremioCatalogs []string `json:"stremioCatalogs"`
	// NuvioRepos are Nuvio provider repositories (manifest.json URLs).
	NuvioRepos []string `json:"nuvioRepos"`
	// PreferredQuality ranks sources: "2160p", "1080p", "720p", "480p" or
	// "" (best first).
	PreferredQuality string `json:"preferredQuality"`
	// TimeoutSeconds bounds each provider's search for sources.
	TimeoutSeconds int `json:"timeoutSeconds"`
	// MediaServers are the user's own Jellyfin, Emby and Plex servers:
	// their movies and episodes are streamed like any provider's.
	MediaServers []MediaServer `json:"mediaServers"`
}

// StreamSite is a built-in streaming site: on or off, and its address
// (empty: its own; sites move to mirrors from time to time).
type StreamSite struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// StreamSiteSettings are the streaming sites the luffy and FreeFlix
// command-line apps stream from, built in: luffy's Cinejoy, and FreeFlix's
// Vidlink, Hexa, Xpass, Mapple and Videasy. They're unofficial sites: off
// until the user turns them on.
type StreamSiteSettings struct {
	Cinejoy StreamSite `json:"cinejoy"`
	Vidlink StreamSite `json:"vidlink"`
	Hexa    StreamSite `json:"hexa"`
	Xpass   StreamSite `json:"xpass"`
	Mapple  StreamSite `json:"mapple"`
	Videasy StreamSite `json:"videasy"`
	// DecryptURL is the enc-dec.app API Vidlink, Hexa and Videasy go
	// through (empty: enc-dec.app's own).
	DecryptURL string `json:"decryptUrl"`
}

// Site returns the settings of the site with id ("cinejoy", "vidlink"...).
func (s StreamSiteSettings) Site(id string) (StreamSite, bool) {
	switch id {
	case "cinejoy":
		return s.Cinejoy, true
	case "vidlink":
		return s.Vidlink, true
	case "hexa":
		return s.Hexa, true
	case "xpass":
		return s.Xpass, true
	case "mapple":
		return s.Mapple, true
	case "videasy":
		return s.Videasy, true
	}
	return StreamSite{}, false
}

// Media server kinds.
const (
	MediaServerJellyfin = "jellyfin"
	MediaServerEmby     = "emby"
	MediaServerPlex     = "plex"
)

// MediaServer is a Jellyfin, Emby or Plex server the user streams from.
// Token is a secret: an API key (Jellyfin, Emby: Dashboard › API Keys) or
// the account's X-Plex-Token. It's never shown to devices on the LAN, nor
// put in the links handed to players: KumoBox adds it when it fetches the
// server's videos.
type MediaServer struct {
	// Kind is MediaServerJellyfin, MediaServerEmby or MediaServerPlex.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// URL is the server's address (http://192.168.1.10:8096,
	// https://plex.example:32400...).
	URL   string `json:"url"`
	Token string `json:"token"`
	// UserID is the Jellyfin/Emby user whose libraries are searched (empty:
	// the token's own, or all of them for an API key).
	UserID string `json:"userId,omitempty"`
}

// TorznabIndexer is a Torznab feed: an indexer of Jackett or Prowlarr (the
// URL ends in /api, e.g. http://127.0.0.1:9696/1/api), or any other
// Torznab server.
type TorznabIndexer struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	APIKey  string `json:"apiKey"`
	Enabled bool   `json:"enabled"`
}

// TorrentSource is a built-in torrent search source. URL overrides the
// site's address (they move to mirrors from time to time).
type TorrentSource struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// TorrentSourceSettings are the torrent search sources for movies and
// shows (extension torrent providers come on top).
type TorrentSourceSettings struct {
	Torznab     []TorznabIndexer `json:"torznab"`
	YTS         TorrentSource    `json:"yts"`         // movies
	EZTV        TorrentSource    `json:"eztv"`        // shows
	PirateBay   TorrentSource    `json:"pirateBay"`   // apibay.org
	TorrentsCSV TorrentSource    `json:"torrentsCsv"` // torrents-csv.com
	Archive     TorrentSource    `json:"archive"`     // archive.org (public domain)
}

func defaultTMDB() TMDBSettings {
	return TMDBSettings{Language: "en-US", Region: "US"}
}

func defaultLetterboxd() LetterboxdSettings {
	return LetterboxdSettings{AutoSync: true, SyncMinutes: 60, LogWatched: true, ImportWatchlist: true}
}

func defaultProviders() ProviderSettings {
	return ProviderSettings{
		Archive:          true,
		StremioCatalogs:  []string{DefaultStremioCatalog},
		NuvioRepos:       []string{},
		PreferredQuality: "1080p",
		TimeoutSeconds:   20,
		MediaServers:     []MediaServer{},
	}
}

// sanitizeMediaServers trims the media servers' fields and drops those of
// an unknown kind or without an address.
func sanitizeMediaServers(list []MediaServer) []MediaServer {
	out := []MediaServer{}
	for _, s := range list {
		s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
		s.Name = strings.TrimSpace(s.Name)
		s.URL = strings.TrimRight(strings.TrimSpace(s.URL), "/")
		s.Token = strings.TrimSpace(s.Token)
		s.UserID = strings.TrimSpace(s.UserID)
		switch s.Kind {
		case MediaServerJellyfin, MediaServerEmby, MediaServerPlex:
		default:
			continue
		}
		if s.URL == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func defaultTorrentSources() TorrentSourceSettings {
	return TorrentSourceSettings{
		Torznab:     []TorznabIndexer{},
		YTS:         TorrentSource{Enabled: true},
		EZTV:        TorrentSource{Enabled: true},
		PirateBay:   TorrentSource{Enabled: true},
		TorrentsCSV: TorrentSource{Enabled: true},
		Archive:     TorrentSource{Enabled: true},
	}
}

// sanitizeKumoBox fixes the KumoBox settings of s in place.
func sanitizeKumoBox(s *Settings) {
	d := Defaults()
	s.TMDB.APIKey = strings.TrimSpace(s.TMDB.APIKey)
	if s.TMDB.Language == "" {
		s.TMDB.Language = d.TMDB.Language
	}
	s.TMDB.Region = strings.ToUpper(strings.TrimSpace(s.TMDB.Region))
	if s.TMDB.Region == "" {
		s.TMDB.Region = d.TMDB.Region
	}
	s.Letterboxd.Username = strings.Trim(strings.TrimSpace(s.Letterboxd.Username), "@/")
	if s.Letterboxd.SyncMinutes < 15 {
		s.Letterboxd.SyncMinutes = d.Letterboxd.SyncMinutes
	}
	if s.Providers.StremioCatalogs == nil {
		s.Providers.StremioCatalogs = []string{}
	}
	if s.Providers.NuvioRepos == nil {
		s.Providers.NuvioRepos = []string{}
	}
	if s.Providers.TimeoutSeconds < 5 || s.Providers.TimeoutSeconds > 120 {
		s.Providers.TimeoutSeconds = d.Providers.TimeoutSeconds
	}
	s.Providers.MediaServers = sanitizeMediaServers(s.Providers.MediaServers)
	sites := &s.Providers.Sites
	for _, site := range []*StreamSite{&sites.Cinejoy, &sites.Vidlink, &sites.Hexa, &sites.Xpass, &sites.Mapple, &sites.Videasy} {
		site.URL = strings.TrimSpace(site.URL)
	}
	sites.DecryptURL = strings.TrimSpace(sites.DecryptURL)
	if s.TorrentSources.Torznab == nil {
		s.TorrentSources.Torznab = []TorznabIndexer{}
	}
}
