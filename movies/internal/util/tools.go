package util

import "sync"

var (
	toolsMu  sync.Mutex
	toolsDir string
)

// SetToolsDir sets the folder where KumoBox puts programs itself (the macOS
// programs setup: ffmpeg, yt-dlp, mpv). On macOS it goes first on the
// PATH, for KumoBox's lookups and the programs it starts; it needn't exist
// yet.
func SetToolsDir(dir string) {
	toolsMu.Lock()
	toolsDir = dir
	toolsMu.Unlock()
	addToolsDir(dir)
}

// ToolsDir is the folder SetToolsDir set, or "".
func ToolsDir() string {
	toolsMu.Lock()
	defer toolsMu.Unlock()
	return toolsDir
}
