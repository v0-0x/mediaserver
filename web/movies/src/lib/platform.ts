// The computer Moviko runs on (status.platform: Go's GOOS), for install hints
// and examples. The server's error messages use the same commands
// (server/internal/util/hints.go).

export type Tool = "ffmpeg" | "mpv" | "yt-dlp"

export function platformName(platform?: string) {
    return platform === "windows" ? "Windows" : platform === "darwin" ? "macOS" : platform === "android" ? "Android" : platform === "mediaserver" ? "mediaserver" : "Linux"
}

export function installHint(platform: string | undefined, tool: Tool) {
    if (platform === "windows") {
        return {
            ffmpeg: "winget install Gyan.FFmpeg",
            mpv: "scoop install mpv",
            "yt-dlp": "winget install yt-dlp.yt-dlp",
        }[tool]
    }
    if (platform === "darwin") return `brew install ${tool}`
    return `sudo pacman -S ${tool}`
}

// Where the hint comes from, said before it.
export function installSource(platform: string | undefined) {
    return platform === "darwin" ? "Install it with Homebrew (brew.sh):" : "Install it with"
}

export function exampleMpvPath(platform?: string) {
    return platform === "windows" ? "C:\\Program Files\\mpv\\mpv.exe" : platform === "darwin" ? "/opt/homebrew/bin/mpv" : "/usr/bin/mpv"
}
