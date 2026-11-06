// Package library is the music side's library: the songs in its folders,
// with their tags (read in Go: MP3, FLAC, Ogg, Opus, M4A…) and lengths
// (ffprobe's, read after, as it has time). It looks at the folders again
// every so often, after an upload and when asked, and only reads the files
// that changed. Version changes with the songs: Utako asks for them again
// when it does.
package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"

	"crypto/sha1"
	"encoding/hex"
	"github.com/v0-0x/mediaserver/music/internal/peer"
)

// Track is a song, as the devices and the dashboard get it.
type Track struct {
	// Path is the file's full path on the server: what the devices stream
	// it by.
	Path        string `json:"path"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	TrackNo     int    `json:"trackNo,omitempty"`
	DiscNo      int    `json:"discNo,omitempty"`
	Year        int    `json:"year,omitempty"`
	DurationMs  int64  `json:"durationMs"`
	AddedAt     int64  `json:"addedAt"`
	// HasArt: a cover (in the file, or a cover.jpg by it) at
	// /api/peer/music/art.
	HasArt bool `json:"hasArt,omitempty"`
}

// entry is a song as kept: with what tells it changed.
type entry struct {
	Track
	Size    int64 `json:"size"`
	ModTime int64 `json:"mod"`
	// Probed: ffprobe was asked for its length (once per change of the
	// file, even when it couldn't say).
	Probed bool `json:"probed,omitempty"`
	// Guessed: its tags couldn't be read here (its title is its file's
	// name): ffprobe's are taken.
	Guessed bool `json:"guessed,omitempty"`
	// Embedded: its cover is in the file (else HasArt is a cover image by
	// it, looked for at each scan: one may come after the song).
	Embedded bool `json:"embedded,omitempty"`
}

// Exts are the files taken as songs.
var Exts = map[string]string{
	".mp3": "audio/mpeg", ".flac": "audio/flac", ".ogg": "audio/ogg", ".oga": "audio/ogg",
	".opus": "audio/ogg", ".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac",
	".alac": "audio/mp4", ".wav": "audio/wav", ".aif": "audio/aiff", ".aiff": "audio/aiff",
	".ape": "audio/x-ape", ".wv": "audio/x-wavpack", ".wma": "audio/x-ms-wma", ".dsf": "audio/x-dsf",
	".mka": "audio/x-matroska",
}

// IsSong reports a file taken as a song, by its name.
func IsSong(name string) bool {
	_, ok := Exts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// Library is the music side's songs.
type Library struct {
	file string // library.json

	mu       sync.RWMutex
	folders  []string
	songs    map[string]*entry
	sorted   []Track // nil: to sort again
	gen      int64
	scanning bool
	lastScan time.Time
	missing  map[string]bool // folders that weren't there at the last scan

	wake    chan struct{}
	probe   string // ffprobe, if there is one
	probing sync.Mutex
	// Changed runs after the songs changed (outside the lock).
	Changed func()

	// saveMu serializes writing the list down (outside mu: a write with its
	// sync takes a while on a slow disk, and the readers mustn't wait).
	saveMu     sync.Mutex
	writtenGen int64
	// The counts and the size (Counts, Size), kept per gen: the dashboard
	// asks every few seconds.
	statsGen             int64
	statsOK              bool
	nSongs, nAlbums, nAr int
	size                 int64
	// artDir keeps the covers once read out of the songs (Art): reading a
	// song's tags for its cover each time is a read of the drive each time.
	artDir string
}

// indexFormat is what library.json's songs hold: an older one's are read
// again from their files (at the next scan).
const indexFormat = 2

type saved struct {
	Format  int      `json:"format"`
	Gen     int64    `json:"gen"`
	Folders []string `json:"folders"`
	Songs   []*entry `json:"songs"`
}

// Open opens the library kept in dir.
func Open(dir string) (*Library, error) {
	l := &Library{file: filepath.Join(dir, "library.json"), artDir: filepath.Join(dir, "art"), songs: map[string]*entry{}, wake: make(chan struct{}, 1), missing: map[string]bool{}}
	l.probe, _ = exec.LookPath("ffprobe")
	raw, err := os.ReadFile(l.file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return l, nil
	case err != nil:
		return nil, err
	}
	var s saved
	if err := json.Unmarshal(raw, &s); err != nil {
		log.Printf("music: the library's list is unreadable (%v): the folders are read again", err)
		return l, nil
	}
	l.gen, l.folders = s.Gen, s.Folders
	for _, e := range s.Songs {
		if e != nil && e.Path != "" {
			if s.Format < indexFormat {
				e.ModTime = 0
			}
			l.songs[e.Path] = e
		}
	}
	return l, nil
}

// toSave is the library as written down; the caller holds l.mu (read is
// enough). Its own copy of the songs: written (writeSaved) outside the lock.
func (l *Library) toSave() saved {
	s := saved{Format: indexFormat, Gen: l.gen, Folders: append([]string{}, l.folders...), Songs: make([]*entry, 0, len(l.songs))}
	for _, e := range l.songs {
		cp := *e
		s.Songs = append(s.Songs, &cp)
	}
	sort.Slice(s.Songs, func(i, j int) bool { return s.Songs[i].Path < s.Songs[j].Path })
	return s
}

// writeSaved writes the library down, one write at a time, the newest last
// (a later gen is never written over by an earlier one still being written).
func (l *Library) writeSaved(s saved) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	if s.Gen < l.writtenGen {
		return nil
	}
	l.writtenGen = s.Gen
	return peer.WriteFile(l.file, raw, 0o600)
}

// Version changes whenever the songs do.
func (l *Library) Version() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return strconv.FormatInt(l.gen, 10)
}

// Tracks are the songs, by artist, album, disc and number.
func (l *Library) Tracks() []Track {
	_, t := l.Snapshot()
	return t
}

// Snapshot is the songs and their Version, as one. The list is sorted once
// per change, and shared: not to be changed by the caller.
func (l *Library) Snapshot() (string, []Track) {
	l.mu.RLock()
	if l.sorted != nil {
		gen, sorted := l.gen, l.sorted
		l.mu.RUnlock()
		return strconv.FormatInt(gen, 10), sorted
	}
	l.mu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	return strconv.FormatInt(l.gen, 10), l.sortedLocked()
}

func (l *Library) sortedLocked() []Track {
	if l.sorted == nil {
		out := make([]Track, 0, len(l.songs))
		for _, e := range l.songs {
			out = append(out, e.Track)
		}
		sort.Slice(out, func(i, j int) bool { return less(&out[i], &out[j]) })
		l.sorted = out
	}
	return l.sorted
}

func sortName(t *Track) string {
	if t.AlbumArtist != "" {
		return strings.ToLower(t.AlbumArtist)
	}
	return strings.ToLower(t.Artist)
}

func less(a, b *Track) bool {
	if x, y := sortName(a), sortName(b); x != y {
		return x < y
	}
	if x, y := strings.ToLower(a.Album), strings.ToLower(b.Album); x != y {
		return x < y
	}
	if a.DiscNo != b.DiscNo {
		return a.DiscNo < b.DiscNo
	}
	if a.TrackNo != b.TrackNo {
		return a.TrackNo < b.TrackNo
	}
	return a.Path < b.Path
}

// Song is the song at path, if it's in the library.
func (l *Library) Song(path string) (Track, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.songs[path]
	if !ok {
		return Track{}, false
	}
	return e.Track, true
}

// Counts are how many songs, albums and artists there are (counted once
// per change: the dashboard asks every few seconds).
func (l *Library) Counts() (songs, albums, artists int) {
	l.mu.RLock()
	if l.statsOK && l.statsGen == l.gen {
		defer l.mu.RUnlock()
		return l.nSongs, l.nAlbums, l.nAr
	}
	l.mu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.countLocked()
	return l.nSongs, l.nAlbums, l.nAr
}

// countLocked counts the songs, albums and artists, and adds up the size.
// Must be called with mu held for writing.
func (l *Library) countLocked() {
	if l.statsOK && l.statsGen == l.gen {
		return
	}
	al, ar := map[string]bool{}, map[string]bool{}
	var size int64
	for _, e := range l.songs {
		name := sortName(&e.Track)
		if name != "" {
			ar[name] = true
		}
		if e.Album != "" {
			al[name+"\x00"+strings.ToLower(e.Album)] = true
		}
		size += e.Size
	}
	l.nSongs, l.nAlbums, l.nAr, l.size = len(l.songs), len(al), len(ar), size
	l.statsGen, l.statsOK = l.gen, true
}

// Folder is a library folder, with how many songs it has.
type Folder struct {
	Path    string `json:"path"`
	Songs   int    `json:"songs"`
	Missing bool   `json:"missing,omitempty"`
}

// Folders are the library's folders.
func (l *Library) Folders() []Folder {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Folder, 0, len(l.folders))
	for _, f := range l.folders {
		n := 0
		for p := range l.songs {
			if within(p, f) {
				n++
			}
		}
		out = append(out, Folder{Path: f, Songs: n, Missing: l.missing[f]})
	}
	return out
}

// Dirs are the library's folders' paths.
func (l *Library) Dirs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string{}, l.folders...)
}

// within reports a path in folder dir (or dir itself).
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// AddFolder adds a folder to the library, and reads it.
func (l *Library) AddFolder(dir string) error {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%q isn't a full path", dir)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s isn't a folder on the server", dir)
	}
	l.mu.Lock()
	for _, f := range l.folders {
		switch {
		case f == dir:
			l.mu.Unlock()
			return fmt.Errorf("%s is in the library already", dir)
		case within(dir, f):
			l.mu.Unlock()
			return fmt.Errorf("%s is in %s, which is in the library already", dir, f)
		case within(f, dir):
			l.mu.Unlock()
			return fmt.Errorf("%s holds %s, which is in the library already: remove that one first", dir, f)
		}
	}
	l.folders = append(l.folders, dir)
	snap := l.toSave()
	l.mu.Unlock()
	err = l.writeSaved(snap)
	l.ScanSoon()
	return err
}

// RemoveFolder takes a folder (and its songs) out of the library. Its
// files stay where they are.
func (l *Library) RemoveFolder(dir string) error {
	l.mu.Lock()
	i := -1
	for j, f := range l.folders {
		if f == dir {
			i = j
		}
	}
	if i < 0 {
		l.mu.Unlock()
		return fmt.Errorf("%s isn't in the library", dir)
	}
	l.folders = append(l.folders[:i:i], l.folders[i+1:]...)
	delete(l.missing, dir)
	for p := range l.songs {
		if within(p, dir) && !l.inFolders(p) {
			delete(l.songs, p)
		}
	}
	l.gen++
	l.sorted = nil
	snap := l.toSave()
	l.mu.Unlock()
	err := l.writeSaved(snap)
	l.changed()
	return err
}

func (l *Library) inFolders(path string) bool {
	for _, f := range l.folders {
		if within(path, f) {
			return true
		}
	}
	return false
}

func (l *Library) changed() {
	if l.Changed != nil {
		l.Changed()
	}
}

// Scanning reports a scan under way, and when the last one ended.
func (l *Library) Scanning() (bool, time.Time) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.scanning, l.lastScan
}

// ScanSoon has the library look at its folders again (now, or once the
// scan under way is over).
func (l *Library) ScanSoon() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Every is how often the folders are looked at, besides when asked.
const Every = 15 * time.Minute

// Run looks at the folders at once, then every so often and when asked,
// until ctx ends.
func (l *Library) Run(ctx context.Context) {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		l.Scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-l.wake:
		}
	}
}

// Scan looks at the folders: songs new or changed are read, songs gone are
// dropped. A folder that isn't there (a drive not mounted) keeps its songs.
func (l *Library) Scan(ctx context.Context) {
	l.mu.Lock()
	if l.scanning {
		l.mu.Unlock()
		return
	}
	l.scanning = true
	folders := append([]string{}, l.folders...)
	known := make(map[string]entry, len(l.songs))
	for p, e := range l.songs {
		known[p] = *e
	}
	l.mu.Unlock()

	found := map[string]*entry{}
	missing := map[string]bool{}
	// The folders with a cover image, looked at once per scan.
	covers := map[string]bool{}
	cover := func(dir string) bool {
		has, ok := covers[dir]
		if !ok {
			has = folderCover(dir) != ""
			covers[dir] = has
		}
		return has
	}
	for _, dir := range folders {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			missing[dir] = true
			for p, e := range known {
				if within(p, dir) {
					e := e
					found[p] = &e
				}
			}
			continue
		}
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			name := d.Name()
			if p != dir && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() || !IsSong(name) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if e, ok := known[p]; ok && e.Size == info.Size() && e.ModTime == info.ModTime().UnixNano() {
				e.HasArt = e.Embedded || cover(filepath.Dir(p))
				found[p] = &e
				return nil
			}
			e := read(p, info, cover)
			if old, ok := known[p]; ok {
				e.AddedAt = old.AddedAt
			}
			found[p] = e
			return nil
		})
	}
	if ctx.Err() != nil {
		l.mu.Lock()
		l.scanning = false
		l.mu.Unlock()
		return
	}

	l.mu.Lock()
	// Folders removed meanwhile: their songs aren't kept.
	for p := range found {
		if !l.inFolders(p) {
			delete(found, p)
		}
	}
	diff := len(found) != len(l.songs)
	for p, e := range found {
		old, ok := l.songs[p]
		// What ffprobe said meanwhile.
		if ok && old.Size == e.Size && old.ModTime == e.ModTime && old.Probed && !e.Probed {
			hasArt := e.HasArt
			*e = *old
			e.HasArt = hasArt
		}
		if !ok || old.Track != e.Track {
			diff = true
		}
	}
	l.songs = found
	l.missing = missing
	if diff {
		l.gen++
		l.sorted = nil
	}
	snap := l.toSave()
	l.scanning = false
	l.lastScan = time.Now()
	n := len(l.songs)
	l.mu.Unlock()
	if err := l.writeSaved(snap); err != nil {
		log.Printf("music: couldn't keep the library's list: %v", err)
	}
	if diff {
		log.Printf("music: %d in the library", n)
		l.changed()
	}
	go l.probeLengths(ctx)
}

// read reads a song's tags; cover tells whether a folder has a cover image.
func read(p string, info fs.FileInfo, cover func(dir string) bool) *entry {
	e := &entry{Track: Track{Path: p}, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	e.AddedAt = min(info.ModTime().Unix(), time.Now().Unix())
	if f, err := os.Open(p); err == nil {
		if m, err := tag.ReadFrom(f); err == nil {
			e.Title = clean(m.Title())
			e.Artist = clean(m.Artist())
			e.Album = clean(m.Album())
			e.AlbumArtist = clean(m.AlbumArtist())
			e.TrackNo, _ = m.Track()
			e.DiscNo, _ = m.Disc()
			e.Year = m.Year()
			e.Embedded = m.Picture() != nil && len(m.Picture().Data) > 0
		}
		f.Close()
	}
	e.HasArt = e.Embedded || cover(filepath.Dir(p))
	e.Guessed = e.Title == ""
	guess(&e.Track)
	return e
}

func clean(s string) string {
	return strings.TrimSpace(strings.TrimRight(s, "\x00"))
}

var leadingNumber = regexp.MustCompile(`^(\d{1,3})\s*[-._)]?\s+`)

// guess fills a title (and an artist, and a number) from the file's name
// when the tags don't say: "01 - Artist - Song.flac".
func guess(t *Track) {
	if t.Title != "" {
		return
	}
	name := strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
	if m := leadingNumber.FindStringSubmatch(name); m != nil {
		if t.TrackNo == 0 {
			t.TrackNo, _ = strconv.Atoi(m[1])
		}
		name = name[len(m[0]):]
	}
	if a, b, ok := strings.Cut(name, " - "); ok && t.Artist == "" {
		t.Artist, name = strings.TrimSpace(a), strings.TrimSpace(b)
	}
	t.Title = strings.TrimSpace(name)
}

// folderCover is the cover image next to a song (cover.jpg, folder.png…),
// or "".
func folderCover(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, rank := "", 99
	for _, d := range ents {
		if d.IsDir() {
			continue
		}
		name := strings.ToLower(d.Name())
		ext := filepath.Ext(name)
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
			continue
		}
		for i, want := range []string{"cover", "folder", "front", "album"} {
			if strings.TrimSuffix(name, ext) == want && i < rank {
				best, rank = filepath.Join(dir, d.Name()), i
			}
		}
	}
	return best
}

// Art is the cover of the song at path: the picture in the file, or the
// cover image by it. Kept once read (artDir), by the song as it was: a
// song changed is read again.
func (l *Library) Art(path string) ([]byte, string, error) {
	l.mu.RLock()
	e, ok := l.songs[path]
	var key string
	if ok {
		key = artKey(path, e.Size, e.ModTime)
	}
	l.mu.RUnlock()
	if !ok {
		return nil, "", fs.ErrNotExist
	}
	if l.artDir != "" {
		if data, mime, err := readArt(l.artDir, key); err == nil {
			if mime == "" {
				return nil, "", fs.ErrNotExist // known to have none
			}
			return data, mime, nil
		}
	}
	data, mime, err := l.readArt(path)
	if l.artDir != "" {
		if err == nil {
			keepArt(l.artDir, key, data, mime)
		} else if errors.Is(err, fs.ErrNotExist) {
			keepArt(l.artDir, key, nil, "")
		}
	}
	return data, mime, err
}

func artKey(path string, size, mod int64) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, size, mod)))
	return hex.EncodeToString(h[:])
}

// readArt is a kept cover: its data and type ("" with no error: the song
// has none, as found before).
func readArt(dir, key string) ([]byte, string, error) {
	mime, err := os.ReadFile(filepath.Join(dir, key+".type"))
	if err != nil {
		return nil, "", err
	}
	if len(mime) == 0 {
		return nil, "", nil
	}
	data, err := os.ReadFile(filepath.Join(dir, key))
	if err != nil {
		return nil, "", err
	}
	return data, string(mime), nil
}

// keepArt keeps a cover (or that there's none: mime ""), best effort.
func keepArt(dir, key string, data []byte, mime string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if mime != "" {
		if err := os.WriteFile(filepath.Join(dir, key), data, 0o600); err != nil {
			return
		}
	}
	_ = os.WriteFile(filepath.Join(dir, key+".type"), []byte(mime), 0o600)
}

// readArt reads a song's cover: the picture in the file, or the cover image
// by it.
func (l *Library) readArt(path string) ([]byte, string, error) {
	if f, err := os.Open(path); err == nil {
		m, err := tag.ReadFrom(f)
		f.Close()
		if err == nil && m.Picture() != nil && len(m.Picture().Data) > 0 {
			pic := m.Picture()
			mime := pic.MIMEType
			if !strings.HasPrefix(mime, "image/") {
				mime = sniffImage(pic.Data)
			}
			return pic.Data, mime, nil
		}
	}
	if c := folderCover(filepath.Dir(path)); c != "" {
		data, err := os.ReadFile(c)
		if err != nil {
			return nil, "", err
		}
		return data, sniffImage(data), nil
	}
	return nil, "", fs.ErrNotExist
}

func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		return "image/png"
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// probeLengths asks ffprobe the length of the songs that don't have one,
// a few at a time, keeping what it learns now and then.
func (l *Library) probeLengths(ctx context.Context) {
	if l.probe == "" || !l.probing.TryLock() {
		return
	}
	defer l.probing.Unlock()
	l.mu.RLock()
	var todo []string
	for p, e := range l.songs {
		if !e.Probed {
			todo = append(todo, p)
		}
	}
	l.mu.RUnlock()
	if len(todo) == 0 {
		return
	}
	sort.Strings(todo)
	const workers = 2
	jobs := make(chan string)
	type result struct {
		path string
		ms   int64
		tags map[string]string
	}
	results := make(chan result)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				ms, tags := probe(ctx, l.probe, p)
				results <- result{p, ms, tags}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, p := range todo {
			select {
			case jobs <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	n, last := 0, time.Now()
	flush := func() {
		var snap saved
		l.mu.Lock()
		if n > 0 {
			l.gen++
			l.sorted = nil
			snap = l.toSave()
		}
		l.mu.Unlock()
		if n > 0 {
			if err := l.writeSaved(snap); err != nil {
				log.Printf("music: couldn't keep the library's list: %v", err)
			}
			l.changed()
		}
		n, last = 0, time.Now()
	}
	for r := range results {
		l.mu.Lock()
		if e, ok := l.songs[r.path]; ok && !e.Probed {
			e.Probed = true
			e.DurationMs = r.ms
			// Tags Go couldn't read (WavPack, APE, AIFF…), from ffprobe.
			if e.Guessed && fill(&e.Track, r.tags) {
				e.Guessed = false
			}
			n++
		}
		l.mu.Unlock()
		if time.Since(last) > 20*time.Second {
			flush()
		}
	}
	flush()
}

// fill takes the tags ffprobe read, for a song whose own couldn't be read
// here; false when it read no title either.
func fill(t *Track, tags map[string]string) bool {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := clean(tags[k]); v != "" {
				return v
			}
		}
		return ""
	}
	title := get("title")
	if title == "" {
		return false
	}
	t.Title = title
	for dst, keys := range map[*string][]string{&t.Artist: {"artist"}, &t.Album: {"album"}, &t.AlbumArtist: {"album_artist", "albumartist", "album artist"}} {
		if v := get(keys...); v != "" {
			*dst = v
		}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(strings.Split(tags["track"], "/")[0])); err == nil {
		t.TrackNo = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(strings.Split(tags["disc"], "/")[0])); err == nil {
		t.DiscNo = n
	}
	return true
}

// probe is a song's length by ffprobe, and the tags it reads.
func probe(ctx context.Context, ffprobe, path string) (int64, map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration:format_tags", "-of", "json", "--", path).Output()
	if err != nil {
		return 0, nil
	}
	var r struct {
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &r) != nil {
		return 0, nil
	}
	secs, _ := strconv.ParseFloat(r.Format.Duration, 64)
	tags := map[string]string{}
	for k, v := range r.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	return int64(secs * 1000), tags
}

// CanProbe reports whether songs get their lengths (ffprobe is there).
func (l *Library) CanProbe() bool { return l.probe != "" }

// Size is how many bytes the songs take.
func (l *Library) Size() int64 {
	l.mu.RLock()
	if l.statsOK && l.statsGen == l.gen {
		defer l.mu.RUnlock()
		return l.size
	}
	l.mu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.countLocked()
	return l.size
}
