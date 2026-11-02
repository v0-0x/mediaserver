package letterboxd

import (
	"context"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// What Letterboxd says about a film becomes one update of its list entry.
// The rules keep the list from going backwards, and keep what was done in
// Moviko since:
//
//   - A viewing (a diary entry not imported before) makes the entry
//     COMPLETED: movies always, unless Moviko has the movie as Watching or
//     Rewatching since a later day than the viewing's; shows only when
//     they aren't Watching or Rewatching in Moviko (episodes are tracked
//     here). Being marked watched without a viewing (watched.csv, a rating,
//     a like, a review without a date) never changes Watching or
//     Rewatching either. A COMPLETED entry is never moved to another status.
//   - A rewatch of an entry that was already COMPLETED counts one more
//     rewatch (Repeat). CompletedAt moves to the viewing's date when it's
//     later. Progress only moves forward (1 for a watched movie, the last
//     aired episode for a watched show).
//   - A Letterboxd rating replaces the Moviko score unless the score is
//     newer: an entry that has a score and was updated in Moviko after the
//     rating was given (the feed's pubDate, or the end of the day of the
//     export's Date column) keeps its score. Same day: Letterboxd wins.
//   - Likes only ever add (a film liked on Letterboxd is liked here; unliking
//     isn't carried over). The watchlist only adds films that aren't on the
//     list at all. A review fills the entry's notes only when they're empty.

const (
	statusCompleted = "COMPLETED"
	statusPlanning  = "PLANNING"
	statusCurrent   = "CURRENT"
	statusRepeating = "REPEATING"
)

// listState is what the list says about a film, as far as the sync cares.
type listState struct {
	exists      bool
	status      string
	progress    int
	score       float64
	scoreAt     int64 // when the score was set (unix seconds), as far as known
	updatedAt   int64 // when the entry was last changed in Moviko
	repeat      int
	liked       bool
	notes       string
	completedAt string // YYYY-MM-DD (00 for unknown parts) or ""
	uri         string
}

func stateOf(e *catalog.ListEntry) listState {
	if e == nil {
		return listState{}
	}
	updated := e.UpdatedAt
	if updated > 1e12 { // milliseconds
		updated /= 1000
	}
	return listState{
		exists:      true,
		status:      e.Status,
		progress:    e.Progress,
		score:       e.Score,
		scoreAt:     updated,
		updatedAt:   updated,
		repeat:      e.Repeat,
		liked:       e.Liked,
		notes:       e.Notes,
		completedAt: fuzzyString(e.CompletedAt),
		uri:         e.LetterboxdURI,
	}
}

// change collects what Letterboxd says about one film.
type change struct {
	mediaID int
	show    bool
	before  listState
	after   listState
	// uri is the film's Letterboxd page (or a viewing's when that's all
	// there is).
	uri string
	// diary counts the diary entries added.
	diary int
}

func newChange(mediaID int, e *catalog.ListEntry) *change {
	st := stateOf(e)
	return &change{mediaID: mediaID, show: catalog.IsShow(mediaID), before: st, after: st}
}

// watched records that the film was seen: on date (a viewing; "" when it's
// only known to be watched), as a rewatch or not.
func (c *change) watched(date string, rewatch bool) {
	a := &c.after
	if a.exists && (a.status == statusCurrent || a.status == statusRepeating) {
		if c.show || date == "" || date < localDay(a.updatedAt) {
			return
		}
	}
	wasCompleted := a.exists && a.status == statusCompleted
	a.exists = true
	a.status = statusCompleted
	if !c.show && a.progress < catalog.MovieEpisode {
		a.progress = catalog.MovieEpisode
	}
	if date != "" {
		if rewatch && wasCompleted {
			a.repeat++
		}
		if date > a.completedAt {
			a.completedAt = date
		}
	}
}

// rate records a Letterboxd rating (0-100) given at the time at.
func (c *change) rate(score int, at int64) {
	if score <= 0 {
		return
	}
	a := &c.after
	if a.score > 0 && a.scoreAt > at {
		return // rated in Moviko since
	}
	a.score = float64(score)
	a.scoreAt = at
}

func (c *change) like() { c.after.liked = true }

// watchlist puts the film on the watchlist if it isn't on the list.
func (c *change) watchlist() {
	if !c.after.exists {
		c.after.exists = true
		c.after.status = statusPlanning
	}
}

// note fills the entry's empty notes with a review.
func (c *change) note(review string) {
	if review = strings.TrimSpace(review); review != "" && strings.TrimSpace(c.after.notes) == "" {
		c.after.notes = review
	}
}

func (c *change) setURI(uri string) {
	if uri != "" {
		c.uri = uri
	}
}

// update is the list update that brings the entry from before to after.
func (c *change) update() (catalog.EntryUpdate, bool) {
	u := catalog.EntryUpdate{MediaID: c.mediaID}
	b, a := c.before, c.after
	if !a.exists {
		return u, false
	}
	changed := false
	if !b.exists || a.status != b.status {
		s := a.status
		u.Status = &s
		changed = true
	}
	if a.progress != b.progress {
		p := a.progress
		u.Progress = &p
		changed = true
	}
	if a.score != b.score {
		s := a.score
		u.Score = &s
		changed = true
	}
	if a.repeat != b.repeat {
		r := a.repeat
		u.Repeat = &r
		changed = true
	}
	if a.liked != b.liked {
		l := a.liked
		u.Liked = &l
		changed = true
	}
	if a.notes != b.notes {
		n := a.notes
		u.Notes = &n
		changed = true
	}
	if a.completedAt != b.completedAt {
		if d := fuzzyDate(a.completedAt); d != nil {
			u.CompletedAt = d
			changed = true
		}
	}
	return u, changed
}

// localDay is the day (YYYY-MM-DD, local time) of a unix time, "" for 0.
func localDay(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("2006-01-02")
}

// changes is the set of films one sync touches, in the order met.
type changes struct {
	entries map[int]*catalog.ListEntry
	byID    map[int]*change
	order   []int
}

func (cs *changes) get(mediaID int) *change {
	if c := cs.byID[mediaID]; c != nil {
		return c
	}
	c := newChange(mediaID, cs.entries[mediaID])
	cs.byID[mediaID] = c
	cs.order = append(cs.order, mediaID)
	return c
}

// newChanges starts a sync from the current list.
func (s *Sync) newChanges(ctx context.Context) (*changes, error) {
	coll, err := s.Catalog.Collection(ctx, "", false)
	if err != nil {
		return nil, err
	}
	cs := &changes{entries: map[int]*catalog.ListEntry{}, byID: map[int]*change{}}
	if coll != nil {
		for _, l := range coll.Lists {
			if l == nil || l.IsCustomList {
				continue
			}
			for _, e := range l.Entries {
				if e != nil && cs.entries[e.MediaID] == nil {
					cs.entries[e.MediaID] = e
				}
			}
		}
	}
	return cs, nil
}

// viewing is a diary entry from Letterboxd.
type viewing struct {
	date    string // YYYY-MM-DD
	rewatch bool
	score   int // 0-100
	liked   bool
	review  string
	tags    string
	lbID    string // catalog.DiaryEntry.LetterboxdID
	at      int64  // when it was logged (unix seconds)
}

// Diary limits: the most entries read for one film, and for all of them.
const (
	diaryLimit    = 1000
	allDiaryLimit = 1_000_000
)

// addViewing adds v to the diary of c's film and applies it to the entry,
// unless the diary already has it: imported before (same LetterboxdID), or a
// viewing the same day, e.g. logged in Moviko and exported, or read from
// both the feed and the export. A Moviko entry found that way is marked as
// exported, since Letterboxd has it now.
func (s *Sync) addViewing(ctx context.Context, c *change, v viewing, now int64) (bool, error) {
	existing, err := s.Catalog.Diary(ctx, c.mediaID, diaryLimit)
	if err != nil {
		return false, err
	}
	for _, e := range existing {
		if e != nil && v.lbID != "" && e.LetterboxdID == v.lbID {
			return false, nil
		}
	}
	for _, e := range existing {
		if e == nil || v.date == "" || e.WatchedDate != v.date {
			continue
		}
		if e.Source == SourceMoviko && e.ExportedAt == 0 {
			if err := s.Catalog.MarkDiaryExported(ctx, []int64{e.ID}, now); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	_, added, err := s.Catalog.AddDiary(ctx, catalog.DiaryEntry{
		MediaID:      c.mediaID,
		WatchedDate:  v.date,
		Rating:       v.score,
		Rewatch:      v.rewatch,
		Liked:        v.liked,
		Review:       v.review,
		Tags:         v.tags,
		Source:       SourceLetterboxd,
		LetterboxdID: v.lbID,
		CreatedAt:    now,
	})
	if err != nil || !added {
		return false, err
	}
	c.diary++
	c.watched(v.date, v.rewatch)
	c.rate(v.score, v.at)
	if v.liked {
		c.like()
	}
	return true, nil
}

// commit saves c to the list and counts it in res.
func (s *Sync) commit(ctx context.Context, c *change, res *Result, now int64) error {
	if c.show && c.after.status == statusCompleted && c.before.status != statusCompleted {
		// A watched show has seen its last aired episode.
		if m, err := s.Catalog.MediaLite(ctx, c.mediaID); err == nil && m != nil {
			p := m.LatestAired()
			if p == 0 {
				p = m.LastEpisode()
			}
			if p > c.after.progress {
				c.after.progress = p
			}
		}
	}
	u, changed := c.update()
	if changed {
		if err := s.Catalog.UpdateEntry(ctx, u); err != nil {
			return err
		}
	}
	if c.after.exists && c.uri != "" && (changed || c.diary > 0 || c.before.uri != c.uri) {
		if err := s.Catalog.SetLetterboxdState(ctx, c.mediaID, c.uri, now); err != nil {
			return err
		}
	}
	switch {
	case !c.before.exists && c.after.exists:
		res.Added++
	case changed || c.diary > 0:
		res.Updated++
	default:
		res.Skipped++
	}
	return nil
}

// commitAll commits every change, in the order met.
func (s *Sync) commitAll(ctx context.Context, cs *changes, res *Result, now int64) error {
	for _, id := range cs.order {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.commit(ctx, cs.byID[id], res, now); err != nil {
			return err
		}
	}
	return nil
}
