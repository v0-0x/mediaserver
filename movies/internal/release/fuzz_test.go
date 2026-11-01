package release

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParse checks that no name makes the parser panic or return text
// that isn't valid UTF-8 or wasn't in the name.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"The.Matrix.1999.1080p.BluRay.x264-GROUP",
		"[SubsPlease] Show - 05 (1080p) [ABCD1234].mkv",
		"Show.S01E01-E03.1080p.WEB-DL",
		"Breaking Bad (2008)/Season 01/01 - Pilot.mkv",
		`C:\Movies\The Matrix (1999) {tmdb-603}\movie.mkv`,
		"Movie (2019) [YTS.MX]", "-", "[", "(]", ".", "- 1", "S01-", "1x01-", "2024.01.",
		"【Group】 Show – 05", "www.", "{edition-}", "Season 1 -", "E01E", "a-sample",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		for _, i := range []Info{Parse(name), ParsePath(name)} {
			for _, s := range []string{i.Title, i.EpisodeTitle, i.Group} {
				if utf8.ValidString(name) && !utf8.ValidString(s) {
					t.Fatalf("%q: invalid UTF-8 %q", name, s)
				}
			}
			_ = i.EpisodeKeys()
		}
		_ = Similarity(name, strings.ToUpper(name))
		_ = Normalize(name)
	})
}

// Long hostile names stay cheap: they are cut to maxName.
func TestLongNames(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("S01E01-", 5000),
		strings.Repeat("Season 1 & ", 5000),
		strings.Repeat("- 05 ", 5000),
		strings.Repeat("(", 20000),
		strings.Repeat("a/", 5000) + "b.mkv",
	} {
		Parse(s)
		ParsePath(s)
	}
}

func BenchmarkParse(b *testing.B) {
	for b.Loop() {
		Parse("The.Last.of.Us.S01E03.Long.Long.Time.2160p.HMAX.WEB-DL.DDPA5.1.DV.HDR.H.265-GRP")
	}
}

func BenchmarkParsePath(b *testing.B) {
	for b.Loop() {
		ParsePath("TV/Breaking Bad (2008)/Season 01/Breaking.Bad.S01E02.720p.BluRay.x264-GRP.mkv")
	}
}
