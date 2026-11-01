package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	fileup "github.com/v0-0x/mediaserver/anime/internal/upload"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

// Uploads (package upload): files and folders sent from a computer into a
// library. The app (web/src/components/UploadDialog) sends them a piece at
// a time to its own Aniko, which keeps them (mediaserver's anime side, for
// the browser signed in to its dashboard) or passes them on to the Aniko they go to
// (one that takes this one's downloads), whose /api/peer/upload takes them.

// uploadTarget is where files can go: a Aniko this one may upload to, or
// (ID "") this one.
type uploadTarget struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	// Here: this one (mediaserver's anime side, to its dashboard's browser).
	Here bool `json:"here,omitempty"`
}

// uploadFolders is a Aniko's library folders files can go to (their index
// is ?folder=), and the most a piece may hold.
type uploadFolders struct {
	Folders []string `json:"folders"`
	Chunk   int      `json:"chunk"`
}

func (s *Server) uploadRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/upload/targets", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only available on this computer")
		}
		out := []uploadTarget{}
		if s.uploadsHere() {
			name, _ := machineHost()
			out = append(out, uploadTarget{Name: name, Online: true, Here: true})
		}
		for _, h := range s.app.Share.UploadHosts() {
			out = append(out, uploadTarget{ID: h.ID, Name: h.Name, Online: h.Online})
		}
		return out, nil
	}))
	// ?target=<id> (none: here); without ?path=, its folders; with
	// ?folder=&path=&total=, where that file stands.
	m.HandleFunc("GET /api/upload", s.uploadAsk)
	// ?target=&folder=&path=&offset=&total=, a piece in the body.
	m.HandleFunc("POST /api/upload", s.uploadAsk)
	// From another Aniko, the same (it may download onto this one).
	m.HandleFunc("GET /api/peer/upload", shared(func(w http.ResponseWriter, r *http.Request) {
		if !peerOf(r).Downloads {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "this Aniko doesn't take files from yours"})
			return
		}
		s.uploadHere(w, r, peerOf(r).Name)
	}))
	m.HandleFunc("POST /api/peer/upload", shared(func(w http.ResponseWriter, r *http.Request) {
		if !peerOf(r).Downloads {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "this Aniko doesn't take files from yours"})
			return
		}
		s.uploadHere(w, r, peerOf(r).Name)
	}))
}

// uploadsHere: this Aniko takes files from the browser itself (it's
// mediaserver's anime side: its dashboard); others take them from other
// Aniko apps only.
func (s *Server) uploadsHere() bool { return s.app.HubStore != nil }

// machineHost is this computer's name, for the list of targets.
func machineHost() (string, error) {
	if n := machineName(); n != "" {
		return n, nil
	}
	return "This server", nil
}

func (s *Server) uploadAsk(w http.ResponseWriter, r *http.Request) {
	if !isTrusted(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only available on this computer"})
		return
	}
	q := r.URL.Query()
	target := q.Get("target")
	if target == "" {
		if !s.uploadsHere() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pick the computer the files go to"})
			return
		}
		s.uploadHere(w, r, "")
		return
	}
	var piece []byte
	if r.Method == http.MethodPost {
		var err error
		if piece, err = io.ReadAll(io.LimitReader(r.Body, fileup.Chunk+1)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len(piece) > fileup.Chunk {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the piece is too large"})
			return
		}
	}
	q.Del("target")
	// A piece carries on when the page goes away mid-way: it's sent again
	// from where it stands anyway.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	status, raw, err := s.app.Share.AskUpload(ctx, target, r.Method, q.Encode(), piece)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the other computer doesn't answer: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// uploadHere takes a request about an upload into this Aniko's library:
// its folders, where a file stands, or a piece. from is the Aniko it comes
// from ("": the browser).
func (s *Server) uploadHere(w http.ResponseWriter, r *http.Request, from string) {
	q := r.URL.Query()
	dirs := s.app.Settings.Get().LibraryDirs()
	if q.Get("path") == "" {
		writeJSON(w, http.StatusOK, uploadFolders{Folders: append([]string{}, dirs...), Chunk: fileup.Chunk})
		return
	}
	i, err := strconv.Atoi(q.Get("folder"))
	if q.Get("folder") == "" {
		i, err = 0, nil
	}
	if err != nil || i < 0 || i >= len(dirs) {
		msg := "no such library folder"
		if len(dirs) == 0 {
			msg = "this Aniko has no library folder to put files in (Settings › Local library)"
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": msg})
		return
	}
	total, err1 := strconv.ParseInt(q.Get("total"), 10, 64)
	offset, err2 := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err1 != nil || (r.Method == http.MethodPost && err2 != nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the file's size and the piece's place are needed"})
		return
	}
	var st fileup.Result
	if r.Method == http.MethodPost {
		// The whole piece first, whatever the answer: one refused unread
		// (it doesn't follow on, say) breaks the connection, and the
		// browser only sees a network error, not where to carry on from.
		var piece []byte
		piece, err = io.ReadAll(io.LimitReader(r.Body, fileup.Chunk+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the piece didn't arrive whole: " + err.Error()})
			return
		}
		st, err = s.receiver().Receive(dirs[i], q.Get("path"), offset, total, bytes.NewReader(piece))
		if err == nil && st.Done {
			full := filepath.Join(dirs[i], filepath.FromSlash(st.Path))
			who := from
			if who == "" {
				who = "the dashboard"
			}
			log.Printf("upload: %s from %s", full, who)
			// The hub's audit log: who sent what.
			if s.app.HubStore != nil {
				ev := anikohub.Event{Kind: "upload", Device: from, Text: "uploaded " + st.Path, Size: st.Have,
					Files: []anikohub.AuditFile{{Name: filepath.Base(full), Path: full, Size: st.Have}}}
				if from == "" {
					ev.By, ev.IP = auditBy(r)
				}
				s.app.HubStore.Log(ev)
			}
		}
	} else {
		st, err = s.receiver().Status(dirs[i], q.Get("path"), total)
	}
	if err != nil {
		var e *fileup.Error
		if errors.As(err, &e) {
			writeJSON(w, e.Status, map[string]any{"error": e.Msg, "have": e.Have})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// receiver takes the files into the library, and has it look at them once
// they're there.
func (s *Server) receiver() *fileup.Receiver {
	s.uploadOnce.Do(func() {
		s.uploads = &fileup.Receiver{
			Free: func(dir string) (uint64, error) {
				_, free, err := util.DiskSpace(dir)
				return free, err
			},
			Done: func(string) { s.app.Scanner.ScanSoon() },
		}
	})
	return s.uploads
}

// uploadQuery is the query of a request about an upload (for tests).
func uploadQuery(target string, folder int, path string, offset, total int64) string {
	q := url.Values{}
	if target != "" {
		q.Set("target", target)
	}
	q.Set("folder", strconv.Itoa(folder))
	q.Set("path", path)
	q.Set("offset", fmt.Sprint(offset))
	q.Set("total", fmt.Sprint(total))
	return q.Encode()
}
