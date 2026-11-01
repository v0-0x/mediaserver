package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

func TestMonthly(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := NewStore(d)
	loc := time.UTC
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, loc)
	put := func(media, ep int, pos, dur float64, at time.Time) {
		if _, err := d.Write(`INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at, seq) VALUES(?, ?, ?, ?, 'local', ?, 0)`, media, ep, pos, dur, at.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	put(1, 1, 1440, 1440, now.AddDate(0, 0, -1))                   // October: finished, 24 min
	put(1, 2, 600, 1440, now.AddDate(0, 0, -2))                    // October: 10 min seen
	put(2, 1, 1400, 1440, time.Date(2026, 9, 3, 0, 0, 0, 0, loc))  // September: finished
	put(3, 1, 1440, 1440, time.Date(2025, 9, 30, 0, 0, 0, 0, loc)) // too old
	months, err := s.Monthly(3, 0.85, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := []Month{{"2026-08", 0, 0}, {"2026-09", 1, 24}, {"2026-10", 1, 34}}
	if len(months) != 3 {
		t.Fatalf("months: %+v", months)
	}
	for i, m := range months {
		if m != want[i] {
			t.Errorf("%d: %+v, want %+v", i, m, want[i])
		}
	}
}
