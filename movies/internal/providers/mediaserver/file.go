package mediaserver

import (
	"fmt"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fileInfo is what a server says about one of its files.
type fileInfo struct {
	name      string
	container string
	// height and width of the video; resolution is Plex's word for it
	// ("1080", "4k", "sd").
	height, width int
	resolution    string
	videoCodec    string
	hdr           bool
	audioCodec    string
	channels      int
}

// fill sets a stream's title (the file's name, then what's in it), type,
// quality and whether a browser plays it as it is.
func (f fileInfo) fill(st *providers.Stream) {
	st.Quality = f.quality()
	st.Type = f.streamType()
	st.NotWebReady = !f.webReady()
	var details []string
	if v := codecName(f.videoCodec); v != "" {
		details = append(details, v)
	}
	if f.hdr {
		details = append(details, "HDR")
	}
	if a := codecName(f.audioCodec); a != "" {
		if ch := channelLayout(f.channels); ch != "" {
			a += " " + ch
		}
		details = append(details, a)
	}
	if st.Type != providers.StreamUnknown {
		details = append(details, strings.ToUpper(st.Type))
	}
	st.Title = f.name
	if len(details) > 0 {
		st.Title += "\n" + strings.Join(details, " · ")
	}
}

// quality is "1080p"... from the video's size (its width first: a
// 1920x800 film is 1080p), else the server's word for it.
func (f fileInfo) quality() string {
	switch {
	case f.width >= 3200:
		return "2160p"
	case f.width >= 1800:
		return "1080p"
	case f.width >= 1200:
		return "720p"
	}
	if q := providers.QualityFromHeight(f.height); q != "" {
		return q
	}
	switch r := strings.ToLower(strings.TrimSpace(f.resolution)); r {
	case "":
		return ""
	case "4k":
		return "2160p"
	case "sd":
		return "480p"
	default:
		return providers.QualityFromText(strings.TrimSuffix(r, "p") + "p")
	}
}

// streamType is the file's container as a stream type: its file name's
// extension first, else what the server says (Jellyfin may say
// "mov,mp4,m4a,3gp,3g2,mj2").
func (f fileInfo) streamType() string {
	if t := providers.GuessType(f.name); t != providers.StreamUnknown {
		return t
	}
	for _, c := range strings.Split(strings.ToLower(f.container), ",") {
		switch strings.TrimSpace(c) {
		case "mkv", "matroska":
			return providers.StreamMKV
		case "mp4", "m4v", "mov":
			return providers.StreamMP4
		case "webm":
			return providers.StreamWebM
		}
	}
	return providers.StreamUnknown
}

// webReady reports a file a browser plays as it is: MP4 or WebM with
// H.264/VP9/AV1 video and AAC/MP3/Opus/Vorbis/FLAC audio.
func (f fileInfo) webReady() bool {
	switch f.streamType() {
	case providers.StreamMP4, providers.StreamWebM:
	default:
		return false
	}
	switch strings.ToLower(f.videoCodec) {
	case "", "h264", "avc", "avc1", "vp8", "vp9", "av1":
	default:
		return false
	}
	switch strings.ToLower(f.audioCodec) {
	case "", "aac", "mp3", "opus", "vorbis", "flac":
	default:
		return false
	}
	return true
}

// codecName is how a codec is usually written.
func codecName(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "":
		return ""
	case "h264", "avc", "avc1":
		return "H.264"
	case "hevc", "h265":
		return "HEVC"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "mpeg2video":
		return "MPEG-2"
	case "aac":
		return "AAC"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	case "dts", "dca":
		return "DTS"
	case "truehd":
		return "TrueHD"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "mp3":
		return "MP3"
	}
	return strings.ToUpper(c)
}

// channelLayout is "5.1" for 6 channels, "2.0" for 2...
func channelLayout(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1.0"
	case n == 2:
		return "2.0"
	case n >= 6:
		return fmt.Sprintf("%d.1", n-1)
	}
	return fmt.Sprintf("%d.0", n)
}
