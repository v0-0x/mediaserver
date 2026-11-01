package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/events"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

type fakeList struct{ coll *anilist.Collection }

func (f *fakeList) Collection(context.Context, string, bool) (*anilist.Collection, error) {
	return f.coll, nil
}

func (f *fakeList) MediaLite(_ context.Context, id int) (*anilist.Media, error) {
	m := &anilist.Media{ID: id}
	m.Title.English = map[int]string{1: "Frieren", 2: "Dungeon Meshi"}[id]
	return m, nil
}

func fixture(t *testing.T) (*Service, *fakeList, func() []Alert) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
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

func entry(id, progress, next int, airingAt int64, status string) *anilist.ListEntry {
	m := &anilist.Media{ID: id, NextAiringEpisode: &anilist.AiringEpisode{Episode: next, AiringAt: int(airingAt)}}
	m.Title.English = map[int]string{1: "Frieren", 2: "Dungeon Meshi"}[id]
	return &anilist.ListEntry{MediaID: id, Status: status, Progress: progress, Media: m}
}

// An anime is noted the first time; an episode out after that is told
// about (its airing time passed, also with the list from before), not one
// already watched, nor with the setting off.
func TestCheckAired(t *testing.T) {
	s, list, got := fixture(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	hour := int64(3600)
	list.coll = &anilist.Collection{Lists: []*anilist.List{{Status: "CURRENT", Entries: []*anilist.ListEntry{
		entry(1, 4, 6, now.Unix()+hour, "CURRENT"), // 5 out, 6 in an hour
		entry(2, 2, 3, now.Unix()+hour, "CURRENT"), // 2 out, watched
	}}}}
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 0 {
		t.Fatalf("told about anime seen the first time: %+v", a)
	}
	// Two hours on: Frieren 6 is out (the list still says it's next).
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
		case 1:
			if x.Kind != "aired" || x.Episode != 6 || x.Title != "Frieren" || x.Body != "Episode 6 is out." {
				t.Errorf("frieren: %+v", x)
			}
		case 2:
			if x.Episode != 3 {
				t.Errorf("dungeon meshi: %+v", x)
			}
		}
	}
	// Once only.
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 0 {
		t.Fatalf("told twice: %+v", a)
	}
	// Watched as it came out (8 out, progress 8): not told; nor with the
	// setting off.
	list.coll.Lists[0].Entries = []*anilist.ListEntry{entry(1, 8, 8, now.Unix()-60, "CURRENT")}
	_ = s.CheckAired(context.Background(), false)
	if len(got()) != 0 {
		t.Error("told about an episode already watched")
	}
	cfg := s.settings.Get()
	cfg.Notify.NewEpisodes = false
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	list.coll.Lists[0].Entries = []*anilist.ListEntry{entry(1, 8, 9, now.Unix()-60, "CURRENT")}
	_ = s.CheckAired(context.Background(), false)
	if len(got()) != 0 {
		t.Error("told with the setting off")
	}
}

func TestReady(t *testing.T) {
	s, _, got := fixture(t)
	file := func(id, ep int) *library.LocalFile {
		return &library.LocalFile{Path: "/a/" + string(rune('a'+id)) + string(rune('0'+ep)), MediaID: id, Episode: ep, Kind: "main"}
	}
	s.NewFiles([]*library.LocalFile{file(1, 3), file(1, 1), file(1, 2), file(2, 5), {Path: "/x", Kind: "main"}})
	a := got()
	if len(a) != 2 {
		t.Fatalf("alerts: %+v", a)
	}
	if a[0].Title != "Frieren" || a[0].Body != "Episodes 1–3 are in your library." || a[0].Path != "" {
		t.Errorf("several: %+v", a[0])
	}
	if a[1].Title != "Dungeon Meshi" || a[1].Path != "/a/c5" || !strings.Contains(a[1].Body, "Episode 5") {
		t.Errorf("one: %+v", a[1])
	}
	// Lots of anime at once (a new folder): how many.
	var many []*library.LocalFile
	for id := 1; id <= 5; id++ {
		many = append(many, file(id, 1))
	}
	s.NewFiles(many)
	if a := got(); len(a) != 1 || a[0].Body != "5 episodes of 5 anime." {
		t.Errorf("summary: %+v", a)
	}
	if !s.Downloaded(1, 4, "frieren", "/a/b4") {
		t.Error("a download not told about")
	}
	if a := got(); len(a) != 1 || a[0].Title != "Frieren" || a[0].Path != "/a/b4" {
		t.Errorf("download: %+v", a)
	}
	cfg := s.settings.Get()
	cfg.Notify.Downloads = false
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if s.Downloaded(1, 5, "x", "") {
		t.Error("told with the setting off")
	}
	s.NewFiles([]*library.LocalFile{file(1, 6)})
	if len(got()) != 0 {
		t.Error("new files told with the setting off")
	}
}

// The last episode out: the list has no next one (the anime has finished),
// all its episodes are out, and the finale is told about.
func TestCheckAiredFinale(t *testing.T) {
	s, list, got := fixture(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	list.coll = &anilist.Collection{Lists: []*anilist.List{{Status: "CURRENT", Entries: []*anilist.ListEntry{entry(1, 10, 12, now.Unix()+3600, "CURRENT")}}}}
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	got()
	twelve := 12
	m := &anilist.Media{ID: 1, Status: "FINISHED", Episodes: &twelve}
	m.Title.English = "Frieren"
	list.coll.Lists[0].Entries = []*anilist.ListEntry{{MediaID: 1, Status: "CURRENT", Progress: 11, Media: m}}
	now = now.Add(2 * time.Hour)
	if err := s.CheckAired(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if a := got(); len(a) != 1 || a[0].Episode != 12 || a[0].Body != "Episode 12 is out." {
		t.Fatalf("finale: %+v", a)
	}
	// A finished anime seen the first time: nothing to tell.
	m2 := &anilist.Media{ID: 2, Status: "FINISHED", Episodes: &twelve}
	list.coll.Lists[0].Entries = append(list.coll.Lists[0].Entries, &anilist.ListEntry{MediaID: 2, Status: "CURRENT", Progress: 3, Media: m2})
	_ = s.CheckAired(context.Background(), false)
	if a := got(); len(a) != 0 {
		t.Fatalf("told about an old anime: %+v", a)
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
	path := filepath.Join(t.TempDir(), "Frieren - 07.mkv")
	if err := os.WriteFile(path, []byte("part"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	s.NewFiles([]*library.LocalFile{{Path: path, MediaID: 1, Episode: 7, Kind: "main", Size: st.Size(), ModTime: st.ModTime().Unix()}})
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
	if len(a) != 1 || a[0].Path != path || a[0].Episode != 7 {
		t.Fatalf("not told when done: %+v", a)
	}
}
