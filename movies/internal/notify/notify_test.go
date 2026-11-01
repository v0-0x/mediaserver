package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

// Two shows and a movie.
var (
	severance = catalog.ShowID(95396)
	andor     = catalog.ShowID(83867)
	dune      = catalog.MovieID(693134)
	names     = map[int]string{severance: "Severance", andor: "Andor", dune: "Dune: Part Two"}
)

type fakeList struct{ coll *catalog.Collection }

func (f *fakeList) Collection(context.Context, string, bool) (*catalog.Collection, error) {
	return f.coll, nil
}

func (f *fakeList) MediaLite(_ context.Context, id int) (*catalog.Media, error) {
	m := &catalog.Media{ID: id}
	m.Title.English = names[id]
	return m, nil
}

func fixture(t *testing.T) (*Service, *fakeList, func() []Alert) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	ch, stop := hub.Subscribe()
	t.Cleanup(stop)
	list := &fakeList{}
	s := New(settings, hub, d, list)
	got := func() []Alert {
		var out []Alert
		for {
			select {
			case b := <-ch:
				var ev struct {
					Type    string `json:"type"`
					Payload Alert  `json:"payload"`
				}
				if json.Unmarshal(b, &ev) == nil && ev.Type == Event {
					out = append(out, ev.Payload)
				}
			case <-time.After(100 * time.Millisecond):
				return out
			}
		}
	}
	return s, list, got
}

// entry is a show on the list, watched up to progress, its last aired
// episode latest and next one to air next at airingAt (keys).
func entry(id, progress, latest, next int, airingAt int64, status string) *catalog.ListEntry {
	m := &catalog.Media{ID: id, Type: catalog.TypeTV, LatestEpisode: latest, NextAiringEpisode: &catalog.AiringEpisode{Episode: next, AiringAt: int(airingAt)}}
	m.Title.English = names[id]
	return &catalog.ListEntry{MediaID: id, Status: status, Progress: progress, Media: m}
}

func key(s, e int) int { return catalog.EpisodeKey(s, e) }

// A show is noted the first time; an episode out after that is told
// about (its air date passed, also with the list from before), not one
// already watched, nor with the setting off; a movie never.
func TestCheckAired(t *testing.T) {
	s, list, got := fixture(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	hour := int64(3600)
	film := &catalog.Media{ID: dune, Type: catalog.TypeMovie}
	list.coll = &catalog.Collection{Lists: []*catalog.List{{Status: "CURRENT", Entries: []*catalog.ListEntry{
		entry(severance, key(2, 4), key(2, 5), key(2, 6), now.Unix()+hour, "CURRENT"), // S02E05 out, S02E06 in an hour
		entry(andor, key(1, 2), key(1, 2), key(1, 3), now.Unix()+hour, "CURRENT"),     // S01E02 out, watched
		{MediaID: dune, Status: "CURRENT", Media: film},
	}}}}
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 0 {
		t.Fatalf("told about shows seen the first time: %+v", a)
	}
	// Two hours on: Severance S02E06 is out (the list still says it's next).
	now = now.Add(2 * time.Hour)
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	a := got()
	if len(a) != 2 {
		t.Fatalf("alerts: %+v", a)
	}
	for _, x := range a {
		switch x.MediaID {
		case severance:
			if x.Kind != "aired" || x.Episode != key(2, 6) || x.Title != "Severance" || x.Body != "S02E06 is out." {
				t.Errorf("severance: %+v", x)
			}
		case andor:
			if x.Episode != key(1, 3) {
				t.Errorf("andor: %+v", x)
			}
		default:
			t.Errorf("not a show's: %+v", x)
		}
	}
	// Once only.
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 0 {
		t.Fatalf("told twice: %+v", a)
	}
	// Watched as it came out (S02E08 out, progress S02E08): not told; nor
	// with the setting off.
	list.coll.Lists[0].Entries = []*catalog.ListEntry{entry(severance, key(2, 8), key(2, 7), key(2, 8), now.Unix()-60, "CURRENT")}
	_ = s.CheckAired(context.Background(), false)
	if len(got()) != 0 {
		t.Error("told about an episode already watched")
	}
	cfg := s.settings.Get()
	cfg.Notify.NewEpisodes = false
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	list.coll.Lists[0].Entries = []*catalog.ListEntry{entry(severance, key(2, 8), key(2, 8), key(2, 9), now.Unix()-60, "CURRENT")}
	_ = s.CheckAired(context.Background(), false)
	if len(got()) != 0 {
		t.Error("told with the setting off")
	}
}

func TestReady(t *testing.T) {
	s, _, got := fixture(t)
	file := func(id, ep int) *library.LocalFile {
		return &library.LocalFile{Path: fmt.Sprintf("/a/%d-%d", id, ep), MediaID: id, Episode: ep, Kind: "main"}
	}
	s.NewFiles([]*library.LocalFile{file(severance, key(1, 3)), file(severance, key(1, 1)), file(severance, key(1, 2)), file(severance, key(2, 1)),
		file(andor, key(1, 5)), file(dune, catalog.MovieEpisode), {Path: "/x", Kind: "main"}})
	a := got()
	if len(a) != 3 {
		t.Fatalf("alerts: %+v", a)
	}
	if a[0].Title != "Severance" || a[0].Body != "S01E01–E03, S02E01 are in your library." || a[0].Path != "" {
		t.Errorf("several: %+v", a[0])
	}
	if a[1].Title != "Andor" || a[1].Path != fmt.Sprintf("/a/%d-%d", andor, key(1, 5)) || !strings.Contains(a[1].Body, "S01E05") {
		t.Errorf("one: %+v", a[1])
	}
	if a[2].Title != "Dune: Part Two" || a[2].Body != "It's in your library." || a[2].Path == "" {
		t.Errorf("a movie: %+v", a[2])
	}
	// Lots of titles at once (a new folder): how many.
	var many []*library.LocalFile
	for id := 1; id <= 5; id++ {
		many = append(many, file(catalog.MovieID(id), catalog.MovieEpisode))
	}
	s.NewFiles(many)
	if a := got(); len(a) != 1 || a[0].Body != "5 files of 5 titles." {
		t.Errorf("summary: %+v", a)
	}
	if !s.Downloaded(severance, key(1, 4), "severance", "/a/b4") {
		t.Error("a download not told about")
	}
	if a := got(); len(a) != 1 || a[0].Title != "Severance" || a[0].Path != "/a/b4" || a[0].Body != "S01E04 is downloaded." {
		t.Errorf("download: %+v", a)
	}
	cfg := s.settings.Get()
	cfg.Notify.Downloads = false
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if s.Downloaded(severance, key(1, 5), "x", "") {
		t.Error("told with the setting off")
	}
	s.NewFiles([]*library.LocalFile{file(severance, key(1, 6))})
	if len(got()) != 0 {
		t.Error("new files told with the setting off")
	}
}

// A show's last episode out: the list has no next one any more, its last
// aired one says it.
func TestCheckAiredFinale(t *testing.T) {
	s, list, got := fixture(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	list.coll = &catalog.Collection{Lists: []*catalog.List{{Status: "CURRENT", Entries: []*catalog.ListEntry{entry(severance, key(1, 8), key(1, 8), key(1, 9), now.Unix()+3600, "CURRENT")}}}}
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	got()
	m := &catalog.Media{ID: severance, Type: catalog.TypeTV, Status: "FINISHED", LatestEpisode: key(1, 9)}
	m.Title.English = "Severance"
	list.coll.Lists[0].Entries = []*catalog.ListEntry{{MediaID: severance, Status: "CURRENT", Progress: key(1, 8), Media: m}}
	now = now.Add(2 * time.Hour)
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 1 || a[0].Episode != key(1, 9) || a[0].Body != "S01E09 is out." {
		t.Fatalf("finale: %+v", a)
	}
	// A finished show seen the first time: nothing to tell.
	m2 := &catalog.Media{ID: andor, Type: catalog.TypeTV, Status: "FINISHED", LatestEpisode: key(2, 12)}
	list.coll.Lists[0].Entries = append(list.coll.Lists[0].Entries, &catalog.ListEntry{MediaID: andor, Status: "CURRENT", Progress: key(1, 3), Media: m2})
	_ = s.CheckAired(context.Background(), false)
	if a := got(); len(a) != 0 {
		t.Fatalf("told about an old show: %+v", a)
	}
}

// A file being written (a torrent downloading into the library folder) is
// told about once it stops changing, not when it appears.
func TestReadyWhenWritten(t *testing.T) {
	s, _, got := fixture(t)
	s.poll = 20 * time.Millisecond
	var now atomic.Int64 // read by the looking at it again, meanwhile
	now.Store(time.Now().UnixNano())
	s.now = func() time.Time { return time.Unix(0, now.Load()) }
	path := filepath.Join(t.TempDir(), "Severance S01E07.mkv")
	if err := os.WriteFile(path, []byte("part"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	s.NewFiles([]*library.LocalFile{{Path: path, MediaID: severance, Episode: key(1, 7), Kind: "main", Size: st.Size(), ModTime: st.ModTime().Unix()}})
	if a := got(); len(a) != 0 {
		t.Fatalf("told while being written: %+v", a)
	}
	// Still growing.
	if err := os.WriteFile(path, []byte("partial, more of it"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 0 {
		t.Fatalf("told while growing: %+v", a)
	}
	// Done: unchanged for a while.
	now.Add(int64(5 * time.Minute))
	time.Sleep(60 * time.Millisecond)
	a := got()
	if len(a) != 1 || a[0].Path != path || a[0].Episode != key(1, 7) {
		t.Fatalf("not told when done: %+v", a)
	}
}
