// Package downloads runs the movie and episode download queue (streaming
// streams). Finished files land in the library and are matched immediately.
package downloads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/release"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

const (
	StatusQueued      = "queued"
	StatusResolving   = "resolving"
	StatusDownloading = "downloading"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
)

type Item struct {
	ID        string            `json:"id"`
	MediaID   int               `json:"mediaId"`
	Episode   int               `json:"episode"`
	Title     string            `json:"title"`
	Image     string            `json:"image"`
	Mode      string            `json:"mode"`   // sub | dub
	Source    string            `json:"source"` // stream (a provider), or a resolver's name
	Provider  string            `json:"provider"`
	Status    string            `json:"status"`
	Progress  float64           `json:"progress"` // 0-1
	Speed     string            `json:"speed"`
	ETA       string            `json:"eta"`
	Done      string            `json:"done,omitempty"` // how much so far when the total isn't known (ffmpeg: minutes of video)
	Error     string            `json:"error,omitempty"`
	Output    string            `json:"output,omitempty"`
	Quality   string            `json:"quality,omitempty"`
	URL       string            `json:"url,omitempty"`
	Referrer  string            `json:"referrer,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	SubURL    string            `json:"subUrl,omitempty"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

// Resolved is what a resolver returns: a direct URL to fetch.
type Resolved struct {
	URL      string
	Referrer string
	Headers  map[string]string
	SubURL   string
	// Container is the video's ("mp4", "mkv", "webm"), "" when unknown:
	// the file is saved in it.
	Container string
	// Page is set for a web page holding the video (YouTube, a video
	// site), which only yt-dlp downloads. A link that turns out to answer
	// with a page goes to yt-dlp too.
	Page bool
}

// container is the container a download is saved in: the resolver's, else
// the one the URL's extension names, else MP4 (HLS streams are copied into
// it).
func container(res *Resolved) string {
	switch c := strings.ToLower(res.Container); c {
	case "mp4", "mkv", "webm":
		return c
	}
	if u, err := url.Parse(res.URL); err == nil {
		switch strings.ToLower(path.Ext(u.Path)) {
		case ".mkv":
			return "mkv"
		case ".webm":
			return "webm"
		}
	}
	return "mp4"
}

// Resolver turns a queued item into a downloadable URL (e.g. through a
// streaming provider).
type Resolver func(ctx context.Context, it *Item) (*Resolved, error)

type Manager struct {
	settings *config.Store
	db       *db.DB
	hub      *events.Hub
	files    *library.Store

	Resolvers map[string]Resolver
	// Done tells about a finished download, instead of the usual toast when
	// it returns true (package notify).
	Done func(Item) bool
	// Ended: a download finished or failed (a hub's audit log).
	Ended func(Item)

	// Lookup gives the movie or show of a download, whose title and year
	// name its folder and file. Unset (tests), they're named after the
	// item's title.
	Lookup func(ctx context.Context, mediaID int) (*catalog.Media, error)

	// saveMu orders the writes of an item (database row and UI event) with
	// its deletion, so a late update can't bring a removed item back.
	// Lock it before mu.
	saveMu sync.Mutex

	mu       sync.Mutex
	wake     *sync.Cond // signaled when pending grows
	items    map[string]*Item
	runs     map[string]*run // runs that haven't ended yet, by item id
	pending  []string        // queued item ids, oldest first (may hold ids since canceled or removed)
	restored []string        // downloads interrupted by the last shutdown, queued by Start

	// fetch downloads res into out (replaced in tests).
	fetch func(r *run, cfg config.Settings, res *Resolved, out string) error
}

// run is one attempt at downloading an item. Canceling or removing the item
// makes the run stale: it no longer touches the item from then on, but it
// stays in Manager.runs until it has stopped, so that the item is never
// downloaded twice at the same time.
type run struct {
	it     *Item
	ctx    context.Context
	cancel context.CancelFunc
	stale  bool // guarded by Manager.mu
}

func NewManager(s *config.Store, d *db.DB, hub *events.Hub, files *library.Store) *Manager {
	m := &Manager{
		settings:  s,
		db:        d,
		hub:       hub,
		files:     files,
		Resolvers: map[string]Resolver{},
		items:     map[string]*Item{},
		runs:      map[string]*run{},
	}
	m.wake = sync.NewCond(&m.mu)
	m.fetch = m.download
	m.load()
	for i := 0; i < 2; i++ {
		go m.worker()
	}
	return m
}

func (m *Manager) load() {
	rows, err := m.db.Query(`SELECT data FROM downloads ORDER BY created_at`)
	if err != nil {
		return
	}
	defer rows.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var it Item
		if json.Unmarshal([]byte(raw), &it) != nil {
			continue
		}
		if it.Title == "" {
			// Saved by an earlier build, under Kumo's name for it.
			var old struct {
				Title string `json:"animeTitle"`
			}
			_ = json.Unmarshal([]byte(raw), &old)
			it.Title = old.Title
		}
		if active(it.Status) {
			it.Status, it.Progress, it.Speed, it.ETA, it.Done = StatusQueued, 0, "", "", ""
			m.restored = append(m.restored, it.ID)
		}
		m.items[it.ID] = &it
	}
}

// Start resumes the downloads that were interrupted when Moviko last quit.
// Call it once the resolvers, and the extensions they rely on, are ready;
// new downloads don't wait for it.
func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, m.restored...)
	m.restored = nil
	m.wake.Broadcast()
}

func active(status string) bool {
	return status == StatusQueued || status == StatusResolving || status == StatusDownloading
}

// queue hands an item to the workers. The caller holds m.mu.
func (m *Manager) queue(id string) {
	m.pending = append(m.pending, id)
	m.wake.Signal()
}

// save publishes the current state of it to the UI, and stores it as well
// when persist is set. Nothing is written once the item has been removed.
func (m *Manager) save(it *Item, persist bool) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	if m.items[it.ID] != it {
		m.mu.Unlock()
		return
	}
	if persist {
		it.UpdatedAt = time.Now().Unix()
	}
	c := *it
	m.mu.Unlock()
	if persist {
		raw, _ := json.Marshal(&c)
		_, _ = m.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET data = excluded.data`, c.ID, string(raw), c.CreatedAt)
	}
	m.hub.Publish(events.DownloadProgress, &c)
}

// update applies fn to the run's item and saves it, unless the run is stale.
func (m *Manager) update(r *run, fn func(it *Item)) {
	m.mu.Lock()
	if r.stale {
		m.mu.Unlock()
		return
	}
	fn(r.it)
	m.mu.Unlock()
	m.save(r.it, true)
}

// progress returns a func that records the run's progress (unless the run
// is stale) and publishes it at most every 700ms.
func (m *Manager) progress(r *run) func(fn func(it *Item)) {
	var last time.Time
	return func(fn func(it *Item)) {
		m.mu.Lock()
		if r.stale {
			m.mu.Unlock()
			return
		}
		fn(r.it)
		m.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			m.save(r.it, false)
		}
	}
}

// List returns all items, newest first.
func (m *Manager) List() []*Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Item, 0, len(m.items))
	for _, it := range m.items {
		c := *it
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Enqueue adds an item to the queue (skipping duplicates still in progress).
func (m *Manager) Enqueue(it Item) (*Item, error) {
	if _, ok := m.Resolvers[it.Source]; !ok && it.URL == "" {
		return nil, fmt.Errorf("unknown download source %q", it.Source)
	}
	m.mu.Lock()
	for _, x := range m.items {
		if x.MediaID == it.MediaID && x.Episode == it.Episode && x.Mode == it.Mode && active(x.Status) {
			c := *x
			m.mu.Unlock()
			return &c, nil
		}
	}
	it.ID = uuid.NewString()
	it.Status = StatusQueued
	it.CreatedAt = time.Now().UnixNano() / int64(time.Millisecond)
	it.UpdatedAt = time.Now().Unix()
	p := &it
	m.items[it.ID] = p
	m.queue(it.ID)
	c := *p
	m.mu.Unlock()
	m.save(p, true)
	return &c, nil
}

func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	it := m.items[id]
	if it == nil || !active(it.Status) {
		m.mu.Unlock()
		return
	}
	it.Status, it.Speed, it.ETA, it.Done = StatusCanceled, "", "", ""
	r := m.runs[id]
	if r != nil {
		r.stale = true
	}
	m.mu.Unlock()
	if r != nil {
		r.cancel()
	}
	m.save(it, true)
}

func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	it := m.items[id]
	if it == nil {
		m.mu.Unlock()
		return errors.New("not found")
	}
	if it.Status != StatusFailed && it.Status != StatusCanceled {
		m.mu.Unlock()
		return nil
	}
	it.Status, it.Error, it.Progress, it.Speed, it.ETA, it.Done = StatusQueued, "", 0, "", "", ""
	// A canceled run that is still stopping requeues the item once it's done.
	if m.runs[id] == nil {
		m.queue(id)
	}
	m.mu.Unlock()
	m.save(it, true)
	return nil
}

// Remove deletes an item from the list (cancelling it first).
func (m *Manager) Remove(id string) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	delete(m.items, id)
	r := m.runs[id]
	if r != nil {
		r.stale = true
	}
	m.mu.Unlock()
	if r != nil {
		r.cancel()
	}
	_, _ = m.db.Write(`DELETE FROM downloads WHERE id = ?`, id)
	m.hub.Publish(events.DownloadProgress, map[string]any{"id": id, "removed": true})
}

// ClearFinished removes completed/failed/canceled items.
func (m *Manager) ClearFinished() {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	var ids []string
	for id, it := range m.items {
		if it.Status == StatusCompleted || it.Status == StatusFailed || it.Status == StatusCanceled {
			ids = append(ids, id)
			delete(m.items, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.db.Write(`DELETE FROM downloads WHERE id = ?`, id)
	}
	m.hub.Publish(events.DownloadProgress, map[string]any{"cleared": true})
}

func (m *Manager) worker() {
	for {
		m.execute(m.next())
	}
}

// next waits for a queued item and claims it.
func (m *Manager) next() *run {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		for len(m.pending) > 0 {
			id := m.pending[0]
			m.pending = m.pending[1:]
			it := m.items[id]
			// Skip items canceled or removed since they were queued, and items
			// whose previous run is still stopping (it requeues them).
			if it == nil || it.Status != StatusQueued || m.runs[id] != nil {
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			r := &run{it: it, ctx: ctx, cancel: cancel}
			m.runs[id] = r
			return r
		}
		m.wake.Wait()
	}
}

func (m *Manager) execute(r *run) {
	err := m.process(r)
	r.cancel()

	m.mu.Lock()
	it := r.it
	delete(m.runs, it.ID)
	if r.stale {
		// Canceled or removed meanwhile. If it has been retried since, it was
		// waiting for this run to stop.
		if m.items[it.ID] == it && it.Status == StatusQueued {
			m.queue(it.ID)
		}
		m.mu.Unlock()
		return
	}
	if err != nil {
		it.Status, it.Error = StatusFailed, err.Error()
	} else {
		it.Status, it.Progress = StatusCompleted, 1
	}
	it.Speed, it.ETA, it.Done = "", "", ""
	done := *it
	m.mu.Unlock()
	m.save(it, true)
	switch done.Status {
	case StatusCompleted:
		if m.Done == nil || !m.Done(done) {
			m.hub.Success("Downloaded " + label(&done))
		}
	case StatusFailed:
		m.hub.Error(fmt.Sprintf("Download failed (%s): %s", label(&done), done.Error))
	}
	if m.Ended != nil {
		m.Ended(done)
	}
}

func (m *Manager) process(r *run) error {
	ctx := r.ctx
	cfg := m.settings.Get()
	m.update(r, func(it *Item) { it.Status = StatusResolving })
	m.mu.Lock()
	it := *r.it // the fields read below never change
	m.mu.Unlock()

	res := &Resolved{URL: it.URL, Referrer: it.Referrer, Headers: it.Headers, SubURL: it.SubURL}
	if res.URL == "" {
		resolve := m.Resolvers[it.Source]
		if resolve == nil {
			return fmt.Errorf("unknown download source %q", it.Source)
		}
		got, err := resolve(ctx, &it)
		if err != nil {
			return err
		}
		if got == nil || got.URL == "" {
			return errors.New("no stream found for this episode")
		}
		res = got
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	title, year := it.Title, 0
	if m.Lookup != nil {
		if media, err := m.Lookup(ctx, it.MediaID); err == nil && media != nil {
			title, year = util.FirstNonEmpty(media.PreferredTitle(), title), media.Year()
		}
	}
	dir, name, err := m.destination(cfg, &it, title, year)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The version (sub/dub) of extension streams is in the name; the
	// streams of other providers have none.
	if it.Mode != "" {
		name = util.SanitizeFilename(fmt.Sprintf("%s [%s]", name, strings.ToUpper(it.Mode)))
	}
	ext := "." + container(res)
	out := filepath.Join(dir, name+ext)

	// Download into a hidden folder next to the destination (the library
	// scanner skips it) and move only finished files into place: a failed or
	// canceled download leaves nothing behind (half-written video, yt-dlp
	// .part/.ytdl/fragment files, subtitles without their video).
	work := filepath.Join(dir, ".kumobox-download-"+it.ID)
	_ = os.RemoveAll(work) // left over by a crash
	if err := os.Mkdir(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	m.update(r, func(it *Item) { it.Status, it.Output = StatusDownloading, out })

	var subs []string
	if res.SubURL != "" {
		ext := strings.ToLower(filepath.Ext(strings.Split(res.SubURL, "?")[0]))
		if ext == "" || len(ext) > 5 {
			ext = ".vtt"
		}
		headers := map[string]string{}
		if res.Referrer != "" {
			headers["Referer"] = res.Referrer
		}
		if b, err := util.GetBytes(ctx, res.SubURL, headers); err == nil {
			sub := name + ".eng" + ext
			if os.WriteFile(filepath.Join(work, sub), b, 0o644) == nil {
				subs = append(subs, sub)
			}
		}
	}

	tmp := filepath.Join(work, name+ext)
	err = m.fetch(r, cfg, res, tmp)
	if ctx.Err() != nil {
		return errors.New("canceled")
	}
	if err != nil {
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return errors.New("the download finished without writing a video file")
	}
	if c := containerOf(tmp); c != "" && "."+c != ext {
		// Saved as it came, and it isn't what the link said it was.
		out = filepath.Join(dir, name+"."+c)
		m.update(r, func(it *Item) { it.Output = out })
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	for _, sub := range subs {
		_ = os.Rename(filepath.Join(work, sub), filepath.Join(dir, sub))
	}

	// Register the file as matched (the movie or show and the episode are
	// known: no search needed) so it appears instantly.
	f := &library.LocalFile{
		Path: out, Dir: dir, Name: filepath.Base(out), Size: st.Size(), ModTime: st.ModTime().Unix(),
		Parsed: library.Parse(out, cfg.LibraryDirs()), MediaID: it.MediaID, Episode: it.Episode,
		Kind: library.KindMain, Locked: true, MatchScore: 1,
	}
	if catalog.IsShow(it.MediaID) && it.Episode > 0 && it.Episode < 1000 {
		f.Kind = library.KindSpecial
	}
	if err := m.files.Save(f); err != nil {
		log.Printf("register download: %v", err)
	}
	m.hub.Publish(events.LibraryUpdated, nil)
	return nil
}

// download fetches res into out by what its link answers with (see
// fetch.go): a video file in Go, an HLS stream with ffmpeg through the
// stream relay, a web page with yt-dlp.
func (m *Manager) download(r *run, cfg config.Settings, res *Resolved, out string) error {
	if res.Page {
		return m.page(r, cfg, res, out)
	}
	headers := requestHeaders(res)
	a, err := open(r.ctx, res.URL, headers, 0, "")
	if err != nil {
		return fmt.Errorf("couldn't reach the provider's link: %w", err)
	}
	if a.resp.StatusCode >= 300 {
		a.close()
		return fmt.Errorf("the provider's link answered %s", a.resp.Status)
	}
	br := bufio.NewReaderSize(a.body, 64<<10)
	head, _ := br.Peek(512)
	a.body = br
	switch stream.LinkKind(a.resp.Header.Get("Content-Type"), head) {
	case stream.LinkHLS:
		a.close()
		return m.ffmpeg(r, cfg, res, out)
	case stream.LinkPage:
		a.close()
		return m.page(r, cfg, res, out)
	case stream.LinkManifest:
		a.close()
		return errors.New("the provider's link is a DASH (or another kind of) manifest, which can't be downloaded")
	}
	return m.file(r, res, headers, a, out)
}

// page downloads the video of a web page with yt-dlp (unless the settings
// say ffmpeg only).
func (m *Manager) page(r *run, cfg config.Settings, res *Resolved, out string) error {
	if _, ok := util.LookPath("yt-dlp"); !ok {
		return fmt.Errorf("this link is a web page, not a video: yt-dlp downloads the videos of web pages (%s)", util.InstallHint("yt-dlp"))
	}
	if cfg.Downloads.Downloader == "ffmpeg" {
		return errors.New("this link is a web page, not a video, and downloads are set not to use yt-dlp")
	}
	if err := checkPublicPage(r.ctx, res.URL); err != nil {
		return err
	}
	return m.ytdlp(r, res, out)
}

// label names a download: the movie's title, or the show's with the
// episode (Show - S01E05).
func label(it *Item) string {
	if catalog.IsShow(it.MediaID) && it.Episode > 0 {
		return it.Title + " - " + catalog.FormatKey(it.Episode)
	}
	return it.Title
}

// titleWithYear is "Title (Year)", the way Plex, Jellyfin and the library
// scanner like movie and show folders named.
func titleWithYear(title string, year int) string {
	title = strings.TrimSpace(title)
	if year > 0 && !strings.HasSuffix(title, fmt.Sprintf("(%d)", year)) {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

// destination is the folder a finished download goes to and its file name
// (without the extension), named the way Plex, Jellyfin and the library
// scanner read them:
//
//	movies: <Title> (<Year>)/<Title> (<Year>)
//	shows:  <Title> (<Year>)/Season 02/<Title> - S02E05  (specials: Specials/)
//
// in the folder that already holds the title's files when there is one.
func (m *Manager) destination(cfg config.Settings, it *Item, title string, year int) (dir, name string, err error) {
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("Title %d", it.MediaID)
	}
	full := titleWithYear(title, year)
	dir, err = m.folderFor(cfg, it.MediaID, full)
	if err != nil {
		return "", "", err
	}
	if !catalog.IsShow(it.MediaID) || it.Episode <= 0 {
		return dir, util.SanitizeFilename(full), nil
	}
	season, _ := catalog.SplitKey(it.Episode)
	seasonDir := fmt.Sprintf("Season %02d", season)
	if season == 0 {
		seasonDir = "Specials"
	}
	return filepath.Join(dir, seasonDir), util.SanitizeFilename(strings.TrimSpace(title) + " - " + catalog.FormatKey(it.Episode)), nil
}

// folderFor is the folder of a movie or show downloads go to: the one that
// already holds its files (its "Title (Year)" folder, when it's inside the
// download/library folder), otherwise a new one named folderName — never
// loose in the library folder itself.
func (m *Manager) folderFor(cfg config.Settings, mediaID int, folderName string) (string, error) {
	baseDir := config.ExpandHome(util.FirstNonEmpty(cfg.Downloads.Dir, cfg.Library.Dir))
	if baseDir == "" {
		return "", errors.New("no download directory configured")
	}
	baseDir = filepath.Clean(baseDir)
	if mediaID > 0 && m.files != nil {
		if existing, _ := m.files.ByMedia(mediaID); len(existing) > 0 {
			counts := map[string]int{}
			best := ""
			for _, f := range existing {
				// The movie's or show's folder, else the file's (a season
				// folder is a show's: its parent is the show's).
				d := filepath.Clean(util.FirstNonEmpty(f.Parsed.Folder, f.Dir))
				if f.Parsed.Folder == "" && !release.NamesTitle(filepath.Base(d)) && filepath.Dir(d) != baseDir {
					d = filepath.Dir(d)
				}
				// Only a folder of its own below the base folder.
				if d == baseDir || !util.IsSubPath(baseDir, d) || strings.HasPrefix(filepath.Base(d), ".") {
					continue
				}
				counts[d]++
				if counts[d] > counts[best] {
					best = d
				}
			}
			if best != "" {
				if st, err := os.Stat(best); err == nil && st.IsDir() {
					return best, nil
				}
			}
		}
	}
	name := strings.TrimSpace(folderName)
	if name == "" {
		name = fmt.Sprintf("Title %d", mediaID)
	}
	return filepath.Join(baseDir, util.SanitizeFilename(name)), nil
}

var reYtdlp = regexp.MustCompile(`MOVIKO\|\s*([\d.]+)%\|([^|]*)\|(.*)$`)

// ytdlp downloads the video of a web page (checkPublicPage first).
func (m *Manager) ytdlp(r *run, res *Resolved, out string) error {
	args := []string{
		"--newline", "--no-colors", "--no-playlist", "--no-mtime",
		"-N", "8", "--fragment-retries", "20", "--retries", "10",
		"--progress-template", "download:MOVIKO|%(progress._percent_str)s|%(progress._speed_str)s|%(progress._eta_str)s",
		"--merge-output-format", strings.TrimPrefix(filepath.Ext(out), "."),
		"-o", strings.ReplaceAll(out, "%", "%%"),
	}
	if res.Referrer != "" {
		args = append(args, "--referer", res.Referrer)
	}
	for k, v := range res.Headers {
		if strings.EqualFold(k, "referer") && res.Referrer != "" {
			continue
		}
		args = append(args, "--add-header", k+":"+v)
	}
	// After "--", a link is never read as one of yt-dlp's options.
	args = append(args, "--", res.URL)
	cmd := exec.CommandContext(r.ctx, "yt-dlp", args...)
	// An interrupt lets yt-dlp stop the ffmpeg it may have started; it gets
	// killed if it's still running 10s later. Windows has no interrupt to
	// send: yt-dlp and the ffmpeg it started are ended at once there.
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return util.KillGroup(cmd.Process)
		}
		return nil
	}
	cmd.WaitDelay = 10 * time.Second
	progress := m.progress(r)
	stdout := &lineWriter{fn: func(line string) {
		mm := reYtdlp.FindStringSubmatch(line)
		if mm == nil {
			return
		}
		p, _ := strconv.ParseFloat(mm[1], 64)
		progress(func(it *Item) {
			it.Progress = p / 100
			it.Speed = strings.TrimSpace(mm[2])
			it.ETA = strings.TrimSpace(mm[3])
		})
	}}
	var lastErr string
	stderr := &lineWriter{fn: func(line string) {
		if strings.Contains(line, "ERROR") {
			lastErr = strings.TrimSpace(line)
		}
	}}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	// Run returns once both writers have consumed all the output, so lastErr
	// is final (and no longer written) from here on.
	stdout.flush()
	stderr.flush()
	if err != nil {
		if lastErr != "" {
			return errors.New(lastErr)
		}
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return nil
}

// ffmpeg downloads an HLS stream: ffmpeg reads it from the stream relay,
// which fetches its playlists (rewritten so that everything they name
// comes through the relay too), segments and keys with the stream's
// headers, never from this computer or the LAN. Of a master playlist, it
// gets the best variant.
func (m *Manager) ffmpeg(r *run, cfg config.Settings, res *Resolved, out string) error {
	ctx := r.ctx
	ffmpegPath, ffprobePath := cfg.Transcode.FfmpegPath, cfg.Transcode.FfprobePath
	if _, ok := util.LookPath(ffmpegPath); !ok {
		return fmt.Errorf("ffmpeg downloads HLS streams, and it isn't installed (%s)", util.InstallHint("ffmpeg"))
	}
	input, inputArgs, err := stream.RelayHLS(ctx, res.URL, requestHeaders(res), ffmpegPath)
	if err != nil {
		return err
	}

	// How long the episode is, for a percentage: from its HLS playlist,
	// else from ffprobe.
	duration := hlsLength(ctx, res)
	if p, ok := util.LookPath(ffprobePath); ok && duration <= 0 {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		args := append([]string(nil), inputArgs...)
		args = append(args, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", input)
		if b, err := exec.CommandContext(pctx, p, args...).Output(); err == nil {
			duration, _ = strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		}
		cancel()
	}

	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1"}
	args = append(args, inputArgs...)
	// The streams are copied as they are into the file's container. The MP4
	// muxer fixes the AAC audio of HLS streams by itself (forcing
	// aac_adtstoasc would refuse any other audio: Opus, AC-3, MP3...).
	args = append(args, "-i", input, "-map", "0:v?", "-map", "0:a?", "-c", "copy")
	if strings.EqualFold(filepath.Ext(out), ".mp4") {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, out)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.WaitDelay = 10 * time.Second
	progress := m.progress(r)
	// Times faster than real time, from ffmpeg's "speed=12.5x".
	var speed float64
	stdout := &lineWriter{fn: func(line string) {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return
		}
		switch k {
		case "out_time_us", "out_time_ms": // both in microseconds
			us, err := strconv.ParseFloat(v, 64)
			if err != nil || us < 0 {
				return
			}
			done := us / 1e6
			progress(func(it *Item) {
				if duration > 0 {
					it.Progress = min(done/duration, 0.99)
					if speed > 0 {
						it.ETA = clock(max(duration-done, 0) / speed)
					}
				} else {
					it.Done = clock(done)
				}
			})
		case "speed":
			v = strings.TrimSpace(v)
			speed, _ = strconv.ParseFloat(strings.TrimSuffix(v, "x"), 64)
			progress(func(it *Item) { it.Speed = v })
		}
	}}
	var lastErr string
	stderr := &lineWriter{fn: func(line string) {
		if line = strings.TrimSpace(line); line != "" {
			lastErr = line
		}
	}}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	// Run returns once both writers have consumed all the output.
	stdout.flush()
	stderr.flush()
	if err != nil {
		if lastErr == "" {
			lastErr = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", lastErr)
	}
	return nil
}

// maxLine caps the length of a line kept by lineWriter.
const maxLine = 64 << 10

// lineWriter hands every non-empty line written to it to fn, without the
// line break (\r ends a line too). It accepts everything, so a command's
// output pipe can never fill up and block it; lines over maxLine are cut.
type lineWriter struct {
	fn   func(line string)
	buf  []byte
	long bool // the line in buf was cut: drop the rest of it
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		w.flush()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) add(b []byte) {
	if w.long {
		return
	}
	if room := maxLine - len(w.buf); len(b) > room {
		b, w.long = b[:room], true
	}
	w.buf = append(w.buf, b...)
}

// flush ends the current line (also used for a last line without a break).
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
	}
	w.buf, w.long = w.buf[:0], false
}
