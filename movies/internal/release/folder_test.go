package release

import "testing"

func TestNamesTitle(t *testing.T) {
	for name, want := range map[string]bool{
		"Breaking Bad (2008)":                    true,
		"Inception.2010.1080p.BluRay.x264-GRP":   true,
		"Breaking.Bad.S02.1080p.BluRay.x264-GRP": true,
		"The Matrix (1999) {imdb-tt0133093}":     true,
		"Lost":                                   true,
		"Season 01":                              false,
		"S2":                                     false,
		"Specials":                               false,
		"Staffel 3":                              false,
		"Featurettes":                            false,
		"Behind The Scenes":                      false,
		"Sample":                                 false,
		"CD1":                                    false,
		"Movies":                                 false,
		"TV Shows":                               false,
		"Downloads":                              false,
		"1080p":                                  false,
		"S01 1080p":                              false,
		"":                                       false,
		"..":                                     false,
	} {
		if got := NamesTitle(name); got != want {
			t.Errorf("NamesTitle(%q) = %v, want %v", name, got, want)
		}
	}
}
