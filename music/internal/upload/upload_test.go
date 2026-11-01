package upload

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func piece(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func refusal(t *testing.T, err error) int {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("not a refusal: %v", err)
	}
	return e.Status
}

// A file comes in pieces, into its folders, and takes its name at the end;
// an upload cut short carries on where it stopped.
func TestReceive(t *testing.T) {
	dir := t.TempDir()
	var done []string
	u := &Receiver{Done: func(p string) { done = append(done, p) }}
	data := bytes.Repeat([]byte("0123456789"), 1000)
	rel := "Show/Season 1/Show - 01.mkv"

	st, err := u.Receive(dir, rel, 0, int64(len(data)), piece(data[:4000]))
	if err != nil || st.Have != 4000 || st.Done {
		t.Fatalf("first piece: %+v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Show", "Season 1", "Show - 01.mkv")); err == nil {
		t.Fatal("the file has its name before it's all there")
	}
	// Sent again from the start (the sender lost track): told where it is.
	_, err = u.Receive(dir, rel, 0, int64(len(data)), piece(data[:4000]))
	var e *Error
	if !errors.As(err, &e) || e.Status != 409 || e.Have != 4000 {
		t.Fatalf("a piece that doesn't follow on: %v", err)
	}
	if st, err := u.Status(dir, rel, int64(len(data))); err != nil || st.Have != 4000 {
		t.Fatalf("status: %+v %v", st, err)
	}
	st, err = u.Receive(dir, rel, 4000, int64(len(data)), piece(data[4000:]))
	if err != nil || !st.Done || st.Have != int64(len(data)) {
		t.Fatalf("last piece: %+v %v", st, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Show", "Season 1", "Show - 01.mkv"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the file isn't what was sent: %v", err)
	}
	if len(done) != 1 || !strings.HasSuffix(done[0], "Show - 01.mkv") {
		t.Fatalf("done: %v", done)
	}
	// The same file again: already there.
	if st, err := u.Receive(dir, rel, 0, int64(len(data)), piece(data)); err != nil || !st.Done {
		t.Fatalf("the same file again: %+v %v", st, err)
	}
	// Another one by the same name: never written over.
	if _, err := u.Receive(dir, rel, 0, 5, piece([]byte("other"))); refusal(t, err) != 409 {
		t.Fatalf("another file by that name: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "Show", "Season 1", "Show - 01.mkv")); !bytes.Equal(got, data) {
		t.Fatal("the file was written over")
	}
	// More than the file, or than a piece holds.
	if _, err := u.Receive(dir, "b.mkv", 0, 3, piece([]byte("abcd"))); refusal(t, err) != 400 {
		t.Fatalf("a piece larger than the file: %v", err)
	}
	if st, _ := u.Status(dir, "b.mkv", 3); st.Have != 0 {
		t.Fatalf("what a refused piece wrote stayed: %+v", st)
	}
	// An empty file.
	if st, err := u.Receive(dir, "empty.srt", 0, 0, piece(nil)); err != nil || !st.Done {
		t.Fatalf("an empty file: %+v %v", st, err)
	}
}

func TestNoRoom(t *testing.T) {
	u := &Receiver{Free: func(string) (uint64, error) { return 100 << 20, nil }}
	if _, err := u.Receive(t.TempDir(), "big.mkv", 0, 1<<30, piece([]byte("x"))); refusal(t, err) != 507 {
		t.Fatalf("no room: %v", err)
	}
}

// Nothing is written outside the folder: no parent folders, absolute
// paths, hidden names, or links out of it.
func TestStaysInItsFolder(t *testing.T) {
	for _, rel := range []string{"../x.mkv", "/etc/x", "a/../../x.mkv", ".hidden/x.mkv", "a/.x", "", "a//b.mkv", "x.mkv.part", `..\x.mkv`, "a/b\x00.mkv", "con:x"} {
		if _, err := Clean(rel); err == nil {
			t.Errorf("%q was taken", rel)
		}
	}
	for _, rel := range []string{"Movie (2020)/Movie (2020).mkv", "a.mkv", `Show\S01\e1.mkv`} {
		if _, err := Clean(rel); err != nil {
			t.Errorf("%q: %v", rel, err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir, out := t.TempDir(), t.TempDir()
	if err := os.Symlink(out, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	u := &Receiver{}
	if _, err := u.Receive(dir, "link/x.mkv", 0, 1, piece([]byte("x"))); err == nil {
		t.Fatal("written through a link out of the folder")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("written out of the folder: %v", entries)
	}
}
