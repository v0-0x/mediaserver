package release

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Season and episode markers, tried at word starts like the tags. Group 1
// is the marker; the numbers that follow some markers ("S01E01E02",
// "S01E01-E03", "Season 1-3") are read by hand because Go's regexps can't
// look ahead.
var (
	reSE         = wordPrefix(`s(\d{1,4})[ -]?(?:e|ep)[ ]?(\d{1,4})`)
	reNx         = bounded(`(\d{1,2})x(\d{2,3})`)
	reSeasonWord = bounded(`(?:seasons?|saisons?|staffeln?|temporadas?|stagione|seizoen|series)[ .-]*(\d{1,2}|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)`)
	reSeasonOrd  = bounded(`(\d{1,2})(?:st|nd|rd|th)[ .-]+season`)
	reSeasonOnly = bounded(`s(\d{1,2})`)
	reEpOnly     = wordPrefix(`e(\d{1,4})|(?:ep|eps|episodes?|episodio|épisode|folge)[ .-]?(\d{1,4})`)
	reAbsolute   = bounded(`-[ ]?(\d{1,4})(?:v\d{1,2})?`) // after a space
	reDateYMD    = bounded(`((?:19|20)\d\d)[ .\-/](\d{1,2})[ .\-/](\d{1,2})`)
	reDateDMY    = bounded(`(\d{1,2})[ .\-/](\d{1,2})[ .\-/]((?:19|20)\d\d)`)

	// Continuations.
	reMoreE      = regexp.MustCompile(`(?i)^(?:[ ]?([-~&+,])[ ]?|[ ]?)(?:e|ep)[ ]?(\d{1,4})`)
	reMoreSE     = regexp.MustCompile(`(?i)^[ ]?([-&+])?[ ]?s(\d{1,4})[ -]?(?:e|ep)[ ]?(\d{1,4})`)
	reMoreDash   = regexp.MustCompile(`^(-)(\d{1,3})`)
	reMoreNx     = regexp.MustCompile(`(?i)^[ ]?([-&+])[ ]?(\d{1,2})x(\d{2,3})`)
	reMoreSeason = regexp.MustCompile(`(?i)^[ ]*(-|~|to|thru|through|&|and|,|\+)[ ]*(?:(?:seasons?|s)[ .-]*)?(\d{1,2})`)
	reMoreSOnly  = regexp.MustCompile(`(?i)^(?:(-)s?|[ ]-[ ](?:s)|[ ]?([&+])[ ]?s)(\d{1,2})`)
	reMoreEpNum  = regexp.MustCompile(`(?i)^[ ]?([-~&+,])[ ]?(?:e|ep|episodes?)?[ .]?(\d{1,4})`)
	reSeasonEp   = regexp.MustCompile(`(?i)^[ .-]*(?:episodes?|ep|e)[ .]?(\d{1,4})`)
	reMoreAbs    = regexp.MustCompile(`^\s?[-~]\s?(\d{1,4})(?:v\d{1,2})?`)
	reVersion    = regexp.MustCompile(`(?i)^v\d{1,2}`)
)

var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}

func atoi(s string) int {
	if n, ok := numberWords[strings.ToLower(s)]; ok {
		return n
	}
	n, _ := strconv.Atoi(s)
	return n
}

// endsWord reports whether position i of s ends a word: the next rune is
// not a letter or digit.
func endsWord(s string, i int) bool { return !isAlnumAt(s, i) }

// skipVersion skips a "v2" right after an episode number.
func skipVersion(s string, i int) int {
	if loc := reVersion.FindStringIndex(s[i:]); loc != nil && endsWord(s, i+loc[1]) {
		return i + loc[1]
	}
	return i
}

// addEpisode adds n to eps; a range ("-") adds everything since the last.
func addEpisode(eps []int, n int, isRange bool) []int {
	if len(eps) > 0 && isRange {
		last := eps[len(eps)-1]
		if n > last && n-last <= 500 {
			for e := last + 1; e <= n; e++ {
				eps = append(eps, e)
			}
			return eps
		}
	}
	for _, e := range eps {
		if e == n {
			return eps
		}
	}
	return append(eps, n)
}

func isRangeSep(sep string) bool {
	switch strings.ToLower(strings.TrimSpace(sep)) {
	case "-", "~", "to", "thru", "through":
		return true
	}
	return false
}

// findEpisodes returns every season/episode/date marker in s. orig is the
// name s was prepared from (same length, separators untouched).
func findEpisodes(s, orig string) []match {
	var out []match
	starts := wordStarts(s)

	// S01E02, S01E02E03, S01E02-E05, S01E02-05, S01E01-S01E03.
	eachWord(reSE, s, starts, func(loc []int) {
		season := atoi(s[loc[4]:loc[5]])
		eps := []int{atoi(s[loc[6]:loc[7]])}
		end := skipVersion(s, loc[7])
		for {
			rest := s[end:]
			if m := reMoreSE.FindStringSubmatchIndex(rest); m != nil && endsWordOrE(s, end+m[1]) &&
				atoi(rest[m[4]:m[5]]) == season {
				eps = addEpisode(eps, atoi(rest[m[6]:m[7]]), m[2] >= 0 && rest[m[2]:m[3]] == "-")
				end = skipVersion(s, end+m[1])
				continue
			}
			if m := reMoreE.FindStringSubmatchIndex(rest); m != nil && endsWordOrE(s, end+m[1]) {
				eps = addEpisode(eps, atoi(rest[m[4]:m[5]]), m[2] >= 0 && isRangeSep(rest[m[2]:m[3]]))
				end = skipVersion(s, end+m[1])
				continue
			}
			if m := reMoreDash.FindStringSubmatchIndex(rest); m != nil && endsWord(s, end+m[1]) {
				if n := atoi(rest[m[4]:m[5]]); n > eps[len(eps)-1] {
					eps = addEpisode(eps, n, true)
					end = skipVersion(s, end+m[1])
					continue
				}
			}
			break
		}
		out = append(out, match{start: loc[2], end: end, kind: kEpisodes, class: strong,
			seasons: []int{season}, episodes: eps})
	})

	// 1x02, 1x02-1x03, 1x02-03.
	eachWord(reNx, s, starts, func(loc []int) {
		season := atoi(s[loc[4]:loc[5]])
		eps := []int{atoi(s[loc[6]:loc[7]])}
		end := loc[3]
		for {
			rest := s[end:]
			if m := reMoreNx.FindStringSubmatchIndex(rest); m != nil && endsWord(s, end+m[1]) &&
				atoi(rest[m[4]:m[5]]) == season {
				eps = addEpisode(eps, atoi(rest[m[6]:m[7]]), rest[m[2]:m[3]] == "-")
				end += m[1]
				continue
			}
			if m := reMoreDash.FindStringSubmatchIndex(rest); m != nil && endsWord(s, end+m[1]) {
				if n := atoi(rest[m[4]:m[5]]); n > eps[len(eps)-1] {
					eps = addEpisode(eps, n, true)
					end += m[1]
					continue
				}
			}
			break
		}
		out = append(out, match{start: loc[2], end: end, kind: kEpisodes, class: strong,
			seasons: []int{season}, episodes: eps})
	})

	// Season 2, Seasons 1-3, Season 1 & 2, Season 1 Episode 2.
	eachWord(reSeasonWord, s, starts, func(loc []int) {
		seasons := []int{atoi(s[loc[4]:loc[5]])}
		end := loc[3]
		for {
			rest := s[end:]
			m := reMoreSeason.FindStringSubmatchIndex(rest)
			if m == nil || !endsWord(s, end+m[1]) {
				break
			}
			sep, num := rest[m[2]:m[3]], rest[m[4]:m[5]]
			// "Show Season 3 - 05" is episode 5, not seasons 3 to 5.
			if sep == "-" && strings.Contains(rest[:m[4]], " ") && strings.HasPrefix(num, "0") {
				break
			}
			seasons = addEpisode(seasons, atoi(num), isRangeSep(sep))
			end += m[1]
		}
		mt := match{start: loc[2], end: end, kind: kSeason, class: strong, seasons: seasons}
		if len(seasons) == 1 {
			if m := reSeasonEp.FindStringSubmatchIndex(s[end:]); m != nil && endsWordOrE(s, end+m[1]) {
				mt.kind = kEpisodes
				mt.episodes = []int{atoi(s[end+m[2] : end+m[3]])}
				mt.end = moreEpisodes(s, skipVersion(s, end+m[1]), &mt.episodes)
			}
		}
		out = append(out, mt)
	})

	// 2nd Season.
	eachWord(reSeasonOrd, s, starts, func(loc []int) {
		out = append(out, match{start: loc[2], end: loc[3], kind: kSeason, class: strong,
			seasons: []int{atoi(s[loc[4]:loc[5]])}})
	})

	// S01, S01-S05, S01-05, S01 & S02.
	eachWord(reSeasonOnly, s, starts, func(loc []int) {
		seasons := []int{atoi(s[loc[4]:loc[5]])}
		end := loc[3]
		for {
			rest := s[end:]
			m := reMoreSOnly.FindStringSubmatchIndex(rest)
			if m == nil || !endsWord(s, end+m[1]) {
				break
			}
			isRange := m[2] >= 0 || strings.Contains(rest[:m[6]], "-")
			seasons = addEpisode(seasons, atoi(rest[m[6]:m[7]]), isRange)
			end += m[1]
		}
		out = append(out, match{start: loc[2], end: end, kind: kSeason, class: strong, seasons: seasons})
	})

	// E05, Ep 5, Episode 5, E01-E03.
	eachWord(reEpOnly, s, starts, func(loc []int) {
		num := loc[4]
		if num < 0 {
			num = loc[6]
		}
		end := skipVersion(s, loc[3])
		if !endsWordOrE(s, end) {
			return
		}
		eps := []int{atoi(s[num:loc[3]])}
		end = moreEpisodes(s, end, &eps)
		out = append(out, match{start: loc[2], end: end, kind: kEpisode, class: strong, episodes: eps})
	})

	// " - 105", " - 05v2", " - 01-12".
	eachWord(reAbsolute, s, starts, func(loc []int) {
		n := atoi(s[loc[4]:loc[5]])
		if loc[2] == 0 || s[loc[2]-1] != ' ' || n == 0 || len(s[loc[4]:loc[5]]) == 4 && n >= 1900 && n <= 2099 {
			return // "9-1-1" is a title, " - 2019" a year
		}
		eps := []int{n}
		end := loc[3]
		if m := reMoreAbs.FindStringSubmatchIndex(s[end:]); m != nil && endsWord(s, end+m[1]) {
			if e := atoi(s[end+m[2] : end+m[3]]); e > n {
				eps = addEpisode(eps, e, true)
				end += m[1]
			}
		}
		out = append(out, match{start: loc[2], end: end, kind: kAbsolute, class: strong, episodes: eps})
	})

	// Daily shows: 2024.01.02, 02.01.2024. Both separators must be the same
	// in the original name, so "AAC 5.1 2019" is not the 5th of January.
	sameSeps := func(loc []int, spaceOK bool) bool {
		a, b := orig[loc[5]], orig[loc[7]]
		return a == b && (spaceOK || a != ' ')
	}
	eachWord(reDateYMD, s, starts, func(loc []int) {
		if !sameSeps(loc, true) {
			return
		}
		if d := makeDate(atoi(s[loc[4]:loc[5]]), atoi(s[loc[6]:loc[7]]), atoi(s[loc[8]:loc[9]])); d != "" {
			out = append(out, match{start: loc[2], end: loc[3], kind: kDate, class: strong, date: d})
		}
	})
	eachWord(reDateDMY, s, starts, func(loc []int) {
		if !sameSeps(loc, false) {
			return
		}
		a, b, y := atoi(s[loc[4]:loc[5]]), atoi(s[loc[6]:loc[7]]), atoi(s[loc[8]:loc[9]])
		d := makeDate(y, b, a) // day first, the European way...
		if b > 12 {
			d = makeDate(y, a, b) // ...unless only the American way works
		}
		if d != "" {
			out = append(out, match{start: loc[2], end: loc[3], kind: kDate, class: strong, date: d})
		}
	})
	return out
}

// endsWordOrE is endsWord, but also accepts an "E" that starts the next
// episode of "S01E01E02".
func endsWordOrE(s string, i int) bool {
	if endsWord(s, i) {
		return true
	}
	return i+1 < len(s) && (s[i] == 'e' || s[i] == 'E') && s[i+1] >= '0' && s[i+1] <= '9'
}

// moreEpisodes reads the episodes listed after a first one ("E01-E03",
// "Episode 1 & 2", "E01E02") into eps and returns where they end.
func moreEpisodes(s string, end int, eps *[]int) int {
	for {
		rest := s[end:]
		if m := reMoreE.FindStringSubmatchIndex(rest); m != nil && endsWordOrE(s, end+m[1]) {
			*eps = addEpisode(*eps, atoi(rest[m[4]:m[5]]), m[2] >= 0 && isRangeSep(rest[m[2]:m[3]]))
			end = skipVersion(s, end+m[1])
			continue
		}
		if m := reMoreEpNum.FindStringSubmatchIndex(rest); m != nil && endsWord(s, end+m[1]) {
			n := atoi(rest[m[4]:m[5]])
			last := (*eps)[len(*eps)-1]
			// "Episode 5 - 2019" is not a range.
			if n <= last || n-last > 500 || len(rest[m[4]:m[5]]) == 4 {
				break
			}
			*eps = addEpisode(*eps, n, isRangeSep(rest[m[2]:m[3]]))
			end = skipVersion(s, end+m[1])
			continue
		}
		return end
	}
	return end
}

var daysIn = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// makeDate formats a valid date as YYYY-MM-DD, or returns "".
func makeDate(y, m, d int) string {
	if y < 1900 || m < 1 || m > 12 || d < 1 || d > daysIn[m] {
		return ""
	}
	if m == 2 && d == 29 && !(y%4 == 0 && (y%100 != 0 || y%400 == 0)) {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}
