package manga

// Downloaded manga, kept the way comic readers (Komga, Kavita, Mihon…)
// expect, so the folder is useful outside Aniko too:
//
//	<dir>/<Title> [anilist-<id>]/
//	    series.json             AniList's facts about the manga
//	    cover.jpg
//	    <Title> - Ch. 012.5.cbz the chapter's pages (001.jpg…) and
//	                            ComicInfo.xml, its metadata
//
// A chapter is a .cbz (a zip of its pages); one being written is .part
// until it's whole.

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SeriesInfo is a manga's series.json.
type SeriesInfo struct {
	AniListID   int      `json:"anilistId"`
	MalID       int      `json:"malId,omitempty"`
	Title       string   `json:"title"`
	English     string   `json:"english,omitempty"`
	Romaji      string   `json:"romaji,omitempty"`
	Native      string   `json:"native,omitempty"`
	Synonyms    []string `json:"synonyms,omitempty"`
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Format      string   `json:"format,omitempty"`
	Status      string   `json:"status,omitempty"`
	Year        int      `json:"year,omitempty"`
	Chapters    int      `json:"chapters,omitempty"` // as AniList knows them; 0: not known
	Volumes     int      `json:"volumes,omitempty"`
	Country     string   `json:"country,omitempty"`
	Adult       bool     `json:"adult,omitempty"`
	SiteURL     string   `json:"siteUrl,omitempty"`
	CoverURL    string   `json:"coverUrl,omitempty"`
	// Where its chapters came from.
	Provider string `json:"provider,omitempty"`
}

// Series is a downloaded manga.
type Series struct {
	SeriesInfo
	Folder   string `json:"folder"` // its folder's name
	Count    int    `json:"count"`  // chapters downloaded
	Size     int64  `json:"size"`
	HasCover bool   `json:"hasCover"`
	Latest   string `json:"latest,omitempty"` // the highest chapter's number
	Updated  int64  `json:"updated"`          // the newest chapter's time
}

// LocalChapter is a downloaded chapter.
type LocalChapter struct {
	Number string `json:"chapter"`
	Title  string `json:"title,omitempty"`
	File   string `json:"file"` // <folder>/<name>.cbz, from the manga folder
	Pages  int    `json:"pages"`
	Size   int64  `json:"size"`
	Added  int64  `json:"added"`
}

// ComicInfo is the metadata in a chapter's ComicInfo.xml (the
// ComicRack schema the readers share).
type ComicInfo struct {
	XMLName     xml.Name `xml:"ComicInfo"`
	Title       string   `xml:"Title,omitempty"`
	Series      string   `xml:"Series"`
	Number      string   `xml:"Number"`
	Count       int      `xml:"Count,omitempty"`
	Volume      int      `xml:"Volume,omitempty"`
	Summary     string   `xml:"Summary,omitempty"`
	Year        int      `xml:"Year,omitempty"`
	Genre       string   `xml:"Genre,omitempty"`
	Web         string   `xml:"Web,omitempty"`
	PageCount   int      `xml:"PageCount"`
	LanguageISO string   `xml:"LanguageISO,omitempty"`
	Manga       string   `xml:"Manga,omitempty"` // Yes, YesAndRightToLeft
	AgeRating   string   `xml:"AgeRating,omitempty"`
	ScanInfo    string   `xml:"ScanInformation,omitempty"`
	Notes       string   `xml:"Notes,omitempty"`
}

// Library is the folder of downloaded manga.
type Library struct {
	// Dir is the folder (it may change with the settings).
	Dir func() string
}

// ErrNotFound: no such manga or chapter in the folder.
var ErrNotFound = errors.New("not in the manga folder")

const seriesFile = "series.json"
const coverFile = "cover.jpg"

// List is the downloaded manga, the most recently updated first.
func (l *Library) List() ([]Series, error) {
	dir := l.Dir()
	out := []Series{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := l.series(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

// series reads a manga's folder: its series.json and its chapters' sizes.
func (l *Library) series(folder string) (*Series, error) {
	root := filepath.Join(l.Dir(), folder)
	raw, err := os.ReadFile(filepath.Join(root, seriesFile))
	if err != nil {
		return nil, err
	}
	s := &Series{Folder: folder}
	if err := json.Unmarshal(raw, &s.SeriesInfo); err != nil || s.AniListID <= 0 {
		return nil, fmt.Errorf("%s: no AniList id", folder)
	}
	_, err = os.Stat(filepath.Join(root, coverFile))
	s.HasCover = err == nil
	entries, _ := os.ReadDir(root)
	best := -1.0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".cbz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		s.Count++
		s.Size += info.Size()
		if t := info.ModTime().Unix(); t > s.Updated {
			s.Updated = t
		}
		if n, ok := numberOf(e.Name()); ok {
			if f, err := strconv.ParseFloat(n, 64); err == nil && f > best {
				best, s.Latest = f, n
			}
		}
	}
	return s, nil
}

// find is the folder of the manga mediaID.
func (l *Library) find(mediaID int) (string, error) {
	entries, err := os.ReadDir(l.Dir())
	if err != nil {
		return "", ErrNotFound
	}
	suffix := fmt.Sprintf("[anilist-%d]", mediaID)
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			return e.Name(), nil
		}
	}
	// One named otherwise (renamed by hand): by its series.json.
	for _, e := range entries {
		if s, err := l.series(e.Name()); err == nil && s.AniListID == mediaID {
			return e.Name(), nil
		}
	}
	return "", ErrNotFound
}

// Series is a downloaded manga and its chapters, in order.
func (l *Library) Series(mediaID int) (*Series, []LocalChapter, error) {
	folder, err := l.find(mediaID)
	if err != nil {
		return nil, nil, err
	}
	s, err := l.series(folder)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	root := filepath.Join(l.Dir(), folder)
	entries, _ := os.ReadDir(root)
	chapters := []LocalChapter{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".cbz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c := LocalChapter{File: folder + "/" + e.Name(), Size: info.Size(), Added: info.ModTime().Unix()}
		c.Number, _ = numberOf(e.Name())
		if ci, pages, err := readCBZ(filepath.Join(root, e.Name())); err == nil {
			c.Pages = pages
			if ci != nil {
				if ci.Number != "" {
					c.Number = ci.Number
				}
				c.Title = ci.Title
			}
		}
		chapters = append(chapters, c)
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		a, _ := strconv.ParseFloat(chapters[i].Number, 64)
		b, _ := strconv.ParseFloat(chapters[j].Number, 64)
		return a < b
	})
	return s, chapters, nil
}

// Has reports the chapter (its number) of the manga is downloaded.
func (l *Library) Has(mediaID int, number string) bool {
	folder, err := l.find(mediaID)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(l.Dir(), folder, chapterName(folder, number)))
	return err == nil
}

// Cover is the path of the manga's cover, if it has one.
func (l *Library) Cover(mediaID int) (string, error) {
	folder, err := l.find(mediaID)
	if err != nil {
		return "", err
	}
	p := filepath.Join(l.Dir(), folder, coverFile)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Page opens the n-th page (from 0) of a chapter (its File).
func (l *Library) Page(file string, n int) (io.ReadCloser, string, error) {
	full, err := l.chapterPath(file)
	if err != nil {
		return nil, "", err
	}
	zr, err := zip.OpenReader(full)
	if err != nil {
		return nil, "", ErrNotFound
	}
	pages := imagesOf(&zr.Reader)
	if n < 0 || n >= len(pages) {
		zr.Close()
		return nil, "", ErrNotFound
	}
	f, err := pages[n].Open()
	if err != nil {
		zr.Close()
		return nil, "", err
	}
	return &zipFile{ReadCloser: f, zr: zr}, imageType(pages[n].Name), nil
}

// Pages is how many pages a chapter has.
func (l *Library) Pages(file string) (int, error) {
	full, err := l.chapterPath(file)
	if err != nil {
		return 0, err
	}
	_, n, err := readCBZ(full)
	return n, err
}

// Delete removes a chapter (its File) of the manga, or the whole manga
// (file ""): its folder, once no chapter is left in it.
func (l *Library) Delete(mediaID int, file string) error {
	folder, err := l.find(mediaID)
	if err != nil {
		return err
	}
	if file != "" {
		full, err := l.chapterPath(file)
		if err != nil || !strings.HasPrefix(file, folder+"/") {
			return ErrNotFound
		}
		return os.Remove(full)
	}
	return os.RemoveAll(filepath.Join(l.Dir(), folder))
}

// chapterPath is a chapter's full path, from its File: within the
// folder, a .cbz, and there.
func (l *Library) chapterPath(file string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(file, "\\", "/"))[1:]
	if clean == "" || clean != file || strings.Count(clean, "/") != 1 || !strings.EqualFold(path.Ext(clean), ".cbz") {
		return "", ErrNotFound
	}
	full := filepath.Join(l.Dir(), filepath.FromSlash(clean))
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		return "", ErrNotFound
	}
	return full, nil
}

// SeriesFolder is the folder name of a manga: its title and AniList id.
func SeriesFolder(title string, mediaID int) string {
	return fmt.Sprintf("%s [anilist-%d]", safeName(title), mediaID)
}

// chapterName is a chapter's file name in its manga's folder: the title
// (the folder's, without the id) and its number, padded to sort.
func chapterName(folder, number string) string {
	t := folder
	if i := strings.LastIndex(t, " [anilist-"); i > 0 {
		t = t[:i]
	}
	return fmt.Sprintf("%s - Ch. %s.cbz", t, padNumber(number))
}

// padNumber pads a chapter number to three digits: 7 → 007, 12.5 → 012.5.
func padNumber(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return "000"
	}
	whole, frac, _ := strings.Cut(n, ".")
	for len(whole) < 3 {
		whole = "0" + whole
	}
	if frac != "" {
		return whole + "." + frac
	}
	return whole
}

// numberOf is the chapter number in a chapter's file name (" - Ch. 012.5.cbz").
func numberOf(name string) (string, bool) {
	i := strings.LastIndex(name, " - Ch. ")
	if i < 0 {
		return "", false
	}
	n := strings.TrimSuffix(strings.TrimSuffix(name[i+len(" - Ch. "):], ".cbz"), ".CBZ")
	whole, frac, has := strings.Cut(n, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if has {
		return whole + "." + frac, true
	}
	return whole, true
}

// safeName is a title fit for a file name on every system.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimRight(out, ". ")
	if len(out) > 120 {
		out = strings.TrimSpace(out[:120])
	}
	if out == "" {
		out = "Manga"
	}
	return out
}

// readCBZ reads a chapter's ComicInfo.xml (nil without one) and counts its
// pages.
func readCBZ(full string) (*ComicInfo, int, error) {
	zr, err := zip.OpenReader(full)
	if err != nil {
		return nil, 0, err
	}
	defer zr.Close()
	var ci *ComicInfo
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "ComicInfo.xml") && f.UncompressedSize64 < 1<<20 {
			if r, err := f.Open(); err == nil {
				var c ComicInfo
				if xml.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&c) == nil {
					ci = &c
				}
				r.Close()
			}
		}
	}
	return ci, len(imagesOf(&zr.Reader)), nil
}

// imagesOf is a zip's images, in name order.
func imagesOf(zr *zip.Reader) []*zip.File {
	var out []*zip.File
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && imageType(f.Name) != "" {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func imageType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	}
	return ""
}

// zipFile closes its zip with it.
type zipFile struct {
	io.ReadCloser
	zr *zip.ReadCloser
}

func (z *zipFile) Close() error {
	err := z.ReadCloser.Close()
	z.zr.Close()
	return err
}
