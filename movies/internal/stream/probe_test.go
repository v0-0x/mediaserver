package stream

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/library"
)

func TestParseProbeChapters(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ffprobe_chapters.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseProbe(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != "matroska,webm" || p.Duration != 2823.021 {
		t.Errorf("container %q, duration %v", p.Container, p.Duration)
	}
	// The cover art isn't a video stream.
	if len(p.Video) != 1 || len(p.Audio) != 2 || len(p.Subtitles) != 3 {
		t.Fatalf("%d video, %d audio, %d subtitle streams; want 1, 2, 3", len(p.Video), len(p.Audio), len(p.Subtitles))
	}
	if a := p.Audio[1]; a.Language != "fre" || a.Title != "Français" || a.TypeIndex != 1 || a.Default {
		t.Errorf("second audio track: %+v", a)
	}
	if s := p.Subtitles[0]; !s.Forced || s.Language != "eng" {
		t.Errorf("forced subtitles: %+v", s)
	}
	if !p.Subtitles[2].Bitmap {
		t.Errorf("PGS subtitles not marked as images: %+v", p.Subtitles[2])
	}
	want := []ProbeChapter{
		{"Previously On", 0, 52.386},
		{"Opening Credits", 52.386, 112.446},
		{"Episode", 112.446, 2731.02},
		{"End Credits", 2731.02, 2790},
		{"Next Episode", 2790, 2823.021}, // its tag is TITLE
		{"", 2823.021, 2823.021},
	}
	if !reflect.DeepEqual(p.Chapters, want) {
		t.Errorf("chapters:\n got %+v\nwant %+v", p.Chapters, want)
	}

	// No chapters: none.
	p, err = parseProbe([]byte(`{"format":{"format_name":"mov,mp4","duration":"10.5"},"streams":[]}`))
	if err != nil || p.Chapters != nil || p.Duration != 10.5 {
		t.Errorf("without chapters: %+v, %v", p, err)
	}
}

// Probe asks ffprobe for the chapters, and asks again only when the file
// changes.
func TestProbeReadsChaptersOncePerVersion(t *testing.T) {
	l, _ := newTestLocal(t, "")
	fixture, err := filepath.Abs(filepath.Join("testdata", "ffprobe_chapters.json"))
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	cfg := l.settings.Get()
	cfg.Transcode.FfprobePath = writeScript(t, `echo run >> '`+runs+`'
for a in "$@"; do [ "$a" = "-show_chapters" ] && chapters=1; done
[ -n "$chapters" ] || { echo "not asked for the chapters" >&2; exit 1; }
cat '`+fixture+`'
`)
	if _, err := l.settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - S01E02.mkv")
	if err := os.WriteFile(video, []byte("episode two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.files.Save(&library.LocalFile{Path: video, Dir: dir, Name: filepath.Base(video), Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		b, _ := os.ReadFile(runs)
		return strings.Count(string(b), "run")
	}

	for range 2 {
		p, err := l.Probe(context.Background(), video)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Chapters) != 6 || p.Chapters[1].Title != "Opening Credits" || p.Path != video {
			t.Fatalf("probe: %+v", p)
		}
	}
	if n := count(); n != 1 {
		t.Fatalf("ffprobe ran %d times; want once, then the saved probe", n)
	}

	// Same size, new contents (a better release, same length): probed again.
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(video, []byte("episode 2!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(video, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Probe(context.Background(), video); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("ffprobe ran %d times; want the changed file probed again", n)
	}
}
