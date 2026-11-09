package api

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSampleManga makes a series in a manga folder: series.json, a cover
// and one chapter of two pages.
func writeSampleManga(t *testing.T, dir string) string {
	t.Helper()
	folder := filepath.Join(dir, "Test Manga [anilist-42]")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "series.json"), []byte(`{"anilistId":42,"title":"Test Manga","chapters":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "cover.jpg"), []byte("\xff\xd8cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(folder, "Test Manga - Ch. 001.cbz"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"001.jpg": "\xff\xd8page one", "002.jpg": "\xff\xd8page two", "ComicInfo.xml": "<ComicInfo><Number>1</Number><Title>The Start</Title></ComicInfo>"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()
	return "Test Manga [anilist-42]/Test Manga - Ch. 001.cbz"
}

// The manga downloaded onto a host (a server) are read on the Aniko it
// shares with: in its Downloaded tab, on the manga's page, and in the
// reader (chapters and pages through the guest); an Aniko it doesn't share
// with can't read them.
func TestMangaFromTheHost(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	other, _ := sharingServer(t)
	mangaDir := t.TempDir()
	cfg := host.app.Settings.Get()
	cfg.Manga.Dir = mangaDir
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	file := writeSampleManga(t, mangaDir)
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	for _, g := range []*Server{guest, other} {
		if w := do(g, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
			t.Fatalf("connect: %d %s", w.Code, w.Body)
		}
		eventually(t, "the host to list it", func() bool { _, ok := peerIn(host, g); return ok })
	}
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	hid := host.app.Share.ID()
	eventually(t, "the library", func() bool {
		do(guest, "GET", "/api/sharing?refresh=1", local, "", nil)
		return len(guest.app.Share.Libraries()) == 1
	})

	// The Downloaded tab: the host's series.
	var list []struct {
		Host   struct{ ID, Name string }
		Series []struct {
			AnilistID int `json:"anilistId"`
			Title     string
			Count     int
		}
	}
	w := do(guest, "GET", "/api/manga-shared", local, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].Host.ID != hid || len(list[0].Series) != 1 || list[0].Series[0].AnilistID != 42 || list[0].Series[0].Count != 1 {
		t.Fatalf("shared manga: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "GET", "/api/manga-shared/cover?host="+hid+"&id=42", local, "", nil); w.Code != http.StatusOK || w.Body.String() != "\xff\xd8cover" {
		t.Fatalf("cover: %d %q", w.Code, w.Body)
	}
	// The manga's page: its chapters there.
	var one []struct {
		Chapters []struct{ File, Chapter, Title string }
	}
	w = do(guest, "GET", "/api/manga-shared/series/42", local, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &one) != nil || len(one) != 1 || len(one[0].Chapters) != 1 || one[0].Chapters[0].File != file || one[0].Chapters[0].Title != "The Start" {
		t.Fatalf("its chapters: %d %s", w.Code, w.Body)
	}
	// The reader: chapters and pages by the server: provider.
	provider := "server:" + hid
	var ch struct {
		Chapters []struct{ ID, Chapter string }
	}
	w = do(guest, "GET", "/api/manga/42/chapters?provider="+url.QueryEscape(provider), local, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &ch) != nil || len(ch.Chapters) != 1 || ch.Chapters[0].ID != file || ch.Chapters[0].Chapter != "1" {
		t.Fatalf("reader's chapters: %d %s", w.Code, w.Body)
	}
	var pages []struct{ URL string }
	w = do(guest, "GET", "/api/manga/pages?provider="+url.QueryEscape(provider)+"&chapterId="+url.QueryEscape(file), local, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &pages) != nil || len(pages) != 2 {
		t.Fatalf("pages: %d %s", w.Code, w.Body)
	}
	if w := do(guest, "GET", pages[1].URL, local, "", nil); w.Code != http.StatusOK || w.Body.String() != "\xff\xd8page two" {
		t.Fatalf("page 2: %d %q", w.Code, w.Body)
	}
	// Not for an Aniko the host doesn't share with.
	req, _ := http.NewRequest("GET", hostSrv.URL+"/api/peer/manga-library", nil)
	if err := other.app.Share.Sign(req, hid, host.app.Share.Whoami().Key); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an Aniko it doesn't share with: %v %v", resp, err)
	}
}
