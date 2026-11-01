package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// NuvioProvider is an installed Nuvio provider seen as a
// providers.Provider (id "nuvio:<id>").
type NuvioProvider struct {
	ext *Loaded
}

// NuvioProviders returns the installed, enabled Nuvio providers.
func (m *Manager) NuvioProviders() []providers.Provider {
	var out []providers.Provider
	for _, l := range m.byType(TypeNuvioProvider) {
		out = append(out, &NuvioProvider{ext: l})
	}
	return out
}

// NuvioProvider returns an installed, enabled Nuvio provider by provider
// id ("nuvio:<id>") or extension id ("nuvio.<id>").
func (m *Manager) NuvioProvider(id string) (*NuvioProvider, error) {
	for _, p := range m.NuvioProviders() {
		np := p.(*NuvioProvider)
		if np.ID() == id || np.ext.Manifest.ID == id {
			return np, nil
		}
	}
	return nil, fmt.Errorf("Nuvio provider %q is not installed or disabled", id)
}

func (p *NuvioProvider) ID() string {
	return "nuvio:" + strings.TrimPrefix(p.ext.Manifest.ID, nuvioExtPrefix)
}
func (p *NuvioProvider) Name() string { return p.ext.Manifest.Name }
func (p *NuvioProvider) Kind() string { return providers.KindNuvio }
func (p *NuvioProvider) Icon() string { return p.ext.Manifest.Icon }

// ExtensionID is the provider's extension id (for logs, settings...).
func (p *NuvioProvider) ExtensionID() string { return p.ext.Manifest.ID }

// Supports reports whether the provider's entry lists mediaType.
func (p *NuvioProvider) Supports(mediaType string) bool {
	meta := p.ext.Manifest.Nuvio
	if meta == nil || len(meta.SupportedTypes) == 0 {
		return mediaType == providers.TypeMovie || mediaType == providers.TypeTV
	}
	for _, t := range meta.SupportedTypes {
		if t == mediaType {
			return true
		}
	}
	return false
}

// Streams runs the provider's getStreams() for the movie or episode.
func (p *NuvioProvider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if r.TMDBID <= 0 {
		// Not the provider's fault: its health stays as it is.
		return nil, providers.Skip(errors.New("Nuvio providers need the title's TMDB id"))
	}
	if r.Type != providers.TypeMovie && r.Type != providers.TypeTV {
		return nil, fmt.Errorf("unknown media type %q", r.Type)
	}
	rt, err := p.ext.Runtime()
	if err != nil {
		return nil, err
	}
	raw, err := rt.callNuvio(ctx, strconv.Itoa(r.TMDBID), r.Type, r.Season, r.Episode)
	if err != nil {
		return nil, err
	}
	return parseNuvioStreams(raw, p.ID(), p.Name())
}

// nuvioStream is a stream as providers return it. They don't agree on
// types (size as text or bytes, quality as "1080p" or 1080...), so most
// fields are read leniently.
type nuvioStream struct {
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	URL       string          `json:"url"`
	Quality   json.RawMessage `json:"quality"`
	Size      json.RawMessage `json:"size"`
	Type      string          `json:"type"`
	Headers   map[string]any  `json:"headers"`
	Provider  string          `json:"provider"`
	Filename  string          `json:"fileName"`
	Subtitles []struct {
		URL      string `json:"url"`
		Lang     string `json:"lang"`
		Language string `json:"language"`
		Label    string `json:"label"`
	} `json:"subtitles"`
	BehaviorHints struct {
		NotWebReady  bool `json:"notWebReady"`
		ProxyHeaders struct {
			Request map[string]any `json:"request"`
		} `json:"proxyHeaders"`
	} `json:"behaviorHints"`
}

func parseNuvioStreams(raw json.RawMessage, providerID, providerName string) ([]providers.Stream, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		// Some providers wrap the list: {streams: [...]}.
		var wrapped struct {
			Streams []json.RawMessage `json:"streams"`
		}
		if json.Unmarshal(raw, &wrapped) != nil || wrapped.Streams == nil {
			return nil, errors.New("getStreams() didn't return a list of streams")
		}
		items = wrapped.Streams
	}
	out := make([]providers.Stream, 0, len(items))
	for _, it := range items {
		var w nuvioStream
		if json.Unmarshal(it, &w) != nil {
			continue
		}
		if s, ok := w.stream(providerID, providerName); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (w nuvioStream) stream(providerID, providerName string) (providers.Stream, bool) {
	s := providers.Stream{
		Provider: providerID, ProviderName: providerName, FileIdx: -1,
		Title: strings.TrimSpace(w.Title), NotWebReady: w.BehaviorHints.NotWebReady, Filename: strings.TrimSpace(w.Filename),
	}
	if s.Title == "" {
		s.Title = strings.TrimSpace(w.Name)
	}
	u := strings.TrimSpace(w.URL)
	switch {
	case nuvioIsHTTP(u):
		s.URL = u
	case strings.HasPrefix(strings.ToLower(u), "magnet:"):
		s.Magnet = u
		s.InfoHash = providers.InfoHashFromMagnet(u)
		if s.InfoHash == "" {
			return s, false
		}
	default:
		return s, false
	}

	headers := w.Headers
	if len(headers) == 0 {
		headers = w.BehaviorHints.ProxyHeaders.Request
	}
	if len(headers) > 0 {
		s.Headers = map[string]string{}
		for k, v := range headers {
			if k = strings.TrimSpace(k); k != "" && v != nil {
				s.Headers[k] = fmt.Sprint(v)
			}
		}
	}
	for _, sub := range w.Subtitles {
		if !nuvioIsHTTP(sub.URL) {
			continue
		}
		lang := sub.Lang
		if lang == "" {
			lang = sub.Language
		}
		s.Subtitles = append(s.Subtitles, providers.Subtitle{URL: sub.URL, Language: lang, Label: sub.Label})
	}

	quality := nuvioFlexString(w.Quality)
	s.Quality = providers.QualityFromText(quality)
	if s.Quality == "" {
		s.Quality = providers.QualityFromText(w.Title)
	}
	if s.Quality == "" {
		s.Quality = providers.QualityFromText(w.Name)
	}
	if size := nuvioFlexString(w.Size); size != "" {
		if n, err := strconv.ParseInt(size, 10, 64); err == nil {
			s.Size = n
		} else {
			s.Size = providers.SizeFromText(size)
		}
	}
	if s.Size == 0 {
		s.Size = providers.SizeFromText(w.Title)
	}
	s.Seeders = providers.SeedersFromText(w.Title)

	switch t := strings.ToLower(strings.TrimSpace(w.Type)); t {
	case "hls", "m3u8":
		s.Type = providers.StreamM3U8
	case "mp4", "mkv", "webm":
		s.Type = t
	default:
		if s.URL != "" {
			s.Type = providers.GuessType(s.URL)
		} else {
			s.Type = providers.StreamUnknown
		}
	}
	return s, true
}

func nuvioIsHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}
