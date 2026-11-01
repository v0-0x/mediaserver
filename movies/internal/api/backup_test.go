package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/history"
)

// Only this computer makes or restores a backup; restoring asks KumoBox to
// start again.
func TestBackupRoutes(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	_ = s.app.History.Save(history.Entry{MediaID: 3, Episode: 2, Position: 300, Duration: 1400, Source: "local"})
	lan, here := "192.168.1.20:5000", "127.0.0.1:5000"
	if w := do(s, "GET", "/api/backup", lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("backup from the network: %d", w.Code)
	}
	w := do(s, "GET", "/api/backup", here, "", nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="kumobox-backup-`) {
		t.Fatalf("backup: %d %v", w.Code, w.Header())
	}
	file := w.Body.String()
	if w := do(s, "POST", "/api/backup/restore", lan, file, nil); w.Code != http.StatusForbidden {
		t.Fatalf("restore from the network: %d", w.Code)
	}
	if w := do(s, "POST", "/api/backup/restore", here, `{"nope":1}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("restore of something else: %d", w.Code)
	}
	_ = s.app.History.Clear(3, 2)
	w = do(s, "POST", "/api/backup/restore", here, file, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	var sum struct {
		History int    `json:"history"`
		SavedTo string `json:"savedTo"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	if sum.History != 1 || sum.SavedTo == "" || s.app.History.Get(3, 2) == nil {
		t.Errorf("restored: %+v", sum)
	}
	select {
	case e := <-s.app.Exits.C():
		if !e.Restart {
			t.Error("not a restart")
		}
	case <-time.After(3 * time.Second):
		t.Error("KumoBox wasn't asked to start again")
	}
}
