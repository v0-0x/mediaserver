package library

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// The catalog the matching tests' TMDB has.
func testCatalog(t *testing.T) *fakeTMDB {
	f := newFakeTMDB(t)
	f.movie(603, "The Matrix", "1999-03-30").imdb = "tt0133093"
	f.movie(604, "The Matrix Reloaded", "2003-05-15")
	f.movie(624860, "The Matrix Resurrections", "2021-12-16")
	f.movie(27205, "Inception", "2010-07-15")
	f.movie(19995, "Avatar", "2009-12-15")
	f.movie(949, "Heat", "1995-12-15")
	f.movie(438631, "Dune", "2021-09-15")
	f.movie(841, "Dune", "1984-12-14")
	f.movie(862, "Toy Story", "1995-11-22")
	f.movie(863, "Toy Story 2", "1999-11-24")
	f.movie(76341, "Mad Max: Fury Road", "2015-05-13")
	f.movie(155, "The Dark Knight", "2008-07-16")

	f.show(1396, "Breaking Bad", "2008-01-20").
		specials("2009-02-17", "Good Cop Bad Cop", "Inside Breaking Bad").
		season(1, 7, "2008-01-20").season(2, 13, "2009-03-08")
	f.show(2316, "The Office", "2005-03-24").season(1, 6, "2005-03-24").season(2, 22, "2005-09-20")
	f.show(2996, "The Office", "2001-07-09").season(1, 6, "2001-07-09").season(2, 6, "2002-09-30")
	f.show(57243, "Doctor Who", "2005-03-26").season(1, 13, "2005-03-26")
	f.show(121, "Doctor Who", "1963-11-23").season(1, 42, "1963-11-23")
	f.show(1429, "Attack on Titan", "2013-04-07").season(1, 25, "2013-04-07").season(2, 12, "2017-04-01").season(3, 22, "2018-07-23")
	f.show(2224, "The Daily Show", "1996-07-22").daily(28, 150, "2023-01-02").daily(29, 150, "2024-01-01")
	f.show(95396, "Severance", "2022-02-18").season(1, 9, "2022-02-18").season(2, 10, "2025-01-17")
	f.show(97546, "Ted Lasso", "2020-08-14").season(1, 10, "2020-08-14")
	return f
}

// A movie library and a TV library as people have them: scene-named files
// loose or in their release folders, Plex-style folders, id tags, extras
// and sample folders, split movies; show folders with season folders,
// scene-named episodes in one folder, multi-episode files, specials, shows
// numbered from their first episode, a daily show.
func TestMatchLibrary(t *testing.T) {
	f := testCatalog(t)
	base := t.TempDir()
	movies, tv := filepath.Join(base, "Movies"), filepath.Join(base, "TV")
	tree(t, movies,
		"The.Matrix.1999.1080p.BluRay.x264-GRP.mkv",
		"The Matrix Collection/The.Matrix.Reloaded.2003.720p.BluRay.x264-GRP.mkv",
		"Inception.2010.1080p.BluRay.x264-SPARKS/sparks-inception.mkv",
		"Inception.2010.1080p.BluRay.x264-SPARKS/Sample/sparks-inception-sample.mkv",
		"Avatar (2009) [tmdbid-19995]/Avatar (2009) - 1080p.mkv",
		"Original Cut {imdb-tt0133093}/Original Cut.mkv",
		"Heat (1995)/Heat (1995) - CD1.avi",
		"Heat (1995)/Heat (1995) - CD2.avi",
		"Dune (2021)/Dune.2021.2160p.WEB-DL.DDP5.1.H.265-GRP.mkv",
		"Dune (2021)/Extras/Dune-trailer.mkv",
		"Dune (2021)/Featurettes/Making Dune.mkv",
		"Dune (1984)/Dune.1984.720p.mkv",
		"Toy.Story.1995.720p.mkv",
		"Mad Max Fury Road (2015)/Mad.Max.Fury.Road.2015.1080p.mkv",
		"Mad Max Fury Road (2015)/Mad.Max.Fury.Road.2015.1080p.en.srt",
		"Unknown Home Video (2003)/Unknown.Home.Video.2003.mkv",
	)
	tree(t, tv,
		"Breaking Bad (2008)/Season 01/Breaking.Bad.S01E01.720p.BluRay.x264-GRP.mkv",
		"Breaking Bad (2008)/Season 01/02 - Cat's in the Bag.mkv",
		"Breaking Bad (2008)/Specials/Breaking Bad - S00E01.mkv",
		"Breaking Bad (2008)/Specials/Inside Breaking Bad.mkv",
		"Breaking Bad (2008)/Extras/Gag Reel.mkv",
		"Breaking.Bad.S02.1080p.BluRay.x264-GRP/Breaking.Bad.S02E01.1080p.BluRay.x264-GRP.mkv",
		"Breaking.Bad.S02.1080p.BluRay.x264-GRP/Breaking.Bad.S02E02.1080p.BluRay.x264-GRP.mkv",
		"The.Office.US.S01E01.720p.HDTV.x264-GRP.mkv",
		"The.Office.US.S02E03-E04.720p.HDTV.x264-GRP.mkv",
		"TV Shows/Doctor Who (2005)/Season 1/Doctor.Who.2005.S01E01.Rose.mkv",
		"Attack on Titan/Attack on Titan - 03 [1080p].mkv",
		"Attack on Titan/Attack on Titan - 26 [1080p].mkv",
		"Attack on Titan/Season 3/Attack on Titan - 38 [1080p].mkv",
		"The Daily Show/The.Daily.Show.2024.01.02.mkv",
		"The.Daily.Show.2024.01.08.Guest.Name.720p.WEB.h264-GRP.mkv",
		"Severance (2022)/Season 1/01 - Good News About Hell.mkv",
		"Severance (2022)/Season 2/Severance.S02E01E02.2160p.mkv",
		"Ted Lasso (2020) {tmdb-97546}/Season 1/Ted Lasso - S01E01 - Pilot.mkv",
	)
	s, p := newMatchingScanner(t, f, movies, tv)
	cfg := s.settings.Get()
	cfg.Library.IgnorePatterns = []string{"*.part"} // samples too: they must turn out extras
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// On the list: Breaking Bad (matched from it, no search), and the
	// sequel of a film whose files are there (which must not take them).
	status := "CURRENT"
	for _, id := range []int{catalog.ShowID(1396), catalog.MovieID(863)} {
		if err := p.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: id, Status: &status}); err != nil {
			t.Fatal(err)
		}
	}
	f.reset()

	res, err := s.Scan(ctx, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	movie := func(id int) string { return fmt.Sprintf("%d/1/main", id) }
	show := func(id, key int) string { return fmt.Sprintf("%d/%d/main", catalog.ShowID(id), key) }
	want := map[string]string{
		"The.Matrix.1999.1080p.BluRay.x264-GRP.mkv":                                            movie(603),
		"The Matrix Collection/The.Matrix.Reloaded.2003.720p.BluRay.x264-GRP.mkv":              movie(604),
		"Inception.2010.1080p.BluRay.x264-SPARKS/sparks-inception.mkv":                         movie(27205),
		"Inception.2010.1080p.BluRay.x264-SPARKS/Sample/sparks-inception-sample.mkv":           "27205/0/extra",
		"Avatar (2009) [tmdbid-19995]/Avatar (2009) - 1080p.mkv":                               movie(19995),
		"Original Cut {imdb-tt0133093}/Original Cut.mkv":                                       movie(603),
		"Heat (1995)/Heat (1995) - CD1.avi":                                                    movie(949),
		"Heat (1995)/Heat (1995) - CD2.avi":                                                    "949/1/part",
		"Dune (2021)/Dune.2021.2160p.WEB-DL.DDP5.1.H.265-GRP.mkv":                              movie(438631),
		"Dune (2021)/Extras/Dune-trailer.mkv":                                                  "438631/0/extra",
		"Dune (2021)/Featurettes/Making Dune.mkv":                                              "438631/0/extra",
		"Dune (1984)/Dune.1984.720p.mkv":                                                       movie(841),
		"Toy.Story.1995.720p.mkv":                                                              movie(862),
		"Mad Max Fury Road (2015)/Mad.Max.Fury.Road.2015.1080p.mkv":                            movie(76341),
		"Unknown Home Video (2003)/Unknown.Home.Video.2003.mkv":                                "0/0/main",
		"Breaking Bad (2008)/Season 01/Breaking.Bad.S01E01.720p.BluRay.x264-GRP.mkv":           show(1396, 1001),
		"Breaking Bad (2008)/Season 01/02 - Cat's in the Bag.mkv":                              show(1396, 1002),
		"Breaking Bad (2008)/Specials/Breaking Bad - S00E01.mkv":                               fmt.Sprintf("%d/1/special", catalog.ShowID(1396)),
		"Breaking Bad (2008)/Specials/Inside Breaking Bad.mkv":                                 fmt.Sprintf("%d/2/special", catalog.ShowID(1396)),
		"Breaking Bad (2008)/Extras/Gag Reel.mkv":                                              fmt.Sprintf("%d/0/extra", catalog.ShowID(1396)),
		"Breaking.Bad.S02.1080p.BluRay.x264-GRP/Breaking.Bad.S02E01.1080p.BluRay.x264-GRP.mkv": show(1396, 2001),
		"Breaking.Bad.S02.1080p.BluRay.x264-GRP/Breaking.Bad.S02E02.1080p.BluRay.x264-GRP.mkv": show(1396, 2002),
		"The.Office.US.S01E01.720p.HDTV.x264-GRP.mkv":                                          show(2316, 1001),
		"The.Office.US.S02E03-E04.720p.HDTV.x264-GRP.mkv":                                      show(2316, 2003) + "-2004",
		"TV Shows/Doctor Who (2005)/Season 1/Doctor.Who.2005.S01E01.Rose.mkv":                  show(57243, 1001),
		"Attack on Titan/Attack on Titan - 03 [1080p].mkv":                                     show(1429, 1003),
		"Attack on Titan/Attack on Titan - 26 [1080p].mkv":                                     show(1429, 2001),
		"Attack on Titan/Season 3/Attack on Titan - 38 [1080p].mkv":                            show(1429, 3001),
		"The Daily Show/The.Daily.Show.2024.01.02.mkv":                                         show(2224, 29002),
		"The.Daily.Show.2024.01.08.Guest.Name.720p.WEB.h264-GRP.mkv":                           show(2224, 29006),
		"Severance (2022)/Season 1/01 - Good News About Hell.mkv":                              show(95396, 1001),
		"Severance (2022)/Season 2/Severance.S02E01E02.2160p.mkv":                              show(95396, 2001) + "-2002",
		"Ted Lasso (2020) {tmdb-97546}/Season 1/Ted Lasso - S01E01 - Pilot.mkv":                show(97546, 1001),
	}
	got := matches(t, s, movies, tv)
	for path, w := range want {
		if got[path] != w {
			t.Errorf("%s: %s, want %s", path, got[path], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d files indexed, want %d (the subtitle isn't one): %v", len(got), len(want), got)
	}
	if res.Matched != len(want)-1 || res.Unmatched != 1 {
		t.Errorf("scan result %+v", res)
	}

	// From the list and the ids: no search.
	for _, q := range []string{"/search/tv?Breaking", "/search/movie?Avatar", "/search/movie?Original", "/search/tv?Ted", "/search/movie?Toy Story 2"} {
		if n := f.count(q); n != 0 {
			t.Errorf("%d searches %s", n, q)
		}
	}
	// A show folder is matched once, not once per file.
	if n := f.count("/search/tv?Attack on Titan"); n != 1 {
		t.Errorf("Attack on Titan searched %d times", n)
	}

	// Rescanning, even matching everything again, searches nothing: what
	// was found is remembered.
	f.reset()
	if _, err := s.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/search/"); n != 0 {
		t.Errorf("%d searches when rescanning", n)
	}
	got = matches(t, s, movies, tv)
	for path, w := range want {
		if got[path] != w {
			t.Errorf("rescan: %s: %s, want %s", path, got[path], w)
		}
	}

	// A new episode in a show folder goes with the folder's other files.
	f.reset()
	tree(t, tv, "Attack on Titan/Attack on Titan - 04 [1080p].mkv")
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, movies, tv)["Attack on Titan/Attack on Titan - 04 [1080p].mkv"]; got != show(1429, 1004) {
		t.Errorf("new episode: %s", got)
	}
	if n := f.count("/search/"); n != 0 {
		t.Errorf("%d searches for a new episode", n)
	}
}

// A {tvdb-…} tag (Plex, Jellyfin, Emby) names the show whatever the
// folder's title says: TMDB finds it by that id, without a search, once.
func TestMatchByTVDBTag(t *testing.T) {
	f := testCatalog(t)
	f.show(1438, "The Wire", "2002-06-02").season(1, 13, "2002-06-02").specials("2002-06-02", "Behind the Wire").tvdb = 79126
	lib := t.TempDir()
	tree(t, lib,
		"Baltimore Stories {tvdb-79126}/Season 1/01 - The Target.mkv",
		"Baltimore Stories {tvdb-79126}/Season 1/Baltimore.Stories.S01E02.mkv",
		"Baltimore Stories {tvdb-79126}/Specials/Baltimore Stories - S00E01.mkv",
	)
	s, _ := newMatchingScanner(t, f, lib)
	f.reset()
	if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	got := matches(t, s, lib)
	show := catalog.ShowID(1438)
	for path, want := range map[string]string{
		"Baltimore Stories {tvdb-79126}/Season 1/01 - The Target.mkv":            fmt.Sprintf("%d/1001/main", show),
		"Baltimore Stories {tvdb-79126}/Season 1/Baltimore.Stories.S01E02.mkv":   fmt.Sprintf("%d/1002/main", show),
		"Baltimore Stories {tvdb-79126}/Specials/Baltimore Stories - S00E01.mkv": fmt.Sprintf("%d/1/special", show),
	} {
		if got[path] != want {
			t.Errorf("%s: %s, want %s", path, got[path], want)
		}
	}
	if n := f.count("/search/"); n != 0 {
		t.Errorf("%d searches", n)
	}
	if n := f.count("/find/79126"); n != 1 {
		t.Errorf("%d requests for the TVDB id, want 1", n)
	}
}

// A film named with a year more than one off TMDB's (its festival premiere
// came long before its release) is looked for once more without the year,
// and taken when the title is the same — but never a remake decades away.
func TestMatchMovieYearFarOff(t *testing.T) {
	f := testCatalog(t)
	f.movie(915935, "Anatomy of a Fall", "2023-05-21")
	lib := t.TempDir()
	tree(t, lib,
		"Anatomy.of.a.Fall.2025.1080p.WEB-DL.mkv",
		"Dune (1987)/Dune.1987.mkv",
		"Anatomy of Fall (2025)/Anatomy.of.Fall.2025.mkv",
	)
	s, _ := newMatchingScanner(t, f, lib)
	if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	got := matches(t, s, lib)
	for path, want := range map[string]string{
		"Anatomy.of.a.Fall.2025.1080p.WEB-DL.mkv":         "915935/1/main",
		"Dune (1987)/Dune.1987.mkv":                       "0/0/main",
		"Anatomy of Fall (2025)/Anatomy.of.Fall.2025.mkv": "0/0/main",
	} {
		if got[path] != want {
			t.Errorf("%s: %s, want %s", path, got[path], want)
		}
	}
}

// Files matched by hand (locked) keep their match, and new files in their
// folder follow it.
func TestMatchFollowsFoldersMatchedByHand(t *testing.T) {
	f := testCatalog(t)
	lib := t.TempDir()
	tree(t, lib, "Mystery Show/Mystery.Show.S01E01.mkv")
	s, p := newMatchingScanner(t, f, lib)
	ctx := context.Background()
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Mystery Show", "Mystery.Show.S01E01.mkv")
	file, _ := s.Store.Get(path)
	if file.MediaID != 0 {
		t.Fatalf("matched %d", file.MediaID)
	}
	media, err := p.MediaLite(ctx, catalog.ShowID(95396))
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(p, nil, 0, false)
	m.AssignManual(ctx, []*LocalFile{file}, media, ManualOptions{})
	if err := s.Store.Save(file); err != nil {
		t.Fatal(err)
	}
	f.reset()
	tree(t, lib, "Mystery Show/Mystery.Show.S01E02.mkv")
	if _, err := s.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	got := matches(t, s, lib)
	if got["Mystery Show/Mystery.Show.S01E01.mkv"] != fmt.Sprintf("%d/1001/main", catalog.ShowID(95396)) ||
		got["Mystery Show/Mystery.Show.S01E02.mkv"] != fmt.Sprintf("%d/1002/main", catalog.ShowID(95396)) {
		t.Errorf("got %v", got)
	}
	if n := f.count("/search/"); n != 0 {
		t.Errorf("%d searches", n)
	}
}

// Matching limited to the list (Settings › Library) never searches TMDB.
func TestMatchOnlyFromList(t *testing.T) {
	f := testCatalog(t)
	lib := t.TempDir()
	tree(t, lib, "Inception (2010)/Inception.2010.mkv", "Breaking.Bad.S01E03.mkv")
	s, p := newMatchingScanner(t, f, lib)
	cfg := s.settings.Get()
	cfg.Library.MatchOutsideList = false
	if _, err := s.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	status := "PLANNING"
	if err := p.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: catalog.ShowID(1396), Status: &status}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	got := matches(t, s, lib)
	if got["Inception (2010)/Inception.2010.mkv"] != "0/0/main" || got["Breaking.Bad.S01E03.mkv"] != fmt.Sprintf("%d/1003/main", catalog.ShowID(1396)) {
		t.Errorf("got %v", got)
	}
	if n := f.count("/search/"); n != 0 {
		t.Errorf("%d searches", n)
	}
}

// The first film's files, with only the sequel on the list: TMDB finds the
// first film. While TMDB can't be searched (offline, rate limited), the
// sequel isn't taken instead (watching the first film would mark the
// sequel watched), nothing is remembered, and the next scan tries again.
func TestSequelNeverTakenForLackOfBetter(t *testing.T) {
	f := testCatalog(t)
	lib := t.TempDir()
	tree(t, lib, "Toy Story (1995)/Toy.Story.1995.1080p.mkv", "Toy Story 2 (1999)/Toy.Story.2.1999.1080p.mkv")
	s, p := newMatchingScanner(t, f, lib)
	ctx := context.Background()
	status := "COMPLETED"
	if err := p.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: catalog.MovieID(863), Status: &status}); err != nil {
		t.Fatal(err)
	}
	f.down.Store(true)
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	got := matches(t, s, lib)
	if got["Toy Story (1995)/Toy.Story.1995.1080p.mkv"] != "0/0/main" || got["Toy Story 2 (1999)/Toy.Story.2.1999.1080p.mkv"] != "863/1/main" {
		t.Errorf("TMDB down: %v", got)
	}
	f.down.Store(false)
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, s, lib)["Toy Story (1995)/Toy.Story.1995.1080p.mkv"]; got != "862/1/main" {
		t.Errorf("TMDB up: %s", got)
	}
}

// Matching got better (matcherVersion): files matched before are matched
// again once. Those TMDB can't confirm a match for keep their old match
// meanwhile; those matched by hand never change.
func TestScanRematchesAfterMatcherUpgrade(t *testing.T) {
	f := testCatalog(t)
	lib := t.TempDir()
	film := filepath.Join(lib, "Toy Story (1995)", "Toy.Story.1995.1080p.BluRay.x264.mkv")
	byHand := filepath.Join(lib, "Other", "Other.2001.mkv")
	touch(t, film)
	touch(t, byHand)
	s, _ := newMatchingScanner(t, f, lib)
	ctx := context.Background()
	if _, err := s.Scan(ctx, ScanOptions{SkipMatching: true}); err != nil {
		t.Fatal(err)
	}
	files, _ := s.Store.All()
	for _, x := range files {
		x.MediaID, x.Episode, x.Kind, x.MatchScore = 863, 1, KindMain, 0.86 // the sequel, by an old matcher
		if x.Path == byHand {
			x.MediaID, x.Locked = 777, true // matched by hand
		}
	}
	if err := s.Store.Save(files...); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.db.SetKV(matcherVersionKey, matcherVersion-1); err != nil {
		t.Fatal(err)
	}
	matchedTo := func(path string) *LocalFile {
		x, err := s.Store.Get(path)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}

	f.down.Store(true)
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := matchedTo(film).MediaID; got != 863 {
		t.Errorf("TMDB down: matched to %d, want the old match (863) kept", got)
	}
	f.down.Store(false)
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := matchedTo(film); got.MediaID != 862 || got.Episode != catalog.MovieEpisode {
		t.Errorf("TMDB up: matched to %d (episode %d), want Toy Story (862)", got.MediaID, got.Episode)
	}
	if got := matchedTo(byHand).MediaID; got != 777 {
		t.Errorf("a match made by hand changed to %d", got)
	}
	f.reset()
	if _, err := s.Scan(ctx, ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/"); n != 0 || matchedTo(film).MediaID != 862 {
		t.Errorf("matched again after the upgrade (%d requests)", n)
	}
}

// Matching by hand: a show's files numbered from the names (shifted), from
// the show's first episode, as episodes of a season, or in file order; a
// movie's files are the movie, its later parts and its extras.
func TestAssignManual(t *testing.T) {
	f := testCatalog(t)
	p, _, _ := newTestPlatform(t, f)
	ctx := context.Background()
	aot, err := p.MediaLite(ctx, catalog.ShowID(1429))
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"/lib"}
	file := func(path string) *LocalFile {
		path = filepath.FromSlash(path)
		x := &LocalFile{Path: path, Dir: filepath.Dir(path), Name: filepath.Base(path), Parsed: Parse(path, roots)}
		x.Kind = x.Parsed.Kind
		return x
	}
	keys := func(files []*LocalFile) []string {
		var out []string
		for _, x := range files {
			v := fmt.Sprintf("%d/%s", x.Episode, x.Kind)
			if x.EpisodeEnd > 0 {
				v += fmt.Sprintf("-%d", x.EpisodeEnd)
			}
			if !x.Locked || x.MediaID == 0 {
				v += " (not locked)"
			}
			out = append(out, v)
		}
		return out
	}
	season := func(n int) *int { return &n }
	m := NewMatcher(p, nil, 0, false)
	for _, c := range []struct {
		name  string
		files []string
		opt   ManualOptions
		want  string
	}{
		{"from the names", []string{"/lib/AoT/AoT.S02E03.mkv", "/lib/AoT/AoT.S00E01.mkv", "/lib/AoT/Extras/Trailer.mkv"}, ManualOptions{}, "[2003/main 1/special 0/extra]"},
		{"absolute", []string{"/lib/AoT/AoT - 26.mkv", "/lib/AoT/AoT - 38.mkv"}, ManualOptions{Absolute: true}, "[2001/main 3001/main]"},
		{"absolute, shifted", []string{"/lib/AoT/AoT - 01.mkv", "/lib/AoT/AoT - 02.mkv"}, ManualOptions{Absolute: true, Offset: 25}, "[2001/main 2002/main]"},
		{"in a season", []string{"/lib/AoT/AoT - 01.mkv", "/lib/AoT/AoT - 02-03.mkv"}, ManualOptions{Season: season(3)}, "[3001/main 3002/main-3003]"},
		{"specials", []string{"/lib/AoT/AoT - 01.mkv"}, ManualOptions{Season: season(0)}, "[1/special]"},
		{"in file order", []string{"/lib/AoT/c.mkv", "/lib/AoT/a.mkv", "/lib/AoT/b.mkv"}, ManualOptions{Renumber: true}, "[1003/main 1001/main 1002/main]"},
		{"in file order, from season 2", []string{"/lib/AoT/b.mkv", "/lib/AoT/a.mkv"}, ManualOptions{Renumber: true, Offset: 25}, "[2002/main 2001/main]"},
	} {
		var files []*LocalFile
		for _, path := range c.files {
			files = append(files, file(path))
		}
		m.AssignManual(ctx, files, aot, c.opt)
		if got := fmt.Sprint(keys(files)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}

	heat, err := p.MediaLite(ctx, catalog.MovieID(949))
	if err != nil {
		t.Fatal(err)
	}
	files := []*LocalFile{file("/lib/Heat (1995)/Heat - CD2.avi"), file("/lib/Heat (1995)/Heat - CD1.avi"), file("/lib/Heat (1995)/Extras/Heat-trailer.mkv")}
	m.AssignManual(ctx, files, heat, ManualOptions{Renumber: true})
	if got := fmt.Sprint(keys(files)); got != "[1/part 1/main 0/extra]" {
		t.Errorf("movie: %s", got)
	}
}

// The episode of a show's file whose number isn't one of its season's: a
// number counted from the show's first episode, the season's number
// followed by the episode's ("101" is S01E01), and E00, a special of the
// season found by its title.
func TestShowKeyOfOtherNumberings(t *testing.T) {
	f := testCatalog(t)
	p, _, _ := newTestPlatform(t, f)
	ctx := context.Background()
	get := func(id int) *catalog.Media {
		media, err := p.MediaLite(ctx, catalog.ShowID(id))
		if err != nil {
			t.Fatal(err)
		}
		return media
	}
	sev, aot, bb := get(95396), get(1429), get(1396)
	m := NewMatcher(p, nil, 0, false)
	for _, c := range []struct {
		media *catalog.Media
		path  string
		want  int
	}{
		{sev, "/lib/Severance/Season 1/101.mkv", 1001},
		{sev, "/lib/Severance/Season 1/Severance - 109 - The We We Are.mkv", 1009},
		{sev, "/lib/Severance/Season 2/Severance - 205.mkv", 2005},
		{sev, "/lib/Severance/Season 2/Severance - 10.mkv", 2010},
		{sev, "/lib/Severance/Season 2/Severance - 12.mkv", 2003}, // the 12th episode
		{aot, "/lib/Attack on Titan/Season 3/Attack on Titan - 38.mkv", 3001},
		{aot, "/lib/Attack on Titan/Season 2/Attack on Titan - 05.mkv", 2005},
		{bb, "/lib/Breaking Bad/Season 02/Breaking Bad - S02E00 - Inside Breaking Bad.mkv", 2},
		{bb, "/lib/Breaking Bad/Season 02/Breaking Bad - S02E00 - Unknown Special.mkv", 0},
	} {
		path := filepath.FromSlash(c.path)
		file := &LocalFile{Path: path, Dir: filepath.Dir(path), Name: filepath.Base(path), Parsed: Parse(path, []string{filepath.FromSlash("/lib")})}
		file.Kind = file.Parsed.Kind
		m.assign(ctx, []*LocalFile{file}, c.media, 1)
		if file.Episode != c.want {
			t.Errorf("%s: %d, want %d", c.path, file.Episode, c.want)
		}
	}
}

// When two files are matched to one episode (two releases of a movie, say),
// the one played is the hand-matched one, then the surer match, then a
// file of that episode alone, then the bigger file. A multi-episode file
// plays for each of its episodes; extras and later parts never do.
func TestEpisodeFile(t *testing.T) {
	a := &LocalFile{Path: "/a/Heat (1995)/Heat.1995.1080p.mkv", Kind: KindMain, Episode: 1, MatchScore: 1, Size: 1}
	b := &LocalFile{Path: "/a/Heat.1995.2160p.mkv", Kind: KindMain, Episode: 1, MatchScore: 0.96, Size: 2}
	if f := EpisodeFile([]*LocalFile{b, a}, 1); f != a {
		t.Errorf("got %s, want the surer match", f.Path)
	}
	b.Locked = true
	if f := EpisodeFile([]*LocalFile{a, b}, 1); f != b {
		t.Errorf("got %s, want the match made by hand", f.Path)
	}
	if f := EpisodeFile([]*LocalFile{a, b}, 2); f != nil {
		t.Errorf("episode 2: %v", f)
	}
	part := &LocalFile{Path: "/a/Heat - CD2.avi", Kind: KindPart, Episode: 1, MatchScore: 1, Size: 9, Locked: true}
	extra := &LocalFile{Path: "/a/Heat-trailer.mkv", Kind: KindExtra, Episode: 1, Locked: true}
	if f := EpisodeFile([]*LocalFile{part, extra, a}, 1); f != a {
		t.Errorf("got %s, want the first part", f.Path)
	}

	double := &LocalFile{Path: "/s/Show.S01E01E02.mkv", Kind: KindMain, Episode: 1001, EpisodeEnd: 1002, MatchScore: 1, Size: 10}
	single := &LocalFile{Path: "/s/Show.S01E02.mkv", Kind: KindMain, Episode: 1002, MatchScore: 1, Size: 5}
	if f := EpisodeFile([]*LocalFile{double}, 1002); f != double {
		t.Errorf("S01E02: %v, want the file holding S01E01-E02", f)
	}
	if f := EpisodeFile([]*LocalFile{double, single}, 1002); f != single {
		t.Errorf("S01E02: %v, want the file of that episode alone", f)
	}
	if f := EpisodeFile([]*LocalFile{double, single}, 1003); f != nil {
		t.Errorf("S01E03: %v", f)
	}
}

// Files are grouped by their movie's or show's folder and title: a
// folder's files of one show are one group, extras included; a collection
// folder's films are one group each; a folder's films of the same title are
// told apart by year.
func TestGroupFiles(t *testing.T) {
	roots := []string{"/lib"}
	var files []*LocalFile
	for _, p := range []string{
		"/lib/Lost/Season 01/Lost.S01E01.mkv",
		"/lib/Lost/Season 02/Lost.S02E01.mkv",
		"/lib/Lost/Extras/Bloopers.mkv",
		"/lib/Collection/Alien.1979.mkv",
		"/lib/Collection/Aliens.1986.mkv",
		"/lib/Dune/Dune.1984.mkv",
		"/lib/Dune/Dune.2021.mkv",
		"/lib/Heat (1995)/Heat - CD1.avi",
		"/lib/Heat (1995)/Heat - CD2.avi",
		"/lib/Loose.Show.S01E01.mkv",
		"/lib/Loose.Show.S01E02.mkv",
		"/lib/movie.mkv",
	} {
		p = filepath.FromSlash(p)
		files = append(files, &LocalFile{Path: p, Dir: filepath.Dir(p), Name: filepath.Base(p), Parsed: Parse(p, roots)})
	}
	var got []string
	for _, g := range groupFiles(files) {
		got = append(got, fmt.Sprintf("%s/%d/%v/%d", g.title, g.year, g.show, len(g.files)))
	}
	want := "[Lost/0/true/3 Alien/1979/false/1 Aliens/1986/false/1 Dune/1984/false/1 Dune/2021/false/1 Heat/1995/false/2 Loose Show/0/true/2 /0/false/1]"
	if fmt.Sprint(got) != want {
		t.Errorf("groups %v\nwant %s", got, want)
	}
}
