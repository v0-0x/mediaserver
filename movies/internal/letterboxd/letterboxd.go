// Package letterboxd connects Moviko to a Letterboxd account.
//
// Letterboxd has no open API, so the sync works with what any member can
// get at:
//
//   - rss.go reads the member's public diary feed (letterboxd.com/<name>/rss/,
//     the latest 50 diary entries, with TMDB ids);
//   - export.go reads the data export (Settings › Data › Export on
//     letterboxd.com: a ZIP of CSV files with the whole history);
//   - csvout.go writes what Moviko logged as a CSV that letterboxd.com/import
//     takes;
//   - api.go is a client of the official API, for installs that were granted
//     access by Letterboxd;
//   - Sync ties them to the catalog: sync.go (connection, status, the
//     background loop), pull.go (feed), import.go (export), apply.go (how
//     both update the list), diary.go (logging viewings, CSV export) and
//     push.go (API sign-in, sending the diary).
package letterboxd

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// DefaultBaseURL is the Letterboxd website. $MOVIKO_LETTERBOXD_URL (or
// Sync.BaseURL) points the feed reader elsewhere, for tests and offline demos.
const DefaultBaseURL = "https://letterboxd.com"

var (
	// ErrNoSuchMember: Letterboxd has no member by that username (or their
	// profile is private).
	ErrNoSuchMember = errors.New("letterboxd: no member by that username")
	// ErrBadUsername: not a Letterboxd username (letters, digits and
	// underscores only).
	ErrBadUsername = errors.New("letterboxd: a username has only letters, digits and underscores")
	// ErrNotConnected: no Letterboxd username is set.
	ErrNotConnected = errors.New("letterboxd: no account connected")
)

// Source values of catalog.DiaryEntry.
const (
	SourceMoviko    = "kumobox"
	SourceLetterboxd = "letterboxd"
)

// userAgent identifies Moviko to Letterboxd.
func userAgent() string { return "Moviko/" + config.AppVersion }

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,40}$`)

// ParseUsername accepts a username as people paste it: "dave", "@dave" or a
// link to the profile ("letterboxd.com/dave", "https://letterboxd.com/dave/films/").
func ParseUsername(s string) (string, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(strings.ToLower(s), "letterboxd.com/"); i >= 0 {
		s = s[i+len("letterboxd.com/"):]
		if j := strings.IndexAny(s, "/?#"); j >= 0 {
			s = s[:j]
		}
	}
	s = strings.Trim(s, "@/ ")
	if !usernameRe.MatchString(s) {
		return "", ErrBadUsername
	}
	return s, nil
}

// ViewerID is the catalog.Viewer id of a Letterboxd member: a stable positive
// number derived from the username, so two Movikos connected to the same
// account recognise each other.
func ViewerID(username string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(username)))
	id := int(h.Sum32() & 0x7fffffff)
	if id == 0 {
		id = 1
	}
	return id
}

// ProfileURL is the member's page on base (DefaultBaseURL when empty).
func ProfileURL(base, username string) string {
	if base == "" {
		base = DefaultBaseURL
	}
	return strings.TrimRight(base, "/") + "/" + strings.ToLower(username) + "/"
}

// filmLinkRe matches a member's page about a film (their review or diary
// entry): https://letterboxd.com/<member>/film/<slug>/[<n>/].
var filmLinkRe = regexp.MustCompile(`^(https?://[^/]+)/[^/]+/film/([^/?#]+)`)

// FilmURL turns a member's link about a film into the film's own page
// (https://letterboxd.com/film/<slug>/). Other links are returned unchanged.
func FilmURL(link string) string {
	if m := filmLinkRe.FindStringSubmatch(link); m != nil {
		return m[1] + "/film/" + m[2] + "/"
	}
	return link
}

// StarsToScore converts a Letterboxd rating (0.5-5 stars) to a Moviko score
// (0-100); no rating (0) stays 0.
func StarsToScore(stars float64) int {
	if stars <= 0 {
		return 0
	}
	return int(math.Round(math.Min(stars, 5) * 20))
}

// ScoreToStars converts a Moviko score (0-100) to Letterboxd stars, rounded
// to the nearest half star (0.5-5); no score (0) stays 0.
func ScoreToStars(score float64) float64 {
	if score <= 0 {
		return 0
	}
	stars := math.Round(score/10) / 2
	return math.Max(0.5, math.Min(5, stars))
}

var dateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)

// normDate returns the YYYY-MM-DD date s starts with, or "".
func normDate(s string) string {
	s = strings.TrimSpace(s)
	if m := dateRe.FindString(s); m != "" {
		if _, err := time.Parse("2006-01-02", m); err == nil {
			return m
		}
	}
	for _, layout := range []string{"2006/01/02", "Jan 2, 2006", "2 Jan 2006", "January 2, 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// endOfDay is the last second of a YYYY-MM-DD day (UTC), 0 for no date: the
// latest moment something dated that day can have happened.
func endOfDay(date string) int64 {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	return t.Add(24*time.Hour - time.Second).Unix()
}

// fuzzyDate converts a YYYY-MM-DD date.
func fuzzyDate(date string) *catalog.FuzzyDate {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil
	}
	y, m, d := t.Year(), int(t.Month()), t.Day()
	return &catalog.FuzzyDate{Year: &y, Month: &m, Day: &d}
}

// fuzzyString writes a FuzzyDate as YYYY-MM-DD with 00 for missing parts, so
// dates compare as strings; "" when there's no year.
func fuzzyString(f catalog.FuzzyDate) string {
	if f.Year == nil || *f.Year <= 0 {
		return ""
	}
	n := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	return fmt.Sprintf("%04d-%02d-%02d", n(f.Year), n(f.Month), n(f.Day))
}

// filmLabel is how a film is named in Result.Unmatched: "Title (Year)".
func filmLabel(title string, year int) string {
	title = strings.TrimSpace(title)
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}
