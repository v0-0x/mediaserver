package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An update only changes the fields it carries.
func TestLocalUpdateKeepsOtherFields(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	m := testMovie(1)
	p.db.SetCache(liteKey(m.ID), m, time.Hour)

	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Score: ptr(80.0), Liked: ptr(true), Notes: ptr("Loved it")}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, m.ID); r.Status != "PLANNING" || r.Score != 80 || !r.Liked || r.Notes != "Loved it" || r.Progress != 0 {
		t.Fatalf("new entry = %+v", r)
	}
	started := FuzzyDate{Year: ptr(2026), Month: ptr(3), Day: ptr(14)}
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Status: ptr("CURRENT"), Progress: ptr(1), StartedAt: &started}); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Repeat: ptr(1), Score: ptr(250.0)}); err != nil {
		t.Fatal(err)
	}
	r := readLocal(t, p, m.ID)
	if r.Status != "CURRENT" || r.Progress != 1 || r.Score != 100 || r.Repeat != 1 || !r.Liked || r.Notes != "Loved it" || formatDate(r.StartedAt) != "2026-03-14" || r.CompletedAt.Year != nil {
		t.Fatalf("updated entry = %+v", r)
	}
	// An empty date clears it.
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, StartedAt: &FuzzyDate{}, Liked: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, m.ID); r.StartedAt.Year != nil || r.Liked {
		t.Fatalf("cleared: %+v", r)
	}
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Status: ptr("WATCHING")}); err == nil {
		t.Fatal("an unknown status was saved")
	}
	c, err := p.Collection(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Find(m.ID); e == nil || e.Status != "CURRENT" || e.Media == nil || e.Media.ID != m.ID || e.ID != m.ID {
		t.Fatalf("collection entry = %+v", e)
	}
}

// A movie put on the Watched list is watched: its progress is 1 (unless
// the update says otherwise), so it can be marked unwatched again.
func TestWatchedMovieHasProgress(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	m, show := testMovie(1), testShow(2, "FINISHED", 8)
	p.db.SetCache(liteKey(m.ID), m, time.Hour)
	p.db.SetCache(liteKey(show.ID), show, time.Hour)
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Status: ptr("COMPLETED"), Score: ptr(70.0)}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, m.ID); r.Status != "COMPLETED" || r.Progress != MovieEpisode || r.Score != 70 {
		t.Fatalf("watched movie: %+v", r)
	}
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: m.ID, Status: ptr("PLANNING"), Progress: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, m.ID); r.Status != "PLANNING" || r.Progress != 0 {
		t.Fatalf("back on the watchlist: %+v", r)
	}
	// A show's progress is the caller's to say.
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: show.ID, Status: ptr("COMPLETED")}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, show.ID); r.Progress != 0 {
		t.Fatalf("show: %+v", r)
	}
}

// The list comes whole, or by type, one list per status with the
// UI's names, the latest changes first.
func TestCollectionTypes(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	movie, show, other := testMovie(10), testShow(10, "RELEASING", 8), testMovie(11)
	for _, m := range []*Media{movie, show, other} {
		p.db.SetCache(liteKey(m.ID), m, time.Hour)
	}
	// updatedAt is in seconds: set it by hand to order the changes.
	touch := func(id int, at int64) {
		t.Helper()
		if _, err := p.db.Write(`UPDATE local_list SET updated_at = ? WHERE media_id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
		p.invalidate()
	}
	for i, u := range []EntryUpdate{
		{MediaID: other.ID, Status: ptr("COMPLETED")},
		{MediaID: show.ID, Status: ptr("CURRENT")},
		{MediaID: movie.ID},
	} {
		if err := p.UpdateEntry(ctx, u); err != nil {
			t.Fatal(err)
		}
		touch(u.MediaID, int64(1000+i))
	}
	all, err := p.Collection(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{"CURRENT": {show.ID}, "PLANNING": {movie.ID}, "COMPLETED": {other.ID}}
	if got := layout(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("all: %v", got)
	}
	var names []string
	for _, l := range all.Lists {
		names = append(names, l.Name)
	}
	if !reflect.DeepEqual(names, []string{"Watching", "Watchlist", "Watched"}) {
		t.Errorf("list names %v", names)
	}
	movies, _ := p.Collection(ctx, "movie", false)
	if got := layout(movies); !reflect.DeepEqual(got, map[string][]int{"PLANNING": {movie.ID}, "COMPLETED": {other.ID}}) {
		t.Errorf("movies: %v", got)
	}
	shows, _ := p.Collection(ctx, TypeTV, false)
	if got := layout(shows); !reflect.DeepEqual(got, map[string][]int{"CURRENT": {show.ID}}) {
		t.Errorf("shows: %v", got)
	}

	// Newest change first within a list.
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: other.ID, Status: ptr("PLANNING")}); err != nil {
		t.Fatal(err)
	}
	touch(other.ID, 2000)
	movies, _ = p.Collection(ctx, TypeMovie, false)
	if got := layout(movies); !reflect.DeepEqual(got, map[string][]int{"PLANNING": {other.ID, movie.ID}}) {
		t.Errorf("after moving one: %v", got)
	}

	if err := p.DeleteEntry(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := p.Collection(ctx, "", false); c.Find(other.ID) != nil || c.Find(movie.ID) == nil {
		t.Errorf("after deleting: %v", layout(c))
	}
}

// Readers (e.g. the HTTP handler encoding the list) keep the snapshot they
// got: changes build new snapshots instead of editing it.
func TestSnapshotsAreNeverModified(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	a, b := testShow(1, "FINISHED", 12), testShow(2, "FINISHED", 12)
	addLocal(t, p, a, "CURRENT", 1001)
	addLocal(t, p, b, "CURRENT", 1004)
	before, err := p.Collection(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := p.Collection(ctx, "", false); again != before {
		t.Error("an unchanged list was read again")
	}
	want, _ := json.Marshal(before)
	entryA := before.Find(a.ID)

	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: a.ID, Status: ptr("COMPLETED"), Progress: ptr(1012)}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpdateProgress(ctx, b.ID, 1005); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(before); string(got) != string(want) {
		t.Fatalf("a change modified a snapshot a reader already had:\nbefore %s\nafter  %s", want, got)
	}
	if entryA.Status != "CURRENT" || entryA.Progress != 1001 {
		t.Fatalf("a change modified an entry a reader already had: %+v", entryA)
	}
	after, _ := p.Collection(ctx, "", false)
	if e := after.Find(a.ID); e.Status != "COMPLETED" || e.Progress != 1012 {
		t.Errorf("changed entry = %+v", e)
	}
	if e := after.Find(b.ID); e.Progress != 1005 {
		t.Errorf("progress = %+v", e)
	}
}

// Many readers encode the list while it changes: run with -race. Every
// snapshot must be internally consistent (each title listed exactly once).
func TestReadersSeeConsistentSnapshots(t *testing.T) {
	const shows = 10
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	for id := 1; id <= shows; id++ {
		addLocal(t, p, testShow(id, "FINISHED", 12), "CURRENT", 1001)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				c, err := p.Collection(ctx, "", false)
				if err != nil {
					t.Error(err)
					return
				}
				raw, err := json.Marshal(c)
				if err != nil {
					t.Error(err)
					return
				}
				var decoded Collection
				_ = json.Unmarshal(raw, &decoded)
				seen := map[int]bool{}
				for _, l := range decoded.Lists {
					for _, e := range l.Entries {
						if seen[e.MediaID] {
							t.Errorf("media %d listed twice in one snapshot", e.MediaID)
							return
						}
						seen[e.MediaID] = true
					}
				}
				if len(seen) != shows {
					t.Errorf("snapshot lists %d shows, want %d", len(seen), shows)
					return
				}
				if _, err := p.MediaLite(ctx, ShowID(1+(r+i)%shows)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	statuses := []string{"COMPLETED", "PAUSED", "CURRENT"}
	for i := range 100 {
		id := ShowID(1 + i%shows)
		if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: id, Status: &statuses[i%len(statuses)], Progress: ptr(1001 + i%12)}); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	close(stop)
	wg.Wait()
}

func TestViewer(t *testing.T) {
	p := newTestPlatform(t, nil)
	if p.LoggedIn() || p.Viewer() != nil {
		t.Fatal("a viewer before connecting")
	}
	v := &Viewer{ID: 4242, Name: "dave", Letterboxd: "https://letterboxd.com/dave/"}
	if err := p.SetViewer(v); err != nil {
		t.Fatal(err)
	}
	v.Name = "changed by the caller"
	if got := p.Viewer(); got == nil || got.Name != "dave" || !p.LoggedIn() {
		t.Fatalf("viewer %+v", got)
	}
	// Still connected after a restart.
	p2 := NewPlatform(p.db, p.hub, p.settings)
	if got := p2.Viewer(); got == nil || got.ID != 4242 || got.Letterboxd != "https://letterboxd.com/dave/" {
		t.Fatalf("after a restart: %+v", got)
	}
	if err := p2.SetViewer(nil); err != nil {
		t.Fatal(err)
	}
	if NewPlatform(p.db, p.hub, p.settings).LoggedIn() || p2.LoggedIn() {
		t.Fatal("still connected after disconnecting")
	}
}

func TestSetLetterboxdState(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	m := testMovie(1)
	addLocal(t, p, m, "COMPLETED", 1)
	if err := p.SetLetterboxdState(ctx, m.ID, "https://letterboxd.com/film/movie-1/", 1_800_000_000); err != nil {
		t.Fatal(err)
	}
	c, _ := p.Collection(ctx, "", false)
	if e := c.Find(m.ID); e.LetterboxdURI != "https://letterboxd.com/film/movie-1/" || e.LetterboxdSynced != 1_800_000_000 || e.Status != "COMPLETED" {
		t.Fatalf("entry %+v", e)
	}
	if err := p.SetLetterboxdState(ctx, 999, "x", 1); err != nil {
		t.Fatalf("a title not on the list: %v", err)
	}
}

// A title page fetched from TMDB refreshes the title's copy in the list;
// when TMDB can't be reached, the saved page is shown, however old.
func TestMediaUpdatesTheList(t *testing.T) {
	var down atomic.Bool
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		if down.Load() {
			reply(w, http.StatusServiceUnavailable, map[string]any{})
			return true
		}
		return false
	})
	p := newTestPlatform(t, f)
	ctx := context.Background()
	old := testShow(94997, "RELEASING", 10)
	addLocal(t, p, old, "CURRENT", 1010)

	m, err := p.Media(ctx, ShowID(94997), false)
	if err != nil {
		t.Fatal(err)
	}
	if m.MediaListEntry == nil || m.MediaListEntry.Status != "CURRENT" || m.MediaListEntry.Progress != 1010 {
		t.Errorf("list entry %+v", m.MediaListEntry)
	}
	c, _ := p.Collection(ctx, "", false)
	if e := c.Find(ShowID(94997)); e.Media.Title.English != "House of the Dragon" || e.Media.LastEpisode() != 3008 || e.Media.Characters != nil {
		t.Errorf("the list's copy wasn't refreshed: %+v", e.Media)
	}

	// Offline, with the saved page expired.
	down.Store(true)
	if _, err := p.db.Write(`UPDATE cache SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	if m, err := p.Media(ctx, ShowID(94997), false); err != nil || m.Title.English != "House of the Dragon" {
		t.Fatalf("offline: %v %v", m, err)
	}
	if _, err := p.Media(ctx, ShowID(1396), false); err == nil {
		t.Fatal("a title never fetched came from nowhere")
	}
}

// The copies of the list's titles are fetched again when due: those whose
// data still changes after maxAge, the others after a month.
func TestRefreshListMedia(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	ctx := context.Background()
	airing := testShow(94997, "RELEASING", 10)
	ended := testShow(1396, "FINISHED", 7)
	movie := testMovie(155)
	stale := testMovie(27205)
	for _, m := range []*Media{airing, ended, movie, stale} {
		addLocal(t, p, m, "CURRENT", 0)
	}
	hourAgo := time.Now().Add(-time.Hour).Unix()
	monthsAgo := time.Now().Add(-60 * 24 * time.Hour).Unix()
	if _, err := p.db.Write(`UPDATE local_list SET media_updated = ? WHERE media_id != ?`, hourAgo, stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Write(`UPDATE local_list SET media_updated = ? WHERE media_id = ?`, monthsAgo, stale.ID); err != nil {
		t.Fatal(err)
	}
	p.invalidate()

	c, err := p.CollectionWithin(ctx, "", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if f.count("/tv/94997") != 1 || f.count("/movie/27205") != 1 || f.count("/tv/1396") != 0 || f.count("/movie/155") != 0 {
		t.Fatalf("fetched: show on the air %d, stale movie %d, ended show %d, movie %d",
			f.count("/tv/94997"), f.count("/movie/27205"), f.count("/tv/1396"), f.count("/movie/155"))
	}
	if e := c.Find(airing.ID); e.Media.Title.English != "House of the Dragon" {
		t.Errorf("not refreshed: %+v", e.Media.Title)
	}
	// Fresh now: not fetched again.
	before := f.total()
	if _, err := p.CollectionWithin(ctx, "", 10*time.Minute); err != nil || f.total() != before {
		t.Errorf("fetched again: %v, %d requests", err, f.total()-before)
	}
}

// After the metadata language (or region) changed, the list's copies are
// due again, also those of titles that don't change any more: until then
// they're kept, for the list to show at once and offline.
func TestResetCacheRefreshesTheList(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	ctx := context.Background()
	movie := testMovie(155)
	addLocal(t, p, movie, "COMPLETED", 1)
	if _, err := p.db.Write(`UPDATE local_list SET media_updated = ?`, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	p.invalidate()

	p.ResetCache()
	if e := p.listEntry(movie.ID); e == nil || e.Media == nil || e.Media.Title.English != "Movie 155" {
		t.Fatalf("the list's copy is gone: %+v", e)
	}
	c, err := p.CollectionWithin(ctx, "", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if f.count("/movie/155") != 1 {
		t.Fatalf("fetched %d times", f.count("/movie/155"))
	}
	if e := c.Find(movie.ID); e == nil || e.Media.Title.English != "The Dark Knight" {
		t.Errorf("not refreshed: %+v", e)
	}
}

func TestInvalidIDs(t *testing.T) {
	p := newTestPlatform(t, nil)
	if _, err := p.MediaLite(context.Background(), 0); !errors.Is(err, errInvalidID) {
		t.Errorf("MediaLite(0): %v", err)
	}
	if _, err := p.Media(context.Background(), -3, false); !errors.Is(err, errInvalidID) {
		t.Errorf("Media(-3): %v", err)
	}
}

// Find returns the entry Entries has for a title (custom lists and entries
// without their media left out, the first one when a title is listed
// twice), without allocating.
func TestCollectionFind(t *testing.T) {
	m := func(id int) *Media { return &Media{ID: id} }
	first := &ListEntry{MediaID: 1, Status: "CURRENT", Media: m(1)}
	c := &Collection{Lists: []*List{
		{Name: "Custom", IsCustomList: true, Entries: []*ListEntry{{MediaID: 3, Media: m(3)}}},
		{Name: "Watching", Entries: []*ListEntry{{MediaID: 2}, first}},
		{Name: "Watched", Entries: []*ListEntry{{MediaID: 1, Status: "COMPLETED", Media: m(1)}, {MediaID: 2, Media: m(2)}}},
	}}
	for _, id := range []int{1, 2, 3, 4} {
		var want *ListEntry
		for _, e := range c.Entries() {
			if e.MediaID == id {
				want = e
				break
			}
		}
		if got := c.Find(id); got != want {
			t.Errorf("Find(%d) = %+v, want %+v", id, got, want)
		}
	}
	if c.Find(1) != first {
		t.Error("not the first entry")
	}
	if n := testing.AllocsPerRun(100, func() { c.Find(2) }); n != 0 {
		t.Errorf("Find allocates %v times", n)
	}
	var none *Collection
	if none.Find(1) != nil {
		t.Error("found in a nil collection")
	}
}
