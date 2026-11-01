package release

import "strings"

// NamesTitle reports whether a folder name is one ParsePath may take a
// movie's or show's title from — "Breaking Bad (2008)",
// "Inception.2010.1080p.BluRay-GRP" — rather than a season folder
// ("Season 01", "Specials"), an extras or part folder ("Featurettes",
// "CD1"), a generic one ("Movies", "Downloads", "1080p") or a title-less
// pack ("S01 1080p"). The library uses it to find the folder a whole movie
// or show sits in.
func NamesTitle(folder string) bool {
	d := strings.TrimSpace(folder)
	if d == "" || d == "." || d == ".." {
		return false
	}
	if _, ok := seasonDir(d); ok {
		return false
	}
	if _, ok := extrasDir(d); ok {
		return false
	}
	if rePartDir.MatchString(d) || reGenericDir.MatchString(d) {
		return false
	}
	return !isGenericTitle(parse(d, nameFolder).Title)
}
