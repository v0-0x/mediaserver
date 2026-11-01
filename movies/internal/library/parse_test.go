package library

import (
	"path/filepath"
	"testing"
)

// A library folder can be a whole drive ("E:\", or "/"): its files are
// read with all their folders, like in any other library folder.
func TestParseDriveRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	path := filepath.Join(root, "TV", "Breaking Bad (2008)", "Season 01", "01 - Pilot.mkv")
	if !underAny(path, []string{root}) || !underAny(filepath.Join(root, "x.mkv"), []string{root}) {
		t.Fatalf("%s isn't in %s", path, root)
	}
	p := Parse(path, []string{root})
	if p.Title != "Breaking Bad" || p.Year != 2008 || p.Season != 1 || p.Episode != 1 || p.Folder != filepath.Join(root, "TV", "Breaking Bad (2008)") {
		t.Errorf("got %+v", p)
	}
}

func TestParse(t *testing.T) {
	root := filepath.FromSlash("/lib/TV")
	movies := filepath.FromSlash("/lib/Movies")
	roots := []string{root, movies}
	in := func(r, p string) string { return filepath.Join(r, filepath.FromSlash(p)) }
	cases := []struct {
		root, path  string
		title       string
		year        int
		season, ep  int
		end, abs    int
		date        string
		kind        string
		show        bool
		folder      string // below its root, "" for none
		folderTitle string
	}{
		{root, "Breaking Bad (2008)/Season 01/Breaking.Bad.S01E02.720p.BluRay.x264-GRP.mkv", "Breaking Bad", 2008, 1, 2, 0, 0, "", KindMain, true, "Breaking Bad (2008)", "Breaking Bad"},
		{root, "Breaking Bad (2008)/Season 01/03 - ...And the Bag's in the River.mkv", "Breaking Bad", 2008, 1, 3, 0, 0, "", KindMain, true, "Breaking Bad (2008)", "Breaking Bad"},
		{root, "Breaking Bad (2008)/Season 02/Breaking.Bad.S02E01E02.mkv", "Breaking Bad", 2008, 2, 1, 2, 0, "", KindMain, true, "Breaking Bad (2008)", "Breaking Bad"},
		{root, "Breaking Bad (2008)/Specials/Breaking Bad - S00E01.mkv", "Breaking Bad", 2008, 0, 1, 0, 0, "", KindSpecial, true, "Breaking Bad (2008)", "Breaking Bad"},
		{root, "Breaking Bad (2008)/Extras/Gag Reel.mkv", "Breaking Bad", 2008, -1, -1, 0, 0, "", KindExtra, false, "Breaking Bad (2008)", "Breaking Bad"},
		{root, "Breaking.Bad.S02.1080p.BluRay.x264-GRP/Breaking.Bad.S02E03.1080p.BluRay.x264-GRP.mkv", "Breaking Bad", 0, 2, 3, 0, 0, "", KindMain, true, "Breaking.Bad.S02.1080p.BluRay.x264-GRP", "Breaking Bad"},
		{root, "The.Office.US.S02E03-E04.720p.mkv", "The Office US", 0, 2, 3, 4, 0, "", KindMain, true, "", ""},
		{root, "Attack on Titan/Attack on Titan - 26 [1080p].mkv", "Attack on Titan", 0, -1, 26, 0, 26, "", KindMain, true, "Attack on Titan", "Attack on Titan"},
		{root, "The Daily Show/The.Daily.Show.2024.01.02.mkv", "The Daily Show", 0, -1, -1, 0, 0, "2024-01-02", KindMain, true, "The Daily Show", "The Daily Show"},
		{root, "TV Shows/Doctor Who (2005)/Season 1/Doctor.Who.2005.S01E01.Rose.mkv", "Doctor Who", 2005, 1, 1, 0, 0, "", KindMain, true, "TV Shows/Doctor Who (2005)", "Doctor Who"},
		{movies, "The.Matrix.1999.1080p.BluRay.x264-GRP.mkv", "The Matrix", 1999, -1, -1, 0, 0, "", KindMain, false, "", ""},
		{movies, "Inception.2010.1080p.BluRay.x264-SPARKS/sparks-inception.mkv", "Inception", 2010, -1, -1, 0, 0, "", KindMain, false, "Inception.2010.1080p.BluRay.x264-SPARKS", "Inception"},
		{movies, "Inception.2010.1080p.BluRay.x264-SPARKS/Sample/sparks-inception-sample.mkv", "Inception", 2010, -1, -1, 0, 0, "", KindExtra, false, "Inception.2010.1080p.BluRay.x264-SPARKS", "Inception"},
		{movies, "Dune (2021)/Featurettes/Making Dune.mkv", "Dune", 2021, -1, -1, 0, 0, "", KindExtra, false, "Dune (2021)", "Dune"},
		{movies, "Heat (1995)/Heat (1995) - CD2.avi", "Heat", 1995, -1, -1, 0, 0, "", KindMain, false, "Heat (1995)", "Heat"},
		// A folder above the library says nothing.
		{movies, "movie.mkv", "", 0, -1, -1, 0, 0, "", KindMain, false, "", ""},
	}
	for _, c := range cases {
		p := Parse(in(c.root, c.path), roots)
		folder := ""
		if c.folder != "" {
			folder = in(c.root, c.folder)
		}
		if p.Title != c.title || p.Year != c.year || p.Season != c.season || p.Episode != c.ep || p.EpisodeEnd != c.end ||
			p.Absolute != c.abs || p.Date != c.date || p.Kind != c.kind || p.Show != c.show || p.Folder != folder || p.FolderTitle != c.folderTitle {
			t.Errorf("%s\n  got  %q %d S%d E%d-%d abs %d %q %s show=%v folder=%q (%q)\n  want %q %d S%d E%d-%d abs %d %q %s show=%v folder=%q (%q)", c.path,
				p.Title, p.Year, p.Season, p.Episode, p.EpisodeEnd, p.Absolute, p.Date, p.Kind, p.Show, p.Folder, p.FolderTitle,
				c.title, c.year, c.season, c.ep, c.end, c.abs, c.date, c.kind, c.show, folder, c.folderTitle)
		}
		if p.V != parseVersion {
			t.Errorf("%s: version %d", c.path, p.V)
		}
	}

	// Ids, release details.
	p := Parse(in(movies, "Avatar (2009) [tmdbid-19995]/Avatar (2009) - 1080p.mkv"), roots)
	if p.TMDBID != 19995 || p.Resolution != "1080p" {
		t.Errorf("Avatar: %+v", p)
	}
	p = Parse(in(movies, "Matrix Cut {imdb-tt0133093}/movie.mkv"), roots)
	if p.IMDbID != "tt0133093" || p.Title != "Matrix Cut" {
		t.Errorf("IMDb tag: %+v", p)
	}
	p = Parse(in(movies, "Inception.2010.1080p.BluRay.x264-SPARKS/sparks-inception.mkv"), roots)
	if p.ReleaseGroup != "SPARKS" || p.Source != "BluRay" || p.Codec != "x264" {
		t.Errorf("release details: %+v", p)
	}
	// A file outside the library folders (a download folder) is read with
	// its own folder only.
	p = Parse(filepath.FromSlash("/home/me/Downloads/Fargo (2014)/Fargo.S02E05.mkv"), roots)
	if p.Title != "Fargo" || p.Year != 2014 || p.Season != 2 || p.Episode != 5 || p.Folder != filepath.FromSlash("/home/me/Downloads/Fargo (2014)") {
		t.Errorf("outside the library: %+v", p)
	}
}

func TestIsVideo(t *testing.T) {
	for _, n := range []string{"a.mkv", "b.MP4", "c.avi", "d.webm", "e.m2ts", "f.rmvb", "g.ogm", "h.wmv", "i.flv", "j.ts", "k.vob", "l.mov"} {
		if !IsVideo(n) {
			t.Errorf("%s should be a video", n)
		}
	}
	for _, n := range []string{"a.srt", "b.ass", "c.nfo", "d.jpg", "e.!qB", "f.part"} {
		if IsVideo(n) {
			t.Errorf("%s should not be a video", n)
		}
	}
	for _, n := range []string{".srt", ".ass", ".vtt", ".sup"} {
		if !SubtitleExtensions[n] {
			t.Errorf("%s should be a subtitle", n)
		}
	}
}

func TestNormalizedKind(t *testing.T) {
	for in, want := range map[string]string{"main": KindMain, "special": KindSpecial, "nc": KindExtra, "extra": KindExtra, "part": KindPart, "": KindMain} {
		if got := normalizedKind(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
