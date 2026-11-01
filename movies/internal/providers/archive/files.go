package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Metadata is what the metadata API says about an item.
type Metadata struct {
	Identifier string
	Title      string
	Year       int
	Files      []File
	// BTIH is the info hash of the item's torrent (archive.org seeds one
	// for most items), "" when unknown.
	BTIH string
	// Restricted items are lent, not streamed; dark items are withdrawn.
	Restricted bool
	Dark       bool
}

// File is one of an item's files.
type File struct {
	Name   string
	Source string // original | derivative | metadata
	Format string // "h.264", "512Kb MPEG4", "Matroska", "SubRip"...
	Size   int64
	Height int
	Width  int
	// Length is the duration in seconds (0 when unknown).
	Length float64
	BTIH   string
}

type rawMetadata struct {
	Metadata map[string]json.RawMessage   `json:"metadata"`
	Files    []map[string]json.RawMessage `json:"files"`
	IsDark   json.RawMessage              `json:"is_dark"`
	BTIH     json.RawMessage              `json:"btih"`
}

// Metadata fetches an item's metadata and file list.
func (p *Provider) Metadata(ctx context.Context, identifier string) (*Metadata, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || strings.ContainsAny(identifier, "/?#") {
		return nil, fmt.Errorf("invalid archive.org identifier %q", identifier)
	}
	var raw rawMetadata
	if err := p.getJSON(ctx, p.base()+"/metadata/"+url.PathEscape(identifier), &raw); err != nil {
		return nil, fmt.Errorf("archive.org item %s: %w", identifier, err)
	}
	if raw.Metadata == nil && len(raw.Files) == 0 {
		return nil, fmt.Errorf("archive.org has no item %q", identifier)
	}
	md := &Metadata{
		Identifier: identifier,
		Title:      firstString(raw.Metadata["title"]),
		Year:       flexInt(raw.Metadata["year"]),
		BTIH:       providers.NormalizeInfoHash(firstString(raw.BTIH)),
		Restricted: truthy(raw.Metadata["access-restricted-item"]),
		Dark:       truthy(raw.IsDark),
	}
	if md.Year == 0 {
		md.Year = flexInt(raw.Metadata["date"])
	}
	if md.BTIH == "" {
		md.BTIH = providers.NormalizeInfoHash(firstString(raw.Metadata["btih"]))
	}
	for _, f := range raw.Files {
		file := File{
			Name:   firstString(f["name"]),
			Source: firstString(f["source"]),
			Format: firstString(f["format"]),
			Size:   int64(flexFloat(f["size"])),
			Height: int(flexFloat(f["height"])),
			Width:  int(flexFloat(f["width"])),
			Length: flexFloat(f["length"]),
			BTIH:   providers.NormalizeInfoHash(firstString(f["btih"])),
		}
		if file.Name == "" {
			continue
		}
		if md.BTIH == "" && file.BTIH != "" {
			md.BTIH = file.BTIH
		}
		md.Files = append(md.Files, file)
	}
	return md, nil
}

func truthy(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return strings.EqualFold(firstString(raw), "true") || firstString(raw) == "1"
}

func flexFloat(raw json.RawMessage) float64 {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	f, _ = strconv.ParseFloat(firstString(raw), 64)
	return f
}

// ItemStreams returns the video files of an item as streams, with the
// item's subtitle files.
func (p *Provider) ItemStreams(ctx context.Context, identifier string) ([]providers.Stream, error) {
	md, err := p.Metadata(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return p.streamsOf(md)
}

func (p *Provider) streamsOf(md *Metadata) ([]providers.Stream, error) {
	if md.Dark {
		return nil, fmt.Errorf("archive.org item %s was withdrawn", md.Identifier)
	}
	if md.Restricted {
		return nil, fmt.Errorf("archive.org item %s can only be borrowed", md.Identifier)
	}
	var videos []File
	var subs []providers.Subtitle
	for _, f := range md.Files {
		switch {
		case isSubtitle(f):
			subs = append(subs, providers.Subtitle{URL: p.downloadURL(md.Identifier, f.Name), Language: subtitleLanguage(f.Name), Label: path.Base(f.Name)})
		case isVideo(f):
			videos = append(videos, f)
		}
	}
	videos = dropExtras(videos)
	sort.SliceStable(videos, func(i, j int) bool { return formatRank(videos[i]) < formatRank(videos[j]) })
	title := md.Title
	if title == "" {
		title = md.Identifier
	}
	if md.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, md.Year)
	}
	out := make([]providers.Stream, 0, len(videos))
	for _, f := range videos {
		u := p.downloadURL(md.Identifier, f.Name)
		typ := providers.GuessType(f.Name)
		s := providers.Stream{
			Provider:     ID,
			ProviderName: Name,
			Title:        fmt.Sprintf("%s\n%s · %s", title, path.Base(f.Name), formatLabel(f)),
			URL:          u,
			Type:         typ,
			Quality:      fileQuality(f),
			Size:         f.Size,
			Subtitles:    subs,
			FileIdx:      -1,
			Filename:     path.Base(f.Name),
			// Matroska and Ogg don't play in every browser.
			NotWebReady: typ == providers.StreamMKV || strings.HasSuffix(strings.ToLower(f.Name), ".ogv"),
		}
		out = append(out, s)
	}
	return out, nil
}

// Torrent returns the info hash and the .torrent URL of an item (the
// archive seeds every public item over BitTorrent with web seeds). btih is
// "" when the metadata doesn't give it.
func (p *Provider) Torrent(ctx context.Context, identifier string) (btih, torrentURL string, err error) {
	md, err := p.Metadata(ctx, identifier)
	if err != nil {
		return "", "", err
	}
	for _, f := range md.Files {
		if strings.EqualFold(f.Format, "Archive BitTorrent") || strings.HasSuffix(strings.ToLower(f.Name), "_archive.torrent") {
			return md.BTIH, p.downloadURL(md.Identifier, f.Name), nil
		}
	}
	if md.BTIH == "" {
		return "", "", errors.New("this item has no torrent")
	}
	return md.BTIH, p.downloadURL(md.Identifier, md.Identifier+"_archive.torrent"), nil
}

// downloadURL is a file's URL: each path segment escaped (names have
// spaces, accents, '#'...).
func (p *Provider) downloadURL(identifier, name string) string {
	parts := strings.Split(name, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return p.base() + "/download/" + url.PathEscape(identifier) + "/" + strings.Join(parts, "/")
}

var videoExts = map[string]bool{".mp4": true, ".m4v": true, ".mkv": true, ".webm": true, ".ogv": true}

func isVideo(f File) bool {
	return videoExts[strings.ToLower(path.Ext(f.Name))]
}

func isSubtitle(f File) bool {
	switch strings.ToLower(path.Ext(f.Name)) {
	case ".srt", ".vtt":
		return true
	}
	return false
}

// dropExtras leaves trailers and samples out, unless they're all there is.
func dropExtras(videos []File) []File {
	var keep []File
	for _, f := range videos {
		n := strings.ToLower(path.Base(f.Name))
		if strings.Contains(n, "trailer") || strings.Contains(n, "sample") || strings.Contains(n, "teaser") {
			continue
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return videos
	}
	return keep
}

// formatRank orders an item's videos: the browser-friendly h.264
// derivatives first, the tiny 512Kb ones and Ogg last.
func formatRank(f File) int {
	switch strings.ToLower(f.Format) {
	case "h.264 hd":
		return 0
	case "h.264", "h.264 ia":
		return 1
	case "mpeg4", "hires mpeg4":
		return 2
	case "matroska":
		return 3
	case "webm":
		return 4
	case "512kb mpeg4":
		return 6
	case "ogg video", "ogg theora":
		return 7
	}
	switch strings.ToLower(path.Ext(f.Name)) {
	case ".mp4", ".m4v":
		return 2
	case ".mkv":
		return 3
	case ".webm":
		return 4
	}
	return 5
}

func formatLabel(f File) string {
	if f.Format != "" {
		return f.Format
	}
	return strings.TrimPrefix(strings.ToLower(path.Ext(f.Name)), ".")
}

func fileQuality(f File) string {
	if q := providers.QualityFromHeight(f.Height); q != "" {
		return q
	}
	switch strings.ToLower(f.Format) {
	case "512kb mpeg4":
		return "240p"
	case "h.264 hd":
		return "720p"
	case "hires mpeg4":
		return "480p"
	}
	return providers.QualityFromText(path.Base(f.Name))
}

var languageNames = map[string]string{
	"english": "en", "french": "fr", "francais": "fr", "spanish": "es", "espanol": "es", "german": "de",
	"deutsch": "de", "italian": "it", "portuguese": "pt", "russian": "ru", "japanese": "ja", "chinese": "zh",
	"dutch": "nl", "polish": "pl", "swedish": "sv", "danish": "da", "finnish": "fi", "norwegian": "no",
	"greek": "el", "turkish": "tr", "arabic": "ar", "hebrew": "he", "korean": "ko", "czech": "cs",
	"hungarian": "hu", "romanian": "ro",
}

// subtitleLanguage guesses a subtitle file's language from its name
// ("Film.en.srt", "Film_eng.srt", "Film - English.srt"), or "".
func subtitleLanguage(name string) string {
	base := strings.TrimSuffix(path.Base(name), path.Ext(name))
	fields := strings.FieldsFunc(base, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == ' ' || r == '(' || r == ')' })
	if len(fields) < 2 {
		if code, ok := languageNames[strings.ToLower(base)]; ok {
			return code
		}
		return ""
	}
	last := strings.ToLower(fields[len(fields)-1])
	if code, ok := languageNames[last]; ok {
		return code
	}
	if len(last) == 2 && strings.IndexFunc(last, func(r rune) bool { return r < 'a' || r > 'z' }) < 0 {
		return last
	}
	if languageCodes3[last] {
		return last
	}
	return ""
}

// languageCodes3 are the ISO 639-2 codes subtitle file names commonly end
// with (other three-letter words, like "end", aren't languages).
var languageCodes3 = map[string]bool{
	"eng": true, "fre": true, "fra": true, "spa": true, "ger": true, "deu": true, "ita": true, "por": true,
	"rus": true, "jpn": true, "chi": true, "zho": true, "dut": true, "nld": true, "pol": true, "swe": true,
	"dan": true, "fin": true, "nor": true, "gre": true, "ell": true, "tur": true, "ara": true, "heb": true,
	"kor": true, "cze": true, "ces": true, "hun": true, "rum": true, "ron": true,
}
