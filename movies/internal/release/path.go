package release

import (
	"regexp"
	"strings"
)

var (
	reSeasonDir   = regexp.MustCompile(`(?i)^(?:season|seizoen|saison|staffel|temporada|stagione|series|sæson|säsong|sesong|kausi|sezon|s)[ ._-]*0*(\d{1,4})(?:[ ._-]*[\[(].*)?$`)
	reSeasonOrdir = regexp.MustCompile(`(?i)^(\d{1,2})(?:st|nd|rd|th)[ ._-]*season$`)
	reSpecialsDir = regexp.MustCompile(`(?i)^(?:specials?|sp|season[ ._-]*0+|s0+)$`)
	rePartDir     = regexp.MustCompile(`(?i)^(?:cd|disc|disk|dvd|part|pt)[ ._-]*(\d{1,2})$`)
	reGenericDir  = regexp.MustCompile(`(?i)^(?:movies?|films?|tv|tv[ ._-]?shows?|shows?|series|tv[ ._-]?series|television|videos?|my[ ._-]videos|media|downloads?|completed?|torrents?|incoming|library|libraries|anime|kids|documentar(?:y|ies)|4k|uhd|hd|sd|\d{3,4}p|new folder(?: \(\d+\))?|subs?|subtitles?|fonts?|video_ts|audio_ts|bdmv|stream|playlist|backup|certificate|clipinf|home|users?|mnt|volumes|srv|data|storage|nas|shares?|public|plex|jellyfin|emby|kodi|desktop|documents|unsorted|seen|watched|unwatched|done|temp|tmp|_?unpack|[a-z]:)$`)
	reGenericFile = regexp.MustCompile(`(?i)^(?:movie|film|video|episode|feature|main|main feature|title|untitled|default|index|output|new|vts[ _]\d+[ _]\d+|\d{5,}|0\d*|title[ _]?t?\d+|t\d{2}|track[ _]?\d+)$`)
	reLeadingEp   = regexp.MustCompile(`^\s*(\d{1,3})(?:\s*[-.]\s*|\s+|_+|$)(.*)$`)
)

// extrasDirs are the folders Plex, Jellyfin and Kodi keep extras in, and
// the extra type each one means.
var extrasDirs = map[string]string{
	"extras": "extra", "extra": "extra", "bonus": "extra", "bonus features": "extra",
	"special features": "extra", "specialfeatures": "extra", "other": "extra", "others": "extra",
	"featurettes": "featurette", "featurette": "featurette",
	"behind the scenes": "behind the scenes", "behindthescenes": "behind the scenes", "making of": "behind the scenes",
	"deleted scenes": "deleted scene", "deletedscenes": "deleted scene", "deleted": "deleted scene",
	"trailers": "trailer", "trailer": "trailer", "interviews": "interview", "interview": "interview",
	"shorts": "short", "short": "short", "samples": "sample", "sample": "sample",
	"scenes": "scene", "scene": "scene", "clips": "extra", "clip": "extra",
}

// maxDirs is how many folders above a file ParsePath looks at.
const maxDirs = 16

// splitPath splits a path on both '/' and '\', whatever the OS.
func splitPath(path string) []string {
	var parts []string
	for _, p := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if p = strings.TrimSpace(p); p != "" && p != "." && p != ".." {
			parts = append(parts, p)
		}
	}
	return parts
}

// seasonDir reads a season folder: "Season 01", "S2", "Staffel 3",
// "Season 1 (2019)", "2nd Season"; "Specials" is season 0.
func seasonDir(d string) (int, bool) {
	if reSpecialsDir.MatchString(d) {
		return 0, true
	}
	if m := reSeasonDir.FindStringSubmatch(d); m != nil {
		return atoi(m[1]), true
	}
	if m := reSeasonOrdir.FindStringSubmatch(d); m != nil {
		return atoi(m[1]), true
	}
	return 0, false
}

func extrasDir(d string) (string, bool) {
	k := strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(d))), " ")
	ex, ok := extrasDirs[k]
	return ex, ok
}

// isGenericTitle reports whether a parsed file title says nothing about
// what the file is ("movie.mkv", "VTS_01_1.VOB", "00001.m2ts").
func isGenericTitle(t string) bool {
	return t == "" || reGenericFile.MatchString(strings.TrimSpace(t))
}

// hasTech reports whether a folder name looked like a release name rather
// than a plain title folder ("Show (2008)").
func (i Info) hasTech() bool {
	return i.Resolution != "" || i.Source != "" || i.Codec != "" || i.Group != "" ||
		len(i.Seasons) > 0 || len(i.Episodes) > 0 || len(i.Audio) > 0 || len(i.HDR) > 0
}

// ParsePath reads a library file path, combining the file name with its
// folders:
//
//	Breaking Bad (2008)/Season 01/01 - Pilot.mkv  → "Breaking Bad" 2008 S01E01 "Pilot"
//	Movies/The Matrix (1999)/movie.mkv            → "The Matrix" 1999
//	Show/Specials/Show - S00E02.mkv               → season 0
//	The Matrix (1999)/Featurettes/Making Of.mkv   → Extra "featurette"
//	Show.S01.1080p.WEB-DL-GRP/Show.S01E01.mkv     → 1080p WEB-DL from the folder
//
// '/' and '\' both separate folders on every OS. Folders like "Movies",
// "TV Shows", "Downloads" or a drive letter are skipped, but any other
// folder can supply the title, so pass the path relative to the library
// root when there is one.
func ParsePath(path string) Info {
	parts := splitPath(path)
	if len(parts) == 0 {
		return Info{}
	}
	file, dirs := parts[len(parts)-1], parts[:len(parts)-1]
	if len(dirs) > maxDirs {
		dirs = dirs[len(dirs)-maxDirs:]
	}
	res := parse(file, nameFile)
	fileHasEpisode := res.IsEpisode()

	folderSeason, inSeasonDir := -1, false
	folderExtra, folderPart := "", 0
	var folder *Info
	for i := len(dirs) - 1; i >= 0 && folder == nil; i-- {
		d := dirs[i]
		if n, ok := seasonDir(d); ok {
			if folderSeason < 0 {
				folderSeason = n
			}
			inSeasonDir = true
			continue
		}
		if ex, ok := extrasDir(d); ok {
			if folderExtra == "" {
				folderExtra = ex
			}
			continue
		}
		if m := rePartDir.FindStringSubmatch(d); m != nil {
			if folderPart == 0 {
				folderPart = atoi(m[1])
			}
			continue
		}
		if reGenericDir.MatchString(d) {
			continue
		}
		di := parse(d, nameFolder)
		if isGenericTitle(di.Title) {
			// A title-less pack folder ("S01 1080p") still has a season.
			if folderSeason < 0 && len(di.Seasons) == 1 {
				folderSeason = di.Seasons[0]
			}
			continue
		}
		folder = &di
	}

	// Ids: the file's, else the nearest folder's.
	for i := len(dirs) - 1; i >= 0; i-- {
		var di Info
		readIDs(asciiPunct.Replace(dirs[i]), &di)
		if res.TMDBID == 0 {
			res.TMDBID = di.TMDBID
		}
		if res.IMDbID == "" {
			res.IMDbID = di.IMDbID
		}
		if res.TVDBID == 0 {
			res.TVDBID = di.TVDBID
		}
	}

	if res.Extra == "" {
		res.Extra = folderExtra
	}
	if res.Part == 0 {
		res.Part = folderPart
	}

	// Seasons from the folders.
	if len(res.Seasons) == 0 {
		switch {
		case folderSeason >= 0:
			res.Seasons = []int{folderSeason}
		case folder != nil && len(folder.Seasons) == 1:
			res.Seasons = []int{folder.Seasons[0]}
		}
	}
	seasonKnown := len(res.Seasons) == 1 && (inSeasonDir || folderSeason >= 0 || folder != nil && len(folder.Seasons) == 1)

	// "Season 01/01 - Pilot.mkv": a bare leading number is the episode.
	if !fileHasEpisode && seasonKnown && res.Extra == "" {
		name, _ := splitExt(file, true)
		if m := reLeadingEp.FindStringSubmatch(name); m != nil {
			res.Episodes = []int{atoi(m[1])}
			res.Title = ""
			res.EpisodeTitle = parse(m[2], nameFolder).Title
			res.Complete = false
			fileHasEpisode = true
		}
	}
	// "Season 02/Show - 05.mkv": the absolute number is the season's episode
	// (Absolute stays set, for shows numbered across seasons).
	if res.Absolute > 0 && len(res.Episodes) == 0 && seasonKnown {
		res.Episodes = []int{res.Absolute}
	}

	// The title.
	fileTitle := res.Title
	ft := ""
	if folder != nil {
		ft = folder.Title
		// Scene files are often named "group-title.mkv" inside the
		// "Title.2019.1080p...-GROUP" folder.
		if g := strings.ToLower(folder.Group); g != "" {
			lt := strings.ToLower(fileTitle)
			switch {
			case strings.HasPrefix(lt, g+"-"):
				fileTitle = fileTitle[len(g)+1:]
			case strings.HasSuffix(lt, "-"+g):
				fileTitle = fileTitle[:len(fileTitle)-len(g)-1]
			}
		}
	}
	if isGenericTitle(fileTitle) {
		fileTitle = ""
	}
	isTV := inSeasonDir || res.IsShow()
	useFolder := false
	switch {
	case ft == "":
	case fileTitle == "":
		useFolder = true
	case folderExtra != "" && res.Extra != "":
		useFolder = true // the extra belongs to the folder's movie or show
	case isTV && (inSeasonDir || !folder.hasTech()):
		useFolder = true // "Show (2008)/..." names the show better than "BB.S01E01"
	case !isTV && (res.Year == 0 || res.Year == folder.Year) && Similarity(fileTitle, ft) >= 0.8:
		useFolder = true // the same title, often better written: "Movie (2019)/movie.2019.mkv"
	case !isTV && folder.Year > 0 && res.Year == 0 && Similarity(fileTitle, ft) < 0.5:
		useFolder = true // "Movie (2019)/mov-grp.mkv"
	}
	if useFolder {
		if fileTitle != "" && res.EpisodeTitle == "" && (res.Extra != "" || !fileHasEpisode) &&
			Similarity(fileTitle, ft) < 0.8 {
			res.EpisodeTitle = fileTitle
		}
		res.Title = ft
		if folder.Year > 0 && (res.Year == 0 || isTV) {
			res.Year = folder.Year
		}
	} else {
		res.Title = fileTitle
		if res.Year == 0 && folder != nil && folder.Year > 0 && Similarity(fileTitle, ft) >= 0.8 {
			res.Year = folder.Year
		}
	}

	// Release details the file name left out, from a release folder.
	if folder != nil {
		mergeTech(&res, *folder)
	}
	return res
}

// mergeTech fills the technical fields res lacks from a folder's.
func mergeTech(res *Info, f Info) {
	if res.Resolution == "" {
		res.Resolution = f.Resolution
	}
	if res.Source == "" {
		res.Source = f.Source
	}
	if res.Codec == "" {
		res.Codec = f.Codec
	}
	if res.BitDepth == 0 {
		res.BitDepth = f.BitDepth
	}
	if len(res.HDR) == 0 {
		res.HDR = f.HDR
	}
	if len(res.Audio) == 0 {
		res.Audio = f.Audio
	}
	if len(res.Languages) == 0 {
		res.Languages = f.Languages
	}
	if res.Edition == "" {
		res.Edition = f.Edition
	}
	if res.Service == "" {
		res.Service = f.Service
	}
	if res.Group == "" {
		res.Group = f.Group
	}
	res.Repack = res.Repack || f.Repack
	res.Proper = res.Proper || f.Proper
}
