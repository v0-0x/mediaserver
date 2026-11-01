package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/downloads"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

func newHub(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a, err := New(t.TempDir(), Options{Hub: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	root := t.TempDir()
	cfg := a.Settings.Get()
	cfg.Library.Dir = root
	if _, err := a.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return a, root
}

// What 2.0.32 put in a hub's trash is deleted when it starts, and noted in
// the audit log, dubbed as the hub downloaded it, to download again.
func TestEmptyHubTrash(t *testing.T) {
	a, root := newHub(t)
	p := filepath.Join(root, "Show", "Show - 03.mkv")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Files.Save(&library.LocalFile{Path: p, Dir: filepath.Dir(p), Name: filepath.Base(p), Size: 500, MediaID: 9, Episode: 3, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	// The hub downloaded it, dubbed: what Download again goes by.
	done, _ := json.Marshal(downloads.Item{ID: "d1", MediaID: 9, Episode: 3, Mode: "dub", Source: "anicli", Provider: "ani-cli", Status: downloads.StatusCompleted, Output: p})
	if _, err := a.DB.Write(`INSERT INTO downloads (id, data, created_at) VALUES ('d1', ?, 1)`, string(done)); err != nil {
		t.Fatal(err)
	}
	a.Downloads = downloads.NewManager(a.Settings, a.DB, a.Hub, a.Files)
	if moved, failed := a.Trash.Put([]string{p}); len(moved) != 1 {
		t.Fatalf("put: %v", failed)
	}

	a.emptyHubTrash()
	if items := a.Trash.Items(); len(items) != 0 {
		t.Errorf("still in the trash: %+v", items)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".aniko-trash")); len(entries) != 0 {
		t.Errorf("still on the disk: %v", entries)
	}
	ev, _, _ := a.HubStore.Audit(anikohub.AuditQuery{Kinds: []string{"delete"}})
	if len(ev) != 1 || ev[0].By != "hub" || len(ev[0].Files) != 1 || ev[0].Again == nil || !ev[0].Again.Dub || ev[0].Again.Provider != "ani-cli" || ev[0].Again.Episodes[0] != 3 {
		t.Fatalf("noted: %+v", ev)
	}
	// Nothing in it: nothing noted.
	a.emptyHubTrash()
	if ev, _, _ := a.HubStore.Audit(anikohub.AuditQuery{Kinds: []string{"delete"}}); len(ev) != 1 {
		t.Errorf("%d deletions noted", len(ev))
	}
}

// A download onto the hub finishing or failing is noted.
func TestAuditDownload(t *testing.T) {
	a, root := newHub(t)
	out := filepath.Join(root, "Show - 04.mkv")
	_ = os.WriteFile(out, make([]byte, 700), 0o644)
	a.auditDownload(downloads.Item{MediaID: 9, Episode: 4, AnimeTitle: "Show", Status: downloads.StatusCompleted, Output: out})
	a.auditDownload(downloads.Item{MediaID: 9, Episode: 5, AnimeTitle: "Show", Status: downloads.StatusFailed, Error: "no stream found"})
	a.auditDownload(downloads.Item{MediaID: 9, Episode: 6, Status: downloads.StatusCanceled})
	ev, _, _ := a.HubStore.Audit(anikohub.AuditQuery{Kinds: anikohub.AuditGroups["downloads"]})
	if len(ev) != 2 {
		t.Fatalf("%d noted: %+v", len(ev), ev)
	}
	if f := ev[0]; f.Kind != "download-failed" || !f.Failed || f.Text != "couldn't download" || f.Detail != "no stream found" || f.Episode != 5 {
		t.Errorf("failed: %+v", f)
	}
	if d := ev[1]; d.Kind != "downloaded" || d.Size != 700 || d.Files[0].Name != "Show - 04.mkv" || d.Title != "Show" {
		t.Errorf("done: %+v", d)
	}
}
