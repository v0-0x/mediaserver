package stream

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
)

// A preview is a small JPEG of the video, the same for the times of one
// step (kept, so made once); a file outside the library has none.
func TestServeThumb(t *testing.T) {
	l, video := newFfmpegLocal(t, "libx264")
	get := func(path string, at string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/local/thumb?path="+url.QueryEscape(path)+"&t="+at, nil)
		l.ServeThumb(w, r, path, mustFloat(t, at))
		return w
	}
	w := get(video, "12.5")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	img, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != thumbWidth || b.Dy() != 180 {
		t.Errorf("preview is %dx%d", b.Dx(), b.Dy())
	}
	// 12.5 and 19 fall on the same preview (10s), already made.
	same := get(video, "19")
	if !bytes.Equal(same.Body.Bytes(), w.Body.Bytes()) {
		t.Error("another picture for the same step")
	}
	if _, ok := thumbStore().get(thumbKey{path: video, mod: modTime(t, video), t: 10}); !ok {
		t.Error("not kept")
	}
	// Past the end: no picture, an error.
	if w := get(video, "300"); w.Code == http.StatusOK {
		t.Error("a preview past the end")
	}
	if w := get("/etc/passwd", "0"); w.Code != http.StatusForbidden {
		t.Errorf("a file outside the library: %d", w.Code)
	}
}

func TestThumbTime(t *testing.T) {
	for in, want := range map[float64]int{0: 0, 9.99: 0, 10: 10, 125.4: 120, -3: 0} {
		if got := ThumbTime(in); got != want {
			t.Errorf("ThumbTime(%v) = %d, want %d", in, got, want)
		}
	}
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func modTime(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UnixNano()
}
