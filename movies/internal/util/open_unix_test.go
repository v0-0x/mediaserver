//go:build !windows

package util

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// macOS's open starts an app when it's given one (a folder named
// Something.app) instead of showing it: those are shown in Finder, selected.
func TestOpenerShowsAppsInsteadOfStartingThem(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "The.Movie.2010.1080p")
	app := filepath.Join(root, "Something.app")
	link := filepath.Join(root, "Movie")
	for _, d := range []string{plain, app} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		goos, target, name string
		args               []string
	}{
		{"darwin", plain, "open", []string{plain}},
		{"darwin", app, "open", []string{"-R", app}},
		{"darwin", app + "/", "open", []string{"-R", app + "/"}},
		{"darwin", link, "open", []string{"-R", link}},
		{"darwin", "https://example.com/a.app", "open", []string{"https://example.com/a.app"}},
		{"linux", app, "xdg-open", []string{app}},
	} {
		name, args := opener(c.goos, c.target)
		if name != c.name || !slices.Equal(args, c.args) {
			t.Errorf("%s %s: %s %q, want %s %q", c.goos, c.target, name, args, c.name, c.args)
		}
	}
}
