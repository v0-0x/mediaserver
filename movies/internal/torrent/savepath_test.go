package torrent

import (
	"path/filepath"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

func TestSavePath(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "Videos")
	cfg := config.Settings{}
	cfg.Library.Dir = lib
	cfg.Torrent.CreateSubfolder = true
	year := 2010
	movie := &catalog.Media{ID: 27205, Type: catalog.TypeMovie, Title: catalog.Title{UserPreferred: "Inception: Part 1"}, StartDate: catalog.FuzzyDate{Year: &year}}
	show := severance()
	file := func(dir string, key int) *library.LocalFile {
		return &library.LocalFile{Path: filepath.Join(dir, "x.mkv"), Dir: dir, Episode: key, Kind: "main"}
	}
	in := func(parts ...string) string { return filepath.Join(append([]string{lib}, parts...)...) }
	for _, tc := range []struct {
		name   string
		media  *catalog.Media
		season int
		files  []*library.LocalFile
		want   string
	}{
		{"movie", movie, -1, nil, in("Inception： Part 1 (2010)")},
		{"movie with files", movie, -1, []*library.LocalFile{file(in("Inception.2010.1080p.BluRay"), 1)}, in("Inception.2010.1080p.BluRay")},
		{"movie files outside the library", movie, -1, []*library.LocalFile{file("/mnt/other", 1)}, in("Inception： Part 1 (2010)")},
		{"movie files in the library folder itself", movie, -1, []*library.LocalFile{file(lib, 1)}, in("Inception： Part 1 (2010)")},
		{"episode", show, 2, nil, in("Severance (2022)", "Season 02")},
		{"whole show", show, -1, nil, in("Severance (2022)")},
		{"specials", show, 0, nil, in("Severance (2022)", "Specials")},
		{"show with season folders", show, 2, []*library.LocalFile{file(in("Severance", "Season 1"), 1001), file(in("Severance", "Season 1"), 1002)}, in("Severance", "Season 02")},
		{"the season's folder", show, 1, []*library.LocalFile{file(in("Severance", "S01"), 1001)}, in("Severance", "S01")},
		{"show with flat files", show, 2, []*library.LocalFile{file(in("Severance"), 1001)}, in("Severance", "Season 02")},
		{"a torrent's folder", show, 2, []*library.LocalFile{file(in("Severance.S01.1080p.WEB-DL-NTb"), 1001)}, in("Severance (2022)", "Season 02")},
		{"a torrent's folder in the show's", show, 2, []*library.LocalFile{file(in("Shows", "Severance", "Severance.S01.1080p.WEB-DL-NTb"), 1001)}, in("Shows", "Severance", "Season 02")},
		{"shared files", show, 2, []*library.LocalFile{{Dir: in("Elsewhere"), Episode: 1001, Kind: "main", Host: "pc"}}, in("Severance (2022)", "Season 02")},
		{"no media", nil, -1, nil, lib},
	} {
		if got := savePath(cfg, tc.media, tc.season, tc.files); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	cfg.Torrent.CreateSubfolder = false
	if got := savePath(cfg, show, 2, nil); got != lib {
		t.Errorf("no subfolders: %q", got)
	}
	cfg.Library.Dir = ""
	if got := savePath(cfg, show, 2, nil); got != "" {
		t.Errorf("no library: %q", got)
	}
}

func TestResultSeason(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  int
		want int
	}{
		{"Show.S02E05.1080p", -1, 2},
		{"Show - Episode Title", 3004, 3},
		{"Show.S02E01-E04.1080p", -1, 2},
		{"Show.S02.1080p", -1, 2},
		{"Show Season 3 Complete", -1, 3},
		{"Show.S01-S03.1080p", -1, -1},
		{"Show Complete Series", -1, -1},
		{"Show 1080p", -1, 7},
	} {
		r := &SearchResult{Name: tc.name, EpisodeNumber: tc.key}
		annotate(r)
		if got := ResultSeason(r, 7); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}
