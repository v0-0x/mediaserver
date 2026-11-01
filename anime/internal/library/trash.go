package library

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

// Trash holds the library files put away from Library tools › Clean up: in
// a hidden .aniko-trash folder on their own drive (at the top of the library
// folder they're in), so putting them there is instant and the scanner
// skips them. They can be put back until the trash is emptied, which frees
// the space, or TrashKeep after.
type Trash struct {
	mu    sync.Mutex
	db    *db.DB
	files *Store
	// roots: the library folders (a file's trash is at the top of its own).
	roots func() []string
}

const (
	trashDir  = ".aniko-trash"
	trashKey  = "library:trash"
	TrashKeep = 30 * 24 * time.Hour
)

// TrashItem is a file in the trash, and the subtitle files next to it that
// went with it.
type TrashItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // where it was
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MediaID   int    `json:"mediaId"`
	Episode   int    `json:"episode"`
	DeletedAt int64  `json:"deletedAt"`
	// Where it is now; the subtitle files (where they were: where they are).
	In    string            `json:"in"`
	Extra map[string]string `json:"extra,omitempty"`
	// The library's record of it (its match, whether that was set by hand),
	// put back with it.
	File *LocalFile `json:"file"`
}

func NewTrash(d *db.DB, files *Store, roots func() []string) *Trash {
	return &Trash{db: d, files: files, roots: roots}
}

func (t *Trash) load() []TrashItem {
	var items []TrashItem
	_, _ = t.db.GetKV(trashKey, &items)
	return items
}

func (t *Trash) save(items []TrashItem) error {
	return t.db.SetKV(trashKey, items)
}

// Items are the files in the trash, the newest first.
func (t *Trash) Items() []TrashItem {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := t.load()
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt > items[j].DeletedAt })
	return items
}

// Put moves library files to the trash. failed says why for those that
// couldn't be (not a library file, gone, in use).
func (t *Trash) Put(paths []string) (moved []string, failed map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	failed = map[string]string{}
	items := t.load()
	for _, p := range paths {
		it, err := t.put(filepath.Clean(p))
		if err != nil {
			failed[p] = err.Error()
			continue
		}
		items = append(items, *it)
		moved = append(moved, it.Path)
	}
	if len(moved) > 0 {
		_ = t.files.Delete(moved...)
		if err := t.save(items); err != nil {
			for _, p := range moved {
				failed[p] = "moved to the trash, but the list of it couldn't be saved: " + err.Error()
			}
		}
	}
	return moved, failed
}

// check: path is a library file that can go: a file (not a link), and not
// the same file as another of the library's reached another way (a library
// folder linked into another, a hard link), which would go with it.
func (t *Trash) check(path string) (*LocalFile, os.FileInfo, error) {
	f, err := t.files.Get(path)
	if err != nil || f == nil {
		return nil, nil, ErrNotLibraryFile
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("it isn't there any more")
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("not a file")
	}
	if f.MediaID > 0 {
		others, _ := t.files.ByMedia(f.MediaID)
		for _, o := range others {
			if o.Path != path && o.Episode == f.Episode && SameFile(o.Path, path) {
				return nil, nil, fmt.Errorf("it's the same file as %s, reached another way: removing it would take that one too", o.Name)
			}
		}
	}
	return f, st, nil
}

func (t *Trash) put(path string) (*TrashItem, error) {
	f, st, err := t.check(path)
	if err != nil {
		return nil, err
	}
	id := randomID()
	it := &TrashItem{ID: id, Path: path, Name: filepath.Base(path), Size: st.Size(), MediaID: f.MediaID, Episode: f.Episode, DeletedAt: time.Now().Unix(), File: f}
	// At the top of its library folder; in its own folder when that's on
	// another drive (a folder linked in from elsewhere).
	var dir string
	for _, d := range []string{t.rootOf(path), filepath.Dir(path)} {
		if d == "" {
			continue
		}
		dir = filepath.Join(d, trashDir)
		if err = os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		writeTrashNote(dir)
		it.In = filepath.Join(dir, id+"-"+it.Name)
		if err = os.Rename(path, it.In); err == nil {
			break
		}
		removeIfEmpty(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("couldn't move it: %w", err)
	}
	for _, from := range subtitlesOf(path) {
		to := filepath.Join(dir, id+"-"+filepath.Base(from))
		if os.Rename(from, to) == nil {
			if it.Extra == nil {
				it.Extra = map[string]string{}
			}
			it.Extra[from] = to
		}
	}
	return it, nil
}

// subtitlesOf: a video's subtitle files ("Show - 01.en.ass" for "Show -
// 01.mkv"), not another video's whose name starts the same ("Show -
// 01.v2.en.ass" is "Show - 01.v2.mkv"'s).
func subtitlesOf(path string) []string {
	name := filepath.Base(path)
	base := strings.TrimSuffix(name, filepath.Ext(name))
	entries, _ := os.ReadDir(filepath.Dir(path))
	var longer []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && n != name && IsVideo(n) {
			if b := strings.TrimSuffix(n, filepath.Ext(n)); strings.HasPrefix(b, base+".") {
				longer = append(longer, b)
			}
		}
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !SubtitleExtensions[strings.ToLower(filepath.Ext(n))] || !strings.HasPrefix(n, base+".") {
			continue
		}
		theirs := false
		for _, b := range longer {
			if strings.HasPrefix(n, b+".") {
				theirs = true
			}
		}
		if !theirs {
			out = append(out, filepath.Join(filepath.Dir(path), n))
		}
	}
	return out
}

// Removed is a library file deleted for good, with its subtitle files.
type Removed struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	Size      int64    `json:"size"`
	MediaID   int      `json:"mediaId"`
	Episode   int      `json:"episode"`
	Kind      string   `json:"kind"` // main | special | nc
	Subtitles []string `json:"subtitles,omitempty"`
}

// Delete deletes library files for good, with their subtitle files (a
// hub's dashboard: no trash; its audit log can download them again).
// failed says why for those that couldn't be.
func (t *Trash) Delete(paths []string) (removed []Removed, failed map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	failed = map[string]string{}
	var gone []string
	for _, given := range paths {
		p := filepath.Clean(given)
		f, st, err := t.check(p)
		if err != nil {
			failed[given] = err.Error()
			continue
		}
		subs := subtitlesOf(p)
		if err := os.Remove(p); err != nil {
			failed[given] = "couldn't delete it: " + err.Error()
			continue
		}
		for _, sub := range subs {
			_ = os.Remove(sub)
		}
		removed = append(removed, Removed{Path: p, Name: filepath.Base(p), Size: st.Size(), MediaID: f.MediaID, Episode: f.Episode, Kind: f.Kind, Subtitles: subs})
		gone = append(gone, p)
	}
	if len(gone) > 0 {
		_ = t.files.Delete(gone...)
	}
	return removed, failed
}

// Removed lists what's in the trash as removed: what emptying it all
// deletes.
func (it TrashItem) Removed() Removed {
	r := Removed{Path: it.Path, Name: it.Name, Size: it.Size, MediaID: it.MediaID, Episode: it.Episode}
	if it.File != nil {
		r.Kind = it.File.Kind
	}
	return r
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SameFile: a and b are one file on disk (a folder linked into another, a
// hard link). False when either can't be read.
func SameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}

// ErrNotLibraryFile: a path that isn't one of the library's files.
var ErrNotLibraryFile = errors.New("not a file of the library")

// rootOf is the library folder a file is in ("" for none).
func (t *Trash) rootOf(path string) string {
	best := ""
	for _, r := range t.roots() {
		if r == "" {
			continue
		}
		r = filepath.Clean(r)
		rel, err := filepath.Rel(r, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(r) > len(best) {
			best = r
		}
	}
	return best
}

// Restore puts files back where they were, with their match.
func (t *Trash) Restore(ids []string) (restored []string, failed map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	failed = map[string]string{}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var keep []TrashItem
	for _, it := range t.load() {
		if !want[it.ID] {
			keep = append(keep, it)
			continue
		}
		if _, err := os.Lstat(it.Path); err == nil {
			failed[it.ID] = "a file is where it was: " + it.Path
			keep = append(keep, it)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(it.Path), 0o755); err != nil {
			failed[it.ID] = err.Error()
			keep = append(keep, it)
			continue
		}
		if err := os.Rename(it.In, it.Path); err != nil {
			failed[it.ID] = "couldn't move it back: " + err.Error()
			keep = append(keep, it)
			continue
		}
		for from, to := range it.Extra {
			if _, err := os.Lstat(from); err != nil {
				_ = os.Rename(to, from)
			}
		}
		if it.File != nil {
			_ = t.files.Save(it.File)
		}
		removeIfEmpty(filepath.Dir(it.In))
		restored = append(restored, it.Path)
	}
	if len(restored) > 0 {
		_ = t.save(keep)
	}
	return restored, failed
}

// Empty deletes files in the trash for good (all of them for no ids): the
// space they took, freed.
func (t *Trash) Empty(ids []string) (freed int64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.empty(func(it TrashItem) bool {
		if ids == nil {
			return true
		}
		for _, id := range ids {
			if id == it.ID {
				return true
			}
		}
		return false
	})
}

// Purge empties what's been in the trash TrashKeep.
func (t *Trash) Purge(now time.Time) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.empty(func(it TrashItem) bool { return now.Sub(time.Unix(it.DeletedAt, 0)) > TrashKeep })
}

func (t *Trash) empty(match func(TrashItem) bool) (int64, error) {
	var keep []TrashItem
	var freed int64
	var firstErr error
	changed := false
	for _, it := range t.load() {
		if !match(it) {
			keep = append(keep, it)
			continue
		}
		err := os.Remove(it.In)
		if errors.Is(err, os.ErrNotExist) && !exists(filepath.Dir(filepath.Dir(it.In))) {
			// Not there because its drive isn't (unplugged): kept for when
			// it's back, rather than forgotten with the file still on it.
			if firstErr == nil {
				firstErr = fmt.Errorf("couldn't delete %s: the drive it's on isn't connected", it.Name)
			}
			keep = append(keep, it)
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = fmt.Errorf("couldn't delete %s: %w", it.Name, err)
			}
			keep = append(keep, it)
			continue
		}
		for _, to := range it.Extra {
			_ = os.Remove(to)
		}
		removeIfEmpty(filepath.Dir(it.In))
		freed += it.Size
		changed = true
	}
	if changed {
		if err := t.save(keep); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return freed, firstErr
}

// removeIfEmpty removes a trash folder with nothing left but its note.
func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || filepath.Base(dir) != trashDir {
		return
	}
	for _, e := range entries {
		if e.Name() != trashNote {
			return
		}
	}
	_ = os.Remove(filepath.Join(dir, trashNote))
	_ = os.Remove(dir)
}

const trashNote = "README.txt"

// writeTrashNote says what the folder is, to whoever finds it.
func writeTrashNote(dir string) {
	p := filepath.Join(dir, trashNote)
	if _, err := os.Stat(p); err == nil {
		return
	}
	_ = os.WriteFile(p, []byte("Aniko's trash: episodes removed in Aniko's Library tools › Clean up.\n"+
		"Put them back or delete them for good there. They're deleted by themselves 30 days after.\n"), 0o644)
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
