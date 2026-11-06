package share

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/events"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// countingLibrary counts how often the whole library is read.
type countingLibrary struct {
	files fakeLibrary
	reads int
}

func (c *countingLibrary) All() ([]*library.LocalFile, error) {
	c.reads++
	return c.files.All()
}

func (c *countingLibrary) Get(path string) (*library.LocalFile, error) { return c.files.Get(path) }

// The library's version is read once, not on every hello: again when the
// library says it changed. The list the others get is encoded once per
// version, and gzipped.
func TestSharedVersionReadOnChange(t *testing.T) {
	d := openDB(t)
	settings, _ := config.NewStore(d)
	hub := events.NewHub()
	lib := &countingLibrary{files: fakeLibrary{{Path: "/a/1.mkv", MediaID: 5, Episode: 1, Kind: "main"}}}
	s, _ := New(d, settings, lib, hub)
	v1 := s.Hello(Caller{Allowed: true}).Files
	for i := 0; i < 20; i++ {
		if v := s.Hello(Caller{Allowed: true}).Files; v != v1 {
			t.Fatalf("version changed with no change: %s then %s", v1, v)
		}
	}
	if lib.reads != 1 {
		t.Errorf("the library was read %d times for 21 hellos (want once)", lib.reads)
	}
	ver, plain, gz, err := s.SharedFilesJSON()
	if err != nil || ver != v1 {
		t.Fatalf("SharedFilesJSON: %q, %v (want %q)", ver, err, v1)
	}
	var fromPlain, fromGz []*library.LocalFile
	if err := json.Unmarshal(plain, &fromPlain); err != nil || len(fromPlain) != 1 {
		t.Fatalf("plain: %v, %d files", err, len(fromPlain))
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	if err := json.Unmarshal(raw, &fromGz); err != nil || len(fromGz) != 1 || fromGz[0].Path != "/a/1.mkv" {
		t.Fatalf("gzipped: %v, %+v", err, fromGz)
	}
	if lib.reads != 1 {
		t.Errorf("encoding the list read the library again (%d reads)", lib.reads)
	}

	// The library changed: a new version at the next hello.
	lib.files = append(lib.files, &library.LocalFile{Path: "/a/2.mkv", MediaID: 5, Episode: 2, Kind: "main"})
	hub.Publish(events.LibraryUpdated, nil)
	deadline := time.Now().Add(3 * time.Second)
	for s.Hello(Caller{Allowed: true}).Files == v1 {
		if time.Now().After(deadline) {
			t.Fatal("the version didn't change after the library did")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, plain, _, _ = s.SharedFilesJSON(); !bytes.Contains(plain, []byte("/a/2.mkv")) {
		t.Error("the list sent doesn't have the new file")
	}
}
