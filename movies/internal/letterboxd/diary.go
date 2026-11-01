package letterboxd

import (
	"context"
	"io"
	"math"
	"sort"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// What's watched in KumoBox goes to Letterboxd as diary entries: logged
// here (LoggedWatched), then exported as a CSV for letterboxd.com/import
// (Export, then MarkExported once imported) or sent with the API (Push).

// LoggedWatched logs a viewing of mediaID today, when Settings.Letterboxd.
// LogWatched is on; call it when KumoBox marks a movie (or show) watched.
// The entry gets the list entry's score and like, and is a rewatch when the
// diary already has the film. A viewing already logged today isn't logged
// again: that entry is returned. It returns nil when logging is off.
func (s *Sync) LoggedWatched(ctx context.Context, mediaID int) (*catalog.DiaryEntry, error) {
	if !s.settings().LogWatched || mediaID <= 0 {
		return nil, nil
	}
	now := s.clock()
	today := now.Format("2006-01-02")
	existing, err := s.Catalog.Diary(ctx, mediaID, diaryLimit)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if e != nil && e.WatchedDate == today {
			return e, nil
		}
	}
	entry := catalog.DiaryEntry{
		MediaID:     mediaID,
		WatchedDate: today,
		Rewatch:     len(existing) > 0,
		Source:      SourceKumoBox,
		CreatedAt:   now.Unix(),
	}
	if coll, err := s.Catalog.Collection(ctx, "", false); err == nil && coll != nil {
		if le := findEntry(coll, mediaID); le != nil {
			entry.Rating = int(math.Round(le.Score))
			entry.Liked = le.Liked
		}
	}
	added, _, err := s.Catalog.AddDiary(ctx, entry)
	if err != nil {
		return nil, err
	}
	if added == nil {
		added = &entry
	}
	s.publishStatus()
	s.requestPush()
	return added, nil
}

func findEntry(coll *catalog.Collection, mediaID int) *catalog.ListEntry {
	for _, l := range coll.Lists {
		if l == nil || l.IsCustomList {
			continue
		}
		for _, e := range l.Entries {
			if e != nil && e.MediaID == mediaID {
				return e
			}
		}
	}
	return nil
}

// Export writes the diary entries logged in KumoBox as a CSV for
// letterboxd.com/import: those not sent yet, or all of them when all is
// set. It returns the ids of the entries written, for MarkExported once the
// member has imported the file. Entries imported from Letterboxd are never
// written (Letterboxd has them).
func (s *Sync) Export(ctx context.Context, w io.Writer, all bool) ([]int64, error) {
	var entries []*catalog.DiaryEntry
	var err error
	if all {
		entries, err = s.Catalog.Diary(ctx, 0, allDiaryLimit)
	} else {
		entries, err = s.Catalog.PendingDiary(ctx)
	}
	if err != nil {
		return nil, err
	}
	var mine []*catalog.DiaryEntry
	for _, e := range entries {
		if e != nil && e.Source == SourceKumoBox {
			mine = append(mine, e)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].WatchedDate != mine[j].WatchedDate {
			return mine[i].WatchedDate < mine[j].WatchedDate
		}
		return mine[i].ID < mine[j].ID
	})
	media := mediaCache{s: s, m: map[int]*catalog.Media{}}
	rows := make([]ImportRow, 0, len(mine))
	ids := make([]int64, 0, len(mine))
	for _, e := range mine {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := importRow(e.MediaID, media.get(ctx, e))
		if row.TMDBID == 0 && row.IMDbID == "" && row.Title == "" {
			continue // nothing Letterboxd could match it by
		}
		row.WatchedDate = e.WatchedDate
		row.Rating = ScoreToStars(float64(e.Rating))
		row.Rewatch = e.Rewatch
		row.Review = e.Review
		row.Tags = e.Tags
		rows = append(rows, row)
		ids = append(ids, e.ID)
	}
	if err := WriteImportCSV(w, rows); err != nil {
		return nil, err
	}
	return ids, nil
}

// ExportWatchlist writes the KumoBox watchlist (PLANNING) as a CSV for
// Letterboxd's watchlist importer and returns how many films it has.
func (s *Sync) ExportWatchlist(ctx context.Context, w io.Writer) (int, error) {
	coll, err := s.Catalog.Collection(ctx, "", false)
	if err != nil {
		return 0, err
	}
	var rows []ImportRow
	seen := map[int]bool{}
	if coll != nil {
		for _, l := range coll.Lists {
			if l == nil || l.IsCustomList {
				continue
			}
			for _, e := range l.Entries {
				if e == nil || e.Status != statusPlanning || seen[e.MediaID] {
					continue
				}
				seen[e.MediaID] = true
				m := e.Media
				if m == nil {
					m, _ = s.Catalog.MediaLite(ctx, e.MediaID)
				}
				if row := importRow(e.MediaID, m); row.TMDBID != 0 || row.IMDbID != "" || row.Title != "" {
					rows = append(rows, row)
				}
			}
		}
	}
	return len(rows), WriteWatchlistCSV(w, rows)
}

// importRow is the film part of a CSV row: the TMDB id for movies only (see
// csvout.go), and the IMDb id, title and year when the media is known.
func importRow(mediaID int, m *catalog.Media) ImportRow {
	var row ImportRow
	if catalog.IsMovie(mediaID) {
		row.TMDBID = catalog.TMDBID(mediaID)
	}
	if m != nil {
		row.Title = exportTitle(m)
		row.Year = m.Year()
		row.IMDbID = m.IMDbID
	}
	return row
}

// exportTitle is the title Letterboxd is most likely to know a film by.
func exportTitle(m *catalog.Media) string {
	return firstNonEmpty(m.Title.English, m.PreferredTitle())
}

// MarkExported records that the diary entries ids are on Letterboxd now
// (after the member imported the CSV of Export).
func (s *Sync) MarkExported(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.Catalog.MarkDiaryExported(ctx, ids, s.clock().Unix()); err != nil {
		return err
	}
	s.publishStatus()
	return nil
}
