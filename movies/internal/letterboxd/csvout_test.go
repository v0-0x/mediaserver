package letterboxd

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

func TestWriteImportCSV(t *testing.T) {
	var buf bytes.Buffer
	err := WriteImportCSV(&buf, []ImportRow{
		{Title: "The Matrix", Year: 1999, TMDBID: 603, IMDbID: "tt0133093", WatchedDate: "2024-03-10", Rating: 4.5, Rewatch: true, Review: "Still \"holds\" up,\nreally.", Tags: "cinema, friends"},
		{Title: "Heat", Year: 1995, TMDBID: 949, WatchedDate: "2024-03-11", Rating: 4},
		{Title: "  Chernobyl \n", Year: 2019, IMDbID: "tt7366338", WatchedDate: "bad date", Rating: 0},
		{Title: "Odd rating", Rating: 3.7},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Title,Year,tmdbID,imdbID,WatchedDate,Rating,Rewatch,Review,Tags\n" +
		"The Matrix,1999,603,tt0133093,2024-03-10,4.5,true,\"Still \"\"holds\"\" up,\nreally.\",\"cinema, friends\"\n" +
		"Heat,1995,949,,2024-03-11,4.0,false,,\n" +
		"Chernobyl,2019,,tt7366338,,,false,,\n" +
		"Odd rating,,,,,3.5,false,,\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
	// It reads back as the same table.
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil || len(recs) != 5 || recs[1][7] != "Still \"holds\" up,\nreally." {
		t.Errorf("read back: %v %q", err, recs)
	}
}

func TestWriteWatchlistCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWatchlistCSV(&buf, []ImportRow{{Title: "Alien", Year: 1979, TMDBID: 348, IMDbID: "tt0078748", Rating: 5}, {Title: "Severance", IMDbID: "tt11280740"}}); err != nil {
		t.Fatal(err)
	}
	want := "Title,Year,tmdbID,imdbID\nAlien,1979,348,tt0078748\nSeverance,,,tt11280740\n"
	if buf.String() != want {
		t.Errorf("got:\n%s", buf.String())
	}
}

func TestFormatRating(t *testing.T) {
	for stars, want := range map[float64]string{0: "", -1: "", 0.5: "0.5", 0.2: "0.5", 3: "3.0", 3.74: "3.5", 3.76: "4.0", 5: "5.0", 9: "5.0"} {
		if got := FormatRating(stars); got != want {
			t.Errorf("FormatRating(%v) = %q, want %q", stars, got, want)
		}
	}
}
