package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

// Local serves library files to the in-app player: direct play when the
// browser can decode them, otherwise an on-the-fly remux/transcode to
// fragmented MP4 with ffmpeg (so every video format plays).
type Local struct {
	settings *config.Store
	files    *library.Store
	db       *db.DB
	// Torrents are the torrents being played (TorrentPrefix paths).
	Torrents Torrents
	// MaxTranscodes: how many videos may be converted at once (0: any). A
	// Aniko Hub's small computer converts one or two at most.
	MaxTranscodes int
	transcoding   atomic.Int32
}

func NewLocal(s *config.Store, f *library.Store, d *db.DB) *Local {
	return &Local{settings: s, files: f, db: d}
}

type ProbeStream struct {
	Index     int    `json:"index"`     // absolute stream index
	TypeIndex int    `json:"typeIndex"` // index among streams of the same type
	Type      string `json:"type"`      // video | audio | subtitle | attachment
	Codec     string `json:"codec"`
	Language  string `json:"language"`
	Title     string `json:"title"`
	Default   bool   `json:"default"`
	Forced    bool   `json:"forced"`
	Channels  int    `json:"channels,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Profile   string `json:"profile,omitempty"`
	PixFmt    string `json:"pixFmt,omitempty"`
	Bitmap    bool   `json:"bitmap,omitempty"`
}

type ExternalSub struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language"`
}

type Probe struct {
	Path         string        `json:"path"`
	Container    string        `json:"container"`
	Duration     float64       `json:"duration"`
	Size         int64         `json:"size"`
	Video        []ProbeStream `json:"video"`
	Audio        []ProbeStream `json:"audio"`
	Subtitles    []ProbeStream `json:"subtitles"`
	ExternalSubs []ExternalSub `json:"externalSubs"`
	// Playback decision: direct | remux | transcode
	Method string `json:"method"`
	Reason string `json:"reason"`
}

var ErrNotInLibrary = errors.New("file is not part of the library")

// TorrentPrefix starts the path of a torrent playing as it downloads
// (package torrentstream): torrent:<its session's ID>. Played like a
// library file, it's read from where the torrent serves it.
const TorrentPrefix = "torrent:"

// ErrTorrentGone: a torrent no longer being played (dropped, Aniko
// restarted).
var ErrTorrentGone = errors.New("this torrent isn't being played any more: play it again")

// Torrents are the torrents being played (set by the app).
type Torrents interface {
	URL(id string) (string, error)
	Size(id string) (int64, bool)
	Serve(w http.ResponseWriter, r *http.Request, id string)
}

// IsTorrent reports a torrent's path.
func IsTorrent(path string) bool { return strings.HasPrefix(path, TorrentPrefix) }

func torrentID(path string) (string, bool) { return strings.CutPrefix(path, TorrentPrefix) }

// ErrFileGone is (wrapped in) the error for a library file that isn't on
// the disk: moved or deleted, or on a drive that isn't connected (its files
// are kept until it is, see library.Scanner).
var ErrFileGone = errors.New("the video file isn't there")

// resolve makes sure only indexed library files can be read.
func (l *Local) resolve(path string) (string, error) {
	if id, ok := torrentID(path); ok {
		if l.Torrents == nil {
			return "", ErrNotInLibrary
		}
		if _, ok := l.Torrents.Size(id); !ok {
			return "", ErrTorrentGone
		}
		return l.Torrents.URL(id)
	}
	path = filepath.Clean(path)
	f, err := l.files.Get(path)
	if err != nil || f == nil {
		return "", ErrNotInLibrary
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", goneError(path)
		}
		return "", err
	}
	return path, nil
}

// goneError says why an indexed file isn't on the disk.
func goneError(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%w: it was moved or deleted (scan the library again)", ErrFileGone)
	}
	// Its folder is gone too: the topmost one missing is the drive's.
	for parent := filepath.Dir(dir); parent != dir; parent = filepath.Dir(dir) {
		if _, err := os.Stat(parent); err == nil {
			break
		}
		dir = parent
	}
	return fmt.Errorf("%w: %s is missing (is its drive connected?)", ErrFileGone, dir)
}

func (l *Local) Probe(ctx context.Context, path string) (*Probe, error) {
	asked := path
	path, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	cfg := l.settings.Get()
	key := probeKey(path)
	var size int64
	torrent := false
	if id, ok := torrentID(asked); ok {
		// A torrent playing as it downloads: read from where it's served,
		// its first pieces fetched for it.
		size, _ = l.Torrents.Size(id)
		torrent = true
	} else {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		size = st.Size()
	}
	var p Probe
	if l.db.GetCache(key, &p) && p.Size == size {
		p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0, nil)
		return &p, nil
	}
	wait := 30 * time.Second
	if torrent {
		wait = 90 * time.Second // its pieces come from its peers
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return nil, errors.New("ffprobe took too long to read the file")
		case errors.As(err, &exit):
			// ffprobe is there but the file is unreadable (broken or unfinished).
			msg := strings.TrimSpace(string(exit.Stderr))
			if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
				msg = msg[i+1:]
			}
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("ffprobe couldn't read the file: %s", msg)
		}
		return nil, fmt.Errorf("ffprobe failed (install ffmpeg: %s): %w", util.InstallHint("ffmpeg"), err)
	}
	var raw struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index       int               `json:"index"`
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Channels    int               `json:"channels"`
			Width       int               `json:"width"`
			Height      int               `json:"height"`
			Profile     string            `json:"profile"`
			PixFmt      string            `json:"pix_fmt"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	p = Probe{Path: path, Container: raw.Format.FormatName, Size: size}
	if torrent {
		p.Path = asked
	}
	p.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	counts := map[string]int{}
	for _, s := range raw.Streams {
		ps := ProbeStream{
			Index: s.Index, Type: s.CodecType, Codec: s.CodecName, Channels: s.Channels, Width: s.Width, Height: s.Height, Profile: s.Profile, PixFmt: s.PixFmt,
			Language: s.Tags["language"], Title: s.Tags["title"], Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
		}
		if ps.Type == "video" && s.Disposition["attached_pic"] == 1 {
			continue
		}
		ps.TypeIndex = counts[ps.Type]
		counts[ps.Type]++
		switch ps.Type {
		case "video":
			p.Video = append(p.Video, ps)
		case "audio":
			p.Audio = append(p.Audio, ps)
		case "subtitle":
			ps.Bitmap = ps.Codec == "hdmv_pgs_subtitle" || ps.Codec == "dvd_subtitle" || ps.Codec == "dvb_subtitle"
			p.Subtitles = append(p.Subtitles, ps)
		}
	}
	if !torrent {
		p.ExternalSubs = externalSubs(path)
	}
	l.db.SetCache(key, p, 30*24*time.Hour)
	p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0, nil)
	return &p, nil
}

func externalSubs(path string) []ExternalSub {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []ExternalSub
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !library.SubtitleExtensions[strings.ToLower(filepath.Ext(name))] || !strings.HasPrefix(name, base) {
			continue
		}
		lang := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(name, base), filepath.Ext(name)), ".-_ ")
		out = append(out, ExternalSub{Name: name, Path: filepath.Join(dir, name), Language: lang})
	}
	return out
}

// probeKey caches probes; v2 added the profile and pixel format.
func probeKey(path string) string { return "probe2:" + path }

// Caps are the video formats a player can decode, like "h264", "hevc" or
// "hevc-10" (10-bit), and "mkv" when it plays Matroska files as they are.
// Videos in them are played or copied as they are; others are converted to
// H.264.
type Caps map[string]bool

// DefaultCaps is what Chromium plays, the desktop app's browser.
var DefaultCaps = Caps{"h264": true, "vp8": true, "vp9": true, "vp9-10": true, "av1": true, "av1-10": true, "mkv": true}

// ParseCaps reads a comma-separated list of formats; none means DefaultCaps.
func ParseCaps(list string) Caps {
	return CapsOf(strings.Split(list, ","))
}

func CapsOf(list []string) Caps {
	c := Caps{}
	for _, f := range list {
		if f = strings.TrimSpace(strings.ToLower(f)); f != "" {
			c[f] = true
		}
	}
	if len(c) == 0 {
		return DefaultCaps
	}
	c["h264"] = true // every player decodes 8-bit H.264
	return c
}

// videoFormat names a video stream's format the way Caps do: its codec,
// with "-10" for 10-bit (or deeper) video, which many devices can't decode
// (10-bit H.264 plays nowhere in a browser).
func videoFormat(v ProbeStream) string {
	if strings.Contains(v.PixFmt, "p10") || strings.Contains(v.PixFmt, "p12") || strings.Contains(v.Profile, "10") {
		return v.Codec + "-10"
	}
	return v.Codec
}

func formatName(f string) string {
	codec, deep := strings.CutSuffix(f, "-10")
	name := map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "vp8": "VP8"}[codec]
	if name == "" {
		name = strings.ToUpper(codec)
	}
	if deep {
		return "10-bit " + name
	}
	return name
}

var browserAudio = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}

// decide picks direct play, remux (copy into fMP4) or transcode for a
// player that decodes caps (nil: DefaultCaps).
func decide(p *Probe, mode string, audioIdx int, caps Caps) (string, string) {
	if caps == nil {
		caps = DefaultCaps
	}
	if mode == "transcode" {
		return "transcode", "forced by settings"
	}
	if len(p.Video) == 0 {
		return "transcode", "no video stream"
	}
	v := p.Video[0]
	if f := videoFormat(v); !caps[f] {
		if mode == "direct" {
			return "direct", "forced by settings"
		}
		return "transcode", fmt.Sprintf("this device can't play %s video", formatName(f))
	}
	audioOK := true
	if len(p.Audio) > 0 {
		a := p.Audio[min(max(audioIdx, 0), len(p.Audio)-1)]
		audioOK = browserAudio[a.Codec] && a.Channels <= 2 || a.Codec == "aac" || a.Codec == "opus"
	}
	// ffprobe calls both Matroska and WebM "matroska,webm".
	mkv := strings.Contains(p.Container, "matroska") && !strings.EqualFold(filepath.Ext(p.Path), ".webm")
	containerOK := strings.Contains(p.Container, "mp4") || strings.Contains(p.Container, "mov") || strings.Contains(p.Container, "webm") && (!mkv || caps["mkv"])
	if mode == "direct" || (containerOK && audioOK && audioIdx == 0 && len(p.Audio) <= 1) {
		return "direct", "the browser can play this file as is"
	}
	if audioOK {
		return "remux", "repackaging into MP4 (no quality loss)"
	}
	return "transcode", "converting the audio track for the browser"
}

// Decide redoes the playback decision of a probe for a player.
func (l *Local) Decide(p *Probe, caps Caps) {
	p.Method, p.Reason = decide(p, l.settings.Get().Transcode.Mode, 0, caps)
}

// plan is how ffmpeg converts a file for one player.
type plan struct {
	method    string // remux | transcode
	videoCopy bool
	audioCopy bool
	hevc      bool // copied HEVC: tagged so Apple devices play it
	audio     int  // the audio track, among the file's audio tracks
	// The video converted by ffmpeg's own (software) encoder, not this
	// computer's: a player that showed no picture of the other (see
	// WithSoftware).
	software bool
	// burn: the file's image subtitles (PGS, VobSub: track burn among its
	// subtitle tracks) drawn onto the video, which is then converted (see
	// WithBurn); -1 for none.
	burn int
}

// WithBurn is the plan with the file's subtitle track n (an image one)
// drawn onto the video: a player can't show those itself, so the video is
// converted with them in it, whatever the player could play.
// The audio is converted too: copied audio keeps its own timing from
// before a seek and falls out of step with the drawn-on video.
func (pl plan) WithBurn(n int) plan {
	pl.burn, pl.videoCopy, pl.hevc, pl.audioCopy, pl.method = n, false, false, false, "transcode"
	return pl
}

// burnable says whether subtitle track n of p can be burned in: an image
// track (text ones the player shows itself).
func burnable(p *Probe, n int) bool {
	return n >= 0 && n < len(p.Subtitles) && p.Subtitles[n].Bitmap
}

// WithSoftware is the plan with the video converted in software (libx264),
// whatever the player could play or the computer's encoder is: the last
// thing a player that showed no picture tries (?encoder=software).
func (pl plan) WithSoftware() plan {
	pl.software, pl.videoCopy, pl.hevc, pl.audioCopy, pl.method = true, false, false, false, "transcode"
	return pl
}

// plan works out the conversion for method (direct or "" means decide),
// audio track and player. HLS players only get AAC audio copied, the one
// codec every HLS player takes.
func (l *Local) plan(p *Probe, method string, audio int, caps Caps, hls bool) plan {
	if caps == nil {
		caps = DefaultCaps
	}
	if method == "" || method == "direct" {
		method, _ = decide(p, l.settings.Get().Transcode.Mode, audio, caps)
		if method == "direct" {
			method = "remux"
		}
	}
	pl := plan{method: method, burn: -1}
	// Video the player decodes is copied, also when transcoding (that's for
	// the audio, e.g. TrueHD next to HEVC a Mac plays as is).
	if len(p.Video) > 0 {
		v := p.Video[0]
		pl.videoCopy = caps[videoFormat(v)]
		pl.hevc = pl.videoCopy && v.Codec == "hevc"
	}
	if len(p.Audio) > 0 {
		pl.audio = min(max(audio, 0), len(p.Audio)-1)
		codec := p.Audio[pl.audio].Codec
		pl.audioCopy = method == "remux" && (codec == "aac" || !hls && browserAudio[codec])
	}
	if !pl.videoCopy || len(p.Audio) > 0 && !pl.audioCopy {
		pl.method = "transcode"
	}
	// Converted video goes with converted audio: copied audio keeps its own
	// timing from the keyframe before a seek (-ss), and the stream would
	// start with seconds of sound from before where it was asked to, its
	// times off by that much.
	if !pl.videoCopy {
		pl.audioCopy = false
	}
	return pl
}

// args are ffmpeg's arguments for the plan, up to the output format.
func (pl plan) args(cfg config.Settings, p *Probe, path string, start float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	hw := cfg.Transcode.HwAccel
	if !pl.videoCopy && !pl.software && hw == "vaapi" {
		args = append(args, "-vaapi_device", cfg.Transcode.VaapiNode)
	}
	if start > 0 {
		if pl.videoCopy {
			start += seekMargin // lands on the keyframe at start, see SeekPoint
		}
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", path)
	// The video: as it is, or with image subtitles drawn on it first (the
	// subtitle track scaled to the picture's size, then laid over it; the
	// encoder's own filters come after, so VAAPI's upload is last). Not
	// scale2ref: it holds the video back to the subtitles' frames, and the
	// stream would start and end with them.
	var video []string
	if pl.burn >= 0 {
		sub := fmt.Sprintf("[0:s:%d]", pl.burn)
		if v := p.Video[0]; v.Width > 0 && v.Height > 0 {
			sub += fmt.Sprintf("scale=%d:%d[s];", v.Width, v.Height)
		} else {
			sub += "copy[s];"
		}
		video = []string{"-filter_complex", sub + "[0:v:0][s]overlay=eof_action=pass%s[out]", "-map", "[out]"}
	} else {
		video = []string{"-map", "0:v:0?"}
	}
	// vf is the encoder's filter chain, or "".
	vf := func(chain string) {
		if pl.burn >= 0 {
			if chain != "" {
				chain = "," + chain
			}
			video[1] = fmt.Sprintf(video[1], chain)
			args = append(args, video...)
		} else {
			args = append(args, video...)
			if chain != "" {
				args = append(args, "-vf", chain)
			}
		}
	}
	if len(p.Audio) > 0 {
		video = append(video, "-map", fmt.Sprintf("0:a:%d?", pl.audio))
	}
	if pl.videoCopy {
		vf("")
		args = append(args, "-c:v", "copy")
		if pl.hevc {
			args = append(args, "-tag:v", "hvc1")
		}
	} else if pl.software {
		if hw == "v4l2m2m" {
			// A Raspberry Pi's CPU: as fast as it goes, at 720p.
			vf("scale='min(1280,iw)':-2,format=yuv420p")
			args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23", "-profile:v", "high", "-g", "48")
		} else {
			vf("")
			args = append(args, "-c:v", "libx264", "-preset", cfg.Transcode.Preset, "-crf", "21", "-pix_fmt", "yuv420p", "-profile:v", "high")
		}
	} else {
		switch hw {
		case "vaapi":
			vf("format=nv12,hwupload")
			args = append(args, "-c:v", "h264_vaapi", "-qp", "23")
		case "nvenc":
			vf("")
			args = append(args, "-c:v", "h264_nvenc", "-preset", "p4", "-cq", "23")
		case "qsv":
			vf("")
			args = append(args, "-c:v", "h264_qsv", "-global_quality", "23")
		case "v4l2m2m":
			// A Raspberry Pi's encoder: 8-bit 4:2:0 up to 1080p, by bitrate.
			vf("scale='min(1920,iw)':-2,format=yuv420p")
			args = append(args, "-c:v", "h264_v4l2m2m",
				"-b:v", "6M", "-maxrate", "8M", "-bufsize", "12M", "-g", "48")
		default:
			vf("")
			args = append(args, "-c:v", "libx264", "-preset", cfg.Transcode.Preset, "-crf", "21", "-pix_fmt", "yuv420p", "-profile:v", "high")
		}
	}
	if pl.audioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	}
	return append(args, "-sn", "-dn")
}

// describe says what the plan does, for the log: "video copied (hevc-10),
// audio converted".
func (pl plan) describe(cfg config.Settings, p *Probe) string {
	video := "no video"
	if len(p.Video) > 0 {
		f := videoFormat(p.Video[0])
		switch {
		case pl.videoCopy:
			video = "video copied (" + f + ")"
		case pl.software:
			video = "video converted in software (" + f + " to h264, libx264)"
		default:
			video = "video converted (" + f + " to h264, " + encoderName(cfg) + ")"
		}
	}
	if pl.burn >= 0 {
		video += ", subtitles drawn on it (track " + strconv.Itoa(pl.burn+1) + ")"
	}
	audio := "audio converted"
	if pl.audioCopy {
		audio = "audio copied"
	}
	if len(p.Audio) == 0 {
		audio = "no audio"
	}
	return video + ", " + audio
}

// movflags make the MP4 a stream players start on before it's done
// (fragments, the header first). A Raspberry Pi's encoder (h264_v4l2m2m)
// gives the video's header (SPS/PPS) only with its first frame, not when
// it starts as other encoders do: written first, the MP4's header had none,
// and a TV played the sound under a grey picture. delay_moov writes it once
// the first frame is there.
func (pl plan) movflags(cfg config.Settings) string {
	flags := "frag_keyframe+empty_moov+default_base_moof"
	if !pl.videoCopy && !pl.software && cfg.Transcode.HwAccel == "v4l2m2m" {
		flags += "+delay_moov"
	}
	return flags
}

// SeekPoint returns where a stream asked to start at t really starts, which
// the player needs to keep subtitles and the position right after seeking.
// Converted video starts at t exactly. Copied video can only start at a
// keyframe: the last one at or before t.
//
// Starting there takes care, as ffmpeg's -ss lands on the last keyframe
// some way before the time given: about 0.13s before with B-frames, plus
// the audio's pre-roll (up to 0.08s for Opus). So copies start at
// keyframe + seekMargin, and a keyframe followed that closely by another
// one (a scene cut) is passed over for the next.
func (l *Local) SeekPoint(ctx context.Context, path string, t float64, audio int, method string, caps Caps, hls bool) (float64, error) {
	start, _, err := l.Seek(ctx, path, t, audio, method, caps, hls, -1)
	return start, err
}

// Seek is SeekPoint, and the method to ask the stream with: "transcode"
// when the video would be copied but its keyframe at t isn't known (the
// file couldn't be read in time), as a copy would then start at an unknown
// frame (a grey picture until the next keyframe, its times off), where
// converted video starts at t exactly. Otherwise the method asked for.
// With burn >= 0 (image subtitles drawn on), the video is converted and
// starts at t exactly.
func (l *Local) Seek(ctx context.Context, path string, t float64, audio int, method string, caps Caps, hls bool, burn int) (float64, string, error) {
	path, err := l.resolve(path)
	if err != nil {
		return 0, "", err
	}
	p, err := l.Probe(ctx, path)
	if err != nil {
		return 0, "", err
	}
	if burn >= 0 && burnable(p, burn) {
		return t, "transcode", nil
	}
	start, known := l.startFor(ctx, path, l.plan(p, method, audio, caps, hls), t)
	if !known {
		return t, "transcode", nil
	}
	return start, method, nil
}

const seekMargin = 0.4

// seekAhead: a keyframe this little after the time asked is taken over the
// last one before it, further back. Skips and resumes are mostly asked for
// at the end of a scene, and the next one starts with a keyframe: a skipped
// ending then isn't partly heard again.
const seekAhead = 1.5

// startFor is where a stream with this plan asked to start at t starts,
// and whether that's known: for copied video, whether its keyframe could
// be found (see Seek).
func (l *Local) startFor(ctx context.Context, path string, pl plan, t float64) (float64, bool) {
	if t <= 0 {
		return 0, true
	}
	if !pl.videoCopy {
		return t, true
	}
	if k, ok := l.seekKeyframe(ctx, path, t); ok {
		return k, true
	}
	return t, false
}

// seekKeyframe finds the keyframe a copy asked to start at t starts at: the
// last one at or before t, or the next one when it's close (seekAhead) and
// the last one isn't.
func (l *Local) seekKeyframe(ctx context.Context, path string, t float64) (float64, bool) {
	// A disk waking up, or a file read for the first time, takes a while:
	// without its keyframe, a copy would start mid-picture (grey until the
	// next keyframe).
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	ffprobe := l.settings.Get().Transcode.FfprobePath
	for _, window := range []float64{20, 120} {
		from := max(0, t-window)
		out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0",
			"-read_intervals", fmt.Sprintf("%.3f%%%.3f", from, t+seekAhead+1), path).Output()
		if err != nil {
			return 0, false
		}
		var keys []float64
		for _, line := range strings.Split(string(out), "\n") {
			ts, flags, ok := strings.Cut(strings.TrimSpace(line), ",")
			if !ok || !strings.HasPrefix(flags, "K") {
				continue
			}
			if v, err := strconv.ParseFloat(ts, 64); err == nil {
				keys = append(keys, v)
			}
		}
		sort.Float64s(keys)
		i := -1
		for j, k := range keys {
			if k <= t+0.0005 {
				i = j
			}
		}
		if i < 0 && from > 0 {
			continue // look further back
		}
		if i+1 < len(keys) && keys[i+1]-t <= seekAhead && (i < 0 || t-keys[i] > 0.5) {
			i++
		}
		if i < 0 {
			return 0, true // no keyframe read before t: from the start
		}
		for i+1 < len(keys) && keys[i+1]-keys[i] < seekMargin+0.05 {
			i++
		}
		return keys[i], true
	}
	return 0, false
}

// ServeFile streams the raw file with range support (direct play).
func (l *Local) ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	if id, ok := torrentID(path); ok && l.Torrents != nil {
		l.Torrents.Serve(w, r, id)
		return
	}
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	// Set the type explicitly so a mislabelled file is never sniffed as HTML.
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if strings.EqualFold(filepath.Ext(path), ".mkv") {
		ct = "video/x-matroska"
	}
	if !strings.HasPrefix(ct, "video/") && !strings.HasPrefix(ct, "audio/") {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// ServeTranscode pipes ffmpeg output as fragmented MP4 starting at `start`
// (see SeekPoint), for players that decode caps.
// ServeTranscode converts the file from start for the player; burn >= 0 draws
// that subtitle track (an image one) onto the video.
func (l *Local) ServeTranscode(w http.ResponseWriter, r *http.Request, path string, start float64, audio int, method string, caps Caps, burn int) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	cfg := l.settings.Get()
	p, err := l.Probe(r.Context(), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pl := l.plan(p, method, audio, caps, false)
	if r.URL.Query().Get("encoder") == "software" {
		pl = pl.WithSoftware()
	}
	if burn >= 0 {
		if !burnable(p, burn) {
			http.Error(w, "that subtitle track can't be drawn onto the video (not an image track)", http.StatusBadRequest)
			return
		}
		pl = pl.WithBurn(burn)
	}
	method = pl.method
	if method == "transcode" {
		release, ok := l.TranscodeSlot()
		if !ok {
			http.Error(w, "this computer is converting as many videos as it can already: try again when one stops", http.StatusServiceUnavailable)
			return
		}
		defer release()
	}
	args := append(pl.args(cfg, p, path, start), "-movflags", pl.movflags(cfg), "-f", "mp4", "pipe:1")
	// What each stream is, for the log: why a player shows no picture
	// starts there.
	log.Printf("stream: %s from %.0fs: %s", filepath.Base(path), start, pl.describe(cfg, p))

	// ffmpeg is stopped when the request ends, or as soon as the player stops
	// reading (see below).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// All of stderr must be read: a damaged file makes ffmpeg print an error
	// per frame, and once the pipe is full it blocks and playback freezes.
	// Wait returns only after this writer has consumed everything.
	stderr := &headWriter{max: 8 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		http.Error(w, "ffmpeg is not installed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Kumo-Method", method)
	if _, err := io.Copy(w, stdout); err != nil {
		// The player went away (or the connection broke): stop ffmpeg now
		// instead of leaving it blocked on a pipe nobody reads.
		cancel()
	}
	_ = cmd.Wait()
	if s := strings.TrimSpace(string(stderr.buf)); s != "" && ctx.Err() == nil {
		log.Printf("ffmpeg: %s", s)
	}
}

// headWriter keeps the first max bytes written to it and discards the rest.
type headWriter struct {
	buf []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	if n := min(len(p), h.max-len(h.buf)); n > 0 {
		h.buf = append(h.buf, p[:n]...)
	}
	return len(p), nil
}

// ServeSubtitle extracts an embedded text subtitle (or reads an external
// subtitle file) and returns WebVTT.
func (l *Local) ServeSubtitle(w http.ResponseWriter, r *http.Request, path string, index int, external string) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	if external != "" {
		// One of the video's own subtitle files (externalSubs): next to
		// it, named after it, and a file, not a link to elsewhere.
		ext := filepath.Clean(external)
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		fi, err := os.Lstat(ext)
		if filepath.Dir(ext) != filepath.Dir(path) || !library.SubtitleExtensions[strings.ToLower(filepath.Ext(ext))] ||
			!strings.HasPrefix(filepath.Base(ext), base) || err != nil || !fi.Mode().IsRegular() {
			http.Error(w, "invalid subtitle", http.StatusForbidden)
			return
		}
		b, err := os.ReadFile(ext)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_, _ = w.Write(ToVTT(b, strings.ToLower(ext)))
		return
	}
	cfg := l.settings.Get()
	key := fmt.Sprintf("subvtt:%s:%d", path, index)
	var cached string
	if l.db.GetCache(key, &cached) {
		_, _ = w.Write([]byte(cached))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-map", fmt.Sprintf("0:s:%d", index), "-f", "webvtt", "pipe:1").Output()
	if err != nil {
		http.Error(w, "could not extract subtitles (image-based subtitles need mpv)", http.StatusUnprocessableEntity)
		return
	}
	l.db.SetCache(key, string(out), 7*24*time.Hour)
	_, _ = w.Write(out)
}

// TranscodeSlot takes one of the conversions allowed at once (see
// MaxTranscodes): release gives it back; false when none is free.
func (l *Local) TranscodeSlot() (release func(), ok bool) {
	if l.MaxTranscodes <= 0 {
		return func() {}, true
	}
	if n := l.transcoding.Add(1); int(n) > l.MaxTranscodes {
		l.transcoding.Add(-1)
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(func() { l.transcoding.Add(-1) }) }, true
}

// Transcoding is how many videos are being converted now.
func (l *Local) Transcoding() int { return int(l.transcoding.Load()) }

// Encoder is what converts video that can't be played as it is: the
// encoder ffmpeg is told to use, "" when ffmpeg isn't there.
func (l *Local) Encoder() string {
	cfg := l.settings.Get()
	if _, ok := util.LookPath(cfg.Transcode.FfmpegPath); !ok {
		return ""
	}
	return encoderName(cfg)
}

// encoderName is the H.264 encoder the settings pick.
func encoderName(cfg config.Settings) string {
	switch cfg.Transcode.HwAccel {
	case "vaapi":
		return "h264_vaapi"
	case "nvenc":
		return "h264_nvenc"
	case "qsv":
		return "h264_qsv"
	case "v4l2m2m":
		return "h264_v4l2m2m"
	}
	return "libx264"
}

// HasV4L2Encoder reports a Raspberry Pi's video encoder, as ffmpeg can use
// it (h264_v4l2m2m, and the Pi's encoder device).
func HasV4L2Encoder(ffmpeg string) bool {
	if _, err := os.Stat("/dev/video11"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	return err == nil && strings.Contains(string(out), "h264_v4l2m2m")
}
