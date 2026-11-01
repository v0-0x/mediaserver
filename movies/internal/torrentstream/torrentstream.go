// Package torrentstream plays a torrent as it downloads: KumoBox's own
// BitTorrent client fetches the file being watched, the pieces about to be
// played first (a reader's readahead), the rest after, and serves it like a
// file (byte ranges for seeking). What it downloads is kept in a cache
// folder only as long as it's played: a torrent nobody has read for a while
// is dropped, with its files.
//
// The in-app player plays the file directly when the browser can, or
// converts it with ffmpeg, which reads it through the stream relay
// (stream.RegisterRemoteHandler); mpv plays it from the relay too.
package torrentstream

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// metadataWait is how long the torrent's details (its files) are
	// waited for, from its peers.
	metadataWait = 90 * time.Second
	// idleAfter: a torrent nobody read this long is dropped.
	idleAfter = 20 * time.Minute
	// maxTorrents are played at once at most; the least recently read goes.
	maxTorrents = 3
	// readahead is how far past where it's read a reader asks for pieces
	// first: a few seconds of a high bitrate video.
	readahead = 24 << 20
	// spare is the room kept free on the cache's drive.
	spare = 1 << 30
)

// ErrUnavailable: this build has no BitTorrent client (Android).
var ErrUnavailable = errors.New("torrents can't be streamed on this device: download them instead")

// Trackers are added to a magnet link that has none (a bare info hash, as
// most addons give): public ones, which most torrents are on.
var Trackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.moeking.me:6969/announce",
	"udp://explodie.org:6969/announce",
	"https://tracker.tamersunion.org:443/announce",
}

// Pick chooses the file to play among a torrent's (their paths in it): its
// index, or -1 for the largest video.
type Pick func(paths []string) int

// Status is how a torrent being played is doing, for the player.
type Status struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Done  int64  `json:"done"`  // bytes of the file downloaded
	Peers int    `json:"peers"` // connected
	Seeds int    `json:"seeds"`
	Speed int64  `json:"speed"` // bytes per second, lately
}

// Engine is KumoBox's BitTorrent client for streaming, started the first
// time a torrent is played.
type Engine struct {
	dir string
	// Free reports the room on the cache's drive (nil: not checked).
	Free func(dir string) (uint64, error)

	mu       sync.Mutex
	client   client
	sessions map[string]*session
	stop     chan struct{}

	// The loopback address the sessions are read from by ffmpeg and mpv
	// (URL), started once.
	loopOnce sync.Once
	loopAddr string
	loopErr  error
}

// session is a torrent being played: the file played of it.
type session struct {
	id       string
	name     string
	size     int64
	t        torrentHandle
	file     fileHandle
	lastRead atomic.Int64 // unix nanoseconds
	readers  atomic.Int32
	// For the speed: what had been downloaded, when.
	speedMu   sync.Mutex
	lastBytes int64
	lastAt    time.Time
	speed     int64
}

// New makes the engine, with its cache in dir (emptied: what an earlier run
// left there is of no use).
func New(dir string) *Engine {
	_ = os.RemoveAll(dir)
	return &Engine{dir: dir, sessions: map[string]*session{}}
}

// Magnet makes a magnet link of an info hash (hex or base32) or a magnet
// link, with the public trackers when it names none.
func Magnet(link, infoHash, name string) (string, error) {
	if link == "" {
		h := strings.ToLower(strings.TrimSpace(infoHash))
		switch len(h) {
		case 40:
			if _, err := hex.DecodeString(h); err != nil {
				return "", errors.New("not a torrent's info hash")
			}
		case 32:
			if _, err := base32.StdEncoding.DecodeString(strings.ToUpper(h)); err != nil {
				return "", errors.New("not a torrent's info hash")
			}
		default:
			return "", errors.New("this torrent has no info hash")
		}
		link = "magnet:?xt=urn:btih:" + h
		if name != "" {
			link += "&dn=" + url.QueryEscape(name)
		}
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "magnet" {
		return "", errors.New("not a magnet link")
	}
	q := u.Query()
	if len(q["tr"]) == 0 {
		for _, tr := range Trackers {
			link += "&tr=" + url.QueryEscape(tr)
		}
	}
	return link, nil
}

// Open starts playing a torrent (a magnet link): it waits for its details
// from its peers, picks the file to play, and has it downloaded, the start
// first. The same file of the same torrent is the same session.
func (e *Engine) Open(ctx context.Context, magnet string, pick Pick) (Status, error) {
	cl, err := e.start()
	if err != nil {
		return Status{}, err
	}
	t, err := cl.addMagnet(magnet)
	if err != nil {
		return Status{}, fmt.Errorf("couldn't add the torrent: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, metadataWait)
	defer cancel()
	select {
	case <-t.gotInfo():
	case <-ctx.Done():
		// Nobody else playing it: it goes.
		e.mu.Lock()
		used := false
		for _, s := range e.sessions {
			used = used || s.t.infoHash() == t.infoHash()
		}
		e.mu.Unlock()
		if !used {
			t.drop()
		}
		return Status{}, errors.New("no peer sent the torrent's details in time: it may have none left (try another one)")
	}
	files := t.files()
	if len(files) == 0 {
		return Status{}, errors.New("the torrent has no files")
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path()
	}
	i := -1
	if pick != nil {
		i = pick(paths)
	}
	if i < 0 || i >= len(files) {
		i = largestVideo(files)
	}
	if i < 0 {
		return Status{}, errors.New("the torrent has no video file")
	}
	f := files[i]
	id := fmt.Sprintf("%s-%d", t.infoHash(), i)
	e.mu.Lock()
	s, ok := e.sessions[id]
	if !ok {
		if e.Free != nil {
			if free, err := e.Free(e.dir); err == nil && free < uint64(f.length())+spare {
				e.mu.Unlock()
				return Status{}, fmt.Errorf("there isn't room for %s (%d MB) in the cache: free some on this computer's drive", filepath.Base(f.path()), f.length()>>20)
			}
		}
		s = &session{id: id, name: filepath.Base(f.path()), size: f.length(), t: t, file: f}
		s.lastRead.Store(time.Now().UnixNano())
		e.sessions[id] = s
		e.evictLocked(id)
		// All of it, at normal priority; what's read goes first.
		f.download()
		log.Printf("torrent stream: playing %s (%d MB) of %s", s.name, s.size>>20, t.name())
	}
	e.mu.Unlock()
	return e.status(s), nil
}

// largestVideo is the index of the largest video file, -1 for none.
func largestVideo(files []fileHandle) int {
	best := -1
	for i, f := range files {
		if !isVideo(f.path()) {
			continue
		}
		if best < 0 || f.length() > files[best].length() {
			best = i
		}
	}
	return best
}

func isVideo(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".webm", ".mov", ".ts", ".m2ts", ".wmv", ".flv", ".mpg", ".mpeg", ".ogv":
		return true
	}
	return false
}

// evictLocked drops the least recently read torrents past maxTorrents
// (never keep).
func (e *Engine) evictLocked(keep string) {
	for len(e.sessions) > maxTorrents {
		var oldest *session
		for id, s := range e.sessions {
			if id == keep || s.readers.Load() > 0 {
				continue
			}
			if oldest == nil || s.lastRead.Load() < oldest.lastRead.Load() {
				oldest = s
			}
		}
		if oldest == nil {
			return
		}
		e.dropLocked(oldest)
	}
}

// dropLocked stops a session; its torrent goes, with its files, when no
// other session plays it.
func (e *Engine) dropLocked(s *session) {
	delete(e.sessions, s.id)
	for _, o := range e.sessions {
		if o.t.infoHash() == s.t.infoHash() {
			return
		}
	}
	s.t.drop()
	log.Printf("torrent stream: done with %s", s.name)
	// The last one: no client (nor its port, nor seeding), which lets go
	// of the files it had open (on Windows those can't be removed before).
	if len(e.sessions) == 0 {
		e.closeClientLocked()
		return
	}
	hash := s.t.infoHash()
	removeDir(filepath.Join(e.dir, hash), func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, o := range e.sessions {
			if o.t.infoHash() == hash {
				return false // played again meanwhile
			}
		}
		return true
	})
}

// closeClientLocked stops the client and its janitor, and removes what's
// in the cache: nothing is played any more.
func (e *Engine) closeClientLocked() {
	if e.stop != nil {
		close(e.stop)
		e.stop = nil
	}
	if e.client != nil {
		e.client.close()
		e.client = nil
	}
	removeDir(e.dir, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.client == nil // nothing played again meanwhile
	})
}

// removeDir removes a dropped torrent's files: again for a while when they
// can't be yet (on Windows, a file the client hasn't closed yet can't be),
// as long as unused says they still are.
func removeDir(dir string, unused func() bool) {
	if os.RemoveAll(dir) == nil {
		return
	}
	go func() {
		for range 30 {
			time.Sleep(time.Second)
			if !unused() {
				return
			}
			if os.RemoveAll(dir) == nil {
				return
			}
		}
		log.Printf("torrent stream: couldn't remove %s", dir)
	}()
}

// Status is how a session is doing.
func (e *Engine) Status(id string) (Status, bool) {
	e.mu.Lock()
	s, ok := e.sessions[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, false
	}
	return e.status(s), true
}

func (e *Engine) status(s *session) Status {
	done := s.file.bytesCompleted()
	peers, seeds := s.t.peers()
	now := time.Now()
	s.speedMu.Lock()
	if !s.lastAt.IsZero() {
		if dt := now.Sub(s.lastAt); dt >= time.Second {
			s.speed = int64(float64(done-s.lastBytes) / dt.Seconds())
			s.lastBytes, s.lastAt = done, now
		}
	} else {
		s.lastBytes, s.lastAt = done, now
	}
	speed := max(s.speed, 0)
	s.speedMu.Unlock()
	return Status{ID: s.id, Name: s.name, Size: s.size, Done: done, Peers: peers, Seeds: seeds, Speed: speed}
}

// Stop stops playing a session now.
func (e *Engine) Stop(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[id]; ok {
		e.dropLocked(s)
	}
}

// ServeHTTP serves the file of the session named by the path's last part
// (".../<id>" or ".../<id>/file"), with byte ranges, as it downloads.
func (e *Engine) Serve(w http.ResponseWriter, r *http.Request, id string) {
	e.mu.Lock()
	s, ok := e.sessions[id]
	e.mu.Unlock()
	if !ok {
		http.Error(w, "this torrent isn't being played any more: play it again", http.StatusNotFound)
		return
	}
	s.readers.Add(1)
	defer s.readers.Add(-1)
	rd := s.file.newReader(r.Context())
	defer rd.Close()
	ct := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(s.name)) {
	case ".mp4", ".m4v":
		ct = "video/mp4"
	case ".mkv":
		ct = "video/x-matroska"
	case ".webm":
		ct = "video/webm"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, s.name, time.Time{}, &touching{ReadSeeker: rd, s: s})
}

// touching notes each read as the session being used.
type touching struct {
	io.ReadSeeker
	s *session
}

func (t *touching) Read(p []byte) (int, error) {
	t.s.lastRead.Store(time.Now().UnixNano())
	return t.ReadSeeker.Read(p)
}

// URL is where a session's file is read from on this computer (by ffmpeg,
// mpv): a loopback address only this computer reaches, the session's
// unguessable ID in it.
func (e *Engine) URL(id string) (string, error) {
	e.loopOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			e.loopErr = err
			return
		}
		e.loopAddr = ln.Addr().String()
		srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := strings.CutPrefix(r.URL.Path, "/t/")
			if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
				http.NotFound(w, r)
				return
			}
			e.Serve(w, r, id)
		})}
		go func() { _ = srv.Serve(ln) }()
	})
	if e.loopErr != nil {
		return "", e.loopErr
	}
	return "http://" + e.loopAddr + "/t/" + id, nil
}

// Size is the size of a session's file.
func (e *Engine) Size(id string) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return 0, false
	}
	return s.size, true
}

// Handler serves a session's file (see Serve), for the stream relay.
func (e *Engine) Handler(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.Serve(w, r, id) })
}

// Close stops everything, the client included.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.sessions {
		s.t.drop()
	}
	e.sessions = map[string]*session{}
	e.closeClientLocked()
}

// start starts the client once, and its janitor, which drops the torrents
// nobody reads any more (and the client with the last one).
func (e *Engine) start() (client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		return e.client, nil
	}
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewTextHandler(logWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
	cl, err := newClient(e.dir, logger)
	if err != nil {
		return nil, err
	}
	e.client = cl
	e.stop = make(chan struct{})
	go e.janitor(e.stop)
	return cl, nil
}

func (e *Engine) janitor(stop chan struct{}) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		e.mu.Lock()
		for _, s := range e.sessions {
			if s.readers.Load() == 0 && time.Since(time.Unix(0, s.lastRead.Load())) > idleAfter {
				e.dropLocked(s)
			}
		}
		// Nothing played (the last one dropped closed the client).
		done := e.client == nil
		e.mu.Unlock()
		if done {
			return
		}
	}
}

// logWriter passes the client's errors on to KumoBox's log.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	log.Printf("torrent stream: %s", strings.TrimSpace(string(p)))
	return len(p), nil
}

// The BitTorrent client, as the engine uses it (client.go; nothing on
// Android: client_android.go).
type client interface {
	addMagnet(magnet string) (torrentHandle, error)
	close()
}

type torrentHandle interface {
	gotInfo() <-chan struct{}
	files() []fileHandle
	infoHash() string
	name() string
	peers() (peers, seeds int)
	drop()
}

type fileHandle interface {
	path() string
	length() int64
	bytesCompleted() int64
	download()
	newReader(ctx context.Context) readSeekCloser
}

type readSeekCloser interface {
	io.ReadSeeker
	io.Closer
}
