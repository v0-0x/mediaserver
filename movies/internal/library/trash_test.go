package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/db"
)

func trashFixture(t *testing.T) (*Trash, *Store, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	root := t.TempDir()
	files := NewStore(d)
	return NewTrash(d, files, func() []string { return []string{"", root} }), files, root
}

func addFile(t *testing.T, files *Store, path string, f LocalFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video of "+filepath.Base(path)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Path, f.Dir, f.Name, f.Kind = path, filepath.Dir(path), filepath.Base(path), "main"
	if err := files.Save(&f); err != nil {
		t.Fatal(err)
	}
}

// A file put away goes to the trash at the top of its library folder, with
// its subtitles, out of the library; put back, it's where it was with its
// match (set by hand) and subtitles.
func TestTrashPutAndRestore(t *testing.T) {
	tr, files, root := trashFixture(t)
	ep := filepath.Join(root, "Show", "Show - 01.mkv")
	addFile(t, files, ep, LocalFile{MediaID: 7, Episode: 1, Locked: true})
	sub := filepath.Join(root, "Show", "Show - 01.en.ass")
	other := filepath.Join(root, "Show", "Show - 010.ass") // another episode's
	for _, p := range []string{sub, other} {
		if err := os.WriteFile(p, []byte("subs"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	moved, failed := tr.Put([]string{ep, "/etc/passwd"})
	if len(moved) != 1 || failed["/etc/passwd"] == "" {
		t.Fatalf("moved %v, failed %v", moved, failed)
	}
	if _, err := os.Stat(ep); !os.IsNotExist(err) {
		t.Error("still where it was")
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Error("its subtitles stayed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another episode's subtitles went too")
	}
	items := tr.Items()
	if len(items) != 1 || filepath.Dir(items[0].In) != filepath.Join(root, trashDir) || items[0].MediaID != 7 {
		t.Fatalf("items: %+v", items)
	}
	if f, _ := files.Get(ep); f != nil {
		t.Error("still in the library")
	}
	if _, err := os.Stat(filepath.Join(root, trashDir, trashNote)); err != nil {
		t.Error("no note in the trash folder")
	}

	// Something new where it was: not put back over it.
	if err := os.WriteFile(ep, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if restored, failed := tr.Restore([]string{items[0].ID}); len(restored) != 0 || failed[items[0].ID] == "" {
		t.Fatalf("restored over a new file: %v %v", restored, failed)
	}
	_ = os.Remove(ep)
	if restored, failed := tr.Restore([]string{items[0].ID}); len(restored) != 1 || len(failed) != 0 {
		t.Fatalf("restore: %v %v", restored, failed)
	}
	if b, err := os.ReadFile(ep); err != nil || string(b) != "video of Show - 01.mkv" {
		t.Errorf("not back: %q %v", b, err)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Error("subtitles not back")
	}
	if f, _ := files.Get(ep); f == nil || f.MediaID != 7 || !f.Locked {
		t.Errorf("match not back: %+v", f)
	}
	if len(tr.Items()) != 0 {
		t.Error("still in the trash")
	}
	if _, err := os.Stat(filepath.Join(root, trashDir)); !os.IsNotExist(err) {
		t.Error("the empty trash folder stayed")
	}
}

// Emptying deletes for good (all, or some); what's been there 30 days goes
// by itself.
func TestTrashEmptyAndPurge(t *testing.T) {
	tr, files, root := trashFixture(t)
	a := filepath.Join(root, "A - 01.mkv")
	b := filepath.Join(root, "B - 01.mkv")
	c := filepath.Join(root, "C - 01.mkv")
	for i, p := range []string{a, b, c} {
		addFile(t, files, p, LocalFile{MediaID: i + 1, Episode: 1})
	}
	if moved, failed := tr.Put([]string{a, b, c}); len(moved) != 3 {
		t.Fatalf("%v %v", moved, failed)
	}
	items := tr.Items()
	var idA string
	for _, it := range items {
		if it.Path == a {
			idA = it.ID
		}
	}
	freed, err := tr.Empty([]string{idA})
	if err != nil || freed != int64(len("video of A - 01.mkv")) {
		t.Fatalf("freed %d, %v", freed, err)
	}
	if len(tr.Items()) != 2 {
		t.Fatal("not one fewer")
	}
	// B put away 31 days ago: purged; C kept.
	tr.mu.Lock()
	list := tr.load()
	for i := range list {
		if list[i].Path == b {
			list[i].DeletedAt = time.Now().Add(-31 * 24 * time.Hour).Unix()
		}
	}
	_ = tr.save(list)
	tr.mu.Unlock()
	if _, err := tr.Purge(time.Now()); err != nil {
		t.Fatal(err)
	}
	if left := tr.Items(); len(left) != 1 || left[0].Path != c {
		t.Fatalf("left: %+v", left)
	}
	if _, err := tr.Empty(nil); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("left on the disk: %v", entries)
	}
}

// One file reached two ways (a library folder linked into another) is no
// other copy: it isn't put away, the one kept would go with it.
func TestTrashSameFile(t *testing.T) {
	tr, files, root := trashFixture(t)
	real := filepath.Join(root, "Show", "Show - 01.mkv")
	addFile(t, files, real, LocalFile{MediaID: 7, Episode: 1})
	if err := os.Symlink(filepath.Join(root, "Show"), filepath.Join(root, "Linked")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	linked := filepath.Join(root, "Linked", "Show - 01.mkv")
	f := LocalFile{Path: linked, Dir: filepath.Dir(linked), Name: "Show - 01.mkv", Kind: "main", MediaID: 7, Episode: 1}
	if err := files.Save(&f); err != nil {
		t.Fatal(err)
	}
	moved, failed := tr.Put([]string{linked})
	if len(moved) != 0 || failed[linked] == "" {
		t.Fatalf("put away the same file: moved %v failed %v", moved, failed)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatal("the file kept is gone:", err)
	}
}

// Another video's subtitles stay, its name starting the same: "Show -
// 01.v2.en.ass" is "Show - 01.v2.mkv"'s, not "Show - 01.mkv"'s.
func TestTrashOtherVideosSubtitles(t *testing.T) {
	tr, files, root := trashFixture(t)
	ep := filepath.Join(root, "Show", "Show - 01.mkv")
	addFile(t, files, ep, LocalFile{MediaID: 7, Episode: 1})
	addFile(t, files, filepath.Join(root, "Show", "Show - 01.v2.mkv"), LocalFile{MediaID: 7, Episode: 1})
	mine := filepath.Join(root, "Show", "Show - 01.en.ass")
	theirs := filepath.Join(root, "Show", "Show - 01.v2.en.ass")
	for _, p := range []string{mine, theirs} {
		if err := os.WriteFile(p, []byte("subs"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, failed := tr.Put([]string{ep}); len(failed) != 0 {
		t.Fatal(failed)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Error("its subtitles stayed")
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Error("the other video's subtitles went too")
	}
}

// Emptied while the drive it's on isn't connected: kept for when it's back
// (the file is still on it), not forgotten.
func TestTrashEmptyDriveGone(t *testing.T) {
	tr, files, root := trashFixture(t)
	ep := filepath.Join(root, "Show", "Show - 01.mkv")
	addFile(t, files, ep, LocalFile{MediaID: 7, Episode: 1})
	if _, failed := tr.Put([]string{ep}); len(failed) != 0 {
		t.Fatal(failed)
	}
	// The drive away: its trash folder and the folder it's in, gone.
	hidden := root + "-away"
	if err := os.Rename(root, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Empty(nil); err == nil {
		t.Error("no word of the drive")
	}
	if n := len(tr.Items()); n != 1 {
		t.Fatalf("forgotten: %d items", n)
	}
	// Back: emptied.
	if err := os.Rename(hidden, root); err != nil {
		t.Fatal(err)
	}
	if freed, err := tr.Empty(nil); err != nil || freed == 0 || len(tr.Items()) != 0 {
		t.Fatalf("freed %d err %v items %d", freed, err, len(tr.Items()))
	}
}

// Deleted for good (a hub): the file and its subtitles gone from the disk
// and the library, nothing in the trash; another video's subtitles and
// what isn't a library file stay.
func TestDelete(t *testing.T) {
	tr, files, root := trashFixture(t)
	ep := filepath.Join(root, "Show", "Show - 01.mkv")
	addFile(t, files, ep, LocalFile{MediaID: 7, Episode: 1})
	v2 := filepath.Join(root, "Show", "Show - 01.v2.mkv")
	addFile(t, files, v2, LocalFile{MediaID: 7, Episode: 1})
	sub := filepath.Join(root, "Show", "Show - 01.en.ass")
	theirs := filepath.Join(root, "Show", "Show - 01.v2.en.ass")
	for _, p := range []string{sub, theirs} {
		if err := os.WriteFile(p, []byte("subs"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, failed := tr.Delete([]string{ep, "/etc/passwd"})
	if len(removed) != 1 || failed["/etc/passwd"] == "" {
		t.Fatalf("removed %v, failed %v", removed, failed)
	}
	r := removed[0]
	if r.Path != ep || r.MediaID != 7 || r.Episode != 1 || r.Kind != "main" || r.Size == 0 || len(r.Subtitles) != 1 || r.Subtitles[0] != sub {
		t.Errorf("removed %+v", r)
	}
	for _, p := range []string{ep, sub} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there", p)
		}
	}
	for _, p := range []string{v2, theirs} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s went too: %v", p, err)
		}
	}
	if f, _ := files.Get(ep); f != nil {
		t.Error("still in the library")
	}
	if len(tr.Items()) != 0 {
		t.Error("something went to the trash")
	}
	if _, err := os.Stat(filepath.Join(root, trashDir)); !os.IsNotExist(err) {
		t.Error("a trash folder was made")
	}
}
