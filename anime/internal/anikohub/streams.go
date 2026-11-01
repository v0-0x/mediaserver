package anikohub

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Streams are the videos the hub sends the devices now, for its dashboard:
// who, what, how (the file as it is, or converted), and how fast.
type Streams struct {
	mu   sync.Mutex
	next int
	live map[int]*Stream
	// HLS: a session's segments are one stream (by the session's ID), named
	// when the session starts.
	keyed  map[string]*Stream
	labels map[string]label
	recent []StreamView // ended, the latest last
	sent   atomic.Int64 // bytes, since the hub started
}

// NewStreams starts with none.
func NewStreams() *Streams {
	return &Streams{live: map[int]*Stream{}, keyed: map[string]*Stream{}, labels: map[string]label{}}
}

type label struct{ path, kind string }

// A session's stream is over when nothing was asked of it for this long.
const keyedIdle = 30 * time.Second

// Label names a session's stream (its file, how it's sent).
func (s *Streams) Label(key, path, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.labels) > 64 {
		s.labels = map[string]label{}
	}
	s.labels[key] = label{path, kind}
}

// Keyed is a session's stream: begun with its first segment, over when
// it's idle (see Live).
func (s *Streams) Keyed(key, device string) *Stream {
	s.mu.Lock()
	if st := s.keyed[key]; st != nil {
		s.mu.Unlock()
		return st
	}
	lb := s.labels[key]
	s.mu.Unlock()
	if lb.kind == "" {
		lb.kind = "hls"
	}
	st := s.Begin(device, lb.path, lb.kind)
	st.key = key
	s.mu.Lock()
	s.keyed[key] = st
	s.mu.Unlock()
	return st
}

// Stream is one being sent.
type Stream struct {
	s       *Streams
	id      int
	device  string
	file    string
	kind    string
	started time.Time
	bytes   atomic.Int64
	key     string       // a session's (Keyed)
	last    atomic.Int64 // when bytes were last sent (ms)
	// The rate: the bytes sent at the last few looks (a second apart at
	// least), over up to rateWindow (a file's are counted in steps).
	looks []look
	rate  float64
}

type look struct {
	at    time.Time
	bytes int64
}

const rateWindow = 10 * time.Second

// StreamView is a stream as the dashboard shows it.
type StreamView struct {
	ID      int     `json:"id"`
	Device  string  `json:"device"`
	File    string  `json:"file"`
	Kind    string  `json:"kind"` // file | remux | transcode | hls
	Started int64   `json:"started"`
	Ended   int64   `json:"ended,omitempty"`
	Bytes   int64   `json:"bytes"`
	Rate    float64 `json:"rate"` // bytes a second, lately
}

// Begin notes a stream starting (End when it's over).
func (s *Streams) Begin(device, path, kind string) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	now := time.Now()
	st := &Stream{s: s, id: s.next, device: device, file: filepath.Base(path), kind: kind, started: now, looks: []look{{now, 0}}}
	s.live[st.id] = st
	return st
}

// Add counts bytes sent.
func (st *Stream) Add(n int64) {
	if n > 0 {
		st.bytes.Add(n)
		st.s.sent.Add(n)
		st.last.Store(time.Now().UnixMilli())
	}
}

// End notes it's over (kept a while among the recent ones).
func (st *Stream) End() {
	s := st.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live[st.id]; !ok {
		return
	}
	delete(s.live, st.id)
	if st.key != "" {
		delete(s.keyed, st.key)
		delete(s.labels, st.key)
	}
	v := st.view(time.Now())
	v.Ended = time.Now().UnixMilli()
	// A request for a few bytes (a player checking the file) isn't one.
	if v.Bytes < 1<<20 {
		return
	}
	s.recent = append(s.recent, v)
	if len(s.recent) > 20 {
		s.recent = s.recent[len(s.recent)-20:]
	}
}

func (st *Stream) view(now time.Time) StreamView {
	b := st.bytes.Load()
	if now.Sub(st.looks[len(st.looks)-1].at) >= time.Second {
		st.looks = append(st.looks, look{now, b})
		for len(st.looks) > 2 && now.Sub(st.looks[1].at) >= rateWindow {
			st.looks = st.looks[1:]
		}
		first := st.looks[0]
		if d := now.Sub(first.at).Seconds(); d > 0 {
			st.rate = float64(b-first.bytes) / d
		}
	}
	return StreamView{ID: st.id, Device: st.device, File: st.file, Kind: st.kind, Started: st.started.UnixMilli(), Bytes: b, Rate: st.rate}
}

// Live are the streams now, the oldest first; Recent those that ended
// lately, the latest first; Sent all the bytes sent since the hub started.
func (s *Streams) Live() []StreamView {
	now := time.Now()
	s.mu.Lock()
	var idle []*Stream
	for _, st := range s.keyed {
		last := max(st.last.Load(), st.started.UnixMilli())
		if now.UnixMilli()-last > keyedIdle.Milliseconds() {
			idle = append(idle, st)
		}
	}
	s.mu.Unlock()
	for _, st := range idle {
		st.End()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StreamView, 0, len(s.live))
	for _, st := range s.live {
		out = append(out, st.view(now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Streams) Recent() []StreamView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StreamView, len(s.recent))
	for i, v := range s.recent {
		out[len(out)-1-i] = v
	}
	return out
}

func (s *Streams) Sent() int64 { return s.sent.Load() }

// Writer counts what a response sends for a stream. A file is still sent by
// the system (sendfile), a few megabytes at a time so the count moves.
func (st *Stream) Writer(w http.ResponseWriter) http.ResponseWriter {
	return &countingWriter{ResponseWriter: w, st: st}
}

type countingWriter struct {
	http.ResponseWriter
	st *Stream
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.st.Add(int64(n))
	return n, err
}

// The step a file is sent in (sendfile), for the count to move: about a
// second of a 1080p episode.
const sendStep = 1 << 20

func (c *countingWriter) ReadFrom(r io.Reader) (int64, error) {
	rf, ok := c.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(writerOnly{c}, r)
	}
	// http.ServeContent sends a file's range as a LimitedReader of it: in
	// steps, each still one the system can send by itself.
	if lr, ok := r.(*io.LimitedReader); ok {
		if f, ok := lr.R.(*os.File); ok {
			var total int64
			for lr.N > 0 {
				step := &io.LimitedReader{R: f, N: min(lr.N, sendStep)}
				n, err := rf.ReadFrom(step)
				lr.N -= n
				total += n
				c.st.Add(n)
				if err != nil {
					return total, err
				}
				if n == 0 {
					break
				}
			}
			return total, nil
		}
	}
	// Anything else (ffmpeg's output) is counted as it's written.
	return io.Copy(writerOnly{c}, r)
}

func (c *countingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

type writerOnly struct{ io.Writer }
