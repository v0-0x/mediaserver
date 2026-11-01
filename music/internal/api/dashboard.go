package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/music/internal/library"
)

// Handler is what the dashboard's Music pages ask, at /api/music/... on the
// anime side (anime/internal/api/sides.go), which lets through its signed-in
// owner only and passes them on here as /api/.... It's never on a port of
// its own.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/status", s.status)
	m.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.deviceViews())
	})
	m.HandleFunc("POST /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Allowed *bool `json:"allowed"`
			Uploads *bool `json:"uploads"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			writeJSON(w, http.StatusBadRequest, errMsg("bad request"))
			return
		}
		d, err := s.devs.set(r.PathValue("id"), b.Allowed, b.Uploads)
		if err != nil {
			writeJSON(w, http.StatusNotFound, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, s.view(d, time.Now()))
	})
	m.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.devs.forget(r.PathValue("id")); err != nil {
			writeJSON(w, http.StatusNotFound, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/folders", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.lib.Folders())
	})
	m.HandleFunc("POST /api/folders", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Path == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("which folder?"))
			return
		}
		if err := s.lib.AddFolder(b.Path); err != nil {
			writeJSON(w, http.StatusBadRequest, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, s.lib.Folders())
	})
	m.HandleFunc("DELETE /api/folders", func(w http.ResponseWriter, r *http.Request) {
		if err := s.lib.RemoveFolder(r.URL.Query().Get("path")); err != nil {
			writeJSON(w, http.StatusNotFound, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, s.lib.Folders())
	})
	m.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		s.lib.ScanSoon()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/tracks", s.tracks)
	m.HandleFunc("GET /api/art", func(w http.ResponseWriter, r *http.Request) {
		serveArt(w, s.lib, r.URL.Query().Get("path"))
	})
	m.HandleFunc("DELETE /api/kept", func(w http.ResponseWriter, r *http.Request) {
		if err := s.kept.forget(); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	// The dashboard's upload dialog (web/anime UploadDialog, base
	// /api/music): this server is its only target.
	m.HandleFunc("GET /api/upload/targets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{{"id": "", "name": s.name, "online": true, "here": true}})
	})
	m.HandleFunc("GET /api/upload", func(w http.ResponseWriter, r *http.Request) { s.uploadHere(w, r, "") })
	m.HandleFunc("POST /api/upload", func(w http.ResponseWriter, r *http.Request) { s.uploadHere(w, r, "") })

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only ever from the anime side, in the program.
		if ip := remoteIP(r); ip == nil || !ip.IsLoopback() {
			writeJSON(w, http.StatusForbidden, errMsg("sign in to the dashboard first"))
			return
		}
		m.ServeHTTP(w, r)
	})
}

// deviceView is a device as the dashboard shows it.
type deviceView struct {
	Device
	Online  bool        `json:"online"`
	Playing *nowPlaying `json:"playing,omitempty"`
}

type nowPlaying struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Path   string `json:"path"`
	HasArt bool   `json:"hasArt"`
	At     int64  `json:"at"`
}

// listening is how long a song started counts as playing (unless the device
// went away).
const listening = 15 * time.Minute

func (s *Server) view(d Device, now time.Time) deviceView {
	v := deviceView{Device: d, Online: now.Sub(time.Unix(d.LastSeen, 0)) < online}
	if d.Playing != "" && now.Sub(time.Unix(d.PlayingAt, 0)) < listening {
		if t, ok := s.lib.Song(d.Playing); ok {
			v.Playing = &nowPlaying{Title: t.Title, Artist: t.Artist, Album: t.Album, Path: t.Path, HasArt: t.HasArt, At: d.PlayingAt}
		}
	}
	return v
}

func (s *Server) deviceViews() []deviceView {
	now := time.Now()
	list := s.devs.list()
	out := make([]deviceView, 0, len(list))
	for _, d := range list {
		out = append(out, s.view(d, now))
	}
	return out
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	songs, albums, artists := s.lib.Counts()
	scanning, last := s.lib.Scanning()
	devs := s.deviceViews()
	var on, allowed int
	listeners := []deviceView{}
	for _, d := range devs {
		if d.Online {
			on++
		}
		if d.Allowed {
			allowed++
		}
		if d.Playing != nil {
			listeners = append(listeners, d)
		}
	}
	var keptInfo any
	if k, ok := s.kept.get(); ok {
		keptInfo = map[string]any{"from": k.From, "updatedAt": k.UpdatedAt, "received": k.Received, "size": len(k.Data)}
	}
	lastScan := int64(0)
	if !last.IsZero() {
		lastScan = last.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.id.ID, "name": s.name, "port": s.opt.Port, "version": s.opt.Version,
		"songs": songs, "albums": albums, "artists": artists,
		"folders": len(s.lib.Dirs()), "scanning": scanning, "lastScan": lastScan,
		"devices": len(devs), "online": on, "allowed": allowed,
		"listening": listeners, "kept": keptInfo, "lengths": s.lib.CanProbe(),
	})
}

// tracks are the songs matching ?q= (any word, in the title, artist,
// album or path), a page at a time (?offset=, ?limit=).
func (s *Server) tracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	words := strings.Fields(strings.ToLower(q.Get("q")))
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 100
	}
	all := s.lib.Tracks()
	match := make([]library.Track, 0, min(len(all), limit))
	total := 0
	for _, t := range all {
		if len(words) > 0 {
			hay := strings.ToLower(t.Title + "\x00" + t.Artist + "\x00" + t.AlbumArtist + "\x00" + t.Album + "\x00" + t.Path)
			ok := true
			for _, w := range words {
				if !strings.Contains(hay, w) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		if total >= offset && len(match) < limit {
			match = append(match, t)
		}
		total++
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "tracks": match})
}
