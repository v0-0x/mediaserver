package config

// Aniko was called Kumo before 2.0.37. What it kept under that name moves to
// the new one the first time the new version starts, and what was set up
// with the old name (a service's environment, a script) still counts.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Getenv reads ANIKO_<name>, or KUMO_<name> as it was set before the rename.
func Getenv(name string) string {
	if v := os.Getenv("ANIKO_" + name); v != "" {
		return v
	}
	return os.Getenv("KUMO_" + name)
}

// oldDataDir is the data folder Kumo had: ~/.local/share/kumo, or the Kumo
// folder next to Aniko's.
func oldDataDir() string {
	dir := defaultDataDir()
	name := "Kumo"
	if filepath.Base(dir) == "aniko" {
		name = "kumo"
	}
	return filepath.Join(filepath.Dir(dir), name)
}

// MoveOld moves the folder or file old to dir when only old exists. It
// returns where the files are now: dir, or old when it couldn't be moved
// (it's tried again on the next start).
func MoveOld(old, dir string) string {
	if old == "" || old == dir {
		return dir
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		return dir
	}
	if _, err := os.Lstat(old); err != nil {
		return dir
	}
	if err := os.Rename(old, dir); err != nil {
		log.Printf("couldn't move %s to %s (Kumo's name to Aniko's): %v", old, dir, err)
		return old
	}
	log.Printf("moved %s to %s (Kumo's name to Aniko's)", old, dir)
	return dir
}

// MoveOldFiles renames the files Kumo kept in its data folder: the
// database (with its journal, which goes first: a database without it
// would lose the last changes) and the server's log (just removed).
func MoveOldFiles(dataDir string) {
	old, db := filepath.Join(dataDir, "kumo.db"), filepath.Join(dataDir, "aniko.db")
	if _, err := os.Stat(old); err == nil {
		if _, err := os.Lstat(db); errors.Is(err, fs.ErrNotExist) {
			for _, suffix := range []string{"-wal", "-shm", ""} {
				MoveOld(old+suffix, db+suffix)
			}
		}
	}
	for _, name := range []string{"kumo.log", "kumo.log.1", "kumo.log.2", "kumo.log.3"} {
		_ = os.Remove(filepath.Join(dataDir, name))
	}
}

// HubDataDir is a hub's data folder when none is given: aniko-hub next to
// Aniko's, or the kumo-hub one a hub installed as Kumo Hub has (its service
// names that one, so it stays where it is).
func HubDataDir() string {
	dir := filepath.Join(filepath.Dir(defaultDataDir()), "aniko-hub")
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if old := filepath.Join(filepath.Dir(dir), "kumo-hub"); isDir(old) {
			return old
		}
	}
	return dir
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// movedPaths points settings that named a file in Kumo's data folder (a
// program the macOS setup put there, say) at the same file in Aniko's,
// once the folder has moved. It says whether anything changed.
func movedPaths(s *Settings) bool {
	old, dir := oldDataDir(), defaultDataDir()
	if _, err := os.Lstat(old); !errors.Is(err, fs.ErrNotExist) || !isDir(dir) {
		return false
	}
	quoted := func(p string) string {
		b, _ := json.Marshal(p + string(filepath.Separator))
		return strings.Trim(string(b), `"`)
	}
	raw, err := json.Marshal(s)
	if err != nil || !strings.Contains(string(raw), quoted(old)) {
		return false
	}
	var next Settings
	if json.Unmarshal([]byte(strings.ReplaceAll(string(raw), quoted(old), quoted(dir))), &next) != nil {
		return false
	}
	*s = next
	return true
}
