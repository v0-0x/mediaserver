package stremio

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// wireStream is a stream object as addons send it. Fields addons get wrong
// (numbers as strings...) are read leniently.
type wireStream struct {
	URL         string         `json:"url"`
	YTID        string         `json:"ytId"`
	InfoHash    string         `json:"infoHash"`
	FileIdx     flexInt        `json:"fileIdx"`
	ExternalURL string         `json:"externalUrl"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Subtitles   []wireSubtitle `json:"subtitles"`
	Sources     []string       `json:"sources"`
	Hints       struct {
		NotWebReady  bool    `json:"notWebReady"`
		BingeGroup   string  `json:"bingeGroup"`
		Filename     string  `json:"filename"`
		VideoSize    flexInt `json:"videoSize"`
		ProxyHeaders struct {
			Request map[string]any `json:"request"`
		} `json:"proxyHeaders"`
	} `json:"behaviorHints"`
}

type wireSubtitle struct {
	ID    any    `json:"id"`
	URL   string `json:"url"`
	Lang  string `json:"lang"`
	Label string `json:"label"`
}

func (w wireSubtitle) subtitle() (providers.Subtitle, bool) {
	if !isHTTPURL(w.URL) {
		return providers.Subtitle{}, false
	}
	return providers.Subtitle{URL: w.URL, Language: strings.TrimSpace(w.Lang), Label: strings.TrimSpace(w.Label)}, true
}

// flexInt is a JSON number that may come as a string; set tells it was
// there at all.
type flexInt struct {
	n   int64
	set bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // ignored rather than failing the whole stream
	}
	f.n, f.set = int64(n), true
	return nil
}

// parseStream turns an addon's stream object into a providers.Stream. It
// returns false for streams Moviko can't use (usenet, archives...).
func parseStream(raw json.RawMessage, a Addon) (providers.Stream, bool) {
	var w wireStream
	if err := json.Unmarshal(raw, &w); err != nil {
		return providers.Stream{}, false
	}
	text := strings.TrimSpace(w.Description)
	if text == "" {
		text = strings.TrimSpace(w.Title)
	}
	name := strings.TrimSpace(w.Name)
	s := providers.Stream{
		Provider:     ProviderID(a.Manifest.ID),
		ProviderName: a.Manifest.Name,
		Title:        text,
		FileIdx:      -1,
		NotWebReady:  w.Hints.NotWebReady,
		Filename:     strings.TrimSpace(w.Hints.Filename),
		YouTubeID:    strings.TrimSpace(w.YTID),
	}
	if s.Title == "" {
		s.Title = name
	}
	if s.Filename == "" {
		s.Filename = filenameFromText(text)
	}

	switch u := strings.TrimSpace(w.URL); {
	case isHTTPURL(u):
		s.URL = u
	case strings.HasPrefix(strings.ToLower(u), "magnet:"):
		s.InfoHash = providers.InfoHashFromMagnet(u)
		s.Magnet = u
	}
	if h := providers.NormalizeInfoHash(w.InfoHash); h != "" && s.URL == "" {
		s.InfoHash = h
	}
	if s.InfoHash != "" {
		if w.FileIdx.set && w.FileIdx.n >= 0 {
			s.FileIdx = int(w.FileIdx.n)
		}
		if s.Magnet == "" {
			s.Magnet = providers.MagnetURI(s.InfoHash, firstNonEmpty(s.Filename, firstLine(text), a.Manifest.Name), trackers(w.Sources))
		}
	}
	if isHTTPURL(w.ExternalURL) {
		s.ExternalURL = strings.TrimSpace(w.ExternalURL)
	}
	if s.URL == "" && s.InfoHash == "" && s.Magnet == "" && s.YouTubeID == "" && s.ExternalURL == "" {
		return providers.Stream{}, false
	}

	if len(w.Hints.ProxyHeaders.Request) > 0 {
		s.Headers = map[string]string{}
		for k, v := range w.Hints.ProxyHeaders.Request {
			if k = strings.TrimSpace(k); k != "" && v != nil {
				s.Headers[k] = fmt.Sprint(v)
			}
		}
	}
	for _, sub := range w.Subtitles {
		if x, ok := sub.subtitle(); ok {
			s.Subtitles = append(s.Subtitles, x)
		}
	}

	all := name + "\n" + text
	s.Quality = providers.QualityFromText(name)
	if s.Quality == "" {
		s.Quality = providers.QualityFromText(text)
	}
	if s.Quality == "" {
		s.Quality = providers.QualityFromText(s.Filename)
	}
	if w.Hints.VideoSize.set && w.Hints.VideoSize.n > 0 {
		s.Size = w.Hints.VideoSize.n
	} else {
		s.Size = providers.SizeFromText(all)
	}
	s.Seeders = providers.SeedersFromText(all)

	switch {
	case s.URL != "":
		s.Type = providers.GuessType(s.URL)
		if s.Type == providers.StreamUnknown && s.Filename != "" {
			s.Type = providers.GuessType(s.Filename)
		}
	case s.Filename != "":
		s.Type = providers.GuessType(s.Filename)
	default:
		s.Type = providers.StreamUnknown
	}
	return s, true
}

// trackers reads a stream's "sources": "tracker:udp://..." entries are
// trackers, "dht:..." ones are ignored.
func trackers(sources []string) []string {
	var out []string
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if t, ok := strings.CutPrefix(src, "tracker:"); ok && t != "" {
			out = append(out, t)
		}
	}
	return out
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".webm": true, ".mov": true, ".ts": true, ".m2ts": true, ".wmv": true,
}

// filenameFromText returns the first line of a stream's description when
// it's a video file name (as torrent addons write it).
func filenameFromText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if videoExts[strings.ToLower(path.Ext(line))] && !strings.ContainsAny(line, "/\\") {
			return line
		}
	}
	return ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}
