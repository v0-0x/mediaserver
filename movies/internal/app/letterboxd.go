package app

import (
	"context"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/letterboxd"
)

// Letterboxd: the sync service, and the diary entries of the movies watched
// in Moviko. A movie is logged when it becomes Watched (COMPLETED) here:
// played to the end (catalog.Platform.OnCompleted), marked watched on its
// page, or put on the Watched list by hand (UpdateEntry). Shows aren't
// logged: Letterboxd is about films. Finishing a movie on another Moviko
// on the same account (its watch history moves this list on) isn't logged
// either: that Moviko logs it.

// diaryLog is the state of logging viewings.
type diaryLog struct {
	// mu: one viewing logged at a time, so that a movie finished two ways
	// at once (played to the end while marked watched) is logged once.
	mu sync.Mutex
	// elsewhere holds the movies another Moviko finished, until their
	// completion comes (or a minute passes).
	elsewhereMu sync.Mutex
	elsewhere   map[int]time.Time
}

// elsewhereFor is how long a completion is put down to another Moviko.
const elsewhereFor = time.Minute

// wireLetterboxd sets up the Letterboxd sync (started with the app).
func (a *App) wireLetterboxd() {
	a.Letterboxd = &letterboxd.Sync{
		Catalog:  a.Platform,
		Settings: a.Settings,
		DB:       a.DB,
		Hub:      a.Hub,
		NotFound: catalog.ErrNotFound,
	}
	a.Platform.OnCompleted = a.completedHere
}

// completedHere is told that playing (or the watch history) made a title
// COMPLETED.
func (a *App) completedHere(mediaID int) {
	if a.finishedElsewhere(mediaID) {
		return
	}
	a.logWatched(a.ctx, mediaID)
}

// logWatched logs a viewing of a movie today (once a day, and only when
// Settings.Letterboxd.LogWatched is on).
func (a *App) logWatched(ctx context.Context, mediaID int) {
	if !catalog.IsMovie(mediaID) || a.Letterboxd == nil {
		return
	}
	a.diary.mu.Lock()
	defer a.diary.mu.Unlock()
	if _, err := a.Letterboxd.LoggedWatched(ctx, mediaID); err != nil && ctx.Err() == nil {
		log.Printf("letterboxd: logging %d in the diary: %v", mediaID, err)
	}
}

// UpdateEntry changes a list entry for the user (a title's page, a plugin):
// a movie that becomes Watched is logged in the diary, as one played to the
// end is, and one rated (or liked) once logged gets the rating in its last
// viewing still to send. Use it instead of Platform.UpdateEntry for changes
// the user made.
func (a *App) UpdateEntry(ctx context.Context, u catalog.EntryUpdate) error {
	var cur *catalog.ListEntry
	if u.Status != nil || u.Notes != nil {
		if coll, err := a.Platform.Collection(ctx, "", false); err == nil && coll != nil {
			cur = coll.Find(u.MediaID)
		}
	}
	// Watched already: an edit of the entry.
	completes := u.Status != nil && *u.Status == "COMPLETED" && catalog.IsMovie(u.MediaID) && (cur == nil || cur.Status != "COMPLETED")
	catalog.FillDates(&u, cur, time.Now())
	if err := a.Platform.UpdateEntry(ctx, u); err != nil {
		return err
	}
	switch {
	case completes:
		a.logWatched(ctx, u.MediaID)
	case catalog.IsMovie(u.MediaID) && (u.Score != nil || u.Liked != nil):
		a.rateLogged(ctx, u.MediaID)
	}
	// Only a review that changed: the list editor sends the notes back
	// with every edit, and notes imported from Letterboxd mustn't go back
	// there as a new review.
	if u.Notes != nil && catalog.IsMovie(u.MediaID) && (cur == nil || strings.TrimSpace(cur.Notes) != strings.TrimSpace(*u.Notes)) {
		a.reviewLogged(ctx, u.MediaID, *u.Notes)
	}
	return nil
}

// reviewLogged gives the latest viewing of a movie, when it was logged here
// and isn't on Letterboxd yet, the review the user just wrote, so that it
// goes to Letterboxd with it. Only reviews written in Moviko get there: a
// review that came from Letterboxd isn't copied into new viewings.
func (a *App) reviewLogged(ctx context.Context, mediaID int, review string) {
	a.diary.mu.Lock()
	defer a.diary.mu.Unlock()
	latest, err := a.Platform.Diary(ctx, mediaID, 1)
	if err != nil || len(latest) == 0 {
		return
	}
	d := latest[0]
	if d.Source != catalog.DiaryMoviko || d.ExportedAt != 0 || d.Review == strings.TrimSpace(review) {
		return
	}
	if err := a.Platform.ReviewDiary(ctx, d.ID, review); err != nil {
		log.Printf("letterboxd: reviewing diary entry %d: %v", d.ID, err)
	}
}

// rateLogged gives the latest viewing of a movie, when it was logged here
// and isn't on Letterboxd yet, the movie's rating and like: a movie is often
// rated after it's watched (and logged).
func (a *App) rateLogged(ctx context.Context, mediaID int) {
	coll, err := a.Platform.Collection(ctx, "", false)
	if err != nil || coll == nil {
		return
	}
	e := coll.Find(mediaID)
	if e == nil {
		return
	}
	a.diary.mu.Lock()
	defer a.diary.mu.Unlock()
	latest, err := a.Platform.Diary(ctx, mediaID, 1)
	if err != nil || len(latest) == 0 {
		return
	}
	d, rating := latest[0], int(math.Round(e.Score))
	if d.Source != catalog.DiaryMoviko || d.ExportedAt != 0 || (d.Rating == rating && d.Liked == e.Liked) {
		return
	}
	if err := a.Platform.RateDiary(ctx, d.ID, rating, e.Liked); err != nil {
		log.Printf("letterboxd: rating diary entry %d: %v", d.ID, err)
	}
}

// progressElsewhere moves the list on for an episode (or a movie) another
// Moviko on the same account finished: its completion isn't logged here.
func (a *App) progressElsewhere(ctx context.Context, mediaID, episode int) {
	a.watchedElsewhere(mediaID)
	changed, err := a.Platform.UpdateProgress(ctx, mediaID, episode)
	if err == nil && changed {
		if coll, err := a.Platform.Collection(ctx, "", false); err == nil && coll != nil {
			if e := coll.Find(mediaID); e != nil && e.Status == "COMPLETED" {
				return // completedHere takes the mark
			}
		}
	}
	a.finishedElsewhere(mediaID) // not completed: no mark left behind
}

// watchedElsewhere notes that another Moviko finished a title, before its
// progress is applied here.
func (a *App) watchedElsewhere(mediaID int) {
	a.diary.elsewhereMu.Lock()
	defer a.diary.elsewhereMu.Unlock()
	if a.diary.elsewhere == nil {
		a.diary.elsewhere = map[int]time.Time{}
	}
	now := time.Now()
	for id, at := range a.diary.elsewhere {
		if now.Sub(at) > elsewhereFor {
			delete(a.diary.elsewhere, id)
		}
	}
	a.diary.elsewhere[mediaID] = now
}

// finishedElsewhere reports (once) whether a completion of mediaID came from
// another Moviko's history.
func (a *App) finishedElsewhere(mediaID int) bool {
	a.diary.elsewhereMu.Lock()
	defer a.diary.elsewhereMu.Unlock()
	at, ok := a.diary.elsewhere[mediaID]
	if !ok {
		return false
	}
	delete(a.diary.elsewhere, mediaID)
	return time.Since(at) <= elsewhereFor
}
