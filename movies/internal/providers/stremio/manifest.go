// Package stremio speaks the Stremio addon protocol: it reads addon
// manifests and addon collections, asks addons for the streams and
// subtitles of a movie or an episode, and keeps the addons the user
// installed (each one becomes a providers.Provider).
//
// The protocol is plain HTTP + JSON: an addon is a base URL (its "transport
// URL" without "/manifest.json") serving /manifest.json and
// /{resource}/{type}/{id}.json. KumoBox only uses the "stream" and
// "subtitles" resources, with IMDb ids ("tt0133093", "tt0944947:1:2" for
// an episode) or, for addons that only take them, TMDB ids ("tmdb:603").
package stremio

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Stremio content types KumoBox uses.
const (
	TypeMovie  = "movie"
	TypeSeries = "series"
)

// Resources KumoBox uses.
const (
	ResourceStream    = "stream"
	ResourceSubtitles = "subtitles"
)

// Manifest describes an addon.
type Manifest struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Logo        string `json:"logo,omitempty"`
	Background  string `json:"background,omitempty"`
	// Types are the content types the addon serves (movie, series,
	// channel, tv...).
	Types     []string   `json:"types"`
	Catalogs  []Catalog  `json:"catalogs"`
	Resources []Resource `json:"resources"`
	// IDPrefixes restrict the ids the addon is asked about ("tt" for IMDb
	// ids...); nil means any id.
	IDPrefixes    []string      `json:"idPrefixes,omitempty"`
	BehaviorHints BehaviorHints `json:"behaviorHints"`
	ContactEmail  string        `json:"contactEmail,omitempty"`
}

// Catalog is a catalog an addon offers (KumoBox lists them, it doesn't
// browse them).
type Catalog struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

// BehaviorHints are an addon's flags.
type BehaviorHints struct {
	// Adult addons need the user's consent.
	Adult bool `json:"adult,omitempty"`
	// P2P addons stream over BitTorrent (the user's IP is visible to
	// peers).
	P2P bool `json:"p2p,omitempty"`
	// Configurable addons have a /configure page; ConfigurationRequired
	// ones don't work until configured there.
	Configurable          bool `json:"configurable,omitempty"`
	ConfigurationRequired bool `json:"configurationRequired,omitempty"`
}

// Resource is a resource an addon serves. In a manifest it's either a name
// ("stream": the manifest's types and idPrefixes apply) or an object
// ({"name": "stream", "types": ["movie"], "idPrefixes": ["tt"]}).
type Resource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types,omitempty"`
	IDPrefixes []string `json:"idPrefixes,omitempty"`
	// short is set for the string form: the manifest's types and prefixes
	// apply.
	short bool
}

func (r *Resource) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		*r = Resource{Name: name, short: true}
		return nil
	}
	var obj struct {
		Name       string   `json:"name"`
		Types      []string `json:"types"`
		IDPrefixes []string `json:"idPrefixes"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("invalid resource: %s", truncate(string(b), 80))
	}
	*r = Resource{Name: obj.Name, Types: obj.Types, IDPrefixes: obj.IDPrefixes}
	return nil
}

func (r Resource) MarshalJSON() ([]byte, error) {
	if r.short {
		return json.Marshal(r.Name)
	}
	type plain struct {
		Name       string   `json:"name"`
		Types      []string `json:"types,omitempty"`
		IDPrefixes []string `json:"idPrefixes,omitempty"`
	}
	return json.Marshal(plain{r.Name, r.Types, r.IDPrefixes})
}

// Validate checks the fields KumoBox relies on.
func (m Manifest) Validate() error {
	var missing []string
	if strings.TrimSpace(m.ID) == "" {
		missing = append(missing, "id")
	}
	if strings.TrimSpace(m.Name) == "" {
		missing = append(missing, "name")
	}
	if len(m.Resources) == 0 {
		missing = append(missing, "resources")
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid addon manifest: missing %s", strings.Join(missing, ", "))
	}
	if len(m.ID) > 200 || len(m.Name) > 200 {
		return errors.New("invalid addon manifest: id or name too long")
	}
	return nil
}

// Serves reports whether the addon serves resource for the content type typ,
// and which id prefixes it accepts then (nil: any id).
func (m Manifest) Serves(resource, typ string) (bool, []string) {
	for _, r := range m.Resources {
		if r.Name != resource {
			continue
		}
		types, prefixes := r.Types, r.IDPrefixes
		if r.short {
			types, prefixes = m.Types, m.IDPrefixes
		}
		if containsFold(types, typ) {
			return true, prefixes
		}
	}
	return false, nil
}

// Useful reports whether the addon serves streams or subtitles of movies or
// series: the only addons KumoBox can use.
func (m Manifest) Useful() bool {
	for _, res := range []string{ResourceStream, ResourceSubtitles} {
		for _, t := range []string{TypeMovie, TypeSeries} {
			if ok, _ := m.Serves(res, t); ok {
				return true
			}
		}
	}
	return false
}

// Type maps a request's media type to Stremio's.
func Type(mediaType string) string {
	if mediaType == providers.TypeTV {
		return TypeSeries
	}
	return TypeMovie
}

// ErrNoIMDbID is returned for titles TMDB has no IMDb id for, when the addon
// only takes IMDb ids.
var ErrNoIMDbID = errors.New("this addon needs the title's IMDb id, and TMDB doesn't know it")

// VideoID is the id to ask the addon for r with (for resource): the IMDb
// id ("tt0133093", "tt0944947:1:2" for an episode) when the addon takes
// it, else the TMDB one ("tmdb:603", "tmdb:1399:1:2") when it takes that.
func (m Manifest) VideoID(resource string, r providers.Request) (string, error) {
	ok, prefixes := m.Serves(resource, Type(r.Type))
	if !ok {
		return "", fmt.Errorf("%s doesn't serve %s of %s", m.Name, resource, Type(r.Type))
	}
	episode := ""
	if r.IsTV() {
		if r.Season < 0 || r.Episode <= 0 {
			return "", errors.New("no episode given")
		}
		episode = fmt.Sprintf(":%d:%d", r.Season, r.Episode)
	}
	var candidates []string
	if imdb := strings.TrimSpace(r.IMDbID); strings.HasPrefix(imdb, "tt") {
		candidates = append(candidates, imdb+episode)
	}
	if r.TMDBID > 0 {
		candidates = append(candidates, fmt.Sprintf("tmdb:%d%s", r.TMDBID, episode))
	}
	for _, id := range candidates {
		if prefixes == nil || hasAnyPrefix(id, prefixes) {
			return id, nil
		}
	}
	if r.IMDbID == "" && (prefixes == nil || hasAnyPrefix("tt0000000", prefixes)) {
		return "", ErrNoIMDbID
	}
	return "", fmt.Errorf("%s doesn't know this title's ids (it takes %s)", m.Name, strings.Join(prefixes, ", "))
}

func hasAnyPrefix(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
