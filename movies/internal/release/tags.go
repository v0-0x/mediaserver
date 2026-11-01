package release

import (
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// kind is what a piece of a name says.
type kind int

const (
	kNone kind = iota
	// Episode-like markers. They end the title, and the words after them
	// up to the next tag are the episode title.
	kEpisodes // S01E02, 1x02, Season 1 Episode 2: a season and episodes
	kSeason   // S01, S01-S03, Season 2: seasons only
	kEpisode  // E05, Ep 5, Episode 5: episodes without a season
	kAbsolute // "Show - 105": an absolute number
	kDate     // 2024.01.02
	// Tags.
	kComplete // "Complete Series", COMPLETE
	kResolution
	kSource
	kCodec
	kBitDepth
	kHDR
	kAudio
	kChannels
	kLanguage
	kEdition
	kRepack
	kProper
	kExtra
	kService
	kPart
	k3D
	kMisc
	kHash
)

func (k kind) episodic() bool { return k >= kEpisodes && k <= kDate }

// class says where a tag may be trusted. Tags are always read after the
// title (the "tech zone"); before it, words like "Web" or "Extended" are
// often part of the title ("Charlotte's Web", "Extended Family").
type class int

const (
	strong     class = iota // never a title word: ends the title wherever it is (1080p, x264, S01E02)
	capsStrong              // ends the title when written in capitals (WEB, CAM, TS), a title word otherwise
	weak                    // a tag even when it trails the title (REPACK, VOSTFR, Director's Cut)
	weakAmb                 // also an ordinary word (PROPER, FRENCH, Extended): trailing tags need capitals
	techOnly                // only a tag after the title (Part 2, 5.1, ENG)
)

// match is one tag found in a name. start/end are byte offsets.
type match struct {
	start, end int
	kind       kind
	class      class
	val        string
	n          int   // a number: bit depth, part
	seasons    []int // episode-like markers
	episodes   []int
	date       string
}

type tagPattern struct {
	re    *wordRe
	kind  kind
	class class
	// val turns the submatches into the tag's value; "" drops the match.
	val func(m []string) string
}

// wordRe is a case-insensitive pattern tried at word starts. first lists
// the bytes a match can start with, so most positions are skipped without
// running the regexp (there are a hundred patterns).
type wordRe struct {
	re    *regexp.Regexp
	first [256]bool
	any   bool // the first byte can't be predicted
}

// bounded compiles p so it only matches whole words: anchored at a word
// start, and the character after it must not be a letter or a digit.
// Group 1 is the tag.
func bounded(p string) *wordRe { return compileWord(p, `(?:[^\pL\pN]|$)`) }

// wordPrefix is bounded without the check after the match, for markers
// that go on ("S01E01E02").
func wordPrefix(p string) *wordRe { return compileWord(p, "") }

func compileWord(p, after string) *wordRe {
	w := &wordRe{re: regexp.MustCompile(`^(?i)(` + p + `)` + after)}
	tree, err := syntax.Parse(p, syntax.Perl)
	if err != nil {
		panic(err)
	}
	if nullable := firstBytes(tree.Simplify(), w); nullable {
		w.any = true
	}
	return w
}

// firstBytes adds the bytes a match of re can start with to w.first (both
// cases) and reports whether re can match the empty string.
func firstBytes(re *syntax.Regexp, w *wordRe) (nullable bool) {
	add := func(r rune) {
		if r >= utf8.RuneSelf {
			w.any = true
			return
		}
		w.first[unicode.ToLower(r)], w.first[unicode.ToUpper(r)] = true, true
	}
	switch re.Op {
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return true
		}
		add(re.Rune[0])
		return false
	case syntax.OpCharClass:
		for i := 0; i+1 < len(re.Rune); i += 2 {
			if re.Rune[i+1] >= utf8.RuneSelf {
				w.any = true
			}
			for r := re.Rune[i]; r <= re.Rune[i+1] && r < utf8.RuneSelf; r++ {
				add(r)
			}
		}
		return false
	case syntax.OpCapture, syntax.OpPlus:
		return firstBytes(re.Sub[0], w)
	case syntax.OpQuest, syntax.OpStar:
		firstBytes(re.Sub[0], w)
		return true
	case syntax.OpRepeat:
		return firstBytes(re.Sub[0], w) || re.Min == 0
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !firstBytes(sub, w) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		nullable = false
		for _, sub := range re.Sub {
			nullable = firstBytes(sub, w) || nullable
		}
		return nullable
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText,
		syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return true
	}
	w.any = true
	return false
}

// wordStarts lists the positions a bounded tag can start at: the start of
// s and every position after a character that is not a letter or digit.
func wordStarts(s string) []int {
	if s == "" {
		return nil
	}
	starts := []int{0}
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && i < len(s) {
			starts = append(starts, i)
		}
	}
	return starts
}

// eachWord calls fn with the submatch indexes (into s) of the match of w
// at every word start, overlapping matches included.
func eachWord(w *wordRe, s string, starts []int, fn func(loc []int)) {
	for _, at := range starts {
		if !w.any && !w.first[s[at]] {
			continue
		}
		loc := w.re.FindStringSubmatchIndex(s[at:])
		if loc == nil {
			continue
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += at
			}
		}
		fn(loc)
	}
}

func lit(v string) func([]string) string { return func([]string) string { return v } }

// withChannels appends the channel count ("5.1") a codec was written with.
func withChannels(v string) func([]string) string {
	return func(m []string) string {
		if len(m) > 2 && m[2] != "" {
			if strings.Contains(v, " ") {
				return v + " " + m[2]
			}
			return v + m[2]
		}
		return v
	}
}

var standardHeights = map[int]bool{240: true, 360: true, 480: true, 540: true, 576: true, 720: true, 1080: true, 1440: true, 2160: true, 4320: true}

// widthResolution names a frame size by its width (crops change the height).
func widthResolution(w, h int) string {
	switch {
	case w >= 3200:
		return "2160p"
	case w >= 2400:
		return "1440p"
	case w >= 1800:
		return "1080p"
	case w >= 1200:
		return "720p"
	case h >= 570:
		return "576p"
	case w >= 400:
		return "480p"
	}
	return ""
}

const ch = `(?:[ .]?([1-9]\.[0-2]))?` // optional channels after an audio codec

var tagPatterns = []tagPattern{
	// Resolution.
	{bounded(`(\d{3,4})[pi]`), kResolution, strong, func(m []string) string {
		n, _ := strconv.Atoi(m[2])
		if !standardHeights[n] {
			return ""
		}
		return strconv.Itoa(n) + "p"
	}},
	{bounded(`(\d{3,4})x(\d{3,4})`), kResolution, strong, func(m []string) string {
		w, _ := strconv.Atoi(m[2])
		h, _ := strconv.Atoi(m[3])
		if h < 200 || w < h {
			return ""
		}
		return widthResolution(w, h)
	}},
	{bounded(`4k|uhd`), kResolution, strong, lit("2160p")},
	{bounded(`8k`), kResolution, strong, lit("4320p")},
	{bounded(`fhd`), kResolution, strong, lit("1080p")},

	// Source.
	{bounded(`(?:(?:bd|br|blu[ -]?ray|uhd)[ -]?)?remux`), kSource, strong, lit("Remux")},
	{bounded(`complete[ -](?:uhd[ -])?(?:blu[ -]?ray|bd|bdmv)`), kSource, strong, lit("BluRay")},
	{bounded(`complete[ -](?:dvd|dvdr)`), kSource, strong, lit("DVD")},
	{bounded(`blu[ -]?ray|bd(?:25|50|66|100)|bdmv|bdiso`), kSource, strong, lit("BluRay")},
	{bounded(`(?:bd|br|bluray)[ -]?rip`), kSource, strong, lit("BDRip")},
	{bounded(`bd`), kSource, capsStrong, lit("BluRay")},
	{bounded(`web[ -]?dl|web[ -]?hd|amzn[ -]?dl`), kSource, strong, lit("WEB-DL")},
	{bounded(`web[ -]?rip|web[ -]?cap`), kSource, strong, lit("WEBRip")},
	{bounded(`web`), kSource, capsStrong, lit("WEB-DL")},
	{bounded(`hd[ -]?tv(?:[ -]?rip)?`), kSource, strong, lit("HDTV")},
	{bounded(`pdtv|sdtv|dsr(?:ip)?|sat[ -]?rip|dth[ -]?rip|dvb[ -]?rip|tv[ -]?rip`), kSource, strong, lit("TVRip")},
	{bounded(`dvd[ -]?rip`), kSource, strong, lit("DVDRip")},
	{bounded(`dvd[ -]?scr(?:eener)?`), kSource, strong, lit("SCR")},
	{bounded(`dvd(?:5|9|r)?|dvd-r|ntsc|pal`), kSource, capsStrong, lit("DVD")},
	{bounded(`hd[ -]?rip`), kSource, strong, lit("HDRip")},
	{bounded(`hd[ -]?cam|cam[ -]?rip|tele[ -]?cam`), kSource, strong, lit("CAM")},
	{bounded(`cam`), kSource, capsStrong, lit("CAM")},
	{bounded(`hd[ -]?ts|tele[ -]?sync|pdvd|predvd(?:rip)?`), kSource, strong, lit("TS")},
	{bounded(`ts`), kSource, capsStrong, lit("TS")},
	{bounded(`hd[ -]?tc|tele[ -]?cine`), kSource, strong, lit("TC")},
	{bounded(`tc`), kSource, capsStrong, lit("TC")},
	{bounded(`scr|screener`), kSource, capsStrong, lit("SCR")},
	{bounded(`r5`), kSource, capsStrong, lit("R5")},
	{bounded(`ppv(?:[ -]?rip)?`), kSource, capsStrong, lit("PPV")},
	{bounded(`vhs[ -]?rip`), kSource, strong, lit("VHSRip")},
	{bounded(`workprint`), kSource, capsStrong, lit("Workprint")},

	// Video codec and bit depth.
	{bounded(`[xh][ .]?265|hevc`), kCodec, strong, lit("x265")},
	{bounded(`[xh][ .]?264|avc`), kCodec, strong, lit("x264")},
	{bounded(`av1`), kCodec, strong, lit("AV1")},
	{bounded(`vp9`), kCodec, strong, lit("VP9")},
	{bounded(`xvid(?:hd)?`), kCodec, strong, lit("XviD")},
	{bounded(`divx`), kCodec, strong, lit("DivX")},
	{bounded(`mpeg[ -]?2`), kCodec, strong, lit("MPEG2")},
	{bounded(`vc[ -]?1`), kCodec, strong, lit("VC1")},
	{bounded(`(8|10|12)[ -]?bits?`), kBitDepth, strong, func(m []string) string { return m[2] }},
	{bounded(`hi10p?`), kBitDepth, strong, lit("10")},

	// HDR.
	{bounded(`hdr10(?:\+|plus|p)`), kHDR, strong, lit("HDR10+")},
	{bounded(`hdr10`), kHDR, strong, lit("HDR10")},
	{bounded(`hdr`), kHDR, capsStrong, lit("HDR")},
	{bounded(`dolby[ .]?vision|dovi`), kHDR, strong, lit("DV")},
	{bounded(`dv`), kHDR, weakAmb, lit("DV")},
	{bounded(`hlg`), kHDR, weak, lit("HLG")},

	// Audio.
	{bounded(`(?:ddpa?|dd\+|eac3|e-ac-3|eac-3|dolby[ .]digital[ .]plus)` + ch), kAudio, strong, withChannels("DDP")},
	{bounded(`(?:dd|ac3|ac-3|dolby[ .]digital)` + `[ .]?([1-9]\.[0-2])`), kAudio, strong, withChannels("DD")},
	{bounded(`ac-?3|dolby[ .]digital`), kAudio, strong, lit("DD")},
	{bounded(`dd`), kAudio, weakAmb, lit("DD")},
	{bounded(`aac(?:[ .-]?lc)?` + ch), kAudio, strong, withChannels("AAC")},
	{bounded(`true[ -]?hd` + ch), kAudio, strong, withChannels("TrueHD")},
	{bounded(`atmos`), kAudio, weakAmb, lit("Atmos")},
	{bounded(`dts[ -]?hd[ .-]?ma` + ch + `|dts[ -]?ma` + ch), kAudio, strong, func(m []string) string {
		c := m[2]
		if c == "" && len(m) > 3 {
			c = m[3]
		}
		if c != "" {
			return "DTS-HD MA " + c
		}
		return "DTS-HD MA"
	}},
	{bounded(`dts[ -]?hd(?:[ .-]?hra)?` + ch), kAudio, strong, withChannels("DTS-HD")},
	{bounded(`dts[ -:]?x`), kAudio, strong, lit("DTS:X")},
	{bounded(`dts[ -]?es` + ch), kAudio, strong, withChannels("DTS-ES")},
	{bounded(`dts` + ch), kAudio, strong, withChannels("DTS")},
	{bounded(`flac` + ch), kAudio, strong, withChannels("FLAC")},
	{bounded(`opus` + ch), kAudio, weakAmb, withChannels("Opus")},
	{bounded(`mp3`), kAudio, strong, lit("MP3")},
	{bounded(`l?pcm` + ch), kAudio, strong, withChannels("LPCM")},
	{bounded(`([1-9]\.[0-2])(?:[ .]?ch)?|([1-8])ch`), kChannels, techOnly, func(m []string) string {
		if m[2] != "" {
			return m[2]
		}
		return m[3] + ".0"
	}},

	// Languages.
	{bounded(`multi(?:[ .-]?(?:audio|lang(?:uage)?s?)|\d)?`), kLanguage, weakAmb, lit("MULTi")},
	{bounded(`dual[ .-]?(?:audio|lang(?:uage)?s?)`), kLanguage, weak, lit("DUAL")},
	{bounded(`dual|dl`), kLanguage, weakAmb, lit("DUAL")},
	{bounded(`truefrench`), kLanguage, weak, lit("TRUEFRENCH")},
	{bounded(`vostfr|vost`), kLanguage, weak, lit("VOSTFR")},
	{bounded(`vff|vfq|vfi|vf2`), kLanguage, weak, func(m []string) string { return strings.ToUpper(m[1]) }},
	{bounded(`vf`), kLanguage, weakAmb, lit("VF")},
	{bounded(`french|fran[cç]ais|fr`), kLanguage, weakAmb, lit("FRENCH")},
	{bounded(`german|deutsch|ger`), kLanguage, weakAmb, lit("GERMAN")},
	{bounded(`italian|italiano`), kLanguage, weakAmb, lit("iTA")},
	{bounded(`spanish|espa[nñ]ol|castellano|castilian`), kLanguage, weakAmb, lit("SPANiSH")},
	{bounded(`latino|latin[ .]spanish`), kLanguage, weakAmb, lit("LATiNO")},
	{bounded(`english`), kLanguage, weakAmb, lit("ENGLISH")},
	{bounded(`japanese`), kLanguage, weakAmb, lit("JAPANESE")},
	{bounded(`korean`), kLanguage, weakAmb, lit("KOREAN")},
	{bounded(`chinese|mandarin|cantonese`), kLanguage, weakAmb, lit("CHINESE")},
	{bounded(`russian`), kLanguage, weakAmb, lit("RUSSIAN")},
	{bounded(`portuguese|brazilian|pt[ -]?br`), kLanguage, weakAmb, lit("PORTUGUESE")},
	{bounded(`hindi|tamil|telugu|malayalam|kannada|bengali|punjabi|marathi`), kLanguage, weakAmb, upper},
	{bounded(`dutch|flemish|swedish|norwegian|danish|finnish|polish|czech|hungarian|turkish|arabic|hebrew|thai|vietnamese|greek|romanian|ukrainian|indonesian|nordic`), kLanguage, weakAmb, upper},
	{bounded(`ita|eng|spa|esp|jpn|jap|kor|chi|rus|por|hin|tam|tel|swe|nor|dan|fin|pol|cze|hun|tur|ara|heb|tha|vie|gre|ukr|nld|dut|deu|fra|fre`), kLanguage, techOnly, func(m []string) string {
		return codeLanguages[strings.ToLower(m[1])]
	}},
	{bounded(`subbed|dubbed|e?subs?|multi[ .-]?subs?|hardsubs?|hardsubbed|softsubs?|hc|hardcoded`), kMisc, weakAmb, lit("")},

	// Editions.
	{bounded(`director'?s[ .]?cut|directors[ .]?cut|director[ .]cut`), kEdition, weak, lit("Director's Cut")},
	{bounded(`extended[ .](?:cut|edition|version)`), kEdition, weak, lit("Extended")},
	{bounded(`extended`), kEdition, weakAmb, lit("Extended")},
	{bounded(`unrated`), kEdition, weak, lit("Unrated")},
	{bounded(`uncut`), kEdition, weakAmb, lit("Uncut")},
	{bounded(`uncensored`), kEdition, weakAmb, lit("Uncensored")},
	{bounded(`theatrical(?:[ .](?:cut|edition|version|release))?`), kEdition, weakAmb, lit("Theatrical")},
	{bounded(`imax(?:[ .](?:edition|version|enhanced))?`), kEdition, weak, lit("IMAX")},
	{bounded(`(?:4k[ .])?remaster(?:ed)?(?:[ .](?:edition|version))?`), kEdition, weakAmb, lit("Remastered")},
	{bounded(`criterion(?:[ .]collection)?`), kEdition, weak, lit("Criterion")},
	{bounded(`special[ .]edition`), kEdition, weak, lit("Special Edition")},
	{bounded(`collector'?s[ .]edition`), kEdition, weak, lit("Collector's Edition")},
	{bounded(`(?:\d{1,3}(?:th|st|nd|rd)[ .])?anniversary(?:[ .]edition)?`), kEdition, weakAmb, lit("Anniversary Edition")},
	{bounded(`ultimate[ .](?:edition|cut)`), kEdition, weak, lit("Ultimate Edition")},
	{bounded(`final[ .]cut`), kEdition, weakAmb, lit("Final Cut")},
	{bounded(`open[ .]matte`), kEdition, weak, lit("Open Matte")},
	{bounded(`redux`), kEdition, weakAmb, lit("Redux")},
	{bounded(`restored`), kEdition, weakAmb, lit("Restored")},
	{bounded(`despecialized`), kEdition, weak, lit("Despecialized")},

	// Release flags.
	{bounded(`repack\d?|rerip\d?`), kRepack, weak, lit("")},
	{bounded(`proper\d?`), kProper, weakAmb, lit("")},
	{bounded(`internal|readnfo|nfofix|dirfix|subfix|syncfix|samplefix|proof`), kMisc, weak, lit("")},
	{bounded(`limited|real|hybrid|retail|festival|stv|ws|fs|hq|uhq`), kMisc, weakAmb, lit("")},
	{bounded(`\d{2,3}fps`), kMisc, strong, lit("")},
	{bounded(`3d|h-?sbs|half-?sbs|sbs|h-?ou|half-?ou`), k3D, weakAmb, lit("")},

	// Extras.
	{bounded(`trailers?|teasers?`), kExtra, weak, lit("trailer")},
	{bounded(`featurettes?`), kExtra, weak, lit("featurette")},
	{bounded(`behind[ .]the[ .]scenes|making[ .]of`), kExtra, weak, lit("behind the scenes")},
	{bounded(`deleted[ .]scenes?`), kExtra, weak, lit("deleted scene")},
	{bounded(`interviews?`), kExtra, weakAmb, lit("interview")},
	{bounded(`sample`), kExtra, weakAmb, lit("sample")},
	{bounded(`extras|bonus(?:[ .]features?)?|special[ .]features`), kExtra, weakAmb, lit("extra")},

	// Packs.
	{bounded(`(?:the[ .])?complete[ .](?:series|collection|seasons?|box[ .]?set|saga|pack|run|tv[ .]series)|all[ .]seasons|full[ .]series`), kComplete, strong, lit("")},
	{bounded(`complete|completa|completo|int[eé]grale|komplett|box[ .]?set|batch`), kComplete, weakAmb, lit("")},

	// Streaming services.
	{bounded(`amzn|amazon`), kService, weakAmb, lit("AMZN")},
	{bounded(`nf|nflx|netflix`), kService, weakAmb, lit("NF")},
	{bounded(`dsnp|dsny|disney\+?`), kService, weakAmb, lit("DSNP")},
	{bounded(`hmax|hbo[ .]?max`), kService, weakAmb, lit("HMAX")},
	{bounded(`max`), kService, techOnly, lit("MAX")},
	{bounded(`atvp|atv\+?|apple[ .]?tv\+?`), kService, weakAmb, lit("ATVP")},
	{bounded(`hulu`), kService, weakAmb, lit("HULU")},
	{bounded(`pcok|peacock`), kService, weakAmb, lit("PCOK")},
	{bounded(`pmtp|pmnt|paramount\+?`), kService, weakAmb, lit("PMTP")},
	{bounded(`crav|stan|ip|cr|bbc|itvx`), kService, techOnly, upper},

	// Parts of a movie split across files.
	{bounded(`(?:cd|disc|disk)[ .-]?(\d{1,2})`), kPart, strong, func(m []string) string { return m[2] }},
	{bounded(`(?:part|pt)[ .-]?(\d{1,2})`), kPart, techOnly, func(m []string) string { return m[2] }},

	// CRC32 checksums: "[ABCD1234]".
	{bounded(`[\[(][0-9a-f]{8}[\])]`), kHash, strong, func(m []string) string {
		if !strings.ContainsAny(m[1], "0123456789") {
			return ""
		}
		return strings.ToUpper(m[1][1:9])
	}},
}

var codeLanguages = map[string]string{
	"ita": "iTA", "eng": "ENGLISH", "spa": "SPANiSH", "esp": "SPANiSH", "jpn": "JAPANESE",
	"jap": "JAPANESE", "kor": "KOREAN", "chi": "CHINESE", "rus": "RUSSIAN", "por": "PORTUGUESE",
	"hin": "HINDI", "tam": "TAMIL", "tel": "TELUGU", "swe": "SWEDISH", "nor": "NORWEGIAN",
	"dan": "DANISH", "fin": "FINNISH", "pol": "POLISH", "cze": "CZECH", "hun": "HUNGARIAN",
	"tur": "TURKISH", "ara": "ARABIC", "heb": "HEBREW", "tha": "THAI", "vie": "VIETNAMESE",
	"gre": "GREEK", "ukr": "UKRAINIAN", "nld": "DUTCH", "dut": "DUTCH", "deu": "GERMAN",
	"fra": "FRENCH", "fre": "FRENCH",
}

func upper(m []string) string { return strings.ToUpper(m[1]) }

// findTags returns every tag pattern match in s, overlapping ones included.
func findTags(s string) []match {
	var out []match
	starts := wordStarts(s)
	for _, p := range tagPatterns {
		eachWord(p.re, s, starts, func(loc []int) {
			sub := make([]string, len(loc)/2)
			for i := range sub {
				if loc[2*i] >= 0 {
					sub[i] = s[loc[2*i]:loc[2*i+1]]
				}
			}
			v := p.val(sub)
			keep := v != "" || p.kind == kMisc || p.kind == kRepack || p.kind == kProper ||
				p.kind == kComplete || p.kind == k3D
			if !keep {
				return
			}
			m := match{start: loc[2], end: loc[3], kind: p.kind, class: p.class, val: v}
			if p.kind == kPart || p.kind == kBitDepth {
				m.n, _ = strconv.Atoi(v)
			}
			out = append(out, m)
		})
	}
	return out
}

// caseOK reports whether a tag's spelling marks it as a tag rather than a
// title word: ALL CAPS, lower case or scene casing (iTA, SPANiSH, MULTi),
// anything but Title Case, which is how title words are written.
func caseOK(t string) bool {
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '.' || r == '-' }) {
		if !isTitleWord(w) {
			return true
		}
	}
	return false
}

// isTitleWord reports whether w is written in Title Case ("Proper").
func isTitleWord(w string) bool {
	n := 0
	for _, r := range w {
		if !unicode.IsLetter(r) {
			continue
		}
		if n == 0 && !unicode.IsUpper(r) || n > 0 && !unicode.IsLower(r) {
			return false
		}
		n++
	}
	return n > 0
}

// isAlnumAt reports whether the rune starting at i is a letter or digit.
func isAlnumAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isAlnumBefore reports whether the rune ending at i is a letter or digit.
func isAlnumBefore(s string, i int) bool {
	if i <= 0 || i > len(s) {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
