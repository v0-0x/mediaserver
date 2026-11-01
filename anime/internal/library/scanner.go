package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/events"
)

type Scanner struct {
	Store    *Store
	platform *anilist.Platform
	settings *config.Store
	hub      *events.Hub
	// OnScanned runs after each scan that went through (in the background).
	OnScanned func()
	// OnNewFiles gets the files a scan found that weren't in the library
	// before (not on the first scan of a library), matched or not.
	OnNewFiles func([]*LocalFile)
	// newMatcher makes the scan's matcher (tests replace it).
	newMatcher func(threshold float64, outsideList bool) *Matcher

	mu      sync.Mutex
	running atomic.Bool
	rescan  atomic.Bool

	startMu   sync.Mutex // serializes StartWatcher (settings changes fire concurrently)
	watcherMu sync.Mutex
	watcher   *fsnotify.Watcher
	stopWatch chan struct{}
}

func NewScanner(store *Store, p *anilist.Platform, s *config.Store, hub *events.Hub) *Scanner {
	sc := &Scanner{Store: store, platform: p, settings: s, hub: hub}
	sc.newMatcher = func(threshold float64, outsideList bool) *Matcher { return NewMatcher(p, threshold, outsideList) }
	return sc
}

// matcherVersion goes up when matching gets better: the files matched
// automatically before are matched again, once, at the next scan.
//
//	2: other seasons kept apart (Code Geass and R2), also when AniList
//	   can't be searched.
const (
	matcherVersion    = 2
	matcherVersionKey = "library:matcher-version"
)

type ScanOptions struct {
	// Re-match every unlocked file, not only new/unmatched ones.
	Full bool `json:"full"`
	// Skip the AniList matching step (just index files).
	SkipMatching bool `json:"skipMatching"`
}

type ScanResult struct {
	Total     int     `json:"total"`
	Added     int     `json:"added"`
	Removed   int     `json:"removed"`
	Matched   int     `json:"matched"`
	Unmatched int     `json:"unmatched"`
	Seconds   float64 `json:"seconds"`
}

type ScanProgress struct {
	Stage   string `json:"stage"` // walking | matching | saving
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

func (s *Scanner) Running() bool { return s.running.Load() }

var ErrScanRunning = errors.New("a library scan is already running")

// ScanDonePayload is sent with events.ScanDone.
type ScanDonePayload struct {
	*ScanResult
	Error string `json:"error,omitempty"`
}

// HasLibrary reports whether any configured library folder exists.
func (s *Scanner) HasLibrary() bool {
	for _, r := range s.settings.Get().LibraryDirs() {
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// Scan indexes the library directories and matches new files.
func (s *Scanner) Scan(ctx context.Context, opts ScanOptions) (res *ScanResult, err error) {
	if !s.mu.TryLock() {
		return nil, ErrScanRunning
	}
	defer func() {
		// Always tell the UI the scan is over, also when it failed.
		if err != nil {
			s.hub.Publish(events.ScanDone, ScanDonePayload{Error: err.Error()})
		}
		s.mu.Unlock()
		// Files changed while we were busy: go again.
		if s.rescan.Swap(false) {
			go s.autoScan()
		}
	}()
	s.running.Store(true)
	defer s.running.Store(false)

	start := time.Now()
	cfg := s.settings.Get()
	roots := cfg.LibraryDirs()

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Message: "Looking for video files…"})
	found := map[string]fs.FileInfo{}
	var walked, failed []string // roots read successfully / folders that couldn't be read
	for _, root := range roots {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue // missing (e.g. an unplugged drive): keep its files as they are
		}
		base := len(found)
		w, err := walkLibrary(root, cfg.Library.IgnorePatterns, func(n int) {
			s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Done: base + n, Message: "Found " + itoa(base+n) + " files…"})
		})
		if err != nil {
			log.Printf("scan: can't read %s: %v", root, err)
			continue
		}
		walked = append(walked, root)
		failed = append(failed, w.failed...)
		for p, info := range w.files {
			found[p] = info
		}
		s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Done: len(found), Message: "Found " + itoa(len(found)) + " files…"})
	}
	if cfg.Library.Off {
		return nil, ErrLibraryOff
	}
	if len(walked) == 0 {
		return nil, errors.New("no library directory found — set one in Settings › Local library")
	}

	existing, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Done: len(found), Message: "Found " + itoa(len(found)) + " files, reading names…"})
	byPath := map[string]*LocalFile{}
	for _, f := range existing {
		byPath[f.Path] = f
	}

	// Matching got better since the files were matched: matched again,
	// once (those matched by hand stay).
	var version int
	_, _ = s.Store.db.GetKV(matcherVersionKey, &version)
	upgrade := version < matcherVersion && !opts.SkipMatching

	res = &ScanResult{Total: len(found)}
	var changed, added []*LocalFile
	var toMatch []*LocalFile
	for path, info := range found {
		old := byPath[path]
		if old != nil && old.Size == info.Size() && old.ModTime == info.ModTime().Unix() && !opts.Full {
			if (old.MediaID == 0 || upgrade) && !old.Locked && !old.Ignored {
				cp := *old
				toMatch = append(toMatch, &cp)
				changed = append(changed, &cp)
			}
			continue
		}
		f := &LocalFile{
			Path:    path,
			Dir:     filepath.Dir(path),
			Name:    filepath.Base(path),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
			Parsed:  Parse(path, roots),
		}
		f.Kind = f.Parsed.Kind
		f.Episode = max(f.Parsed.Episode, 0)
		f.AiredEpisode = f.Parsed.Episode
		if old != nil {
			f.Locked, f.Ignored = old.Locked, old.Ignored
			if old.Locked {
				f.MediaID, f.Episode, f.Kind, f.MatchScore = old.MediaID, old.Episode, old.Kind, old.MatchScore
			}
		} else {
			res.Added++
			added = append(added, f)
		}
		if !f.Locked && !f.Ignored {
			toMatch = append(toMatch, f)
		}
		changed = append(changed, f)
	}
	removed := s.removedFiles(byPath, found, roots, walked, failed)
	res.Removed = len(removed)

	if !opts.SkipMatching && len(toMatch) > 0 {
		m := s.newMatcher(cfg.Library.MatchThreshold, cfg.Library.MatchOutsideList)
		m.MatchFiles(ctx, toMatch, func(done, total int, title string) {
			s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "matching", Done: done, Total: total, Message: "Matching " + title})
		})
		if upgrade {
			// Matched again only to do better: a file AniList couldn't
			// confirm a match for keeps its old one, and the upgrade is
			// done again next scan.
			for _, f := range toMatch {
				if old := byPath[f.Path]; f.MediaID == 0 && old != nil && old.MediaID != 0 && old.Size == f.Size && old.ModTime == f.ModTime {
					f.MediaID, f.Episode, f.Kind, f.MatchScore = old.MediaID, old.Episode, old.Kind, old.MatchScore
				}
			}
			if m.SearchFailures() == 0 && ctx.Err() == nil {
				defer func() {
					if err == nil {
						_ = s.Store.db.SetKV(matcherVersionKey, matcherVersion)
					}
				}()
			}
		}
	} else if upgrade && len(toMatch) == 0 {
		_ = s.Store.db.SetKV(matcherVersionKey, matcherVersion)
	}

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "saving", Message: "Saving…"})
	// Matching can take minutes; don't overwrite what the user (or a
	// finished download) changed in the meantime.
	if err := s.Store.SaveScanned(changed, byPath); err != nil {
		return nil, err
	}
	if err := s.Store.Delete(removed...); err != nil {
		return nil, err
	}
	all, _ := s.Store.All()
	for _, f := range all {
		if f.Ignored {
			continue
		}
		if f.MediaID > 0 {
			res.Matched++
		} else {
			res.Unmatched++
		}
	}
	res.Seconds = time.Since(start).Seconds()
	s.hub.Publish(events.ScanDone, ScanDonePayload{ScanResult: res})
	s.hub.Publish(events.LibraryUpdated, nil)
	if s.OnScanned != nil {
		go s.OnScanned()
	}
	if s.OnNewFiles != nil && len(added) > 0 && len(byPath) > 0 {
		// A file that went from one place to another (a folder renamed) isn't
		// new: one gone with its name and size.
		moved := map[string]bool{}
		for _, p := range removed {
			if f := byPath[p]; f != nil {
				moved[fmt.Sprintf("%s|%d", f.Name, f.Size)] = true
			}
		}
		var fresh []*LocalFile
		for _, f := range added {
			if !moved[fmt.Sprintf("%s|%d", f.Name, f.Size)] {
				fresh = append(fresh, f)
			}
		}
		if len(fresh) > 0 {
			go s.OnNewFiles(fresh)
		}
	}
	return res, nil
}

// removedFiles picks the indexed files that are really gone. A file is only
// dropped when the folder it lives in was read successfully and the file
// itself no longer exists, so an unplugged drive, an unmounted share or an
// unreadable folder never wipes matches. Files outside the library folders
// (e.g. a separate download folder) are dropped only when their folder still
// exists but the file doesn't.
func (s *Scanner) removedFiles(byPath map[string]*LocalFile, found map[string]fs.FileInfo, roots, walked, failed []string) []string {
	// A root that is readable but suddenly empty while we know files in it
	// is most likely a mount point whose drive isn't mounted.
	foundIn := map[string]int{}
	knownIn := map[string]int{}
	for p := range found {
		for _, r := range walked {
			if underAny(p, []string{r}) {
				foundIn[r]++
			}
		}
	}
	for p := range byPath {
		for _, r := range walked {
			if underAny(p, []string{r}) {
				knownIn[r]++
			}
		}
	}
	var trusted []string
	for _, r := range walked {
		if foundIn[r] == 0 && knownIn[r] > 0 {
			log.Printf("scan: %s is empty but %d files were indexed there — is the drive mounted? Keeping them.", r, knownIn[r])
			s.hub.Error("Library folder " + r + " looks empty — is the drive connected? Aniko kept its " + itoa(knownIn[r]) + " files.")
			continue
		}
		trusted = append(trusted, r)
	}

	var removed []string
	for p := range byPath {
		if _, ok := found[p]; ok {
			continue
		}
		if underAny(p, failed) {
			continue
		}
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			continue // still there (e.g. written after the walk) or unknown
		}
		switch {
		case underAny(p, trusted):
			removed = append(removed, p)
		case underAny(p, roots):
			// in a missing or suspicious root: keep
		default:
			if st, err := os.Stat(filepath.Dir(p)); err == nil && st.IsDir() {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

// ErrLibraryOff: this computer's library is off (Settings › Local library).
var ErrLibraryOff = errors.New("this computer's library is off: it plays the ones your Aniko Hub and other computers share with it (Settings › Local library)")

// Forget drops from the index the files of folders that left the library
// (taken out of it, or the library turned off); they stay on the disk. A
// file also in a folder still in the library stays. Unlike a scan, this
// doesn't wait for the folder's drive: a library moved to an Aniko Hub with
// its drive is gone from here for good. It waits for a running scan.
func (s *Scanner) Forget(folders []string) {
	if len(folders) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	roots := s.settings.Get().LibraryDirs()
	all, err := s.Store.All()
	if err != nil {
		log.Printf("library: %v", err)
		return
	}
	var gone []string
	for _, f := range all {
		if underAny(f.Path, folders) && !underAny(f.Path, roots) {
			gone = append(gone, f.Path)
		}
	}
	if len(gone) == 0 {
		return
	}
	if err := s.Store.Delete(gone...); err != nil {
		log.Printf("library: %v", err)
		return
	}
	log.Printf("library: forgot the %d files of %s (no longer in the library; nothing was deleted)", len(gone), strings.Join(folders, ", "))
	s.hub.Publish(events.LibraryUpdated, nil)
}

// ForgetGone drops from the index every file that isn't on the disk, its
// drive gone or not: for a computer whose library is off, where nothing
// should be kept for a drive coming back (one that moved to an Aniko Hub,
// say). Files still there (a download kept here) stay.
func (s *Scanner) ForgetGone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Store.All()
	if err != nil {
		log.Printf("library: %v", err)
		return
	}
	var gone []string
	for _, f := range all {
		if _, err := os.Stat(f.Path); errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			gone = append(gone, f.Path)
		}
	}
	if len(gone) == 0 {
		return
	}
	if err := s.Store.Delete(gone...); err != nil {
		log.Printf("library: %v", err)
		return
	}
	log.Printf("library: forgot %d files that aren't on the disk (the library is off)", len(gone))
	s.hub.Publish(events.LibraryUpdated, nil)
}

// ScanSoon starts an incremental scan in the background, or queues one if a
// scan is already running.
func (s *Scanner) ScanSoon() { go s.autoScan() }

// autoScan runs an incremental scan for the folder watcher.
func (s *Scanner) autoScan() {
	if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil {
		if errors.Is(err, ErrScanRunning) {
			s.rescan.Store(true)
			return
		}
		log.Printf("auto scan: %v", err)
	}
}

func ignored(name string, patterns []string) bool {
	return ignorePattern(name, patterns) != ""
}

// ignorePattern returns the ignore pattern name matches, if any.
func ignorePattern(name string, patterns []string) string {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(strings.ToLower(p), lower); ok {
			return p
		}
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------------------
// File watcher (automatic library refresh)

// StartWatcher watches the library directories and triggers an incremental
// scan shortly after files are added, removed or renamed.
func (s *Scanner) StartWatcher() {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.StopWatcher()
	cfg := s.settings.Get()
	if !cfg.Library.AutoRefresh {
		return
	}
	var dirs []string
	for _, root := range cfg.LibraryDirs() {
		// Same walk as the scanner, so symlinked folders are watched too.
		if res, err := walkLibrary(root, cfg.Library.IgnorePatterns, nil); err == nil {
			dirs = append(dirs, res.dirs...)
		}
	}
	// macOS watches a folder by opening every file in it, and those count
	// against the files Aniko may have open, its network connections too: a
	// library bigger than that is checked for changes every few minutes
	// instead.
	polling := false
	if budget := watchBudget(); budget > 0 {
		if n, over := overBudget(dirs, budget); over {
			log.Printf("library watcher: the library has %d+ files, more than macOS lets Aniko watch (%d): it checks for new episodes every %v instead", n, budget, pollEvery)
			dirs, polling = nil, true
		}
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("library watcher: %v", err)
		return
	}
	for _, dir := range dirs {
		_ = w.Add(dir)
	}
	stop := make(chan struct{})
	s.watcherMu.Lock()
	s.watcher, s.stopWatch = w, stop
	s.watcherMu.Unlock()

	go func() {
		var timer *time.Timer
		trigger := make(chan struct{}, 1)
		var poll <-chan time.Time
		if polling {
			t := time.NewTicker(pollEvery)
			defer t.Stop()
			poll = t.C
		}
		for {
			select {
			case <-poll:
				go s.autoScan()
			case <-stop:
				_ = w.Close()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// Hidden folders (e.g. downloads in progress) are skipped,
				// like the scanner does.
				if strings.HasPrefix(filepath.Base(ev.Name), ".") {
					continue
				}
				if ev.Op&fsnotify.Create != 0 {
					if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
						_ = w.Add(ev.Name)
					}
				}
				if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Write) == 0 {
					continue
				}
				if !IsVideo(ev.Name) && filepath.Ext(ev.Name) != "" {
					continue
				}
				// Debounce: torrents/downloads write files for a while.
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(8*time.Second, func() {
					select {
					case trigger <- struct{}{}:
					default:
					}
				})
			case <-trigger:
				go s.autoScan()
			case err := <-w.Errors:
				if err != nil {
					log.Printf("library watcher: %v", err)
				}
			}
		}
	}()
}

// pollEvery is how often a library too big to watch is checked for changes.
var pollEvery = 10 * time.Minute

// overBudget counts the entries of dirs (and the dirs themselves), the
// files watching them takes on macOS, until there are more than budget.
func overBudget(dirs []string, budget int) (n int, over bool) {
	for _, dir := range dirs {
		n++
		if entries, err := os.ReadDir(dir); err == nil {
			n += len(entries)
		}
		if n > budget {
			return n, true
		}
	}
	return n, false
}

func (s *Scanner) StopWatcher() {
	s.watcherMu.Lock()
	defer s.watcherMu.Unlock()
	if s.stopWatch != nil {
		close(s.stopWatch)
		s.stopWatch = nil
		s.watcher = nil
	}
}
