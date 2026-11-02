package config

import (
	"os"
	"path/filepath"
	"testing"
)

// KumoBox's data folder becomes Moviko's, unless Moviko has one already.
func TestMoveOld(t *testing.T) {
	dir := t.TempDir()
	old, now := filepath.Join(dir, "kumobox"), filepath.Join(dir, "moviko")
	if got := MoveOld(old, now); got != now {
		t.Fatalf("nothing to move: %q", got)
	}
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := MoveOld(old, now); got != now {
		t.Fatalf("moved to %q", got)
	}
	if _, err := os.Stat(filepath.Join(now, "settings.json")); err != nil {
		t.Fatalf("the files didn't move: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the old folder stays: %v", err)
	}
	// Both: Moviko's is kept, KumoBox's left alone.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := MoveOld(old, now); got != now {
		t.Fatalf("both: %q", got)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("KumoBox's folder went: %v", err)
	}
}

// The database moves with its journal; logs go; a database Moviko has
// already is never replaced.
func TestMoveOldFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("kumobox.db", "db")
	write("kumobox.db-wal", "wal")
	write("kumobox.log", "log")
	write("kumobox.log.1", "log")
	MoveOldFiles(dir)
	for name, want := range map[string]string{"moviko.db": "db", "moviko.db-wal": "wal"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
	for _, name := range []string{"kumobox.db", "kumobox.db-wal", "kumobox.log", "kumobox.log.1", "moviko.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s is there: %v", name, err)
		}
	}
	write("kumobox.db", "older")
	MoveOldFiles(dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "moviko.db")); string(b) != "db" {
		t.Fatalf("Moviko's database was replaced: %q", b)
	}
}

func TestGetenvOldName(t *testing.T) {
	t.Setenv("MOVIKO_DEVICE_NAME", "")
	t.Setenv("KUMOBOX_DEVICE_NAME", "Den TV")
	if got := Getenv("DEVICE_NAME"); got != "Den TV" {
		t.Fatalf("KUMOBOX_ only: %q", got)
	}
	t.Setenv("MOVIKO_DEVICE_NAME", "Living room")
	if got := Getenv("DEVICE_NAME"); got != "Living room" {
		t.Fatalf("both: %q", got)
	}
}

// A setting naming a file in KumoBox's data folder follows it to Moviko's.
func TestMovedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("APPDATA", filepath.Join(home, "data", "Roaming"))
	old, dir := oldDataDir(), defaultDataDir()
	s := Defaults()
	s.Mpv.Path = filepath.Join(old, "bin", "mpv")
	if movedPaths(&s) {
		t.Fatal("changed before the folder moved")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if !movedPaths(&s) || s.Mpv.Path != filepath.Join(dir, "bin", "mpv") {
		t.Fatalf("mpv: %q", s.Mpv.Path)
	}
	if movedPaths(&s) {
		t.Fatal("changed twice")
	}
}
