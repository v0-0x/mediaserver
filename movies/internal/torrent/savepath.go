package torrent

// Where torrents are saved
//
// Torrents go to the library folder, where the library scanner finds and
// matches them: a movie in "Title (Year)/", a show's episodes and season
// packs in "Title (Year)/Season 02/", a whole show in "Title (Year)/" (when
// Settings › Torrent client › Create a subfolder is on; else straight into
// the library folder). A title that already has files in the library
// keeps them together: its torrents go to the folder (and season folder)
// those files are in.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// SavePath is where a torrent of media goes: see savePath. season is the
// season of a show's episode or pack, -1 for a movie or a whole show.
// files are the media's files in the library, nil when there are none.
func (m *Manager) SavePath(media *catalog.Media, season int, files []*library.LocalFile) string {
	return savePath(m.settings.Get(), media, season, files)
}

// TitleFolder is the name of a movie's or show's own folder: "Title (Year)".
func TitleFolder(media *catalog.Media) string {
	t := media.PreferredTitle()
	if y := media.Year(); y > 0 {
		t = fmt.Sprintf("%s (%d)", t, y)
	}
	return util.SanitizeFilename(t)
}

// SeasonFolder is the name of a show's season folder: "Season 02", or
// "Specials" for season 0.
func SeasonFolder(season int) string {
	if season == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %02d", season)
}

// ResultSeason is the one season a show's torrent holds: an episode's, a
// range's or a season pack's. It's -1 for a pack of several seasons or of
// the whole show, and def when the name doesn't say.
func ResultSeason(r *SearchResult, def int) int {
	if r.EpisodeNumber > 0 && !r.IsBatch {
		s, _ := catalog.SplitKey(r.EpisodeNumber)
		return s
	}
	f := checkName(r.Name)
	switch {
	case f.Episode >= 0 && f.Season >= 0:
		return f.Season
	case f.Pack && f.SeasonFrom > 0 && f.SeasonFrom == f.SeasonTo:
		return f.SeasonFrom
	case f.Pack:
		return -1
	}
	return def
}

func savePath(cfg config.Settings, media *catalog.Media, season int, files []*library.LocalFile) string {
	dir := config.ExpandHome(cfg.Library.Dir)
	if dir == "" {
		return ""
	}
	lib := filepath.Clean(dir)
	if !cfg.Torrent.CreateSubfolder || media == nil || media.PreferredTitle() == "" {
		return lib
	}
	show := media.IsShow()
	if show && season >= 0 {
		if d := seasonDir(lib, files, season); d != "" {
			return d
		}
	}
	root := titleDir(lib, files, show)
	if root == "" {
		root = filepath.Join(lib, TitleFolder(media))
	}
	if show && season >= 0 {
		return filepath.Join(root, SeasonFolder(season))
	}
	return root
}

var reSeasonDir = regexp.MustCompile(`(?i)^(?:season|series|saison|staffel|temporada|stagione|s)[ ._-]*\d{1,3}$|^specials?$`)

// inLibrary reports whether d is a folder inside the library folder (not
// the library folder itself, nor a hidden one).
func inLibrary(lib, d string) bool {
	return d != lib && util.IsSubPath(lib, d) && !strings.HasPrefix(filepath.Base(d), ".")
}

// seasonDir is the season folder the files of a show's season are in
// ("…/Season 02"), "" when none is.
func seasonDir(lib string, files []*library.LocalFile, season int) string {
	return mostCommon(files, func(f *library.LocalFile) string {
		if s, _ := catalog.SplitKey(f.Episode); s != season {
			return ""
		}
		d := filepath.Clean(f.Dir)
		if !inLibrary(lib, d) || !reSeasonDir.MatchString(filepath.Base(d)) {
			return ""
		}
		return d
	})
}

// titleDir is the folder of the title its files are in, "" when none is a
// title's own. A show's is the folder above its season folders; a folder
// named like a release ("Show.S01.1080p.WEB-GROUP") is a torrent's, not
// the show's: the folder above it, if it's not the library folder, is.
func titleDir(lib string, files []*library.LocalFile, show bool) string {
	return mostCommon(files, func(f *library.LocalFile) string {
		d := filepath.Clean(f.Dir)
		if !show {
			if inLibrary(lib, d) {
				return d
			}
			return ""
		}
		for inLibrary(lib, d) {
			base := filepath.Base(d)
			if fc := checkName(base); !reSeasonDir.MatchString(base) && fc.Episode < 0 && !fc.Pack {
				return d
			}
			d = filepath.Dir(d)
		}
		return ""
	})
}

// mostCommon is the folder dirOf gives for the most files ("" ignored).
func mostCommon(files []*library.LocalFile, dirOf func(*library.LocalFile) string) string {
	counts := map[string]int{}
	best := ""
	for _, f := range files {
		if f == nil || f.Host != "" || (f.Kind != "" && f.Kind != "main") {
			continue
		}
		d := dirOf(f)
		if d == "" {
			continue
		}
		counts[d]++
		if counts[d] > counts[best] || (counts[d] == counts[best] && d < best) {
			best = d
		}
	}
	return best
}
