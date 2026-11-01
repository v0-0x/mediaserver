// Package providers holds what every streaming provider of KumoBox shares:
// the request a provider gets for a movie or an episode, the streams it
// answers with, and helpers to read qualities, sizes and seeders out of the
// free text sources come with, rank streams and query many providers at
// once.
//
// The providers themselves live elsewhere: the built-in Internet Archive one
// in providers/archive, Stremio addons in providers/stremio and Nuvio
// JavaScript providers in the extensions sandbox (they all implement
// Provider). This package depends on none of them.
package providers

import (
	"context"
	"net/url"
	"strings"
)

// Media types of a Request (and of Provider.Supports).
const (
	TypeMovie = "movie"
	TypeTV    = "tv"
)

// Provider kinds.
const (
	KindBuiltin   = "builtin"
	KindStremio   = "stremio"
	KindNuvio     = "nuvio"
	KindExtension = "extension"
	// KindMediaServer is the user's own Jellyfin, Emby or Plex server.
	KindMediaServer = "mediaserver"
)

// Stream types.
const (
	StreamM3U8    = "m3u8"
	StreamMP4     = "mp4"
	StreamMKV     = "mkv"
	StreamWebM    = "webm"
	StreamUnknown = "unknown"
)

// Request is what a provider is asked for: a movie, or one episode of a
// show. Providers pick the ids they understand (TMDB, IMDb) or search by
// title and year.
type Request struct {
	// MediaID is the KumoBox media id (see catalog/ids.go).
	MediaID int `json:"mediaId"`
	// TMDBID is the TMDB movie or TV id, IMDbID the IMDb one ("tt0133093",
	// the show's for an episode), empty when TMDB doesn't know it.
	TMDBID int    `json:"tmdbId"`
	IMDbID string `json:"imdbId,omitempty"`
	// Type is TypeMovie or TypeTV.
	Type          string `json:"type"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle,omitempty"`
	// Year is the release (first air) year, 0 when unknown.
	Year int `json:"year,omitempty"`
	// Season and Episode of a show's episode (0 for movies).
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

// IsTV reports whether r asks for a show's episode.
func (r Request) IsTV() bool { return r.Type == TypeTV }

// Subtitle is an external subtitle file.
type Subtitle struct {
	URL string `json:"url"`
	// Language is a code (en, eng, pt-BR...) or a name, as the source gives
	// it.
	Language string `json:"language"`
	Label    string `json:"label,omitempty"`
}

// Stream is one way to watch the requested movie or episode. Exactly how
// depends on which fields are set, in this order of preference: URL (a
// direct http(s) link, possibly needing Headers), InfoHash/Magnet (a
// torrent, FileIdx being the video's index in it), YouTubeID, ExternalURL
// (a web page to open in the browser).
type Stream struct {
	// Provider is the provider's id ("archive", "stremio:<addon id>",
	// "nuvio:<id>"...), ProviderName its display name.
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName"`
	// Title describes the stream (often a release name, possibly over
	// several lines).
	Title string `json:"title"`

	URL string `json:"url,omitempty"`
	// Type is StreamM3U8, StreamMP4, StreamMKV, StreamWebM or
	// StreamUnknown.
	Type string `json:"type"`
	// Quality is "2160p", "1080p", "720p", "480p"... or "" when unknown.
	Quality string `json:"quality"`
	// Size in bytes (0 when unknown).
	Size int64 `json:"size,omitempty"`
	// Headers must be sent with every request for URL.
	Headers   map[string]string `json:"headers,omitempty"`
	Subtitles []Subtitle        `json:"subtitles,omitempty"`

	InfoHash string `json:"infoHash,omitempty"`
	// FileIdx is the video's index in the torrent, -1 when not given (the
	// largest file is the video).
	FileIdx int    `json:"fileIdx"`
	Magnet  string `json:"magnet,omitempty"`

	ExternalURL string `json:"externalUrl,omitempty"`
	YouTubeID   string `json:"youTubeId,omitempty"`

	// NotWebReady is set when a browser can't play URL as is (wrong
	// container, needs headers...): it goes through mpv, the proxy or a
	// transcode.
	NotWebReady bool `json:"notWebReady,omitempty"`
	// Filename is the video's file name, when known (helps subtitle
	// matching and release-name parsing).
	Filename string `json:"filename,omitempty"`
	Seeders  int    `json:"seeders,omitempty"`
}

// Direct reports whether s is a direct http(s) link.
func (s Stream) Direct() bool { return isHTTP(s.URL) }

// IsTorrent reports whether s is a torrent (and not a direct link).
func (s Stream) IsTorrent() bool { return !s.Direct() && (s.InfoHash != "" || s.Magnet != "") }

// Provider finds streams of movies and episodes.
type Provider interface {
	ID() string
	Name() string
	// Kind is KindBuiltin, KindStremio, KindNuvio or KindExtension.
	Kind() string
	// Supports reports whether the provider has streams of mediaType
	// (TypeMovie or TypeTV).
	Supports(mediaType string) bool
	Streams(ctx context.Context, r Request) ([]Stream, error)
}

// SubtitleProvider finds external subtitles (Stremio subtitle addons...).
// Implementations usually also have ID() and Name().
type SubtitleProvider interface {
	Subtitles(ctx context.Context, r Request) ([]Subtitle, error)
}

// Video is the file a stream links to, as subtitle providers match
// subtitles with it: its size in bytes and OpenSubtitles hash (0 and ""
// when unknown), and its file name.
type Video struct {
	Size     int64
	Hash     string
	Filename string
}

// VideoSubtitleProvider is a SubtitleProvider that can also find the
// subtitles made for the very video file playing.
type VideoSubtitleProvider interface {
	SubtitleProvider
	VideoSubtitles(ctx context.Context, r Request, v Video) ([]Subtitle, error)
}

// Iconer is implemented by providers that have a logo.
type Iconer interface {
	Icon() string
}

// Info describes a provider for the UI.
type Info struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Kind  string   `json:"kind"`
	Icon  string   `json:"icon,omitempty"`
	Types []string `json:"types"`
}

// Describe returns p's Info.
func Describe(p Provider) Info {
	inf := Info{ID: p.ID(), Name: p.Name(), Kind: p.Kind(), Types: []string{}}
	if ic, ok := p.(Iconer); ok {
		inf.Icon = ic.Icon()
	}
	for _, t := range []string{TypeMovie, TypeTV} {
		if p.Supports(t) {
			inf.Types = append(inf.Types, t)
		}
	}
	return inf
}

func isHTTP(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}
