package library

import (
	"testing"
	"time"
)

// StartWatcher doesn't wait for the library to be walked (a large one on a
// slow disk): the folders are found in the background, and the watcher is
// ready a moment later.
func TestStartWatcherDoesntWaitForTheWalk(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir+"/Show/01.mkv")
	s, settings := newTestScanner(t, dir)
	cfg := settings.Get()
	cfg.Library.AutoRefresh = true
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	old := walkForWatch
	walkForWatch = func(root string, patterns []string, progress func(int)) (*walkResult, error) {
		time.Sleep(1500 * time.Millisecond) // a slow disk
		return walkLibrary(root, patterns, progress)
	}
	t.Cleanup(func() { walkForWatch = old })

	start := time.Now()
	s.StartWatcher()
	t.Cleanup(s.StopWatcher)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("StartWatcher took %v (waited for the walk)", time.Since(start))
	}
	if s.WatcherReady() {
		t.Fatal("ready before the folders were walked")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !s.WatcherReady() {
		if time.Now().After(deadline) {
			t.Fatal("the watcher never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.StopWatcher()
	if s.WatcherReady() {
		t.Error("ready after it was stopped")
	}
}
