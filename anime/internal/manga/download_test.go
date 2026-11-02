package manga

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/extensions"
)

func pngBytes(t *testing.T, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	for x := 0; x < 4; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A site with two pages per chapter and a cover; a referer it insists on.
func testSite(t *testing.T) *httptest.Server {
	red, blue := pngBytes(t, color.RGBA{255, 0, 0, 255}), pngBytes(t, color.RGBA{0, 0, 255, 255})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover" && r.Header.Get("Referer") != "https://reader.example/" {
			http.Error(w, "no referer", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/a.png", "/cover":
			w.Write(red)
		case "/b.png":
			w.Write(blue)
		default:
			http.NotFound(w, r)
		}
	}))
}

func testDownloader(t *testing.T, site *httptest.Server, send Sender) (*Downloader, *Library) {
	dir := t.TempDir()
	lib := &Library{Dir: func() string { return filepath.Join(dir, "Manga") }}
	chapters := 3
	media := func(ctx context.Context, id int) (*anilist.Media, error) {
		m := &anilist.Media{ID: id, Format: "MANGA", Status: "RELEASING", Genres: []string{"Action", "Comedy"}, Chapters: &chapters,
			Description: "<b>Two</b> friends &amp; a cat.<br>The end."}
		m.Title.English = "Test: Manga?"
		m.Title.Romaji = "Tesuto"
		m.CoverImage.Large = site.URL + "/cover"
		return m, nil
	}
	d := NewDownloader(&Service{}, lib, media, send, filepath.Join(dir, "sending"))
	d.client = site.Client() // the test site is on this computer
	d.pages = func(ctx context.Context, provider, chapterID string) ([]extensions.MangaPage, error) {
		h := map[string]string{"Referer": "https://reader.example/"}
		return []extensions.MangaPage{{URL: site.URL + "/b.png", Index: 1, Headers: h}, {URL: site.URL + "/a.png", Index: 0, Headers: h}}, nil
	}
	return d, lib
}

func waitDone(t *testing.T, d *Downloader, n int) []Job {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		jobs := d.List()
		done := 0
		for _, j := range jobs {
			if j.State == Done || j.State == Failed {
				done++
			}
		}
		if done >= n {
			return jobs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("downloads didn't finish: %+v", d.List())
	return nil
}

func TestDownloadChapters(t *testing.T) {
	site := testSite(t)
	defer site.Close()
	d, lib := testDownloader(t, site, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	n, err := d.Add(42, "", "prov", []ChapterRef{{ID: "c1", Chapter: "1", Title: "The start"}, {ID: "c2", Chapter: "2.5"}}, "", "")
	if err != nil || n != 2 {
		t.Fatalf("added %d, %v", n, err)
	}
	for _, j := range waitDone(t, d, 2) {
		if j.State != Done || j.Pages != 2 || j.Got != 2 {
			t.Errorf("job %+v", j)
		}
	}

	series, chapters, err := lib.Series(42)
	if err != nil {
		t.Fatal(err)
	}
	if series.Folder != "Test Manga [anilist-42]" || series.Count != 2 || !series.HasCover || series.Latest != "2.5" {
		t.Errorf("series %+v", series)
	}
	if series.Title != "Test: Manga?" || series.Description != "Two friends & a cat.\nThe end." || series.Chapters != 3 || series.SiteURL != "https://anilist.co/manga/42" {
		t.Errorf("series.json %+v", series.SeriesInfo)
	}
	if len(chapters) != 2 || chapters[0].Number != "1" || chapters[0].Title != "The start" || chapters[0].Pages != 2 ||
		chapters[1].Number != "2.5" || chapters[0].File != "Test Manga [anilist-42]/Test Manga - Ch. 001.cbz" {
		t.Errorf("chapters %+v", chapters)
	}

	// The pages in the provider's order (a, then b), and the metadata.
	rc, ct, err := lib.Page(chapters[0].File, 0)
	if err != nil || ct != "image/png" {
		t.Fatalf("page: %v %s", err, ct)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	img, err := png.Decode(bytes.NewReader(got))
	if r, g, b, _ := img.At(0, 0).RGBA(); err != nil || r != 0xffff || g != 0 || b != 0 {
		t.Errorf("page 1 isn't the first page: %v", err)
	}
	ci, pages, err := readCBZ(filepath.Join(lib.Dir(), filepath.FromSlash(chapters[1].File)))
	if err != nil || pages != 2 || ci == nil || ci.Series != "Test: Manga?" || ci.Number != "2.5" || ci.Web != "https://anilist.co/manga/42" ||
		ci.Genre != "Action, Comedy" || ci.Manga != "YesAndRightToLeft" || ci.PageCount != 2 || !strings.Contains(ci.Notes, "AniList 42") {
		t.Errorf("ComicInfo %+v, %d pages, %v", ci, pages, err)
	}

	// Downloaded already: not again.
	if n, _ := d.Add(42, "", "prov", []ChapterRef{{ID: "c1", Chapter: "1"}}, "", ""); n != 0 {
		t.Errorf("a downloaded chapter was queued again")
	}
	if !lib.Has(42, "2.5") || lib.Has(42, "3") {
		t.Error("Has")
	}

	// Nothing outside the folder is read.
	for _, bad := range []string{"../x.cbz", "Test Manga [anilist-42]/../../x.cbz", "/etc/passwd", "Test Manga [anilist-42]/series.json", "a/b/c.cbz"} {
		if _, _, err := lib.Page(bad, 0); err == nil {
			t.Errorf("read %q", bad)
		}
	}
	if _, _, err := lib.Page(chapters[0].File, 2); err == nil {
		t.Error("a page past the end")
	}

	list, _ := lib.List()
	if len(list) != 1 || list[0].AniListID != 42 {
		t.Errorf("list %+v", list)
	}
	if err := lib.Delete(42, chapters[0].File); err != nil {
		t.Fatal(err)
	}
	if _, chapters, _ = lib.Series(42); len(chapters) != 1 {
		t.Errorf("after deleting one: %d", len(chapters))
	}
	if err := lib.Delete(42, ""); err != nil {
		t.Fatal(err)
	}
	if list, _ := lib.List(); len(list) != 0 {
		t.Errorf("after deleting the manga: %+v", list)
	}
}

// For a media server: built in the staging folder, sent with the series'
// details and cover, then gone from here.
func TestDownloadToServer(t *testing.T) {
	site := testSite(t)
	defer site.Close()
	var sentFolder string
	var sent []string
	send := func(ctx context.Context, target, folder string, files []string, progress func(int)) error {
		if target != "pi" {
			t.Errorf("target %q", target)
		}
		sentFolder = folder
		for _, f := range files {
			if _, err := os.Stat(f); err != nil {
				t.Errorf("sent a file that isn't there: %s", f)
			}
			sent = append(sent, filepath.Base(f))
		}
		progress(100)
		return nil
	}
	d, lib := testDownloader(t, site, send)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	if n, _ := d.Add(42, "", "prov", []ChapterRef{{ID: "c1", Chapter: "7"}}, "pi", "Pi"); n != 1 {
		t.Fatal("not added")
	}
	jobs := waitDone(t, d, 1)
	if jobs[0].State != Done || jobs[0].TargetName != "Pi" {
		t.Fatalf("job %+v", jobs[0])
	}
	if sentFolder != "Test Manga [anilist-42]" || strings.Join(sent, ",") != "series.json,Test Manga - Ch. 007.cbz,cover.jpg" {
		t.Errorf("sent %q %v", sentFolder, sent)
	}
	if list, _ := lib.List(); len(list) != 0 {
		t.Errorf("kept here too: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(d.staging, sentFolder, "Test Manga - Ch. 007.cbz")); err == nil {
		t.Error("the chapter stayed in the staging folder")
	}
	var info SeriesInfo
	raw, _ := os.ReadFile(filepath.Join(d.staging, sentFolder, "series.json"))
	if json.Unmarshal(raw, &info) != nil || info.AniListID != 42 {
		t.Errorf("series.json in staging: %s", raw)
	}
}

func TestNames(t *testing.T) {
	if got := SeriesFolder(`  A/B: C*? "D".  `, 7); got != "A B C D [anilist-7]" {
		t.Errorf("folder %q", got)
	}
	for in, want := range map[string]string{"1": "001", "12.5": "012.5", "1234": "1234", "": "000"} {
		if got := padNumber(in); got != want {
			t.Errorf("pad %q = %q", in, got)
		}
	}
	for in, want := range map[string]string{"X - Ch. 001.cbz": "1", "X - Ch. 012.5.cbz": "12.5", "X - Ch. 000.cbz": "0"} {
		if got, ok := numberOf(in); !ok || got != want {
			t.Errorf("number of %q = %q", in, got)
		}
	}
}
