package letterboxd

import (
	"context"
	"errors"
	"sort"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// Pull reads the connected member's diary feed and brings its new entries
// into KumoBox: each becomes a diary entry (Source "letterboxd", LetterboxdID
// the feed's guid, so it's only added once) and updates the film's list
// entry (see apply.go for the rules). The feed only has the latest 50
// entries; older ones come with Import.
func (s *Sync) Pull(ctx context.Context) (*Result, error) {
	username := s.settings().Username
	if username == "" {
		return nil, ErrNotConnected
	}
	release, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	feed, err := FetchFeed(ctx, s.httpClient(), s.baseURL(), username)
	var res *Result
	displayName := ""
	if err == nil {
		displayName = feed.DisplayName
		s.refreshViewer(username, feed.DisplayName)
		res, err = s.pullFeed(ctx, feed)
	}
	s.record(err, displayName)
	release()
	s.changed(res)
	s.publishStatus()
	return res, err
}

// refreshViewer keeps the catalog's viewer in step with the member's name.
func (s *Sync) refreshViewer(username, displayName string) {
	v := s.Catalog.Viewer()
	switch {
	case v == nil || v.ID != ViewerID(username):
		_ = s.Catalog.SetViewer(viewer(username, displayName, s.apiAvatar(username)))
	case displayName != "" && v.Name != displayName:
		c := *v
		c.Name = displayName
		_ = s.Catalog.SetViewer(&c)
	}
}

// pullFeed applies the feed's entries, oldest first.
func (s *Sync) pullFeed(ctx context.Context, feed *Feed) (*Result, error) {
	res := &Result{Unmatched: []string{}}
	items := append([]DiaryItem(nil), feed.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		di, dj := itemDate(items[i]), itemDate(items[j])
		if di != dj {
			return di < dj
		}
		return items[i].Published.Before(items[j].Published)
	})
	cs, err := s.newChanges(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock().Unix()
	unmatched := map[string]bool{}
	var lookupErr error
	matched := 0
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		id, err := s.resolveItem(ctx, it)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			lookupErr = err
		}
		if id == 0 {
			unmatched[filmLabel(it.FilmTitle, it.FilmYear)] = true
			continue
		}
		matched++
		c := cs.get(id)
		c.setURI(FilmURL(it.Link))
		at := it.Published.Unix()
		if it.Published.IsZero() {
			at = endOfDay(it.WatchedDate)
		}
		if it.WatchedDate == "" {
			// A review that isn't in the diary: there's no viewing to log,
			// and nothing to tell it apart on the next read, so it only
			// counts for a film that isn't on the list yet.
			if !c.before.exists {
				c.watched("", false)
				c.rate(StarsToScore(it.Rating), at)
				c.note(it.Review)
				if it.Liked {
					c.like()
				}
			}
			continue
		}
		if _, err := s.addViewing(ctx, c, viewing{
			date:    it.WatchedDate,
			rewatch: it.Rewatch,
			score:   StarsToScore(it.Rating),
			liked:   it.Liked,
			review:  it.Review,
			lbID:    it.GUID,
			at:      at,
		}, now); err != nil {
			return res, err
		}
	}
	if err := s.commitAll(ctx, cs, res, now); err != nil {
		return res, err
	}
	for _, c := range cs.byID {
		res.Diary += c.diary
	}
	res.Unmatched = sortedUnique(unmatched)
	if matched == 0 && lookupErr != nil {
		return res, lookupErr
	}
	return res, nil
}

func itemDate(it DiaryItem) string {
	if it.WatchedDate != "" {
		return it.WatchedDate
	}
	if !it.Published.IsZero() {
		return it.Published.Format("2006-01-02")
	}
	return ""
}

// resolveItem is the media id of a feed entry: from its TMDB id, or found by
// title and year. 0 when there's no match.
func (s *Sync) resolveItem(ctx context.Context, it DiaryItem) (int, error) {
	switch {
	case it.TMDBMovieID > 0:
		return catalog.MovieID(it.TMDBMovieID), nil
	case it.TMDBTVID > 0:
		return catalog.ShowID(it.TMDBTVID), nil
	}
	return s.findMovie(ctx, it.FilmTitle, it.FilmYear)
}

// findMovie looks a film up by title and year: its media id, 0 when there's
// no match. The error is set when the lookup failed for another reason than
// finding nothing.
func (s *Sync) findMovie(ctx context.Context, title string, year int) (int, error) {
	if title == "" {
		return 0, nil
	}
	m, err := s.Catalog.FindMovie(ctx, title, year)
	if err != nil {
		if s.NotFound != nil && errors.Is(err, s.NotFound) {
			return 0, nil
		}
		return 0, err
	}
	if m == nil {
		return 0, nil
	}
	if m.ID != 0 {
		return m.ID, nil
	}
	return catalog.MovieID(m.TMDBID), nil
}
