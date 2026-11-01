package letterboxd

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

// The data export
//
// letterboxd.com › Settings › Data › Export gives a ZIP of CSV files:
//
//	profile.csv     Date Joined, Username, Given Name, Family Name, Email Address, ...
//	diary.csv       Date, Name, Year, Letterboxd URI, Rating, Rewatch, Tags, Watched Date
//	watched.csv     Date, Name, Year, Letterboxd URI
//	ratings.csv     Date, Name, Year, Letterboxd URI, Rating
//	watchlist.csv   Date, Name, Year, Letterboxd URI
//	reviews.csv     Date, Name, Year, Letterboxd URI, Rating, Rewatch, Review, Tags, Watched Date
//	likes/films.csv Date, Name, Year, Letterboxd URI
//
// plus files KumoBox has no use for (comments, lists/, deleted/, orphaned/,
// likes/lists.csv...). "Date" is when the row was made (logged, rated,
// added), Rating is 0.5-5 stars, Rewatch is "Yes" or empty, Letterboxd URI a
// boxd.it short link: the film's in watched, ratings, watchlist and likes,
// the viewing's in diary and reviews (the same link for a viewing and its
// review). Files are read by column names, whatever their order, and
// missing columns read as empty.

// ExportFilm is a row of one of the export's film files. Which fields are
// set depends on the file (see above).
type ExportFilm struct {
	Date        string  `json:"date,omitempty"` // YYYY-MM-DD
	Name        string  `json:"name"`
	Year        int     `json:"year,omitempty"`
	URI         string  `json:"uri,omitempty"`
	Rating      float64 `json:"rating,omitempty"` // stars, 0 for none
	Rewatch     bool    `json:"rewatch,omitempty"`
	Tags        string  `json:"tags,omitempty"`
	WatchedDate string  `json:"watchedDate,omitempty"` // YYYY-MM-DD
	Review      string  `json:"review,omitempty"`      // plain text
}

// Profile is the member, from profile.csv.
type Profile struct {
	Username   string `json:"username"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	DateJoined string `json:"dateJoined,omitempty"`
}

// Export is what was read from a data export (or from some of its files).
type Export struct {
	Profile   *Profile     `json:"profile,omitempty"`
	Diary     []ExportFilm `json:"diary"`
	Watched   []ExportFilm `json:"watched"`
	Ratings   []ExportFilm `json:"ratings"`
	Watchlist []ExportFilm `json:"watchlist"`
	Reviews   []ExportFilm `json:"reviews"`
	Likes     []ExportFilm `json:"likes"`
	// Files lists the files that were read ("diary.csv", "likes/films.csv"...).
	Files []string `json:"files"`
}

// Merge adds the rows of o (another file of the same export) to e.
func (e *Export) Merge(o *Export) {
	if o == nil {
		return
	}
	if o.Profile != nil {
		e.Profile = o.Profile
	}
	e.Diary = append(e.Diary, o.Diary...)
	e.Watched = append(e.Watched, o.Watched...)
	e.Ratings = append(e.Ratings, o.Ratings...)
	e.Watchlist = append(e.Watchlist, o.Watchlist...)
	e.Reviews = append(e.Reviews, o.Reviews...)
	e.Likes = append(e.Likes, o.Likes...)
	e.Files = append(e.Files, o.Files...)
}

// Empty reports whether e has no film at all.
func (e *Export) Empty() bool {
	return e == nil || len(e.Diary)+len(e.Watched)+len(e.Ratings)+len(e.Watchlist)+len(e.Reviews)+len(e.Likes) == 0
}

// ErrNotExport: the file isn't (part of) a Letterboxd data export.
var ErrNotExport = errors.New("letterboxd: not a Letterboxd data export (the ZIP from letterboxd.com › Settings › Data › Export, or one of its CSV files)")

const (
	kindProfile   = "profile"
	kindDiary     = "diary"
	kindWatched   = "watched"
	kindRatings   = "ratings"
	kindWatchlist = "watchlist"
	kindReviews   = "reviews"
	kindLikes     = "likes"
)

// kindFiles are the export's files, in the order they're read.
var kindFiles = []struct{ kind, file string }{
	{kindProfile, "profile.csv"},
	{kindWatched, "watched.csv"},
	{kindDiary, "diary.csv"},
	{kindReviews, "reviews.csv"},
	{kindRatings, "ratings.csv"},
	{kindLikes, "likes/films.csv"},
	{kindWatchlist, "watchlist.csv"},
}

// Size limits: a big export is a few MB.
const (
	maxCSVSize = 64 << 20
	maxZipSize = 256 << 20
)

// ReadExportZip reads the export ZIP. Its files may sit in a folder (when the
// ZIP was unpacked and zipped again); the copies in deleted/ and orphaned/
// are left out.
func ReadExportZip(r io.ReaderAt, size int64) (*Export, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("letterboxd: not a ZIP file: %w", err)
	}
	found := map[string]*zip.File{}
	depth := map[string]int{}
	for _, f := range zr.File {
		kind, d := zipKind(f.Name)
		if kind == "" || f.FileInfo().IsDir() {
			continue
		}
		if cur, ok := found[kind]; !ok || d < depth[kind] || (d == depth[kind] && f.Name < cur.Name) {
			found[kind] = f
			depth[kind] = d
		}
	}
	if len(found) == 0 {
		return nil, ErrNotExport
	}
	ex := &Export{}
	for _, kf := range kindFiles {
		f := found[kf.kind]
		if f == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("letterboxd: %s: %w", f.Name, err)
		}
		t, err := readCSV(io.LimitReader(rc, maxCSVSize))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("letterboxd: %s: %w", f.Name, err)
		}
		ex.add(kf.kind, t)
		ex.Files = append(ex.Files, kf.file)
	}
	return ex, nil
}

// zipKind is the kind of export file at name, and how deep in folders it
// is; "" for files KumoBox doesn't read.
func zipKind(name string) (string, int) {
	name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
	parts := strings.Split(strings.Trim(name, "/"), "/")
	for _, p := range parts {
		if p == "__MACOSX" {
			return "", 0
		}
	}
	base := strings.ToLower(parts[len(parts)-1])
	parent := ""
	if len(parts) > 1 {
		parent = strings.ToLower(parts[len(parts)-2])
	}
	if base == "films.csv" && parent == "likes" {
		return kindLikes, len(parts) - 2
	}
	switch parent {
	case "deleted", "orphaned", "lists", "likes":
		return "", 0
	}
	for _, kf := range kindFiles {
		if base == kf.file {
			return kf.kind, len(parts) - 1
		}
	}
	return "", 0
}

// ReadExportFile reads one uploaded file: the export ZIP, or one of its CSV
// files (which one is told by its columns, and by name for the files that
// have the same columns: watched, watchlist and likes/films).
func ReadExportFile(name string, r io.Reader) (*Export, error) {
	br := bufio.NewReader(r)
	if head, _ := br.Peek(4); bytes.Equal(head, []byte("PK\x03\x04")) {
		data, err := io.ReadAll(io.LimitReader(br, maxZipSize+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxZipSize {
			return nil, fmt.Errorf("letterboxd: %s is too big", name)
		}
		return ReadExportZip(bytes.NewReader(data), int64(len(data)))
	}
	t, err := readCSV(io.LimitReader(br, maxCSVSize))
	if err != nil {
		return nil, fmt.Errorf("letterboxd: %s: %w", name, err)
	}
	kind := kindFromHeader(t, name)
	if kind == "" {
		return nil, ErrNotExport
	}
	ex := &Export{}
	ex.add(kind, t)
	file := kind + ".csv"
	if kind == kindLikes {
		file = "likes/films.csv"
	}
	ex.Files = []string{file}
	return ex, nil
}

func kindFromHeader(t *table, name string) string {
	switch {
	case t.has("datejoined") || (t.has("username") && !t.has("name")):
		return kindProfile
	case !t.has("name"):
		return ""
	case t.has("review"):
		return kindReviews
	case t.has("watcheddate"):
		return kindDiary
	case t.has("rating"):
		return kindRatings
	}
	name = strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(name)
	switch {
	case strings.Contains(base, "watchlist"):
		return kindWatchlist
	case strings.Contains(base, "like") || base == "films.csv" || strings.Contains(name, "likes/"):
		return kindLikes
	}
	return kindWatched
}

func (e *Export) add(kind string, t *table) {
	if kind == kindProfile {
		if len(t.rows) > 0 {
			row := t.rows[0]
			e.Profile = &Profile{
				Username:   strings.TrimSpace(t.get(row, "username")),
				GivenName:  strings.TrimSpace(t.get(row, "givenname")),
				FamilyName: strings.TrimSpace(t.get(row, "familyname")),
				DateJoined: normDate(t.get(row, "datejoined")),
			}
		}
		return
	}
	var out []ExportFilm
	for _, row := range t.rows {
		if f, ok := t.film(row); ok {
			out = append(out, f)
		}
	}
	switch kind {
	case kindDiary:
		e.Diary = append(e.Diary, out...)
	case kindWatched:
		e.Watched = append(e.Watched, out...)
	case kindRatings:
		e.Ratings = append(e.Ratings, out...)
	case kindWatchlist:
		e.Watchlist = append(e.Watchlist, out...)
	case kindReviews:
		e.Reviews = append(e.Reviews, out...)
	case kindLikes:
		e.Likes = append(e.Likes, out...)
	}
}

// table is a CSV file: its rows and where each column is.
type table struct {
	cols map[string]int
	rows [][]string
}

// colAliases are other names a column goes by (in CSVs written for
// Letterboxd's importer rather than by its exporter).
var colAliases = map[string][]string{
	"name":          {"title"},
	"letterboxduri": {"uri", "url"},
}

func (t *table) index(col string) (int, bool) {
	if i, ok := t.cols[col]; ok {
		return i, true
	}
	for _, alias := range colAliases[col] {
		if i, ok := t.cols[alias]; ok {
			return i, true
		}
	}
	return 0, false
}

func (t *table) has(col string) bool {
	_, ok := t.index(col)
	return ok
}

func (t *table) get(row []string, col string) string {
	i, ok := t.index(col)
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

func (t *table) film(row []string) (ExportFilm, bool) {
	f := ExportFilm{
		Date:        normDate(t.get(row, "date")),
		Name:        strings.TrimSpace(t.get(row, "name")),
		Year:        parseYear(t.get(row, "year")),
		URI:         strings.TrimSpace(t.get(row, "letterboxduri")),
		Rating:      parseStars(t.get(row, "rating")),
		Rewatch:     yes(t.get(row, "rewatch")),
		Tags:        strings.TrimSpace(t.get(row, "tags")),
		WatchedDate: normDate(t.get(row, "watcheddate")),
		Review:      reviewText(t.get(row, "review")),
	}
	return f, f.Name != ""
}

func parseYear(s string) int {
	s = strings.TrimSpace(s)
	if y, err := strconv.Atoi(s); err == nil {
		return y
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return 0
}

// reviewText converts a review of the export (plain text, possibly with the
// few HTML tags Letterboxd allows) to plain text, keeping its line breaks.
func reviewText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	text, _, _ := parseDescription(strings.ReplaceAll(s, "\n", "<br>"))
	return text
}

// normCol reduces a column name to lower-case letters and digits ("Watched
// Date", "WatchedDate" and "watched_date" are all "watcheddate").
func normCol(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// readCSV reads a CSV file with a header row, tolerating a byte order mark,
// rows with missing or extra fields, stray quotes and blank lines.
func readCSV(r io.Reader) (*table, error) {
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, bom) {
		_, _ = br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	t := &table{cols: map[string]int{}}
	header := false
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if blankRecord(rec) {
			continue
		}
		if !header {
			for i, h := range rec {
				if k := normCol(h); k != "" {
					if _, dup := t.cols[k]; !dup {
						t.cols[k] = i
					}
				}
			}
			header = true
			continue
		}
		t.rows = append(t.rows, rec)
	}
	if !header {
		return nil, errors.New("empty file")
	}
	return t, nil
}

func blankRecord(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}
