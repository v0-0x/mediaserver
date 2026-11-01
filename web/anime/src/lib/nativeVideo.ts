// Android's own video player (the TV app's, NativeVideo.java) in place of the
// page's. The Fire TV's WebView plays a video's sound under a grey picture,
// whatever the video (other apps' developers report grey, black or white
// pictures from about 2026-10-11, in any app); Android's player shows it.
//
// It draws on a surface behind the page, which is see-through while it plays
// (html.native-video, index.css), with the player's controls and subtitles
// over it. The player keeps its <video> element: nativeVideo() gives it this
// player's time, state and events in place of its own, so everything else in
// the player works the same with either.

import { randomId } from "./playback"
import { android, androidApp, tv } from "./tv"

// What the app says (NativeVideo.send).
type NativeEvent = {
    id: string
    type: "time" | "state" | "seeked" | "frame" | "error"
    t: number // where it is, in seconds
    d: number // how long, or -1 (not known yet, a stream being converted)
    b: number // buffered up to
    playing: boolean
    paused: boolean // not set to play
    state: number // Media3's: 1 idle, 2 buffering, 3 ready, 4 ended
    seeking: boolean
    seekable: boolean
    w: number
    h: number
    code?: number // an error's, as a video element's (2 network, 3 decode, 4 format)
    message?: string
}

declare global {
    interface Window {
        __anikoVideo?: (e: NativeEvent) => void
    }
}

const BUFFERING = 2
const READY = 3
const ENDED = 4

// The app has it (one from before 2.0.42 doesn't).
export const nativeVideoAvailable = androidApp && typeof android?.video === "function"

const KEY = "aniko-native-video"

// Used on this device: on a TV by default (turned off in the player's menu).
export function nativeVideoOn(): boolean {
    if (!nativeVideoAvailable) return false
    try {
        const v = localStorage.getItem(KEY)
        if (v === "1" || v === "0") return v === "1"
    } catch {
        /* the default */
    }
    return tv
}

export function setNativeVideo(on: boolean) {
    try {
        localStorage.setItem(KEY, on ? "1" : "0")
    } catch {
        /* for this video only */
    }
}

export type NativeVideo = {
    // Plays url (Aniko's own address) from start, paused until play().
    open(url: string, o: { start: number; hls: boolean }): void
    close(): void
}

// The video the app plays (one at a time): its events go to it.
let current: { id: string; on: (e: NativeEvent) => void } | null = null
if (nativeVideoAvailable) {
    window.__anikoVideo = e => {
        if (current && e.id === current.id) current.on(e)
    }
}

const made = new WeakMap<HTMLVideoElement, NativeVideo>()

// The player's video element, played by Android's player: its time, state
// and events are that player's from now on.
export function nativeVideo(v: HTMLVideoElement): NativeVideo {
    const had = made.get(v)
    if (had) return had

    let id = ""
    let t = 0
    let at = 0 // when t was said
    let duration = NaN
    let buffered = 0
    let playing = false
    let paused = true
    let ended = false
    let seeking = false
    let seekTo: number | null = null
    let ready = 0
    let width = 0
    let height = 0
    let seekable = false
    let frames = 0
    let error: MediaError | null = null
    let volume = 1
    let muted = false
    let rate = 1
    let waiting = false
    // Play or pause just asked: the app's word on it until it has done it
    // is about before.
    let asked: boolean | null = null
    let askedAt = 0

    const send = (o: Record<string, unknown>) => {
        if (id) android?.video?.(JSON.stringify({ id, ...o }))
    }
    const fire = (type: string) => v.dispatchEvent(new Event(type))
    // Between the app's reports (four a second): on from the last one.
    const now = () => {
        if (seekTo !== null) return seekTo
        if (!playing) return t
        const x = t + ((performance.now() - at) / 1000) * rate
        return Number.isFinite(duration) ? Math.min(x, duration) : x
    }
    const ranges = (end: () => number) =>
        ({
            get length() {
                return end() > 0 ? 1 : 0
            },
            start: () => 0,
            end: () => end(),
        }) as unknown as TimeRanges
    const prop = (name: string, get: () => unknown, set?: (x: any) => void) => Object.defineProperty(v, name, { configurable: true, get, set })
    const method = (name: string, value: (...a: any[]) => unknown) => Object.defineProperty(v, name, { configurable: true, writable: true, value })

    prop("currentTime", now, x => {
        const to = Math.max(0, Number(x) || 0)
        if (!id) {
            t = to
            return
        }
        seekTo = to
        seeking = true
        ended = false
        send({ op: "seek", t: to })
        fire("seeking")
    })
    prop("duration", () => duration)
    prop("paused", () => paused)
    prop("ended", () => ended)
    prop("seeking", () => seeking)
    prop("readyState", () => ready)
    prop("buffered", () => ranges(() => buffered))
    prop("seekable", () => ranges(() => (seekable && Number.isFinite(duration) ? duration : 0)))
    prop("videoWidth", () => width)
    prop("videoHeight", () => height)
    prop("error", () => error)
    const sendVolume = () => send({ op: "volume", volume: muted ? 0 : volume })
    prop(
        "volume",
        () => volume,
        x => {
            volume = Math.max(0, Math.min(1, Number(x)))
            sendVolume()
            fire("volumechange")
        },
    )
    prop(
        "muted",
        () => muted,
        x => {
            muted = !!x
            sendVolume()
            fire("volumechange")
        },
    )
    prop(
        "playbackRate",
        () => rate,
        x => {
            rate = Number(x) || 1
            send({ op: "rate", rate })
            fire("ratechange")
        },
    )
    method("play", () => {
        if (!id) return Promise.resolve()
        asked = true
        askedAt = performance.now()
        if (paused) {
            paused = false
            ended = false
            fire("play")
        }
        send({ op: "play" })
        return Promise.resolve()
    })
    method("pause", () => {
        if (!id) return
        asked = false
        askedAt = performance.now()
        if (!paused) {
            paused = true
            playing = false
            fire("pause")
        }
        send({ op: "pause" })
    })
    // The page's own video never has a source here.
    method("load", () => {})
    method("getVideoPlaybackQuality", () => ({ totalVideoFrames: frames, droppedVideoFrames: 0, corruptedVideoFrames: 0, creationTime: performance.now() }))

    const on = (e: NativeEvent) => {
        t = e.t
        at = performance.now()
        buffered = e.b
        seekable = e.seekable
        width = e.w
        height = e.h
        const d = e.d >= 0 ? e.d : NaN
        const newDuration = !(Number.isNaN(d) && Number.isNaN(duration)) && d !== duration
        duration = d

        if (e.type === "error") {
            const code = e.code ?? 3
            error = { code, message: e.message ?? "", MEDIA_ERR_ABORTED: 1, MEDIA_ERR_NETWORK: 2, MEDIA_ERR_DECODE: 3, MEDIA_ERR_SRC_NOT_SUPPORTED: 4 } as MediaError
            playing = false
            fire("error")
            return
        }
        if (e.type === "frame") frames++

        const wasReady = ready
        ready = e.state === READY || e.state === ENDED ? 4 : e.state === BUFFERING && ready > 0 ? 2 : ready
        if (wasReady === 0 && ready > 0) {
            fire("loadedmetadata")
            fire("loadeddata")
        }
        if (newDuration) fire("durationchange")

        if (e.type === "seeked" && seekTo !== null) {
            seekTo = null
            seeking = false
            fire("seeked")
            fire("timeupdate")
        }

        // Ended: as a video element does, paused, then "ended".
        if (e.state === ENDED) {
            playing = false
            if (!ended) {
                ended = true
                fire("timeupdate")
                if (!paused) {
                    paused = true
                    fire("pause")
                }
                fire("ended")
            }
            return
        }

        // Play or pause, once the app has done what was asked.
        if (asked !== null && (e.paused === !asked || performance.now() - askedAt > 2000)) asked = null
        if (asked === null && e.paused !== paused) {
            paused = e.paused
            fire(paused ? "pause" : "play")
        }

        if (e.state === BUFFERING && !paused) {
            if (!waiting) {
                waiting = true
                fire("waiting")
            }
        } else if (e.state === READY && (waiting || wasReady < 4)) {
            waiting = false
            fire("canplay")
            fire("canplaythrough")
        }
        if (e.playing !== playing) {
            playing = e.playing
            if (playing) fire("playing")
        }
        if (e.type === "time" && playing) fire("timeupdate")
    }

    const nv: NativeVideo = {
        open(url, o) {
            nv.close()
            id = randomId()
            t = Math.max(0, o.start)
            at = performance.now()
            duration = NaN
            buffered = 0
            playing = false
            paused = true
            ended = false
            seeking = false
            seekTo = null
            ready = 0
            width = 0
            height = 0
            frames = 0
            error = null
            waiting = false
            asked = null
            current = { id, on }
            document.documentElement.classList.add("native-video")
            send({ op: "open", url: new URL(url, location.href).href, start: t, hls: o.hls, paused: true, volume: muted ? 0 : volume, rate })
        },
        close() {
            if (!id) return
            send({ op: "close" })
            if (current?.id === id) {
                current = null
                document.documentElement.classList.remove("native-video")
            }
            id = ""
            playing = false
            paused = true
        },
    }
    made.set(v, nv)
    return nv
}
