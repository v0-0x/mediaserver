package letterboxd

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
)

// Letterboxd's importer
//
// letterboxd.com/import takes a CSV with a header row naming its columns.
// It matches each row to a film by tmdbID first, then imdbID, then Title and
// Year, so the ids are written whenever they're known. tmdbID is a TMDB
// *movie* id: a show's TMDB TV id would match an unrelated film, so shows
// only get their IMDb id, title and year. WatchedDate makes the row a diary
// entry (without it the film is only marked watched); Rating is 0.5-5 in
// half stars; Rewatch is true/false; Tags are comma separated.

// ImportRow is a row of the CSV for letterboxd.com/import.
type ImportRow struct {
	Title       string
	Year        int
	TMDBID      int    // TMDB movie id, 0 for shows
	IMDbID      string // tt0133093
	WatchedDate string // YYYY-MM-DD, "" for no diary entry
	Rating      float64
	Rewatch     bool
	Review      string
	Tags        string
}

// ImportHeader is the header of the diary CSV.
var ImportHeader = []string{"Title", "Year", "tmdbID", "imdbID", "WatchedDate", "Rating", "Rewatch", "Review", "Tags"}

// WatchlistHeader is the header of the watchlist CSV (imported on
// letterboxd.com › Watchlist › Import).
var WatchlistHeader = []string{"Title", "Year", "tmdbID", "imdbID"}

// WriteImportCSV writes rows as a CSV for letterboxd.com/import.
func WriteImportCSV(w io.Writer, rows []ImportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(ImportHeader); err != nil {
		return err
	}
	for _, r := range rows {
		rewatch := "false"
		if r.Rewatch {
			rewatch = "true"
		}
		rec := []string{
			cleanField(r.Title), yearField(r.Year), idField(r.TMDBID), strings.TrimSpace(r.IMDbID),
			normDate(r.WatchedDate), FormatRating(r.Rating), rewatch,
			strings.TrimSpace(strings.ReplaceAll(r.Review, "\r\n", "\n")), cleanField(r.Tags),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteWatchlistCSV writes films as a watchlist CSV (only the title, year
// and ids of rows are used).
func WriteWatchlistCSV(w io.Writer, rows []ImportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(WatchlistHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{cleanField(r.Title), yearField(r.Year), idField(r.TMDBID), strings.TrimSpace(r.IMDbID)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// FormatRating writes stars the importer's way, with one decimal ("3.5",
// "4.0"), rounded to a half star; "" for no rating.
func FormatRating(stars float64) string {
	if stars <= 0 {
		return ""
	}
	return strconv.FormatFloat(ScoreToStars(stars*20), 'f', 1, 64)
}

func yearField(y int) string {
	if y <= 0 {
		return ""
	}
	return strconv.Itoa(y)
}

func idField(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

// cleanField puts a one-line value on one line.
func cleanField(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
