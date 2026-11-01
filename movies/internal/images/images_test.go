package images

import (
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestRasterType(t *testing.T) {
	cases := map[string]string{
		"\xff\xd8\xff\xe0\x00\x10JFIF\x00":                 "image/jpeg",
		"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR":              "image/png",
		"GIF89a\x01\x00\x01\x00":                           "image/gif",
		"RIFF\x00\x00\x00\x00WEBPVP8 ":                     "image/webp",
		"\x00\x00\x00\x1cftypavif\x00\x00\x00\x00":         "image/avif",
		"<!doctype html><script>alert(1)</script>":         "",
		"<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>": "",
		"<?xml version=\"1.0\"?><svg></svg>":               "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := rasterType([]byte(in)); got != want {
			t.Errorf("rasterType(%q) = %q, want %q", in, got, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Close stops a download under way and waits for it: nothing is left in
// (or written to) the folder afterwards.
func TestCloseStopsDownloads(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	// An image that never finishes coming.
	c.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = pw.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 1024)...))
			<-r.Context().Done()
			pw.CloseWithError(r.Context().Err())
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}, nil
	})}
	c.Prefetch("https://image.example/a.png")
	files := func() []string {
		var out []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		return out
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(files()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the download never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.Close()
	if left := files(); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	c.Prefetch("https://image.example/b.png") // ignored once closed
	if left := files(); len(left) != 0 {
		t.Fatalf("written after Close: %v", left)
	}
}
