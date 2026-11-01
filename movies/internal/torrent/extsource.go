package torrent

// Torrent providers of extensions
//
// Extensions (Seanime-style, see package extensions) bring torrent
// providers of their own: a search for free text, a "smart" search for a
// title's episode or batch, and a way to get a result's magnet link. The
// Manager wraps each one in a Source, so they're searched along with the
// built-in sources.

import (
	"context"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// Provider is the torrent provider of an extension.
type Provider interface {
	ID() string
	Name() string
	Search(ctx context.Context, query string) ([]*SearchResult, error)
	SmartSearch(ctx context.Context, q SmartQuery) ([]*SearchResult, error)
	// Magnet resolves the magnet link of a result (some providers only
	// have it on the torrent's page).
	Magnet(ctx context.Context, r *SearchResult) (string, error)
}

// SmartQuery is what an extension's smart search gets. Episode is the key
// of the episode looked for, 0 for a movie or a whole show; with Batch it's
// the season's key (S02 is 2000), whose pack is looked for.
type SmartQuery struct {
	Media      *catalog.Media
	Query      string // free text overrides the generated one
	Episode    int
	Batch      bool
	Resolution string // "1080", "" for any
}

// extensionSource is an extension's Provider as a Source. id is the
// provider's id, made unique among the sources.
type extensionSource struct {
	id string
	p  Provider
}

func (e *extensionSource) ID() string   { return e.id }
func (e *extensionSource) Name() string { return e.p.Name() }

// Supports: extensions don't say what they find; they're asked everything.
func (e *extensionSource) Supports(string) bool { return true }

func (e *extensionSource) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	res, err := e.p.Search(ctx, query)
	return e.tidy(res, MediaQuery{}), err
}

func (e *extensionSource) SearchMedia(ctx context.Context, q MediaQuery) ([]*SearchResult, error) {
	q = q.normalized()
	if q.Query != "" && q.Type == "" {
		return e.Search(ctx, q.Query)
	}
	sq := SmartQuery{Media: q.catalogMedia(), Query: q.Query, Resolution: strings.TrimSuffix(q.Resolution, "p")}
	if q.Type == MediaTV {
		switch {
		case q.Episode > 0:
			sq.Episode = catalog.EpisodeKey(q.Season, q.Episode)
		case q.Season > 0:
			sq.Episode, sq.Batch = catalog.EpisodeKey(q.Season, 0), true
		default:
			sq.Batch = true
		}
	}
	res, err := e.p.SmartSearch(ctx, sq)
	return e.tidy(res, q), err
}

func (e *extensionSource) Magnet(ctx context.Context, r *SearchResult) (string, error) {
	return e.p.Magnet(ctx, r)
}

// tidy labels an extension's results as this source's, and turns their
// episode numbers into keys: extensions number episodes within the season
// (5 for S02E05), as anime providers do.
func (e *extensionSource) tidy(res []*SearchResult, q MediaQuery) []*SearchResult {
	out := res[:0]
	for _, r := range res {
		if r == nil {
			continue
		}
		r.Provider = e.id
		n := r.EpisodeNumber
		if n > 0 && n < catalog.EpisodeKey(1, 0) {
			r.EpisodeNumber = -1
		}
		annotate(r)
		if r.EpisodeNumber < 0 && !r.IsBatch && n > 0 && n < catalog.EpisodeKey(1, 0) && q.Type == MediaTV && q.Season > 0 {
			r.EpisodeNumber = catalog.EpisodeKey(q.Season, n)
		}
		out = append(out, r)
	}
	return out
}

// catalogMedia is the media q was made from, or one made up from what q
// says.
func (q MediaQuery) catalogMedia() *catalog.Media {
	if q.Media != nil {
		return q.Media
	}
	m := &catalog.Media{TMDBID: q.TMDBID, IMDbID: q.IMDbID, TVDBID: q.TVDBID, Synonyms: []string{}}
	m.Title.English, m.Title.UserPreferred = q.Title, q.Title
	if len(q.AltTitles) > 0 {
		m.Title.Native = q.AltTitles[0]
		m.Synonyms = append(m.Synonyms, q.AltTitles...)
	}
	switch q.Type {
	case MediaTV:
		m.Type, m.Format = catalog.TypeTV, catalog.TypeTV
		if q.TMDBID > 0 {
			m.ID = catalog.ShowID(q.TMDBID)
		}
	default:
		m.Type, m.Format = catalog.TypeMovie, catalog.TypeMovie
		m.ID = catalog.MovieID(q.TMDBID)
	}
	if q.Year > 0 {
		y := q.Year
		m.StartDate.Year, m.SeasonYear = &y, &y
	}
	return m
}
