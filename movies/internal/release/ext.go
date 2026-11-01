package release

import "strings"

// VideoExtensions lists every container the library picks up (lower case,
// with the dot). mpv plays all of them; the in-app player remuxes or
// transcodes the ones browsers can't.
var VideoExtensions = []string{
	".mkv", ".mk3d", ".mp4", ".m4v", ".avi", ".webm", ".mov", ".qt", ".wmv", ".asf",
	".flv", ".f4v", ".ts", ".m2ts", ".mts", ".m2t", ".tp", ".trp", ".ogv", ".ogm",
	".mpg", ".mpeg", ".mpe", ".mpv", ".m1v", ".m2v", ".vob", ".evo", ".3gp", ".3g2",
	".rm", ".rmvb", ".divx", ".xvid", ".dv", ".mxf", ".nut", ".wtv", ".dvr-ms", ".amv",
	".y4m", ".hevc", ".h264", ".h265", ".264", ".265", ".av1", ".ivf", ".bik", ".nsv",
	".viv", ".flc", ".fli", ".gifv",
}

// SubtitleExtensions lists the external subtitle files picked up next to
// videos (lower case, with the dot).
var SubtitleExtensions = []string{
	".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup", ".smi", ".sami", ".ttml", ".dfxp",
}

var videoExt, subtitleExt = extSet(VideoExtensions), extSet(SubtitleExtensions)

func extSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, e := range list {
		m[e] = true
	}
	return m
}

// IsVideo reports whether path names a video file, by its extension.
func IsVideo(path string) bool { return videoExt[Ext(path)] }

// IsSubtitle reports whether path names a subtitle file, by its extension.
func IsSubtitle(path string) bool { return subtitleExt[Ext(path)] }

// Ext is the lower-case extension of the last element of path, dot
// included ("" when there is none). Unlike filepath.Ext it treats both '/'
// and '\' as separators on every OS, so Windows paths read the same on
// Linux, and it ignores "extensions" with spaces ("Mr. Robot").
func Ext(path string) string {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	i := strings.LastIndexByte(base, '.')
	if i < 0 || i == len(base)-1 {
		return ""
	}
	e := strings.ToLower(base[i:])
	if strings.ContainsAny(e, " \t") {
		return ""
	}
	return e
}

// downloadSuffixes are what clients append to files still downloading
// (and .torrent files themselves, and the files that come with releases);
// they hide the real extension or would read as part of the group.
var downloadSuffixes = map[string]bool{
	".torrent": true, ".part": true, ".!qb": true, ".!ut": true, ".crdownload": true,
	".nfo": true, ".sfv": true, ".nzb": true, ".srr": true, ".txt": true, ".jpg": true, ".png": true,
}

// tagExtensions are video extensions that are also release tags: a
// release name "Movie.2019.DVDRip.XviD" or "Movie.2019.TS" has none.
var tagExtensions = map[string]bool{
	".ts": true, ".dv": true, ".xvid": true, ".divx": true, ".264": true, ".265": true,
	".h264": true, ".h265": true, ".hevc": true, ".av1": true, ".evo": true, ".rm": true,
}

// splitExt removes a known media, subtitle or download extension from a
// file name. container is the media/subtitle extension without its dot.
// Unless file is set (a name read from disk), an extension that is also a
// tag is only taken as one when written in lower case and not as "H.264".
func splitExt(name string, file bool) (rest, container string) {
	rest = name
	for downloadSuffixes[Ext(rest)] {
		rest = rest[:len(rest)-len(Ext(rest))]
	}
	e := Ext(rest)
	if !file && tagExtensions[e] {
		raw := rest[len(rest)-len(e):]
		before := strings.ToLower(rest[:len(rest)-len(e)])
		if raw != strings.ToLower(raw) || strings.HasSuffix(before, "h") || strings.HasSuffix(before, "x") {
			return rest, ""
		}
	}
	switch {
	case videoExt[e]:
		return rest[:len(rest)-len(e)], e[1:]
	case subtitleExt[e]:
		rest = rest[:len(rest)-len(e)]
		// Drop up to three language/flag parts: "Movie.2019.pt-BR.forced.srt".
		for range 3 {
			i := strings.LastIndexByte(rest, '.')
			if i <= 0 || !isSubtitleTag(rest[i+1:]) {
				break
			}
			rest = rest[:i]
		}
		return rest, e[1:]
	}
	return rest, ""
}

// isSubtitleTag reports whether a dot-separated part of a subtitle file
// name is a language or a flag rather than part of the title.
func isSubtitleTag(seg string) bool {
	l := strings.ToLower(seg)
	switch l {
	case "forced", "sdh", "cc", "hi", "default", "full", "signs", "songs", "commentary",
		"english", "french", "german", "spanish", "italian", "portuguese", "brazilian",
		"dutch", "russian", "japanese", "korean", "chinese", "arabic", "hebrew", "turkish",
		"polish", "swedish", "norwegian", "danish", "finnish", "greek", "czech",
		"hungarian", "romanian", "thai", "vietnamese", "indonesian", "hindi", "ukrainian":
		return true
	}
	// ISO 639-1/2 codes, optionally with a region: en, eng, pt-BR, zh_Hans.
	main, region, _ := strings.Cut(strings.ReplaceAll(l, "_", "-"), "-")
	if len(main) < 2 || len(main) > 3 || !isLowerASCII(main) {
		return false
	}
	return region == "" || len(region) >= 2 && len(region) <= 4 && isLowerASCII(region)
}

func isLowerASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return s != ""
}
