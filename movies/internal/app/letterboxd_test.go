package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/history"
)

const testTMDBKey = "0123456789abcdef0123456789abcdef"

// newTestApp is an app whose TMDB is a stand-in serving the catalog
// package's fixtures (/movie/155 is movie_155.json).
func newTestApp(t *testing.T) *App {
	t.Helper()
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "_") + ".json"
		raw, err := os.ReadFile(filepath.Join("..", "catalog", "testdata", name))
		if err != nil || r.URL.Query().Get("api_key") != testTMDBKey {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(tmdb.Close)
	t.Setenv("KUMOBOX_TMDB_URL", tmdb.URL)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	cfg := a.Settings.Get()
	cfg.TMDB.APIKey = testTMDBKey
	if _, err := a.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return a
}

func diaryCount(t *testing.T, a *App, mediaID int) int {
	t.Helper()
	d, err := a.Platform.Diary(context.Background(), mediaID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(d)
}

// waitDiary waits for mediaID's diary to have want entries (the player's
// completions are logged in the background), and checks it stays so.
func waitDiary(t *testing.T, a *App, mediaID, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for diaryCount(t, a, mediaID) != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a second entry would be there by now
	if got := diaryCount(t, a, mediaID); got != want {
		t.Fatalf("media %d: %d diary entries, want %d", mediaID, got, want)
	}
}

func status(t *testing.T, a *App, mediaID int) string {
	t.Helper()
	coll, err := a.Platform.Collection(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := coll.Find(mediaID); e != nil {
		return e.Status
	}
	return ""
}

// A movie watched to the end is logged in the diary once, whichever way it
// became Watched.
func TestCompletedMovieLogged(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// Played to the end (the player moves the progress on).
	if changed, err := a.Platform.UpdateProgress(ctx, 155, catalog.MovieEpisode); err != nil || !changed {
		t.Fatalf("progress: %v %v", changed, err)
	}
	if s := status(t, a, 155); s != "COMPLETED" {
		t.Fatalf("status %q", s)
	}
	waitDiary(t, a, 155, 1)
	d, _ := a.Platform.Diary(ctx, 155, 0)
	if e := d[0]; e.Source != catalog.DiaryKumoBox || e.WatchedDate != time.Now().Format("2006-01-02") || e.Rewatch || e.ExportedAt != 0 {
		t.Errorf("entry: %+v", e)
	}

	// Edited (rated) afterwards, and marked watched again the same day: no
	// second entry, and the rating goes to the one logged.
	done, score, liked := "COMPLETED", 80.0, true
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Status: &done, Score: &score, Liked: &liked}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Platform.UpdateProgress(ctx, 155, catalog.MovieEpisode); err != nil {
		t.Fatal(err)
	}
	waitDiary(t, a, 155, 1)
	if d, _ := a.Platform.Diary(ctx, 155, 0); d[0].Rating != 80 || !d[0].Liked {
		t.Errorf("rated afterwards: %+v", d[0])
	}
	// A review written afterwards goes with it too.
	review := "  Why so serious?  "
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Notes: &review}); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.Platform.Diary(ctx, 155, 0); d[0].Review != "Why so serious?" {
		t.Errorf("reviewed afterwards: %q", d[0].Review)
	}
	// Notes sent back unchanged with another edit (as the list editor does)
	// don't overwrite a review written on the diary entry itself.
	if err := a.Platform.ReviewDiary(ctx, d[0].ID, "Edited in the diary"); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Score: &score, Notes: &review}); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.Platform.Diary(ctx, 155, 0); d[0].Review != "Edited in the diary" {
		t.Errorf("unchanged notes: %q", d[0].Review)
	}
	// Once on Letterboxd, it's left as it was sent.
	if err := a.Letterboxd.MarkExported(ctx, []int64{d[0].ID}); err != nil {
		t.Fatal(err)
	}
	score = 60
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Score: &score}); err != nil {
		t.Fatal(err)
	}
	review = "Changed my mind"
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Score: &score, Notes: &review}); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.Platform.Diary(ctx, 155, 0); d[0].Rating != 80 || d[0].Review != "Edited in the diary" {
		t.Errorf("edited after sending: %+v", d[0])
	}

	// Put on the Watched list by hand: logged once.
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 27205, Status: &done}); err != nil {
		t.Fatal(err)
	}
	waitDiary(t, a, 27205, 1)

	// Shows aren't logged.
	show := catalog.ShowID(1396)
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: show, Status: &done}); err != nil {
		t.Fatal(err)
	}
	waitDiary(t, a, show, 0)
}

// Logging can be turned off, and what another KumoBox on the account
// finished is logged there, not here.
func TestCompletedMovieNotLogged(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	a.progressFromHistory([]history.Entry{{MediaID: 155, Episode: catalog.MovieEpisode, UpdatedAt: time.Now().Unix()}})
	if s := status(t, a, 155); s != "COMPLETED" {
		t.Fatalf("status %q", s)
	}
	waitDiary(t, a, 155, 0)

	cfg := a.Settings.Get()
	cfg.Letterboxd.LogWatched = false
	if _, err := a.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Platform.UpdateProgress(ctx, 27205, catalog.MovieEpisode); err != nil {
		t.Fatal(err)
	}
	waitDiary(t, a, 27205, 0)
	// Played here after all: logged (the mark of the other KumoBox's
	// completion was taken).
	cfg.Letterboxd.LogWatched = true
	if _, err := a.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Platform.UpdateProgress(ctx, 155, catalog.MovieEpisode); err != nil {
		t.Fatal(err)
	}
	waitDiary(t, a, 155, 1)
}

// Marked watched by hand, a movie gets the dates playing it to the end
// gives it, as its diary entry does.
func TestMarkedWatchedDates(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	done := "COMPLETED"
	if err := a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: 155, Status: &done}); err != nil {
		t.Fatal(err)
	}
	coll, err := a.Platform.Collection(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e := coll.Find(155)
	y, m, d := time.Now().Date()
	if e == nil || e.CompletedAt.Year == nil || *e.CompletedAt.Year != y || *e.CompletedAt.Month != int(m) || *e.CompletedAt.Day != d || e.StartedAt.Year == nil {
		t.Fatalf("entry %+v, want watched today", e)
	}
}
