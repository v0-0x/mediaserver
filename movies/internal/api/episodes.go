package api

import (
	"net/http"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// markEpisode marks one episode (by its key; a movie's is 1) watched or
// unwatched. The list only stores how far you got, so "watched" raises the
// progress to that episode (it never lowers it) and "unwatched" lowers it
// to the episode before (it never raises it). The current progress is read
// here rather than taken from the page, which may be out of date. Specials
// (season 0) don't count.
func (s *Server) markEpisode(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var body struct {
		Episode int  `json:"episode"`
		Watched bool `json:"watched"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.Episode <= 0 {
		return nil, badRequest("invalid episode")
	}
	media, err := s.app.Platform.MediaLite(r.Context(), id)
	if err != nil {
		return nil, err
	}
	coll, err := s.app.Platform.Collection(r.Context(), "", false)
	if err != nil {
		return nil, err
	}
	var cur catalog.ListEntry
	if e := coll.Find(id); e != nil {
		cur = *e
	}
	if season, _ := catalog.SplitKey(body.Episode); media.IsShow() && season == 0 {
		return map[string]any{"changed": false, "progress": cur.Progress, "status": cur.Status}, nil
	}
	if !media.IsShow() {
		body.Episode = catalog.MovieEpisode
	}
	next, status, repeat := markedProgress(cur.Status, cur.Progress, cur.Repeat, media, body.Episode, body.Watched)
	if next == cur.Progress && status == cur.Status {
		return map[string]any{"changed": false, "progress": cur.Progress, "status": cur.Status}, nil
	}
	u := catalog.EntryUpdate{MediaID: id, Status: &status, Progress: &next}
	if repeat != cur.Repeat {
		u.Repeat = &repeat
	}
	// Watched to the end today, as when it's played to the end: the finish
	// date is filled in when it's still empty.
	if status == "COMPLETED" && cur.Status != "COMPLETED" && cur.CompletedAt.Year == nil {
		y, m, d := time.Now().Date()
		month := int(m)
		u.CompletedAt = &catalog.FuzzyDate{Year: &y, Month: &month, Day: &d}
	}
	// Through the app: a movie marked watched is logged in the diary.
	if err := s.app.UpdateEntry(r.Context(), u); err != nil {
		return nil, err
	}
	return map[string]any{"changed": true, "progress": next, "status": status}, nil
}

// markedProgress works out the list entry after marking the episode key of
// media as watched or unwatched. status is "" when the title isn't in the
// list. Reaching the last episode completes a movie, or a show that's over
// (one still on the air never is).
func markedProgress(status string, progress, repeat int, media *catalog.Media, key int, watched bool) (int, string, int) {
	next := min(progress, media.PrevEpisode(key))
	if watched {
		next = max(progress, key)
	}
	last := catalog.MovieEpisode
	if media.IsShow() {
		last = media.LastEpisode()
	}
	finishable := media.Finishable()
	if finishable {
		next = min(next, last)
	}
	switch {
	case finishable && next >= last && (next > progress || watched && key >= last && status != "COMPLETED"):
		// Finished: complete it (a rewatch counts once more). Also when the
		// last episode is marked watched with the progress there already: a
		// movie (or a show watched to the end) put back on the watchlist,
		// paused or dropped.
		if status == "REPEATING" {
			repeat++
		}
		status = "COMPLETED"
	case !media.IsShow() && !watched && next == 0 && progress > 0:
		// A movie marked unwatched goes back on the watchlist, rather than
		// "watching" with nothing watched.
		status = "PLANNING"
	case status == "COMPLETED" && next < progress:
		status = "CURRENT" // reopened
	case next > progress && status != "REPEATING":
		status = "CURRENT" // not in the list, planned, paused or dropped
	}
	return next, status, repeat
}
