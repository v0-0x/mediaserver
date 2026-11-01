//go:build !windows && !darwin

package config

import (
	"os"
	"path/filepath"
	"syscall"
)

// videosFolder is the home folder for videos: the library's default
const videosFolder = "Videos"

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "kumobox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "kumobox")
}

func runtimeDir() string {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return filepath.Join(base, "kumobox")
	}
	// The shared temporary folder, where the desktop app looks too. Another
	// user may have made the folder first (or put a symlink there): the
	// shell token never goes into a folder that isn't this user's alone.
	dir := filepath.Join(os.TempDir(), "kumobox")
	if ownPrivateDir(dir) {
		return dir
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = defaultDataDir()
	}
	return filepath.Join(base, "kumobox", "run")
}

// ownPrivateDir makes dir when it's missing and reports whether it's a
// folder (not a symlink) of this user's that only they can open, closing it
// to others when it isn't yet.
func ownPrivateDir(dir string) bool {
	_ = os.Mkdir(dir, 0o700)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || sys.Uid != uint32(os.Getuid()) {
		return false
	}
	if st.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700) == nil
	}
	return true
}

// findQbittorrent prefers a native qBittorrent and falls back to the
// Flatpak export (system-wide or per-user install).
func findQbittorrent(home string) string {
	for _, p := range []string{
		"/usr/bin/qbittorrent",
		"/var/lib/flatpak/exports/bin/org.qbittorrent.qBittorrent",
		filepath.Join(home, ".local/share/flatpak/exports/bin/org.qbittorrent.qBittorrent"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/bin/qbittorrent"
}

func defaultTransmission() string { return "/usr/bin/transmission-gtk" }
