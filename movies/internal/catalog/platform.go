package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Platform is the catalog service the rest of the app talks to: movie and
// show data from TMDB, cached in the database (and served from there,
// however old, when TMDB can't be reached), and the user's list and diary,
// kept in the database (list.go, diary.go). The "viewer" is the connected
// Letterboxd member, set by package letterboxd.
type Platform struct {
	client   *Client
	db       *db.DB
	hub      *events.Hub
	settings *config.Store

	// OnCompleted is called, in a goroutine of its own, when UpdateProgress
	// moves a list entry to COMPLETED (a movie or a show watched to the
	// end). Set it before the Platform is used.
	OnCompleted func(mediaID int)

	mu     sync.RWMutex
	viewer *Viewer
	// list holds the list snapshots shared with every caller of
	// Collection (list.go). They are never modified: a change to the list
	// drops them, and the next reader builds new ones.
	list *listState
	// listVersion counts the changes to the list, so that a snapshot built
	// while one was made isn't kept.
	listVersion uint64

	// listMu serializes changes to the list, so that a read-modify-write
	// (progress) can't be undone by another change made meanwhile.
	listMu sync.Mutex

	// refreshing allows one background refresh of the list's media at a
	// time (list.go); refreshFailed is when the last one failed.
	refreshing    atomic.Bool
	refreshFailed atomic.Int64

	// discoverMu allows one Discover fetch at a time, and discovering one
	// refresh in the background (discover.go).
	discoverMu  sync.Mutex
	discovering atomic.Bool
}

const kvViewer = "letterboxd_viewer"

// NewPlatform returns the catalog. settings may be nil (tests): then the
// defaults apply, without a TMDB key.
func NewPlatform(d *db.DB, hub *events.Hub, settings *config.Store) *Platform {
	p := &Platform{db: d, hub: hub, settings: settings}
	p.client = NewClient(p.cfg)
	var v Viewer
	if ok, _ := d.GetKV(kvViewer, &v); ok && (v.ID != 0 || v.Name != "") {
		p.viewer = &v
	}
	return p
}

func (p *Platform) cfg() config.Settings {
	if p.settings == nil {
		return config.Defaults()
	}
	return p.settings.Get()
}

// Client is the TMDB client.
func (p *Platform) Client() *Client { return p.client }

// Configured reports whether a TMDB key is set.
func (p *Platform) Configured() bool { return p.client.Configured() }

// mapper returns what mapping TMDB data needs, for the current settings.
// Genre names are only fetched for lists (withGenres): a title page has
// them already.
func (p *Platform) mapper(ctx context.Context, withGenres bool) *mapping {
	mp := &mapping{c: p.client, region: p.cfg().TMDB.Region, now: time.Now(), genres: map[string]map[int]string{}}
	if withGenres {
		for _, kind := range []string{TypeMovie, TypeTV} {
			if g, err := p.client.genreNames(ctx, kind); err == nil {
				mp.genres[kind] = g
			}
		}
	}
	return mp
}

// ---------------------------------------------------------------------------
// Viewer

// SetViewer connects a Letterboxd member (nil disconnects). It is saved, so
// it's still connected after a restart.
func (p *Platform) SetViewer(v *Viewer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v == nil {
		if err := p.db.DeleteKV(kvViewer); err != nil {
			return err
		}
		p.viewer = nil
		return nil
	}
	c := *v
	if err := p.db.SetKV(kvViewer, c); err != nil {
		return err
	}
	p.viewer = &c
	return nil
}

// Viewer is the connected Letterboxd member, nil when none is.
func (p *Platform) Viewer() *Viewer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.viewer == nil {
		return nil
	}
	c := *p.viewer
	return &c
}

// LoggedIn reports whether a Letterboxd member is connected.
func (p *Platform) LoggedIn() bool { return p.Viewer() != nil }

// ---------------------------------------------------------------------------
// Media

func liteKey(id int) string   { return "media-lite:" + strconv.Itoa(id) }
func detailKey(id int) string { return "media:" + strconv.Itoa(id) }

// Cache lifetimes. Titles still airing (or not out yet) change more often.
const (
	liteTTL        = 24 * time.Hour
	detailLiveTTL  = 12 * time.Hour
	detailFinalTTL = 3 * 24 * time.Hour
	seasonTTL      = 12 * time.Hour
	collectionTTL  = 7 * 24 * time.Hour
)

// live reports a title whose data still changes: a show on the air, or a
// title not out yet (or whose status isn't known).
func live(m *Media) bool {
	return m == nil || m.Status != "FINISHED" && m.Status != "CANCELLED"
}

func detailTTL(m *Media) time.Duration {
	if live(m) {
		return detailLiveTTL
	}
	return detailFinalTTL
}

// errInvalidID is the error for a media id that's neither a movie's nor a
// show's.
var errInvalidID = errors.New("invalid media id")

// HasDetails reports a title whose full page data is saved (and fresh).
func (p *Platform) HasDetails(id int) bool {
	var m Media
	return p.db.GetCache(detailKey(id), &m)
}

// Media returns a title's full page: description, cast, trailer,
// recommendations, its collection's other movies...
func (p *Platform) Media(ctx context.Context, id int, refresh bool) (*Media, error) {
	if id <= 0 {
		return nil, errInvalidID
	}
	var m Media
	if !refresh && p.db.GetCache(detailKey(id), &m) {
		p.attachListEntry(&m)
		return &m, nil
	}
	fresh, err := p.fetchMedia(ctx, id)
	if err != nil {
		if p.db.GetStaleCache(detailKey(id), &m) {
			p.attachListEntry(&m)
			return &m, nil
		}
		return nil, err
	}
	p.attachListEntry(fresh)
	return fresh, nil
}

// fetchMedia gets a title's page from TMDB and saves it: in the cache, and
// with its list entry, if it has one.
func (p *Platform) fetchMedia(ctx context.Context, id int) (*Media, error) {
	kind := KindOf(id)
	it, err := p.client.details(ctx, kind, TMDBID(id))
	if err != nil {
		return nil, err
	}
	// With the genre names: the recommendations only have genre ids.
	m := p.mapper(ctx, true).media(it, kind)
	if m == nil {
		return nil, ErrNotFound
	}
	if m.BelongsTo != nil {
		m.Relations = p.relations(ctx, m)
	}
	p.db.SetCache(detailKey(id), m, detailTTL(m))
	p.db.SetCache(liteKey(id), m.Lite(), liteTTL)
	p.saveListMedia(m.Lite())
	return m, nil
}

// relations lists the other movies of a movie's collection: the ones
// released before it are prequels, after it sequels, and those without a
// date (or with the same) "other".
func (p *Platform) relations(ctx context.Context, m *Media) *struct {
	Edges []RelationEdge `json:"edges"`
} {
	key := "tmdb-collection:" + strconv.Itoa(m.BelongsTo.ID)
	var col tmdbCollection
	if !p.db.GetCache(key, &col) {
		got, err := p.client.collection(ctx, m.BelongsTo.ID)
		if err != nil {
			if !p.db.GetStaleCache(key, &col) {
				return nil
			}
		} else {
			col = *got
			p.db.SetCache(key, col, collectionTTL)
		}
	}
	mp := p.mapper(ctx, true)
	out := &struct {
		Edges []RelationEdge `json:"edges"`
	}{Edges: []RelationEdge{}}
	mine := formatDate(m.StartDate)
	for i := range col.Parts {
		part := mp.media(&col.Parts[i], TypeMovie)
		if part == nil || part.ID == m.ID {
			continue
		}
		rel := "OTHER"
		if theirs := formatDate(part.StartDate); len(mine) == 10 && len(theirs) == 10 {
			switch {
			case theirs < mine:
				rel = "PREQUEL"
			case theirs > mine:
				rel = "SEQUEL"
			}
		}
		out.Edges = append(out.Edges, RelationEdge{RelationType: rel, Node: part.Lite()})
	}
	return out
}

// attachListEntry sets the media's list entry (nil when it isn't in the
// list), and how long until its next episode airs.
func (p *Platform) attachListEntry(m *Media) {
	m.MediaListEntry = nil
	if e := p.listEntry(m.ID); e != nil {
		m.MediaListEntry = &ListEntryLite{ID: e.ID, Status: e.Status, Progress: e.Progress, Score: e.Score, Repeat: e.Repeat}
	}
	untilAiring(m, time.Now())
}

// untilAiring recomputes the time until the next episode airs, which a
// saved copy has as it was when saved.
func untilAiring(m *Media, now time.Time) {
	if n := m.NextAiringEpisode; n != nil && n.AiringAt > 0 {
		c := *n
		c.TimeUntilAiring = c.AiringAt - int(now.Unix())
		m.NextAiringEpisode = &c
	}
}

// MediaLite returns a title without its page details, looking in the list
// and the cache before asking TMDB.
func (p *Platform) MediaLite(ctx context.Context, id int) (*Media, error) {
	if id <= 0 {
		return nil, errInvalidID
	}
	if e := p.listEntry(id); e != nil && e.Media != nil {
		return e.Media, nil
	}
	var m Media
	if p.db.GetCache(liteKey(id), &m) {
		return &m, nil
	}
	full, err := p.Media(ctx, id, false)
	if err != nil {
		if p.db.GetStaleCache(liteKey(id), &m) {
			return &m, nil
		}
		return nil, err
	}
	return full.Lite(), nil
}

// cachedLite is a title as the list or the cache have it, however old,
// without asking TMDB; nil when they don't.
func (p *Platform) cachedLite(id int) *Media {
	if e := p.listEntry(id); e != nil && e.Media != nil {
		return e.Media
	}
	var m Media
	if p.db.GetStaleCache(liteKey(id), &m) {
		return &m
	}
	return nil
}

// batchWorkers is how many titles MediaBatch fetches at once (TMDB has no
// batch lookup).
const batchWorkers = 4

// MediaBatch returns several titles (lite). The ones TMDB couldn't give are
// missing from the result, and the first error is returned with it.
func (p *Platform) MediaBatch(ctx context.Context, ids []int) (map[int]*Media, error) {
	out := map[int]*Media{}
	var missing []int
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if e := p.listEntry(id); e != nil && e.Media != nil {
			out[id] = e.Media
			continue
		}
		var m Media
		if p.db.GetCache(liteKey(id), &m) {
			out[id] = &m
		} else {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		next     = make(chan int)
	)
	for range min(batchWorkers, len(missing)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range next {
				m, err := p.MediaLite(ctx, id)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					out[id] = m
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range missing {
		next <- id
	}
	close(next)
	wg.Wait()
	return out, firstErr
}

// ---------------------------------------------------------------------------
// Seasons

func seasonKey(id, season int) string { return fmt.Sprintf("season:%d:%d", id, season) }

// Season returns a season of a show with its episodes (titles, synopses,
// stills, air dates). Season 0 holds the specials.
func (p *Platform) Season(ctx context.Context, id, season int) (*SeasonDetail, error) {
	if !IsShow(id) || season < 0 {
		return nil, errors.New("not a season of a show")
	}
	var s SeasonDetail
	key := seasonKey(id, season)
	if p.db.GetCache(key, &s) {
		return &s, nil
	}
	got, err := p.client.season(ctx, TMDBID(id), season)
	if err != nil {
		if p.db.GetStaleCache(key, &s) {
			return &s, nil
		}
		return nil, err
	}
	out := p.mapper(ctx, false).seasonDetail(id, got)
	p.db.SetCache(key, out, seasonTTL)
	return out, nil
}

// ---------------------------------------------------------------------------

// ResetCache forgets the TMDB data saved, but the list's own copies: titles
// are then fetched again, e.g. in another language. The list's copies are
// kept (the list shows at once, also offline) but due again, finished
// titles too, which would otherwise wait a month.
func (p *Platform) ResetCache() {
	for _, prefix := range []string{"media:", "media-lite:", "season:", "search:", "schedule:", "discover", "tmdb-"} {
		p.db.DeleteCachePrefix(prefix)
	}
	p.client.forgetGenres()
	if _, err := p.db.Write(`UPDATE local_list SET media_updated = 0`); err == nil {
		p.invalidate()
	}
}
