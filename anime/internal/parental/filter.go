package parental

import (
	"context"
	"strings"
)

// Lookup finds anime (and manga) by AniList ID, from what Aniko keeps of
// AniList (asking it for the ones it doesn't have). Not found: missing.
type Lookup func(ctx context.Context, ids []int) map[int]Media

// Filter takes out of Aniko's JSON answers what the rules block, wherever
// they list anime: AniList's own objects (an "id" with "isAdult" or
// "genres"), and Aniko's that name one by "mediaId" (local files, watch
// history, list entries, shared files…). Blocked items are dropped from
// the lists they're in; an answer that is itself about a blocked anime (its
// page) is blocked whole.
type Filter struct {
	Rules  *Rules
	Lookup Lookup
}

// Apply returns v (decoded JSON) without what's blocked, and whether v is
// itself blocked (with why).
func (f Filter) Apply(ctx context.Context, v any) (any, string) {
	if !f.Rules.Active() {
		return v, ""
	}
	var known map[int]Media
	// The 18+ flag and genres of the anime only named by ID (or missing
	// their genres), when the rules look at those; with 18+ blocked, every
	// anime's tags.
	if (f.Rules.NoAdult || len(f.Rules.Genres) > 0) && !f.Rules.OnlyAllowed && f.Lookup != nil {
		need := map[int]bool{}
		collect(v, need, f.Rules.NoAdult)
		if len(need) > 0 {
			ids := make([]int, 0, len(need))
			for id := range need {
				ids = append(ids, id)
			}
			known = f.Lookup(ctx, ids)
		}
	}
	if o, ok := v.(map[string]any); ok {
		if why := f.blocked(o, known); why != "" {
			return nil, why
		}
	}
	return f.walk(v, known), ""
}

func (f Filter) walk(v any, known map[int]Media) any {
	switch t := v.(type) {
	case []any:
		out := t[:0]
		for _, e := range t {
			if o, ok := e.(map[string]any); ok && f.blocked(o, known) != "" {
				continue
			}
			out = append(out, f.walk(e, known))
		}
		return out
	case map[string]any:
		for k, e := range t {
			t[k] = f.walk(e, known)
		}
		return t
	}
	return v
}

// The keys under which Aniko's and AniList's objects carry an anime.
var mediaKeys = []string{"media", "node", "mediaRecommendation"}

// blocked says why o is blocked: it is an anime, or it is about one.
func (f Filter) blocked(o map[string]any, known map[int]Media) string {
	if m, ok := asMedia(o, known); ok {
		return f.Rules.Reason(m)
	}
	for _, k := range mediaKeys {
		if c, ok := o[k].(map[string]any); ok {
			if m, ok := asMedia(c, known); ok {
				return f.Rules.Reason(m)
			}
		}
	}
	if id := number(o["mediaId"]); id > 0 {
		m, ok := known[id]
		if !ok {
			m = Media{ID: id}
		}
		return f.Rules.Reason(m)
	}
	return ""
}

// asMedia reads o as an AniList anime or manga.
func asMedia(o map[string]any, known map[int]Media) (Media, bool) {
	id := number(o["id"])
	if id <= 0 {
		return Media{}, false
	}
	adult, hasAdult := o["isAdult"].(bool)
	genres, hasGenres := o["genres"].([]any)
	if !hasAdult && !hasGenres {
		return Media{}, false
	}
	m := Media{ID: id, Adult: adult, Known: true}
	for _, g := range genres {
		if s, ok := g.(string); ok {
			m.Genres = append(m.Genres, s)
		}
	}
	if k, ok := known[id]; ok {
		if !hasGenres {
			m.Genres = k.Genres
		}
		m.Adult = m.Adult || k.Adult
		m.Mature = k.Mature
	}
	return m, true
}

// collect finds the IDs of the anime v names without their genres (all
// of them with tags: their tags are never there).
func collect(v any, need map[int]bool, tags bool) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			collect(e, need, tags)
		}
	case map[string]any:
		if id := number(t["id"]); id > 0 {
			_, hasAdult := t["isAdult"]
			_, hasGenres := t["genres"]
			if hasAdult && (!hasGenres || tags) || hasGenres && tags {
				need[id] = true
			}
		}
		if id := number(t["mediaId"]); id > 0 {
			nested := false
			for _, k := range mediaKeys {
				if c, ok := t[k].(map[string]any); ok && number(c["id"]) == id {
					nested = true
				}
			}
			if !nested {
				need[id] = true
			}
		}
		for _, e := range t {
			collect(e, need, tags)
		}
	}
}

func number(v any) int {
	if f, ok := v.(float64); ok && f == float64(int(f)) {
		return int(f)
	}
	return 0
}

// Message is what a device shows for a blocked anime.
func Message(why string) string {
	switch {
	case why == "18+":
		return "This anime is 18+: it's blocked on this device (parental controls)."
	case why == "blocked", why == "not allowed":
		return "This anime is blocked on this device (parental controls)."
	case strings.TrimSpace(why) != "":
		return "This anime is blocked on this device: " + why + " (parental controls)."
	}
	return "Blocked on this device (parental controls)."
}
