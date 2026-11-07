package stream

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// Image subtitles (PGS, VobSub) drawn onto the video: the subtitle track
// goes through scale2ref and overlay before the encoder's own filters,
// whatever the encoder, and the video is converted even where the player
// could play it as it is.
func TestBurnedSubtitlesArgs(t *testing.T) {
	p := &Probe{Path: "/a/Show - 01.mkv", Container: "matroska,webm",
		Video:     []ProbeStream{{Codec: "hevc", Profile: "Main 10", PixFmt: "yuv420p10le", Width: 1920, Height: 1080}},
		Audio:     []ProbeStream{{Codec: "aac", Channels: 2}},
		Subtitles: []ProbeStream{{Codec: "subrip", TypeIndex: 0}, {Codec: "hdmv_pgs_subtitle", TypeIndex: 1, Bitmap: true}}}
	st := testSettings(t)
	cfg := st.Get()
	l := &Local{settings: st}
	join := func(a []string) string { return strings.Join(a, " ") }
	if burnable(p, 0) || !burnable(p, 1) || burnable(p, 2) || burnable(p, -1) {
		t.Fatal("burnable: only the image track")
	}
	caps := CapsOf([]string{"h264", "hevc", "hevc-10"})
	for _, tc := range []struct {
		hw       string
		software bool
		want     string
	}{
		{"none", false, "overlay=eof_action=pass[out] -map [out] -map 0:a:0? -c:v libx264"},
		{"vaapi", false, "overlay=eof_action=pass,format=nv12,hwupload[out] -map [out] -map 0:a:0? -c:v h264_vaapi"},
		{"v4l2m2m", false, "overlay=eof_action=pass,scale='min(1920,iw)':-2,format=yuv420p[out] -map [out] -map 0:a:0? -c:v h264_v4l2m2m"},
		{"v4l2m2m", true, "overlay=eof_action=pass,scale='min(1280,iw)':-2,format=yuv420p[out] -map [out] -map 0:a:0? -c:v libx264 -preset ultrafast"},
		{"nvenc", false, "overlay=eof_action=pass[out] -map [out] -map 0:a:0? -c:v h264_nvenc"},
	} {
		cfg.Transcode.HwAccel = tc.hw
		pl := l.plan(p, "", 0, caps, false)
		if !pl.videoCopy {
			t.Fatalf("%s: the player plays HEVC, the video should be copied without subtitles", tc.hw)
		}
		if tc.software {
			pl = pl.WithSoftware()
		}
		pl = pl.WithBurn(1)
		a := join(pl.args(cfg, p, "/a/x.mkv", 7))
		if pl.videoCopy || pl.method != "transcode" || pl.burn != 1 || !strings.Contains(a, "-filter_complex [0:s:1]scale=1920:1080[s];[0:v:0][s]") || !strings.Contains(a, tc.want) || strings.Contains(a, "-map 0:v:0?") || strings.Contains(a, "-vf ") || strings.Contains(a, "-c:v copy") {
			t.Errorf("%s (software %v): %s", tc.hw, tc.software, a)
		}
		if !strings.Contains(a, "-ss 7.000 -i") || !strings.Contains(a, "-c:a aac") {
			t.Errorf("%s: converted video starts at t exactly: %s", tc.hw, a)
		}
	}
	cfg.Transcode.HwAccel = "none"
	if d := l.plan(p, "", 0, caps, false).WithBurn(1).describe(cfg, p); !strings.Contains(d, "subtitles drawn on it (track 2)") || strings.Contains(d, "copied (") {
		t.Errorf("described: %s", d)
	}
	// Without: as before.
	a := join(l.plan(p, "", 0, caps, false).args(cfg, p, "/a/x.mkv", 0))
	if !strings.Contains(a, "-map 0:v:0? -map 0:a:0? -c:v copy") || strings.Contains(a, "filter_complex") {
		t.Errorf("no subtitles: %s", a)
	}
}

// pgsSup is a PGS subtitle file (.sup) with one subtitle: a white box of
// w by h at x, y of a W by H picture, from 1s to 8s. Written by hand (ffmpeg
// can't make image subtitles out of text ones).
func pgsSup(W, H, x, y, w, h int) []byte {
	var out bytes.Buffer
	seg := func(secs float64, typ byte, data []byte) {
		out.WriteString("PG")
		_ = binary.Write(&out, binary.BigEndian, uint32(secs*90000))
		_ = binary.Write(&out, binary.BigEndian, uint32(0))
		out.WriteByte(typ)
		_ = binary.Write(&out, binary.BigEndian, uint16(len(data)))
		out.Write(data)
	}
	be16 := func(b *bytes.Buffer, v int) { _ = binary.Write(b, binary.BigEndian, uint16(v)) }
	// RLE: a line of one colour, then the end of the line.
	var rle bytes.Buffer
	for row := 0; row < h; row++ {
		colour := byte(1)
		if row < 10 || row >= h-10 {
			colour = 0
		}
		for n := w; n > 0; {
			run := min(n, 63)
			n -= run
			if colour == 0 {
				rle.Write([]byte{0, byte(run)})
			} else {
				rle.Write([]byte{0, 0x80 | byte(run), colour})
			}
		}
		rle.Write([]byte{0, 0})
	}
	pcs := func(objects int, comp int, state byte) []byte {
		var b bytes.Buffer
		be16(&b, W)
		be16(&b, H)
		b.WriteByte(0x10)
		be16(&b, comp)
		b.Write([]byte{state, 0, 0, byte(objects)})
		if objects > 0 {
			be16(&b, 0) // object
			b.Write([]byte{0, 0})
			be16(&b, x)
			be16(&b, y)
		}
		return b.Bytes()
	}
	var wds bytes.Buffer
	wds.Write([]byte{1, 0})
	for _, v := range []int{x, y, w, h} {
		be16(&wds, v)
	}
	pds := []byte{0, 0, 0, 16, 128, 128, 0, 1, 235, 128, 128, 255} // 0 clear, 1 white
	var ods bytes.Buffer
	be16(&ods, 0)
	ods.Write([]byte{0, 0xC0})
	n := rle.Len() + 4
	ods.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)})
	be16(&ods, w)
	be16(&ods, h)
	ods.Write(rle.Bytes())
	seg(1, 0x16, pcs(1, 0, 0x80))
	seg(1, 0x17, wds.Bytes())
	seg(1, 0x14, pds)
	seg(1, 0x15, ods.Bytes())
	seg(1, 0x80, nil)
	seg(8, 0x16, pcs(0, 1, 0))
	seg(8, 0x17, wds.Bytes())
	seg(8, 0x80, nil)
	return out.Bytes()
}

// A file with a PGS track, converted with it drawn on: the stream plays,
// starts where asked, and the frame at 3s has the white box where the
// subtitle is (and not at 10s, after it).
func TestBurnedSubtitlesStream(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	l, plain := newFfmpegLocal(t, "libx264")
	dir := filepath.Dir(plain)
	sup := filepath.Join(dir, "subs.sup")
	if err := os.WriteFile(sup, pgsSup(320, 240, 60, 150, 200, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "pgs.mkv")
	if out, err := exec.Command(ffmpeg, "-loglevel", "error", "-i", plain, "-i", sup, "-map", "0:v", "-map", "0:a", "-map", "1:s", "-c", "copy", video).CombinedOutput(); err != nil {
		t.Skipf("can't mux a PGS track: %v %s", err, out)
	}
	if err := l.files.Save(&library.LocalFile{Path: video, Dir: dir, Name: filepath.Base(video), Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	p, err := l.Probe(context.Background(), video)
	if err != nil || len(p.Subtitles) != 1 || !p.Subtitles[0].Bitmap {
		t.Fatalf("probe: %v %+v", err, p)
	}
	// Seek: converted, from t exactly, whatever the keyframes.
	if start, method, err := l.Seek(context.Background(), video, 2, 0, "remux", nil, false, 0); err != nil || start != 2 || method != "transcode" {
		t.Fatalf("Seek with burn: %v %s %v", start, method, err)
	}
	// A text track can't be drawn on.
	rec := httptest.NewRecorder()
	l.ServeTranscode(rec, httptest.NewRequest(http.MethodGet, "/", nil), plain, 0, 0, "remux", nil, 0)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("burning a file without image subtitles: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	l.ServeTranscode(rec, httptest.NewRequest(http.MethodGet, "/", nil), video, 2, 0, "remux", nil, 0)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Kumo-Method") != "transcode" {
		t.Fatalf("stream: %d %s", rec.Code, rec.Body.String())
	}
	mp4 := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(mp4, rec.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := probeDuration(t, rec.Body.Bytes()); d < 27.5 || d > 28.5 {
		t.Errorf("the stream from 2s lasts %.2fs, want 28", d)
	}
	// The box: white in the middle of it at 3s (1s into the stream), the
	// test pattern's own colour at 10s (8s in).
	pixel := func(at string) []byte {
		out, err := exec.Command(ffmpeg, "-loglevel", "error", "-ss", at, "-i", mp4, "-frames:v", "1", "-vf", "crop=4:4:158:171", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1").Output()
		if err != nil {
			t.Fatalf("frame at %s: %v", at, err)
		}
		return out
	}
	if px := pixel("1"); len(px) == 0 || px[0] < 200 {
		t.Errorf("at 3s the box should be white, got %v", px)
	}
	if px := pixel("8"); len(px) == 0 || px[0] >= 200 {
		t.Errorf("at 10s the subtitle is gone, got %v", px)
	}
}

// Converted video goes with converted audio, whatever the audio: copied
// audio keeps its timing from the keyframe before a seek, and the stream
// would start with sound from before where it was asked to.
func TestConvertedVideoConvertsAudio(t *testing.T) {
	p := &Probe{Path: "/a/x.mkv", Video: []ProbeStream{{Codec: "hevc"}}, Audio: []ProbeStream{{Codec: "aac", Channels: 2}}}
	l := &Local{settings: testSettings(t)}
	if pl := l.plan(p, "", 0, CapsOf([]string{"h264"}), false); pl.videoCopy || pl.audioCopy || pl.method != "transcode" {
		t.Fatalf("HEVC the player can't play, AAC: %+v", pl)
	}
	copied := l.plan(p, "", 0, CapsOf([]string{"h264", "hevc"}), false)
	if !copied.videoCopy || !copied.audioCopy || copied.method != "remux" {
		t.Fatalf("HEVC the player plays, AAC: %+v", copied)
	}
	if sw := copied.WithSoftware(); sw.audioCopy {
		t.Fatalf("software: %+v", sw)
	}
}
