package player

import (
	"strings"
	"unicode"
)

// Languages of audio and subtitle tracks
//
// Files tag their tracks with ISO 639-2 codes, bibliographic ("fre", "ger")
// or terminological ("fra", "deu"), newer ones with BCP 47 tags ("fr",
// "pt-BR"); TMDB gives a title's original language in ISO 639-1 ("fr"), and
// the settings hold lists like "fre,fra,fr". All of them are compared by
// their ISO 639-1 code.

type language struct {
	code  string   // ISO 639-1
	other []string // ISO 639-2/B and /T codes and other aliases
	names []string // names in English and in the language itself
}

var languages = []language{
	{"en", []string{"eng"}, []string{"english"}},
	{"fr", []string{"fre", "fra"}, []string{"french", "français", "francais"}},
	{"de", []string{"ger", "deu"}, []string{"german", "deutsch"}},
	{"es", []string{"spa"}, []string{"spanish", "español", "espanol", "castellano", "latino"}},
	{"it", []string{"ita"}, []string{"italian", "italiano"}},
	{"pt", []string{"por"}, []string{"portuguese", "português", "portugues"}},
	{"ja", []string{"jpn", "jp"}, []string{"japanese", "日本語"}},
	{"ko", []string{"kor"}, []string{"korean", "한국어"}},
	// TMDB calls Cantonese "cn".
	{"zh", []string{"chi", "zho", "cmn", "yue", "cn"}, []string{"chinese", "mandarin", "cantonese", "中文"}},
	{"ru", []string{"rus"}, []string{"russian", "русский"}},
	{"ar", []string{"ara"}, []string{"arabic"}},
	{"hi", []string{"hin"}, []string{"hindi"}},
	{"nl", []string{"dut", "nld"}, []string{"dutch", "nederlands", "flemish"}},
	{"sv", []string{"swe"}, []string{"swedish", "svenska"}},
	{"no", []string{"nor", "nb", "nob", "nn", "nno"}, []string{"norwegian", "norsk"}},
	{"da", []string{"dan"}, []string{"danish", "dansk"}},
	{"fi", []string{"fin"}, []string{"finnish", "suomi"}},
	{"pl", []string{"pol"}, []string{"polish", "polski"}},
	{"tr", []string{"tur"}, []string{"turkish", "türkçe"}},
	{"cs", []string{"cze", "ces"}, []string{"czech", "čeština"}},
	{"sk", []string{"slo", "slk"}, []string{"slovak"}},
	{"hu", []string{"hun"}, []string{"hungarian", "magyar"}},
	{"el", []string{"gre", "ell"}, []string{"greek"}},
	{"he", []string{"heb", "iw"}, []string{"hebrew"}},
	{"th", []string{"tha"}, []string{"thai"}},
	{"vi", []string{"vie"}, []string{"vietnamese"}},
	{"id", []string{"ind", "in"}, []string{"indonesian"}},
	{"ms", []string{"may", "msa"}, []string{"malay"}},
	{"uk", []string{"ukr"}, []string{"ukrainian"}},
	{"ro", []string{"rum", "ron"}, []string{"romanian"}},
	{"bg", []string{"bul"}, []string{"bulgarian"}},
	{"hr", []string{"hrv"}, []string{"croatian"}},
	{"sr", []string{"srp"}, []string{"serbian"}},
	{"sl", []string{"slv"}, []string{"slovenian", "slovene"}},
	{"fa", []string{"per", "fas"}, []string{"persian", "farsi"}},
	{"ta", []string{"tam"}, []string{"tamil"}},
	{"te", []string{"tel"}, []string{"telugu"}},
	{"ml", []string{"mal"}, []string{"malayalam"}},
	{"kn", []string{"kan"}, []string{"kannada"}},
	{"bn", []string{"ben"}, []string{"bengali", "bangla"}},
	{"mr", []string{"mar"}, []string{"marathi"}},
	{"pa", []string{"pan"}, []string{"punjabi"}},
	{"ur", []string{"urd"}, []string{"urdu"}},
	{"ca", []string{"cat"}, []string{"catalan"}},
	{"eu", []string{"baq", "eus"}, []string{"basque"}},
	{"gl", []string{"glg"}, []string{"galician"}},
	{"is", []string{"ice", "isl"}, []string{"icelandic"}},
	{"et", []string{"est"}, []string{"estonian"}},
	{"lv", []string{"lav"}, []string{"latvian"}},
	{"lt", []string{"lit"}, []string{"lithuanian"}},
	{"tl", []string{"tgl", "fil"}, []string{"tagalog", "filipino"}},
	{"sq", []string{"alb", "sqi"}, []string{"albanian"}},
	{"mk", []string{"mac", "mkd"}, []string{"macedonian"}},
	{"bs", []string{"bos"}, []string{"bosnian"}},
	{"af", []string{"afr"}, []string{"afrikaans"}},
	{"sw", []string{"swa"}, []string{"swahili"}},
	{"hy", []string{"arm", "hye"}, []string{"armenian"}},
	{"ka", []string{"geo", "kat"}, []string{"georgian"}},
	{"cy", []string{"wel", "cym"}, []string{"welsh"}},
	{"ga", []string{"gle"}, []string{"irish"}},
	{"la", []string{"lat"}, []string{"latin"}},
}

var (
	langCodes = map[string]string{} // any code or name -> ISO 639-1
	// titleWords are the words of a track title that name its language:
	// the names, and the 3-letter codes of the common languages ("ENG 5.1").
	titleWords = map[string]string{}
)

func init() {
	for _, l := range languages {
		langCodes[l.code] = l.code
		for _, c := range l.other {
			langCodes[c] = l.code
		}
		for _, n := range l.names {
			langCodes[n] = l.code
			titleWords[n] = l.code
		}
	}
	for _, c := range []string{"eng", "fre", "fra", "ger", "deu", "spa", "ita", "por", "jpn", "kor", "chi", "zho", "rus"} {
		titleWords[c] = langCodes[c]
	}
}

// langCode is the ISO 639-1 code of a language code or name ("fre", "fra",
// "fr-FR" and "French" are all "fr"), "" for none or an unknown language
// ("und", "mul"; TMDB's "xx"). Codes it doesn't know stay as they are,
// lowercased.
func langCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "und", "mul", "zxx", "mis", "qaa", "xx", "unknown", "none":
		return ""
	}
	if c, ok := langCodes[s]; ok {
		return c
	}
	// A BCP 47 tag with a region or script (pt-BR, es-419, zh-Hant), or a
	// subtitle file's suffix ("forced.en", "en.sdh").
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' }) {
		if c, ok := langCodes[part]; ok {
			return c
		}
	}
	return s
}

// langMatch reports whether two language codes (or names) are the same
// language.
func langMatch(a, b string) bool {
	ca := langCode(a)
	return ca != "" && ca == langCode(b)
}

// langIn reports whether lang is one of a comma separated list of
// languages ("fre,fra,fr").
func langIn(lang, list string) bool {
	for _, l := range strings.Split(list, ",") {
		if langMatch(lang, l) {
			return true
		}
	}
	return false
}

// firstLang is the first language of a list ("fre,fra,fr" -> "fr").
func firstLang(list string) string {
	for _, l := range strings.Split(list, ",") {
		if c := langCode(l); c != "" {
			return c
		}
	}
	return ""
}

// titleLang is the language a track's title names ("English 5.1",
// "Français", "ENG"), "" if none.
func titleLang(title string) string {
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if c, ok := titleWords[w]; ok {
			return c
		}
	}
	return ""
}

// trackLang is the language of a track: its tag, else what its title says.
func trackLang(t Track) string {
	if c := langCode(t.Lang); c != "" {
		return c
	}
	return titleLang(t.Title)
}
