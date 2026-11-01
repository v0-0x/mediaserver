package util

import "runtime"

// InstallHint says how to install a program KumoBox uses, for messages.
func InstallHint(tool string) string {
	if runtime.GOOS == "android" {
		return tool + " doesn't run on Android: play from a computer's shared library"
	}
	if runtime.GOOS == "windows" {
		switch tool {
		case "ffmpeg":
			return "winget install Gyan.FFmpeg"
		case "mpv":
			return "scoop install mpv (extras bucket), or get it from mpv.io"
		case "yt-dlp":
			return "winget install yt-dlp.yt-dlp"
		}
		return "install " + tool
	}
	if runtime.GOOS == "darwin" {
		switch tool {
		case "ffmpeg", "yt-dlp":
			return "Settings › Desktop app & programs › Install downloads it"
		case "mpv":
			return "get it from mpv.io, or brew install mpv on an Apple silicon Mac"
		}
		return "install " + tool
	}
	switch tool {
	case "ffmpeg", "mpv", "yt-dlp":
		return "sudo pacman -S " + tool
	}
	return "install " + tool
}
