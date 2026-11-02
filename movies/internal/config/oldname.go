package config

// Moviko was called KumoBox before 2.0.9. What it kept under that name moves
// to the new one the first time the new version starts, and what was set up
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

// Getenv reads MOVIKO_<name>, or KUMOBOX_<name> as it was set before the
// rename.
func Getenv(name string) string {
	if v := os.Getenv("MOVIKO_" + name); v != "" {
		return v
	}
	return os.Getenv("KUMOBOX_" + name)
}

// oldDataDir is the data folder KumoBox had: ~/.local/share/kumobox, or the
// KumoBox folder next to Moviko's.
func oldDataDir() string {
	dir := defaultDataDir()
	name := "KumoBox"
	if filepath.Base(dir) == "moviko" {
		name = "kumobox"
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
		log.Printf("couldn't move %s to %s (KumoBox's name to Moviko's): %v", old, dir, err)
		return old
	}
	log.Printf("moved %s to %s (KumoBox's name to Moviko's)", old, dir)
	return dir
}

// MoveOldFiles renames the files KumoBox kept in its data folder: the
// database (with its journal, which goes first: a database without it would
// lose the last changes) and the server's log (just removed).
func MoveOldFiles(dataDir string) {
	old, db := filepath.Join(dataDir, "kumobox.db"), filepath.Join(dataDir, "moviko.db")
	if _, err := os.Stat(old); err == nil {
		if _, err := os.Lstat(db); errors.Is(err, fs.ErrNotExist) {
			for _, suffix := range []string{"-wal", "-shm", ""} {
				MoveOld(old+suffix, db+suffix)
			}
		}
	}
	for _, name := range []string{"kumobox.log", "kumobox.log.1", "kumobox.log.2", "kumobox.log.3"} {
		_ = os.Remove(filepath.Join(dataDir, name))
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// movedPaths points settings that named a file in KumoBox's data folder (a
// program the macOS setup put there, say) at the same file in Moviko's, once
// the folder has moved. It says whether anything changed.
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
