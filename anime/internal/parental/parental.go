// Package parental holds parental controls: what a device may watch, set
// for it on the computer (or Aniko Hub) that shares with it, and enforced on
// both: the host shares and streams nothing else to it, and the device's
// own Aniko hides the rest and refuses to open or play it (filter.go).
package parental

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"slices"
	"strings"
)

// Rules are a device's parental controls.
type Rules struct {
	// NoAdult blocks 18+ anime (and manga): AniList's own, Hentai and
	// Ecchi, and those tagged with sexual content (Media.Mature).
	NoAdult bool `json:"noAdult,omitempty"`
	// Genres are blocked AniList genres ("Ecchi", "Horror"…).
	Genres []string `json:"genres,omitempty"`
	// Blocked anime, by AniList ID.
	Blocked []int `json:"blocked,omitempty"`
	// OnlyAllowed: nothing but Allowed may be watched.
	OnlyAllowed bool  `json:"onlyAllowed,omitempty"`
	Allowed     []int `json:"allowed,omitempty"`
	// PIN lifts them on the device itself, for when the host is gone for
	// good: "salt:sha256(salt+pin)" in hex, never the PIN.
	PIN string `json:"pin,omitempty"`
	// SetAt: when they were last changed (ms).
	SetAt int64 `json:"setAt,omitempty"`
}

// Active reports rules that restrict something.
func (r *Rules) Active() bool {
	return r != nil && (r.NoAdult || len(r.Genres) > 0 || len(r.Blocked) > 0 || r.OnlyAllowed)
}

// Clean returns the rules tidied: genres trimmed and unique, IDs positive
// and unique, sorted, within limits.
func (r Rules) Clean() Rules {
	var genres []string
	for _, g := range r.Genres {
		if g = strings.TrimSpace(g); g != "" && len(g) <= 40 && !slices.Contains(genres, g) {
			genres = append(genres, g)
		}
	}
	slices.Sort(genres)
	r.Genres = genres
	r.Blocked = ids(r.Blocked)
	r.Allowed = ids(r.Allowed)
	if len(r.Genres) > 64 {
		r.Genres = r.Genres[:64]
	}
	return r
}

func ids(in []int) []int {
	var out []int
	for _, id := range in {
		if id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	if len(out) > 5000 {
		out = out[:5000]
	}
	return out
}

// Media is what the rules look at: an anime's (or manga's) 18+ flag and
// genres. Known: the rest was found (an ID alone, not found, is blocked
// only when the rules allow only some anime or block it by ID). Mature:
// tagged with sexual content (anilist.MatureTags).
type Media struct {
	ID     int
	Adult  bool
	Genres []string
	Mature bool
	Known  bool
}

// adultGenres are AniList's genres that count as 18+: Hentai is its 18+
// genre (even where the flag is missing), Ecchi its fan service one.
var adultGenres = []string{"Hentai", "Ecchi"}

// Reason says why m is blocked, "" when it isn't.
func (r *Rules) Reason(m Media) string {
	if !r.Active() {
		return ""
	}
	if slices.Contains(r.Blocked, m.ID) {
		return "blocked"
	}
	if r.OnlyAllowed && !slices.Contains(r.Allowed, m.ID) {
		return "not allowed"
	}
	// Allowed by name: let through whatever else it is.
	if r.OnlyAllowed {
		return ""
	}
	if r.NoAdult && (m.Adult || m.Mature) {
		return "18+"
	}
	for _, g := range m.Genres {
		if slices.ContainsFunc(r.Genres, func(b string) bool { return strings.EqualFold(b, g) }) {
			return g
		}
	}
	if r.NoAdult && slices.ContainsFunc(m.Genres, func(g string) bool {
		return slices.ContainsFunc(adultGenres, func(a string) bool { return strings.EqualFold(a, g) })
	}) {
		return "18+"
	}
	return ""
}

// Merge combines several hosts' rules into the strictest: any one's blocks
// count; with more than one allow-list, only what they all allow.
func Merge(all []Rules) *Rules {
	var out Rules
	var lists [][]int
	for _, r := range all {
		if !r.Active() {
			continue
		}
		out.NoAdult = out.NoAdult || r.NoAdult
		out.Genres = append(out.Genres, r.Genres...)
		out.Blocked = append(out.Blocked, r.Blocked...)
		if r.OnlyAllowed {
			out.OnlyAllowed = true
			lists = append(lists, r.Allowed)
		}
		out.SetAt = max(out.SetAt, r.SetAt)
	}
	if len(lists) > 0 {
		allowed := lists[0]
		for _, l := range lists[1:] {
			allowed = slices.DeleteFunc(slices.Clone(allowed), func(id int) bool { return !slices.Contains(l, id) })
		}
		out.Allowed = allowed
	}
	out = out.Clean()
	if !out.Active() {
		return nil
	}
	return &out
}

// HashPIN returns what Rules.PIN keeps of a PIN ("" for none).
func HashPIN(pin string) string {
	if pin == "" {
		return ""
	}
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)
	s := hex.EncodeToString(salt)
	return s + ":" + digest(s, pin)
}

// CheckPIN reports whether pin is the one hashed in h.
func CheckPIN(h, pin string) bool {
	salt, sum, ok := strings.Cut(h, ":")
	if !ok || pin == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(digest(salt, pin)), []byte(sum)) == 1
}

func digest(salt, pin string) string {
	d := sha256.Sum256([]byte(salt + pin))
	return hex.EncodeToString(d[:])
}

// Genres are AniList's anime genres, for choosing which to block.
var Genres = []string{
	"Action", "Adventure", "Comedy", "Drama", "Ecchi", "Fantasy", "Hentai", "Horror", "Mahou Shoujo", "Mecha", "Music",
	"Mystery", "Psychological", "Romance", "Sci-Fi", "Slice of Life", "Sports", "Supernatural", "Thriller",
}
