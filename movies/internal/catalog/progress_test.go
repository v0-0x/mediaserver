package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

func TestProgressUpdate(t *testing.T) {
	now := time.Date(2026, time.October, 6, 20, 30, 0, 0, time.Local)
	someDate := FuzzyDate{Year: ptr(2025)}
	ended := testShow(1, "FINISHED", 6, 6) // S01E01 1001 … S02E06 2006
	airing := testShow(2, "RELEASING", 6, 6)
	unknown := testShow(3, "FINISHED") // no seasons known
	movie := testMovie(4)
	e := func(status string, progress int) *ListEntry {
		return &ListEntry{Status: status, Progress: progress}
	}
	type want struct {
		ok        bool
		status    string
		progress  int
		repeat    int // 0: not changed
		started   bool
		completed bool
	}
	cases := []struct {
		name  string
		media *Media
		entry *ListEntry
		key   int
		want  want
	}{
		{"not in list", ended, nil, 1001, want{ok: true, status: "CURRENT", progress: 1001, started: true}},
		{"not in list, last episode", ended, nil, 2006, want{ok: true, status: "COMPLETED", progress: 2006, started: true, completed: true}},
		{"planned show starts", ended, e("PLANNING", 0), 1003, want{ok: true, status: "CURRENT", progress: 1003, started: true}},
		{"planned show keeps its progress", ended, e("PLANNING", 1005), 1002, want{ok: true, status: "CURRENT", progress: 1005, started: true}},
		{"planned show, same episode", ended, e("PLANNING", 1005), 1005, want{ok: true, status: "CURRENT", progress: 1005, started: true}},
		{"planned show, last episode", ended, e("PLANNING", 1005), 2006, want{ok: true, status: "COMPLETED", progress: 2006, started: true, completed: true}},
		{"watching, next episode", ended, e("CURRENT", 1005), 1006, want{ok: true, status: "CURRENT", progress: 1006}},
		{"watching, next season", ended, e("CURRENT", 1006), 2001, want{ok: true, status: "CURRENT", progress: 2001}},
		{"watching, first episode", ended, e("CURRENT", 0), 1001, want{ok: true, status: "CURRENT", progress: 1001, started: true}},
		{"watching, same episode", ended, e("CURRENT", 1005), 1005, want{}},
		{"watching, earlier season", ended, e("CURRENT", 2001), 1003, want{}},
		{"watching, last episode", ended, e("CURRENT", 2005), 2006, want{ok: true, status: "COMPLETED", progress: 2006, completed: true}},
		{"past the last episode", ended, e("CURRENT", 2005), 2008, want{ok: true, status: "COMPLETED", progress: 2008, completed: true}},
		{"specials never count", ended, e("CURRENT", 1005), EpisodeKey(0, 3), want{}},
		{"a show on the air never completes", airing, e("CURRENT", 2005), 2006, want{ok: true, status: "CURRENT", progress: 2006}},
		{"unknown episodes never complete", unknown, e("CURRENT", 1030), 1031, want{ok: true, status: "CURRENT", progress: 1031}},
		{"paused show resumes", ended, e("PAUSED", 1002), 1003, want{ok: true, status: "CURRENT", progress: 1003}},
		{"dropped show, earlier episode", ended, e("DROPPED", 1004), 1002, want{}},
		{"completed show, rewatch starts", ended, e("COMPLETED", 2006), 1001, want{ok: true, status: "REPEATING", progress: 1001}},
		{"completed show, last episode", ended, e("COMPLETED", 2006), 2006, want{}},
		{"completed show, a new season", airing, e("COMPLETED", 1006), 2001, want{ok: true, status: "CURRENT", progress: 2001}},
		{"completed show on the air, earlier episode", airing, e("COMPLETED", 1006), 1003, want{}},
		{"rewatching, next episode", ended, e("REPEATING", 1003), 1004, want{ok: true, status: "REPEATING", progress: 1004}},
		{"rewatching, earlier episode", ended, e("REPEATING", 1003), 1002, want{}},
		{"rewatch finished", ended, &ListEntry{Status: "REPEATING", Progress: 2005, Repeat: 1}, 2006,
			want{ok: true, status: "COMPLETED", progress: 2006, repeat: 2, completed: true}},
		{"start date kept", ended, &ListEntry{Status: "PLANNING", StartedAt: someDate}, 1001,
			want{ok: true, status: "CURRENT", progress: 1001}},
		{"finish date kept", ended, &ListEntry{Status: "REPEATING", Progress: 2005, Repeat: 1, CompletedAt: someDate}, 2006,
			want{ok: true, status: "COMPLETED", progress: 2006, repeat: 2}},
		{"no episode", ended, nil, 0, want{}},
		{"movie watched", movie, nil, MovieEpisode, want{ok: true, status: "COMPLETED", progress: 1, started: true, completed: true}},
		{"planned movie", movie, e("PLANNING", 0), MovieEpisode, want{ok: true, status: "COMPLETED", progress: 1, started: true, completed: true}},
		{"movie being watched", movie, e("CURRENT", 1), MovieEpisode, want{ok: true, status: "COMPLETED", progress: 1, completed: true}},
		{"movie watched again", movie, &ListEntry{Status: "COMPLETED", Progress: 1, CompletedAt: someDate}, MovieEpisode,
			want{ok: true, status: "COMPLETED", progress: 1, repeat: 1}},
		{"movie rewatched", movie, &ListEntry{Status: "REPEATING", Progress: 1, Repeat: 2, CompletedAt: someDate}, MovieEpisode,
			want{ok: true, status: "COMPLETED", progress: 1, repeat: 3}},
		{"a movie's one episode, whatever the key", movie, nil, 1005, want{ok: true, status: "COMPLETED", progress: 1, started: true, completed: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.entry != nil {
				tc.entry.MediaID = tc.media.ID
			}
			u, ok := progressUpdate(tc.entry, tc.media, tc.key, now)
			if ok != tc.want.ok {
				t.Fatalf("ok = %v, want %v (%+v)", ok, tc.want.ok, u)
			}
			if !ok {
				return
			}
			if u.MediaID != tc.media.ID || u.Status == nil || u.Progress == nil {
				t.Fatalf("incomplete update %+v", u)
			}
			if *u.Status != tc.want.status || *u.Progress != tc.want.progress {
				t.Errorf("got %s at %d, want %s at %d", *u.Status, *u.Progress, tc.want.status, tc.want.progress)
			}
			repeat := 0
			if u.Repeat != nil {
				repeat = *u.Repeat
			}
			if repeat != tc.want.repeat {
				t.Errorf("repeat = %d, want %d", repeat, tc.want.repeat)
			}
			if (u.StartedAt != nil) != tc.want.started {
				t.Errorf("start date set = %v, want %v", u.StartedAt != nil, tc.want.started)
			}
			if (u.CompletedAt != nil) != tc.want.completed {
				t.Errorf("finish date set = %v, want %v", u.CompletedAt != nil, tc.want.completed)
			}
			for _, d := range []*FuzzyDate{u.StartedAt, u.CompletedAt} {
				if d != nil && (*d.Year != 2026 || *d.Month != 10 || *d.Day != 6) {
					t.Errorf("date = %d-%d-%d, want today", *d.Year, *d.Month, *d.Day)
				}
			}
		})
	}
}

// End to end on the list: watching an earlier episode of a planned show
// with recorded progress starts it without losing that progress; the last
// episode completes it, and OnCompleted hears of it.
func TestUpdateProgressLocalList(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	show := testShow(1, "FINISHED", 6, 6)
	addLocal(t, p, show, "PLANNING", 1005)
	completed := make(chan int, 4)
	p.OnCompleted = func(id int) { completed <- id }

	if ok, err := p.UpdateProgress(ctx, show.ID, 1002); err != nil || !ok {
		t.Fatalf("UpdateProgress = %v, %v", ok, err)
	}
	if r := readLocal(t, p, show.ID); r.Status != "CURRENT" || r.Progress != 1005 || r.StartedAt.Year == nil {
		t.Fatalf("after S01E02: %+v, want CURRENT at S01E05, started", r)
	}
	if ok, err := p.UpdateProgress(ctx, show.ID, 1004); err != nil || ok {
		t.Fatalf("an earlier episode changed the list: %v, %v", ok, err)
	}
	if ok, err := p.UpdateProgress(ctx, show.ID, 2006); err != nil || !ok {
		t.Fatalf("UpdateProgress = %v, %v", ok, err)
	}
	if r := readLocal(t, p, show.ID); r.Status != "COMPLETED" || r.Progress != 2006 || r.CompletedAt.Year == nil {
		t.Fatalf("after the last episode: %+v, want COMPLETED at S02E06", r)
	}
	select {
	case id := <-completed:
		if id != show.ID {
			t.Errorf("OnCompleted(%d), want %d", id, show.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnCompleted wasn't called")
	}

	// A movie watched to the end, then again: each viewing is told.
	movie := testMovie(5)
	p.db.SetCache(liteKey(movie.ID), movie, time.Hour)
	for i := 1; i <= 2; i++ {
		if ok, err := p.UpdateProgress(ctx, movie.ID, MovieEpisode); err != nil || !ok {
			t.Fatalf("viewing %d: %v, %v", i, ok, err)
		}
		if id := <-completed; id != movie.ID {
			t.Errorf("OnCompleted(%d), want the movie", id)
		}
	}
	if r := readLocal(t, p, movie.ID); r.Status != "COMPLETED" || r.Progress != 1 || r.Repeat != 1 {
		t.Fatalf("movie watched twice: %+v", r)
	}
	select {
	case id := <-completed:
		t.Errorf("OnCompleted(%d) once too often", id)
	default:
	}
}

func TestWatchingUpdate(t *testing.T) {
	now := time.Date(2026, time.October, 6, 20, 30, 0, 0, time.Local)
	someDate := FuzzyDate{Year: ptr(2025)}
	e := func(status string, progress int) *ListEntry {
		return &ListEntry{MediaID: 1, Status: status, Progress: progress}
	}
	cases := []struct {
		name    string
		entry   *ListEntry
		ok      bool // moved to CURRENT
		started bool // start date set
	}{
		{"not in list", nil, true, true},
		{"planned show", e("PLANNING", 0), true, true},
		{"planned show with progress", e("PLANNING", 1005), true, true},
		{"paused show", e("PAUSED", 1003), true, false},
		{"paused before the first episode", e("PAUSED", 0), true, true},
		{"dropped show", e("DROPPED", 1004), true, false},
		{"start date kept", &ListEntry{MediaID: 1, Status: "PLANNING", StartedAt: someDate}, true, false},
		{"watching", e("CURRENT", 1002), false, false},
		{"watching, nothing watched yet", e("CURRENT", 0), false, false},
		{"rewatching", e("REPEATING", 1003), false, false},
		{"completed", e("COMPLETED", 2006), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := watchingUpdate(tc.entry, 1, now)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if u.MediaID != 1 || u.Status == nil || *u.Status != "CURRENT" {
				t.Fatalf("update %+v, want status CURRENT", u)
			}
			if u.Progress != nil || u.Score != nil || u.Repeat != nil || u.CompletedAt != nil {
				t.Errorf("update %+v changes more than the status and start date", u)
			}
			if (u.StartedAt != nil) != tc.started {
				t.Errorf("start date set = %v, want %v", u.StartedAt != nil, tc.started)
			}
			if d := u.StartedAt; d != nil && (*d.Year != 2026 || *d.Month != 10 || *d.Day != 6) {
				t.Errorf("start date = %d-%d-%d, want today", *d.Year, *d.Month, *d.Day)
			}
		})
	}
}

func TestFillDates(t *testing.T) {
	now := time.Date(2026, time.October, 6, 20, 30, 0, 0, time.Local)
	someDate := FuzzyDate{Year: ptr(2025)}
	e := func(status string, progress int) *ListEntry {
		return &ListEntry{MediaID: 1, Status: status, Progress: progress}
	}
	cases := []struct {
		name               string
		entry              *ListEntry
		status             string // "": the update keeps the status
		given              bool   // the update has its own dates
		started, completed bool   // dates set to today
	}{
		{"marked watched, not in list", nil, "COMPLETED", false, true, true},
		{"marked watched from the watchlist", e("PLANNING", 0), "COMPLETED", false, true, true},
		{"show finished", e("CURRENT", 2005), "COMPLETED", false, false, true},
		{"finish date kept", &ListEntry{MediaID: 1, Status: "CURRENT", Progress: 1003, CompletedAt: someDate}, "COMPLETED", false, false, false},
		{"put on Watching", nil, "CURRENT", false, true, false},
		{"watchlist to Watching", e("PLANNING", 0), "CURRENT", false, true, false},
		{"start date kept", &ListEntry{MediaID: 1, Status: "PLANNING", StartedAt: someDate}, "CURRENT", false, false, false},
		{"paused show resumed", e("PAUSED", 1003), "CURRENT", false, false, false},
		{"put on the watchlist", nil, "PLANNING", false, false, false},
		{"dropped", e("CURRENT", 1003), "DROPPED", false, false, false},
		{"watched already", e("COMPLETED", 1), "COMPLETED", false, false, false},
		{"rated: no status", e("CURRENT", 0), "", false, false, false},
		{"dates of the review dialog", nil, "COMPLETED", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := EntryUpdate{MediaID: 1}
			if tc.status != "" {
				u.Status = &tc.status
			}
			if tc.given {
				u.StartedAt, u.CompletedAt = &someDate, &FuzzyDate{}
			}
			FillDates(&u, tc.entry, now)
			isToday := func(d *FuzzyDate) bool {
				return d != nil && d.Year != nil && *d.Year == 2026 && *d.Month == 10 && *d.Day == 6
			}
			if isToday(u.StartedAt) != tc.started || isToday(u.CompletedAt) != tc.completed {
				t.Errorf("started today %v, completed today %v; want %v, %v", isToday(u.StartedAt), isToday(u.CompletedAt), tc.started, tc.completed)
			}
			if !tc.started && !tc.given && u.StartedAt != nil || !tc.completed && !tc.given && u.CompletedAt != nil {
				t.Errorf("update %+v sets a date", u)
			}
		})
	}
}

// End to end on the list: starting a show puts it on the watching list
// keeping its progress, and tells the UI; a completed one stays so.
func TestStartWatchingLocalList(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	planned, fresh, done := testShow(1, "FINISHED", 12), testShow(2, "FINISHED", 12), testShow(3, "FINISHED", 12)
	p.db.SetCache(liteKey(fresh.ID), fresh, time.Hour)
	addLocal(t, p, planned, "PLANNING", 1005)
	addLocal(t, p, done, "COMPLETED", 1012)
	updates, unsubscribe := p.hub.Subscribe()
	defer unsubscribe()

	if ok, err := p.StartWatching(ctx, planned.ID); err != nil || !ok {
		t.Fatalf("StartWatching(planned) = %v, %v", ok, err)
	}
	if r := readLocal(t, p, planned.ID); r.Status != "CURRENT" || r.Progress != 1005 {
		t.Fatalf("planned show after starting it: %+v, want CURRENT at S01E05", r)
	}
	select {
	case raw := <-updates:
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				MediaID int `json:"mediaId"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil || ev.Type != events.CollectionUpdate || ev.Payload.MediaID != planned.ID {
			t.Errorf("event %s, want %s for the show", raw, events.CollectionUpdate)
		}
	default:
		t.Error("no event: the UI wouldn't show the change")
	}
	if ok, err := p.StartWatching(ctx, planned.ID); err != nil || ok {
		t.Fatalf("starting it again changed the list: %v, %v", ok, err)
	}

	if ok, err := p.StartWatching(ctx, fresh.ID); err != nil || !ok {
		t.Fatalf("StartWatching(not in list) = %v, %v", ok, err)
	}
	if r := readLocal(t, p, fresh.ID); r.Status != "CURRENT" || r.Progress != 0 || r.Media.ID != fresh.ID {
		t.Fatalf("new entry: %+v, want CURRENT at 0", r)
	}

	if ok, err := p.StartWatching(ctx, done.ID); err != nil || ok {
		t.Fatalf("StartWatching(completed) = %v, %v; want no change", ok, err)
	}
	if r := readLocal(t, p, done.ID); r.Status != "COMPLETED" || r.Progress != 1012 {
		t.Fatalf("completed show after starting it again: %+v", r)
	}
}

// Starting and finishing a planned movie, one after the other in either
// order (the player serializes them), completes it.
func TestStartWatchingAndFinishing(t *testing.T) {
	for _, startFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("start first %v", startFirst), func(t *testing.T) {
			p := newTestPlatform(t, nil)
			ctx := context.Background()
			movie := testMovie(1)
			addLocal(t, p, movie, "PLANNING", 0)

			steps := []func() error{
				func() error { _, err := p.StartWatching(ctx, movie.ID); return err },
				func() error { _, err := p.UpdateProgress(ctx, movie.ID, MovieEpisode); return err },
			}
			if !startFirst {
				steps[0], steps[1] = steps[1], steps[0]
			}
			for _, step := range steps {
				if err := step(); err != nil {
					t.Fatal(err)
				}
			}
			if r := readLocal(t, p, movie.ID); r.Status != "COMPLETED" || r.Progress != 1 {
				t.Fatalf("got %+v, want COMPLETED at 1", r)
			}
		})
	}
}

// Progress updates made at the same time (two players, a sync) each count:
// none is lost to another's read-modify-write.
func TestConcurrentProgress(t *testing.T) {
	p := newTestPlatform(t, nil)
	show := testShow(1, "RELEASING", 50)
	addLocal(t, p, show, "CURRENT", 0)
	var wg sync.WaitGroup
	for ep := 1; ep <= 20; ep++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.UpdateProgress(context.Background(), show.ID, EpisodeKey(1, ep)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if r := readLocal(t, p, show.ID); r.Progress != EpisodeKey(1, 20) {
		t.Fatalf("progress %d, want S01E20", r.Progress)
	}
}
