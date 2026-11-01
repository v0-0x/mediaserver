package catalog

import "fmt"

// Media IDs
//
// KumoBox gives every movie and show one integer id, so the rest of the app
// (library files, watch history, list entries, track preferences...) can
// keep using a plain int: a movie is its TMDB movie id, a show is ShowBase
// plus its TMDB TV id. TMDB numbers movies and shows separately (movie 1399
// and show 1399 are different titles), and its ids are far below ShowBase.

const ShowBase = 1_000_000_000

// MovieID is the media id of the TMDB movie tmdbID.
func MovieID(tmdbID int) int { return tmdbID }

// ShowID is the media id of the TMDB TV show tmdbID.
func ShowID(tmdbID int) int { return ShowBase + tmdbID }

// IsShow reports whether a media id is a show's.
func IsShow(id int) bool { return id > ShowBase }

// IsMovie reports whether a media id is a movie's.
func IsMovie(id int) bool { return id > 0 && id < ShowBase }

// TMDBID is the TMDB id (movie or TV) behind a media id.
func TMDBID(id int) int {
	if IsShow(id) {
		return id - ShowBase
	}
	return id
}

// KindOf is "TV" for a show's media id and "MOVIE" for a movie's.
func KindOf(id int) string {
	if IsShow(id) {
		return TypeTV
	}
	return TypeMovie
}

// Media types (Media.Type).
const (
	TypeMovie = "MOVIE"
	TypeTV    = "TV"
)

// Episode keys
//
// An episode of a show is identified by one int too, its key:
// season*1000 + episode (S02E05 is 2005, the special S00E03 is 3). Keys sort
// in watching order, so "progress" (the last episode watched) only moves
// forward, and specials (season 0) never count as progress. A movie has a
// single "episode": key 1.

const keySeason = 1000

// EpisodeKey is the key of episode e of season s.
func EpisodeKey(s, e int) int { return s*keySeason + e }

// SplitKey is the season and episode of a key.
func SplitKey(key int) (season, episode int) { return key / keySeason, key % keySeason }

// FormatKey writes a key the usual way: S02E05.
func FormatKey(key int) string {
	s, e := SplitKey(key)
	return fmt.Sprintf("S%02dE%02d", s, e)
}

// MovieEpisode is the one "episode" of a movie.
const MovieEpisode = 1
