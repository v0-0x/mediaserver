// Package release parses movie and TV file names, library paths and
// torrent release names: "The.Matrix.1999.1080p.BluRay.x264-GROUP",
// "House.of.the.Dragon.S02E01.2160p.WEB.H265-GROUP",
// "Breaking Bad (2008)/Season 01/01 - Pilot.mkv".
//
// It works on byte offsets: the name is "prepared" into a copy of the same
// length where dots and underscores used as separators become spaces, tags
// and episode markers are found in that copy, and the title is what comes
// before the first tag that can't be a title word. Words that can be both
// ("Web", "French", "Extended") only count as tags after the title, or when
// their spelling gives them away (WEB, FRENCH, EXTENDED).
package release

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Info is what a name says about a release.
type Info struct {
	Title        string   `json:"title"`                  // "The Matrix", "Ocean's Eleven", "9-1-1"
	Year         int      `json:"year,omitempty"`         // release year, 0 if none
	Seasons      []int    `json:"seasons,omitempty"`      // S01 → [1]; S01-S03 → [1 2 3]; specials → [0]
	Episodes     []int    `json:"episodes,omitempty"`     // E05 → [5]; S01E01-E03 → [1 2 3]
	Absolute     int      `json:"absolute,omitempty"`     // "Show - 105": an episode number without a season
	Date         string   `json:"date,omitempty"`         // daily shows: "2024-01-02"
	EpisodeTitle string   `json:"episodeTitle,omitempty"` // for extras found by ParsePath: the extra's own name
	Complete     bool     `json:"complete,omitempty"`     // "Complete Series", "COMPLETE", a season pack
	Resolution   string   `json:"resolution,omitempty"`   // "2160p", "1080p", "720p", "576p", "480p"...
	Source       string   `json:"source,omitempty"`       // BluRay, Remux, WEB-DL, WEBRip, HDTV, DVDRip, BDRip, HDRip, CAM, TS...
	Codec        string   `json:"codec,omitempty"`        // x264, x265, AV1, VP9, XviD...
	BitDepth     int      `json:"bitDepth,omitempty"`     // 8, 10, 12
	HDR          []string `json:"hdr,omitempty"`          // HDR, HDR10, HDR10+, DV, HLG
	Audio        []string `json:"audio,omitempty"`        // DDP5.1, DD5.1, AAC2.0, Atmos, TrueHD, DTS-HD MA, FLAC...
	Languages    []string `json:"languages,omitempty"`    // MULTi, DUAL, FRENCH, GERMAN, iTA, SPANiSH...
	Edition      string   `json:"edition,omitempty"`      // "Director's Cut", "Extended, Remastered"...
	Service      string   `json:"service,omitempty"`      // streaming service: AMZN, NF, DSNP, ATVP, HMAX...
	Group        string   `json:"group,omitempty"`        // release group: "RARBG", "YTS.MX", "SubsPlease"
	Repack       bool     `json:"repack,omitempty"`
	Proper       bool     `json:"proper,omitempty"`
	Part         int      `json:"part,omitempty"`      // CD1/Disc 2/Part 3 of a movie split across files
	Container    string   `json:"container,omitempty"` // file extension: mkv, mp4, srt...
	Extra        string   `json:"extra,omitempty"`     // trailer|featurette|behind the scenes|deleted scene|interview|short|scene|sample|extra
	TMDBID       int      `json:"tmdbId,omitempty"`    // from {tmdb-603} / [tmdbid-603]
	IMDbID       string   `json:"imdbId,omitempty"`    // from {imdb-tt0133093} / [imdbid-tt0133093]
	TVDBID       int      `json:"tvdbId,omitempty"`    // from {tvdb-81189} / [tvdbid-81189]
}

// IsEpisode reports whether the name points at specific episodes (by
// number, absolute number or air date) rather than a movie, a season pack
// or an extra.
func (i Info) IsEpisode() bool {
	return i.Extra == "" && (len(i.Episodes) > 0 || i.Absolute > 0 || i.Date != "")
}

// IsSeasonPack reports whether the name is whole seasons without episodes.
func (i Info) IsSeasonPack() bool {
	return i.Extra == "" && len(i.Seasons) > 0 && len(i.Episodes) == 0 && i.Absolute == 0 && i.Date == ""
}

// IsShow reports whether the name is TV: episodes or season packs.
func (i Info) IsShow() bool { return i.IsEpisode() || i.IsSeasonPack() }

// IsMovie reports whether the name is a movie: no season, episode,
// absolute number or date, not an extra, and not a "Complete" pack
// (a whole series or collection is never one movie).
func (i Info) IsMovie() bool {
	return i.Extra == "" && !i.Complete && len(i.Seasons) == 0 && len(i.Episodes) == 0 &&
		i.Absolute == 0 && i.Date == ""
}

// EpisodeKeys returns the episode key (season*1000+episode, see
// catalog.EpisodeKey) of every episode when exactly one season is known,
// nil otherwise. Episodes of 1000 or more have no key.
func (i Info) EpisodeKeys() []int {
	if len(i.Seasons) != 1 || len(i.Episodes) == 0 {
		return nil
	}
	keys := make([]int, 0, len(i.Episodes))
	for _, e := range i.Episodes {
		if e >= 0 && e < 1000 {
			keys = append(keys, i.Seasons[0]*1000+e)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// Parse reads one name: a torrent/release name or a file name (a known
// media, subtitle or download extension is stripped and kept in
// Container). Use ParsePath for library paths, which also reads folders.
func Parse(name string) Info { return parse(name, nameRelease) }

// maxYear is the latest plausible release year.
var maxYear = time.Now().Year() + 1

// maxName is the longest name read, in bytes (after its extension).
const maxName = 512

var (
	reIDTag      = regexp.MustCompile(`(?i)[\[{(]\s*(tmdb|tmdbid|imdb|imdbid|tvdb|tvdbid)\s*[-=:]\s*(tt\d+|\d+)\s*[\]})]`)
	reBareIMDb   = regexp.MustCompile(`(?i)[\[{(]\s*(tt\d{7,9})\s*[\]})]`)
	reEditionTag = regexp.MustCompile(`(?i)\{\s*edition\s*[-=:]\s*([^}]+)\}`)
	reWebsite    = regexp.MustCompile(`(?i)^\s*[\[(]?\s*(?:https?://)?www\.[^\s\])]+\s*[\])]?\s*(?:[-:|]\s*)?`)
	reYear       = bounded(`(?:19|20)\d\d`)
	reExtraEnd   = regexp.MustCompile(`(?i)(?:\s*-\s*)(trailer|teaser|sample|scene|clip|interview|behindthescenes|behind the scenes|deleted|deletedscene|deleted scene|featurette|short|other|extra)\s*$`)
	reSampleEnd  = regexp.MustCompile(`(?i)[\s._-]sample\s*$`)
	reSampleHead = regexp.MustCompile(`(?i)^\s*sample[\s._-]`)
)

var extraWords = map[string]string{
	"trailer": "trailer", "teaser": "trailer", "sample": "sample", "scene": "scene",
	"clip": "extra", "interview": "interview", "behindthescenes": "behind the scenes",
	"behind the scenes": "behind the scenes", "deleted": "deleted scene",
	"deletedscene": "deleted scene", "deleted scene": "deleted scene",
	"featurette": "featurette", "short": "short", "other": "extra", "extra": "extra",
}

// prepare returns a copy of name of the same length where the dots and
// underscores that separate words are spaces. Dots stay in "5.1", before a
// space ("Mr. Robot", "Vol. 2") and at the end.
func prepare(name string) string {
	b := []byte(name)
	isDigit := func(i int) bool { return i >= 0 && i < len(b) && b[i] >= '0' && b[i] <= '9' }
	for i, c := range b {
		switch c {
		case '_':
			b[i] = ' '
		case '.':
			if i+1 >= len(b) || b[i+1] == ' ' {
				continue
			}
			if isDigit(i-1) && isDigit(i+1) && !isDigit(i-2) && !isDigit(i+2) {
				continue
			}
			b[i] = ' '
		}
	}
	return string(b)
}

// asciiPunct maps look-alike punctuation to ASCII so one set of patterns
// reads every name.
var asciiPunct = strings.NewReplacer(
	"–", "-", "—", "-", "―", "-", "−", "-", "【", "[", "】", "]", "「", "[", "」", "]",
	"［", "[", "］", "]", "（", "(", "）", ")", "：", ":", "’", "'", "‘", "'", " ", " ",
)

// What a name passed to parse is.
const (
	nameRelease = iota // a release or file name (Parse)
	nameFile           // a file name read from disk: every known extension is one
	nameFolder         // a folder name: no extension
)

// parse reads one name.
func parse(name string, what int) Info {
	var info Info
	name = strings.TrimSpace(name)
	if what != nameFolder {
		name, info.Container = splitExt(name, what == nameFile)
	}
	// Real names are far shorter; this bounds the work on hostile input.
	if len(name) > maxName {
		name = strings.ToValidUTF8(name[:maxName], "")
	}
	name = asciiPunct.Replace(name)
	name = readIDs(name, &info)
	name = readExtraSuffix(name, &info)
	orig := name
	s := prepare(orig)
	p := &parser{s: s, orig: orig}
	p.run(&info)
	return info
}

// readIDs takes Plex/Jellyfin/Emby tags out of a name: {tmdb-603},
// [tmdbid-603], {imdb-tt0133093}, [imdbid-tt...], {tvdb-123},
// {edition-Director's Cut}.
func readIDs(name string, info *Info) string {
	name = reIDTag.ReplaceAllStringFunc(name, func(t string) string {
		m := reIDTag.FindStringSubmatch(t)
		switch v := m[2]; strings.ToLower(m[1]) {
		case "tmdb", "tmdbid":
			info.TMDBID, _ = strconv.Atoi(v)
		case "tvdb", "tvdbid":
			info.TVDBID, _ = strconv.Atoi(v)
		case "imdb", "imdbid":
			if !strings.HasPrefix(strings.ToLower(v), "tt") {
				v = "tt" + v
			}
			info.IMDbID = strings.ToLower(v)
		}
		return " "
	})
	name = reBareIMDb.ReplaceAllStringFunc(name, func(t string) string {
		info.IMDbID = strings.ToLower(reBareIMDb.FindStringSubmatch(t)[1])
		return " "
	})
	name = reEditionTag.ReplaceAllStringFunc(name, func(t string) string {
		info.Edition = strings.TrimSpace(reEditionTag.FindStringSubmatch(t)[1])
		return " "
	})
	return name
}

// readExtraSuffix reads the extra type that Plex and Jellyfin put at the
// end of local extras ("Movie (2019)-trailer", "Movie-behindthescenes")
// and the "sample" scene groups put around sample files.
func readExtraSuffix(name string, info *Info) string {
	if m := reExtraEnd.FindStringSubmatchIndex(name); m != nil && hasAlnum(name[:m[0]]) {
		info.Extra = extraWords[strings.ToLower(name[m[2]:m[3]])]
		return name[:m[0]]
	}
	if m := reSampleEnd.FindStringIndex(name); m != nil && hasAlnum(name[:m[0]]) {
		info.Extra = "sample"
		return name[:m[0]]
	}
	if m := reSampleHead.FindStringIndex(name); m != nil && hasAlnum(name[m[1]:]) {
		info.Extra = "sample"
		return strings.Repeat(" ", m[1]) + name[m[1]:]
	}
	return name
}

// parser holds one name being parsed. s and orig have the same length: s
// is the prepared copy the patterns read, orig keeps the original
// separators for group names and acronyms.
type parser struct {
	s, orig string
	noCase  bool // the name is all lower or all upper case: spelling says nothing

	titleStart, titleEnd int // the title zone; the title itself ends at strippedEnd
	strippedEnd          int
	epStart, epEnd       int // the episode title zone, -1 when none
	groupStart, groupEnd int // the release group's span, -1 when none
	junkEnd              int // a website prefix ends here
}

type yearCand struct{ start, end, year int }

func (p *parser) run(info *Info) {
	s := p.s
	p.epStart, p.epEnd, p.groupStart, p.groupEnd = -1, -1, -1, -1
	hasUp, hasLow := false, false
	for _, r := range p.orig {
		hasUp = hasUp || unicode.IsUpper(r)
		hasLow = hasLow || unicode.IsLower(r)
	}
	p.noCase = !(hasUp && hasLow)

	// Leading junk and bracketed groups: "www.site.com - ", "[SubsPlease] ".
	leadGroup, leadStart, leadEnd := "", -1, -1
	if loc := reWebsite.FindStringIndex(p.orig); loc != nil {
		p.junkEnd, p.titleStart = loc[1], loc[1]
	}
	for {
		i := skipSpaces(s, p.titleStart)
		if i >= len(s) || s[i] != '[' {
			break
		}
		j := strings.IndexByte(s[i:], ']')
		if j < 0 || !hasAlnum(outsideBrackets(s[i+j+1:])) {
			break
		}
		content := strings.TrimSpace(p.orig[i+1 : i+j])
		switch {
		case isWebsite(content):
			p.junkEnd = i + j + 1
		case leadGroup == "" && content != "" && !isTagText(content):
			leadGroup, leadStart, leadEnd = content, i, i+j+1
		}
		p.titleStart = i + j + 1
	}

	all := append(findTags(s), findEpisodes(s, p.orig)...)
	all = dropBefore(all, p.junkEnd)
	if leadStart >= 0 {
		all = dropOverlapping(all, leadStart, leadEnd)
	}

	// Strong tags end the title.
	var strongs []match
	for _, m := range all {
		if p.isStrong(m) {
			strongs = append(strongs, m)
		}
	}
	strongs = resolve(strongs)
	years := p.findYears(strongs)
	strongs = dropLooseBeforeYear(strongs, years)

	first := -1
	for i, m := range strongs {
		if m.start >= p.titleStart {
			first = i
			break
		}
	}
	firstStart := len(s)
	if first >= 0 {
		firstStart = strongs[first].start
	}

	// The release year: the last plausible year before the first tag (a
	// bracketed one wins: "Movie (1999) Remastered 2015"), else the first
	// one after the tags.
	var year *yearCand
	for i := range years {
		y := &years[i]
		if y.start < firstStart && (year == nil || !p.bracketed(year.start, year.end) || p.bracketed(y.start, y.end)) {
			year = y
		}
	}
	if year == nil {
		techFrom := firstStart
		if first >= 0 && strongs[first].kind.episodic() {
			techFrom = len(s)
			for _, m := range strongs[first:] {
				if !m.kind.episodic() {
					techFrom = m.start
					break
				}
			}
		}
		for i := range years {
			if years[i].start >= techFrom {
				year = &years[i]
				break
			}
		}
	}
	p.titleEnd = firstStart
	if year != nil {
		info.Year = year.year
		if year.start < p.titleEnd {
			p.titleEnd = year.start
		}
	}

	// Tags trailing the title: "Le.Film.FRENCH.1080p", "Movie (Director's Cut) (2019)".
	p.strippedEnd = p.titleEnd
	for {
		e := trimRightSeps(s, p.titleStart, p.strippedEnd)
		best := -1
		for i, m := range all {
			if m.end == e && m.start >= p.titleStart && p.trailingTag(m) && (best < 0 || m.start < all[best].start) {
				best = i
			}
		}
		if best < 0 || !keepsTitle(s[p.titleStart:all[best].start]) {
			break
		}
		p.strippedEnd = all[best].start
	}

	p.findGroup(info, all, strongs, year, leadGroup, leadStart, leadEnd)

	// The episode title: after the episode markers, up to the next tag.
	if first >= 0 && strongs[first].kind.episodic() {
		end := strongs[first].end
		next := len(s)
		hasEps := len(strongs[first].episodes) > 0 || strongs[first].kind == kDate
		for _, m := range strongs[first+1:] {
			if m.start < end {
				continue
			}
			// "S01 - 05", "Season 2 Ep 5" go together; "S01E01 eps1.0..." don't.
			loose := m.kind == kEpisode || m.kind == kAbsolute
			if m.kind.episodic() && !hasAlnum(s[end:m.start]) && !(loose && hasEps) {
				end = m.end
				hasEps = hasEps || len(m.episodes) > 0
				continue
			}
			next = m.start
			break
		}
		if p.groupStart >= end && p.groupStart < next {
			next = p.groupStart
		}
		sorted := append([]match(nil), all...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a].start < sorted[b].start })
		for _, m := range sorted {
			if m.start >= end && m.start < next && p.endsEpisodeTitle(m) {
				next = m.start
				break
			}
		}
		p.epStart, p.epEnd = end, next
	}

	// Everything outside the title and the episode title is read as tags.
	var tags []match
	for _, m := range all {
		inTitle := m.start >= p.titleStart && m.start < p.strippedEnd
		inEp := p.epStart >= 0 && m.start >= p.epStart && m.start < p.epEnd
		inGroup := p.groupStart >= 0 && m.start < p.groupEnd && m.end > p.groupStart
		if inTitle || inEp || inGroup {
			continue
		}
		if year != nil && m.start < year.end && m.end > year.start {
			continue
		}
		tags = append(tags, m)
	}
	tags = resolve(tags)
	p.fill(info, tags)

	info.Title = cleanText(s, p.orig, p.titleStart, p.strippedEnd)
	if p.epStart >= 0 && (len(info.Episodes) > 0 || info.Absolute > 0 || info.Date != "") {
		info.EpisodeTitle = cleanText(s, p.orig, p.epStart, p.epEnd)
	}
	// "trailer.mkv", "Sample.mkv", "Making Of.mkv": the whole name is the
	// extra's type.
	if info.Extra == "" && info.Year == 0 && len(info.Seasons) == 0 && !info.IsEpisode() {
		a, b := skipSpaces(s, p.titleStart), trimRightSeps(s, p.titleStart, p.strippedEnd)
		for _, m := range all {
			if m.kind == kExtra && m.start == a && m.end == b && m.val != "extra" && m.val != "interview" {
				info.Extra, info.Title = m.val, ""
			}
		}
	}
}

// isStrong reports whether m ends the title wherever it is.
func (p *parser) isStrong(m match) bool {
	switch m.class {
	case strong:
		return true
	case capsStrong:
		return !p.noCase && caseOK(p.s[m.start:m.end])
	}
	return false
}

// trailingTag reports whether m, right at the end of the title zone, is a
// tag rather than the title's last word.
func (p *parser) trailingTag(m match) bool {
	switch m.class {
	case weak:
		return true
	case weakAmb, capsStrong:
		return !p.noCase && caseOK(p.s[m.start:m.end]) || p.bracketed(m.start, m.end)
	}
	return false
}

// endsEpisodeTitle reports whether m, inside the episode title zone, starts
// the tags: "Show.S01E01.Pilot.REPACK.1080p".
func (p *parser) endsEpisodeTitle(m match) bool {
	t := p.s[m.start:m.end]
	switch m.class {
	case weak, strong:
		return true
	case weakAmb, capsStrong:
		return caseOK(t) || p.bracketed(m.start, m.end)
	case techOnly:
		return m.kind != kPart && strings.ToUpper(t) == t && caseOK(t)
	}
	return false
}

// bracketed reports whether [start,end) is the first thing inside brackets.
func (p *parser) bracketed(start, end int) bool {
	i := start - 1
	for i >= 0 && p.s[i] == ' ' {
		i--
	}
	return i >= 0 && (p.s[i] == '(' || p.s[i] == '[' || p.s[i] == '{')
}

// findYears lists the plausible years that could be the release year:
// not inside another tag, and with some title before them ("1917" alone
// is a title).
func (p *parser) findYears(strongs []match) []yearCand {
	var out []yearCand
	eachWord(reYear, p.s, wordStarts(p.s), func(loc []int) {
		y, _ := strconv.Atoi(p.s[loc[2]:loc[3]])
		if y < 1900 || y > maxYear || loc[2] < p.titleStart || !hasAlnum(p.s[p.titleStart:loc[2]]) {
			return
		}
		for _, m := range strongs {
			if m.start < loc[3] && m.end > loc[2] {
				return
			}
		}
		out = append(out, yearCand{loc[2], loc[3], y})
	})
	return out
}

// dropLooseBeforeYear drops "Episode 4" and " - 2" markers followed by a
// year when the name has no season: "Star Wars Episode 4 A New Hope 1977",
// "Movie - 2 (2019)" are titles.
func dropLooseBeforeYear(strongs []match, years []yearCand) []match {
	for _, m := range strongs {
		if m.kind == kEpisodes || m.kind == kSeason || m.kind == kDate {
			return strongs
		}
	}
	out := strongs[:0:0]
	for _, m := range strongs {
		if m.kind == kEpisode || m.kind == kAbsolute {
			later := false
			for _, y := range years {
				later = later || y.start >= m.end
			}
			if later {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// fill copies the tags into info.
func (p *parser) fill(info *Info, tags []match) {
	var seasons, episodes []int
	var loose []match
	var editions []string
	if info.Edition != "" {
		editions = append(editions, info.Edition)
	}
	audioAt := map[int]int{} // tag index → index in info.Audio
	for i, m := range tags {
		switch m.kind {
		case kEpisodes:
			seasons = addAll(seasons, m.seasons)
			episodes = addAll(episodes, m.episodes)
		case kSeason:
			seasons = addAll(seasons, m.seasons)
		case kEpisode, kAbsolute:
			loose = append(loose, m)
		case kDate:
			if info.Date == "" {
				info.Date = m.date
			}
		case kComplete:
			info.Complete = true
		case kResolution:
			if info.Resolution == "" {
				info.Resolution = m.val
			}
		case kSource:
			if info.Source == "" || m.val == "Remux" {
				info.Source = m.val
			}
			if strings.Contains(strings.ToLower(p.s[m.start:m.end]), "uhd") && info.Resolution == "" {
				info.Resolution = "2160p"
			}
		case kCodec:
			if info.Codec == "" {
				info.Codec = m.val
			}
		case kBitDepth:
			if info.BitDepth == 0 {
				info.BitDepth = m.n
			}
		case kHDR:
			info.HDR = addString(info.HDR, m.val)
		case kAudio:
			info.Audio = addString(info.Audio, m.val)
			audioAt[i] = indexOf(info.Audio, m.val)
			if strings.HasPrefix(strings.ToLower(p.s[m.start:m.end]), "ddpa") {
				info.Audio = addString(info.Audio, "Atmos") // DDPA5.1: DD+ with Atmos
			}
		case kChannels:
			// "TrueHD 7.1", "TrueHD Atmos 7.1": the channels of the codec
			// written right before them.
			j := i - 1
			for j > 0 && tags[j].kind == kAudio && tags[j].val == "Atmos" && !hasAlnum(p.s[tags[j-1].end:tags[j].start]) {
				j--
			}
			if a, ok := audioAt[j]; ok && j >= 0 && !hasAlnum(p.s[tags[j].end:tags[j+1].start]) &&
				!strings.ContainsAny(info.Audio[a], "0123456789") && info.Audio[a] != "Atmos" {
				if strings.ContainsAny(info.Audio[a], " :") {
					info.Audio[a] += " "
				}
				info.Audio[a] += m.val
			} else {
				info.Audio = addString(info.Audio, m.val)
			}
		case kLanguage:
			info.Languages = addString(info.Languages, m.val)
		case kEdition:
			editions = addString(editions, m.val)
		case kRepack:
			info.Repack = true
		case kProper:
			info.Proper = true
		case kExtra:
			if info.Extra == "" {
				info.Extra = m.val
			}
		case kService:
			if info.Service == "" {
				info.Service = m.val
			}
		case kPart:
			if info.Part == 0 {
				info.Part = m.n
			}
		}
	}
	info.Edition = strings.Join(editions, ", ")
	sort.Ints(seasons)
	if len(episodes) == 0 && len(loose) > 0 {
		l := loose[0]
		if len(seasons) == 1 || len(l.episodes) > 1 {
			episodes = l.episodes
		} else {
			info.Absolute = l.episodes[0]
		}
	}
	info.Seasons, info.Episodes = seasons, episodes
	if len(seasons) > 0 && len(episodes) == 0 && info.Absolute == 0 && info.Date == "" {
		info.Complete = true
	}
}

// findGroup finds the release group: a leading "[Group]", a trailing
// "-GROUP" after the tags, or a trailing "[YTS.MX]"/"(YIFY)".
func (p *parser) findGroup(info *Info, all, strongs []match, year *yearCand, lead string, leadStart, leadEnd int) {
	if lead != "" {
		info.Group, p.groupStart, p.groupEnd = lead, leadStart, leadEnd
		return
	}
	s, orig := p.s, p.orig
	// tagBefore reports whether a non-episode tag (or the year, when
	// yearOK) lies between the title and position d.
	tagBefore := func(d int, yearOK bool) bool {
		if yearOK && year != nil && year.end <= d && year.start >= p.titleStart {
			return true
		}
		for _, m := range strongs {
			if m.start >= p.titleEnd && m.end <= d && !m.kind.episodic() {
				return true
			}
		}
		return false
	}

	// Peel the brackets at the end: "x264-GROUP[rarbg]", "[YTS.MX]".
	end := len(s)
	bStart, bEnd := -1, -1
	for {
		e := trimRightSpaces(s, end)
		if e == 0 || (s[e-1] != ']' && s[e-1] != ')') {
			end = e
			break
		}
		open := byte('[')
		if s[e-1] == ')' {
			open = '('
		}
		o := strings.LastIndexByte(s[:e-1], open)
		if o < 0 || o < p.titleEnd {
			end = e
			break
		}
		if bStart < 0 {
			bStart, bEnd = o, e
		}
		end = o
	}

	// "-GROUP" / " - GROUP" right before them.
	ts := end
	for ts > 0 && isGroupByte(orig[ts-1]) {
		ts--
	}
	if ts < end {
		d, spaced := -1, false
		switch {
		case ts >= 1 && orig[ts-1] == '-':
			d = ts - 1
		case ts >= 3 && orig[ts-1] == ' ' && orig[ts-2] == '-' && orig[ts-3] == ' ':
			d, spaced = ts-2, true
		}
		tok := strings.Trim(orig[ts:end], ".")
		ok := d >= p.titleEnd && d > p.titleStart && hasLetter(tok) && !isTagText(tok) && !isWebsite(tok)
		for _, m := range all {
			if ok && m.start < d && m.end > d && m.kind != kPart {
				ok = false // the dash of WEB-DL, DTS-HD...
			}
		}
		if ok {
			if spaced {
				ok = tagBefore(d, false)
			} else {
				ok = tagBefore(d, true) || endsAMatch(strongs, s, d)
			}
		}
		if ok {
			info.Group, p.groupStart, p.groupEnd = tok, d, end
			return
		}
	}

	// "[YTS.MX]", "(YIFY)", "[rarbg]".
	if bStart >= 0 {
		content := strings.TrimSpace(orig[bStart+1 : bEnd-1])
		if content != "" && !strings.ContainsAny(content, " ,") && hasLetter(content) &&
			!isTagText(content) && !isWebsite(content) && (tagBefore(bStart, false) || !isTitleWord(content)) {
			info.Group, p.groupStart, p.groupEnd = content, bStart, bEnd
		}
	}
}

// endsAMatch reports whether a strong tag ends right before d, brackets
// aside: "x264-GROUP", "[x264]-GROUP", "S01E01-GROUP".
func endsAMatch(strongs []match, s string, d int) bool {
	for _, m := range strongs {
		if m.end <= d && strings.Trim(s[m.end:d], "[](){} ") == "" {
			return true
		}
	}
	return false
}

func isGroupByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '@' || c == '&' || c == '!' || c >= 0x80
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

var reDomain = regexp.MustCompile(`(?i)(?:^|\.)(?:www\.|[a-z0-9-]+\.(?:com|org|net|to|cc|info|ws|xyz|club|io|lol|nz|re|rs|cx|pw|ru|vip|biz|link|top|site|online|cd|se|si|li|ch|unblockit|fun|win|cam|onl|ms|bz|ph|tw|pics))$`)

// isWebsite reports whether s names a website ("www.site.com",
// "eztv.re") rather than a group ("YTS.MX" counts as a group).
func isWebsite(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(strings.ToLower(s), "www.") || strings.Contains(s, "://") ||
		!strings.Contains(s, " ") && reDomain.MatchString(s)
}

// isTagText reports whether s (a bracket's content, a dash group) holds a
// tag, a year or an episode marker rather than a group name.
func isTagText(s string) bool {
	s = strings.TrimSpace(s)
	if y, err := strconv.Atoi(s); err == nil && y >= 1900 && y <= maxYear {
		return true
	}
	p := prepare(s)
	noCase := strings.ToUpper(s) == s || strings.ToLower(s) == s
	for _, m := range append(findTags(p), findEpisodes(p, s)...) {
		switch {
		case m.class == strong && m.kind != kHash:
			return true
		case m.class == capsStrong && !noCase && caseOK(p[m.start:m.end]):
			return true
		}
	}
	return false
}

// resolve keeps the longest of overlapping matches, leftmost first.
func resolve(ms []match) []match {
	sorted := append([]match(nil), ms...)
	sort.SliceStable(sorted, func(a, b int) bool {
		x, y := sorted[a], sorted[b]
		if x.start != y.start {
			return x.start < y.start
		}
		if x.end != y.end {
			return x.end > y.end
		}
		return x.class < y.class
	})
	var out []match
	end := -1
	for _, m := range sorted {
		if m.start >= end {
			out = append(out, m)
			end = m.end
		}
	}
	return out
}

func dropBefore(ms []match, pos int) []match {
	if pos <= 0 {
		return ms
	}
	out := ms[:0]
	for _, m := range ms {
		if m.start >= pos {
			out = append(out, m)
		}
	}
	return out
}

func dropOverlapping(ms []match, start, end int) []match {
	out := ms[:0]
	for _, m := range ms {
		if m.start >= end || m.end <= start {
			out = append(out, m)
		}
	}
	return out
}

func addAll(list, more []int) []int {
	for _, n := range more {
		found := false
		for _, l := range list {
			found = found || l == n
		}
		if !found {
			list = append(list, n)
		}
	}
	return list
}

func addString(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, l := range list {
		if l == v {
			return list
		}
	}
	return append(list, v)
}

func indexOf(list []string, v string) int {
	for i, l := range list {
		if l == v {
			return i
		}
	}
	return -1
}

func skipSpaces(s string, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return i
}

func trimRightSpaces(s string, end int) int {
	for end > 0 && s[end-1] == ' ' {
		end--
	}
	return end
}

// trimRightSeps moves end left over separators and brackets.
func trimRightSeps(s string, start, end int) int {
	for end > start && strings.IndexByte(" -_.,;:~+|/()[]{}", s[end-1]) >= 0 {
		end--
	}
	return end
}

// outsideBrackets drops the bracketed parts of s.
func outsideBrackets(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

var articles = map[string]bool{
	"the": true, "a": true, "an": true, "le": true, "la": true, "les": true, "l": true,
	"der": true, "die": true, "das": true, "el": true, "los": true, "las": true, "il": true, "lo": true,
}

// keepsTitle reports whether what is left of a title after removing a
// trailing tag is still a title (not empty, not just "The").
func keepsTitle(rest string) bool {
	n := Normalize(rest)
	return n != "" && !articles[n]
}

// cleanText turns s[a:b] into display text: separators trimmed, empty
// brackets dropped, and dotted acronyms put back ("S.W.A.T.").
func cleanText(s, orig string, a, b int) string {
	if a >= b {
		return ""
	}
	var words []string
	var run []string
	flush := func() {
		if len(run) >= 2 {
			words = append(words, strings.Join(run, ".")+".")
		} else {
			words = append(words, run...)
		}
		run = run[:0]
	}
	for i := a; i < b; {
		if s[i] == ' ' {
			i++
			continue
		}
		j := i
		for j < b && s[j] != ' ' {
			j++
		}
		w := s[i:j]
		// A single letter followed by a dot in the original name.
		letter := strings.TrimSuffix(w, ".")
		if len(letter) == 1 && isLetterByte(letter[0]) && i+1 < len(orig) && orig[i+1] == '.' {
			run = append(run, letter)
		} else {
			flush()
			words = append(words, w)
		}
		i = j
	}
	flush()
	return tidy(strings.Join(words, " "))
}

func isLetterByte(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// tidy trims separators and unbalanced brackets from both ends of t and
// drops empty brackets.
func tidy(t string) string {
	for _, e := range []string{"()", "[]", "{}", "( )", "[ ]", "{ }"} {
		t = strings.ReplaceAll(t, e, " ")
	}
	t = strings.Join(strings.Fields(t), " ")
	for {
		before := t
		t = strings.Trim(t, " -_,;:~+|/")
		t = strings.TrimLeft(t, ".")
		if strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "..") {
			if r := []rune(t); len(r) >= 2 && !unicode.IsLetter(r[len(r)-2]) {
				t = strings.TrimSuffix(t, ".")
			}
		}
		if t != "" {
			last, firstC := t[len(t)-1], t[0]
			opens := strings.Count(t, "(") + strings.Count(t, "[") + strings.Count(t, "{")
			closes := strings.Count(t, ")") + strings.Count(t, "]") + strings.Count(t, "}")
			switch {
			case strings.IndexByte("([{", last) >= 0:
				t = t[:len(t)-1]
			case strings.IndexByte(")]}", firstC) >= 0:
				t = t[1:]
			case strings.IndexByte(")]}", last) >= 0 && closes > opens:
				t = t[:len(t)-1]
			case strings.IndexByte("([{", firstC) >= 0 && opens > closes:
				t = t[1:]
			}
		}
		if t == before {
			return t
		}
	}
}
