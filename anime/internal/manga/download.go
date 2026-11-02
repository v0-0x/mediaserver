package manga

// Downloading chapters: a provider's pages fetched one after another, put
// in a .cbz with their metadata (ComicInfo.xml), in this device's manga
// folder, or sent on to a media server's (Send) and then deleted here.
// One chapter at a time, in the order asked, so a provider isn't hammered.
// The queue lives in memory: an Aniko that stops forgets what was still
// waiting (what's done is in the folder).

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/extensions"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

// Job states.
const (
	Queued      = "queued"
	Downloading = "downloading"
	Sending     = "sending" // to the media server
	Done        = "done"
	Failed      = "failed"
)

// Job is a chapter to download.
type Job struct {
	ID        string `json:"id"`
	MediaID   int    `json:"mediaId"`
	Title     string `json:"title"` // the manga's
	Provider  string `json:"provider"`
	ChapterID string `json:"chapterId"`
	Chapter   string `json:"chapter"` // its number
	Name      string `json:"name,omitempty"`
	// Target: "" this device, else the media server (an Aniko's id) it
	// goes to, named TargetName.
	Target     string `json:"target,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	State      string `json:"state"`
	Pages      int    `json:"pages"`
	Got        int    `json:"got"` // pages fetched (or, sending, percent sent)
	Error      string `json:"error,omitempty"`
	Added      int64  `json:"added"`
	cancel     context.CancelFunc
}

// ChapterRef is a provider's chapter to download.
type ChapterRef struct {
	ID      string `json:"id"`
	Chapter string `json:"chapter"`
	Title   string `json:"title"`
}

// Sender sends finished files to a media server's manga folder: each into
// the folder of the manga (its name), as the file's base name.
type Sender func(ctx context.Context, target, folder string, files []string, progress func(percent int)) error

// Downloader downloads chapters.
type Downloader struct {
	lib   *Library
	pages func(ctx context.Context, provider, chapterID string) ([]extensions.MangaPage, error)
	media func(ctx context.Context, id int) (*anilist.Media, error)
	send  Sender
	// staging holds what goes to a media server, until it's there.
	staging string
	// OnChange runs when a job moves (for the page's live updates).
	OnChange func()
	client   *http.Client

	mu   sync.Mutex
	jobs []*Job
	wake chan struct{}
	seq  int
}

// NewDownloader downloads into lib (or staging, for a media server).
func NewDownloader(svc *Service, lib *Library, media func(ctx context.Context, id int) (*anilist.Media, error), send Sender, staging string) *Downloader {
	return &Downloader{
		lib: lib, pages: svc.Pages, media: media, send: send, staging: staging,
		wake: make(chan struct{}, 1),
		client: &http.Client{
			Timeout: 2 * time.Minute,
			Transport: &http.Transport{
				Proxy:                 util.PublicProxy,
				DialContext:           util.PublicDialContext(15 * time.Second),
				ResponseHeaderTimeout: 45 * time.Second,
				MaxIdleConnsPerHost:   4,
			},
		},
	}
}

// ErrNoChapters: nothing to download.
var ErrNoChapters = errors.New("no chapters to download")

// Add queues chapters of a manga, leaving out those already waiting or
// downloaded here (when they're for this device). It returns how many were
// added.
func (d *Downloader) Add(mediaID int, title, provider string, chapters []ChapterRef, target, targetName string) (int, error) {
	if mediaID <= 0 || provider == "" || len(chapters) == 0 {
		return 0, ErrNoChapters
	}
	if len(chapters) > 2000 {
		return 0, errors.New("too many chapters at once")
	}
	d.mu.Lock()
	added := 0
	for _, c := range chapters {
		if c.ID == "" || len(c.ID) > 2048 || len(c.Chapter) > 64 {
			continue
		}
		if target == "" && d.lib.Has(mediaID, c.Chapter) {
			continue
		}
		dup := false
		for _, j := range d.jobs {
			if j.MediaID == mediaID && j.Chapter == c.Chapter && j.Target == target && (j.State == Queued || j.State == Downloading || j.State == Sending) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		d.seq++
		d.jobs = append(d.jobs, &Job{
			ID: strconv.Itoa(d.seq), MediaID: mediaID, Title: title, Provider: provider,
			ChapterID: c.ID, Chapter: c.Chapter, Name: c.Title, Target: target, TargetName: targetName,
			State: Queued, Added: time.Now().Unix(),
		})
		added++
	}
	d.mu.Unlock()
	if added > 0 {
		d.changed()
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
	return added, nil
}

// List is the queue and what's done, oldest first.
func (d *Downloader) List() []Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Job, 0, len(d.jobs))
	for _, j := range d.jobs {
		c := *j
		c.cancel = nil
		out = append(out, c)
	}
	return out
}

// Remove cancels a job, or forgets it once it's done or failed.
func (d *Downloader) Remove(id string) bool {
	d.mu.Lock()
	found := false
	for i, j := range d.jobs {
		if j.ID == id {
			if j.cancel != nil {
				j.cancel()
			}
			d.jobs = append(d.jobs[:i], d.jobs[i+1:]...)
			found = true
			break
		}
	}
	d.mu.Unlock()
	if found {
		d.changed()
	}
	return found
}

// Clear forgets what's done and what failed.
func (d *Downloader) Clear() {
	d.mu.Lock()
	kept := d.jobs[:0]
	for _, j := range d.jobs {
		if j.State != Done && j.State != Failed {
			kept = append(kept, j)
		}
	}
	d.jobs = kept
	d.mu.Unlock()
	d.changed()
}

// Retry queues a failed job again.
func (d *Downloader) Retry(id string) bool {
	d.mu.Lock()
	ok := false
	for _, j := range d.jobs {
		if j.ID == id && j.State == Failed {
			j.State, j.Error, j.Got = Queued, "", 0
			ok = true
		}
	}
	d.mu.Unlock()
	if ok {
		d.changed()
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
	return ok
}

// Run downloads what's queued until ctx ends.
func (d *Downloader) Run(ctx context.Context) {
	for {
		j := d.next()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			}
		}
		jctx, cancel := context.WithCancel(ctx)
		d.mu.Lock()
		j.cancel = cancel
		d.mu.Unlock()
		err := d.do(jctx, j)
		cancel()
		d.mu.Lock()
		j.cancel = nil
		switch {
		case err == nil:
			j.State = Done
		case jctx.Err() != nil && ctx.Err() == nil:
			// Removed while it ran: gone from the list already.
		default:
			j.State, j.Error = Failed, err.Error()
			log.Printf("manga: %s chapter %s: %v", j.Title, j.Chapter, err)
		}
		d.mu.Unlock()
		d.changed()
		if ctx.Err() != nil {
			return
		}
	}
}

func (d *Downloader) next() *Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, j := range d.jobs {
		if j.State == Queued {
			j.State = Downloading
			return j
		}
	}
	return nil
}

func (d *Downloader) changed() {
	if d.OnChange != nil {
		d.OnChange()
	}
}

func (d *Downloader) set(j *Job, f func(*Job)) {
	d.mu.Lock()
	f(j)
	d.mu.Unlock()
	d.changed()
}

// do downloads a chapter.
func (d *Downloader) do(ctx context.Context, j *Job) error {
	m, err := d.media(ctx, j.MediaID)
	if err != nil {
		return fmt.Errorf("AniList doesn't answer about this manga: %w", err)
	}
	title := j.Title
	if title == "" {
		title = firstNonEmpty(m.Title.English, m.Title.Romaji, m.Title.Native)
	}
	folder := SeriesFolder(title, m.ID)
	if existing, err := d.lib.find(m.ID); err == nil {
		folder = existing // kept where it already is (a title changed since)
	}
	base := d.lib.Dir()
	if j.Target != "" {
		base = d.staging
	}
	dir := filepath.Join(base, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info := seriesInfo(m, title, j.Provider)
	if err := writeJSON(filepath.Join(dir, seriesFile), info); err != nil {
		return err
	}
	cover := filepath.Join(dir, coverFile)
	if _, err := os.Stat(cover); err != nil && info.CoverURL != "" {
		if data, _, err := d.fetch(ctx, info.CoverURL, nil); err == nil {
			_ = os.WriteFile(cover, data, 0o644)
		}
	}

	pages, err := d.pages(ctx, j.Provider, j.ChapterID)
	if err != nil {
		return fmt.Errorf("the provider didn't list the pages: %w", err)
	}
	if len(pages) == 0 {
		return errors.New("the provider has no pages for this chapter")
	}
	sort.SliceStable(pages, func(a, b int) bool { return pages[a].Index < pages[b].Index })
	d.set(j, func(j *Job) { j.Pages = len(pages) })
	name := chapterName(folder, j.Chapter)
	final := filepath.Join(dir, name)
	if err := d.writeCBZ(ctx, j, final, pages, info); err != nil {
		return err
	}
	if j.Target == "" {
		return nil
	}

	// To the media server, then gone from here.
	d.set(j, func(j *Job) { j.State, j.Got = Sending, 0 })
	files := []string{filepath.Join(dir, seriesFile), final}
	if _, err := os.Stat(cover); err == nil {
		files = append(files, cover)
	}
	if d.send == nil {
		return errors.New("this Aniko can't send files to a media server")
	}
	err = d.send(ctx, j.Target, folder, files, func(p int) { d.set(j, func(j *Job) { j.Got = p }) })
	if err != nil {
		return err
	}
	_ = os.Remove(final)
	return nil
}

// writeCBZ fetches the pages into a .cbz, with ComicInfo.xml.
func (d *Downloader) writeCBZ(ctx context.Context, j *Job, final string, pages []extensions.MangaPage, info SeriesInfo) error {
	part := final + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(part)
		}
	}()
	zw := zip.NewWriter(f)
	width := len(strconv.Itoa(len(pages)))
	if width < 3 {
		width = 3
	}
	for i, p := range pages {
		var data []byte
		var ext string
		var err error
		for try := 0; try < 3; try++ {
			if data, ext, err = d.fetch(ctx, p.URL, p.Headers); err == nil || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(try+1) * 2 * time.Second):
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("page %d didn't download: %w", i+1, err)
		}
		// Images are already compressed: stored as they are.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%0*d%s", width, i+1, ext), Method: zip.Store, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		d.set(j, func(j *Job) { j.Got = i + 1 })
	}
	ci := comicInfo(info, j, len(pages))
	w, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(ci); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(part, final); err != nil {
		return err
	}
	ok = true
	return nil
}

// maxImage is the largest page (or cover) taken.
const maxImage = 40 << 20

// fetch gets an image, with the provider's headers: its bytes and the file
// extension its type says.
func (d *Downloader) fetch(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, "", errors.New("not a web address")
	}
	req.Header.Set("User-Agent", util.UserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("the site answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImage {
		return nil, "", errors.New("the image is too large")
	}
	ext := extOf(http.DetectContentType(data))
	if ext == "" {
		ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		ext = extOf(ct)
	}
	if ext == "" {
		return nil, "", errors.New("the site didn't send an image")
	}
	return data, ext, nil
}

func extOf(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	}
	return ""
}

var reTag = regexp.MustCompile(`<[^>]*>`)

// seriesInfo is what series.json says, from AniList.
func seriesInfo(m *anilist.Media, title, provider string) SeriesInfo {
	info := SeriesInfo{
		AniListID: m.ID, Title: title, English: m.Title.English, Romaji: m.Title.Romaji, Native: m.Title.Native,
		Synonyms: m.Synonyms, Genres: m.Genres, Format: m.Format, Status: m.Status, Year: m.Year(),
		Country: m.CountryOfOrigin, Adult: m.IsAdult, SiteURL: m.SiteURL, Provider: provider,
		Description: strings.TrimSpace(htmlText(m.Description)),
		CoverURL:    firstNonEmpty(m.CoverImage.ExtraLarge, m.CoverImage.Large, m.CoverImage.Medium),
	}
	if m.IDMal != nil {
		info.MalID = *m.IDMal
	}
	if m.Chapters != nil {
		info.Chapters = *m.Chapters
	}
	if m.Volumes != nil {
		info.Volumes = *m.Volumes
	}
	if info.SiteURL == "" {
		info.SiteURL = fmt.Sprintf("https://anilist.co/manga/%d", m.ID)
	}
	return info
}

// comicInfo is a chapter's ComicInfo.xml.
func comicInfo(info SeriesInfo, j *Job, pages int) ComicInfo {
	ci := ComicInfo{
		Title: j.Name, Series: info.Title, Number: j.Chapter, Count: info.Chapters, Summary: info.Description,
		Year: info.Year, Genre: strings.Join(info.Genres, ", "), Web: info.SiteURL, PageCount: pages,
		LanguageISO: "en", Manga: "YesAndRightToLeft",
		Notes: fmt.Sprintf("AniList %d; downloaded by Aniko from %s", info.AniListID, j.Provider),
	}
	if info.Country == "KR" || info.Country == "CN" {
		ci.Manga = "Yes" // manhwa and manhua read left to right
	}
	if info.Adult {
		ci.AgeRating = "Adults Only 18+"
	}
	return ci
}

func htmlText(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n").Replace(s)
	s = reTag.ReplaceAllString(s, "")
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#039;", "'", "&#39;", "'").Replace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
