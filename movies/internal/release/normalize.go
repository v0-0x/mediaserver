package release

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// foldTable folds the accented and special Latin letters titles use to
// plain ASCII ("Amélie" → "amelie", "Æon" → "aeon"). Keys are lower case.
var foldTable = func() map[rune]string {
	m := map[rune]string{}
	for _, p := range [][2]string{
		{"àáâãäåāăąǎǻạảấầẩẫậắằẳẵặ", "a"}, {"çćĉċč", "c"}, {"ďđ", "d"},
		{"èéêëēĕėęěẹẻẽếềểễệ", "e"}, {"ĝğġģ", "g"}, {"ĥħ", "h"},
		{"ìíîïĩīĭįıǐỉị", "i"}, {"ĵ", "j"}, {"ķ", "k"}, {"ĺļľŀł", "l"},
		{"ñńņňŉ", "n"}, {"òóôõöøōŏőǒǿọỏốồổỗộớờởỡợơ", "o"}, {"ŕŗř", "r"},
		{"śŝşšș", "s"}, {"ţťŧț", "t"}, {"ùúûüũūŭůűųǔưụủứừửữự", "u"}, {"ŵ", "w"},
		{"ýÿŷỳỵỷỹ", "y"}, {"źżž", "z"},
	} {
		for _, r := range p[0] {
			m[r] = p[1]
		}
	}
	for r, v := range map[rune]string{'æ': "ae", 'œ': "oe", 'ß': "ss", 'ð': "d", 'þ': "th", 'ĳ': "ij"} {
		m[r] = v
	}
	return m
}()

// Normalize turns a title into the form titles are matched in: lower case,
// accents folded, "&" → "and", apostrophes and dots dropped ("Ocean's" →
// "oceans", "S.W.A.T." → "swat"), other punctuation as spaces, spaces
// collapsed. "The Lord of the Rings: The Two Towers" → "the lord of the
// rings the two towers".
func Normalize(title string) string {
	rs := []rune(strings.ToLower(title))
	var b strings.Builder
	b.Grow(len(title))
	letterRun := 0 // letters right before the current rune
	for i, r := range rs {
		if f, ok := foldTable[r]; ok {
			b.WriteString(f)
			letterRun++
			continue
		}
		switch {
		case r == '&':
			b.WriteString(" and ")
		case r == '\'' || r == '`' || r == '´' || r == 'ʼ':
			// dropped: "ocean's" → "oceans"
		case r == '.':
			// Dropped inside acronyms and numbers ("s.w.a.t", "2.0"), a
			// space between words ("mr.robot").
			if letterRun >= 2 && i+1 < len(rs) && unicode.IsLetter(rs[i+1]) {
				b.WriteByte(' ')
			}
		case unicode.Is(unicode.Mn, r):
			// combining accents
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
		if unicode.IsLetter(r) {
			letterRun++
		} else {
			letterRun = 0
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// numberTokens canonicalizes the ways titles write numbers, so "Ocean's
// Eleven" matches "Oceans 11" and "Rocky II" matches "Rocky 2".
var numberTokens = map[string]string{
	"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6",
	"seven": "7", "eight": "8", "nine": "9", "ten": "10", "eleven": "11", "twelve": "12",
	"thirteen": "13", "fourteen": "14", "fifteen": "15", "sixteen": "16",
	"seventeen": "17", "eighteen": "18", "nineteen": "19", "twenty": "20",
	"ii": "2", "iii": "3", "iv": "4", "vi": "6", "vii": "7", "viii": "8", "ix": "9",
	"xi": "11", "xii": "12", "xiii": "13", "xiv": "14", "xv": "15", "xvi": "16",
}

// simTokens is a title's words for Similarity: normalized, numbers
// canonical, a leading article dropped.
func simTokens(title string) []string {
	ts := strings.Fields(Normalize(title))
	for i, t := range ts {
		if n, ok := numberTokens[t]; ok {
			ts[i] = n
		} else if n, err := strconv.Atoi(t); err == nil {
			ts[i] = strconv.Itoa(n) // "007" stays a word, "01" → "1"
			if strings.HasPrefix(t, "00") {
				ts[i] = t
			}
		}
	}
	// A last lone V or X is a roman numeral ("Rocky V", "Saw X").
	if len(ts) >= 2 {
		switch ts[len(ts)-1] {
		case "v":
			ts[len(ts)-1] = "5"
		case "x":
			ts[len(ts)-1] = "10"
		}
	}
	for i, t := range ts {
		if a, ok := abbreviations[t]; ok {
			ts[i] = a
		}
	}
	// "The Matrix", "Matrix, The": the article says nothing.
	if len(ts) >= 2 && (ts[0] == "the" || ts[0] == "a" || ts[0] == "an") {
		ts = ts[1:]
	}
	if len(ts) >= 2 && ts[len(ts)-1] == "the" {
		ts = ts[:len(ts)-1]
	}
	return ts
}

// abbreviations are spelled out for Similarity ("Dr Who" ~ "Doctor Who").
var abbreviations = map[string]string{
	"dr": "doctor", "mr": "mister", "st": "saint", "vs": "versus", "pt": "part", "vol": "volume",
}

// Similarity scores how alike two titles are, from 0 to 1, on their
// normalized words. It takes the best of an edit-distance ratio on the
// joined words (typos, "Spiderman" vs "Spider-Man"), the same on the
// sorted words (word order) and a fuzzy token overlap (extra or missing
// words), ignores a leading article, and lowers the score when the titles
// carry different numbers (sequels: "Iron Man" vs "Iron Man 2").
func Similarity(a, b string) float64 {
	ta, tb := simTokens(a), simTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	if strings.Join(ta, " ") == strings.Join(tb, " ") {
		return 1
	}
	score := ratio(strings.Join(ta, ""), strings.Join(tb, ""))
	sa, sb := append([]string(nil), ta...), append([]string(nil), tb...)
	sort.Strings(sa)
	sort.Strings(sb)
	score = max(score, ratio(strings.Join(sa, ""), strings.Join(sb, "")), tokenOverlap(ta, tb))
	if !sameNumbers(ta, tb) {
		score *= 0.8
	}
	return score
}

// tokenOverlap is a Dice coefficient where words count as shared when
// they are close enough (one typo in a long word).
func tokenOverlap(ta, tb []string) float64 {
	used := make([]bool, len(tb))
	total := 0.0
	for _, a := range ta {
		best, bi := 0.0, -1
		for j, b := range tb {
			if used[j] {
				continue
			}
			if r := ratio(a, b); r > best {
				best, bi = r, j
			}
		}
		if bi >= 0 && best >= 0.8 {
			used[bi] = true
			total += best
		}
	}
	return 2 * total / float64(len(ta)+len(tb))
}

// sameNumbers reports whether both titles carry the same numbers.
func sameNumbers(ta, tb []string) bool {
	nums := func(ts []string) string {
		var ns []string
		for _, t := range ts {
			if t[0] >= '0' && t[0] <= '9' {
				ns = append(ns, t)
			}
		}
		sort.Strings(ns)
		return strings.Join(ns, " ")
	}
	return nums(ta) == nums(tb)
}

// ratio is 1 - levenshtein/maxLen over runes.
func ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	n := max(len(ra), len(rb))
	if n == 0 {
		return 1
	}
	return 1 - float64(levenshtein(ra, rb))/float64(n)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
