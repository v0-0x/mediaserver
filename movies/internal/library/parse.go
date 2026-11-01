package library

import (
	"path/filepath"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/release"
)

// VideoExtensions lists every container the scanner picks up (lower case,
// with the dot). mpv can play all of them; the in-app player remuxes or
// transcodes the ones browsers can't.
var VideoExtensions = extensionSet(release.VideoExtensions)

// SubtitleExtensions are the external subtitle files picked up next to
// videos (the in-app player and mpv load them).
var SubtitleExtensions = extensionSet(release.SubtitleExtensions)

func extensionSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, e := range list {
		m[e] = true
	}
	return m
}

// IsVideo reports whether path names a video file, by its extension.
func IsVideo(path string) bool { return release.IsVideo(path) }

// File kinds (LocalFile.Kind and Parsed.Kind).
const (
	// KindMain is a movie or an episode of a show.
	KindMain = "main"
	// KindSpecial is a special of a show (season 0, or a file in its
	// Specials folder).
	KindSpecial = "special"
	// KindExtra is bonus material: a trailer, a featurette, behind the
	// scenes, deleted scenes, a sample... It is never taken for an episode
	// or for the movie, only listed with them.
	KindExtra = "extra"
	// KindPart is a later part of a movie split across files (CD2, Part
	// 2): its key is the movie's, but playing the movie starts with part 1.
	KindPart = "part"
)

// parseVersion goes up when Parse reads names differently: the files
// indexed with an older version are read again at the next scan.
//
//	2: release names (movies and shows), instead of anime names.
const parseVersion = 2

// Parsed is what a file's path says about it (see release.ParsePath),
// kept with the file in the index. Season is -1 when the name has none,
// Episode -1 when it has no episode number (for a number without a season,
// "Show - 105", it is that absolute number).
type Parsed struct {
	V            int    `json:"v"`
	Title        string `json:"title"`
	Year         int    `json:"year"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeEnd   int    `json:"episodeEnd,omitempty"` // the last episode of a multi-episode file: S01E01E02 → 2
	Absolute     int    `json:"absolute,omitempty"`   // an episode number without a season
	Date         string `json:"date,omitempty"`       // daily shows: 2024-01-02
	EpisodeTitle string `json:"episodeTitle"`
	Part         int    `json:"part"` // CD1, Part 2: a movie split across files
	// Show: the name or its folders say it's a show's (episodes, a season).
	Show  bool   `json:"show,omitempty"`
	Extra string `json:"extra,omitempty"` // trailer, featurette, sample...
	Kind  string `json:"kind"`            // main | special | extra
	// Folder is the folder that names the movie or show the file belongs
	// to ("…/Breaking Bad (2008)" for "…/Breaking Bad (2008)/Season 01/x.mkv"),
	// "" when the file is loose in the library; FolderTitle the title in
	// its name. Files are matched by folder.
	Folder       string   `json:"folder,omitempty"`
	FolderTitle  string   `json:"folderTitle"`
	ReleaseGroup string   `json:"releaseGroup"`
	Resolution   string   `json:"resolution"`
	Source       string   `json:"source"`
	Codec        string   `json:"codec,omitempty"`
	HDR          []string `json:"hdr,omitempty"`
	Audio        []string `json:"audio,omitempty"`
	Edition      string   `json:"edition,omitempty"`
	// Ids from Plex/Jellyfin/Emby tags: "Movie (2010) {tmdb-27205}",
	// "[imdbid-tt1375666]", "{tvdb-81189}".
	TMDBID int    `json:"tmdbId,omitempty"`
	IMDbID string `json:"imdbId,omitempty"`
	TVDBID int    `json:"tvdbId,omitempty"`
}

// Parse reads what a file's path says about it. roots are the library
// folders: only the folders below them are read (a folder above the
// library says nothing about the file, "/home/me/Videos"). A file outside
// them is read with its own folder only.
func Parse(path string, roots []string) Parsed {
	path = filepath.Clean(path)
	root := rootOf(path, roots)
	var rel string
	if root != "" {
		rel, _ = filepath.Rel(root, path)
	}
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		root = filepath.Dir(filepath.Dir(path))
		rel = filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path))
	}
	info := release.ParsePath(rel)
	p := Parsed{
		V:            parseVersion,
		Title:        info.Title,
		Year:         info.Year,
		Season:       -1,
		Episode:      -1,
		Absolute:     info.Absolute,
		Date:         info.Date,
		EpisodeTitle: info.EpisodeTitle,
		Part:         info.Part,
		Extra:        info.Extra,
		Show:         info.IsShow() || len(info.Seasons) > 0,
		ReleaseGroup: info.Group,
		Resolution:   info.Resolution,
		Source:       info.Source,
		Codec:        info.Codec,
		HDR:          info.HDR,
		Audio:        info.Audio,
		Edition:      info.Edition,
		TMDBID:       info.TMDBID,
		IMDbID:       info.IMDbID,
		TVDBID:       info.TVDBID,
	}
	if len(info.Seasons) > 0 {
		p.Season = info.Seasons[0]
	}
	switch {
	case len(info.Episodes) > 0:
		p.Episode = info.Episodes[0]
		if last := slicesMax(info.Episodes); last > p.Episode {
			p.EpisodeEnd = last
		}
	case info.Absolute > 0:
		p.Episode = info.Absolute
	}
	switch {
	case info.Extra != "":
		p.Kind = KindExtra
	case p.Season == 0:
		p.Kind = KindSpecial
		// "Specials/Inside Breaking Bad.mkv": the special's title, found
		// among the show's specials, even when it holds the show's.
		if p.Episode < 0 && p.EpisodeTitle == "" {
			if t := release.Parse(filepath.Base(rel)).Title; release.Normalize(t) != release.Normalize(p.Title) {
				p.EpisodeTitle = t
			}
		}
	default:
		p.Kind = KindMain
	}
	p.Folder, p.FolderTitle = titleFolder(root, rel)
	return p
}

// rootOf is the library folder path is in (the deepest, when they nest),
// "" when none.
func rootOf(path string, roots []string) string {
	best := ""
	for _, r := range roots {
		r = filepath.Clean(r)
		if r == "" || r == "." {
			continue
		}
		if underAny(path, []string{r}) && path != r && len(r) > len(best) {
			best = r
		}
	}
	return best
}

// titleFolder finds, among the folders of rel (a path below root), the
// one that names the movie or show (the nearest that isn't a season,
// extras, part or generic folder), and the title it gives.
func titleFolder(root, rel string) (dir, title string) {
	parts := strings.Split(rel, string(filepath.Separator))
	for i := len(parts) - 2; i >= 0; i-- {
		if release.NamesTitle(parts[i]) {
			dir = filepath.Join(append([]string{root}, parts[:i+1]...)...)
			return dir, release.Parse(parts[i]).Title
		}
	}
	return "", ""
}

func slicesMax(xs []int) int {
	m := 0
	for _, x := range xs {
		m = max(m, x)
	}
	return m
}

// normalizedKind maps the kinds files indexed by older versions had to
// today's: Kumo's "nc" (creditless openings and other bonus videos) is an
// extra.
func normalizedKind(kind string) string {
	switch kind {
	case KindMain, KindSpecial, KindExtra, KindPart:
		return kind
	case "nc":
		return KindExtra
	}
	return KindMain
}
