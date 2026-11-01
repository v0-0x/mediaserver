package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// The user's list lives in the local database (local_list), each entry with
// a copy of its title (lite media) so that the list shows at once, also
// offline. The copies of titles whose data still changes (shows on the air,
// titles not out yet) are fetched again from TMDB once a day, in the
// background; the others once a month.

var listNames = map[string]string{
	"CURRENT":   "Watching",
	"REPEATING": "Rewatching",
	"PLANNING":  "Watchlist",
	"PAUSED":    "Paused",
	"COMPLETED": "Watched",
	"DROPPED":   "Dropped",
}

// How old the copy of a title kept with its entry may get.
const (
	liveMaxAge  = 24 * time.Hour
	finalMaxAge = 30 * 24 * time.Hour
	// refreshedWithin is what Collection(refresh) asks for.
	refreshedWithin = 10 * time.Minute
	// Titles fetched again per refresh: while the caller waits, and in the
	// background.
	waitRefreshLimit       = 40
	backgroundRefreshLimit = 200
	// After a background refresh failed (TMDB down, offline), the next one
	// waits this long.
	refreshBackoff = 10 * time.Minute
)

// listState is the list as read from the database: its snapshots by type,
// and when the copy of a title in it is next due for a refresh.
type listState struct {
	byType  map[string]*Collection
	nextDue time.Time
}

func normalizeType(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case TypeMovie:
		return TypeMovie
	case TypeTV:
		return TypeTV
	}
	return ""
}

// Collection returns the user's list: its movies ("MOVIE"), shows ("TV") or
// both (""). The result is shared with other callers and must not be
// modified. refresh fetches the data of titles still airing again first,
// unless it's recent.
func (p *Platform) Collection(ctx context.Context, mediaType string, refresh bool) (*Collection, error) {
	if refresh {
		return p.CollectionWithin(ctx, mediaType, refreshedWithin)
	}
	st, err := p.state()
	if err != nil {
		return nil, err
	}
	p.refreshSoon(st)
	return st.byType[normalizeType(mediaType)], nil
}

// CollectionWithin is Collection, with the data of the titles still airing
// (or not out yet) at most maxAge old: older copies are fetched again from
// TMDB first, as far as it answers.
func (p *Platform) CollectionWithin(ctx context.Context, mediaType string, maxAge time.Duration) (*Collection, error) {
	if p.client.Configured() && !p.backingOff() {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if n, err := p.refreshListMedia(ctx, maxAge, waitRefreshLimit); err != nil {
			p.refreshFailed.Store(time.Now().Unix())
		} else if n > 0 {
			p.refreshFailed.Store(0)
		}
		cancel()
	}
	st, err := p.state()
	if err != nil {
		return nil, err
	}
	return st.byType[normalizeType(mediaType)], nil
}

// state is the current list, read from the database when it changed.
func (p *Platform) state() (*listState, error) {
	p.mu.RLock()
	st, version := p.list, p.listVersion
	p.mu.RUnlock()
	if st != nil {
		return st, nil
	}
	st, err := p.readList()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	// A change made while reading may be missing: then don't keep it.
	if p.listVersion == version {
		p.list = st
	}
	p.mu.Unlock()
	return st, nil
}

// invalidate drops the list snapshots after a change.
func (p *Platform) invalidate() {
	p.mu.Lock()
	p.listVersion++
	p.list = nil
	p.mu.Unlock()
}

const entryCols = `media_id, status, progress, score, repeat, notes, liked, started_at, completed_at,
	letterboxd_uri, letterboxd_synced, media, media_updated, updated_at`

// scanEntry reads a local_list row; ok is false for a row without its
// title's data.
func scanEntry(row interface{ Scan(...any) error }) (e *ListEntry, mediaUpdated int64, ok bool, err error) {
	e = &ListEntry{}
	var liked int
	var started, completed, raw string
	if err := row.Scan(&e.MediaID, &e.Status, &e.Progress, &e.Score, &e.Repeat, &e.Notes, &liked, &started, &completed,
		&e.LetterboxdURI, &e.LetterboxdSynced, &raw, &mediaUpdated, &e.UpdatedAt); err != nil {
		return nil, 0, false, err
	}
	e.ID = e.MediaID
	e.Liked = liked != 0
	e.StartedAt, e.CompletedAt = parseDate(started), parseDate(completed)
	var m Media
	if json.Unmarshal([]byte(raw), &m) != nil || m.ID == 0 {
		return e, mediaUpdated, false, nil
	}
	e.Media = &m
	return e, mediaUpdated, true, nil
}

// readList reads the whole list and builds its snapshots.
func (p *Platform) readList() (*listState, error) {
	rows, err := p.db.Query(`SELECT ` + entryCols + ` FROM local_list ORDER BY updated_at DESC, media_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []*ListEntry
	st := &listState{}
	for rows.Next() {
		e, updated, ok, err := scanEntry(rows)
		if err != nil || !ok {
			continue
		}
		entries = append(entries, e)
		due := time.Unix(updated, 0).Add(finalMaxAge)
		if live(e.Media) {
			due = time.Unix(updated, 0).Add(liveMaxAge)
		}
		if st.nextDue.IsZero() || due.Before(st.nextDue) {
			st.nextDue = due
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	st.byType = map[string]*Collection{
		"":        buildCollection(entries, ""),
		TypeMovie: buildCollection(entries, TypeMovie),
		TypeTV:    buildCollection(entries, TypeTV),
	}
	return st, nil
}

// buildCollection groups the entries of a type into one list per status.
func buildCollection(entries []*ListEntry, mediaType string) *Collection {
	lists := map[string]*List{}
	for _, e := range entries {
		if mediaType != "" && KindOf(e.MediaID) != mediaType {
			continue
		}
		l, ok := lists[e.Status]
		if !ok {
			l = &List{Name: listNames[e.Status], Status: e.Status, Entries: []*ListEntry{}}
			lists[e.Status] = l
		}
		l.Entries = append(l.Entries, e)
	}
	c := &Collection{Lists: []*List{}}
	for _, s := range Statuses {
		if l, ok := lists[s]; ok {
			c.Lists = append(c.Lists, l)
		}
	}
	return c
}

// listEntry is a title's entry in the list snapshot, nil when it isn't in
// the list (or the list can't be read).
func (p *Platform) listEntry(mediaID int) *ListEntry {
	st, err := p.state()
	if err != nil {
		return nil
	}
	return st.byType[""].Find(mediaID)
}

// entry reads a title's entry from the database (nil when it isn't in the
// list), for changes made under listMu.
func (p *Platform) entry(mediaID int) (*ListEntry, error) {
	e, _, _, err := scanEntry(p.db.QueryRow(`SELECT `+entryCols+` FROM local_list WHERE media_id = ?`, mediaID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// saveListMedia updates the copy of a title kept with its list entry, if it
// has one.
func (p *Platform) saveListMedia(m *Media) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	res, err := p.db.Write(`UPDATE local_list SET media = ?, media_updated = ? WHERE media_id = ?`, string(raw), time.Now().Unix(), m.ID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		p.invalidate()
	}
}

// ---------------------------------------------------------------------------
// Refreshing the titles of the list

func (p *Platform) backingOff() bool {
	failed := p.refreshFailed.Load()
	return failed > 0 && time.Since(time.Unix(failed, 0)) < refreshBackoff
}

// refreshSoon refreshes, in the background, the titles of the list whose
// copy is due, if any is.
func (p *Platform) refreshSoon(st *listState) {
	if st.nextDue.IsZero() || time.Now().Before(st.nextDue) || !p.client.Configured() || p.backingOff() {
		return
	}
	if !p.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		n, err := p.refreshListMedia(ctx, liveMaxAge, backgroundRefreshLimit)
		if err != nil {
			p.refreshFailed.Store(time.Now().Unix())
		} else {
			p.refreshFailed.Store(0)
		}
		if n > 0 {
			p.hub.Publish(events.CollectionUpdate, nil)
		}
	}()
}

// refreshListMedia fetches again (at most limit of) the titles of the list
// whose copy is due: older than a month, or than maxAge for those whose
// data still changes. It returns how many it refreshed, and the first error.
func (p *Platform) refreshListMedia(ctx context.Context, maxAge time.Duration, limit int) (int, error) {
	rows, err := p.db.Query(`SELECT media_id, media, media_updated FROM local_list ORDER BY media_updated`)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var due []int
	for rows.Next() {
		var id int
		var raw string
		var updated int64
		if rows.Scan(&id, &raw, &updated) != nil {
			continue
		}
		var m Media
		_ = json.Unmarshal([]byte(raw), &m)
		age := now.Sub(time.Unix(updated, 0))
		if age >= finalMaxAge || (m.ID == 0 || live(&m)) && age >= maxAge {
			due = append(due, id)
		}
	}
	rows.Close()
	if len(due) > limit {
		due = due[:limit]
	}
	if len(due) == 0 {
		return 0, nil
	}
	var (
		mu       sync.Mutex
		done     int
		firstErr error
		wg       sync.WaitGroup
		next     = make(chan int)
	)
	for range min(batchWorkers, len(due)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range next {
				_, err := p.fetchMedia(ctx, id)
				if errors.Is(err, ErrNotFound) {
					// Gone from TMDB: keep the copy, don't ask every time.
					_, _ = p.db.Write(`UPDATE local_list SET media_updated = ? WHERE media_id = ?`, time.Now().Unix(), id)
					err = nil
				}
				mu.Lock()
				if err == nil {
					done++
				} else if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range due {
		if ctx.Err() != nil {
			break
		}
		next <- id
	}
	close(next)
	wg.Wait()
	return done, firstErr
}

// ---------------------------------------------------------------------------
// Changing the list

// UpdateEntry creates or updates a list entry. Fields the update leaves out
// keep their value (a new entry starts on the watchlist), but for a movie
// put on the Watched list: it's watched, its progress is 1.
func (p *Platform) UpdateEntry(ctx context.Context, u EntryUpdate) error {
	if u.Status != nil && listNames[*u.Status] == "" {
		return errors.New("invalid list status " + *u.Status)
	}
	if u.Status != nil && *u.Status == "COMPLETED" && u.Progress == nil && IsMovie(u.MediaID) {
		one := MovieEpisode
		u.Progress = &one
	}
	media, err := p.MediaLite(ctx, u.MediaID)
	if err != nil {
		return err
	}
	p.listMu.Lock()
	defer p.listMu.Unlock()
	return p.saveEntry(media, u)
}

// saveEntry writes an update of the entry of media. A single statement, so
// that fields the update leaves out keep their value. p.listMu must be held.
func (p *Platform) saveEntry(media *Media, u EntryUpdate) error {
	raw, _ := json.Marshal(media.Lite())
	var score any
	if u.Score != nil {
		score = min(max(*u.Score, 0), 100)
	}
	var liked any
	if u.Liked != nil {
		liked = b2i(*u.Liked)
	}
	var started, completed any
	if u.StartedAt != nil {
		started = formatDate(*u.StartedAt)
	}
	if u.CompletedAt != nil {
		completed = formatDate(*u.CompletedAt)
	}
	now := time.Now().Unix()
	_, err := p.db.Write(`INSERT INTO local_list(media_id, type, status, progress, score, repeat, notes, liked, started_at, completed_at, media, media_updated, updated_at)
		VALUES(?1, ?2, COALESCE(?3, 'PLANNING'), COALESCE(?4, 0), COALESCE(?5, 0), COALESCE(?6, 0), COALESCE(?7, ''), COALESCE(?8, 0),
			COALESCE(?9, ''), COALESCE(?10, ''), ?11, ?12, ?12)
		ON CONFLICT(media_id) DO UPDATE SET status = COALESCE(?3, status), progress = COALESCE(?4, progress),
			score = COALESCE(?5, score), repeat = COALESCE(?6, repeat), notes = COALESCE(?7, notes), liked = COALESCE(?8, liked),
			started_at = COALESCE(?9, started_at), completed_at = COALESCE(?10, completed_at), updated_at = excluded.updated_at`,
		media.ID, KindOf(media.ID), orNull(u.Status), orNull(u.Progress), score, orNull(u.Repeat), orNull(u.Notes), liked,
		started, completed, string(raw), now)
	if err != nil {
		return err
	}
	p.invalidate()
	p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": media.ID})
	return nil
}

// orNull is v's value, or nil (SQL NULL) when v is nil.
func orNull[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DeleteEntry removes a title from the list.
func (p *Platform) DeleteEntry(ctx context.Context, mediaID int) error {
	p.listMu.Lock()
	defer p.listMu.Unlock()
	if _, err := p.db.Write(`DELETE FROM local_list WHERE media_id = ?`, mediaID); err != nil {
		return err
	}
	p.invalidate()
	p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": mediaID})
	return nil
}

// SetLetterboxdState records a movie's Letterboxd page and when its entry
// was last synced with Letterboxd. Nothing happens to a title that isn't in
// the list.
func (p *Platform) SetLetterboxdState(ctx context.Context, mediaID int, uri string, syncedAt int64) error {
	p.listMu.Lock()
	defer p.listMu.Unlock()
	res, err := p.db.Write(`UPDATE local_list SET letterboxd_uri = ?, letterboxd_synced = ? WHERE media_id = ?`, uri, syncedAt, mediaID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		p.invalidate()
	}
	return nil
}
