package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// referenceHash is the OpenSubtitles hash of a whole file, as its
// documentation computes it: the size plus every 64-bit little-endian
// word of the first and of the last 64 KiB.
func referenceHash(data []byte) string {
	h := uint64(len(data))
	for _, chunk := range [][]byte{data[:65536], data[len(data)-65536:]} {
		for i := 0; i < len(chunk); i += 8 {
			h += binary.LittleEndian.Uint64(chunk[i : i+8])
		}
	}
	return fmt.Sprintf("%016x", h)
}

// An online stream's size and OpenSubtitles hash come from two byte
// ranges of it, fetched with its headers and never from this computer or
// the LAN; a host that doesn't take ranges gives the size only, and an
// HLS stream nothing.
func TestVideoOf(t *testing.T) {
	data := make([]byte, 300<<10)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		if r.Header.Get("Referer") != "https://site.example/" {
			http.Error(w, "no referer", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/plain.mkv" {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		http.ServeContent(w, r, "movie.mkv", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)
	headers := map[string]string{"Referer": "https://site.example/"}
	ctx := context.Background()

	// On this computer, like the LAN: refused.
	LocalHosts = nil
	if _, err := VideoOf(ctx, RegisterRemote(srv.URL+"/movie.mkv", headers)); err == nil {
		t.Fatal("read a stream on this computer")
	}
	u, _ := url.Parse(srv.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} } // stands for a public host

	v, err := VideoOf(ctx, RegisterRemote(srv.URL+"/movie.mkv", headers))
	if err != nil || v.Size != int64(len(data)) || v.Hash != referenceHash(data) {
		t.Fatalf("got %+v, %v; want size %d, hash %s", v, err, len(data), referenceHash(data))
	}
	mu.Lock()
	if want := fmt.Sprintf("bytes=0-65535|bytes=%d-%d", len(data)-65536, len(data)-1); ranges[0]+"|"+ranges[1] != want {
		t.Errorf("asked %q, want %s", ranges, want)
	}
	mu.Unlock()

	if v, err := VideoOf(ctx, RegisterRemote(srv.URL+"/plain.mkv", headers)); err != nil || v.Size != int64(len(data)) || v.Hash != "" {
		t.Errorf("without ranges: %+v, %v", v, err)
	}
	if v, err := VideoOf(ctx, RegisterRemoteHLS(srv.URL+"/index.m3u8", headers)); err != nil || v != (providers.Video{}) {
		t.Errorf("HLS: %+v, %v", v, err)
	}
	if _, err := VideoOf(ctx, RemotePrefix+"0123456789abcdef01234567"); err != ErrRemoteGone {
		t.Errorf("unknown stream: %v", err)
	}
}

type videoSubs struct {
	mu  sync.Mutex
	got []providers.Video
}

func (p *videoSubs) Subtitles(context.Context, providers.Request) ([]providers.Subtitle, error) {
	return []providers.Subtitle{{URL: "https://subs.example/any.srt", Language: "eng"}}, nil
}

func (p *videoSubs) VideoSubtitles(_ context.Context, r providers.Request, v providers.Video) ([]providers.Subtitle, error) {
	p.mu.Lock()
	p.got = append(p.got, v)
	p.mu.Unlock()
	return []providers.Subtitle{{URL: fmt.Sprintf("https://subs.example/%s/%s.srt", r.IMDbID, v.Hash), Language: "eng"}}, nil
}

// The subtitles of an online stream are asked of the subtitle providers
// with the very file's size, hash and name.
func TestVideoSubtitles(t *testing.T) {
	data := make([]byte, 200<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "movie.mkv", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	defer func(old func() []string) { LocalHosts = old }(LocalHosts)
	u, _ := url.Parse(srv.URL)
	LocalHosts = func() []string { return []string{HostKey(u)} }

	p := &videoSubs{}
	s := NewService(nil)
	s.SubtitleProviders = func() []providers.SubtitleProvider { return []providers.SubtitleProvider{p} }
	movie := &catalog.Media{ID: 603, Type: catalog.TypeMovie, IMDbID: "tt0133093"}
	subs, err := s.VideoSubtitles(context.Background(), movie, 0, RegisterRemote(srv.URL+"/movie.mkv", nil), "The Matrix.mkv")
	if err != nil {
		t.Fatal(err)
	}
	want := providers.Video{Size: int64(len(data)), Hash: referenceHash(data), Filename: "The Matrix.mkv"}
	if len(p.got) != 1 || p.got[0] != want {
		t.Fatalf("asked with %+v, want %+v", p.got, want)
	}
	if len(subs) != 1 || subs[0].URL != "https://subs.example/tt0133093/"+want.Hash+".srt" {
		t.Errorf("subtitles %+v", subs)
	}
	// A stream Moviko no longer knows.
	if _, err := s.VideoSubtitles(context.Background(), movie, 0, RemotePrefix+"0123456789abcdef01234567", ""); err != ErrRemoteGone {
		t.Errorf("unknown stream: %v", err)
	}
}
