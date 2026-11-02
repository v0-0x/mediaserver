package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

func newTestScanner(t *testing.T, dirs ...string) (*Scanner, *config.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Get()
	cfg.Library.Dir = dirs[0]
	cfg.Library.ExtraDirs = dirs[1:]
	cfg.Library.AutoRefresh = false
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewScanner(NewStore(d), nil, settings, events.NewHub()), settings
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(t *testing.T, s *Scanner) []string {
	t.Helper()
	all, err := s.Store.All()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range all {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func scan(t *testing.T, s *Scanner) *ScanResult {
	t.Helper()
	res, err := s.Scan(context.Background(), ScanOptions{SkipMatching: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// waitIndexed waits for a scan to index path.
func waitIndexed(t *testing.T, s *Scanner, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if f, err := s.Store.Get(path); err == nil && f != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never indexed: %v", path, paths(t, s))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The watcher scans the library when a folder is moved into it (a torrent
// client moving a finished download): a release folder whose name has dots
// ("Show.S01.1080p.WEB-DL-GRP") too, though its videos send no events of
// their own; and its subfolders are watched from then on.
func TestWatcherSeesFoldersMovedIn(t *testing.T) {
	lib, outside := t.TempDir(), t.TempDir()
	s, settings := newTestScanner(t, lib)
	cfg := settings.Get()
	cfg.Library.AutoRefresh = true
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	debounce := watchDebounce
	watchDebounce = 50 * time.Millisecond
	t.Cleanup(func() { watchDebounce = debounce })
	s.StartWatcher()
	t.Cleanup(func() {
		// No scan may still read the folders when they're removed.
		s.StopWatcher()
		time.Sleep(4 * watchDebounce)
		for deadline := time.Now().Add(10 * time.Second); s.Running() && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		s.mu.Lock() // the last scan is over
		s.mu.Unlock()
	})

	release := filepath.Join(outside, "Show.S01.1080p.WEB-DL-GRP")
	touch(t, filepath.Join(release, "Season 1", "Show.S01E01.mkv"))
	moved := filepath.Join(lib, filepath.Base(release))
	if err := os.Rename(release, moved); err != nil {
		t.Fatal(err)
	}
	waitIndexed(t, s, filepath.Join(moved, "Season 1", "Show.S01E01.mkv"))
	touch(t, filepath.Join(moved, "Season 1", "Show.S01E02.mkv"))
	waitIndexed(t, s, filepath.Join(moved, "Season 1", "Show.S01E02.mkv"))
}

func TestScanFollowsSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "hdd", "TV")
	touch(t, filepath.Join(real, "Severance (2022)", "Season 01", "Severance.S01E01.1080p.mkv"))
	other := filepath.Join(base, "other", "Fargo")
	touch(t, filepath.Join(other, "Fargo.S01E01.mkv"))
	if err := os.Symlink(other, filepath.Join(real, "Fargo")); err != nil { // symlinked show folder
		if runtime.GOOS == "windows" {
			t.Skip("Windows only lets administrators (or Developer Mode) make symlinks:", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(real, "Severance (2022)", "loop")); err != nil { // loop back to the root
		t.Fatal(err)
	}
	root := filepath.Join(base, "Videos", "TV")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, root); err != nil { // symlinked library root
		t.Fatal(err)
	}
	s, _ := newTestScanner(t, root)
	scan(t, s)
	got := paths(t, s)
	want := []string{
		filepath.Join(root, "Fargo", "Fargo.S01E01.mkv"),
		filepath.Join(root, "Severance (2022)", "Season 01", "Severance.S01E01.1080p.mkv"),
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Paths are read below the root as configured.
	f, _ := s.Store.Get(want[0])
	if f.Parsed.Title != "Fargo" || f.Parsed.Folder != filepath.Join(root, "Fargo") || f.Parsed.Season != 1 || f.Parsed.Episode != 1 {
		t.Errorf("parsed %+v", f.Parsed)
	}
}

func TestScanKeepsFilesOfMissingDrives(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "TV")
	extra := filepath.Join(base, "usb", "Movies")
	mount := filepath.Join(base, "mnt") // a mount point that stays behind empty
	a := filepath.Join(main, "Show A", "Season 01", "Show A - S01E01.mkv")
	b := filepath.Join(main, "Show A", "Season 01", "Show A - S01E02.mkv")
	c := filepath.Join(extra, "Movie B (2010)", "Movie B (2010).mkv")
	d := filepath.Join(mount, "Movie C (2012)", "Movie.C.2012.1080p.mkv")
	for _, p := range []string{a, b, c, d} {
		touch(t, p)
	}
	s, _ := newTestScanner(t, main, extra, mount)
	if res := scan(t, s); res.Total != 4 {
		t.Fatalf("first scan found %d files", res.Total)
	}

	// The user deletes an episode, unplugs the USB drive and the other
	// drive gets unmounted (its mount point stays as an empty folder).
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "usb")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(mount, "Movie C (2012)")); err != nil {
		t.Fatal(err)
	}
	res := scan(t, s)
	got := paths(t, s)
	want := []string{a, d, c}
	sort.Strings(want)
	if res.Removed != 1 || len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("removed=%d got %q\nwant %q", res.Removed, got, want)
	}
}

// A folder taken out of the library, or the library turned off: its files
// go from the index, also when its drive is gone (moved to a Moviko Hub), but
// not those in a folder still in it, and nothing on the disk.
func TestForgetFoldersOutOfTheLibrary(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Anime")
	usb := filepath.Join(base, "usb", "Anime")
	a := filepath.Join(main, "Show A", "Show A - 01.mkv")
	b := filepath.Join(main, "Show B", "Show B - 01.mkv")
	c := filepath.Join(usb, "Show C", "Show C - 01.mkv")
	for _, p := range []string{a, b, c} {
		touch(t, p)
	}
	s, settings := newTestScanner(t, main, usb)
	scan(t, s)
	set := func(change func(*config.Settings)) {
		cfg := settings.Get()
		change(&cfg)
		if _, err := settings.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}

	// The USB drive went to the hub, and out of the library.
	if err := os.RemoveAll(filepath.Join(base, "usb")); err != nil {
		t.Fatal(err)
	}
	set(func(c *config.Settings) { c.Library.ExtraDirs = nil })
	s.Forget([]string{usb})
	if got := paths(t, s); len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("after the USB folder went: %q", got)
	}
	// Only one show's folder is the library now: the rest of the old one goes.
	set(func(c *config.Settings) { c.Library.Dir = filepath.Join(main, "Show A") })
	s.Forget([]string{main})
	if got := paths(t, s); len(got) != 1 || got[0] != a {
		t.Fatalf("after narrowing the folder: %q", got)
	}
	// The library off: nothing left, and the file is still on the disk.
	set(func(c *config.Settings) { c.Library.Off = true })
	if len(settings.Get().LibraryDirs()) != 0 {
		t.Fatal("folders while the library is off")
	}
	s.Forget([]string{filepath.Join(main, "Show A")})
	if got := paths(t, s); len(got) != 0 {
		t.Fatalf("with the library off: %q", got)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatalf("the file itself: %v", err)
	}
	if _, err := s.Scan(context.Background(), ScanOptions{SkipMatching: true}); !errors.Is(err, ErrLibraryOff) {
		t.Fatalf("a scan with the library off: %v", err)
	}

	// Still indexed from before, outside every folder: one on a drive that
	// went to the hub (kept so far, its drive being gone), and a download
	// still on this computer. With the library off, the first goes.
	moved := filepath.Join(base, "mnt", "big", "Media", "Show E - 01.mkv")
	download := filepath.Join(base, "Downloads", "Show F - 01.mkv")
	touch(t, download)
	if err := s.Store.Save(&LocalFile{Path: moved, Dir: filepath.Dir(moved), Name: filepath.Base(moved), MediaID: 5, Episode: 1, Kind: "main"},
		&LocalFile{Path: download, Dir: filepath.Dir(download), Name: filepath.Base(download), MediaID: 6, Episode: 1, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	s.ForgetGone()
	if got := paths(t, s); len(got) != 1 || got[0] != download {
		t.Fatalf("after forgetting what isn't there: %q", got)
	}
}

func TestScanKeepsDownloadsOutsideLibrary(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "TV")
	touch(t, filepath.Join(lib, "Show", "Show.S01E01.mkv"))
	s, _ := newTestScanner(t, lib)
	dl := filepath.Join(base, "Downloads", "Show", "Season 01", "Show - S01E02.mp4")
	touch(t, dl)
	// Registered by the downloader, outside the library folders.
	if err := s.Store.Save(&LocalFile{Path: dl, Dir: filepath.Dir(dl), Name: filepath.Base(dl), MediaID: catalog.ShowID(7), Episode: 1002, Kind: KindMain, Locked: true}); err != nil {
		t.Fatal(err)
	}
	scan(t, s)
	if f, _ := s.Store.Get(dl); f == nil || f.MediaID != catalog.ShowID(7) || f.Episode != 1002 {
		t.Fatalf("download was dropped by the scan: %+v", f)
	}
	if err := os.Remove(dl); err != nil {
		t.Fatal(err)
	}
	scan(t, s)
	if f, _ := s.Store.Get(dl); f != nil {
		t.Fatalf("deleted download is still indexed")
	}
}

func TestSaveScannedKeepsConcurrentChanges(t *testing.T) {
	s, _ := newTestScanner(t, t.TempDir())
	orig := &LocalFile{Path: "/lib/x.mkv", Dir: "/lib", Name: "x.mkv", Kind: KindMain}
	if err := s.Store.Save(orig); err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]*LocalFile{orig.Path: orig}
	// While the scan was matching, the user matched the file by hand…
	manual := *orig
	manual.MediaID, manual.Episode, manual.Locked = 5, 3, true
	if err := s.Store.Save(&manual); err != nil {
		t.Fatal(err)
	}
	// …and a download registered a new file the scan also found.
	dl := &LocalFile{Path: "/lib/y.mkv", Dir: "/lib", Name: "y.mkv", MediaID: 9, Episode: 1, Kind: KindMain, Locked: true}
	if err := s.Store.Save(dl); err != nil {
		t.Fatal(err)
	}

	scanned := *orig
	scanned.MediaID, scanned.Episode = 8, 1
	newer := &LocalFile{Path: "/lib/y.mkv", Dir: "/lib", Name: "y.mkv", Kind: KindMain}
	if err := s.Store.SaveScanned([]*LocalFile{&scanned, newer}, snapshot); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Store.Get("/lib/x.mkv"); f.MediaID != 5 || f.Episode != 3 || !f.Locked {
		t.Fatalf("manual match overwritten: %+v", f)
	}
	if f, _ := s.Store.Get("/lib/y.mkv"); f.MediaID != 9 || !f.Locked {
		t.Fatalf("download overwritten: %+v", f)
	}

	// Without concurrent changes the scan result is stored.
	snapshot = map[string]*LocalFile{"/lib/x.mkv": &manual}
	rescanned := manual
	rescanned.Size = 42
	if err := s.Store.SaveScanned([]*LocalFile{&rescanned}, snapshot); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Store.Get("/lib/x.mkv"); f.Size != 42 {
		t.Fatalf("scan result not saved: %+v", f)
	}
}

// The episodes a multi-episode file holds are stored with it; what Kumo
// kept in that column (the number in the name) and its "nc" kind read as
// today's.
func TestStoreMultiEpisodeAndOldRows(t *testing.T) {
	s, _ := newTestScanner(t, t.TempDir())
	f := &LocalFile{Path: "/tv/Show.S01E01E02.mkv", Dir: "/tv", Name: "Show.S01E01E02.mkv", MediaID: catalog.ShowID(3), Episode: 1001, EpisodeEnd: 1002, Kind: KindMain}
	if err := s.Store.Save(f); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Store.Get(f.Path)
	if got.EpisodeEnd != 1002 || !got.Covers(1002) || got.Covers(1003) || len(got.Keys()) != 2 {
		t.Errorf("multi-episode file: %+v keys %v", got, got.Keys())
	}
	if _, err := s.Store.db.Write(`INSERT INTO local_files(path, dir, name, media_id, episode, aired_episode, kind) VALUES('/tv/old.mkv', '/tv', 'old.mkv', 7, 1005, 5, 'nc')`); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Store.Get("/tv/old.mkv")
	if old.EpisodeEnd != 0 || old.Kind != KindExtra {
		t.Errorf("old row: %+v", old)
	}
}

func TestFoldersExplainSkippedFolders(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "TV")
	s1 := filepath.Join(lib, "Fargo (2014)")
	touch(t, filepath.Join(s1, "Fargo.S01E01.mkv"))
	touch(t, filepath.Join(s1, "Fargo.S01E02.mkv"))
	touch(t, filepath.Join(s1, "Artwork", "poster.jpg")) // extras: not listed
	// Still downloading in qBittorrent.
	s2 := filepath.Join(lib, "Fargo Season 2")
	touch(t, filepath.Join(s2, "Fargo.S02E01.mkv.!qB"))
	touch(t, filepath.Join(s2, "Fargo.S02E02.mkv.!qB"))
	touch(t, filepath.Join(lib, "Some Show Sample Clips", "clip.mkv"))
	touch(t, filepath.Join(lib, "Packed", "show.rar"))
	touch(t, filepath.Join(lib, "Packed", "show.r00"))
	touch(t, filepath.Join(lib, "Partial", "Show.S01E01.mkv.part"))
	touch(t, filepath.Join(lib, "Artbook", "Scans", "01.jpg"))
	touch(t, filepath.Join(lib, "Notes", "readme.txt"))
	touch(t, filepath.Join(lib, "Moviko Download", ".kumobox-download-abc", "Show - S01E01.mp4"))
	if err := os.MkdirAll(filepath.Join(lib, "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestScanner(t, lib)
	scan(t, s)

	byLabel := func() map[string]FolderInfo {
		t.Helper()
		fs, err := s.Folders()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]FolderInfo{}
		for _, f := range fs {
			out[f.Label] = f
		}
		return out
	}
	got := byLabel()
	if f := got["Fargo (2014)"]; f.Videos != 2 || f.NotIndexed != 0 || f.Problem != "" {
		t.Errorf("season 1: %+v", f)
	}
	problems := map[string]string{
		"Fargo Season 2":         "Still downloading (2 unfinished files)",
		"Some Show Sample Clips": "“*sample*”",
		"Packed":                 "archives (.rar)",
		"Partial":                "Still downloading (1 unfinished file)",
		"Artbook":                "No video files in this folder or its subfolders",
		"Notes":                  "No video files here (1 .txt)",
		"Moviko Download":       "Still downloading (1 unfinished file)",
		"Empty":                  "The folder is empty",
	}
	for label, want := range problems {
		if f, ok := got[label]; !ok || !strings.Contains(f.Problem, want) {
			t.Errorf("%s: want problem %q, got %+v (listed: %v)", label, want, f, ok)
		}
	}
	for _, label := range []string{"Fargo (2014)/Artwork", "Artbook/Scans", "Moviko Download/.kumobox-download-abc"} {
		if f, ok := got[label]; ok {
			t.Errorf("%s shouldn't be listed: %+v", label, f)
		}
	}

	// The download finished, but no scan ran yet: index it on the spot.
	for _, n := range []string{"Fargo.S02E01.mkv", "Fargo.S02E02.mkv"} {
		if err := os.Rename(filepath.Join(s2, n+".!qB"), filepath.Join(s2, n)); err != nil {
			t.Fatal(err)
		}
	}
	if f := byLabel()["Fargo Season 2"]; f.Videos != 2 || f.NotIndexed != 2 || f.Problem != "" {
		t.Errorf("finished, not indexed: %+v", f)
	}
	files, err := s.IndexFolders([]string{s2})
	if err != nil || len(files) != 2 || files[0].MediaID != 0 || files[0].Parsed.Season != 2 || files[0].Parsed.Episode != 1 || files[1].Parsed.Episode != 2 {
		t.Fatalf("index: %d files %v", len(files), err)
	}
	if f := byLabel()["Fargo Season 2"]; f.NotIndexed != 0 {
		t.Errorf("after indexing: %+v", f)
	}
	// Indexing again changes nothing.
	if again, err := s.IndexFolders([]string{s2}); err != nil || len(again) != 2 {
		t.Fatalf("index again: %d files %v", len(again), err)
	}
	for _, dir := range []string{base, filepath.Join(lib, "..", "x"), "TV"} {
		if _, err := s.IndexFolders([]string{dir}); err == nil {
			t.Errorf("indexing %s must be refused", dir)
		}
	}

	// Files ignored by hand.
	all, _ := s.Store.All()
	for _, f := range all {
		if f.Dir == s1 {
			f.Ignored = true
			if err := s.Store.Save(f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if f := byLabel()["Fargo (2014)"]; !strings.Contains(f.Problem, "ignore its 2 videos") {
		t.Errorf("ignored by hand: %+v", f)
	}
}

// Files indexed by an older version are read again (names are read
// differently now), without touching what was matched by hand.
func TestScanReparsesOldFiles(t *testing.T) {
	lib := t.TempDir()
	s, _ := newTestScanner(t, lib)
	path := filepath.Join(lib, "Fargo (2014)", "Season 02", "Fargo.S02E05.mkv")
	touch(t, path)
	scan(t, s)
	f, _ := s.Store.Get(path)
	f.Parsed = Parsed{Title: "Fargo", Episode: 5} // as Kumo read it
	f.MediaID, f.Episode, f.Locked = catalog.ShowID(60622), 2005, true
	if err := s.Store.Save(f); err != nil {
		t.Fatal(err)
	}
	scan(t, s)
	got, _ := s.Store.Get(path)
	if got.Parsed.V != parseVersion || got.Parsed.Season != 2 || got.Parsed.Year != 2014 || got.MediaID != catalog.ShowID(60622) || got.Episode != 2005 || !got.Locked {
		t.Errorf("reparsed: %+v", got)
	}
}

// New files are told about, not on a library's first scan, and not those
// moved (a folder renamed).
func TestScanTellsNewFiles(t *testing.T) {
	dir := t.TempDir()
	s, _ := newTestScanner(t, dir)
	got := make(chan []string, 4)
	s.OnNewFiles = func(fs []*LocalFile) {
		var names []string
		for _, f := range fs {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		got <- names
	}
	next := func() []string {
		select {
		case n := <-got:
			return n
		case <-time.After(300 * time.Millisecond):
			return nil
		}
	}
	touch(t, filepath.Join(dir, "Show", "Show - 01.mkv"))
	scan(t, s)
	if n := next(); n != nil {
		t.Fatalf("first scan told: %v", n)
	}
	touch(t, filepath.Join(dir, "Show", "Show - 02.mkv"))
	scan(t, s)
	if n := next(); len(n) != 1 || n[0] != "Show - 02.mkv" {
		t.Fatalf("new file: %v", n)
	}
	// The folder renamed: its files aren't new.
	if err := os.Rename(filepath.Join(dir, "Show"), filepath.Join(dir, "Show (2024)")); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "Show (2024)", "Show - 03.mkv"))
	scan(t, s)
	if n := next(); len(n) != 1 || n[0] != "Show - 03.mkv" {
		t.Fatalf("after a rename: %v", n)
	}
}
