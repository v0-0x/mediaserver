// Package api is the music side's HTTP: what the Utako apps ask on its own
// port (/api/peer/..., every request signed: package peer), and what the
// dashboard's Music pages ask through the anime side (Handler).
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/music/internal/library"
	"github.com/v0-0x/mediaserver/music/internal/peer"
	fileup "github.com/v0-0x/mediaserver/music/internal/upload"
)

// Options say how the music side runs.
type Options struct {
	DataDir string
	// Version is mediaserver's, as the devices see it.
	Version string
	// Port is where the devices reach it (0: DefaultPort).
	Port int
	// DashboardPort is the dashboard's, where a browser that opens this
	// side's port is sent.
	DashboardPort int
	// Free reports the room left on a folder's drive (nil: not checked).
	Free func(dir string) (uint64, error)
}

// DefaultPort is the music side's (anime 43211, movies & TV 43311).
const DefaultPort = 43411

// Server is the music side.
type Server struct {
	opt  Options
	name string
	id   *peer.Identity
	lib  *library.Library
	devs *devices
	kept *keptSettings
	recv *fileup.Receiver

	http   *http.Server
	ln     net.Listener
	ann    *peer.Announcer
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New opens the music side's data in o.DataDir.
func New(o Options) (*Server, error) {
	if o.Port == 0 {
		o.Port = DefaultPort
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, err
	}
	id, err := peer.Load(filepath.Join(o.DataDir, "identity.json"))
	if err != nil {
		return nil, err
	}
	lib, err := library.Open(o.DataDir)
	if err != nil {
		return nil, err
	}
	devs, err := openDevices(o.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{opt: o, name: hostname(), id: id, lib: lib, devs: devs, kept: &keptSettings{file: filepath.Join(o.DataDir, "settings-kept.json")}}
	s.recv = &fileup.Receiver{Free: o.Free, Done: func(string) { lib.ScanSoon() }}
	return s, nil
}

// hostname is the computer's name, without its domain: the server's name
// to the devices.
func hostname() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	if h = peer.CleanName(h); h == "" {
		return "mediaserver"
	}
	return h
}

// ID is the server's, as the devices know it.
func (s *Server) ID() string { return s.id.ID }

// Port is where the devices reach it.
func (s *Server) Port() int { return s.opt.Port }

// Start listens for the devices, sends the beacon and reads the library.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.opt.Port))
	if err != nil {
		return fmt.Errorf("could not start the music side: %v (is mediaserver already running?)", err)
	}
	s.ln = ln
	s.http = &http.Server{Handler: s.peerHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(3)
	go func() {
		defer s.wg.Done()
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("music: %v", err)
		}
	}()
	go func() {
		defer s.wg.Done()
		s.lib.Run(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.background(ctx)
	}()
	log.Printf("music: for the Utako apps at port %d (%s, %s)", s.opt.Port, s.name, s.id.ID)
	return nil
}

// background sends the beacon every few seconds, and keeps the devices'
// list.
func (s *Server) background(ctx context.Context) {
	ann, err := peer.NewAnnouncer()
	if err != nil {
		log.Printf("music: devices won't find the server by themselves (%v): add it by address in Utako", err)
	}
	beacon := time.NewTicker(5 * time.Second)
	keep := time.NewTicker(time.Minute)
	defer beacon.Stop()
	defer keep.Stop()
	for {
		if ann != nil {
			ann.Announce(s.beacon())
		}
		select {
		case <-ctx.Done():
			if ann != nil {
				ann.Close()
			}
			s.devs.flush()
			return
		case <-beacon.C:
		case <-keep.C:
			s.devs.flush()
		}
	}
}

func (s *Server) beacon() peer.Beacon {
	return peer.Beacon{Protocol: 1, ID: s.id.ID, Name: s.name, Port: s.opt.Port, Key: s.id.PublicKey(), Version: s.opt.Version, Platform: "mediaserver", Role: "hub"}
}

// Shutdown stops the music side.
func (s *Server) Shutdown() {
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.http.Shutdown(ctx)
		cancel()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// ---------------------------------------------------------------------------
// The devices' requests

type ctxKey struct{}

type asker struct {
	dev    Device
	caller *peer.Caller
}

func askerOf(r *http.Request) asker { return r.Context().Value(ctxKey{}).(asker) }

// smallBody is the most a device not allowed may send (a hello has none).
const smallBody = 1 << 20

func (s *Server) peerHandler() http.Handler {
	signed := http.NewServeMux()
	signed.HandleFunc("GET /api/peer/hello", s.hello)
	signed.HandleFunc("GET /api/peer/music/library", s.allowed(s.library))
	signed.HandleFunc("GET /api/peer/local/file", s.allowed(s.file))
	signed.HandleFunc("GET /api/peer/music/art", s.allowed(s.art))
	signed.HandleFunc("GET /api/peer/music/settings", s.allowed(s.getSettings))
	signed.HandleFunc("POST /api/peer/music/settings", s.allowed(s.putSettings))
	signed.HandleFunc("GET /api/peer/upload", s.allowed(s.peerUpload))
	signed.HandleFunc("POST /api/peer/upload", s.allowed(s.peerUpload))

	m := http.NewServeMux()
	m.HandleFunc("GET /api/peer/whoami", func(w http.ResponseWriter, r *http.Request) {
		if !peer.IsLocalNetwork(remoteIP(r)) {
			writeJSON(w, http.StatusForbidden, errMsg("not from the local network"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": s.id.ID, "name": s.name, "key": s.id.PublicKey(), "version": s.opt.Version, "role": "hub", "platform": "mediaserver", "side": "music"})
	})
	m.Handle("/api/peer/", s.verify(signed))
	// A browser: to the dashboard, which has the Music pages.
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || s.opt.DashboardPort == 0 {
			http.NotFound(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		http.Redirect(w, r, fmt.Sprintf("http://%s:%d/#music", host, s.opt.DashboardPort), http.StatusFound)
	})
	return m
}

// verify lets through the requests a device signed (its answer signed back),
// from the home network, noting the device as seen.
func (s *Server) verify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := remoteIP(r)
		if !peer.IsLocalNetwork(ip) {
			writeJSON(w, http.StatusForbidden, errMsg("not from the local network"))
			return
		}
		if !peer.IsPeerRequest(r) {
			writeJSON(w, http.StatusUnauthorized, errMsg("this is mediaserver's music side, for the Utako apps"))
			return
		}
		// A large body is read (to check it) only from a device let in:
		// anyone else could have megabytes read for nothing.
		if r.ContentLength > smallBody || r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody {
			if d, ok := s.devs.get(r.Header.Get(peer.HeaderID)); !ok || !d.Allowed {
				writeJSON(w, http.StatusRequestEntityTooLarge, errMsg("too large"))
				return
			}
		}
		now := time.Now()
		c, sig, err := s.id.Verify(r, now)
		if sig != nil {
			date := now.UTC().Format(http.TimeFormat)
			if reply, err := sig.Reply(date); err == nil {
				w.Header().Set("Date", date)
				w.Header().Set(peer.HeaderReply, reply)
			}
		}
		if err != nil {
			writeJSON(w, http.StatusForbidden, errMsg(err.Error()))
			return
		}
		dev := s.devs.seen(c, ip.String(), now)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, asker{dev: dev, caller: c})))
	})
}

// allowed lets through the devices the dashboard shares the music with.
func (s *Server) allowed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a := askerOf(r); !a.dev.Allowed {
			writeJSON(w, http.StatusForbidden, errMsg(fmt.Sprintf("%s doesn't share its music with this device yet: allow %q in its dashboard (Music › Devices)", s.name, a.dev.Name)))
			return
		}
		next(w, r)
	}
}

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	a := askerOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.id.ID, "name": s.name, "version": s.opt.Version,
		"shares":  a.dev.Allowed,
		"uploads": a.dev.Allowed && a.dev.Uploads,
		"files":   s.lib.Version(),
	})
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	version, tracks := s.lib.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "tracks": tracks})
}

// file streams a song (Range included): a ticket, which the player repeats
// when it seeks.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if _, ok := s.lib.Song(p); !ok {
		writeJSON(w, http.StatusNotFound, errMsg("that song isn't in the library (any more)"))
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errMsg("the song isn't there: is the drive it's on plugged in?"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		writeJSON(w, http.StatusNotFound, errMsg("the song isn't there"))
		return
	}
	s.devs.playing(askerOf(r).dev.ID, p, time.Now())
	if ct := library.Exts[strings.ToLower(filepath.Ext(p))]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}

func (s *Server) art(w http.ResponseWriter, r *http.Request) {
	serveArt(w, s.lib, r.URL.Query().Get("path"))
}

func serveArt(w http.ResponseWriter, lib *library.Library, p string) {
	data, mime, err := lib.Art(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, errMsg("no cover"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// sealedBody is settings and logins as they cross the network: sealed for
// the server when a device sends them, for the device when it asks.
type sealedBody struct {
	Sealed    string `json:"sealed"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	a := askerOf(r)
	var b sealedBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Sealed == "" {
		writeJSON(w, http.StatusBadRequest, errMsg("no settings sent"))
		return
	}
	plain, err := s.id.OpenFrom(a.caller.Pub, s.id.ID, a.caller.ID, b.Sealed)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errMsg("the settings weren't sealed for this server"))
		return
	}
	if err := s.kept.put(plain, b.UpdatedAt, a.dev); err != nil {
		writeJSON(w, http.StatusInternalServerError, errMsg("couldn't keep them: "+err.Error()))
		return
	}
	log.Printf("music: settings and logins from %s kept", a.dev.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	a := askerOf(r)
	k, ok := s.kept.get()
	if !ok {
		writeJSON(w, http.StatusNotFound, errMsg("the server has no settings yet: send them from a device first"))
		return
	}
	sealed, err := s.id.SealFor(a.caller.Pub, a.caller.ID, s.id.ID, k.Data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
		return
	}
	log.Printf("music: settings and logins given to %s", a.dev.Name)
	writeJSON(w, http.StatusOK, sealedBody{Sealed: sealed, UpdatedAt: k.UpdatedAt})
}

func (s *Server) peerUpload(w http.ResponseWriter, r *http.Request) {
	a := askerOf(r)
	if !a.dev.Uploads {
		writeJSON(w, http.StatusForbidden, errMsg(fmt.Sprintf("%s doesn't take songs from this device: turn on Let it upload here for %q in its dashboard (Music › Devices)", s.name, a.dev.Name)))
		return
	}
	s.uploadHere(w, r, a.dev.Name)
}

// ---------------------------------------------------------------------------
// Uploads (package upload), from a device or the dashboard's browser

// uploadHere takes a request about an upload into the library: its
// folders, where a file stands, or a piece. from is the device it comes
// from ("": the dashboard).
func (s *Server) uploadHere(w http.ResponseWriter, r *http.Request, from string) {
	q := r.URL.Query()
	dirs := s.lib.Dirs()
	if q.Get("path") == "" {
		writeJSON(w, http.StatusOK, map[string]any{"folders": dirs, "chunk": fileup.Chunk})
		return
	}
	i, err := strconv.Atoi(q.Get("folder"))
	if q.Get("folder") == "" {
		i, err = 0, nil
	}
	if err != nil || i < 0 || i >= len(dirs) {
		msg := "no such library folder"
		if len(dirs) == 0 {
			msg = "the music side has no folder to put songs in yet: add one in the dashboard (Music › Library)"
		}
		writeJSON(w, http.StatusNotFound, errMsg(msg))
		return
	}
	total, err1 := strconv.ParseInt(q.Get("total"), 10, 64)
	offset, err2 := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err1 != nil || (r.Method == http.MethodPost && err2 != nil) {
		writeJSON(w, http.StatusBadRequest, errMsg("the file's size and the piece's place are needed"))
		return
	}
	var st fileup.Result
	if r.Method == http.MethodPost {
		// The whole piece first, whatever the answer: one refused unread
		// breaks the connection, and the sender only sees a network error.
		piece, err := io.ReadAll(io.LimitReader(r.Body, fileup.Chunk+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errMsg("the piece didn't arrive whole: "+err.Error()))
			return
		}
		st, err = s.recv.Receive(dirs[i], q.Get("path"), offset, total, bytes.NewReader(piece))
		if err == nil && st.Done {
			who := from
			if who == "" {
				who = "the dashboard"
			}
			log.Printf("music: uploaded %s from %s", filepath.Join(dirs[i], filepath.FromSlash(st.Path)), who)
		}
		if err != nil {
			uploadError(w, err)
			return
		}
	} else {
		st, err = s.recv.Status(dirs[i], q.Get("path"), total)
		if err != nil {
			uploadError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func uploadError(w http.ResponseWriter, err error) {
	var e *fileup.Error
	if errors.As(err, &e) {
		writeJSON(w, e.Status, map[string]any{"error": e.Msg, "have": e.Have})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
}

// ---------------------------------------------------------------------------
// Settings and logins a device sent, for the others

type keptSettings struct {
	file string
	mu   sync.Mutex
}

type kept struct {
	Data      []byte `json:"data"`
	UpdatedAt int64  `json:"updatedAt"`
	From      string `json:"from"`
	FromID    string `json:"fromId"`
	Received  int64  `json:"received"`
}

func (k *keptSettings) put(data []byte, updatedAt int64, from Device) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	raw, _ := json.Marshal(kept{Data: data, UpdatedAt: updatedAt, From: from.Name, FromID: from.ID, Received: time.Now().Unix()})
	return peer.WriteFile(k.file, raw, 0o600)
}

func (k *keptSettings) get() (kept, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	raw, err := os.ReadFile(k.file)
	if err != nil {
		return kept{}, false
	}
	var out kept
	if json.Unmarshal(raw, &out) != nil || len(out.Data) == 0 {
		return kept{}, false
	}
	return out, true
}

func (k *keptSettings) forget() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := os.Remove(k.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errMsg(msg string) map[string]string { return map[string]string{"error": msg} }

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func encodeKey(pub []byte) string { return base64.StdEncoding.EncodeToString(pub) }
