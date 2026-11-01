package letterboxd

import (
	"archive/zip"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// exportFiles is a small but realistic data export (as Letterboxd writes it,
// with a byte order mark on profile.csv and CRLF line ends in reviews.csv).
var exportFiles = map[string]string{
	"profile.csv": string(bom) + "Date Joined,Username,Given Name,Family Name,Email Address,Location,Website,Bio,Pronoun,Favorite Films\n" +
		"2019-06-01,davevis,Dave,Vis,dave@example.com,,,\"Films, mostly.\",He / his,\n",
	"diary.csv": "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Tags,Watched Date\n" +
		"2020-05-02,The Matrix,1999,https://boxd.it/aaa1,4,,,2020-05-01\n" +
		"2024-03-11,The Matrix,1999,https://boxd.it/aaa2,4.5,Yes,\"cinema, friends\",2024-03-10\n" +
		"2023-11-05,Parasite,2019,https://boxd.it/bbb1,5,,,2023-11-04\n" +
		"2022-01-01,Unknown Film,2001,https://boxd.it/zzz1,2,,,2022-01-01\n",
	"watched.csv": "Date,Name,Year,Letterboxd URI\n" +
		"2020-05-02,The Matrix,1999,https://boxd.it/2a1m\n" +
		"2023-11-05,Parasite,2019,https://boxd.it/hTha\n" +
		"2019-06-01,Heat,1995,https://boxd.it/2bfq\n" +
		"2022-01-01,Unknown Film,2001,https://boxd.it/zzzz\n",
	"ratings.csv": "Date,Name,Year,Letterboxd URI,Rating\n" +
		"2024-03-11,The Matrix,1999,https://boxd.it/2a1m,4.5\n" +
		"2023-11-05,Parasite,2019,https://boxd.it/hTha,5\n" +
		"2019-06-01,Heat,1995,https://boxd.it/2bfq,3.5\n",
	"reviews.csv": "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Review,Tags,Watched Date\r\n" +
		"2024-03-11,The Matrix,1999,https://boxd.it/aaa2,4.5,Yes,\"Still holds up.\r\n\r\nThe <i>lobby</i> scene, \"\"wow\"\".\",\"cinema, friends\",2024-03-10\r\n" +
		"2019-06-02,Heat,1995,https://boxd.it/ccc1,3.5,,\"The diner scene.\",,\r\n",
	"likes/films.csv": "Date,Name,Year,Letterboxd URI\n" +
		"2024-03-11,The Matrix,1999,https://boxd.it/2a1m\n",
	"watchlist.csv": "Date,Name,Year,Letterboxd URI\n" +
		"2024-01-01,Alien,1979,https://boxd.it/2bc8\n" +
		"2018-01-01,Heat,1995,https://boxd.it/2bfq\n",
	// Not read: deleted entries, lists, liked reviews, comments.
	"deleted/diary.csv": "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Tags,Watched Date\n" +
		"2021-01-01,Deleted Film,2000,https://boxd.it/del1,1,,,2021-01-01\n",
	"lists/best-of-the-nineties.csv": "Letterboxd list export v7\nDate,Name,Tags,URL,Description\n2024-03-09,Best of the nineties,,https://boxd.it/l1,\n\nPosition,Name,Year,URL,Description\n1,The Matrix,1999,https://boxd.it/2a1m,\n",
	"likes/reviews.csv":              "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Review,Tags,Watched Date\n2024-01-01,Other,2010,https://boxd.it/o1,3,,Nice,,2024-01-01\n",
	"comments.csv":                   "Date,Content,Comment\n2024-01-01,https://boxd.it/x,hi\n",
}

// buildZip zips files, under folder when it isn't "".
func buildZip(t *testing.T, folder string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		if folder != "" {
			name = folder + "/" + name
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	// macOS junk
	if w, err := zw.Create("__MACOSX/._diary.csv"); err == nil {
		_, _ = w.Write([]byte{0, 5, 22, 7})
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkExport(t *testing.T, ex *Export) {
	t.Helper()
	if ex.Profile == nil || ex.Profile.Username != "davevis" || ex.Profile.GivenName != "Dave" || ex.Profile.DateJoined != "2019-06-01" {
		t.Errorf("profile = %+v", ex.Profile)
	}
	if len(ex.Diary) != 4 || len(ex.Watched) != 4 || len(ex.Ratings) != 3 || len(ex.Reviews) != 2 || len(ex.Likes) != 1 || len(ex.Watchlist) != 2 {
		t.Fatalf("counts: diary %d watched %d ratings %d reviews %d likes %d watchlist %d",
			len(ex.Diary), len(ex.Watched), len(ex.Ratings), len(ex.Reviews), len(ex.Likes), len(ex.Watchlist))
	}
	want := ExportFilm{Date: "2024-03-11", Name: "The Matrix", Year: 1999, URI: "https://boxd.it/aaa2", Rating: 4.5, Rewatch: true, Tags: "cinema, friends", WatchedDate: "2024-03-10"}
	if ex.Diary[1] != want {
		t.Errorf("diary row = %+v", ex.Diary[1])
	}
	rv := ex.Reviews[0]
	if rv.Review != "Still holds up.\n\nThe lobby scene, \"wow\"." || rv.WatchedDate != "2024-03-10" || !rv.Rewatch {
		t.Errorf("review = %+v", rv)
	}
	if ex.Reviews[1].WatchedDate != "" || ex.Reviews[1].Review != "The diner scene." {
		t.Errorf("undated review = %+v", ex.Reviews[1])
	}
	for _, d := range ex.Diary {
		if d.Name == "Deleted Film" {
			t.Error("deleted/diary.csv was read")
		}
	}
	if ex.Ratings[2].Rating != 3.5 || ex.Watched[2].URI != "https://boxd.it/2bfq" {
		t.Errorf("ratings/watched = %+v / %+v", ex.Ratings[2], ex.Watched[2])
	}
	wantFiles := []string{"profile.csv", "watched.csv", "diary.csv", "reviews.csv", "ratings.csv", "likes/films.csv", "watchlist.csv"}
	if !reflect.DeepEqual(ex.Files, wantFiles) {
		t.Errorf("files = %v", ex.Files)
	}
}

func TestReadExportZip(t *testing.T) {
	for _, folder := range []string{"", "letterboxd-davevis-2024-03-11-10-00-utc"} {
		t.Run("folder="+folder, func(t *testing.T) {
			data := buildZip(t, folder, exportFiles)
			ex, err := ReadExportZip(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			checkExport(t, ex)
			// The same through ReadExportFile.
			ex, err = ReadExportFile("letterboxd.zip", bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			checkExport(t, ex)
		})
	}
}

func TestReadExportZipNotExport(t *testing.T) {
	data := buildZip(t, "", map[string]string{"readme.txt": "hello", "lists/x.csv": "a,b\n"})
	if _, err := ReadExportZip(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrNotExport) {
		t.Errorf("err = %v", err)
	}
	if _, err := ReadExportZip(strings.NewReader("not a zip"), 9); err == nil {
		t.Error("garbage read as a ZIP")
	}
}

func TestReadExportFileKinds(t *testing.T) {
	tests := []struct {
		name, file, content string
		kind                string
	}{
		{"diary", "diary.csv", exportFiles["diary.csv"], kindDiary},
		{"reviews", "reviews.csv", exportFiles["reviews.csv"], kindReviews},
		{"ratings", "ratings.csv", exportFiles["ratings.csv"], kindRatings},
		{"profile", "profile.csv", exportFiles["profile.csv"], kindProfile},
		{"watched", "watched.csv", exportFiles["watched.csv"], kindWatched},
		{"watchlist by name", "my-watchlist (1).csv", exportFiles["watchlist.csv"], kindWatchlist},
		{"likes by folder", "likes/films.csv", exportFiles["likes/films.csv"], kindLikes},
		{"likes alone", "films.csv", exportFiles["likes/films.csv"], kindLikes},
		{"unknown name is watched", "export.csv", exportFiles["watched.csv"], kindWatched},
		{"diary by header whatever the name", "watchlist.csv", exportFiles["diary.csv"], kindDiary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex, err := ReadExportFile(tt.file, strings.NewReader(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{
				kindDiary: len(ex.Diary), kindReviews: len(ex.Reviews), kindRatings: len(ex.Ratings),
				kindWatched: len(ex.Watched), kindWatchlist: len(ex.Watchlist), kindLikes: len(ex.Likes),
			}
			if ex.Profile != nil {
				counts[kindProfile] = 1
			}
			for kind, n := range counts {
				if (kind == tt.kind) != (n > 0) {
					t.Errorf("%s has %d rows (want kind %s)", kind, n, tt.kind)
				}
			}
		})
	}
	for _, bad := range []string{exportFiles["lists/best-of-the-nineties.csv"], exportFiles["comments.csv"], "", "\n\n"} {
		if _, err := ReadExportFile("x.csv", strings.NewReader(bad)); err == nil {
			t.Errorf("%q read as an export file", bad)
		}
	}
}

func TestReadExportFileQuirks(t *testing.T) {
	// Columns in another order, one missing (Letterboxd URI), a BOM, CRLF,
	// blank lines, a stray quote, a short row, an extra column, a multi-line
	// quoted review, a rating with a comma, an empty name.
	csv := string(bom) + "Watched Date,Name,Year,Rating,Rewatch,Review,Extra\r\n" +
		"\r\n" +
		"2024-01-02,Heat,1995,\"4,5\",yes,\"Line one\nline \"\"two\"\"\",x\r\n" +
		"2024-01-03,Alien,1979,,,He said \"hi\" twice\r\n" +
		"2024-01-04,Short\r\n" +
		",,,,,,\r\n" +
		"2024-01-05,,2000,3,,,\r\n"
	ex, err := ReadExportFile("reviews.csv", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	want := []ExportFilm{
		{Name: "Heat", Year: 1995, Rating: 4.5, Rewatch: true, WatchedDate: "2024-01-02", Review: "Line one\nline \"two\""},
		{Name: "Alien", Year: 1979, WatchedDate: "2024-01-03", Review: `He said "hi" twice`},
		{Name: "Short", WatchedDate: "2024-01-04"},
	}
	if !reflect.DeepEqual(ex.Reviews, want) {
		t.Errorf("rows:\n got %+v\nwant %+v", ex.Reviews, want)
	}
}

func TestExportMerge(t *testing.T) {
	a, _ := ReadExportFile("watched.csv", strings.NewReader(exportFiles["watched.csv"]))
	b, _ := ReadExportFile("ratings.csv", strings.NewReader(exportFiles["ratings.csv"]))
	a.Merge(b)
	a.Merge(nil)
	if len(a.Watched) != 4 || len(a.Ratings) != 3 || !reflect.DeepEqual(a.Files, []string{"watched.csv", "ratings.csv"}) || a.Empty() {
		t.Errorf("merged = %+v", a)
	}
	if !(&Export{}).Empty() {
		t.Error("empty export not Empty")
	}
}
