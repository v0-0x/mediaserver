package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Aniko Hub's data moves into anime/ the first time, with a link left where
// it was; after that, the old service's --data-dir (the link) still finds it.
func TestMoveOldHub(t *testing.T) {
	share := t.TempDir()
	old := filepath.Join(share, "aniko-hub")
	root := filepath.Join(share, "mediaserver")
	if err := os.MkdirAll(filepath.Join(old, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "aniko.db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isHubData(old) || isHubData(root) {
		t.Fatal("isHubData is wrong")
	}
	dir := moveOldHub(root, old)
	if dir != filepath.Join(root, "anime") {
		t.Fatalf("anime data in %s", dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "aniko.db")); err != nil || string(b) != "db" {
		t.Fatalf("the database didn't move: %v", err)
	}
	if st, err := os.Lstat(old); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("no link left at %s: %v", old, err)
	}
	// The old service's run: its --data-dir is the link.
	if !isHubData(old) {
		t.Fatal("the link isn't taken for Aniko Hub's data")
	}
	if again := moveOldHub(root, old); again != dir {
		t.Fatalf("a second run uses %s", again)
	}
	// Nothing to move: a new anime/ in root.
	fresh := filepath.Join(t.TempDir(), "mediaserver")
	if got := moveOldHub(fresh, filepath.Join(share, "nothing")); got != filepath.Join(fresh, "anime") {
		t.Fatalf("fresh install: %s", got)
	}
}
