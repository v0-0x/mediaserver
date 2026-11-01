// How the in-app player gets local files that need converting.
//
// Chromium (the desktop app, Chrome, Brave, Edge) and Firefox play the
// converted file as one continuous stream. Safari, and every browser on
// iPhone and iPad (they're all Safari underneath), only play video they can
// fetch in byte ranges, so they get it as HLS: a playlist of short segments.

const ua = typeof navigator === "undefined" ? "" : navigator.userAgent

// iPadOS asks for desktop sites, so it says it's a Mac; touch gives it away.
const appleMobile = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && typeof navigator !== "undefined" && navigator.maxTouchPoints > 1)
const chromium = /Chrome\/|Chromium\/|Edg\/|OPR\/|Electron\//.test(ua) && !appleMobile
const safari = appleMobile || (/AppleWebKit\//.test(ua) && /Safari\//.test(ua) && !chromium)

// localStorage["aniko-local-stream"] = "hls" or "progressive" forces one way,
// for trying the other one out.
function override(): string {
    try {
        return localStorage.getItem("aniko-local-stream") ?? ""
    } catch {
        return ""
    }
}

export function prefersHls() {
    const o = override()
    if (o === "hls") return true
    if (o === "progressive") return false
    return safari
}

// The video formats this browser decodes, for the server to copy instead of
// converting them (HEVC on Macs and iPhones, for example). "mse" checks
// what hls.js can feed the browser, "element" what a <video> plays itself.
export function videoCaps(via: "mse" | "element"): string[] {
    const MS: any = typeof window === "undefined" ? undefined : ((window as any).ManagedMediaSource ?? window.MediaSource)
    const el = typeof document === "undefined" ? null : document.createElement("video")
    const can = (codecs: string) => {
        const type = `video/mp4; codecs="${codecs}"`
        try {
            return via === "mse" ? !!MS?.isTypeSupported?.(type) : !!el && el.canPlayType(type) !== ""
        } catch {
            return false
        }
    }
    const caps = ["h264"]
    const tests: [string, string][] = [
        ["h264-10", "avc1.6E0028"],
        ["hevc", "hvc1.1.6.L120.90"],
        ["hevc-10", "hvc1.2.4.L120.90"],
        ["av1", "av01.0.08M.08"],
        ["av1-10", "av01.0.08M.10"],
        ["vp9", "vp09.00.40.08"],
        ["vp9-10", "vp09.02.40.10"],
    ]
    const none = noPicture()
    for (const [cap, codecs] of tests) {
        // Android's WebView says yes to 10-bit H.264 and plays only its
        // sound: the TVs' decoders don't have it.
        if (cap === "h264-10" && /Android/.test(ua)) continue
        if (none.includes(cap)) continue
        if (can(codecs)) caps.push(cap)
    }
    // Chromium plays Matroska (.mkv) files as they are; Safari doesn't, nor
    // Android's (a TV's WebView): those get them repackaged into MP4.
    if (chromium && via === "element" && !/Android/.test(ua)) caps.push("mkv")
    return caps
}

// A video's format as the server names it (stream.videoFormat): "hevc-10".
export function videoFormat(v: { codec: string; profile?: string; pixFmt?: string }) {
    const deep = /p10|p12/.test(v.pixFmt ?? "") || /10/.test(v.profile ?? "")
    return deep ? `${v.codec}-10` : v.codec
}

// The formats this device said it plays and then showed no picture of (the
// player's picture watchdog): converted for it from then on. Plain H.264
// isn't put here, as everything is converted to it.
const NO_PICTURE = "aniko-no-picture"

export function noPicture(): string[] {
    try {
        const v = JSON.parse(localStorage.getItem(NO_PICTURE) ?? "[]")
        return Array.isArray(v) ? v.filter(x => typeof x === "string") : []
    } catch {
        return []
    }
}

export function markNoPicture(format: string) {
    if (format === "h264") return
    try {
        const all = noPicture()
        if (!all.includes(format)) localStorage.setItem(NO_PICTURE, JSON.stringify([...all, format]))
    } catch {
        /* not kept: asked again next time */
    }
}

// This device's video converted in software (libx264) rather than by the
// computer's or the hub's encoder, whatever it could play as is: chosen in
// the player (Grey picture?), or by the picture watchdog when the encoder's
// video showed only a flat grey here.
const SOFTWARE_VIDEO = "aniko-software-video"

export function softwareVideo(): boolean {
    try {
        return localStorage.getItem(SOFTWARE_VIDEO) === "1"
    } catch {
        return false
    }
}

export function setSoftwareVideo(on: boolean) {
    try {
        if (on) localStorage.setItem(SOFTWARE_VIDEO, "1")
        else localStorage.removeItem(SOFTWARE_VIDEO)
    } catch {
        /* not kept: asked again next time */
    }
}

// What the video shows, from a small copy of the frame: "grey" when it's one
// flat grey (what a decoder shows when it has no picture to start from),
// "picture" otherwise, "unknown" when it can't be read (drawn outside the
// page, or from another site).
export function pictureState(v: HTMLVideoElement, canvas: HTMLCanvasElement): "picture" | "grey" | "unknown" {
    try {
        const ctx = canvas.getContext("2d", { willReadFrequently: true })
        if (!ctx || !v.videoWidth) return "unknown"
        ctx.drawImage(v, 0, 0, canvas.width, canvas.height)
        const d = ctx.getImageData(0, 0, canvas.width, canvas.height).data
        let n = 0
        let sum = 0
        let sq = 0
        let chroma = 0
        for (let i = 0; i < d.length; i += 4) {
            if (d[i + 3] < 250) continue
            const y = (d[i] + d[i + 1] + d[i + 2]) / 3
            sum += y
            sq += y * y
            chroma = Math.max(chroma, Math.abs(d[i] - d[i + 1]), Math.abs(d[i + 1] - d[i + 2]), Math.abs(d[i] - d[i + 2]))
            n++
        }
        if (n < (d.length / 4) * 0.9) return "unknown"
        const mean = sum / n
        const sd = Math.sqrt(Math.max(0, sq / n - mean * mean))
        return sd < 6 && chroma < 16 && mean > 60 && mean < 200 ? "grey" : "picture"
    } catch {
        return "unknown"
    }
}

// The formats marked as showing no picture here and the software conversion,
// forgotten: they were the WebView's doing, and Android's own player plays
// the video now (lib/nativeVideo).
export function forgetNoPicture() {
    try {
        localStorage.removeItem(NO_PICTURE)
        localStorage.removeItem(SOFTWARE_VIDEO)
    } catch {
        /* nothing kept either */
    }
}

// A random id; crypto.randomUUID only exists on https and localhost, and
// devices on the home network use plain http.
export function randomId() {
    try {
        if (typeof crypto !== "undefined" && crypto.getRandomValues) {
            return Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, "0")).join("")
        }
    } catch {
        /* fall through */
    }
    return Math.random().toString(36).slice(2) + Date.now().toString(36)
}
