package letterboxd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

func date(y, m, d int) catalog.FuzzyDate {
	return catalog.FuzzyDate{Year: &y, Month: &m, Day: &d}
}

func checkEntry(t *testing.T, got *catalog.ListEntry, want catalog.ListEntry) {
	t.Helper()
	if got == nil {
		t.Errorf("media %d: no list entry", want.MediaID)
		return
	}
	if got.Status != want.Status || got.Progress != want.Progress || got.Score != want.Score ||
		got.Repeat != want.Repeat || got.Liked != want.Liked || got.Notes != want.Notes ||
		fuzzyString(got.CompletedAt) != fuzzyString(want.CompletedAt) ||
		(want.LetterboxdURI != "" && got.LetterboxdURI != want.LetterboxdURI) {
		t.Errorf("media %d:\n got status %s progress %d score %v repeat %d liked %v notes %q completed %s uri %s\nwant status %s progress %d score %v repeat %d liked %v notes %q completed %s uri %s",
			want.MediaID,
			got.Status, got.Progress, got.Score, got.Repeat, got.Liked, got.Notes, fuzzyString(got.CompletedAt), got.LetterboxdURI,
			want.Status, want.Progress, want.Score, want.Repeat, want.Liked, want.Notes, fuzzyString(want.CompletedAt), want.LetterboxdURI)
	}
}

// eventTypes drains the event types published so far.
func eventTypes(ch <-chan []byte) []string {
	var out []string
	for {
		select {
		case raw := <-ch:
			var e events.Event
			_ = json.Unmarshal(raw, &e)
			out = append(out, e.Type)
		default:
			return out
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

const (
	matrix    = 603
	reloaded  = 604
	parasite  = 496243
	dune2     = 693134
	obscure   = 5000
	heat      = 949
	alien     = 348
	chernobyl = catalog.ShowBase + 87108
)

func TestConnectAndPull(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	evs, unsub := ts.hub.Subscribe()
	defer unsub()

	v, res, err := ts.Connect(ctx, "https://letterboxd.com/DaveVis/")
	if err != nil {
		t.Fatal(err)
	}
	if got := ts.store.Get().Letterboxd.Username; got != "davevis" {
		t.Errorf("saved username = %q", got)
	}
	if v.ID != ViewerID("davevis") || v.Name != "Dave Vis" || v.Letterboxd != "https://letterboxd.com/davevis/" || v.MediaListOptions.ScoreFormat != "POINT_5_HALF" {
		t.Errorf("viewer = %+v", v)
	}
	if got := ts.cat.Viewer(); got == nil || got.ID != v.ID {
		t.Errorf("catalog viewer = %+v", got)
	}
	want := &Result{Added: 6, Diary: 6, Unmatched: []string{"Nonexistent Movie (1900)"}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	checkEntry(t, ts.cat.entry(matrix), catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 90, Repeat: 1, Liked: true,
		CompletedAt: date(2024, 3, 10), LetterboxdURI: "https://letterboxd.com/film/the-matrix/"})
	checkEntry(t, ts.cat.entry(parasite), catalog.ListEntry{MediaID: parasite, Status: "COMPLETED", Progress: 1, Score: 100, CompletedAt: date(2024, 3, 2)})
	checkEntry(t, ts.cat.entry(dune2), catalog.ListEntry{MediaID: dune2, Status: "COMPLETED", Progress: 1, Score: 70, CompletedAt: date(2024, 3, 5)})
	checkEntry(t, ts.cat.entry(obscure), catalog.ListEntry{MediaID: obscure, Status: "COMPLETED", Progress: 1, Score: 40, CompletedAt: date(2024, 2, 15)})
	// A show: progress is its last episode.
	checkEntry(t, ts.cat.entry(chernobyl), catalog.ListEntry{MediaID: chernobyl, Status: "COMPLETED", Progress: 1005, CompletedAt: date(2024, 2, 20)})
	// A review without a diary date: watched, rated, noted; no diary entry.
	checkEntry(t, ts.cat.entry(reloaded), catalog.ListEntry{MediaID: reloaded, Status: "COMPLETED", Progress: 1, Score: 60, Notes: "The highway chase, though."})
	if d := ts.cat.diaryOf(reloaded); len(d) != 0 {
		t.Errorf("undated review logged: %+v", d)
	}
	if e := ts.cat.entry(matrix); e.LetterboxdSynced != testNow.Unix() {
		t.Errorf("synced at %d", e.LetterboxdSynced)
	}

	d := ts.cat.diaryOf(matrix)
	if len(d) != 2 {
		t.Fatalf("matrix diary = %+v", d)
	}
	first, second := d[0], d[1]
	if first.WatchedDate != "2020-05-01" || first.Rating != 80 || first.Rewatch || !first.Liked || first.Source != "letterboxd" || first.LetterboxdID != "letterboxd-watch-401000007" || first.Review != "" {
		t.Errorf("first viewing = %+v", first)
	}
	if second.WatchedDate != "2024-03-10" || second.Rating != 90 || !second.Rewatch || second.LetterboxdID != "letterboxd-review-568371234" ||
		!strings.HasPrefix(second.Review, "Still holds up.") {
		t.Errorf("second viewing = %+v", second)
	}
	if d := ts.cat.diaryOf(dune2); len(d) != 1 || d[0].Review != "Loud. Very loud." {
		t.Errorf("dune diary = %+v", d)
	}

	types := eventTypes(evs)
	if !contains(types, events.CollectionUpdate) || !contains(types, StatusEvent) {
		t.Errorf("events = %v", types)
	}
	st := ts.Status(ctx)
	if !st.Connected || st.Username != "davevis" || st.DisplayName != "Dave Vis" || st.LastSync != testNow.Unix() || st.LastError != "" ||
		st.ProfileURL != "https://letterboxd.com/davevis/" || st.APIConnected || st.Syncing {
		t.Errorf("status = %+v", st)
	}

	// A second read has nothing new.
	updates := ts.cat.updateCount()
	res, err = ts.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want = &Result{Skipped: 6, Unmatched: []string{"Nonexistent Movie (1900)"}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("second pull = %+v", res)
	}
	if n := ts.cat.updateCount(); n != updates {
		t.Errorf("second pull made %d updates", n-updates)
	}
	if types := eventTypes(evs); contains(types, events.CollectionUpdate) {
		t.Errorf("second pull published %v", types)
	}
}

func TestPullKeepsMovikoState(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	ts.settings(t, func(l *config.LetterboxdSettings) { l.Username = "davevis" })
	at := func(y, m, d int) int64 { return time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.UTC).Unix() }
	// Rated in Moviko after both Letterboxd ratings: the score stays.
	ts.cat.put(catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 50, CompletedAt: date(2019, 1, 1), UpdatedAt: at(2024, 3, 11)})
	// Rated before: Letterboxd's newer rating wins.
	ts.cat.put(catalog.ListEntry{MediaID: dune2, Status: "PAUSED", Score: 50, UpdatedAt: at(2024, 1, 1)})
	// Being watched in Moviko: a show stays Watching.
	ts.cat.put(catalog.ListEntry{MediaID: chernobyl, Status: "CURRENT", Progress: 1003, UpdatedAt: at(2024, 1, 1)})
	// Rewatching since after the viewing: stays.
	ts.cat.put(catalog.ListEntry{MediaID: parasite, Status: "REPEATING", Score: 0, UpdatedAt: at(2024, 3, 5)})
	// On the watchlist: an undated review doesn't touch it.
	ts.cat.put(catalog.ListEntry{MediaID: reloaded, Status: "PLANNING", UpdatedAt: at(2024, 1, 1)})
	// Logged in Moviko the same day as on Letterboxd, not exported yet.
	ts.cat.put(catalog.ListEntry{MediaID: obscure, Status: "COMPLETED", Progress: 1, Score: 40, CompletedAt: date(2024, 2, 15), UpdatedAt: at(2024, 2, 15)})
	mine, _, _ := ts.cat.AddDiary(ctx, catalog.DiaryEntry{MediaID: obscure, WatchedDate: "2024-02-15", Rating: 40, Source: SourceMoviko})

	res, err := ts.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkEntry(t, ts.cat.entry(matrix), catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 50, Repeat: 1, Liked: true, CompletedAt: date(2024, 3, 10)})
	checkEntry(t, ts.cat.entry(dune2), catalog.ListEntry{MediaID: dune2, Status: "COMPLETED", Progress: 1, Score: 70, CompletedAt: date(2024, 3, 5)})
	checkEntry(t, ts.cat.entry(chernobyl), catalog.ListEntry{MediaID: chernobyl, Status: "CURRENT", Progress: 1003})
	checkEntry(t, ts.cat.entry(parasite), catalog.ListEntry{MediaID: parasite, Status: "REPEATING", Score: 100})
	checkEntry(t, ts.cat.entry(reloaded), catalog.ListEntry{MediaID: reloaded, Status: "PLANNING"})
	checkEntry(t, ts.cat.entry(obscure), catalog.ListEntry{MediaID: obscure, Status: "COMPLETED", Progress: 1, Score: 40, CompletedAt: date(2024, 2, 15)})

	if d := ts.cat.diaryOf(chernobyl); len(d) != 1 {
		t.Errorf("chernobyl diary = %+v", d)
	}
	d := ts.cat.diaryOf(obscure)
	if len(d) != 1 || d[0].ID != mine.ID || d[0].ExportedAt != testNow.Unix() {
		t.Errorf("obscure diary = %+v", d)
	}
	want := &Result{Updated: 4, Skipped: 2, Diary: 5, Unmatched: []string{"Nonexistent Movie (1900)"}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

func TestChangeRules(t *testing.T) {
	day := func(y, m, d int) int64 { return time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.Local).Unix() }
	tests := []struct {
		name   string
		id     int
		before *catalog.ListEntry
		apply  func(c *change)
		want   listState
	}{
		{"new movie viewing", matrix, nil, func(c *change) { c.watched("2024-01-02", false) },
			listState{exists: true, status: "COMPLETED", progress: 1, completedAt: "2024-01-02"}},
		{"rewatch of a watched movie", matrix, &catalog.ListEntry{Status: "COMPLETED", Progress: 1, CompletedAt: date(2020, 1, 1)},
			func(c *change) { c.watched("2024-01-02", true) },
			listState{exists: true, status: "COMPLETED", progress: 1, repeat: 1, completedAt: "2024-01-02"}},
		{"rewatch flag on a first watch", matrix, nil, func(c *change) { c.watched("2024-01-02", true) },
			listState{exists: true, status: "COMPLETED", progress: 1, completedAt: "2024-01-02"}},
		{"older viewing keeps the later date", matrix, &catalog.ListEntry{Status: "COMPLETED", Progress: 1, CompletedAt: date(2024, 5, 1)},
			func(c *change) { c.watched("2024-01-02", false) },
			listState{exists: true, status: "COMPLETED", progress: 1, completedAt: "2024-05-01"}},
		{"partial date is older", matrix, &catalog.ListEntry{Status: "COMPLETED", Progress: 1, CompletedAt: catalog.FuzzyDate{Year: new(2024)}},
			func(c *change) { c.watched("2024-01-02", false) },
			listState{exists: true, status: "COMPLETED", progress: 1, completedAt: "2024-01-02"}},
		{"movie watched since Moviko's rewatch began", matrix, &catalog.ListEntry{Status: "REPEATING", Progress: 0, UpdatedAt: day(2024, 1, 1)},
			func(c *change) { c.watched("2024-01-02", true) },
			listState{exists: true, status: "COMPLETED", progress: 1, completedAt: "2024-01-02"}},
		{"movie rewatch began after the viewing", matrix, &catalog.ListEntry{Status: "REPEATING", UpdatedAt: day(2024, 2, 1)},
			func(c *change) { c.watched("2024-01-02", true) },
			listState{exists: true, status: "REPEATING"}},
		{"marked watched doesn't end a rewatch", matrix, &catalog.ListEntry{Status: "CURRENT", UpdatedAt: day(2024, 1, 1)},
			func(c *change) { c.watched("", false) },
			listState{exists: true, status: "CURRENT"}},
		{"show being watched", chernobyl, &catalog.ListEntry{Status: "CURRENT", Progress: 1002, UpdatedAt: day(2020, 1, 1)},
			func(c *change) { c.watched("2024-01-02", false) },
			listState{exists: true, status: "CURRENT", progress: 1002}},
		{"dropped show watched", chernobyl, &catalog.ListEntry{Status: "DROPPED", Progress: 1002},
			func(c *change) { c.watched("2024-01-02", false) },
			listState{exists: true, status: "COMPLETED", progress: 1002, completedAt: "2024-01-02"}},
		{"rating with no score", matrix, &catalog.ListEntry{Status: "COMPLETED", UpdatedAt: day(2024, 6, 1)},
			func(c *change) { c.rate(70, day(2024, 1, 1)) },
			listState{exists: true, status: "COMPLETED", score: 70}},
		{"newer Moviko score stays", matrix, &catalog.ListEntry{Status: "COMPLETED", Score: 40, UpdatedAt: day(2024, 6, 1)},
			func(c *change) { c.rate(70, day(2024, 1, 1)) },
			listState{exists: true, status: "COMPLETED", score: 40}},
		{"newer Letterboxd rating wins", matrix, &catalog.ListEntry{Status: "COMPLETED", Score: 40, UpdatedAt: day(2023, 6, 1)},
			func(c *change) { c.rate(70, day(2024, 1, 1)) },
			listState{exists: true, status: "COMPLETED", score: 70}},
		{"older then newer Letterboxd ratings", matrix, nil,
			func(c *change) { c.rate(70, day(2024, 1, 1)); c.rate(90, day(2024, 2, 1)); c.rate(30, day(2023, 1, 1)) },
			listState{score: 90}},
		{"watchlist adds", alien, nil, func(c *change) { c.watchlist() }, listState{exists: true, status: "PLANNING"}},
		{"watchlist leaves a listed film", heat, &catalog.ListEntry{Status: "DROPPED"}, func(c *change) { c.watchlist() }, listState{exists: true, status: "DROPPED"}},
		{"notes only when empty", heat, &catalog.ListEntry{Status: "COMPLETED", Notes: "mine"}, func(c *change) { c.note("theirs") }, listState{exists: true, status: "COMPLETED", notes: "mine"}},
		{"likes add", heat, &catalog.ListEntry{Status: "COMPLETED"}, func(c *change) { c.like() }, listState{exists: true, status: "COMPLETED", liked: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.before != nil {
				tt.before.MediaID = tt.id
			}
			c := newChange(tt.id, tt.before)
			tt.apply(c)
			got := c.after
			got.scoreAt, got.updatedAt = 0, 0
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestChangeUpdate(t *testing.T) {
	c := newChange(matrix, &catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 50})
	if _, changed := c.update(); changed {
		t.Error("no change reported as one")
	}
	c.watched("2024-01-02", true)
	c.rate(80, time.Now().Unix())
	u, changed := c.update()
	if !changed || u.Status != nil || u.Progress != nil || *u.Score != 80 || *u.Repeat != 1 || fuzzyString(*u.CompletedAt) != "2024-01-02" || u.Liked != nil || u.Notes != nil {
		t.Errorf("update = %+v", u)
	}
	c = newChange(alien, nil)
	c.watchlist()
	if u, changed := c.update(); !changed || *u.Status != "PLANNING" || u.Progress != nil {
		t.Errorf("new entry update = %+v", u)
	}
}

func TestImport(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	ts.settings(t, func(l *config.LetterboxdSettings) { l.Username = "davevis"; l.ImportWatchlist = true })
	// Rated in Moviko after the Letterboxd rating (2019-06-01).
	ts.cat.put(catalog.ListEntry{MediaID: heat, Status: "COMPLETED", Progress: 1, Score: 80, UpdatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()})
	data := buildZip(t, "", exportFiles)
	ex, err := ReadExportZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	res, err := ts.Import(ctx, ex)
	if err != nil {
		t.Fatal(err)
	}
	want := &Result{Added: 3, Updated: 1, Diary: 3, Unmatched: []string{"Unknown Film (2001)"}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	checkEntry(t, ts.cat.entry(matrix), catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 90, Repeat: 1, Liked: true,
		CompletedAt: date(2024, 3, 10), LetterboxdURI: "https://boxd.it/2a1m"})
	checkEntry(t, ts.cat.entry(parasite), catalog.ListEntry{MediaID: parasite, Status: "COMPLETED", Progress: 1, Score: 100, CompletedAt: date(2023, 11, 4)})
	checkEntry(t, ts.cat.entry(heat), catalog.ListEntry{MediaID: heat, Status: "COMPLETED", Progress: 1, Score: 80, Notes: "The diner scene."})
	checkEntry(t, ts.cat.entry(alien), catalog.ListEntry{MediaID: alien, Status: "PLANNING"})

	d := ts.cat.diaryOf(matrix)
	if len(d) != 2 {
		t.Fatalf("matrix diary = %+v", d)
	}
	if d[0].LetterboxdID != "export:https://boxd.it/aaa1:2020-05-01" || d[0].Rating != 80 || d[0].Rewatch || !d[0].Liked || d[0].Review != "" {
		t.Errorf("first viewing = %+v", d[0])
	}
	if d[1].LetterboxdID != "export:https://boxd.it/aaa2:2024-03-10" || d[1].Rating != 90 || !d[1].Rewatch ||
		d[1].Review != "Still holds up.\n\nThe lobby scene, \"wow\"." || d[1].Tags != "cinema, friends" || d[1].Source != SourceLetterboxd {
		t.Errorf("second viewing = %+v", d[1])
	}
	// Each film was looked up once.
	for k, n := range ts.cat.finds {
		if n != 1 {
			t.Errorf("%s looked up %d times", k, n)
		}
	}

	// Again: nothing new.
	updates := ts.cat.updateCount()
	res, err = ts.Import(ctx, ex)
	if err != nil {
		t.Fatal(err)
	}
	if want := (&Result{Skipped: 4, Unmatched: []string{"Unknown Film (2001)"}}); !reflect.DeepEqual(res, want) {
		t.Errorf("second import = %+v", res)
	}
	if n := ts.cat.updateCount(); n != updates {
		t.Errorf("second import made %d updates", n-updates)
	}

	// The feed has the same Matrix viewings: they aren't logged twice.
	if _, err := ts.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if d := ts.cat.diaryOf(matrix); len(d) != 2 {
		t.Errorf("matrix diary after pull = %+v", d)
	}
	if d := ts.cat.diaryOf(parasite); len(d) != 2 {
		t.Errorf("parasite diary after pull = %+v", d)
	}
}

func TestImportWithoutWatchlist(t *testing.T) {
	ts := newTestSync(t)
	ts.settings(t, func(l *config.LetterboxdSettings) { l.ImportWatchlist = false })
	ex, err := ReadExportFile("watchlist.csv", strings.NewReader(exportFiles["watchlist.csv"]))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ts.Import(context.Background(), ex)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added+res.Updated != 0 || ts.cat.entry(alien) != nil || len(ts.cat.finds) != 0 {
		t.Errorf("watchlist imported: %+v", res)
	}
}

func TestImportReviewsAlone(t *testing.T) {
	ts := newTestSync(t)
	ex, err := ReadExportFile("reviews.csv", strings.NewReader(exportFiles["reviews.csv"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Import(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	// A dated review is a diary entry; an undated one fills the notes.
	if d := ts.cat.diaryOf(matrix); len(d) != 1 || d[0].LetterboxdID != "export:https://boxd.it/aaa2:2024-03-10" || !strings.HasPrefix(d[0].Review, "Still holds up.") {
		t.Errorf("matrix diary = %+v", d)
	}
	checkEntry(t, ts.cat.entry(heat), catalog.ListEntry{MediaID: heat, Status: "COMPLETED", Progress: 1, Score: 70, Notes: "The diner scene."})
}

func manyFilms(ts *testSync, n int) *Export {
	ex := &Export{}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("Film %d", i)
		ts.cat.films[filmKey(name, 2000)] = 10000 + i
		ex.Watched = append(ex.Watched, ExportFilm{Name: name, Year: 2000, Date: "2024-01-01"})
	}
	return ex
}

func TestImportBoundsConcurrency(t *testing.T) {
	ts := newTestSync(t)
	ex := manyFilms(ts, 60)
	var running, peak atomic.Int32
	ts.cat.findHook = func(string) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		return nil
	}
	res, err := ts.Import(context.Background(), ex)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 60 || len(res.Unmatched) != 0 {
		t.Errorf("result = %+v", res)
	}
	if p := peak.Load(); p > importWorkers || p < 1 {
		t.Errorf("%d lookups at once", p)
	}
}

func TestImportCancel(t *testing.T) {
	ts := newTestSync(t)
	ex := manyFilms(ts, 60)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	ts.cat.findHook = func(string) error {
		if calls.Add(1) == 10 {
			cancel()
		}
		return nil
	}
	if _, err := ts.Import(ctx, ex); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if n := ts.cat.updateCount(); n != 0 {
		t.Errorf("%d updates after cancel", n)
	}
	if n := calls.Load(); n > 10+importWorkers {
		t.Errorf("%d lookups after cancel", n)
	}
}

func TestImportLookupsFail(t *testing.T) {
	ts := newTestSync(t)
	ex := manyFilms(ts, 5)
	down := errors.New("tmdb: no API key")
	ts.cat.findHook = func(string) error { return down }
	res, err := ts.Import(context.Background(), ex)
	if !errors.Is(err, down) || res == nil || len(res.Unmatched) != 5 {
		t.Errorf("err = %v, res = %+v", err, res)
	}
	if st := ts.Status(context.Background()); st.LastError == "" {
		t.Error("error not in status")
	}
}

func TestExport(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	add := func(e catalog.DiaryEntry) int64 {
		got, _, err := ts.cat.AddDiary(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	m := add(catalog.DiaryEntry{MediaID: matrix, WatchedDate: "2024-03-01", Rating: 87, Rewatch: true, Review: "Great, again.", Tags: "a, b", Source: SourceMoviko})
	c := add(catalog.DiaryEntry{MediaID: chernobyl, WatchedDate: "2024-02-01", Source: SourceMoviko})
	h := add(catalog.DiaryEntry{MediaID: heat, WatchedDate: "2024-01-15", Rating: 70, Source: SourceMoviko, ExportedAt: 123})
	add(catalog.DiaryEntry{MediaID: parasite, WatchedDate: "2024-01-20", Rating: 100, Source: SourceLetterboxd, LetterboxdID: "letterboxd-watch-1"})

	var buf bytes.Buffer
	ids, err := ts.Export(ctx, &buf, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "Title,Year,tmdbID,imdbID,WatchedDate,Rating,Rewatch,Review,Tags\n" +
		"Chernobyl,,,tt7366338,2024-02-01,,false,,\n" +
		"The Matrix,1999,603,tt0133093,2024-03-01,4.5,true,\"Great, again.\",\"a, b\"\n"
	if buf.String() != want {
		t.Errorf("pending CSV:\n%s\nwant:\n%s", buf.String(), want)
	}
	if !reflect.DeepEqual(ids, []int64{c, m}) {
		t.Errorf("ids = %v", ids)
	}
	if st := ts.Status(ctx); st.Pending != 2 {
		t.Errorf("pending = %d", st.Pending)
	}

	buf.Reset()
	ids, err = ts.Export(ctx, &buf, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int64{h, c, m}) || !strings.Contains(buf.String(), "\n,,949,,2024-01-15,3.5,false,,\n") {
		t.Errorf("all: ids %v\n%s", ids, buf.String())
	}

	if err := ts.MarkExported(ctx, []int64{c, m}); err != nil {
		t.Fatal(err)
	}
	if st := ts.Status(ctx); st.Pending != 0 {
		t.Errorf("pending after marking = %d", st.Pending)
	}
	buf.Reset()
	if ids, err := ts.Export(ctx, &buf, false); err != nil || len(ids) != 0 || buf.String() != strings.Join(ImportHeader, ",")+"\n" {
		t.Errorf("empty export: %v %v %q", ids, err, buf.String())
	}
}

func TestExportWatchlist(t *testing.T) {
	ts := newTestSync(t)
	year := 1979
	ts.cat.media[alien] = &catalog.Media{ID: alien, TMDBID: alien, IMDbID: "tt0078748", Title: catalog.Title{English: "Alien"}, SeasonYear: &year}
	ts.cat.put(catalog.ListEntry{MediaID: alien, Status: "PLANNING"})
	ts.cat.put(catalog.ListEntry{MediaID: chernobyl, Status: "PLANNING"})
	ts.cat.put(catalog.ListEntry{MediaID: heat, Status: "COMPLETED"})
	var buf bytes.Buffer
	n, err := ts.ExportWatchlist(context.Background(), &buf)
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	want := "Title,Year,tmdbID,imdbID\nAlien,1979,348,tt0078748\nChernobyl,,,tt7366338\n"
	if buf.String() != want {
		t.Errorf("got:\n%s", buf.String())
	}
}

func TestLoggedWatched(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	ts.cat.put(catalog.ListEntry{MediaID: matrix, Status: "COMPLETED", Progress: 1, Score: 85, Liked: true})

	e, err := ts.LoggedWatched(ctx, matrix)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil || e.ID == 0 || e.WatchedDate != "2024-03-12" || e.Rating != 85 || !e.Liked || e.Rewatch || e.Source != SourceMoviko || e.ExportedAt != 0 {
		t.Fatalf("entry = %+v", e)
	}
	// The same day again: the same entry.
	again, err := ts.LoggedWatched(ctx, matrix)
	if err != nil || again.ID != e.ID || len(ts.cat.diaryOf(matrix)) != 1 {
		t.Errorf("logged twice: %+v %v", again, err)
	}
	// The next day: a rewatch.
	*ts.clock = testNow.Add(24 * time.Hour)
	next, err := ts.LoggedWatched(ctx, matrix)
	if err != nil || next.ID == e.ID || !next.Rewatch || next.WatchedDate != "2024-03-13" {
		t.Errorf("rewatch = %+v %v", next, err)
	}
	// A film off the list has no rating.
	if e, err := ts.LoggedWatched(ctx, heat); err != nil || e.Rating != 0 || e.Liked || e.Rewatch {
		t.Errorf("heat = %+v %v", e, err)
	}
	if st := ts.Status(ctx); st.Pending != 3 {
		t.Errorf("pending = %d", st.Pending)
	}

	ts.settings(t, func(l *config.LetterboxdSettings) { l.LogWatched = false })
	*ts.clock = testNow.Add(48 * time.Hour)
	if e, err := ts.LoggedWatched(ctx, matrix); e != nil || err != nil {
		t.Errorf("logged while off: %+v %v", e, err)
	}
}

func TestConnectErrors(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	if _, err := ts.Pull(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("pull without account: %v", err)
	}
	if _, _, err := ts.Connect(ctx, "ghost"); !errors.Is(err, ErrNoSuchMember) {
		t.Errorf("unknown member: %v", err)
	}
	if _, _, err := ts.Connect(ctx, "not a name"); !errors.Is(err, ErrBadUsername) {
		t.Errorf("bad name: %v", err)
	}
	if u := ts.store.Get().Letterboxd.Username; u != "" || ts.cat.Viewer() != nil {
		t.Errorf("failed connects saved %q / %+v", u, ts.cat.Viewer())
	}
	// A member who disappeared: the error is kept.
	ts.settings(t, func(l *config.LetterboxdSettings) { l.Username = "ghost" })
	if _, err := ts.Pull(ctx); !errors.Is(err, ErrNoSuchMember) {
		t.Errorf("pull: %v", err)
	}
	if st := ts.Status(ctx); st.LastError == "" || st.LastSync != 0 || !st.Connected {
		t.Errorf("status = %+v", st)
	}
}

func TestDisconnect(t *testing.T) {
	ts := newTestSync(t)
	ctx := context.Background()
	if _, _, err := ts.Connect(ctx, "davevis"); err != nil {
		t.Fatal(err)
	}
	_ = ts.setKV(kvToken, Token{AccessToken: "x"})
	if err := ts.Disconnect(); err != nil {
		t.Fatal(err)
	}
	st := ts.Status(ctx)
	if st.Connected || st.APIConnected || st.LastSync != 0 || ts.store.Get().Letterboxd.Username != "" || ts.cat.Viewer() != nil {
		t.Errorf("after disconnect: %+v", st)
	}
	if ts.cat.entry(matrix) == nil {
		t.Error("list data removed")
	}
}

func TestStartPullsInBackground(t *testing.T) {
	ts := newTestSync(t)
	ts.settings(t, func(l *config.LetterboxdSettings) { l.Username = "davevis"; l.AutoSync = true })
	ts.firstDelay, ts.tick = 5*time.Millisecond, 5*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts.Start(ctx)
	select {
	case p := <-ts.feedHits:
		if p != "/davevis/rss/" {
			t.Errorf("fetched %s", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no pull")
	}
	deadline := time.Now().Add(5 * time.Second)
	for ts.Status(ctx).LastSync == 0 {
		if time.Now().After(deadline) {
			t.Fatal("pull didn't finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Not again before SyncMinutes.
	select {
	case <-ts.feedHits:
		t.Error("pulled again at once")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
}

func TestStartIdleWithoutAutoSync(t *testing.T) {
	ts := newTestSync(t)
	ts.settings(t, func(l *config.LetterboxdSettings) { l.Username = "davevis"; l.AutoSync = false })
	ts.firstDelay, ts.tick = time.Millisecond, time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	ts.Start(ctx)
	select {
	case <-ts.feedHits:
		t.Error("pulled with AutoSync off")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
}
