import { useQueryClient } from "@tanstack/react-query"
import Hls from "hls.js"
import {
    ArrowLeft,
    Captions,
    Check,
    FastForward,
    Gauge,
    Languages,
    ListVideo,
    Loader2,
    Maximize,
    Maximize2,
    Minimize,
    MonitorPlay,
    Pause,
    Play,
    RotateCcw,
    RotateCw,
    SkipBack,
    SkipForward,
    Volume1,
    Volume2,
    VolumeX,
    PictureInPicture2,
    X,
} from "lucide-react"
import { forwardRef, memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "@/lib/toast"
import { api, ApiError, qs } from "@/lib/api"
import { nativeVideo, nativeVideoAvailable, nativeVideoOn, setNativeVideo } from "@/lib/nativeVideo"
import { forgetNoPicture, markNoPicture, pictureState, prefersHls, randomId, setSoftwareVideo, softwareVideo, videoCaps, videoFormat } from "@/lib/playback"
import { canPip, closeMini, expandMini, openMini } from "@/lib/pip"
import { useSettings } from "@/lib/queries"
import { playerStore, useStore, type PlayerRequest } from "@/lib/store"
import { formatDelay, setSubDelay, subDelay, subLine, subStyleStore, subStyleVars } from "@/lib/subtitles"
import type { EntryView, EpisodeView, Probe, StreamSource, TrackPrefs } from "@/lib/types"
import { android, androidApp, move, tv } from "@/lib/tv"
import { cn, formatDuration, img, readLocal, title as animeTitle } from "@/lib/utils"
import { parseVTT, type Cue } from "@/lib/vtt"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger } from "../ui"
import { NativeSubtitles } from "./NativeSubtitles"

type SubOption = { key: string; label: string; lang: string; title: string; index: number; url: string; bitmap?: boolean }
type Skip = { type: string; start: number; end: number }
// The AniSkip intervals the player skips: openings, endings and recaps.
const SKIPPED = ["op", "mixed-op", "ed", "mixed-ed", "recap"]
// What one is: AniSkip calls the next episode's preview a recap too (one in
// the second half of the episode is that).
const skipName = (s: Skip, length: number) =>
    s.type === "ed" || s.type === "mixed-ed" ? "ending" : s.type === "recap" ? (length > 0 && s.start > length / 2 ? "preview" : "recap") : "opening"
// Skipped once each: an episode can have two of a kind (a recap and a preview).
const skipKey = (s: Skip) => `${s.type}@${Math.round(s.start)}`

const langNames: Record<string, string> = {
    jpn: "Japanese", ja: "Japanese", eng: "English", en: "English", spa: "Spanish", es: "Spanish", fre: "French", fra: "French", fr: "French",
    ger: "German", deu: "German", de: "German", ita: "Italian", it: "Italian", por: "Portuguese", pt: "Portuguese", rus: "Russian", ru: "Russian",
    chi: "Chinese", zho: "Chinese", zh: "Chinese", kor: "Korean", ko: "Korean", ara: "Arabic", ar: "Arabic", und: "Unknown",
}
const langName = (l?: string) => (l ? langNames[l.toLowerCase()] ?? l.toUpperCase() : "")
const normLang = (l?: string) => {
    const x = (l ?? "").toLowerCase()
    const map: Record<string, string> = { ja: "jpn", jp: "jpn", en: "eng", es: "spa", fr: "fre", fra: "fre", de: "ger", deu: "ger", it: "ita", pt: "por", ru: "rus", zh: "chi", zho: "chi", ko: "kor", ar: "ara" }
    return map[x] ?? x
}
const langIn = (lang: string, list: string) => list.split(",").map(s => normLang(s.trim())).filter(Boolean).includes(normLang(lang))

// mini: the small picture-in-picture player (the desktop app's /mini page,
// lib/pip.ts).
export function PlayerOverlay({ mini = false }: { mini?: boolean }) {
    const [req, setReq] = useState<PlayerRequest | null>(playerStore.get())
    useEffect(
        () =>
            playerStore.subscribe(() => {
                // The episode that's playing asked for again: it goes on
                // playing (a new request object would reload the stream).
                const next = playerStore.get()
                setReq(prev => (prev && next && JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
            }),
        [],
    )
    // The big player plays an episode: a small one somewhere stops.
    useEffect(() => {
        if (req && !mini) closeMini()
    }, [req, mini])
    // What goes fullscreen: around the player, which is made anew for each
    // episode; the next one (or one picked in Episodes) stays fullscreen.
    const stage = useRef<HTMLDivElement>(null)
    if (!req) return null
    return (
        <div ref={stage} data-player-overlay className="fixed inset-0 z-[80] bg-black fade-in">
            <Player
                key={JSON.stringify(req)}
                req={req}
                mini={mini}
                stage={stage}
                onClose={() => {
                    playerStore.set(null)
                    if (mini) closeMini()
                }}
            />
        </div>
    )
}

function Player({ req, onClose, mini, stage }: { req: PlayerRequest; onClose: () => void; mini: boolean; stage: React.RefObject<HTMLDivElement | null> }) {
    const settings = useSettings()
    const videoRef = useRef<HTMLVideoElement>(null)
    const containerRef = useRef<HTMLDivElement>(null)
    const hlsRef = useRef<Hls | null>(null)
    const trackRef = useRef<TextTrack | null>(null)

    const [title, setTitle] = useState("")
    const [error, setError] = useState("")
    const [loading, setLoading] = useState(true)
    const [buffering, setBuffering] = useState(false)

    // local
    const [probe, setProbe] = useState<Probe | null>(null)
    const [method, setMethod] = useState<"direct" | "remux" | "transcode">("direct")
    const [offset, setOffset] = useState(0) // where the converted stream was asked to start
    const [audioIndex, setAudioIndex] = useState(0)
    // The converted stream: base is where it really starts (copied video can
    // only start at a keyframe), so subtitles and times stay in sync.
    const [stream, setStream] = useState<{ url: string; base: number; hls: boolean; id?: string } | null>(null)
    const [restart, setRestart] = useState(0)
    // A new <video> element (see the picture watchdog).
    const [videoKey, setVideoKey] = useState(0)
    const pictureTries = useRef(0)
    // Streams that stopped early, opened again (see onEnded).
    const earlyEnds = useRef(0)
    // Where the episode is at (absolute), also while the video loads: where
    // it was asked to start (see where).
    const expected = useRef(0)
    // A broken connection (Wi-Fi, the computer asleep): opened again from
    // there, a few times (see retry).
    const netRetries = useRef(0)
    const retriedAt = useRef(0)
    // Where it was when it couldn't be reached: Try again goes on from there.
    const unreachableAt = useRef<number | null>(null)
    const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
    // The video plays, since it was (re)opened: the spinner shows until then.
    const [started, setStarted] = useState(false)
    // Seeks pressed in a row (a remote's Left/Right): one restart of the
    // converted stream from where they end, not one each (see seekBy).
    const seekTarget = useRef<number | null>(null)
    const seekTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
    // How long Left/Right has been held (bigger steps).
    const held = useRef(0)
    // The second shown, and the buffered end: drawn again when they change,
    // not at each of the video's time updates (a TV is slow at it).
    const shownSecond = useRef(-1)
    const shownBuffered = useRef(-1)
    // stream
    const [sources, setSources] = useState<StreamSource[]>([])
    const [sourceIdx, setSourceIdx] = useState(0)

    const [subs, setSubs] = useState<SubOption[]>([])
    const [subKey, setSubKey] = useState<string>("off")
    // Why it didn't play, with its buttons: a TV's remote goes to them.
    const errorBox = useRef<HTMLDivElement>(null)
    useEffect(() => {
        if (!tv || !error) return
        const box = errorBox.current
        ;(box?.querySelector<HTMLElement>("[data-primary]") ?? box?.querySelector<HTMLElement>("button"))?.focus({ preventScroll: true })
    }, [error])
    // The subtitles turned off with C, for C to turn them back on.
    const lastSub = useRef<string | null>(null)
    const [cues, setCues] = useState<Cue[]>([])
    // How they look on this device; seconds they're moved (this anime's).
    const subStyle = useStore(subStyleStore)
    const [delay, setDelay] = useState(() => subDelay(req.mediaId))
    const [skips, setSkips] = useState<Skip[]>([])

    const [paused, setPaused] = useState(true)
    const [time, setTime] = useState(0) // absolute time
    const [duration, setDuration] = useState(0)
    const [buffered, setBuffered] = useState(0)
    const [volume, setVolume] = useState(() => Number(readLocal("aniko-volume") ?? 1))
    const [muted, setMuted] = useState(false)
    const [rate, setRate] = useState(1)
    const [fullscreen, setFullscreen] = useState(() => !!document.fullscreenElement)
    const [controls, setControls] = useState(true)
    const [hoverTime, setHoverTime] = useState<{ x: number; t: number } | null>(null)
    // Where Left/Right (a remote's) go, a moment: the seek bar shows it.
    const [seekShown, setSeekShown] = useState<number | null>(null)
    const seekShownTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
    // Previews on the seek bar (local files): off for this episode after a
    // few fail (no ffmpeg where the file is, say).
    const [thumbFails, setThumbFails] = useState(0)
    const [nextEp, setNextEp] = useState<{ path?: string; episode: number } | null>(null)
    // The show's episodes (the Episodes panel, where another one is picked).
    const [episodes, setEpisodes] = useState<EpisodeView[] | null>(null)
    // An episode without its own picture shows the show's.
    const [showArt, setShowArt] = useState("")
    const [showEpisodes, setShowEpisodes] = useState(false)
    // Closing: it fades out, then goes.
    const [episodesLeaving, setEpisodesLeaving] = useState(false)
    const episodesButton = useRef<HTMLButtonElement>(null)
    // Where it started from a saved position: "Start over" shows a moment.
    const [resumedFrom, setResumedFrom] = useState(0)
    // The "Up next" card put away for this episode (and no playing on).
    const [upNextOff, setUpNextOff] = useState(false)

    const startAt = useRef(0)
    const lastReport = useRef(0)
    const ended = useRef(false)
    const skipped = useRef<Set<string>>(new Set())
    const prefsRef = useRef<TrackPrefs | null>(null)
    // An online stream that's the dub.
    const dubbed = useRef(false)

    const mediaId = req.mediaId
    const episode = req.episode
    const source = req.kind === "local" ? "local" : req.provider === "ani-cli" ? "anicli" : "stream"
    // Android's own player plays the video (the TV app's, lib/nativeVideo).
    // What the page's player found showing no picture here (the WebView's
    // doing) is forgotten: it plays Matroska files as they are, too.
    const [native] = useState(() => {
        const on = nativeVideoOn()
        if (on) forgetNoPicture()
        return on
    })
    // Safari and iPhone/iPad get converted files as HLS (see lib/playback).
    const hlsMode = useMemo(() => prefersHls(), [])
    const allCaps = useMemo(() => {
        const c = videoCaps(hlsMode && Hls.isSupported() ? "mse" : "element")
        return native && !c.includes("mkv") ? [...c, "mkv"] : c
    }, [hlsMode, native])
    // Formats that showed no picture here: converted (see the picture
    // watchdog). And, last, the video converted in software rather than by
    // the computer's encoder: kept for this device once chosen.
    const [noPic, setNoPic] = useState<string[]>([])
    const caps = useMemo(() => allCaps.filter(c => !noPic.includes(c)), [allCaps, noPic])
    const [software, setSoftware] = useState(() => !native && softwareVideo())
    const clientId = useMemo(() => randomId(), [])
    const base = req.kind === "local" && method !== "direct" && stream ? stream.base : offset
    const absTime = (v: HTMLVideoElement) => v.currentTime + base
    // Where the episode is at: the video's time once it has one, and until
    // then (loading, failed before it loaded) where it was to start.
    const where = (v: HTMLVideoElement | null = videoRef.current) => (v && v.readyState >= 1 && v.currentTime > 0 ? absTime(v) : expected.current)
    // A line for the log (here, and the computer's for its files): what
    // went wrong in the player.
    const note = (text: string) => api.post("/api/playback/note", { path: req.kind === "local" ? req.path : "", text: `${title || "episode"}: ${text}` }).catch(() => {})

    // ------------------------------------------------------------------ load
    useEffect(() => {
        let cancelled = false
        ;(async () => {
            try {
                setLoading(true)
                if (req.kind === "local") {
                    const res = await api.post<{ probe: Probe; title: string; resumeAt: number; tracks: TrackPrefs | null }>("/api/playback/local", {
                        path: req.path,
                        mediaId: req.mediaId,
                        episode: req.episode,
                        player: "builtin",
                        start: req.start,
                        caps,
                    })
                    if (cancelled) return
                    prefsRef.current = res.tracks
                    setTitle(res.title)
                    setProbe(res.probe)
                    setDuration(res.probe.duration)
                    const audio = (res.probe.audio ?? []).map((a, i) => ({ index: i, lang: a.language, title: a.title }))
                    let ai = res.probe.audio?.findIndex(a => a.default) ?? 0
                    if (ai < 0) ai = 0
                    const p = res.tracks
                    // Sub/dub chosen for this anime (or a "dub" default) picks the
                    // audio of dual-audio files, unless a track was picked by hand.
                    const mode = p?.streamMode || (settings?.aniCli.defaultMode === "dub" ? "dub" : "")
                    let handAudio = !!(settings?.playback.rememberTracks && p && (p.audioLang || p.audioTitle || p.audioIndex))
                    let handSub = !!(settings?.playback.rememberTracks && p && (p.subOff || p.subLang || p.subTitle || p.subIndex))
                    // An audio track picked before a sub/dub choice that doesn't
                    // fit it (and the subtitles picked with it) gives way to the
                    // choice, in a file that has both.
                    const english = (a: { lang: string; title: string }) => isEnglishTrack(a.lang, a.title)
                    const dual = audio.some(english) && audio.some(a => !english(a))
                    if (handAudio && p && mode && dual && (p.audioLang || p.audioTitle) && isEnglishTrack(p.audioLang, p.audioTitle) !== (mode === "dub")) {
                        handAudio = false
                        handSub = false
                    }
                    let modeAudio = false
                    if (handAudio && p) {
                        const m =
                            audio.find(a => normLang(a.lang) === normLang(p.audioLang) && a.title === p.audioTitle) ??
                            audio.find(a => p.audioTitle && a.title === p.audioTitle) ??
                            audio.find(a => p.audioLang && normLang(a.lang) === normLang(p.audioLang)) ??
                            (p.audioIndex ? audio[p.audioIndex - 1] : undefined)
                        if (m) ai = m.index
                    } else if (mode && audio.length > 1 && (mode === "dub" ? audio.some(a => isEnglishTrack(a.lang, a.title)) : true)) {
                        const m =
                            mode === "dub"
                                ? audio.find(a => isEnglishTrack(a.lang, a.title))
                                : (audio.find(a => isJapaneseTrack(a.lang, a.title)) ?? audio.find(a => !isEnglishTrack(a.lang, a.title)))
                        if (m) {
                            ai = m.index
                            modeAudio = true
                        }
                    } else if (settings?.playback.preferredAudioLang) {
                        const m = audio.find(a => langIn(a.lang, settings.playback.preferredAudioLang))
                        if (m) ai = m.index
                    }
                    // Handed over from the other player: its tracks.
                    const handed = req.tracks
                    if (handed?.audio !== undefined && handed.audio < audio.length) ai = handed.audio
                    setAudioIndex(ai)
                    // Converted in software on this device (Grey picture?): always
                    // converted, never the file as it is.
                    const m =
                        !native && softwareVideo() && res.probe.video?.length ? "transcode" : ai === 0 && res.probe.method === "direct" ? "direct" : res.probe.method === "direct" ? "remux" : res.probe.method
                    setMethod(m)
                    // Ignore saved positions past the end of this file.
                    // (One handed over from the other player is where it was, also
                    // in the last seconds.)
                    startAt.current = req.start !== undefined ? Math.min(req.start, Math.max(0, res.probe.duration - 1)) : res.resumeAt && res.resumeAt < res.probe.duration - 10 ? res.resumeAt : 0
                    if (m !== "direct") setOffset(startAt.current)
                    if (startAt.current > 30 && !handed) setResumedFrom(startAt.current)

                    const subOpts: SubOption[] = [
                        ...(res.probe.subtitles ?? []).map(s => ({
                            key: `e${s.typeIndex}`,
                            label: [langName(s.language), s.title].filter(Boolean).join(" · ") || `Track ${s.typeIndex + 1}`,
                            lang: s.language,
                            title: s.title,
                            index: s.typeIndex,
                            bitmap: s.bitmap,
                            url: `/api/local/subtitle${qs({ path: req.path, index: s.typeIndex })}`,
                        })),
                        ...(res.probe.externalSubs ?? []).map((s, i) => ({
                            key: `x${i}`,
                            label: `${s.language || "External"} · ${s.name}`,
                            lang: s.language,
                            title: s.name,
                            index: 1000 + i,
                            url: `/api/local/subtitle${qs({ path: req.path, external: s.path })}`,
                        })),
                    ]
                    setSubs(subOpts)
                    if (modeAudio && !handSub && mode === "dub") {
                        // With a dub only signs & songs (on-screen text), if there are any.
                        const signs = subOpts.find(o => !o.bitmap && /sign|song|forced/i.test(o.title) && (!o.lang || langIn(o.lang, "eng,en")))
                        setSubKey(signs ? signs.key : "off")
                    } else if (modeAudio && !handSub && mode === "sub") {
                        const full = subOpts.find(o => !o.bitmap && (langIn(o.lang, "eng,en") || /english/i.test(o.title)) && !/sign|song|forced/i.test(o.title))
                        setSubKey(full ? full.key : pickSub(subOpts, null, settings?.playback.preferredSubLang ?? "", (res.probe.subtitles ?? []).find(s => s.default)?.typeIndex))
                    } else {
                        setSubKey(pickSub(subOpts, handSub ? p : null, settings?.playback.preferredSubLang ?? "", (res.probe.subtitles ?? []).find(s => s.default)?.typeIndex))
                    }
                    // On a TV, a dub starts without subtitles (also when some
                    // were kept with it): turned on by hand when wanted.
                    if (tv && audio[ai] && isEnglishTrack(audio[ai].lang, audio[ai].title)) setSubKey("off")
                    if (handed?.sub && (handed.sub === "off" || subOpts.some(o => o.key === handed.sub))) setSubKey(handed.sub)
                } else {
                    const res = await api.get<{ sources: StreamSource[]; resumeAt: number; tracks: TrackPrefs | null; title: string; dub?: boolean }>(
                        `/api/onlinestream/sources${qs({ provider: req.provider, mediaId: req.mediaId, episode: req.episode, dub: req.dub, server: req.server })}`,
                    )
                    if (cancelled) return
                    prefsRef.current = res.tracks
                    // The other version, when the one asked for has no stream.
                    const dub = res.dub ?? !!req.dub
                    dubbed.current = dub
                    if (dub !== !!req.dub) toast.info(dub ? "This episode is only dubbed: playing the dub" : "No dub of this episode: playing it with subtitles")
                    const media = await api.get<EntryView>(`/api/anime/${req.mediaId}`).catch(() => null)
                    setTitle(`${animeTitle(media?.media) || res.title || "Episode"} — Episode ${req.episode}${dub ? " (Dub)" : ""}`)
                    const sorted = [...res.sources].sort((a, b) => qualityRank(b.quality) - qualityRank(a.quality))
                    setSources(sorted)
                    const handed = req.tracks
                    const si = handed?.source !== undefined && handed.source < sorted.length ? handed.source : 0
                    setSourceIdx(si)
                    startAt.current = req.start ?? (res.resumeAt || 0)
                    if (startAt.current > 30 && !handed) setResumedFrom(startAt.current)
                    applyStreamSubs(sorted[si], handed?.sub)
                }
                // Next episode
                api.get<EntryView>(`/api/anime/${mediaId}`)
                    .then(e => {
                        if (cancelled) return
                        setEpisodes(e.episodes)
                        setShowArt(e.media.bannerImage || e.media.coverImage?.large || "")
                        const n = e.episodes.find(x => x.number === episode + 1)
                        if (!n) return
                        if (req.kind === "local") n.file && setNextEp({ path: n.file.path, episode: n.number })
                        else if (n.aired) setNextEp({ episode: n.number })
                    })
                    .catch(() => {})
            } catch (e: any) {
                if (!cancelled) setError(e.message || String(e))
            } finally {
                if (!cancelled) setLoading(false)
            }
        })()
        return () => {
            cancelled = true
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])

    // handed: the subtitles picked in the other player (picture in picture).
    // On a TV, a dub starts without them.
    const applyStreamSubs = (src?: StreamSource, handed?: string) => {
        const opts: SubOption[] = (src?.subtitles ?? []).map((s, i) => ({ key: `s${i}`, label: s.language || `Subtitle ${i + 1}`, lang: s.language, title: s.language, index: i, url: s.url }))
        setSubs(opts)
        const def = opts.find((_, i) => src?.subtitles[i].isDefault) ?? opts.find(o => /eng/i.test(o.lang))
        if (handed && (handed === "off" || opts.some(o => o.key === handed))) setSubKey(handed)
        else if (tv && dubbed.current) setSubKey("off")
        else setSubKey(pickSub(opts, prefsRef.current, settings?.playback.preferredSubLang ?? "", undefined, def?.key))
    }

    // ------------------------------------------------------------ video src
    // A converted local file: ask the server where the stream will start
    // (or start an HLS session), then load it.
    useEffect(() => {
        if (req.kind !== "local" || !probe || method === "direct") {
            setStream(null)
            return
        }
        let cancelled = false
        ;(async () => {
            try {
                if (hlsMode) {
                    const s = await api.post<{ id: string; url: string; start: number }>("/api/local/hls", {
                        path: req.path,
                        start: offset,
                        audio: audioIndex,
                        method,
                        caps,
                        client: clientId,
                    })
                    if (!cancelled) setStream({ url: s.url, base: s.start, hls: true, id: s.id })
                } else {
                    const c = caps.join(",")
                    // Where the stream starts, and how: converted when the
                    // keyframe there isn't known (a copy would start at an
                    // unknown frame), or the server didn't say.
                    const sp = await api
                        .get<{ start: number; method?: string }>(`/api/local/seekpoint${qs({ path: req.path, t: offset.toFixed(3), audio: audioIndex, method, caps: c })}`)
                        // Not reached (the stream won't be either, and is
                        // tried again): as asked.
                        .catch(e => ({ start: offset, method: e instanceof ApiError && offset > 0 ? "transcode" : undefined }))
                    if (!cancelled)
                        setStream({
                            url: `/api/local/transcode${qs({ path: req.path, start: sp.start.toFixed(3), audio: audioIndex, method: sp.method || method, caps: c, encoder: software ? "software" : undefined })}`,
                            base: sp.start,
                            hls: false,
                        })
                }
            } catch (e: any) {
                if (!cancelled) setError(e.message || String(e))
            }
        })()
        return () => {
            cancelled = true
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [req, probe, method, offset, audioIndex, restart, caps, software])

    // End the HLS session when the player closes.
    const sessionRef = useRef<string | undefined>(undefined)
    useEffect(() => {
        sessionRef.current = stream?.id
    }, [stream])
    useEffect(
        () => () => {
            if (sessionRef.current) api.del(`/api/local/hls/${sessionRef.current}`).catch(() => {})
        },
        [],
    )

    const videoSrc = useMemo(() => {
        if (req.kind === "local") {
            if (!probe) return ""
            if (method === "direct") return `/api/local/file${qs({ path: req.path })}`
            return stream?.url ?? ""
        }
        return sources[sourceIdx]?.url ?? ""
    }, [req, probe, method, stream, sources, sourceIdx])

    useEffect(() => {
        const v = videoRef.current
        if (!v || !videoSrc) return
        hlsRef.current?.destroy()
        hlsRef.current = null
        setStarted(false)
        const src = sources[sourceIdx]
        const localHls = req.kind === "local" && method !== "direct" && !!stream?.hls
        // HLS can start right where asked; the progressive stream starts at
        // its keyframe (stream.base).
        const resume = localHls ? Math.max(0, offset - stream!.base) : req.kind === "local" ? (method === "direct" ? startAt.current : 0) : startAt.current
        expected.current = (req.kind === "local" && method !== "direct" && stream ? stream.base : offset) + resume
        const isHls = localHls || (req.kind === "stream" && (src?.type === "m3u8" || /m3u8/i.test(src?.originalUrl ?? "")))
        if (native) {
            // Android's player, behind the page: HLS and all, from where asked.
            const nv = nativeVideo(v)
            nv.open(videoSrc, { start: resume, hls: isHls })
            v.volume = volume
            v.playbackRate = rate
            v.play().catch(() => {})
            return () => nv.close()
        }
        let onMeta: (() => void) | null = null
        if (isHls && Hls.isSupported()) {
            // A local session's playlist grows while it's converted: give a
            // start, or players begin at its end like a live stream.
            // Memory: hls.js keeps everything already watched (the whole
            // episode by the end); keep the last 90s for going back, and up
            // to 30 MB (at least 60s) ahead.
            const hls = new Hls({
                startPosition: localHls ? resume : resume > 0 ? resume : -1,
                maxBufferLength: 60,
                maxBufferSize: 30 * 1000 * 1000,
                backBufferLength: 90,
            })
            hls.loadSource(videoSrc)
            hls.attachMedia(v)
            let recoveries = 0
            hls.on(Hls.Events.ERROR, (_, data) => {
                if (!data.fatal) return
                // A decoding hiccup: hls.js starts the decoder over, then
                // with the audio codec swapped, before anything else.
                if (data.type === Hls.ErrorTypes.MEDIA_ERROR && recoveries < 2) {
                    if (++recoveries === 2) hls.swapAudioCodec()
                    hls.recoverMediaError()
                    return
                }
                // The connection broke: the same again from there.
                if (data.type === Hls.ErrorTypes.NETWORK_ERROR && netRetries.current < (localHls ? MAX_RETRIES : 2)) {
                    retry()
                    return
                }
                if (localHls) {
                    // Anything else: convert everything.
                    const at = where(v)
                    startAt.current = 0
                    setOffset(at)
                    if (method === "remux") setMethod("transcode")
                    else {
                        note(`the converted video can't be played here (${data.details})`)
                        setError("This video could not be played (" + data.details + ")")
                    }
                    return
                }
                if (sourceIdx + 1 < sources.length) {
                    toast.warning(`Source failed, trying ${sources[sourceIdx + 1].quality || "the next one"}…`)
                    setSourceIdx(i => i + 1)
                } else setError("The stream could not be played (" + data.details + ")")
            })
            hlsRef.current = hls
        } else {
            v.src = videoSrc
            if (resume > 0) {
                onMeta = () => {
                    v.currentTime = resume
                }
                v.addEventListener("loadedmetadata", onMeta, { once: true })
            }
        }
        v.volume = volume
        v.playbackRate = rate
        // Not allowed to play by itself: the play button shows, not the spinner.
        v.play().catch(e => e?.name === "NotAllowedError" && setStarted(true))
        return () => {
            // Not this source's start for the next one.
            if (onMeta) v.removeEventListener("loadedmetadata", onMeta)
            hlsRef.current?.destroy()
            hlsRef.current = null
            // Let go of it: a video element left with its source keeps its
            // connection (and the computer's conversion) and a decoder.
            v.pause()
            v.removeAttribute("src")
            v.load()
        }
        // A seek within one keyframe interval sets up the same stream again:
        // it still has to reload.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [videoSrc, stream, videoKey])

    // No picture: a TV's WebView sometimes plays the sound under a grey
    // picture: either no frame is shown at all, or the frames shown are one
    // flat grey (what a decoder shows with no picture to start from; seen on
    // the Fire TV with the Pi's video, 2026-10-30). Sound for a few seconds
    // with no frame shown, or only flat grey ones:
    //   1. it's opened again from there, in a new video element, as closing
    //      and opening the player would do (a first-time glitch);
    //   2. when still nothing is decoded and the video was copied (the TV
    //      said it plays that format), it's converted, and the format is
    //      converted for this device from then on (lib/playback);
    //   3. when converted video shows nothing either (a computer's encoder
    //      the TV can't take), it's converted in software, and from then on
    //      for this device (lib/playback softwareVideo);
    //   4. then it says so, and the sound plays on.
    // Each try is in the computer's log, where the file is (a note).
    useEffect(() => {
        const v = videoRef.current
        // Not for a file without video (sound only).
        // Nor with Android's player: the page doesn't draw that video.
        if (!v || !videoSrc || native || req.kind !== "local" || pictureTries.current >= 4 || (probe && !probe.video?.length)) return
        // Frames shown (requestVideoFrameCallback), or else decoded.
        const rvfc = "requestVideoFrameCallback" in v
        let frames = 0
        let handle = 0
        const onFrame = () => {
            frames++
            handle = v.requestVideoFrameCallback(onFrame)
        }
        if (rvfc) handle = v.requestVideoFrameCallback(onFrame)
        const decoded = () => ("getVideoPlaybackQuality" in v ? v.getVideoPlaybackQuality().totalVideoFrames : 1)
        const shown = () => (rvfc ? frames : decoded()) > 0
        // A small copy of the frame, to tell a picture from flat grey.
        const canvas = document.createElement("canvas")
        canvas.width = 32
        canvas.height = 18
        let grey = false
        let from = -1
        // Counted again from a seek; not while Aniko is in the background
        // (nothing is drawn then).
        const onSeek = () => void (from = -1)
        v.addEventListener("seeking", onSeek)
        const timer = setInterval(() => {
            if (v.paused || v.readyState < 3 || v.seeking || document.hidden) {
                from = -1
                return
            }
            if (shown()) {
                // A picture (or one that can't be read): nothing to do.
                if (pictureState(v, canvas) !== "grey") return clearInterval(timer)
                grey = true
            }
            if (from < 0) {
                from = v.currentTime
                return
            }
            if (v.currentTime - from < 3) return
            clearInterval(timer)
            const at = absTime(v)
            const tries = ++pictureTries.current
            const vt = probe?.video?.[0]
            const format = vt ? videoFormat(vt) : ""
            // The server copies the video when the TV says it plays it.
            const copied = !software && !!format && caps.includes(format)
            note(
                `${grey ? "only a flat grey picture" : "no picture"} after 3s of sound (try ${tries}, ${method}${software ? " software" : ""}${stream?.hls ? " hls" : ""}, ` +
                    `video ${copied ? "copied" : "converted"}, from ${Math.round(at)}s, ` +
                    `${format || "?"} ${vt?.height ?? "?"}p, ${decoded()} decoded, ${rvfc ? frames + " shown" : "no frame callback"}, ` +
                    `${v.videoWidth}x${v.videoHeight}, ready ${v.readyState})`,
            )
            const again = (next: () => void) => {
                startAt.current = 0
                setOffset(at)
                next()
                setMethod("transcode")
            }
            if (tries === 1) {
                reopen(at)
            } else if (decoded() > 0 && !grey) {
                // Frames decoded, none shown: converting wouldn't change that.
            } else if (copied) {
                toast.info("Converting the video for this device…")
                markNoPicture(format)
                again(() => setNoPic(n => [...n, format]))
            } else if (!software) {
                toast.info("Converting the video another way…")
                setSoftwareVideo(true)
                again(() => setSoftware(true))
            } else {
                pictureTries.current = 4
                toast.error("This device shows no picture of this video: only its sound plays.")
            }
        }, 500)
        return () => {
            clearInterval(timer)
            v.removeEventListener("seeking", onSeek)
            if (rvfc && handle) v.cancelVideoFrameCallback(handle)
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [videoSrc, stream, videoKey])

    // ------------------------------------------------------------- subtitles
    useEffect(() => {
        const sub = subs.find(s => s.key === subKey)
        if (!sub) {
            setCues([])
            return
        }
        if (sub.bitmap) {
            toast.warning("Image-based subtitles (PGS/VobSub) can only be shown in mpv")
            setCues([])
            return
        }
        let cancelled = false
        fetch(sub.url)
            .then(r => {
                if (!r.ok) throw new Error("subtitle unavailable")
                return r.text()
            })
            .then(t => !cancelled && setCues(parseVTT(t)))
            .catch(() => {
                if (!cancelled) {
                    setCues([])
                    toast.error("Could not load these subtitles")
                }
            })
        return () => {
            cancelled = true
        }
    }, [subKey, subs])

    // A new video element has no track yet.
    useEffect(() => {
        trackRef.current = null
    }, [videoKey])
    useEffect(() => {
        const v = videoRef.current
        if (!v) return
        if (!trackRef.current) {
            trackRef.current = v.addTextTrack("subtitles", "Aniko", "und")
        }
        const tr = trackRef.current
        // Hidden while they change: a cue removed while it shows can stay
        // drawn otherwise (Chromium, the stream started over elsewhere).
        tr.mode = "hidden"
        while (tr.cues && tr.cues.length) tr.removeCue(tr.cues[0])
        for (const c of cues) {
            const s = c.start - base + delay
            const e = c.end - base + delay
            if (e <= 0) continue
            try {
                const cue = new VTTCue(Math.max(0, s), e, c.text.replace(/<[^>]+>/g, m => (/^<\/?(i|b|u)>$/i.test(m) ? m : "")))
                cue.line = subLine(subStyle)
                tr.addCue(cue)
            } catch {
                /* ignore bad cue */
            }
        }
        // Over Android's player, the page draws them (NativeSubtitles).
        tr.mode = native ? "hidden" : "showing"
    }, [cues, base, videoKey, delay, subStyle, native])
    // Subtitles earlier or later (z / x, or the subtitles menu): kept for the
    // anime.
    const moveSubs = (by: number) => {
        const d = by === 0 ? 0 : Math.round((delay + by) * 10) / 10
        setDelay(d)
        if (mediaId > 0) setSubDelay(mediaId, d)
        toast.info(`Subtitle timing: ${formatDelay(d)}${d === 0 ? "" : d > 0 ? " (later)" : " (earlier)"}`, { id: "sub-delay" })
    }

    // -------------------------------------------------------------- progress
    const report = useCallback(
        (force = false, didEnd = false) => {
            const v = videoRef.current
            if (!v || mediaId <= 0) return
            const now = Date.now()
            if (!force && now - lastReport.current < 5000) return
            lastReport.current = now
            const position = where(v)
            const dur = duration || v.duration || 0
            if (position < 3 && !didEnd) return
            api.post("/api/playback/progress", { mediaId, episode, position, duration: dur, source, ended: didEnd, title, paused: v.paused }).catch(() => {})
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [mediaId, episode, source, duration, base, title],
    )

    const saveTrackPrefs = (next: { audio?: number; sub?: string; mode?: "sub" | "dub" }) => {
        if (mediaId <= 0) return
        if (!settings?.playback.rememberTracks) {
            if (next.mode) api.put(`/api/anime/${mediaId}/language`, { mode: next.mode }).then(languageChanged, () => {})
            return
        }
        const a = next.audio ?? audioIndex
        const sk = next.sub ?? subKey
        const audio = probe?.audio?.[a]
        const sub = subs.find(s => s.key === sk)
        const body: TrackPrefs = {
            mediaId,
            audioLang: audio?.language ?? prefsRef.current?.audioLang ?? "",
            audioTitle: audio?.title ?? prefsRef.current?.audioTitle ?? "",
            audioIndex: audio ? a + 1 : prefsRef.current?.audioIndex ?? 0,
            subLang: sub?.lang ?? "",
            subTitle: sub?.title ?? "",
            subIndex: sub ? (sub.index >= 1000 ? 0 : sub.index + 1) : 0,
            subOff: sk === "off",
            streamMode: next.mode,
        }
        prefsRef.current = body
        api.put(`/api/playback/tracks/${mediaId}`, body).then(next.mode ? languageChanged : undefined, () => {})
    }
    // The anime page shows the sub/dub picked here, and new anime start in it.
    const qc = useQueryClient()
    const languageChanged = () => {
        qc.invalidateQueries({ queryKey: ["language", mediaId] })
        qc.invalidateQueries({ queryKey: ["status"] })
    }

    // --------------------------------------------------------------- actions
    const clampTime = (t: number) => {
        const v = videoRef.current
        const total = duration || v?.duration || 0
        return Math.max(0, Math.min(total > 0 ? total - 0.5 : t, t))
    }
    // HLS can seek within what's converted so far.
    const inHls = (t: number) => {
        const v = videoRef.current
        if (!v || !stream?.hls || !v.seekable.length) return false
        const rel = t - stream.base
        return rel >= 0 && rel < v.seekable.end(v.seekable.length - 1) - 1
    }
    // Going to t starts the converted stream over.
    const restarts = (t: number) => req.kind === "local" && method !== "direct" && !inHls(t)
    const seekTo = (t: number) => {
        const v = videoRef.current
        if (!v) return
        if (seekTimer.current) clearTimeout(seekTimer.current)
        seekTimer.current = null
        seekTarget.current = null
        t = clampTime(t)
        expected.current = t
        if (req.kind === "local" && method !== "direct") {
            if (inHls(t)) {
                v.currentTime = t - stream!.base
                return
            }
            // Otherwise the stream starts over from there.
            v.pause()
            startAt.current = 0
            setStarted(false)
            setOffset(t)
            setRestart(n => n + 1)
            setTime(t)
        } else {
            v.currentTime = t
        }
    }
    const seekBy = (d: number) => {
        const v = videoRef.current
        if (!v) return
        const t = clampTime((seekTarget.current ?? where(v)) + d)
        setSeekShown(t)
        if (seekShownTimer.current) clearTimeout(seekShownTimer.current)
        seekShownTimer.current = setTimeout(() => setSeekShown(null), 1500)
        if (seekTarget.current === null && !restarts(t)) return seekTo(t)
        // A restart: a moment later, from where the presses in a row end.
        seekTarget.current = t
        setTime(t)
        if (seekTimer.current) clearTimeout(seekTimer.current)
        seekTimer.current = setTimeout(() => {
            const to = seekTarget.current
            seekTimer.current = null
            seekTarget.current = null
            if (to !== null) seekTo(to)
        }, 600)
    }
    useEffect(
        () => () => {
            if (seekTimer.current) clearTimeout(seekTimer.current)
            if (retryTimer.current) clearTimeout(retryTimer.current)
            if (seekShownTimer.current) clearTimeout(seekShownTimer.current)
        },
        [],
    )
    // The same video again from at, in a new video element (as closing and
    // opening the player would).
    const reopen = (at: number) => {
        expected.current = at
        startAt.current = method === "direct" || req.kind !== "local" ? at : 0
        if (req.kind === "local" && method !== "direct") {
            setStream(null)
            setOffset(at)
            setRestart(n => n + 1)
        }
        setVideoKey(k => k + 1)
    }
    // The connection broke (Wi-Fi, the computer asleep or gone): the same
    // again from where it was, after a moment (longer each time), rather
    // than converting it or saying it can't be played.
    const retry = (at = where()) => {
        const n = ++netRetries.current
        retriedAt.current = at
        setStarted(false)
        // Another computer's file: it may have gone to sleep. Woken up (if it
        // can be from here), it answers the next tries.
        if (n === 1 && req.kind === "local" && req.path.startsWith("kumo://")) api.post("/api/sharing/wake", { path: req.path }).catch(() => {})
        toast.info(n === 1 ? "Connection lost: trying again…" : `Still trying to reach it (${n})…`)
        if (retryTimer.current) clearTimeout(retryTimer.current)
        retryTimer.current = setTimeout(() => reopen(at), 1000 * Math.min(8, 2 ** (n - 1)))
    }
    const togglePlay = () => {
        const v = videoRef.current
        if (!v) return
        if (v.paused) v.play().catch(() => {})
        else v.pause()
    }
    const toggleFullscreen = () => {
        if (mini) return expand()
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
        else (stage.current ?? containerRef.current)?.requestFullscreen().catch(() => {})
    }
    // Picture in picture (the desktop app's, lib/pip.ts). The episode as it
    // is here, for the other player: where it's at, and the tracks.
    const pip = useMemo(() => !mini && canPip(), [mini])
    const handOver = (): PlayerRequest => {
        const tracks = { audio: audioIndex, sub: subKey, source: sourceIdx }
        const start = Math.max(0, where() - 1)
        return req.kind === "local" ? { kind: "local", path: req.path, mediaId, episode, start, tracks } : { ...req, start, tracks }
    }
    const toPip = async () => {
        const v = videoRef.current
        if (!pip || !v) return
        const handed = handOver()
        const wasPaused = v.paused
        v.pause()
        try {
            await openMini(handed)
        } catch (e: any) {
            toast.error(`Picture in picture didn't open: ${e?.message ?? e}`)
            if (!wasPaused) v.play().catch(() => {})
            return
        }
        report(true)
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
        onClose()
    }
    // The small player's way back: the big one, from where it's at.
    const expand = () => {
        const handed = handOver()
        videoRef.current?.pause()
        report(true)
        expandMini(handed)
    }
    const changeAudio = (i: number) => {
        const at = where()
        setAudioIndex(i)
        if (method === "direct") setMethod("remux")
        startAt.current = 0
        setOffset(at)
        // English or original audio, in a file that has both: that's dub or
        // sub for this anime from now on.
        const audio = probe?.audio ?? []
        const english = (a: { language: string; title: string }) => isEnglishTrack(a.language, a.title)
        const picked = audio[i]
        const mode = picked && audio.some(english) && audio.some(a => !english(a)) ? (english(picked) ? "dub" : "sub") : undefined
        // On a TV: the dub without subtitles, the original with them.
        if (tv && picked && english(picked)) {
            setSubKey("off")
            saveTrackPrefs({ audio: i, mode, sub: "off" })
            return
        }
        const full = subs.find(o => !o.bitmap && (langIn(o.lang, "eng,en") || /english/i.test(o.title)) && !/sign|song|forced/i.test(o.title))
        if (tv && mode === "sub" && subKey === "off" && full) {
            setSubKey(full.key)
            saveTrackPrefs({ audio: i, mode, sub: full.key })
            return
        }
        saveTrackPrefs({ audio: i, mode })
    }
    const changeSub = (key: string) => {
        setSubKey(key)
        saveTrackPrefs({ sub: key })
    }
    const changeSource = (i: number) => {
        const v = videoRef.current
        startAt.current = v ? v.currentTime : 0
        setSourceIdx(i)
        applyStreamSubs(sources[i])
    }
    const close = () => {
        report(true)
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
        onClose()
    }
    // The next episode. finished: this one counts as watched to the end (it
    // ended, or Up next was taken in its ending); else where it's at is saved.
    const playNext = (finished = false) => {
        if (!nextEp) return
        if (!finished) report(true)
        else if (!ended.current) {
            ended.current = true
            report(true, true)
        }
        if (req.kind === "local" && nextEp.path) playerStore.set({ kind: "local", path: nextEp.path, mediaId, episode: nextEp.episode })
        else if (req.kind === "stream") playerStore.set({ ...req, episode: nextEp.episode })
    }
    // Episodes: one that can play here (a file for a file, aired for a
    // stream), and playing it; where this one's at is saved.
    const playable = (n: EpisodeView) => (req.kind === "local" ? !!n.file : n.aired || n.hasFile)
    const playEpisode = (n: EpisodeView) => {
        setShowEpisodes(false)
        if (n.number === episode || !playable(n)) return
        report(true)
        if (req.kind === "local" && n.file) playerStore.set({ kind: "local", path: n.file.path, mediaId, episode: n.number })
        else if (req.kind === "stream") playerStore.set({ ...req, episode: n.number, start: undefined })
    }
    const toggleEpisodes = () => {
        if (showEpisodes) {
            if (episodesLeaving) return
            setEpisodesLeaving(true)
            setTimeout(() => {
                setShowEpisodes(false)
                setEpisodesLeaving(false)
            }, 160)
            if (tv) episodesButton.current?.focus({ preventScroll: true })
            return
        }
        setShowEpisodes(true)
        // Watched marks and where each was left, as they are now.
        api.get<EntryView>(`/api/anime/${mediaId}`)
            .then(e => setEpisodes(e.episodes))
            .catch(() => {})
    }
    const openInMpv = async () => {
        const v = videoRef.current
        const at = where(v)
        try {
            if (req.kind === "local") await api.post("/api/playback/local", { path: req.path, mediaId, episode, player: "mpv", start: at })
            else {
                const src = sources[sourceIdx]
                await api.post("/api/playback/stream", {
                    mediaId,
                    episode,
                    url: src.originalUrl,
                    headers: src.headers,
                    referrer: src.referrer,
                    subtitles: src.subtitles.map(s => s.original),
                    title,
                    source,
                    start: at,
                })
            }
            v?.pause()
            onClose()
        } catch (e: any) {
            toast.error(e.message)
        }
    }

    // AniSkip's times are recorded on one version of the episode and land on
    // random scenes in a longer or shorter one: ask for the ones that fit
    // this one's length, once it's known.
    const length = Number.isFinite(duration) ? Math.round(duration) : 0
    useEffect(() => {
        setSkips([])
        if (length <= 0 || mediaId <= 0 || episode <= 0) return
        let cancelled = false
        api.get<Skip[]>(`/api/playback/skips${qs({ mediaId, episode, length })}`)
            .then(s => !cancelled && setSkips(s ?? []))
            .catch(() => {})
        return () => {
            cancelled = true
        }
    }, [mediaId, episode, length])
    const skippable = skips.filter(s => SKIPPED.includes(s.type))
    const currentSkip = skippable.find(s => time >= s.start && time < s.end - 1)
    const skipWhat = currentSkip && skipName(currentSkip, length)
    const autoSkip =
        skipWhat === "ending"
            ? settings?.playback.skipOutroAniSkip
            : skipWhat === "opening"
              ? settings?.playback.skipIntroAniSkip
              : skipWhat
                ? settings?.playback.skipRecapAniSkip
                : false
    useEffect(() => {
        if (!currentSkip || !autoSkip || skipped.current.has(skipKey(currentSkip))) return
        skipped.current.add(skipKey(currentSkip))
        seekTo(currentSkip.end)
        toast.info(`Skipped ${skipWhat}`)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [currentSkip && skipKey(currentSkip), autoSkip])

    // Up next: shown from the ending (AniSkip's, when it plays rather than
    // being skipped), or the last 15 seconds. With playing on by itself, it
    // counts down to the end of the video: nothing after the ending is cut.
    const videoLength = Number.isFinite(duration) ? duration : 0
    const endingSkip = skippable.find(s => skipName(s, length) === "ending")
    const upNextAt = endingSkip && endingSkip.start > videoLength / 2 && !settings?.playback.skipOutroAniSkip ? endingSkip.start : videoLength - 15
    const showUpNext = !!nextEp && videoLength > 60 && started && !upNextOff && time >= upNextAt && time < videoLength
    const autoNext = !!settings?.playback.autoPlayNext
    const countdown = Math.max(0, Math.ceil(videoLength - time))
    // Gone back before it (rewound, also once it ended): it's shown, and
    // plays on, again.
    useEffect(() => {
        if (time >= upNextAt - 5) return
        ended.current = false
        if (upNextOff) setUpNextOff(false)
    }, [upNextOff, time, upNextAt])
    // Where it resumed, a moment: Start over.
    const showResumed = resumedFrom > 0 && started && time >= resumedFrom - 1 && time < resumedFrom + 10
    const startOver = () => {
        setResumedFrom(0)
        // The opening is skipped again, as on a first play.
        skipped.current.clear()
        seekTo(0)
    }

    // -------------------------------------------------------------- keyboard
    // Keys go to the player, not to what had the focus on the page behind it:
    // the episode card that opened it plays it again on Space or Enter.
    useEffect(() => {
        // TV: closed, the remote's focus goes back to what opened it (the
        // episode's card), not to the top of the page.
        const opener = document.activeElement as HTMLElement | null
        containerRef.current?.focus({ preventScroll: true })
        return () => {
            if (tv && opener && opener !== document.body && opener.isConnected && !opener.closest("[data-tv-scope]")) opener.focus({ preventScroll: true })
        }
    }, [])
    useEffect(() => {
        // Typing, or in one of the player's menus: the keys are theirs.
        const theirs = (t: EventTarget | null) => !!(t as HTMLElement | null)?.closest?.("input, textarea, select, [contenteditable], [role=menu], [role=listbox]")
        const onKey = (e: KeyboardEvent) => {
            if (e.ctrlKey || e.metaKey || e.altKey || theirs(e.target)) return
            const v = videoRef.current
            const box = containerRef.current
            // TV (the remote): on the picture, Left/Right seek, OK plays or
            // pauses and Up goes to the controls; on those, the arrows move
            // between them (lib/tv) and Down goes back to the picture.
            const t = e.target as HTMLElement
            // The Episodes panel: Back (and e) put it away; the arrows are
            // its list's (a TV's remote moves in it, lib/tv: it's a dialog),
            // and OK plays the one picked.
            const panel = box?.querySelector<HTMLElement>("[data-episodes]")
            if (panel) {
                if (e.key === "Escape" || e.key === "e") {
                    e.preventDefault()
                    e.stopPropagation()
                    toggleEpisodes()
                    return
                }
                if (e.key.startsWith("Arrow") || (panel.contains(t) && (e.key === "Enter" || e.key === " "))) return
            }
            const onPicture = !box || t === box || !box.contains(t) || !!t.closest("[data-tv-skip]")
            if (tv && box && e.key.startsWith("Arrow") && (!onPicture || e.key === "ArrowUp" || e.key === "ArrowDown")) {
                poke()
                if (!onPicture) {
                    if (!move(e.key === "ArrowUp" ? "up" : e.key === "ArrowDown" ? "down" : e.key === "ArrowLeft" ? "left" : "right", box) && e.key === "ArrowDown") box.focus()
                } else if (e.key === "ArrowUp") {
                    box.querySelector<HTMLElement>('[data-tv-controls="bottom"] button')?.focus()
                }
                e.preventDefault()
                e.stopPropagation()
                return
            }
            let handled = true
            switch (e.key) {
                case " ":
                case "k":
                    if (!e.repeat) togglePlay()
                    break
                case "Enter":
                    // Held down (a remote's OK): once, not again and again.
                    if (tv && onPicture && !t.closest("[data-tv-skip]")) !e.repeat && togglePlay()
                    else handled = false
                    break
                case "ArrowLeft":
                case "ArrowRight": {
                    // Held on a remote: bigger steps the longer it is.
                    held.current = e.repeat ? held.current + 1 : 0
                    const step = tv ? (held.current > 12 ? 60 : held.current > 4 ? 30 : 10) : 5
                    seekBy(e.key === "ArrowLeft" ? -step : step)
                    break
                }
                case "j":
                    seekBy(-10)
                    break
                case "l":
                    seekBy(10)
                    break
                case "ArrowUp":
                    setVolume(x => Math.min(1, x + 0.05))
                    break
                case "ArrowDown":
                    setVolume(x => Math.max(0, x - 0.05))
                    break
                case "f":
                    toggleFullscreen()
                    break
                case "p":
                    if (mini) expand()
                    else if (pip) toPip()
                    else handled = false
                    break
                case "m":
                    setMuted(m => !m)
                    break
                case "s":
                    if (currentSkip) seekTo(currentSkip.end)
                    break
                case "z":
                    if (subKey !== "off") moveSubs(-0.1)
                    break
                case "x":
                    if (subKey !== "off") moveSubs(0.1)
                    break
                case "n":
                    playNext()
                    break
                case "e":
                    if (!mini && episodes?.length) toggleEpisodes()
                    else handled = false
                    break
                // Subtitles off, and back on (the ones there were).
                case "c": {
                    const on = subs.find(s => s.key === lastSub.current && !s.bitmap) ?? subs.find(s => !s.bitmap)
                    if (subKey !== "off") {
                        lastSub.current = subKey
                        changeSub("off")
                    } else if (on) changeSub(on.key)
                    break
                }
                // Closes (like mpv's q; a remote's Stop).
                case "q":
                    close()
                    break
                case "Escape":
                    if (document.fullscreenElement) handled = false
                    // Up next showing: Back puts it away (and the episode
                    // doesn't play on).
                    else if (showUpNext) setUpNextOff(true)
                    // TV: Back hides the controls first, then closes.
                    else if (tv && controls && !paused) {
                        setControls(false)
                        box?.focus({ preventScroll: true })
                        // Not shown again (poke) by this key.
                        e.preventDefault()
                        e.stopPropagation()
                        return
                    } else close()
                    break
                default:
                    if (e.key === "0") startOver()
                    else if (/^[1-9]$/.test(e.key) && v) seekTo(((duration || v.duration) * Number(e.key)) / 10)
                    else handled = false
            }
            if (handled) {
                // The player hears keys first (capture phase): not the page
                // behind it too, nor a focused button (Space would click it).
                e.preventDefault()
                e.stopPropagation()
            }
            poke()
        }
        // Firefox clicks a focused button when Space comes back up anyway.
        const onKeyUp = (e: KeyboardEvent) => {
            if (e.key === " " && !e.ctrlKey && !e.metaKey && !e.altKey && !theirs(e.target)) e.preventDefault()
        }
        window.addEventListener("keydown", onKey, true)
        window.addEventListener("keyup", onKeyUp, true)
        return () => {
            window.removeEventListener("keydown", onKey, true)
            window.removeEventListener("keyup", onKeyUp, true)
        }
    })

    useEffect(() => {
        const v = videoRef.current
        if (!v) return
        v.volume = volume
        v.muted = muted
        try {
            localStorage.setItem("aniko-volume", String(volume))
        } catch {
            /* ignore */
        }
    }, [volume, muted, videoKey])
    useEffect(() => {
        if (videoRef.current) videoRef.current.playbackRate = rate
    }, [rate, videoKey])
    useEffect(() => {
        const onFs = () => setFullscreen(!!document.fullscreenElement)
        document.addEventListener("fullscreenchange", onFs)
        return () => document.removeEventListener("fullscreenchange", onFs)
    }, [])

    // TV: Skip opening/ending takes the remote's focus when it shows (OK
    // skips; Left/Right still seek), unless it's on the controls.
    const skipRef = useRef<HTMLButtonElement>(null)
    // Up next stands for Skip ending (and preview) while it shows: its Play now.
    const showingSkip = !!currentSkip && !(showUpNext && (skipWhat === "ending" || skipWhat === "preview"))
    useEffect(() => {
        if (!tv || !showingSkip) return
        const box = containerRef.current
        const a = document.activeElement
        if (!box || a === box || !box.contains(a)) skipRef.current?.focus({ preventScroll: true })
        return () => {
            if (document.activeElement === skipRef.current || document.activeElement === document.body) box?.focus({ preventScroll: true })
        }
    }, [showingSkip])
    // TV: Up next takes the remote's focus too (OK plays the next episode,
    // Back puts it away).
    const upNextRef = useRef<HTMLButtonElement>(null)
    useEffect(() => {
        if (!tv || !showUpNext) return
        const box = containerRef.current
        const a = document.activeElement
        if (!box || a === box || a === document.body || !box.contains(a)) upNextRef.current?.focus({ preventScroll: true })
        return () => {
            if (document.activeElement === upNextRef.current || document.activeElement === document.body) box?.focus({ preventScroll: true })
        }
    }, [showUpNext])
    // The Android app keeps the screen on while a video plays.
    useEffect(() => {
        android?.keepAwake?.(!paused)
        return () => android?.keepAwake?.(false)
    }, [paused])

    // auto-hide controls
    const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
    const poke = () => {
        setControls(true)
        if (hideTimer.current) clearTimeout(hideTimer.current)
        hideTimer.current = setTimeout(() => {
            if (videoRef.current?.paused) return
            // Not while one of its menus is open (subtitles, audio…), nor
            // Episodes (it sits on them).
            if (document.querySelector('[role="menu"], [data-episodes]')) return poke()
            setControls(false)
            // TV: the remote's focus doesn't stay on hidden buttons.
            if (tv && containerRef.current?.querySelector("[data-tv-controls]:focus-within")) containerRef.current.focus()
        }, tv ? 4000 : 2600)
    }
    useEffect(() => () => void (hideTimer.current && clearTimeout(hideTimer.current)), [])
    useEffect(() => () => report(true), [report])

    const total = duration || 0
    const pct = total > 0 ? (time / total) * 100 : 0
    // The seek bar's preview: where the mouse is over it, or where Left/Right
    // go; with a picture of the video there (each 10 seconds), for files.
    const previewAt = hoverTime ? hoverTime.t : seekShown
    const previewStep = previewAt === null ? null : Math.max(0, Math.floor(previewAt / 10) * 10)
    const thumbStep = useDebounced(previewStep, 120)
    const thumbs = req.kind === "local" && thumbFails < 3 && total > 0
    const thumbSrc = thumbs && thumbStep !== null ? `/api/local/thumb${qs({ path: req.path, t: thumbStep })}` : ""
    const aspect = probe?.video?.[0]?.width && probe.video[0].height ? probe.video[0].width / probe.video[0].height : 16 / 9
    const VolumeIcon = muted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2

    return (
        <div
            ref={containerRef}
            tabIndex={-1}
            data-tv-scope
            className={cn("fixed inset-0 z-[80] bg-black outline-none fade-in", !controls && !paused && "cursor-none")}
            onMouseMove={poke}
            onClick={e => e.target === e.currentTarget && togglePlay()}
        >
            <video
                key={videoKey}
                ref={videoRef}
                className="absolute inset-0 size-full"
                style={subStyleVars(subStyle)}
                playsInline
                // Black until the first frame (Android's WebView draws a grey
                // one with a play button otherwise).
                poster={BLANK}
                crossOrigin="anonymous"
                onClick={togglePlay}
                onDoubleClick={toggleFullscreen}
                onPlay={() => {
                    setPaused(false)
                    poke()
                }}
                onPause={() => {
                    setPaused(true)
                    setControls(true)
                    report(true)
                }}
                onWaiting={() => setBuffering(true)}
                onPlaying={() => {
                    setBuffering(false)
                    setStarted(true)
                }}
                onCanPlay={() => setBuffering(false)}
                onTimeUpdate={e => {
                    const v = e.currentTarget
                    if (v.currentTime > 0) expected.current = absTime(v)
                    // Played on well after the connection came back.
                    if (netRetries.current && Math.abs(expected.current - retriedAt.current) > 30) netRetries.current = 0
                    // Seeks waiting to happen show where they go.
                    const now = absTime(v)
                    if (seekTarget.current === null && Math.floor(now) !== shownSecond.current) {
                        shownSecond.current = Math.floor(now)
                        setTime(now)
                    }
                    const end = v.buffered.length ? v.buffered.end(v.buffered.length - 1) + base : 0
                    if (Math.abs(end - shownBuffered.current) >= 1) {
                        shownBuffered.current = end
                        setBuffered(end)
                    }
                    report()
                }}
                onLoadedMetadata={e => {
                    const v = e.currentTarget
                    if (req.kind !== "local" || method === "direct") setDuration(v.duration || duration)
                }}
                onEnded={() => {
                    if (ended.current) return
                    const v = videoRef.current
                    const at = v ? absTime(v) : 0
                    const total = duration || (v && Number.isFinite(v.duration) ? v.duration : 0)
                    // Stopped well before the episode's end: the stream broke
                    // (the computer went to sleep, its connection dropped), it
                    // didn't end. Go on from there, rather than counting the
                    // episode as watched and playing the next one.
                    if (v && total > 0 && total - at > 20 && earlyEnds.current < 3) {
                        earlyEnds.current++
                        note(`the video stopped at ${Math.round(at)}s of ${Math.round(total)}s (${method})`)
                        toast.info("The video stopped early: going on from there…")
                        reopen(at)
                        return
                    }
                    ended.current = true
                    report(true, true)
                    if (settings?.playback.autoPlayNext && nextEp && !upNextOff) playNext(true)
                }}
                onError={() => {
                    const v = videoRef.current
                    if (!v?.error) return
                    const code = v.error.code
                    const at = where(v)
                    const why = mediaErrorText(v)
                    // The browser can't tell a video it can't fetch (the
                    // computer asleep, the connection gone) from one it can't
                    // play: it's fetched to see, before trying another way.
                    const check: Promise<Fetchable> =
                        code === MediaError.MEDIA_ERR_DECODE ? Promise.resolve("ok") : code === MediaError.MEDIA_ERR_NETWORK && !hlsRef.current ? Promise.resolve("down") : fetchable(videoSrc)
                    check.then(state => {
                        // Another video by now (closed, or opened again).
                        if (videoRef.current !== v) return
                        if (state === "down") {
                            if (netRetries.current < MAX_RETRIES) retry(at)
                            else {
                                note(`the video can't be reached${why}`)
                                unreachableAt.current = at
                                setError("The video can't be reached. Is the computer that has it on, and on the network?")
                            }
                            return
                        }
                        if (req.kind === "local" && method === "direct") {
                            toast.info("Switching to compatibility mode…")
                            startAt.current = 0
                            setOffset(at)
                            setMethod("remux")
                        } else if (req.kind === "local" && method === "remux") {
                            setOffset(at)
                            setMethod("transcode")
                        } else if (req.kind === "stream" && !hlsRef.current) {
                            if (sourceIdx + 1 < sources.length) setSourceIdx(i => i + 1)
                            else setError(`This video could not be played${why}.`)
                        } else if (req.kind === "local") {
                            // Converted, and still not playable here.
                            note(`the converted video can't be played here${why}`)
                            setError(`This device can't play the converted video${why}.`)
                        }
                    })
                }}
            />

            {native && <NativeSubtitles video={videoRef} cues={cues} base={base} delay={delay} style={subStyle} lifted={controls && !mini} />}
            {(loading || buffering || !started) && !error && (
                <div className="pointer-events-none absolute inset-0 grid place-items-center">
                    <Loader2 className={cn("animate-spin text-white/80", mini ? "size-8" : "size-12")} />
                </div>
            )}
            {error && (
                <div className={cn("absolute inset-0 grid place-items-center", mini ? "p-2" : "p-6")}>
                    <div ref={errorBox} className={cn("max-w-md rounded-xl border border-line-strong bg-surface-1 text-center", mini ? "p-3" : "p-6")}>
                        <p className={cn("font-semibold", mini ? "text-sm" : "text-lg")}>Playback failed</p>
                        <p className={cn("text-muted", mini ? "mt-1 line-clamp-2 text-xs" : "mt-2 text-sm")}>{error}</p>
                        <div className={cn("flex justify-center gap-2", mini ? "mt-3" : "mt-5")}>
                            <button className="h-10 rounded-xl bg-white/10 px-4 text-sm font-medium hover:bg-white/15" onClick={close}>
                                Close
                            </button>
                            {unreachableAt.current !== null && (
                                <button
                                    data-primary
                                    className="h-10 rounded-xl bg-brand px-4 text-sm font-medium text-white"
                                    onClick={() => {
                                        const at = unreachableAt.current ?? where()
                                        unreachableAt.current = null
                                        netRetries.current = 0
                                        setError("")
                                        retry(at)
                                    }}
                                >
                                    Try again
                                </button>
                            )}
                            {(req.kind === "local" || sources.length > 0) && !androidApp && !mini && (
                                <button className="h-10 rounded-xl bg-brand px-4 text-sm font-medium text-white" onClick={openInMpv}>
                                    Open in mpv
                                </button>
                            )}
                        </div>
                    </div>
                </div>
            )}

            {/* top bar. The small window's: what plays, back to Aniko and close;
                dragging it moves the window. */}
            {mini && (
                <div
                    className={cn(
                        "absolute inset-x-0 top-0 flex items-center gap-1.5 bg-gradient-to-b from-black/80 to-transparent px-3 pt-2 pb-8 transition-opacity duration-300 [-webkit-app-region:drag]",
                        controls || paused ? "opacity-100" : "opacity-0",
                    )}
                >
                    <p className="min-w-0 flex-1 truncate text-[13px] font-semibold text-white">{title}</p>
                    <button onClick={expand} title="Back to Aniko (p)" aria-label="Back to Aniko" className="grid size-8 shrink-0 place-items-center rounded-full bg-white/10 text-white hover:bg-white/20 [-webkit-app-region:no-drag]">
                        <Maximize2 className="size-4" />
                    </button>
                    <button onClick={close} title="Close" aria-label="Close" className="grid size-8 shrink-0 place-items-center rounded-full bg-white/10 text-white hover:bg-white/20 [-webkit-app-region:no-drag]">
                        <X className="size-4" />
                    </button>
                </div>
            )}
            <div data-tv-controls="top" hidden={mini} className={cn("absolute inset-x-0 top-0 flex items-center gap-4 bg-gradient-to-b from-black/80 to-transparent px-6 pt-5 pb-14 transition-opacity duration-300", controls || paused ? "opacity-100" : "pointer-events-none opacity-0")}>
                <button onClick={close} className="grid size-10 place-items-center rounded-full bg-white/10 text-white backdrop-blur hover:bg-white/20" aria-label="Close player">
                    <ArrowLeft className="size-5" />
                </button>
                <div className="min-w-0">
                    <p className="truncate text-lg font-semibold text-white">{title}</p>
                    {req.kind === "local" && probe && (
                        <p className="text-xs text-white/60">
                            {method === "direct" ? "Direct play" : method === "remux" ? "Direct stream (remuxed)" : "Transcoding"} · {probe.video?.[0]?.codec?.toUpperCase()}{" "}
                            {probe.video?.[0]?.height ? `${probe.video[0].height}p` : ""}
                        </p>
                    )}
                    {req.kind === "stream" && sources[sourceIdx] && (
                        <p className="text-xs text-white/60">
                            {sources[sourceIdx].server} · {sources[sourceIdx].quality || "auto"}
                        </p>
                    )}
                </div>
            </div>

            {/* bottom controls */}
            <div
                data-tv-controls="bottom"
                className={cn(
                    "absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/50 to-transparent transition-opacity duration-300",
                    mini ? "px-3 pt-10 pb-1.5" : "px-6 pt-20 pb-5",
                    controls || paused ? "opacity-100" : "pointer-events-none opacity-0",
                )}
            >
                {/* scrubber */}
                <div
                    className={cn("group/seek relative cursor-pointer", mini ? "mb-1 h-4" : "mb-3 h-5")}
                    onMouseMove={e => {
                        const r = e.currentTarget.getBoundingClientRect()
                        setHoverTime({ x: e.clientX - r.left, t: ((e.clientX - r.left) / r.width) * total })
                    }}
                    onMouseLeave={() => setHoverTime(null)}
                    onClick={e => {
                        const r = e.currentTarget.getBoundingClientRect()
                        seekTo(((e.clientX - r.left) / r.width) * total)
                    }}
                >
                    <div className="absolute inset-x-0 top-1/2 h-1 -translate-y-1/2 overflow-hidden rounded-full bg-white/20 transition-all group-hover/seek:h-1.5">
                        <div className="absolute inset-y-0 left-0 bg-white/30" style={{ width: `${total ? Math.min(100, (buffered / total) * 100) : 0}%` }} />
                        {skippable.map(s => (
                            <div key={skipKey(s)} className="absolute inset-y-0 bg-amber-300/50" style={{ left: `${(s.start / total) * 100}%`, width: `${((s.end - s.start) / total) * 100}%` }} />
                        ))}
                        <div className="absolute inset-y-0 left-0 bg-brand" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="absolute top-1/2 size-3.5 -translate-x-1/2 -translate-y-1/2 scale-0 rounded-full bg-white shadow transition-transform group-hover/seek:scale-100" style={{ left: `${pct}%` }} />
                    {previewAt !== null && total > 0 && (
                        <div
                            className="pointer-events-none absolute bottom-full mb-2 flex -translate-x-1/2 flex-col items-center gap-1.5"
                            style={{
                                left: hoverTime
                                    ? `clamp(${thumbs && !mini ? 5.5 : 2}rem, ${hoverTime.x}px, calc(100% - ${thumbs && !mini ? 5.5 : 2}rem))`
                                    : `clamp(${thumbs && !mini ? 5.5 : 2}rem, ${(previewAt / total) * 100}%, calc(100% - ${thumbs && !mini ? 5.5 : 2}rem))`,
                            }}
                        >
                            {thumbs && !mini && (
                                <div className="w-44 overflow-hidden rounded-lg bg-black/80 ring-1 ring-white/15 shadow-2xl" style={{ aspectRatio: String(aspect) }}>
                                    {thumbSrc && (
                                        <img
                                            src={thumbSrc}
                                            alt=""
                                            className="size-full object-cover"
                                            onLoad={() => thumbFails && setThumbFails(0)}
                                            onError={() => setThumbFails(n => n + 1)}
                                        />
                                    )}
                                </div>
                            )}
                            <div className="rounded-md bg-black/80 px-2 py-1 text-xs font-medium text-white tabular-nums">{formatDuration(previewAt)}</div>
                        </div>
                    )}
                </div>

                <div className={cn("flex items-center text-white", mini ? "gap-0.5" : "gap-2")}>
                    <CtrlButton small={mini} onClick={togglePlay} label={paused ? "Play (k)" : "Pause (k)"}>
                        {paused ? <Play className={cn("fill-white", mini ? "size-5" : "size-6")} /> : <Pause className={cn("fill-white", mini ? "size-5" : "size-6")} />}
                    </CtrlButton>
                    {!mini && (
                        // Not on a phone, where the buttons wouldn't fit across (its
                        // own buttons set the volume).
                        <CtrlButton onClick={startOver} label="Start over (0)" className="max-sm:hidden">
                            <SkipBack className="size-5" />
                        </CtrlButton>
                    )}
                    <CtrlButton small={mini} onClick={() => seekBy(-10)} label="Back 10s (j)">
                        <RotateCcw className={mini ? "size-4" : "size-5"} />
                    </CtrlButton>
                    <CtrlButton small={mini} onClick={() => seekBy(10)} label="Forward 10s (l)">
                        <RotateCw className={mini ? "size-4" : "size-5"} />
                    </CtrlButton>
                    {nextEp && (
                        <CtrlButton small={mini} onClick={() => playNext()} label="Next episode (n)">
                            <SkipForward className={mini ? "size-4" : "size-5"} />
                        </CtrlButton>
                    )}
                    {!mini && episodes && episodes.length > 1 && (
                        <CtrlButton ref={episodesButton} onClick={toggleEpisodes} label="Episodes (e)" className={cn("data-episodes-toggle", showEpisodes && !episodesLeaving && "bg-white/15 text-brand-strong")}>
                            <ListVideo className="size-5" />
                        </CtrlButton>
                    )}
                    <div className="group/vol flex items-center max-sm:hidden">
                        <CtrlButton small={mini} onClick={() => setMuted(m => !m)} label="Mute (m)">
                            <VolumeIcon className={mini ? "size-4" : "size-5"} />
                        </CtrlButton>
                        <input
                            hidden={mini}
                            type="range"
                            min={0}
                            max={1}
                            step={0.01}
                            value={muted ? 0 : volume}
                            onChange={e => {
                                setVolume(Number(e.target.value))
                                setMuted(false)
                            }}
                            className="w-0 accent-white opacity-0 transition-all duration-200 group-hover/vol:w-24 group-hover/vol:opacity-100"
                        />
                    </div>
                    <span className={cn("font-medium text-white/85 tabular-nums", mini ? "ml-1 text-xs" : "ml-2 text-sm")}>
                        {formatDuration(time)} <span className="text-white/45">/ {formatDuration(total)}</span>
                    </span>

                    <div className="ml-auto flex items-center gap-1">
                        {/* subtitles */}
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <button className={cn("grid place-items-center rounded-full hover:bg-white/15", mini ? "size-8" : "size-10", subKey !== "off" && "text-brand-strong")} aria-label="Subtitles">
                                    <Captions className={mini ? "size-4" : "size-5"} />
                                </button>
                            </DropdownTrigger>
                            <DropdownContent className="z-[90] max-h-[min(20rem,var(--radix-dropdown-menu-content-available-height))] overflow-y-auto">
                                <DropdownLabel>Subtitles</DropdownLabel>
                                <DropdownItem onSelect={() => changeSub("off")}>{subKey === "off" ? "● " : ""}Off</DropdownItem>
                                {subs.map(s => (
                                    <DropdownItem key={s.key} onSelect={() => changeSub(s.key)} disabled={s.bitmap}>
                                        {subKey === s.key ? "● " : ""}
                                        {s.label}
                                        {s.bitmap ? " (mpv only)" : ""}
                                    </DropdownItem>
                                ))}
                                {subKey !== "off" && (
                                    <>
                                        <DropdownSeparator />
                                        <DropdownLabel>Timing: {formatDelay(delay)}</DropdownLabel>
                                        <DropdownItem keepOpen onSelect={() => moveSubs(-0.5)}>
                                            Earlier by 0.5s
                                        </DropdownItem>
                                        <DropdownItem keepOpen onSelect={() => moveSubs(-0.1)}>
                                            Earlier by 0.1s (z)
                                        </DropdownItem>
                                        <DropdownItem keepOpen onSelect={() => moveSubs(0.1)}>
                                            Later by 0.1s (x)
                                        </DropdownItem>
                                        <DropdownItem keepOpen onSelect={() => moveSubs(0.5)}>
                                            Later by 0.5s
                                        </DropdownItem>
                                        {delay !== 0 && (
                                            <DropdownItem keepOpen onSelect={() => moveSubs(0)}>
                                                Reset timing
                                            </DropdownItem>
                                        )}
                                    </>
                                )}
                            </DropdownContent>
                        </Dropdown>
                        {/* audio */}
                        {req.kind === "local" && (probe?.audio?.length ?? 0) > 1 && !mini && (
                            <Dropdown>
                                <DropdownTrigger asChild>
                                    <button className="grid size-10 place-items-center rounded-full hover:bg-white/15" aria-label="Audio track">
                                        <Languages className="size-5" />
                                    </button>
                                </DropdownTrigger>
                                <DropdownContent className="z-[90]">
                                    <DropdownLabel>Audio</DropdownLabel>
                                    {(probe?.audio ?? []).map((a, i) => (
                                        <DropdownItem key={i} onSelect={() => changeAudio(i)}>
                                            {audioIndex === i ? "● " : ""}
                                            {[langName(a.language), a.title, a.codec?.toUpperCase(), a.channels ? `${a.channels}ch` : ""].filter(Boolean).join(" · ") || `Track ${i + 1}`}
                                        </DropdownItem>
                                    ))}
                                </DropdownContent>
                            </Dropdown>
                        )}
                        {/* quality / speed */}
                        {!mini && (
                            <Dropdown>
                                <DropdownTrigger asChild>
                                    <button className="grid size-10 place-items-center rounded-full hover:bg-white/15" aria-label="Settings">
                                        <Gauge className="size-5" />
                                    </button>
                                </DropdownTrigger>
                                <DropdownContent className="z-[90] max-h-[min(24rem,var(--radix-dropdown-menu-content-available-height))] overflow-y-auto">
                                    {req.kind === "stream" && sources.length > 1 && (
                                        <>
                                            <DropdownLabel>Source</DropdownLabel>
                                            {sources.map((s, i) => (
                                                <DropdownItem key={i} onSelect={() => changeSource(i)}>
                                                    {sourceIdx === i ? "● " : ""}
                                                    {[s.server, s.quality, s.label].filter(Boolean).join(" · ")}
                                                </DropdownItem>
                                            ))}
                                            <DropdownSeparator />
                                        </>
                                    )}
                                    {req.kind === "local" && method !== "transcode" && (
                                        <>
                                            <DropdownItem
                                                onSelect={() => {
                                                    const v = videoRef.current
                                                    // The video too: copied, it would stay as it was.
                                                    const vt = probe?.video?.[0]
                                                    if (vt) setNoPic(n => [...n, videoFormat(vt)])
                                                    setOffset(v ? absTime(v) : 0)
                                                    setMethod("transcode")
                                                }}
                                            >
                                                Force transcoding
                                            </DropdownItem>
                                            <DropdownSeparator />
                                        </>
                                    )}
                                    {nativeVideoAvailable && (
                                        <>
                                            <DropdownItem
                                                onSelect={() => {
                                                    // Opened again from here, by the other player.
                                                    setNativeVideo(!native)
                                                    note(native ? "Android's video player turned off on this device" : "Android's video player turned on on this device")
                                                    playerStore.set(handOver())
                                                }}
                                            >
                                                {native ? "● Android's video player (turn off)" : "No picture? Use Android's video player"}
                                            </DropdownItem>
                                            <DropdownSeparator />
                                        </>
                                    )}
                                    {req.kind === "local" && !native && !!probe?.video?.length && (
                                        <>
                                            <DropdownItem
                                                onSelect={() => {
                                                    const v = videoRef.current
                                                    const on = !software
                                                    setSoftwareVideo(on)
                                                    note(on ? "grey picture: converted in software from now on, on this device" : "converted as before again, on this device")
                                                    if (on) toast.info("Converting the video in software on this device from now on")
                                                    setOffset(v ? absTime(v) : 0)
                                                    setSoftware(on)
                                                    setMethod("transcode")
                                                }}
                                            >
                                                {software ? "● Converted in software here (turn off)" : "Grey picture? Convert in software"}
                                            </DropdownItem>
                                            <DropdownSeparator />
                                        </>
                                    )}
                                    <DropdownLabel>Speed</DropdownLabel>
                                    {[0.75, 1, 1.25, 1.5, 2].map(r => (
                                        <DropdownItem key={r} onSelect={() => setRate(r)}>
                                            {rate === r ? "● " : ""}
                                            {r}×
                                        </DropdownItem>
                                    ))}
                                </DropdownContent>
                            </Dropdown>
                        )}
                        {!androidApp && !mini && (
                            <CtrlButton onClick={openInMpv} label="Continue in mpv">
                                <MonitorPlay className="size-5" />
                            </CtrlButton>
                        )}
                        {pip && (
                            <CtrlButton onClick={toPip} label="Picture in picture (p)">
                                <PictureInPicture2 className="size-5" />
                            </CtrlButton>
                        )}
                        {!tv && !mini && (
                            <CtrlButton onClick={toggleFullscreen} label="Fullscreen (f)">
                                {fullscreen ? <Minimize className="size-5" /> : <Maximize className="size-5" />}
                            </CtrlButton>
                        )}
                    </div>
                </div>
            </div>

            {/* skip (the next episode is in the controls, and n). Above the
                controls: their gradient reaches up behind the button and
                would otherwise take its clicks while the controls show. */}
            <div className={cn("absolute z-10 flex flex-col items-end", mini ? "right-3 bottom-14 gap-2" : "right-8 bottom-32 gap-3")}>
                {currentSkip && showingSkip && (
                    <button
                        ref={skipRef}
                        data-tv-skip
                        onClick={() => seekTo(currentSkip.end)}
                        className={cn("flex items-center gap-2 bg-white font-semibold text-black shadow-2xl rise-in hover:bg-white/90", mini ? "h-8 rounded-lg px-3 text-xs" : "h-11 rounded-xl px-5 text-sm")}
                    >
                        <FastForward className={cn("fill-black", mini ? "size-3.5" : "size-4")} /> Skip {skipWhat}
                    </button>
                )}
                {showUpNext && nextEp && (
                    <div data-tv-skip className={cn("bg-black/80 text-white shadow-2xl ring-1 ring-white/10 backdrop-blur rise-in", mini ? "w-52 rounded-xl p-2.5" : "w-72 rounded-2xl p-4")}>
                        <p className="text-xs font-medium tracking-wide text-white/60 uppercase">Up next</p>
                        <p className={cn("mt-0.5 font-semibold", mini && "text-sm")}>Episode {nextEp.episode}</p>
                        <p className={cn("mt-0.5 text-white/70 tabular-nums", mini ? "text-xs" : "text-sm")}>{autoNext ? (countdown <= 10 ? `Playing in ${countdown}s` : "Plays when this one ends") : "Ready when you are"}</p>
                        <div className={cn("flex gap-2", mini ? "mt-2" : "mt-3")}>
                            <button
                                ref={upNextRef}
                                onClick={() => playNext(true)}
                                className={cn("flex flex-1 items-center justify-center gap-2 bg-white font-semibold text-black hover:bg-white/90", mini ? "h-8 rounded-lg text-xs" : "h-10 rounded-xl text-sm")}
                            >
                                <Play className={cn("fill-black", mini ? "size-3.5" : "size-4")} /> Play now
                            </button>
                            <button onClick={() => setUpNextOff(true)} className={cn("font-medium text-white/80 hover:bg-white/10 hover:text-white", mini ? "h-8 rounded-lg px-2 text-xs" : "h-10 rounded-xl px-3 text-sm")}>
                                {autoNext ? "Cancel" : "Hide"}
                            </button>
                        </div>
                    </div>
                )}
            </div>
            {showEpisodes && episodes && !mini && (
                <EpisodesPanel
                    episodes={episodes}
                    current={episode}
                    progress={total > 0 ? Math.round((time / total) * 100) / 100 : 0}
                    art={showArt}
                    leaving={episodesLeaving}
                    playable={playable}
                    onPlay={playEpisode}
                    onClose={toggleEpisodes}
                />
            )}
            {showResumed && !mini && !showEpisodes && (
                <div className="absolute bottom-32 left-8 z-10 flex items-center gap-3 rounded-xl bg-black/75 py-2 pr-2 pl-4 text-sm text-white shadow-2xl ring-1 ring-white/10 backdrop-blur rise-in">
                    <span className="tabular-nums">Resumed at {formatDuration(resumedFrom)}</span>
                    {tv ? (
                        <span className="pr-2 text-white/60">Start over is in the controls (Up)</span>
                    ) : (
                        <button onClick={startOver} className="flex h-8 items-center gap-1.5 rounded-lg bg-white/15 px-3 font-medium hover:bg-white/25">
                            <SkipBack className="size-3.5" /> Start over
                        </button>
                    )}
                </div>
            )}
        </div>
    )
}

// value, once it stopped changing for ms.
function useDebounced<T>(value: T, ms: number): T {
    const [v, setV] = useState(value)
    useEffect(() => {
        const t = setTimeout(() => setV(value), ms)
        return () => clearTimeout(t)
    }, [value, ms])
    return v
}

// How many times a broken connection is tried again (see retry).
const MAX_RETRIES = 6

type Fetchable = "ok" | "down" | "error"

// Whether a video can be fetched (its first bytes): "down" when the
// connection fails, or the computer that has it doesn't answer.
async function fetchable(src: string): Promise<Fetchable> {
    if (!src) return "error"
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), 20_000)
    try {
        const r = await fetch(src, { headers: { Range: "bytes=0-1" }, signal: ctl.signal, credentials: "same-origin", cache: "no-store" })
        return r.ok ? "ok" : r.status >= 502 && r.status <= 504 ? "down" : "error"
    } catch {
        return "down"
    } finally {
        clearTimeout(timer)
        ctl.abort()
    }
}

// An empty picture (a transparent pixel): the video's poster.
const BLANK = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

const CtrlButton = forwardRef<HTMLButtonElement, { children: React.ReactNode; onClick: () => void; label: string; small?: boolean; className?: string }>(function CtrlButton(
    { children, onClick, label, small, className },
    ref,
) {
    return (
        <button
            ref={ref}
            onClick={onClick}
            title={label}
            aria-label={label}
            className={cn("grid shrink-0 place-items-center rounded-full transition hover:bg-white/15 active:scale-95", small ? "size-8" : "size-10", className)}
        >
            {children}
        </button>
    )
})

// The show's episodes, a small grid in the corner above the controls (the
// Episodes button's): each one's picture, number, name and how far it was
// watched, the one playing ringed in the theme's color. Picked, it plays.
// Inside the player (not a menu of the page's), so it works in fullscreen.
function EpisodesPanel({
    episodes,
    current,
    progress,
    art,
    leaving,
    playable,
    onPlay,
    onClose,
}: {
    episodes: EpisodeView[]
    current: number
    progress: number // the one playing, 0-1
    art: string
    leaving: boolean
    playable: (n: EpisodeView) => boolean
    onPlay: (n: EpisodeView) => void
    onClose: () => void
}) {
    const panel = useRef<HTMLDivElement>(null)
    const list = useRef<HTMLDivElement>(null)
    const chips = useRef<HTMLDivElement>(null)
    // The player's handlers are new each time it draws (several times a
    // second): through refs, the tiles aren't drawn again for that.
    const handlers = useRef({ onPlay, onClose })
    handlers.current = { onPlay, onClose }
    const play = useCallback((n: EpisodeView) => handlers.current.onPlay(n), [])
    // A long show a hundred at a time (a TV draws a long list slowly),
    // opening on the hundred with the one playing.
    const ranges = useMemo(() => {
        const out: EpisodeView[][] = []
        for (let i = 0; i < episodes.length; i += EPISODE_RANGE) out.push(episodes.slice(i, i + EPISODE_RANGE))
        return out
    }, [episodes])
    const [range, setRange] = useState(() => Math.max(0, ranges.findIndex(r => r.some(n => n.number === current))))
    const shown = ranges[range] ?? episodes
    // Opens at the one playing (on a TV, with the remote on it), its
    // hundred's button in sight.
    useEffect(() => {
        const chip = chips.current?.querySelector<HTMLElement>("[aria-pressed=true]")
        if (chip && chips.current) chips.current.scrollLeft = chip.offsetLeft - chips.current.clientWidth / 2 + chip.offsetWidth / 2
        const el = list.current?.querySelector<HTMLElement>("[data-current]")
        if (el && list.current) list.current.scrollTop = el.offsetTop - list.current.clientHeight / 2 + el.offsetHeight / 2
        if (tv) el?.focus({ preventScroll: true })
    }, [])
    // A click anywhere else puts it away (and does what it does there).
    useEffect(() => {
        const down = (e: PointerEvent) => {
            const t = e.target as HTMLElement | null
            if (!t || panel.current?.contains(t) || t.closest(".data-episodes-toggle")) return
            handlers.current.onClose()
        }
        document.addEventListener("pointerdown", down, true)
        return () => document.removeEventListener("pointerdown", down, true)
    }, [])
    return (
        <div
            ref={panel}
            data-episodes
            role="dialog"
            aria-label="Episodes"
            className={cn(
                "absolute bottom-[6.75rem] left-4 z-30 flex max-h-[min(30rem,calc(100%-9.5rem))] w-[clamp(19rem,24vw,30rem)] max-w-[calc(100%-2rem)] origin-bottom-left flex-col overflow-hidden rounded-2xl border border-line bg-surface-1/[0.98] text-fg shadow-2xl shadow-black/60 sm:left-6",
                leaving ? "corner-out" : "corner-in",
            )}
        >
            <div className="flex items-center gap-2 px-3.5 pt-3 pb-2">
                <h2 className="flex-1 text-sm font-semibold">
                    Episodes <span className="ml-0.5 text-xs font-normal text-subtle tabular-nums">{episodes.length}</span>
                </h2>
                <button onClick={onClose} aria-label="Close episodes" title="Close (e)" className="focus-ring grid size-7 place-items-center rounded-full text-muted transition-colors hover:bg-surface-3 hover:text-fg">
                    <X className="size-4" />
                </button>
            </div>
            {ranges.length > 1 && (
                <div ref={chips} className="no-scrollbar flex shrink-0 gap-1 overflow-x-auto scroll-smooth px-3.5 pb-2">
                    {ranges.map((r, i) => (
                        <button
                            key={i}
                            aria-pressed={i === range}
                            onClick={() => {
                                setRange(i)
                                list.current?.scrollTo({ top: 0 })
                            }}
                            className={cn(
                                "focus-ring h-6 shrink-0 rounded-full px-2.5 text-[11px] font-medium tabular-nums transition-colors",
                                i === range ? "bg-brand text-white" : "bg-surface-3 text-muted hover:text-fg",
                            )}
                        >
                            {r[0].number}–{r[r.length - 1].number}
                        </button>
                    ))}
                </div>
            )}
            <div ref={list} className="grid grid-cols-3 content-start gap-x-2 gap-y-3 overflow-y-auto overscroll-contain scroll-smooth px-3 pb-3.5">
                {shown.map(n => (
                    <EpisodeTile
                        key={n.number}
                        ep={n}
                        art={art}
                        playing={n.number === current}
                        ok={n.number === current || playable(n)}
                        progress={n.number === current ? progress : n.watched ? 1 : n.history?.duration ? n.resumeAt / n.history.duration : 0}
                        onPlay={play}
                    />
                ))}
            </div>
        </div>
    )
}

// One episode in the grid. Drawn again only when what it shows changes
// (the one playing as it goes on), not as the player ticks.
const EpisodeTile = memo(function EpisodeTile({ ep: n, art, playing, ok, progress, onPlay }: { ep: EpisodeView; art: string; playing: boolean; ok: boolean; progress: number; onPlay: (n: EpisodeView) => void }) {
    const name = n.title || `Episode ${n.number}`
    return (
        <button
            data-current={playing || undefined}
            disabled={!ok}
            onClick={() => onPlay(n)}
            title={ok ? name : `${name}: ${n.aired ? "not downloaded" : "not out yet"}`}
            className={cn("group/ep flex min-w-0 flex-col gap-1 rounded-lg p-0.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-brand", !ok && "opacity-40")}
        >
            <span
                className={cn(
                    "relative block aspect-video w-full overflow-hidden rounded-md bg-surface-3 ring-1 transition-[box-shadow,transform] duration-200",
                    playing ? "ring-2 ring-brand" : "ring-line",
                    ok && !playing && "group-hover/ep:-translate-y-0.5 group-hover/ep:ring-line-strong",
                )}
            >
                {(n.image || art) && <img src={img(n.image || art)} alt="" loading="lazy" className={cn("size-full object-cover", !n.image && "opacity-60")} />}
                <span className="absolute top-1 left-1 rounded bg-black/70 px-1 text-[10px] leading-4 font-semibold text-white tabular-nums">{n.number}</span>
                {n.watched && !playing && (
                    <span className="absolute top-1 right-1 grid size-4 place-items-center rounded-full bg-brand text-white">
                        <Check className="size-2.5 stroke-[3]" />
                    </span>
                )}
                {playing && (
                    <span className="absolute inset-0 grid place-items-center bg-black/45">
                        <Play className="size-4 fill-white text-white" />
                    </span>
                )}
                {progress > 0.01 && (
                    <span className="absolute inset-x-0 bottom-0 h-[3px] bg-black/50">
                        <span className="block h-full bg-brand transition-[width] duration-500" style={{ width: `${Math.min(100, progress * 100)}%` }} />
                    </span>
                )}
            </span>
            <span className={cn("line-clamp-2 px-0.5 text-[11px] leading-tight", playing ? "font-semibold text-fg" : "text-muted group-hover/ep:text-fg")}>{name}</span>
        </button>
    )
})

const EPISODE_RANGE = 100

function qualityRank(q: string) {
    const m = /(\d{3,4})/.exec(q ?? "")
    if (m) return Number(m[1])
    if (/auto|default|best/i.test(q ?? "")) return 5000
    return 0
}

// Why the video element gave up, as far as the browser says.
function mediaErrorText(v: HTMLVideoElement | null): string {
    const e = v?.error
    if (!e) return ""
    const kind = { 1: "stopped", 2: "network error", 3: "can't decode it", 4: "format not supported" }[e.code] ?? `error ${e.code}`
    return ` (${kind}${e.message ? `: ${e.message}` : ""})`
}

// An English (dub) or Japanese (original) track, by language tag or title —
// dual-audio releases often only name their tracks ("English 5.1").
function isEnglishTrack(lang?: string, title?: string) {
    const l = (lang ?? "").trim().toLowerCase()
    const t = (title ?? "").toLowerCase()
    if (t.includes("commentary")) return false
    return l === "eng" || l === "en" || t.includes("english") || t.includes("dub") || /\beng\b/.test(t)
}

function isJapaneseTrack(lang?: string, title?: string) {
    const l = (lang ?? "").trim().toLowerCase()
    const t = (title ?? "").toLowerCase()
    return l === "jpn" || l === "ja" || l === "jp" || t.includes("japanese") || /\b(jpn|jap)\b/.test(t)
}

function pickSub(opts: SubOption[], prefs: TrackPrefs | null, preferred: string, defaultIndex?: number, fallbackKey?: string): string {
    if (prefs?.subOff) return "off"
    if (prefs && (prefs.subLang || prefs.subTitle || prefs.subIndex)) {
        const m =
            opts.find(o => normLang(o.lang) === normLang(prefs.subLang) && o.title === prefs.subTitle) ??
            opts.find(o => prefs.subTitle && o.title === prefs.subTitle) ??
            opts.find(o => prefs.subLang && normLang(o.lang) === normLang(prefs.subLang) && !/sign|song/i.test(o.title)) ??
            (prefs.subIndex ? opts.find(o => o.index === prefs.subIndex! - 1) : undefined)
        if (m && !m.bitmap) return m.key
    }
    if (preferred) {
        const full = opts.find(o => langIn(o.lang, preferred) && !/sign|song|forced/i.test(o.title) && !o.bitmap)
        if (full) return full.key
        const any = opts.find(o => langIn(o.lang, preferred) && !o.bitmap)
        if (any) return any.key
    }
    if (fallbackKey) return fallbackKey
    if (defaultIndex !== undefined) {
        const d = opts.find(o => o.index === defaultIndex && !o.bitmap)
        if (d) return d.key
    }
    const first = opts.find(o => !o.bitmap)
    return first ? first.key : "off"
}
