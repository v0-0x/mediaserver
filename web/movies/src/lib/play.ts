import { useNavigate } from "react-router-dom"
import { toast } from "@/lib/toast"
import { api, desktop } from "./api"
import { isShow } from "./ids"
import { useStatus } from "./queries"
import { playerStore } from "./store"
import type { PlayResult, ProviderStream } from "./types"
import { safeUrl } from "./utils"

// The streaming provider last played from for a movie or show (remembered
// by this browser), or "".
export function rememberedProvider(mediaId: number) {
    try {
        return localStorage.getItem(`kumobox-provider-${mediaId}`) ?? ""
    } catch {
        return ""
    }
}

export function rememberProvider(mediaId: number, provider: string) {
    try {
        if (provider) localStorage.setItem(`kumobox-provider-${mediaId}`, provider)
    } catch {
        /* storage unavailable */
    }
}

// Opens a web page in the browser (the system's, from the desktop app).
// The URL can come from untrusted data (a provider's stream, an addon's
// configure page), so only real web links are ever opened.
export function openExternal(url: string) {
    const safe = safeUrl(url)
    if (!safe) return
    const d = desktop()
    if (d) d.openExternal(safe)
    else window.open(safe, "_blank", "noopener,noreferrer")
}

// The page a YouTube video or web page stream opens.
export function streamPage(s: ProviderStream) {
    return s.youTubeId ? `https://www.youtube.com/watch?v=${encodeURIComponent(s.youTubeId)}` : (s.externalUrl ?? "")
}

// Central place deciding between mpv and the in-app player.
export function usePlay() {
    const { data: status } = useStatus()
    const navigate = useNavigate()
    const settings = status?.settings
    const remote = status?.client === "lan" // LAN devices can't use this PC's mpv

    const localPlayer = (override?: "mpv" | "builtin") => (remote ? "builtin" : (override ?? settings?.playback.defaultPlayer ?? "builtin"))
    const streamPlayer = localPlayer

    const playLocal = async (path: string, mediaId: number, episode: number, opts: { player?: "mpv" | "builtin"; start?: number } = {}) => {
        if (localPlayer(opts.player) === "builtin") {
            playerStore.set({ kind: "local", path, mediaId, episode, start: opts.start })
            return
        }
        const t = toast.loading("Opening mpv…")
        try {
            await api.post("/api/playback/local", { path, mediaId, episode, player: "mpv", start: opts.start })
            toast.success("Playing in mpv", { id: t })
        } catch (e: any) {
            toast.error(e.message, { id: t })
        }
    }

    // Plays a provider's stream: a direct link in the in-app player or mpv,
    // a torrent goes to the torrent client, a YouTube video or a web page
    // opens in the browser. Without a stream, the best direct link of
    // provider ("" for any) plays.
    const playStream = async (mediaId: number, episode: number, opts: { stream?: ProviderStream; provider?: string; player?: "mpv" | "builtin"; start?: number } = {}) => {
        const s = opts.stream
        const provider = s?.provider ?? opts.provider ?? ""
        if (provider) rememberProvider(mediaId, provider)
        if (s && (s.kind === "youtube" || s.kind === "external")) {
            openExternal(streamPage(s))
            return
        }
        if (s?.kind === "torrent") return sendTorrent(mediaId, episode, s)
        if (streamPlayer(opts.player) === "builtin") {
            playerStore.set({ kind: "stream", mediaId, episode, provider, streamId: s?.id, stream: s, start: opts.start })
            return
        }
        const t = toast.loading(s ? "Opening mpv…" : "Finding a stream…")
        try {
            await api.post<PlayResult>("/api/providers/play", { mediaId, episode, streamId: s?.id, stream: s, provider, player: "mpv", start: opts.start })
            toast.success("Playing in mpv", { id: t })
        } catch (e: any) {
            toast.error(e.message, { id: t })
        }
    }

    // Sends a torrent stream to the torrent client, saved into the library.
    const sendTorrent = async (mediaId: number, episode: number, s: ProviderStream) => {
        const t = toast.loading("Sending to the torrent client…")
        try {
            const res = await api.post<PlayResult>("/api/providers/play", { mediaId, episode, streamId: s.id, stream: s })
            toast.success(res.action === "torrent" && res.savePath ? `Downloading into ${res.savePath}` : "Sent to the torrent client", { id: t })
        } catch (e: any) {
            toast.error(e.message, { id: t })
        }
    }

    // Plays an episode (key; 1 for a movie): its file when there is one,
    // otherwise the best link of the provider played from last for it (or
    // the default provider). Without one, its page opens on the streams of
    // that episode.
    const watch = async (mediaId: number, episode: number, filePath?: string | null) => {
        if (filePath) return playLocal(filePath, mediaId, episode)
        const online = settings?.onlineStream.enabled !== false
        const provider = online ? rememberedProvider(mediaId) || settings?.onlineStream.defaultProvider || "" : ""
        if (!provider) {
            navigate(`/entry?id=${mediaId}&tab=${online ? "stream" : "torrents"}${online && isShow(mediaId) && episode > 0 ? `&episode=${episode}` : ""}`)
            return
        }
        return playStream(mediaId, episode, { provider })
    }

    return { playLocal, playStream, sendTorrent, watch, localPlayer, streamPlayer, remote }
}
