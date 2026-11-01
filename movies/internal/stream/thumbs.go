package stream

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Seek-bar previews: a small picture of the video every thumbStep seconds,
// each made when it's asked for, from the keyframe at that time (one picture
// decoded: fast, also for a computer serving a TV). They're kept in memory a
// while; the browser keeps them too.

const (
	thumbStep  = 10 // seconds between two previews
	thumbWidth = 240
	thumbsKept = 400 // about 3 MB
)

type thumbKey struct {
	path string
	mod  int64
	t    int
}

type thumbCache struct {
	mu    sync.Mutex
	pics  map[thumbKey][]byte
	order []thumbKey
	// Two pictures made at a time at most: a seek bar moved fast asks for
	// many, and those given up on (the browser cancels them) aren't made.
	busy chan struct{}
}

var (
	thumbsOnce sync.Once
	thumbs     *thumbCache
)

func thumbStore() *thumbCache {
	thumbsOnce.Do(func() { thumbs = &thumbCache{pics: map[thumbKey][]byte{}, busy: make(chan struct{}, 2)} })
	return thumbs
}

func (c *thumbCache) get(k thumbKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.pics[k]
	return b, ok
}

func (c *thumbCache) put(k thumbKey, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pics[k]; ok {
		return
	}
	c.pics[k] = b
	c.order = append(c.order, k)
	for len(c.order) > thumbsKept {
		delete(c.pics, c.order[0])
		c.order = c.order[1:]
	}
}

// ThumbTime is the preview time a time falls on.
func ThumbTime(t float64) int {
	if t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		return 0
	}
	return int(t/thumbStep) * thumbStep
}

// ServeThumb sends the preview at t of a library file, as JPEG.
func (l *Local) ServeThumb(w http.ResponseWriter, r *http.Request, path string, t float64) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	k := thumbKey{path: path, mod: st.ModTime().UnixNano(), t: ThumbTime(t)}
	c := thumbStore()
	pic, ok := c.get(k)
	if !ok {
		pic, err = l.makeThumb(r.Context(), c, path, k.t)
		if err != nil {
			if r.Context().Err() == nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			}
			return
		}
		c.put(k, pic)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(pic)))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(pic)
}

func (l *Local) makeThumb(ctx context.Context, c *thumbCache, path string, t int) ([]byte, error) {
	select {
	case c.busy <- struct{}{}:
		defer func() { <-c.busy }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg := l.settings.Get()
	// Only keyframes decoded, from the one at t (input seeking lands on a
	// keyframe): the video stream, not a cover picture attached to the file.
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-skip_frame", "nokey", "-noaccurate_seek", "-ss", strconv.Itoa(t), "-i", path,
		"-map", "0:V:0", "-an", "-sn", "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", thumbWidth),
		"-f", "image2", "-c:v", "mjpeg", "-q:v", "6", "pipe:1").Output()
	if err != nil {
		return nil, fmt.Errorf("no preview: %v", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no preview at %ds", t)
	}
	return out, nil
}
