package catalog

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedule(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format(time.DateOnly) }
	var failing atomic.Bool
	failing.Store(true)
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		episode := func(s, e int, date string) map[string]any {
			return map[string]any{"season_number": s, "episode_number": e, "air_date": date, "name": FormatKey(EpisodeKey(s, e))}
		}
		switch r.URL.Path {
		case "/tv/500/season/2":
			reply(w, http.StatusOK, map[string]any{"season_number": 2, "name": "Season 2", "episodes": []any{
				episode(2, 1, day(-7)), episode(2, 2, day(0)), episode(2, 3, day(7)), episode(2, 4, day(14)),
			}})
		case "/tv/503/season/1":
			if failing.Load() {
				reply(w, http.StatusInternalServerError, map[string]any{})
				return true
			}
			reply(w, http.StatusOK, map[string]any{"season_number": 1, "episodes": []any{episode(1, 1, day(1)), episode(1, 2, "")}})
		default:
			return false
		}
		return true
	})
	p := newTestPlatform(t, f)
	ctx := context.Background()

	airing := testShow(500, "RELEASING", 3, 4)
	airing.Seasons[1].AirDate = day(-7)
	airing.LatestEpisode = 2002
	airing.NextAiringEpisode = &AiringEpisode{Episode: 2003}
	addLocal(t, p, airing, "CURRENT", 2001)
	done := testShow(501, "RELEASING", 5)
	addLocal(t, p, done, "COMPLETED", 1005)
	ended := testShow(502, "FINISHED", 10)
	ended.EndDate = parseDate("2015-05-01")
	addLocal(t, p, ended, "PAUSED", 1003)
	other := testShow(503, "RELEASING", 2)
	addLocal(t, p, other, "PLANNING", 0)
	addLocal(t, p, testMovie(504), "CURRENT", 0)

	start := today.AddDate(0, 0, -1).Unix()
	end := today.AddDate(0, 0, 9).Unix()
	got, err := p.Schedule(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	// A season failed: what came is shown, but not kept.
	if len(got) != 2 || got[0].Episode != 2002 || got[1].Episode != 2003 {
		t.Fatalf("with a season failing: %+v", got)
	}
	failing.Store(false)
	got, err = p.Schedule(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	var keys []int
	for _, a := range got {
		keys = append(keys, a.Media.TMDBID*10000+a.Episode)
	}
	want := []int{5002002, 5031001, 5002003} // by air date
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] || keys[2] != want[2] {
		t.Fatalf("schedule %v, want %v", keys, want)
	}
	if a := got[0]; a.AiringAt != int(today.Unix()) || a.Title != "S02E02" || a.Media.ID != airing.ID ||
		a.TimeUntilAiring > 0 || a.TimeUntilAiring < -24*3600 {
		t.Errorf("first: %+v", a)
	}
	if f.count("/tv/500/season/1") != 0 || f.count("/tv/501/season/1") != 0 || f.count("/tv/502/season/1") != 0 {
		t.Error("asked for seasons with nothing airing, or of shows finished or ended")
	}
	before := f.total()
	if again, err := p.Schedule(ctx, start, end); err != nil || len(again) != 3 || f.total() != before {
		t.Errorf("not cached: %d items, %v, %d requests", len(again), err, f.total()-before)
	}
}

// The calendar follows the list: a show put on it after the calendar was
// looked at shows up at once, one dropped goes.
func TestScheduleFollowsTheList(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/tv/600/season/1" {
			return false
		}
		reply(w, http.StatusOK, map[string]any{"season_number": 1, "episodes": []any{
			map[string]any{"season_number": 1, "episode_number": 1, "air_date": today.AddDate(0, 0, 1).Format(time.DateOnly)},
		}})
		return true
	})
	p := newTestPlatform(t, f)
	ctx := context.Background()
	start, end := today.Unix(), today.AddDate(0, 0, 7).Unix()
	if got, err := p.Schedule(ctx, start, end); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v, %v", got, err)
	}
	addLocal(t, p, testShow(600, "RELEASING", 8), "CURRENT", 0)
	if got, err := p.Schedule(ctx, start, end); err != nil || len(got) != 1 || got[0].Episode != 1001 {
		t.Fatalf("after following a show: %v, %v", got, err)
	}
	if _, err := p.db.Write(`UPDATE local_list SET status = 'DROPPED'`); err != nil {
		t.Fatal(err)
	}
	p.invalidate()
	if got, err := p.Schedule(ctx, start, end); err != nil || len(got) != 0 {
		t.Fatalf("after dropping it: %v, %v", got, err)
	}
}

func TestAiringSeasons(t *testing.T) {
	now := time.Now()
	start, end := now.Unix(), now.AddDate(0, 0, 7).Unix()
	show := testShow(1, "RELEASING", 10, 10, 8)
	show.Seasons[2].AirDate = now.AddDate(0, 3, 0).Format(time.DateOnly) // announced, months away
	show.LatestEpisode = 2010
	if got := airingSeasons(show, start, end); len(got) != 1 || got[0] != 2 {
		t.Errorf("between seasons: %v", got)
	}
	show.Seasons[2].AirDate = ""
	show.NextAiringEpisode = &AiringEpisode{Episode: 3001}
	if got := airingSeasons(show, start, end); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("next season on the air: %v", got)
	}
	if got := airingSeasons(testMovie(1), start, end); got != nil {
		t.Errorf("a movie: %v", got)
	}
}
