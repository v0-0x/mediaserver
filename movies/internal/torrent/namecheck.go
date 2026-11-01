package torrent

// A small reader of movie and TV release names
// ("Show.S01E02.1080p.WEB.h264-GROUP", "Show S01-S03 Complete 720p",
// "Movie (1999) [1080p] [BluRay] [YTS]"): enough to tell which episode or
// seasons a torrent holds, its year, resolution and group, so the search
// sources can label results and drop those of another episode. The
// release package's full parser can replace it: checkName is the only
// entry point.

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// nameFacts is what a release name says about its content.
type nameFacts struct {
	// Season and Episode of an episode, EpisodeTo the last one of a range
	// (S01E01-E03); Episode is -1 when the name gives none.
	Season, Episode, EpisodeTo int
	// Pack: several episodes (a range, a season, seasons, a whole show).
	Pack bool
	// SeasonFrom and SeasonTo are the seasons of a pack; both 0 when the
	// name doesn't say ("Show Complete Series").
	SeasonFrom, SeasonTo int
	// Years in the name, in order, except one that starts it (the title
	// of "1917" or "2012").
	Years      []int
	Resolution string // 2160p, 1080p, 720p, 576p, 480p or ""
	Group      string
}

var (
	// S01E02, S01E02E03, S01E02-E03, S01E02-03, S1 E2.
	ncEpisode = regexp.MustCompile(`\bs(\d{1,3}) ?e(\d{1,4})((?: ?-? ?e\d{1,4}| ?- ?\d{1,4}\b)*)`)
	// 1x02.
	ncCross = regexp.MustCompile(`\b(\d{1,2})x(\d{2,3})\b`)
	// Season 1 Episode 2.
	ncSeasonEpisode = regexp.MustCompile(`\b(?:season|saison|staffel|temporada|stagione) ?(\d{1,3}) ?(?:episode|ep|episodio|folge) ?(\d{1,4})\b`)
	// S01-S03, S01-03, Seasons 1-3, Season 1 to 3.
	ncSeasonRange = regexp.MustCompile(`\b(?:s|(?:season|saison|staffel|temporada|stagione)s? ?)(\d{1,2}) ?(?:-|~|to|thru|through|&|and) ?(?:s|(?:season|saison|staffel|temporada|stagione)s? ?)?(\d{1,2})\b`)
	// S01, Season 1.
	ncSeason   = regexp.MustCompile(`\b(?:s(\d{1,2})|(?:season|saison|staffel|temporada|stagione) ?(\d{1,3}))\b`)
	ncComplete = regexp.MustCompile(`\b(?:complete|integrale)\b`)
	ncDigits   = regexp.MustCompile(`\d+`)
	ncYear     = regexp.MustCompile(`\b(?:19|20)\d\d\b`)
	ncRes      = regexp.MustCompile(`\b(2160|1080|720|576|480)[pi]`)
	ncUHD      = regexp.MustCompile(`\b(?:4k|uhd)\b`)

	ncExtension = regexp.MustCompile(`(?i)\.(?:mkv|mp4|avi|m4v|ts|wmv|torrent)$`)
	ncTrailTag  = regexp.MustCompile(`\s*(?:\[[^\]]*\]|\([^)]*\)|\{[^}]*\})\s*$`)
	ncLeadTag   = regexp.MustCompile(`^\[([^\]]{1,30})\]`)
	ncGroupWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]{1,24}$`)
	// What follows the last "-" without being a group: WEB-DL, DTS-HD, x264.
	ncNotGroup = regexp.MustCompile(`(?i)^(?:dl|hd|rip|ma|x26[45]|h26[45]|hevc|avc|aac|ac3|dts|sdr|hdr|uhd|\d+p|\d+|s\d+(?:e\d+)?|e\d+)$`)
	// A group follows release details, not a title ("Spider-Man").
	ncTech = regexp.MustCompile(`(?i)\d{3,4}[pi]|x26[45]|h\.?26[45]|hevc|xvid|divx|web|blu-?ray|bdrip|brrip|hdtv|dvdrip|remux|hdrip|dvd|\bs\d{1,3}e\d|\b(?:19|20)\d\d\b`)
)

// checkName reads a release name.
func checkName(name string) nameFacts {
	f := nameFacts{Season: -1, Episode: -1, EpisodeTo: -1}
	s := ncNormalize(name)
	f.Resolution = ncResolution(s)
	f.Group = ncGroup(name)
	f.Years = ncYears(s)

	if m := ncEpisode.FindStringSubmatch(s); m != nil {
		f.Season, f.Episode = atoiOr(m[1], -1), atoiOr(m[2], -1)
		f.EpisodeTo = f.Episode
		for _, d := range ncDigits.FindAllString(m[3], -1) {
			f.EpisodeTo = max(f.EpisodeTo, atoiOr(d, -1))
		}
	} else if m := ncSeasonEpisode.FindStringSubmatch(s); m != nil {
		f.Season, f.Episode = atoiOr(m[1], -1), atoiOr(m[2], -1)
		f.EpisodeTo = f.Episode
	} else if m := ncCross.FindStringSubmatch(s); m != nil {
		f.Season, f.Episode = atoiOr(m[1], -1), atoiOr(m[2], -1)
		f.EpisodeTo = f.Episode
	}
	if f.Episode >= 0 {
		if f.EpisodeTo > f.Episode {
			f.Pack = true
			f.SeasonFrom, f.SeasonTo = f.Season, f.Season
		}
		return f
	}

	if m := ncSeasonRange.FindStringSubmatch(s); m != nil {
		if a, b := atoiOr(m[1], -1), atoiOr(m[2], -1); a >= 0 && a < b {
			f.Pack, f.SeasonFrom, f.SeasonTo = true, a, b
		}
	}
	if !f.Pack {
		if m := ncSeason.FindStringSubmatch(s); m != nil {
			n := atoiOr(m[1]+m[2], -1)
			if n >= 0 {
				f.Pack, f.SeasonFrom, f.SeasonTo = true, n, n
			}
		}
	}
	if ncComplete.MatchString(s) {
		f.Pack = true
	}
	return f
}

// singleEpisode reports whether the name is one episode's.
func (f nameFacts) singleEpisode() bool { return f.Episode >= 0 && f.EpisodeTo == f.Episode }

// coversSeason reports whether a pack holds season n.
func (f nameFacts) coversSeason(n int) bool {
	if f.SeasonFrom == 0 && f.SeasonTo == 0 {
		return true // the whole show
	}
	return n >= f.SeasonFrom && n <= f.SeasonTo
}

// ncNormalize lowercases a name and turns its separators into spaces.
func ncNormalize(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer(".", " ", "_", " ", "+", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// ncResolution finds the resolution in a normalized name.
func ncResolution(s string) string {
	if m := ncRes.FindStringSubmatch(s); m != nil {
		return m[1] + "p"
	}
	if ncUHD.MatchString(s) {
		return "2160p"
	}
	return ""
}

// ncYears lists the years of a normalized name, but not one that starts it
// (a title) nor one in the future.
func ncYears(s string) []int {
	var out []int
	last := time.Now().Year() + 1
	for _, loc := range ncYear.FindAllStringIndex(s, -1) {
		if loc[0] == 0 {
			continue
		}
		if y := atoiOr(s[loc[0]:loc[1]], 0); y <= last {
			out = append(out, y)
		}
	}
	return out
}

// ncGroup finds the release group: what follows the last "-" of a scene
// name ("…x264-GROUP[eztv].mkv"), or a leading "[Group]".
func ncGroup(name string) string {
	s := ncExtension.ReplaceAllString(strings.TrimSpace(name), "")
	for {
		t := ncTrailTag.ReplaceAllString(s, "")
		if t == s || t == "" {
			break
		}
		s = t
	}
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '-'); i >= 0 && i < len(s)-1 {
		g := strings.TrimSpace(s[i+1:])
		if ncGroupWord.MatchString(g) && !ncNotGroup.MatchString(g) && ncTech.MatchString(s[:i]) {
			return g
		}
	}
	if m := ncLeadTag.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// annotate fills what a result's name tells and its source didn't: the
// resolution, the group, the episode key (season*1000+episode, -1 when the
// torrent isn't one episode) and whether it's a pack.
func annotate(r *SearchResult) {
	f := checkName(r.Name)
	if r.Resolution == "" {
		r.Resolution = f.Resolution
	}
	if r.ReleaseGroup == "" {
		r.ReleaseGroup = f.Group
	}
	if f.Pack {
		r.IsBatch = true
	}
	if r.EpisodeNumber <= 0 || r.IsBatch {
		r.EpisodeNumber = -1
		if f.singleEpisode() && f.Episode > 0 && !r.IsBatch {
			r.EpisodeNumber = catalog.EpisodeKey(f.Season, f.Episode)
		}
	}
	if r.FormattedSize == "" && r.Size > 0 {
		r.FormattedSize = humanSize(r.Size)
	}
}

// matchesMedia reports whether a result may be what q asks for: it's
// dropped only when its name clearly says otherwise (another episode or
// season, a show's episode for a movie, a movie of another year).
func matchesMedia(r *SearchResult, q MediaQuery) bool {
	f := checkName(r.Name)
	if f.Episode < 0 && !f.Pack && r.EpisodeNumber > 0 && !r.IsBatch {
		// The source knew the episode (EZTV's API) though the name doesn't say.
		f.Season, f.Episode = catalog.SplitKey(r.EpisodeNumber)
		f.EpisodeTo = f.Episode
	}
	switch q.Type {
	case MediaMovie:
		if f.Episode > 0 || (f.Pack && f.SeasonFrom > 0) {
			return false
		}
		return yearMatches(f.Years, q)
	case MediaTV:
		return episodeMatches(f, q)
	}
	return true
}

// episodeMatches reports whether a show's release may hold what q asks for.
func episodeMatches(f nameFacts, q MediaQuery) bool {
	hasEpisode := f.Episode >= 0
	switch {
	case q.Episode > 0: // an episode: itself, a range or a pack with it
		if hasEpisode {
			return f.Season == q.Season && q.Episode >= f.Episode && q.Episode <= f.EpisodeTo
		}
		if f.Pack {
			return f.coversSeason(q.Season)
		}
	case q.Season > 0: // a season: packs with it, not single episodes
		if hasEpisode {
			return f.Season == q.Season && f.EpisodeTo > f.Episode
		}
		if f.Pack {
			return f.coversSeason(q.Season)
		}
	default: // the whole show: anything but single episodes
		if hasEpisode {
			return f.EpisodeTo > f.Episode
		}
	}
	return true
}

// yearMatches reports whether the year of a movie's release name (the last
// one that isn't part of the title: "Blade.Runner.2049.2017") is within a
// year of q's. A name without a year matches.
func yearMatches(years []int, q MediaQuery) bool {
	if q.Year <= 0 {
		return true
	}
	inTitle := map[int]bool{}
	for _, t := range append([]string{q.Title}, q.AltTitles...) {
		for _, d := range ncDigits.FindAllString(t, -1) {
			inTitle[atoiOr(d, -1)] = true
		}
	}
	for i := len(years) - 1; i >= 0; i-- {
		if inTitle[years[i]] {
			continue
		}
		d := years[i] - q.Year
		return d >= -1 && d <= 1
	}
	return true
}
