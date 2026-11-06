package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGuess(t *testing.T) {
	for name, want := range map[string]Track{
		"/m/01 - Artist - Title.mp3": {Title: "Title", Artist: "Artist", TrackNo: 1},
		"/m/07. Song.flac":           {Title: "Song", TrackNo: 7},
		"/m/Just a song.ogg":         {Title: "Just a song"},
		"/m/1999.mp3":                {Title: "1999"},
	} {
		got := Track{Path: name}
		guess(&got)
		got.Path = ""
		if got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

func TestScan(t *testing.T) {
	dir, music := t.TempDir(), t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(music, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("not much of a song "+rel), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("A/one.mp3")
	write("A/two.flac")
	write("A/two.flac.part")
	write(".trash/three.mp3")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	l.Changed = func() { changed++ }
	if err := l.AddFolder(music); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l.Scan(ctx)
	if n := len(l.Tracks()); n != 2 {
		t.Fatalf("%d songs", n)
	}
	v := l.Version()
	// Nothing changed: the same version.
	l.Scan(ctx)
	if l.Version() != v {
		t.Fatal("the version changed with nothing new")
	}
	// Kept across a start.
	l2, _ := Open(dir)
	if len(l2.Tracks()) != 2 || l2.Version() != v || len(l2.Dirs()) != 1 {
		t.Fatal("not kept")
	}
	// A song gone.
	os.Remove(a)
	l.Scan(ctx)
	if len(l.Tracks()) != 1 || l.Version() == v {
		t.Fatal("a song gone is still there")
	}
	// The folder away (a drive unplugged): its songs stay.
	moved := music + "-away"
	if err := os.Rename(music, moved); err != nil {
		t.Fatal(err)
	}
	l.Scan(ctx)
	if f := l.Folders(); len(l.Tracks()) != 1 || !f[0].Missing {
		t.Fatalf("away: %d songs, %+v", len(l.Tracks()), f)
	}
	os.Rename(moved, music)
	l.Scan(ctx)
	if f := l.Folders(); f[0].Missing || f[0].Songs != 1 {
		t.Fatalf("back: %+v", f)
	}
	if changed == 0 {
		t.Fatal("Changed never ran")
	}
}

// Lengths, and tags Go can't read, from ffprobe (when it's there).
func TestLengths(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir, music := t.TempDir(), t.TempDir()
	song := filepath.Join(music, "tone.wv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-metadata", "title=A Tone", "-metadata", "artist=The Sines", song).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg can't make a WavPack file: %v %s", err, out)
	}
	l, _ := Open(dir)
	if !l.CanProbe() {
		t.Skip("no ffprobe")
	}
	_ = l.AddFolder(music)
	l.Scan(context.Background())
	deadline := time.Now().Add(20 * time.Second)
	for {
		tr := l.Tracks()
		if len(tr) == 1 && tr[0].DurationMs > 0 {
			if tr[0].DurationMs < 2900 || tr[0].DurationMs > 3100 || tr[0].Title != "A Tone" || tr[0].Artist != "The Sines" {
				t.Fatalf("%+v", tr[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no length: %+v", tr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A cover image that comes after its songs (uploaded last, say) is theirs
// at the next scan.
func TestCoverAfterTheSongs(t *testing.T) {
	dir, music := t.TempDir(), t.TempDir()
	album := filepath.Join(music, "Album")
	_ = os.MkdirAll(album, 0o755)
	if err := os.WriteFile(filepath.Join(album, "01 Song.mp3"), []byte("not much of a song"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, _ := Open(dir)
	_ = l.AddFolder(music)
	l.Scan(context.Background())
	if l.Tracks()[0].HasArt {
		t.Fatal("a cover before there is one")
	}
	v := l.Version()
	_ = os.WriteFile(filepath.Join(album, "Cover.JPG"), []byte("\xff\xd8\xff"), 0o644)
	l.Scan(context.Background())
	if !l.Tracks()[0].HasArt || l.Version() == v {
		t.Fatalf("the cover isn't the song's: %+v (version %s)", l.Tracks()[0], l.Version())
	}
	if data, mime, err := l.Art(l.Tracks()[0].Path); err != nil || mime != "image/jpeg" || len(data) != 3 {
		t.Fatalf("art: %v %s %d", err, mime, len(data))
	}
}

// A cover read out of a song is kept: served again without the song (its
// drive asleep, say), and read again only when the song changes.
func TestArtKept(t *testing.T) {
	dir, music := t.TempDir(), t.TempDir()
	album := filepath.Join(music, "Album")
	_ = os.MkdirAll(album, 0o755)
	song := filepath.Join(album, "01 Song.mp3")
	if err := os.WriteFile(song, []byte("not much of a song"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(album, "cover.png"), []byte("\x89PNG\r\n"), 0o644)
	l, _ := Open(dir)
	_ = l.AddFolder(music)
	l.Scan(context.Background())
	data, mime, err := l.Art(song)
	if err != nil || mime != "image/png" || len(data) != 6 {
		t.Fatalf("art: %v %s %d", err, mime, len(data))
	}
	// The cover gone: still served, from what was kept.
	_ = os.Remove(filepath.Join(album, "cover.png"))
	if data, mime, err := l.Art(song); err != nil || mime != "image/png" || len(data) != 6 {
		t.Fatalf("art once kept: %v %s %d", err, mime, len(data))
	}
	// A song without a cover is known to have none, without reading it.
	other := filepath.Join(album, "02 Other.mp3")
	_ = os.WriteFile(other, []byte("another"), 0o644)
	l.Scan(context.Background())
	if _, _, err := l.Art(other); err == nil {
		t.Fatal("a cover for a song without one")
	}
	_ = os.Remove(other)
	if _, _, err := l.Art(other); err == nil {
		t.Fatal("a cover for a song without one, the second time")
	}
}
