package util

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"Star Trek: Picard (2020)":   "Star Trek： Picard (2020)",
		"What If...?":                "What If",
		"AC/DC: Live":                "AC_DC： Live",
		`a\b*c?d"e<f>g|h`:            "a_b_cd_e_f_g_h",
		"tab\there\x00":              "tab_here_",
		"  Spaces  and  dots...  ":   "Spaces and dots",
		"...":                        "untitled",
		"":                           "untitled",
		"CON":                        "_CON",
		"nul.mkv":                    "_nul.mkv",
		"Com1 (2010)":                "Com1 (2010)",
		"Breaking Bad - S01E01":      "Breaking Bad - S01E01",
		"Amélie (2001)":              "Amélie (2001)",
		"Mission: Impossible – Dead": "Mission： Impossible – Dead",
	} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	long := SanitizeFilename(strings.Repeat("é", 200))
	if len(long) > 180 || !utf8.ValidString(long) {
		t.Errorf("long name: %d bytes, valid %v", len(long), utf8.ValidString(long))
	}
}
