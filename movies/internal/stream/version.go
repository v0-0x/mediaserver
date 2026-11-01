package stream

import (
	"sort"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/release"
)

// Versions
//
// A show is best watched in one version: the same provider, quality and
// release (same audio, subtitles and episode cuts from one episode to the
// next). Auto play next keeps the provider and version of the episode that
// just played; the provider and version of the last stream of a show that
// played are also remembered (app), and preferred for the next episodes
// played by hand: the Play button and the Watch online list.

// Version is which of a provider's streams played: the provider, and what
// the stream's release name says.
type Version struct {
	Provider string `json:"provider"`
	// Quality is "1080p"... ("" when unknown).
	Quality string `json:"quality,omitempty"`
	// Source (WEB-DL, BluRay...) and Group (the release group) come from
	// the release name.
	Source string `json:"source,omitempty"`
	Group  string `json:"group,omitempty"`
	// Dub is set for an extension's dubbed version.
	Dub bool `json:"dub,omitempty"`
}

// ReleaseName is the name a stream's release goes by: its file name, else
// its title's first line.
func ReleaseName(s providers.Stream) string {
	if s.Filename != "" {
		return s.Filename
	}
	t := strings.TrimSpace(s.Title)
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// NewVersion is the version of a stream of provider in quality, whose
// release is named name.
func NewVersion(provider, quality, name string, dub bool) Version {
	v := Version{Provider: provider, Quality: providers.QualityFromText(quality), Dub: dub}
	if name != "" {
		info := release.Parse(name)
		v.Source, v.Group = info.Source, info.Group
		if v.Quality == "" {
			v.Quality = providers.QualityFromText(info.Resolution)
		}
	}
	return v
}

// VersionOf is a stream's version.
func VersionOf(s providers.Stream) Version {
	return NewVersion(s.Provider, s.Quality, ReleaseName(s), false)
}

// Same reports whether two versions are the same (to save one only when it
// changed).
func (v Version) Same(o Version) bool {
	return v.Provider == o.Provider && v.Quality == o.Quality && v.Source == o.Source &&
		strings.EqualFold(v.Group, o.Group) && v.Dub == o.Dub
}

// score is how much of v a stream of v's provider has: its release group
// (4), source (2) and quality (1). -1 for another provider's.
func (v Version) score(s providers.Stream) int {
	if s.Provider != v.Provider {
		return -1
	}
	o := VersionOf(s)
	n := 0
	if v.Group != "" && strings.EqualFold(v.Group, o.Group) {
		n += 4
	}
	if v.Source != "" && v.Source == o.Source {
		n += 2
	}
	if v.Quality != "" && v.Quality == o.Quality {
		n++
	}
	return n
}

// preferVersion moves the direct links of v's provider before every other
// stream, the ones most like v first (the others keep their order), and
// marks the best ones Preferred. Without v, streams are returned as they
// are.
func preferVersion(streams []Stream, v *Version) []Stream {
	if v == nil || v.Provider == "" {
		return streams
	}
	type scored struct {
		s     Stream
		score int
	}
	var mine []scored
	var rest []Stream
	for _, s := range streams {
		if s.Kind == KindDirect && s.Provider == v.Provider {
			mine = append(mine, scored{s, v.score(s.Stream)})
		} else {
			rest = append(rest, s)
		}
	}
	if len(mine) == 0 {
		return streams
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].score > mine[j].score })
	out := make([]Stream, 0, len(streams))
	for _, m := range mine {
		m.s.Preferred = m.score == mine[0].score
		out = append(out, m.s)
	}
	return append(out, rest...)
}
