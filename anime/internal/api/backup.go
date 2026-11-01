package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/backup"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/lifecycle"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// Settings › Backup & data: the user's data as one file, and back. Only this
// computer: a backup can hold the logins, a restore changes everything.
func (s *Server) backupRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		if !isTrusted(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the computer Aniko runs on can make a backup"})
			return
		}
		logins := r.URL.Query().Get("logins") == "1"
		computer := share.Hostname()
		f, err := backup.Make(s.app.DB, s.app.Settings.Get(), logins, config.AppVersion, computer)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		name := fmt.Sprintf("aniko-backup-%s-%s.json", safeName.ReplaceAllString(computer, "-"), time.Now().Format("2006-01-02"))
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, f)
	})
	// A backup put back: merged with what's here, then Aniko starts again
	// (everything reads its data afresh).
	m.HandleFunc("POST /api/backup/restore", func(w http.ResponseWriter, r *http.Request) {
		if !isTrusted(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the computer Aniko runs on can restore a backup"})
			return
		}
		f, err := backup.Read(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		sum, err := backup.Restore(s.app.DB, s.app.History, s.app.Settings, f, filepath.Join(s.app.DataDir, "backups"), config.AppVersion, share.Hostname())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "couldn't restore it: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, sum)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.app.Exits.Request(lifecycle.Exit{Restart: true})
		}()
	})
}

// safeName keeps a computer's name fit for a file name.
var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
