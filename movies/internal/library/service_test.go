package library

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/history"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return &Service{Store: NewStore(d), History: history.NewStore(d), DB: d}
}

// show is the media id of test show n.
func show(n int) int { return catalog.ShowID(n) }

// watching is a list of shows of one 12-episode season being watched: show
// number → progress (episode of season 1).
func watching(progress map[int]int) *catalog.Collection {
	l := &catalog.List{Name: "Watching", Status: "CURRENT"}
	for n, p := range progress {
		m := testShow(n, "Show", 12)
		key := 0
		if p > 0 {
			key = catalog.EpisodeKey(1, p)
		}
		l.Entries = append(l.Entries, &catalog.ListEntry{MediaID: m.ID, Status: "CURRENT", Progress: key, Media: m})
	}
	return &catalog.Collection{Lists: []*catalog.List{l}}
}

func TestHideContinue(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	// offered returns the episode offered for each show (by number), as
	// the number of the episode in season 1.
	offered := func(progress map[int]int) map[int]int {
		out := map[int]int{}
		for _, it := range s.continueWatching(ctx, watching(progress), nil) {
			_, e := catalog.SplitKey(it.Episode)
			out[it.Media.TMDBID] = e
		}
		return out
	}
	hide := func(n, ep int) {
		t.Helper()
		if err := s.HideContinue(show(n), catalog.EpisodeKey(1, ep)); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(n, ep int) {
		t.Helper()
		if err := s.History.Save(history.Entry{MediaID: show(n), Episode: catalog.EpisodeKey(1, ep), Position: 300, Duration: 1400}); err != nil {
			t.Fatal(err)
		}
	}

	list := map[int]int{1: 3, 2: 5}
	// Last watched of the first one: a rewatch of episode 2. Once hidden,
	// its history must not offer it again with that episode. (s has no
	// Platform, so even looking it up for that would panic.)
	watch(1, 2)
	if got := offered(list); len(got) != 2 || got[1] != 4 || got[2] != 6 {
		t.Fatalf("before hiding: %v", got)
	}
	hide(1, 4)
	if got := offered(list); len(got) != 1 || got[2] != 6 {
		t.Fatalf("hidden show still offered: %v", got)
	}

	// Progress made elsewhere (another device, marked by hand): it offers
	// another episode, so it is back.
	if got := offered(map[int]int{1: 4, 2: 5}); got[1] != 5 {
		t.Fatalf("not back for the next episode: %v", got)
	}

	// Watching another show doesn't bring it back, watching it does.
	watch(2, 6)
	if got := offered(list); got[1] != 0 {
		t.Fatalf("back after watching another show: %v", got)
	}
	// Hidden a minute ago: the history has whole seconds, and watching in
	// the same second as hiding counts as before.
	_ = s.updateHidden(func(items map[int]hiddenItem) {
		it := items[show(1)]
		it.HiddenAt -= 60
		items[show(1)] = it
	})
	watch(1, 4)
	if got := offered(list); got[1] != 4 {
		t.Fatalf("not back after watching it again: %v", got)
	}

	// Undo. Hiding also forgets the show that came back by being watched.
	hide(2, 6)
	if got := offered(list); got[2] != 0 {
		t.Fatalf("hidden show still offered: %v", got)
	}
	if _, ok := s.hiddenContinue()[show(1)]; ok {
		t.Fatalf("show watched again is still stored: %v", s.hiddenContinue())
	}
	if err := s.UnhideContinue(show(2)); err != nil {
		t.Fatal(err)
	}
	if got := offered(list); got[2] != 6 {
		t.Fatalf("not back after unhiding: %v", got)
	}
}

func TestHideContinueConcurrent(t *testing.T) {
	s := newTestService(t)
	var wg sync.WaitGroup
	for id := 1; id <= 20; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.HideContinue(id, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(s.hiddenContinue()); n != 20 {
		t.Fatalf("%d of 20 hidden titles saved", n)
	}
}

// Files other Moviko apps share fill in the episodes this computer doesn't
// have; its own always win.
func TestSharedFilesFillIn(t *testing.T) {
	s := newTestService(t)
	id := show(5)
	local := []*LocalFile{
		{Path: "/a/Show S01E01.mkv", Name: "Show S01E01.mkv", MediaID: id, Episode: 1001, Kind: KindMain, Size: 100},
		{Path: "/a/Show S00E01.mkv", Name: "Show S00E01.mkv", MediaID: id, Episode: 1, Kind: KindSpecial},
	}
	shared := []*LocalFile{
		{Path: "kumobox://h//b/Show S01E01.mkv", Name: "Show S01E01.mkv", MediaID: id, Episode: 1001, Kind: KindMain, Size: 900, Host: "h", MatchScore: 1},
		{Path: "kumobox://h//b/Show S01E02.mkv", Name: "Show S01E02.mkv", MediaID: id, Episode: 1002, Kind: KindMain, Host: "h"},
		{Path: "kumobox://h//b/Show S00E01.mkv", Name: "Show S00E01.mkv", MediaID: id, Episode: 1, Kind: KindSpecial, Host: "h"},
		{Path: "kumobox://h//b/Show - Featurette.mkv", Name: "Show - Featurette.mkv", MediaID: id, Kind: KindExtra, Host: "h"},
		{Path: "kumobox://h//b/Other S01E01.mkv", Name: "Other S01E01.mkv", MediaID: show(6), Episode: 1001, Kind: KindMain, Host: "h"},
	}
	s.Remote = func() []*LocalFile { return shared }

	got := map[string]bool{}
	for _, f := range s.sharedFor(id, local) {
		got[f.Name] = true
	}
	if !got["Show S01E01.mkv"] || !got["Show S01E02.mkv"] || !got["Show - Featurette.mkv"] || got["Show S00E01.mkv"] || got["Other S01E01.mkv"] {
		t.Errorf("shared files of the show: %v", got)
	}

	all := append(append([]*LocalFile{}, local...), s.sharedFor(id, local)...)
	if f := EpisodeFile(all, 1001); f == nil || f.Host != "" {
		t.Errorf("S01E01 plays from %+v, want this computer's file", f)
	}
	if f := EpisodeFile(all, 1002); f == nil || f.Host != "h" {
		t.Errorf("S01E02 plays from %+v, want the shared file", f)
	}

	items := s.continueWatching(context.Background(), watching(map[int]int{5: 1}), map[int][]*LocalFile{id: all})
	if len(items) != 1 || items[0].Episode != 1002 || items[0].Index != 2 || items[0].Total != 12 || !items[0].HasFile || items[0].FilePath != "kumobox://h//b/Show S01E02.mkv" {
		t.Errorf("continue watching: %+v", items)
	}
}

// Continue watching follows the list: a show whose entry was just updated
// (watched on another device, marked by hand) comes before one watched here
// long ago.
func TestContinueWatchingFollowsTheList(t *testing.T) {
	s := newTestService(t)
	coll := watching(map[int]int{5: 3, 6: 7})
	for _, e := range coll.Entries() {
		e.UpdatedAt = 1000
		if e.MediaID == show(6) {
			e.UpdatedAt = time.Now().Unix()
		}
	}
	if err := s.History.Save(history.Entry{MediaID: show(5), Episode: 1004, Position: 100, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	// Watched here, but before the other change.
	if _, err := s.DB.Write(`UPDATE watch_history SET updated_at = ?`, time.Now().Unix()-3600); err != nil {
		t.Fatal(err)
	}
	items := s.continueWatching(context.Background(), coll, nil)
	if len(items) != 2 || items[0].Media.ID != show(6) || items[0].Episode != 1008 || items[1].Media.ID != show(5) || items[1].Episode != 1004 {
		var got []int
		for _, it := range items {
			got = append(got, it.Episode)
		}
		t.Errorf("continue watching %v, want show 6 (S01E08) first", got)
	}
}

// Continue watching goes on to the next season, offers nothing after the
// last episode, and never an episode that hasn't aired.
func TestContinueWatchingSeasons(t *testing.T) {
	s := newTestService(t)
	ended := testShow(1, "Ended", 2, 3)
	airing := testShow(2, "Airing", 4, 4)
	airing.Status, airing.LatestEpisode = "RELEASING", 2002
	l := &catalog.List{Status: "CURRENT", Entries: []*catalog.ListEntry{
		{MediaID: ended.ID, Status: "CURRENT", Progress: 1002, Media: ended},
		{MediaID: airing.ID, Status: "CURRENT", Progress: 2001, Media: airing},
	}}
	got := map[int]int{}
	for _, it := range s.continueWatching(context.Background(), &catalog.Collection{Lists: []*catalog.List{l}}, nil) {
		got[it.Media.TMDBID] = it.Episode
	}
	if got[1] != 2001 || got[2] != 2002 {
		t.Errorf("offered %v", got)
	}
	l.Entries[0].Progress, l.Entries[1].Progress = 2003, 2002
	got = map[int]int{}
	for _, it := range s.continueWatching(context.Background(), &catalog.Collection{Lists: []*catalog.List{l}}, nil) {
		got[it.Media.TMDBID] = it.Episode
	}
	if len(got) != 0 {
		t.Errorf("offered %v after the last (aired) episodes", got)
	}
}

func testMovie(id int, title string, year int) *catalog.Media {
	m := &catalog.Media{ID: catalog.MovieID(id), TMDBID: id, Type: catalog.TypeMovie, Format: catalog.TypeMovie, SeasonYear: &year}
	m.Title.English, m.Title.UserPreferred = title, title
	return m
}

func testShow(id int, title string, seasons ...int) *catalog.Media {
	m := &catalog.Media{ID: catalog.ShowID(id), TMDBID: id, Type: catalog.TypeTV, Format: catalog.TypeTV, Status: "FINISHED"}
	m.Title.English, m.Title.UserPreferred = title, title
	for i, n := range seasons {
		m.Seasons = append(m.Seasons, catalog.Season{Number: i + 1, EpisodeCount: n})
	}
	return m
}

// The title page lists a show's episodes by season with TMDB's details,
// its specials (season 0, special files, files whose episode isn't known)
// and its extras; a multi-episode file is the file of each of its
// episodes. A movie has one episode, its later parts and extras are
// "others".
func TestFillEpisodes(t *testing.T) {
	s := newTestService(t)
	m := testShow(1396, "Breaking Bad", 2, 2)
	m.Seasons = append([]catalog.Season{{Number: 0, EpisodeCount: 1}}, m.Seasons...)
	details := map[int]*catalog.Episode{
		1001: {Key: 1001, Title: "Pilot", AirDate: "2008-01-20", Still: "/still.jpg", Runtime: 58},
		1002: {Key: 1002, Title: "Cat's in the Bag...", AirDate: "2008-01-27"},
		2001: {Key: 2001, Title: "Seven Thirty-Seven", AirDate: "2009-03-08"},
		2002: {Key: 2002, Title: "Grilled", AirDate: "2999-03-15"},
		1:    {Key: 1, Title: "Good Cop Bad Cop", AirDate: "2009-02-17"},
	}
	double := &LocalFile{Path: "/a/S02E01E02.mkv", Kind: KindMain, Episode: 2001, EpisodeEnd: 2002}
	files := []*LocalFile{
		{Path: "/a/S01E02.mkv", Kind: KindMain, Episode: 1002},
		double,
		{Path: "/a/S03E01.mkv", Kind: KindMain, Episode: 3001},
		{Path: "/a/S00E01.mkv", Kind: KindSpecial, Episode: 1},
		{Path: "/a/S00E07.mkv", Name: "S00E07.mkv", Kind: KindSpecial, Episode: 7},
		{Path: "/a/Unknown.mkv", Name: "Unknown.mkv", Kind: KindMain},
		{Path: "/a/Featurette.mkv", Name: "Featurette.mkv", Kind: KindExtra, Parsed: Parsed{Extra: "featurette"}},
	}
	v := &EntryView{}
	s.fillEpisodes(v, m, &catalog.ListEntry{Status: "CURRENT", Progress: 1002}, files, details)
	var keys []int
	for _, ep := range v.Episodes {
		keys = append(keys, ep.Key)
	}
	if len(keys) != 5 || keys[0] != 1001 || keys[4] != 3001 {
		t.Fatalf("episodes %v", keys)
	}
	e1, e2, e3, e4 := v.Episodes[0], v.Episodes[1], v.Episodes[2], v.Episodes[3]
	if e1.Title != "Pilot" || e1.Image != "/still.jpg" || e1.Runtime != 58 || !e1.Watched || !e1.Aired || e1.Season != 1 || e1.Episode != 1 || e1.Number != 1001 || e1.HasFile {
		t.Errorf("S01E01: %+v", e1)
	}
	if !e2.HasFile || !e2.Watched || e3.Watched || !e3.Aired || e3.File != double || e4.File != double || !e4.Aired {
		t.Errorf("S01E02 %+v, S02E01 %+v, S02E02 %+v", e2, e3, e4)
	}
	if v.NextEpisode != e3 {
		t.Errorf("next episode %+v, want S02E01", v.NextEpisode)
	}
	if len(v.Specials) != 3 || v.Specials[0].Title != "Good Cop Bad Cop" || !v.Specials[0].HasFile || v.Specials[1].Key != 7 || v.Specials[1].Title != "S00E07.mkv" ||
		v.Specials[2].Key != 0 || v.Specials[2].Title != "Unknown.mkv" {
		t.Errorf("specials %+v", v.Specials)
	}
	if len(v.Others) != 1 || v.Others[0].File.Name != "Featurette.mkv" || v.Others[0].Title != "Featurette" || v.Others[0].Kind != KindExtra {
		t.Errorf("extras %+v", v.Others)
	}

	// Without any season's details (TMDB not answering, seasons not
	// fetched), the episodes and the next one are still keys, from the
	// season list.
	v = &EntryView{}
	s.fillEpisodes(v, testShow(1396, "Breaking Bad", 7, 13), &catalog.ListEntry{Status: "CURRENT", Progress: 1007}, nil, nil)
	if n := v.NextEpisode; n == nil || n.Key != 2001 || n.Number != 2001 || n.Season != 2 || n.Episode != 1 || len(v.Episodes) != 20 {
		t.Errorf("next episode without details: %+v", n)
	}

	movie := testMovie(155, "The Dark Knight", 2008)
	movie.Status = "FINISHED"
	v = &EntryView{}
	s.fillEpisodes(v, movie, nil, []*LocalFile{
		{Path: "/m/tdk - cd1.mkv", Kind: KindMain, Episode: 1, Parsed: Parsed{Part: 1}},
		{Path: "/m/tdk - cd2.mkv", Kind: KindPart, Episode: 1, Parsed: Parsed{Part: 2}},
		{Path: "/m/tdk-trailer.mkv", Kind: KindExtra, Parsed: Parsed{Extra: "trailer"}},
	}, nil)
	if len(v.Episodes) != 1 || v.Episodes[0].Key != catalog.MovieEpisode || v.Episodes[0].Title != "The Dark Knight" || !v.Episodes[0].HasFile ||
		v.Episodes[0].File.Path != "/m/tdk - cd1.mkv" || v.NextEpisode != v.Episodes[0] {
		t.Errorf("movie: %+v", v.Episodes)
	}
	if len(v.Others) != 2 || v.Others[0].Title != "Part 2" || v.Others[0].Kind != KindPart || v.Others[1].Title != "Trailer" {
		t.Errorf("movie extras: %+v %+v", v.Others[0], v.Others[1])
	}
	// Not on the list: watched as far as the history goes.
	if err := s.History.Save(history.Entry{MediaID: movie.ID, Episode: 1, Position: 7000, Duration: 7200}); err != nil {
		t.Fatal(err)
	}
	v = &EntryView{}
	s.fillEpisodes(v, movie, nil, nil, nil)
	if !v.Episodes[0].Watched || v.Episodes[0].HasFile {
		t.Errorf("watched movie: %+v", v.Episodes[0])
	}
}

// A title page fetches the seasons it has files of, and those of the
// episodes watched and next: not all of them.
func TestSeasonsWanted(t *testing.T) {
	m := testShow(1, "Long Show", 10, 10, 10, 10, 10, 10, 10, 10, 10, 10)
	m.Seasons = append(m.Seasons, catalog.Season{Number: 0, EpisodeCount: 3})
	files := []*LocalFile{
		{Kind: KindMain, Episode: 3001, EpisodeEnd: 3002},
		{Kind: KindSpecial, Episode: 2},
		{Kind: KindExtra},
		{Kind: KindMain, Episode: 42001}, // a season TMDB doesn't have
	}
	for _, c := range []struct {
		progress int
		want     string
	}{
		{0, "[0 1 3]"},
		{5010, "[0 3 5 6]"},
		{5005, "[0 3 5]"},
	} {
		if got := fmt.Sprint(SeasonsWanted(m, files, c.progress)); got != c.want {
			t.Errorf("progress %d: %s, want %s", c.progress, got, c.want)
		}
	}
	if got := SeasonsWanted(testMovie(1, "Movie", 2000), files, 0); got != nil {
		t.Errorf("movie: %v", got)
	}
}

func TestEpisodeRanges(t *testing.T) {
	for _, c := range []struct {
		keys []int
		want string
	}{
		{[]int{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009, 1010, 2001, 2002, 2003}, "S01E01–E10, S02E01–E03"},
		{[]int{1003, 1001, 1002, 1005, 2001, 2002, 2002}, "S01E01–E03, E05, S02E01–E02"},
		{[]int{4012}, "S04E12"},
		{nil, ""},
	} {
		if got := EpisodeRanges(c.keys); got != c.want {
			t.Errorf("%v: %q, want %q", c.keys, got, c.want)
		}
	}
}

// The Local library page sums each title's files up: episodes by season,
// size, resolutions, release groups, the newest file, what's unwatched.
func TestSummarize(t *testing.T) {
	s := newTestService(t)
	bb := testShow(1396, "Breaking Bad", 7, 13)
	file := func(path string, kind string, key, end int, size, mod int64) *LocalFile {
		return &LocalFile{Path: path, Dir: filepath.Dir(path), Name: filepath.Base(path), Kind: kind, Episode: key, EpisodeEnd: end, Size: size, ModTime: mod, MediaID: bb.ID,
			Parsed: Parsed{Folder: "/tv/Breaking Bad (2008)", Resolution: "1080p", ReleaseGroup: "GRP"}}
	}
	files := []*LocalFile{
		file("/tv/Breaking Bad (2008)/Season 02/S02E01.mkv", KindMain, 2001, 0, 100, 30),
		file("/tv/Breaking Bad (2008)/Season 01/S01E01.mkv", KindMain, 1001, 0, 100, 10),
		file("/tv/Breaking Bad (2008)/Season 01/S01E02E03.mkv", KindMain, 1002, 1003, 200, 20),
		file("/tv/Breaking Bad (2008)/Specials/S00E01.mkv", KindSpecial, 1, 0, 50, 5),
		file("/tv/Breaking Bad (2008)/Extras/Gag Reel.mkv", KindExtra, 0, 0, 10, 40),
	}
	files[0].Parsed.Resolution, files[0].Parsed.ReleaseGroup = "2160p", ""
	t1 := s.summarize(bb, &catalog.ListEntry{Status: "CURRENT", Progress: 1002}, files)
	if fmt.Sprint(t1.Episodes) != "[1001 1002 1003 2001]" || t1.Ranges != "S01E01–E03, S02E01" || t1.Specials != 1 || t1.Extras != 1 ||
		t1.Size != 460 || t1.AddedAt != 40 || t1.Dir != "/tv/Breaking Bad (2008)" || t1.Unwatched != 2 || t1.Next == nil || t1.Next.Episode != 1002 ||
		fmt.Sprint(t1.Resolutions) != "[1080p 2160p]" || fmt.Sprint(t1.ReleaseGroups) != "[GRP]" {
		t.Errorf("show: %+v", t1)
	}
	if len(t1.Seasons) != 2 || t1.Seasons[0] != (SeasonFiles{Season: 1, Count: 3, Total: 7, Ranges: "S01E01–E03"}) || t1.Seasons[1].Count != 1 || t1.Seasons[1].Total != 13 {
		t.Errorf("seasons: %+v", t1.Seasons)
	}
	if t1.Files[0].Episode != 1001 || t1.Files[3].Kind != KindSpecial || t1.Files[4].Kind != KindExtra {
		t.Errorf("files not in order: %v %v", t1.Files[0].Path, t1.Files[4].Path)
	}
	if t2 := s.summarize(bb, &catalog.ListEntry{Status: "COMPLETED", Progress: 2013}, files); t2.Unwatched != 0 || t2.Next != nil {
		t.Errorf("watched: %+v", t2)
	}

	movie := testMovie(949, "Heat", 1995)
	parts := []*LocalFile{
		{Path: "/m/Heat (1995)/Heat - CD2.avi", Dir: "/m/Heat (1995)", Kind: KindPart, Episode: 1, Size: 700, MediaID: 949, Parsed: Parsed{Part: 2}},
		{Path: "/m/Heat (1995)/Heat - CD1.avi", Dir: "/m/Heat (1995)", Kind: KindMain, Episode: 1, Size: 700, MediaID: 949, Parsed: Parsed{Part: 1}},
	}
	t3 := s.summarize(movie, nil, parts)
	if t3.Ranges != "" || fmt.Sprint(t3.Episodes) != "[1]" || t3.Unwatched != 1 || t3.Next == nil || t3.Next.Path != "/m/Heat (1995)/Heat - CD1.avi" || t3.Extras != 1 || t3.Dir != "/m/Heat (1995)" {
		t.Errorf("movie: %+v", t3)
	}
}

// The Local library page's titles: this computer's files, or another
// Moviko's, with their movies and shows.
func TestTitles(t *testing.T) {
	f := testCatalog(t)
	p, d, _ := newTestPlatform(t, f)
	s := &Service{Store: NewStore(d), Platform: p, History: history.NewStore(d), DB: d}
	local := []*LocalFile{
		{Path: "/m/Heat (1995)/Heat.mkv", Dir: "/m/Heat (1995)", Name: "Heat.mkv", MediaID: 949, Episode: 1, Kind: KindMain, ModTime: 10},
		{Path: "/tv/BB/S01E01.mkv", Dir: "/tv/BB", Name: "S01E01.mkv", MediaID: catalog.ShowID(1396), Episode: 1001, Kind: KindMain, ModTime: 20},
		{Path: "/tv/x.mkv", Dir: "/tv", Name: "x.mkv", Kind: KindMain},                                          // unmatched
		{Path: "/tv/y.mkv", Dir: "/tv", Name: "y.mkv", MediaID: 862, Episode: 1, Kind: KindMain, Ignored: true}, // ignored
	}
	if err := s.Store.Save(local...); err != nil {
		t.Fatal(err)
	}
	s.Remote = func() []*LocalFile {
		return []*LocalFile{{Path: "kumobox://h1//d/Dune.mkv", Name: "Dune.mkv", Dir: "/d", MediaID: 438631, Episode: 1, Kind: KindMain, Host: "h1"}}
	}
	ctx := context.Background()
	titles, err := s.Titles(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 2 || titles[0].Media.ID != catalog.ShowID(1396) || titles[0].Ranges != "S01E01" || titles[1].Media.ID != 949 || titles[1].Media.PreferredTitle() != "Heat" {
		t.Fatalf("titles: %+v", titles)
	}
	shared, err := s.Titles(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0].Media.ID != 438631 || shared[0].Dir != "" {
		t.Errorf("shared: %+v", shared)
	}
}
