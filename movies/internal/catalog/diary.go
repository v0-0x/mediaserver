package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// The diary: each viewing of a movie (or a finished show), what Letterboxd
// calls diary entries. Moviko logs its own (source "kumobox", to export to
// Letterboxd) and keeps the ones imported from Letterboxd (source
// "letterboxd", identified by their LetterboxdID so they're imported once).

// Diary sources.
const (
	DiaryMoviko    = "kumobox"
	DiaryLetterboxd = "letterboxd"
)

const diaryCols = `id, media_id, watched_date, rating, rewatch, liked, review, tags, source, letterboxd_id, exported_at, created_at`

func scanDiary(row interface{ Scan(...any) error }) (*DiaryEntry, error) {
	var e DiaryEntry
	var rewatch, liked int
	if err := row.Scan(&e.ID, &e.MediaID, &e.WatchedDate, &e.Rating, &rewatch, &liked, &e.Review, &e.Tags, &e.Source,
		&e.LetterboxdID, &e.ExportedAt, &e.CreatedAt); err != nil {
		return nil, err
	}
	e.Rewatch, e.Liked = rewatch != 0, liked != 0
	return &e, nil
}

// AddDiary logs a viewing. The watched date defaults to today, the source to
// "kumobox". An entry whose LetterboxdID is already in the diary isn't added
// again: added is false, and the entry returned is the one there. Nor is one
// the user deleted (see DeleteDiary): added is false, without an entry.
func (p *Platform) AddDiary(ctx context.Context, e DiaryEntry) (*DiaryEntry, bool, error) {
	if e.MediaID <= 0 {
		return nil, false, errInvalidID
	}
	e.LetterboxdID = strings.TrimSpace(e.LetterboxdID)
	if e.Source == "" {
		e.Source = DiaryMoviko
	}
	if e.WatchedDate == "" {
		e.WatchedDate = time.Now().Format(time.DateOnly)
	}
	if e.LetterboxdID != "" {
		// Letterboxd's copy of a viewing the user deleted: by its id (the
		// feed read again), or the same film the same day (the export's
		// copy of it, or of one logged here and sent).
		var deleted int
		if err := p.db.QueryRow(`SELECT COUNT(*) FROM diary_deleted WHERE letterboxd_id = ? OR (media_id = ? AND watched_date = ?)`,
			e.LetterboxdID, e.MediaID, e.WatchedDate).Scan(&deleted); err != nil {
			return nil, false, err
		}
		if deleted > 0 {
			return nil, false, nil
		}
	}
	e.Rating = min(max(e.Rating, 0), 100)
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	res, err := p.db.Write(`INSERT OR IGNORE INTO diary(media_id, watched_date, rating, rewatch, liked, review, tags, source, letterboxd_id, exported_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.MediaID, e.WatchedDate, e.Rating, b2i(e.Rewatch), b2i(e.Liked), e.Review, e.Tags, e.Source, e.LetterboxdID, e.ExportedAt, e.CreatedAt)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Imported before (the only way an insert is ignored).
		old, err := scanDiary(p.db.QueryRow(`SELECT `+diaryCols+` FROM diary WHERE letterboxd_id = ? AND letterboxd_id != ''`, e.LetterboxdID))
		if err != nil {
			return nil, false, err
		}
		old.Media = p.cachedLite(old.MediaID)
		return old, false, nil
	}
	e.ID, _ = res.LastInsertId()
	e.Media = p.cachedLite(e.MediaID)
	p.hub.Publish(events.DiaryUpdated, map[string]any{"mediaId": e.MediaID})
	return &e, true, nil
}

// Diary lists the viewings of a title (mediaID 0: of every title), newest
// first, at most limit of them (0: all), each with its title (lite).
func (p *Platform) Diary(ctx context.Context, mediaID int, limit int) ([]*DiaryEntry, error) {
	q := `SELECT ` + diaryCols + ` FROM diary`
	var args []any
	if mediaID > 0 {
		q += ` WHERE media_id = ?`
		args = append(args, mediaID)
	}
	q += ` ORDER BY watched_date DESC, id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return p.queryDiary(ctx, q, args...)
}

// PendingDiary lists the viewings logged in Moviko that haven't been sent
// to Letterboxd yet, oldest first.
func (p *Platform) PendingDiary(ctx context.Context) ([]*DiaryEntry, error) {
	return p.queryDiary(ctx, `SELECT `+diaryCols+` FROM diary WHERE source = ? AND exported_at = 0 ORDER BY watched_date, id`, DiaryMoviko)
}

func (p *Platform) queryDiary(ctx context.Context, q string, args ...any) ([]*DiaryEntry, error) {
	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	out := []*DiaryEntry{}
	var ids []int
	for rows.Next() {
		e, err := scanDiary(rows)
		if err != nil {
			continue
		}
		out = append(out, e)
		ids = append(ids, e.MediaID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		media, _ := p.MediaBatch(ctx, ids)
		for _, e := range out {
			if m := media[e.MediaID]; m != nil {
				e.Media = m
			} else {
				e.Media = p.cachedLite(e.MediaID)
			}
		}
	}
	return out, nil
}

// MarkDiaryExported records that viewings went to Letterboxd at a time.
func (p *Platform) MarkDiaryExported(ctx context.Context, ids []int64, at int64) error {
	if len(ids) == 0 {
		return nil
	}
	err := p.db.Tx(func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE diary SET exported_at = ? WHERE id = ?`, at, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		p.hub.Publish(events.DiaryUpdated, nil)
	}
	return err
}

// RateDiary sets the rating (0-100) and like of a viewing: a movie rated
// once it was logged. Gone already is fine.
func (p *Platform) RateDiary(ctx context.Context, id int64, rating int, liked bool) error {
	res, err := p.db.Write(`UPDATE diary SET rating = ?, liked = ? WHERE id = ?`, min(max(rating, 0), 100), b2i(liked), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		p.hub.Publish(events.DiaryUpdated, nil)
	}
	return nil
}

// ReviewDiary sets the review of a viewing.
func (p *Platform) ReviewDiary(ctx context.Context, id int64, review string) error {
	res, err := p.db.Write(`UPDATE diary SET review = ? WHERE id = ?`, strings.TrimSpace(review), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		p.hub.Publish(events.DiaryUpdated, nil)
	}
	return nil
}

// DeleteDiary removes a viewing from the diary (gone already is fine). One
// Letterboxd has (imported from it, or sent to it) is remembered, so that
// syncing the diary doesn't bring it back (see AddDiary).
func (p *Platform) DeleteDiary(ctx context.Context, id int64) error {
	deleted := false
	err := p.db.Tx(func(tx *sql.Tx) error {
		var mediaID int
		var date, source, lbID string
		var exported int64
		err := tx.QueryRow(`SELECT media_id, watched_date, source, letterboxd_id, exported_at FROM diary WHERE id = ?`, id).
			Scan(&mediaID, &date, &source, &lbID, &exported)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM diary WHERE id = ?`, id); err != nil {
			return err
		}
		deleted = true
		if source == DiaryLetterboxd || lbID != "" || exported != 0 {
			_, err = tx.Exec(`INSERT INTO diary_deleted(media_id, watched_date, letterboxd_id, deleted_at) VALUES(?, ?, ?, ?)`,
				mediaID, date, lbID, time.Now().Unix())
		}
		return err
	})
	if err == nil && deleted {
		p.hub.Publish(events.DiaryUpdated, nil)
	}
	return err
}
