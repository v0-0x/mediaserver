package catalog

import (
	"context"
	"time"
)

// What watching does to the list: playing a title puts it on the watching
// list (StartWatching), finishing an episode or a movie moves its progress
// on and, at the end, marks it watched (UpdateProgress).

// UpdateProgress records that the user finished an episode (by its key, or
// MovieEpisode for a movie); see progressUpdate for the rules. It reports
// whether the list was changed. When the entry ends up COMPLETED,
// OnCompleted is told.
func (p *Platform) UpdateProgress(ctx context.Context, mediaID, episodeKey int) (bool, error) {
	media, err := p.MediaLite(ctx, mediaID)
	if err != nil {
		return false, err
	}
	p.listMu.Lock()
	entry, err := p.entry(mediaID)
	if err != nil {
		p.listMu.Unlock()
		return false, err
	}
	u, ok := progressUpdate(entry, media, episodeKey, time.Now())
	if ok {
		err = p.saveEntry(media, u)
	}
	p.listMu.Unlock()
	if !ok || err != nil {
		return false, err
	}
	if u.Status != nil && *u.Status == "COMPLETED" {
		if fn := p.OnCompleted; fn != nil {
			go fn(mediaID)
		}
	}
	return true, nil
}

// Finishable reports whether watching up to the last known episode of m
// means it's watched to the end: always for a movie, only when it's over
// for a show (a show on the air gets more episodes).
func (m *Media) Finishable() bool {
	if !m.IsShow() {
		return true
	}
	return (m.Status == "FINISHED" || m.Status == "CANCELLED") && m.LastEpisode() > 0
}

// progressUpdate works out the list update for having finished the episode
// key of media, given its list entry (nil when it isn't in the list). ok is
// false when nothing needs to change.
//
// Progress (the key of the last episode watched) only moves forward:
// finishing an episode at or before it changes nothing, except that a
// planned title becomes "watching" (keeping its progress). Specials (season
// 0) never count. The exception is watching a finished show again, which
// starts a rewatch from that episode.
//
//	not in list, PLANNING, PAUSED, DROPPED -> CURRENT
//	COMPLETED, an earlier episode          -> REPEATING (a show that's over)
//	COMPLETED, a new episode               -> CURRENT (a new season)
//	reaching the last episode              -> COMPLETED (a rewatch: repeat+1)
//
// A movie is finished by watching it: once more when it's watched already
// (a rewatch: repeat+1). The last episode of a show only finishes it when
// the show is over (ended or cancelled). The start and finish dates are
// filled in when they are still empty.
func progressUpdate(entry *ListEntry, media *Media, key int, now time.Time) (u EntryUpdate, ok bool) {
	u.MediaID = media.ID
	if key <= 0 {
		return u, false
	}
	show := media.IsShow()
	if show {
		if season, _ := SplitKey(key); season == 0 {
			return u, false // a special
		}
	} else {
		key = MovieEpisode
	}
	finishable := media.Finishable()
	last := MovieEpisode
	if show {
		last = media.LastEpisode()
	}
	finishes := func(progress int) bool { return finishable && progress >= last }

	status, progress := "CURRENT", key
	rewatch := entry != nil && entry.Status == "REPEATING"
	// A movie has a single "episode": watching it always finishes it, also
	// when its progress says it was watched already.
	if entry != nil {
		switch entry.Status {
		case "COMPLETED":
			switch {
			case !show:
				rewatch = true // the movie, once more
			case key > entry.Progress:
				// An episode after the ones watched: a new season.
			case finishable && !finishes(key):
				status = "REPEATING"
			default:
				return u, false // watched already
			}
		case "REPEATING":
			if key <= entry.Progress && show {
				return u, false
			}
			status = "REPEATING"
		case "PLANNING":
			progress = max(key, entry.Progress)
		default: // CURRENT, PAUSED, DROPPED
			if key <= entry.Progress && show {
				return u, false
			}
		}
	}
	today := fuzzyDate(now)
	if needsStartDate(entry) {
		u.StartedAt = today
	}
	if finishes(progress) {
		if rewatch {
			r := entry.Repeat + 1
			u.Repeat = &r
		}
		status = "COMPLETED"
		if entry == nil || entry.CompletedAt.Year == nil {
			u.CompletedAt = today
		}
	}
	u.Status, u.Progress = &status, &progress
	return u, true
}

// StartWatching records that the user started playing a title; see
// watchingUpdate for the rules. It reports whether the list was changed.
func (p *Platform) StartWatching(ctx context.Context, mediaID int) (bool, error) {
	if e := p.listEntry(mediaID); e != nil {
		if _, ok := watchingUpdate(e, mediaID, time.Now()); !ok {
			return false, nil
		}
	}
	media, err := p.MediaLite(ctx, mediaID)
	if err != nil {
		return false, err
	}
	p.listMu.Lock()
	defer p.listMu.Unlock()
	entry, err := p.entry(mediaID)
	if err != nil {
		return false, err
	}
	u, ok := watchingUpdate(entry, mediaID, time.Now())
	if !ok {
		return false, nil
	}
	if err := p.saveEntry(media, u); err != nil {
		return false, err
	}
	return true, nil
}

// watchingUpdate works out the list update for having started to play a
// title, given its list entry (nil when it isn't in the list). ok is false
// when nothing needs to change. Only the status (and start date) changes,
// the progress is kept:
//
//	not in list, PLANNING, PAUSED, DROPPED -> CURRENT
//	CURRENT, REPEATING, COMPLETED          -> unchanged
//
// A watched title is left as it is: finishing it (or one of its episodes)
// again starts a rewatch (see progressUpdate). The start date is filled in
// like there.
func watchingUpdate(entry *ListEntry, mediaID int, now time.Time) (u EntryUpdate, ok bool) {
	u.MediaID = mediaID
	if entry != nil {
		switch entry.Status {
		case "CURRENT", "REPEATING", "COMPLETED":
			return u, false
		}
	}
	status := "CURRENT"
	u.Status = &status
	if needsStartDate(entry) {
		u.StartedAt = fuzzyDate(now)
	}
	return u, true
}

// FillDates gives a change the user makes to the list (marking a title
// watched, putting it on Watching or Watched) the dates watching would give
// it (progressUpdate): today as the start date of a title started now, and
// as the finish date of one finished now, when the change sets none and
// the entry (nil when it isn't in the list) has none yet.
func FillDates(u *EntryUpdate, entry *ListEntry, now time.Time) {
	if u.Status == nil || (entry != nil && entry.Status == *u.Status) {
		return
	}
	switch *u.Status {
	case "CURRENT", "COMPLETED":
		if u.StartedAt == nil && needsStartDate(entry) {
			u.StartedAt = fuzzyDate(now)
		}
	}
	if *u.Status == "COMPLETED" && u.CompletedAt == nil && (entry == nil || entry.CompletedAt.Year == nil) {
		u.CompletedAt = fuzzyDate(now)
	}
}

// needsStartDate reports whether a title the user starts watching now gets
// today as its start date: it has none, and wasn't being watched already
// (progress without a start date means it started on an unknown day).
func needsStartDate(entry *ListEntry) bool {
	return entry == nil || (entry.StartedAt.Year == nil && (entry.Status == "PLANNING" || entry.Progress == 0))
}

// fuzzyDate returns the day of t.
func fuzzyDate(t time.Time) *FuzzyDate {
	y, m, d := t.Date()
	month := int(m)
	return &FuzzyDate{Year: &y, Month: &month, Day: &d}
}
