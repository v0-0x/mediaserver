package letterboxd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

var errNotFound = errors.New("catalog: not found")

// fakeCatalog is an in-memory catalog.Platform.
type fakeCatalog struct {
	mu      sync.Mutex
	now     func() time.Time
	viewer  *catalog.Viewer
	entries map[int]*catalog.ListEntry
	diary   []*catalog.DiaryEntry
	nextID  int64
	// films FindMovie knows, by filmKey(title, year); media MediaLite knows.
	films map[string]int
	media map[int]*catalog.Media

	updates   []catalog.EntryUpdate
	finds     map[string]int // FindMovie calls per film
	findHook  func(title string) error
	lbSynced  map[int]int64
	setViewer int
}

func newFakeCatalog(now func() time.Time) *fakeCatalog {
	return &fakeCatalog{
		now:      now,
		entries:  map[int]*catalog.ListEntry{},
		films:    map[string]int{},
		media:    map[int]*catalog.Media{},
		finds:    map[string]int{},
		lbSynced: map[int]int64{},
	}
}

func (f *fakeCatalog) SetViewer(v *catalog.Viewer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setViewer++
	if v == nil {
		f.viewer = nil
		return nil
	}
	c := *v
	f.viewer = &c
	return nil
}

func (f *fakeCatalog) Viewer() *catalog.Viewer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.viewer == nil {
		return nil
	}
	c := *f.viewer
	return &c
}

func (f *fakeCatalog) Collection(ctx context.Context, mediaType string, refresh bool) (*catalog.Collection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lists := map[string]*catalog.List{}
	for _, e := range f.entries {
		l := lists[e.Status]
		if l == nil {
			l = &catalog.List{Name: e.Status, Status: e.Status}
			lists[e.Status] = l
		}
		c := *e
		c.Media = f.mediaOf(e.MediaID)
		l.Entries = append(l.Entries, &c)
	}
	coll := &catalog.Collection{}
	for _, s := range catalog.Statuses {
		if l := lists[s]; l != nil {
			// In the platform's order (the last changed first), not the
			// map's random one.
			sort.Slice(l.Entries, func(i, j int) bool {
				a, b := l.Entries[i], l.Entries[j]
				if a.UpdatedAt != b.UpdatedAt {
					return a.UpdatedAt > b.UpdatedAt
				}
				return a.MediaID < b.MediaID
			})
			coll.Lists = append(coll.Lists, l)
		}
	}
	return coll, nil
}

func (f *fakeCatalog) mediaOf(id int) *catalog.Media {
	if m := f.media[id]; m != nil {
		return m
	}
	return &catalog.Media{ID: id, TMDBID: catalog.TMDBID(id), Type: catalog.KindOf(id)}
}

func (f *fakeCatalog) MediaLite(ctx context.Context, id int) (*catalog.Media, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.media[id]; m != nil {
		return m, nil
	}
	return nil, errNotFound
}

func (f *fakeCatalog) FindMovie(ctx context.Context, title string, year int) (*catalog.Media, error) {
	if f.findHook != nil {
		if err := f.findHook(title); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filmKey(title, year)
	f.finds[k]++
	id, ok := f.films[k]
	if !ok {
		return nil, errNotFound
	}
	return f.mediaOf(id), nil
}

func (f *fakeCatalog) UpdateEntry(ctx context.Context, u catalog.EntryUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u)
	e := f.entries[u.MediaID]
	if e == nil {
		if u.Status == nil {
			return errors.New("fake: a new entry needs a status")
		}
		e = &catalog.ListEntry{ID: u.MediaID, MediaID: u.MediaID}
		f.entries[u.MediaID] = e
	}
	if u.Status != nil {
		e.Status = *u.Status
	}
	if u.Progress != nil {
		e.Progress = *u.Progress
	}
	if u.Score != nil {
		e.Score = *u.Score
	}
	if u.Repeat != nil {
		e.Repeat = *u.Repeat
	}
	if u.Notes != nil {
		e.Notes = *u.Notes
	}
	if u.Liked != nil {
		e.Liked = *u.Liked
	}
	if u.StartedAt != nil {
		e.StartedAt = *u.StartedAt
	}
	if u.CompletedAt != nil {
		e.CompletedAt = *u.CompletedAt
	}
	e.UpdatedAt = f.now().Unix()
	return nil
}

func (f *fakeCatalog) SetLetterboxdState(ctx context.Context, mediaID int, uri string, syncedAt int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[mediaID]
	if e == nil {
		return errors.New("fake: no entry")
	}
	e.LetterboxdURI = uri
	e.LetterboxdSynced = syncedAt
	f.lbSynced[mediaID] = syncedAt
	return nil
}

func (f *fakeCatalog) AddDiary(ctx context.Context, e catalog.DiaryEntry) (*catalog.DiaryEntry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.LetterboxdID != "" {
		for _, d := range f.diary {
			if d.LetterboxdID == e.LetterboxdID {
				c := *d
				return &c, false, nil
			}
		}
	}
	f.nextID++
	e.ID = f.nextID
	c := e
	f.diary = append(f.diary, &c)
	out := c
	return &out, true, nil
}

func (f *fakeCatalog) Diary(ctx context.Context, mediaID int, limit int) ([]*catalog.DiaryEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*catalog.DiaryEntry
	for i := len(f.diary) - 1; i >= 0; i-- {
		d := f.diary[i]
		if mediaID == 0 || d.MediaID == mediaID {
			c := *d
			out = append(out, &c)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeCatalog) PendingDiary(ctx context.Context) ([]*catalog.DiaryEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*catalog.DiaryEntry
	for _, d := range f.diary {
		if d.Source == SourceKumoBox && d.ExportedAt == 0 {
			c := *d
			out = append(out, &c)
		}
	}
	return out, nil
}

func (f *fakeCatalog) MarkDiaryExported(ctx context.Context, ids []int64, at int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		for _, d := range f.diary {
			if d.ID == id {
				d.ExportedAt = at
			}
		}
	}
	return nil
}

// test accessors

func (f *fakeCatalog) entry(id int) *catalog.ListEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.entries[id]; e != nil {
		c := *e
		return &c
	}
	return nil
}

func (f *fakeCatalog) put(e catalog.ListEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID = e.MediaID
	f.entries[e.MediaID] = &e
}

func (f *fakeCatalog) diaryOf(mediaID int) []catalog.DiaryEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []catalog.DiaryEntry
	for _, d := range f.diary {
		if d.MediaID == mediaID {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WatchedDate < out[j].WatchedDate })
	return out
}

func (f *fakeCatalog) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

// testSync is a Sync on a fake catalog, a real database and settings store,
// and a fake letterboxd.com serving testdata/feed.xml for davevis.
type testSync struct {
	*Sync
	cat   *fakeCatalog
	store *config.Store
	hub   *events.Hub
	feed  *httptest.Server
	clock *time.Time
	// feedHits counts the feed requests.
	feedHits chan string
}

// the test clock: a day after the feed's last entry
var testNow = time.Date(2024, 3, 12, 9, 0, 0, 0, time.UTC)

func newTestSync(t *testing.T) *testSync {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	now := testNow
	ts := &testSync{store: store, hub: events.NewHub(), clock: &now, feedHits: make(chan string, 100)}
	clock := func() time.Time { return *ts.clock }
	ts.cat = newFakeCatalog(clock)
	fixture := readFixture(t, "feed.xml")
	ts.feed = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case ts.feedHits <- r.URL.Path:
		default:
		}
		if r.URL.Path == "/davevis/rss/" {
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = w.Write(fixture)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.feed.Close)
	ts.Sync = &Sync{
		Catalog:  ts.cat,
		Settings: store,
		DB:       d,
		Hub:      ts.hub,
		HTTP:     ts.feed.Client(),
		BaseURL:  ts.feed.URL,
		NotFound: errNotFound,
		now:      clock,
	}
	// What the fake TMDB knows.
	ts.cat.films[filmKey("An Obscure Film", 1971)] = 5000
	ts.cat.films[filmKey("The Matrix", 1999)] = 603
	ts.cat.films[filmKey("Parasite", 2019)] = 496243
	ts.cat.films[filmKey("Heat", 1995)] = 949
	ts.cat.films[filmKey("Alien", 1979)] = 348
	ts.cat.media[catalog.ShowID(87108)] = &catalog.Media{
		ID: catalog.ShowID(87108), TMDBID: 87108, IMDbID: "tt7366338", Type: catalog.TypeTV, Status: "FINISHED",
		Title:   catalog.Title{English: "Chernobyl", UserPreferred: "Chernobyl"},
		Seasons: []catalog.Season{{Number: 0, EpisodeCount: 1}, {Number: 1, EpisodeCount: 5}},
	}
	year := 1999
	ts.cat.media[603] = &catalog.Media{ID: 603, TMDBID: 603, IMDbID: "tt0133093", Type: catalog.TypeMovie, Title: catalog.Title{English: "The Matrix"}, SeasonYear: &year}
	return ts
}

func (ts *testSync) settings(t *testing.T, fn func(*config.LetterboxdSettings)) {
	t.Helper()
	s := ts.store.Get()
	fn(&s.Letterboxd)
	if _, err := ts.store.Save(s); err != nil {
		t.Fatal(err)
	}
}
