package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/history"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// Clean up lists the episodes watched to the end and those there twice;
// only this computer can put files away, and they come back.
func TestCleanup(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir = root
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	add := func(name string, media, ep, size int) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.app.Files.Save(&library.LocalFile{Path: p, Dir: root, Name: name, Size: int64(size), MediaID: media, Episode: ep, Kind: "main"}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	watched := add("Show - 01.mkv", 5, 1, 100)
	add("Show - 02.mkv", 5, 2, 100)
	add("Show - 02 [720p].mkv", 5, 2, 50)
	_ = s.app.History.MarkFinished(5, 1, 1400, "local")
	_ = s.app.History.Save(history.Entry{MediaID: 5, Episode: 2, Position: 100, Duration: 1400, Source: "local"})

	w := do(s, "GET", "/api/library/cleanup", "127.0.0.1:5000", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var view cleanupView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Watched) != 1 || view.Watched[0].Path != watched || view.Watched[0].WatchedAt == 0 {
		t.Errorf("watched: %+v", view.Watched)
	}
	if len(view.Duplicates) != 1 || len(view.Duplicates[0]) != 2 || view.Duplicates[0][0].Size != 100 {
		t.Errorf("duplicates: %+v", view.Duplicates)
	}
	if view.LibraryBytes != 250 {
		t.Errorf("library size %d", view.LibraryBytes)
	}
	// Two copies the same size: the same one is kept (listed first) each
	// time.
	a := add("Show - 03 (v2).mkv", 5, 3, 70)
	add("Show - 03.mkv", 5, 3, 70)
	for i := 0; i < 20; i++ {
		var v cleanupView
		_ = json.Unmarshal(do(s, "GET", "/api/library/cleanup", "127.0.0.1:5000", "", nil).Body.Bytes(), &v)
		for _, g := range v.Duplicates {
			if g[0].Episode == 3 && g[0].Path != a {
				t.Fatalf("run %d: kept %s", i, g[0].Path)
			}
		}
	}
	if view.Disk == nil || view.Disk.Total == 0 {
		t.Errorf("disk: %+v", view.Disk)
	}

	body := `{"paths":["` + strings.ReplaceAll(watched, `\`, `\\`) + `"]}`
	if w := do(s, "POST", "/api/library/trash", "192.168.1.20:5000", body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a device on the network put a file away: %d", w.Code)
	}
	if _, err := os.Stat(watched); err != nil {
		t.Fatal("moved by a device on the network")
	}
	if w := do(s, "POST", "/api/library/trash", "127.0.0.1:5000", body, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"moved":1`) {
		t.Fatalf("trash: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(watched); !os.IsNotExist(err) {
		t.Fatal("still there")
	}
	items := s.app.Trash.Items()
	if len(items) != 1 {
		t.Fatalf("trash: %+v", items)
	}
	if w := do(s, "POST", "/api/library/trash/restore", "127.0.0.1:5000", `{"ids":["`+items[0].ID+`"]}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"restored":1`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(watched); err != nil {
		t.Fatal("not back")
	}
	if w := do(s, "POST", "/api/library/trash/empty", "192.168.1.20:5000", `{}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a device on the network emptied the trash: %d", w.Code)
	}
}
