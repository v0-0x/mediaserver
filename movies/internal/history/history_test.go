package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/db"
)

func TestMergeKeepsTheNewest(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := NewStore(d)
	if err := s.Save(Entry{MediaID: 1, Episode: 2, Position: 100, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	here := s.Get(1, 2).UpdatedAt

	// Older, from another KumoBox: this one's stays.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 900, Duration: 1400, Source: "local", UpdatedAt: here - 60}); ok {
		t.Error("an older position replaced a newer one")
	}
	// Newer: taken, with its time.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 700, Duration: 1400, Source: "local", UpdatedAt: here + 60}); !ok {
		t.Error("a newer position wasn't taken")
	}
	if e := s.Get(1, 2); e.Position != 700 || e.UpdatedAt != here+60 {
		t.Errorf("entry %+v", e)
	}
	// One this KumoBox hadn't.
	if ok, _ := s.Merge(Entry{MediaID: 5, Episode: 1, Position: 300, Duration: 1400, UpdatedAt: here + 120}); !ok {
		t.Error("a new entry wasn't taken")
	}
	if got := s.Since(here+60, 10); len(got) != 1 || got[0].MediaID != 5 {
		t.Errorf("since: %+v", got)
	}
	if got := s.Since(0, 10); len(got) != 2 || got[0].UpdatedAt > got[1].UpdatedAt {
		t.Errorf("since 0, oldest first: %+v", got)
	}
	// From the future: taken as from now, so what's saved here next wins.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 5, Duration: 1400, UpdatedAt: here + 10*365*24*3600}); !ok {
		t.Error("a newer position wasn't taken")
	}
	if e := s.Get(1, 2); e.UpdatedAt > time.Now().Add(maxAhead).Unix() {
		t.Errorf("a time from the future was kept: %+v", e)
	}
	if ok, _ := s.Merge(Entry{MediaID: 0, Episode: 1, UpdatedAt: here}); ok {
		t.Error("a bad entry was taken")
	}
}

// Positions are handed out in the order they were stored, whatever their
// time: one merged late from another KumoBox, with an older time, still comes
// after what was asked for already.
func TestAfterIsTheStoredOrder(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := NewStore(d)
	for ep := 1; ep <= 3; ep++ {
		if err := s.Save(Entry{MediaID: 1, Episode: ep, Position: 100, Duration: 1400, Source: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	got := s.After(0, 10)
	if len(got) != 3 || got[0].Episode != 1 || got[2].Episode != 3 || got[0].Seq >= got[1].Seq || got[1].Seq >= got[2].Seq {
		t.Fatalf("after 0: %+v", got)
	}
	last := got[2].Seq
	if more := s.After(last, 10); len(more) != 0 {
		t.Fatalf("after the last: %+v", more)
	}

	// From another KumoBox, watched an hour ago: still new to whoever asks.
	merged, err := s.MergeAll([]Entry{
		{MediaID: 2, Episode: 1, Position: 50, Duration: 1400, UpdatedAt: time.Now().Add(-time.Hour).Unix()},
		{MediaID: 2, Episode: 2, Position: 60, Duration: 1400, UpdatedAt: time.Now().Add(-time.Hour).Unix()},
		{MediaID: 0, Episode: 1, UpdatedAt: 1}, // bad: left out
	})
	if err != nil || len(merged) != 2 {
		t.Fatalf("merged %+v %v", merged, err)
	}
	more := s.After(last, 10)
	if len(more) != 2 || more[0].MediaID != 2 || more[0].Seq <= last {
		t.Fatalf("a late entry wasn't handed out: %+v", more)
	}
	if lim := s.After(0, 2); len(lim) != 2 || lim[0].Episode != 1 {
		t.Fatalf("limit: %+v", lim)
	}
	// Latest: the last stored first.
	if l := s.Latest(2); len(l) != 2 || l[0].MediaID != 2 || l[0].Episode != 2 || l[1].Episode != 1 {
		t.Fatalf("latest: %+v", l)
	}
	// Saved again: moves to the end.
	if err := s.Save(Entry{MediaID: 1, Episode: 1, Position: 200, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if l := s.Latest(1); len(l) != 1 || l[0].MediaID != 1 || l[0].Episode != 1 || l[0].Position != 200 {
		t.Fatalf("latest after a save: %+v", l)
	}
	// An older one merged changes nothing, and isn't handed out again.
	top := s.Latest(1)[0].Seq
	if merged, _ := s.MergeAll([]Entry{{MediaID: 1, Episode: 1, Position: 10, Duration: 1400, UpdatedAt: 1}}); len(merged) != 0 {
		t.Fatalf("an older position was taken: %+v", merged)
	}
	if more := s.After(top, 10); len(more) != 0 {
		t.Fatalf("handed out again: %+v", more)
	}
}

// A database from before the stored order: its positions get one, by
// their time, and new ones come after.
func TestSeqForOlderDatabases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kumobox.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// As an older KumoBox left it: no seq column.
	for _, q := range []string{
		`DROP INDEX idx_watch_history_seq`,
		`ALTER TABLE watch_history DROP COLUMN seq`,
		`INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at) VALUES(1, 1, 100, 1400, 'local', 1000), (1, 2, 100, 1400, 'local', 2000)`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	_ = d.Close()
	if d, err = db.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := NewStore(d)
	got := s.After(0, 10)
	if len(got) != 2 || got[0].Seq != 1000 || got[1].Seq != 2000 {
		t.Fatalf("after the migration: %+v", got)
	}
	if err := s.Save(Entry{MediaID: 3, Episode: 1, Position: 100, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if l := s.Latest(1); len(l) != 1 || l[0].MediaID != 3 || l[0].Seq != 2001 {
		t.Fatalf("a new one: %+v", l)
	}
	// Opened again: nothing changes.
	_ = d.Close()
	if d, err = db.Open(path); err != nil {
		t.Fatal(err)
	}
	if l := NewStore(d).Latest(3); len(l) != 3 || l[0].Seq != 2001 {
		t.Fatalf("opened again: %+v", l)
	}
}
