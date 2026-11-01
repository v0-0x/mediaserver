package letterboxd

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// importWorkers is how many films are looked up at once: an export can have
// thousands, found one by one by title and year.
const importWorkers = 4

// exportFilm is everything an export says about one film.
type exportFilm struct {
	name string
	year int
	// uri is the film's page (from watched, ratings, likes or the
	// watchlist), viewingURI a diary entry's.
	uri, viewingURI string
	watched         bool
	liked           bool
	watchlist       bool
	ratings         []ExportFilm
	diary           []ExportFilm
	reviews         []ExportFilm
}

func filmKey(name string, year int) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " ")) + "|" + strconv.Itoa(year)
}

// groupExport gathers the rows of ex by film.
func groupExport(ex *Export, withWatchlist bool) (map[string]*exportFilm, []string) {
	films := map[string]*exportFilm{}
	var keys []string
	get := func(r ExportFilm) *exportFilm {
		k := filmKey(r.Name, r.Year)
		f := films[k]
		if f == nil {
			f = &exportFilm{name: r.Name, year: r.Year}
			films[k] = f
			keys = append(keys, k)
		}
		return f
	}
	filmURI := func(f *exportFilm, uri string) {
		if f.uri == "" {
			f.uri = uri
		}
	}
	for _, r := range ex.Watched {
		f := get(r)
		f.watched = true
		filmURI(f, r.URI)
	}
	for _, r := range ex.Ratings {
		f := get(r)
		f.ratings = append(f.ratings, r)
		filmURI(f, r.URI)
	}
	for _, r := range ex.Likes {
		f := get(r)
		f.liked = true
		filmURI(f, r.URI)
	}
	for _, r := range ex.Diary {
		f := get(r)
		f.diary = append(f.diary, r)
		if f.viewingURI == "" {
			f.viewingURI = r.URI
		}
	}
	for _, r := range ex.Reviews {
		f := get(r)
		f.reviews = append(f.reviews, r)
		if f.viewingURI == "" {
			f.viewingURI = r.URI
		}
	}
	if withWatchlist {
		for _, r := range ex.Watchlist {
			f := get(r)
			f.watchlist = true
			filmURI(f, r.URI)
		}
	}
	sort.Strings(keys)
	return films, keys
}

// Import brings a data export into KumoBox: watched films become Watched
// (COMPLETED), ratings scores, diary entries (and the reviews written with
// them) diary entries, likes likes, and with Settings.Letterboxd.
// ImportWatchlist the watchlist's films that aren't on the list go on the
// Watchlist. Films are found by title and year; those that can't be are in
// Result.Unmatched. Importing the same export again adds nothing.
func (s *Sync) Import(ctx context.Context, ex *Export) (*Result, error) {
	res := &Result{Unmatched: []string{}}
	if ex.Empty() {
		return res, nil
	}
	release, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	res, err = s.importExport(ctx, ex)
	release()
	if err != nil {
		s.recordError(err)
	}
	s.changed(res)
	s.publishStatus()
	return res, err
}

func (s *Sync) importExport(ctx context.Context, ex *Export) (*Result, error) {
	res := &Result{Unmatched: []string{}}
	films, keys := groupExport(ex, s.settings().ImportWatchlist)
	ids, lookupErr := s.resolveFilms(ctx, films, keys)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	cs, err := s.newChanges(ctx)
	if err != nil {
		return res, err
	}
	now := s.clock().Unix()
	unmatched := map[string]bool{}
	matched := 0
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		f := films[k]
		id := ids[k]
		if id == 0 {
			unmatched[filmLabel(f.name, f.year)] = true
			continue
		}
		matched++
		c := cs.get(id)
		if err := s.importFilm(ctx, c, f, now); err != nil {
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

// importFilm applies what the export says about f to c.
func (s *Sync) importFilm(ctx context.Context, c *change, f *exportFilm, now int64) error {
	c.setURI(f.viewingURI)
	c.setURI(f.uri)

	diary := append([]ExportFilm(nil), f.diary...)
	sort.SliceStable(diary, func(i, j int) bool {
		di, dj := firstNonEmpty(diary[i].WatchedDate, diary[i].Date), firstNonEmpty(diary[j].WatchedDate, diary[j].Date)
		if di != dj {
			return di < dj
		}
		return diary[i].Date < diary[j].Date
	})
	usedReview := make([]bool, len(f.reviews))
	// review finds the review written with a diary entry: same link, or
	// else the same day.
	review := func(d ExportFilm, date string) *ExportFilm {
		for pass := 0; pass < 2; pass++ {
			for i := range f.reviews {
				r := &f.reviews[i]
				if usedReview[i] {
					continue
				}
				if (pass == 0 && d.URI != "" && r.URI == d.URI) ||
					(pass == 1 && date != "" && firstNonEmpty(r.WatchedDate, r.Date) == date) {
					usedReview[i] = true
					return r
				}
			}
		}
		return nil
	}
	add := func(d ExportFilm, rv *ExportFilm) error {
		date := firstNonEmpty(d.WatchedDate, d.Date)
		v := viewing{
			date:    date,
			rewatch: d.Rewatch,
			score:   StarsToScore(d.Rating),
			liked:   f.liked,
			tags:    d.Tags,
			lbID:    "export:" + firstNonEmpty(d.URI, filmKey(f.name, f.year)) + ":" + date,
			at:      endOfDay(firstNonEmpty(d.Date, date)),
		}
		if rv != nil {
			v.review = rv.Review
			if v.tags == "" {
				v.tags = rv.Tags
			}
		}
		_, err := s.addViewing(ctx, c, v, now)
		return err
	}
	for _, d := range diary {
		if err := add(d, review(d, firstNonEmpty(d.WatchedDate, d.Date))); err != nil {
			return err
		}
	}
	for i, r := range f.reviews {
		if usedReview[i] {
			continue
		}
		if r.WatchedDate != "" {
			// A diary entry whose diary.csv row isn't there (reviews.csv
			// uploaded alone).
			if err := add(r, &f.reviews[i]); err != nil {
				return err
			}
			continue
		}
		// A review that isn't in the diary.
		c.watched("", false)
		c.rate(StarsToScore(r.Rating), endOfDay(r.Date))
		c.note(r.Review)
	}
	if f.watched {
		c.watched("", false)
	}
	// Rating or liking a film marks it watched on Letterboxd.
	for _, r := range f.ratings {
		c.watched("", false)
		c.rate(StarsToScore(r.Rating), endOfDay(r.Date))
	}
	if f.liked {
		c.watched("", false)
		c.like()
	}
	if f.watchlist {
		c.watchlist()
	}
	return nil
}

// resolveFilms finds the media id of each film (0 when there's no match),
// importWorkers at a time. The error is the first lookup failure that
// wasn't just "not found".
func (s *Sync) resolveFilms(ctx context.Context, films map[string]*exportFilm, keys []string) (map[string]int, error) {
	ids := make(map[string]int, len(keys))
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	jobs := make(chan string)
	workers := min(importWorkers, len(keys))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				if ctx.Err() != nil {
					continue
				}
				f := films[k]
				id, err := s.findMovie(ctx, f.name, f.year)
				mu.Lock()
				ids[k] = id
				if err != nil && firstErr == nil && ctx.Err() == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for _, k := range keys {
		select {
		case jobs <- k:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return ids, firstErr
}
