import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"
import type { FuzzyDate, Media } from "./types"
import { tv } from "./tv"

export function cn(...inputs: ClassValue[]) {
    return twMerge(clsx(inputs))
}

// The title the app shows (in the metadata language), then the original one.
export function title(m?: Media | null) {
    if (!m) return ""
    return m.title.userPreferred || m.title.english || m.title.romaji || m.title.native || ""
}

// The original title when it differs from the one shown.
export function originalTitle(m?: Media | null) {
    if (!m) return ""
    const t = m.title.native || m.title.romaji || ""
    return t && t !== title(m) ? t : ""
}

// safeUrl returns url only when it's safe to use as a link or a window to
// open — http(s), mailto, or a relative/in-page path — and "" otherwise.
// Untrusted content (plugin UI, extension/addon manifests, provider data)
// can supply "javascript:", "data:", "blob:" or "file:" URLs that would run
// code on the app's origin or reach the shell; this keeps those out of href,
// window.open and openExternal.
export function safeUrl(url?: string | null): string {
    if (!url) return ""
    const u = url.trim()
    // Relative links and in-page anchors stay on our own origin.
    if (/^[/.#?]/.test(u)) return u
    try {
        const scheme = new URL(u, location.origin).protocol
        return scheme === "http:" || scheme === "https:" || scheme === "mailto:" ? u : ""
    } catch {
        return ""
    }
}

// Artwork is downloaded once by the server and served from its local cache
// (/api/img), so posters keep working offline.
export function img(url?: string | null) {
    if (!url) return ""
    if (url.startsWith("http://") || url.startsWith("https://")) return `/api/img?u=${encodeURIComponent(url)}`
    return url
}

// A cover the size it's shown: "poster" (a title's page), "card" (a grid's;
// on a TV the smaller one does, at its scale, and takes a quarter of the
// memory), "thumb" (small, in lists).
export function cover(m?: Media | null, size: "poster" | "card" | "thumb" = "card") {
    const c = m?.coverImage
    const big = size === "poster" || (size === "card" && !tv)
    return img((big ? c?.extraLarge || c?.large : c?.large || c?.extraLarge) || c?.medium || "")
}

// The poster attributes of an <img> shown `width` CSS pixels wide: the sizes
// TMDB has (w342, w500, w780) in srcSet, so the browser loads the smallest
// one that is sharp on this screen. Cards and thumbnails don't need the
// biggest: decoded, a w780 poster takes 3.6 MB of memory, a w342 one 0.7 MB.
export function poster(m: Media | null | undefined, width: number) {
    const c = m?.coverImage
    const sizes: [string | undefined, number][] = [
        [c?.medium, 342],
        [c?.large, 500],
        [c?.extraLarge, 780],
    ]
    const set = sizes.filter(([u]) => u).map(([u, w]) => `${img(u)} ${w}w`)
    if (set.length < 2) return { src: cover(m) }
    return { src: img(c?.large || c?.extraLarge), srcSet: set.join(", "), sizes: `${width}px`, onError: withoutSrcSet }
}

// The attributes of an <img> showing a TMDB backdrop (w1280) `width` CSS
// pixels wide: with the smaller sizes TMDB has (w300, w780), for the
// browser to load the one the screen needs. Decoded, a w1280 backdrop takes
// 3.7 MB of memory, a w300 one 0.3 MB.
export function backdrop(url: string | undefined, width: number) {
    const full = img(url)
    if (!url || !url.includes("/w1280/")) return { src: full }
    const at = (size: string) => img(url.replace("/w1280/", `/${size}/`))
    return { src: full, srcSet: `${at("w300")} 300w, ${at("w780")} 780w, ${full} 1280w`, sizes: `${width}px`, onError: withoutSrcSet }
}

// A size picked from srcSet that can't be loaded (offline, and never
// downloaded) falls back to src: the size Moviko keeps for offline use.
function withoutSrcSet(e: React.SyntheticEvent<HTMLImageElement>) {
    e.currentTarget.removeAttribute("srcset")
}

export function banner(m?: Media | null) {
    return img(m?.bannerImage || m?.coverImage?.extraLarge || "")
}

// The release (first air) year, or 0.
export function year(m?: Media | null) {
    return m?.seasonYear || m?.startDate?.year || 0
}

// The release (first air) date as YYYY-MM-DD, "" when unknown: sorts by date.
export const releaseDateOf = (m?: Media | null) => fuzzyDate(m?.startDate)

export function fuzzyDate(d?: FuzzyDate | null) {
    if (!d?.year) return ""
    return `${d.year}-${String(d.month ?? 1).padStart(2, "0")}-${String(d.day ?? 1).padStart(2, "0")}`
}

// "2026-03-14" as the user's locale writes it ("Mar 14, 2026").
export function formatDate(iso?: string, opts: Intl.DateTimeFormatOptions = { year: "numeric", month: "short", day: "numeric" }) {
    if (!iso) return ""
    const d = new Date(iso.length === 10 ? iso + "T00:00:00" : iso)
    return isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, opts)
}

// Today as YYYY-MM-DD, in the user's timezone.
export function today() {
    const d = new Date()
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
}

// 134 minutes → "2h 14m".
export function runtimeLabel(min?: number | null) {
    if (!min || min <= 0) return ""
    const h = Math.floor(min / 60)
    const m = min % 60
    return h > 0 ? `${h}h${m ? ` ${m}m` : ""}` : `${m}m`
}

const FORMATS: Record<string, string> = {
    MOVIE: "Movie",
    TV: "TV series",
    MINISERIES: "Miniseries",
}
export const formatLabel = (f?: string) => (f ? (FORMATS[f] ?? f.charAt(0) + f.slice(1).toLowerCase()) : "")

// "Movie · 2010 · 2h 28m", "TV series · 2019 · 24 episodes".
export function mediaSummary(m: Media) {
    const extra = m.type === "TV" ? (m.episodes ? plural(m.episodes, "episode") : "") : runtimeLabel(m.duration)
    return [formatLabel(m.format), year(m) || "", extra].filter(Boolean).join(" · ")
}

// A media's status in movie and TV words: a movie is released or upcoming,
// a show airing, ended or cancelled.
export function statusLabel(s?: string, type?: string) {
    switch (s) {
        case "FINISHED":
            return type === "TV" ? "Ended" : "Released"
        case "RELEASING":
            return type === "MOVIE" ? "In theaters" : "Airing"
        case "NOT_YET_RELEASED":
            return "Upcoming"
        case "CANCELLED":
            return "Cancelled"
        default:
            return s ? s.charAt(0) + s.slice(1).toLowerCase() : ""
    }
}

// List statuses keep AniList's codes; the UI names them in movie words.
export const LIST_STATUS: Record<string, string> = {
    CURRENT: "Watching",
    REPEATING: "Rewatching",
    PLANNING: "Watchlist",
    PAUSED: "Paused",
    COMPLETED: "Watched",
    DROPPED: "Dropped",
}

// The Home page's sections (settings.ui.homeSections lists the ones shown,
// in order; the ids are Kumo's).
export const HOME_SECTIONS: { id: string; label: string; help: string }[] = [
    { id: "continue", label: "Continue watching", help: "Movies you stopped and the next episodes of your shows" },
    { id: "genres", label: "Genres", help: "Filter the rows of your list by genre" },
    { id: "watching", label: "Watching", help: "What you're watching and rewatching" },
    { id: "airing", label: "Airing shows", help: "Shows on your list with episodes coming out" },
    { id: "planning", label: "Watchlist", help: "What you plan to watch" },
    { id: "completed", label: "Recently watched", help: "What you finished last" },
]

// Original languages Search filters by (ISO 639-1).
export const LANGUAGES: { value: string; label: string }[] = [
    ["en", "English"],
    ["fr", "French"],
    ["es", "Spanish"],
    ["de", "German"],
    ["it", "Italian"],
    ["pt", "Portuguese"],
    ["ja", "Japanese"],
    ["ko", "Korean"],
    ["zh", "Chinese"],
    ["hi", "Hindi"],
    ["sv", "Swedish"],
    ["da", "Danish"],
    ["no", "Norwegian"],
    ["nl", "Dutch"],
    ["pl", "Polish"],
    ["ru", "Russian"],
    ["tr", "Turkish"],
    ["th", "Thai"],
].map(([value, label]) => ({ value, label }))

export const languageName = (code?: string) => (code ? (LANGUAGES.find(l => l.value === code)?.label ?? code.toUpperCase()) : "")

// Scores are 0-100; Letterboxd shows 0.5-5 stars, a half star per 10 points.
export const scoreToStars = (score?: number | null) => Math.round((score ?? 0) / 10) / 2
export const starsToScore = (stars: number) => Math.round(stars * 2) * 10

// "★★★½" for a 0-100 score, "" for none.
export function starText(score?: number | null) {
    const stars = scoreToStars(score)
    if (stars <= 0) return ""
    return "★".repeat(Math.floor(stars)) + (stars % 1 ? "½" : "")
}

export function formatDuration(sec: number) {
    if (!isFinite(sec) || sec < 0) sec = 0
    const h = Math.floor(sec / 3600)
    const m = Math.floor((sec % 3600) / 60)
    const s = Math.floor(sec % 60)
    if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`
    return `${m}:${String(s).padStart(2, "0")}`
}

export function formatBytes(b: number) {
    if (!b) return "0 B"
    const u = ["B", "KB", "MB", "GB", "TB"]
    const i = Math.min(Math.floor(Math.log(b) / Math.log(1024)), u.length - 1)
    return `${(b / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${u[i]}`
}

export function formatSpeed(b: number) {
    return b > 0 ? `${formatBytes(b)}/s` : "—"
}

export function timeUntil(seconds: number) {
    if (seconds <= 0) return "now"
    const d = Math.floor(seconds / 86400)
    const h = Math.floor((seconds % 86400) / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    if (d > 0) return `${d}d ${h}h`
    if (h > 0) return `${h}h ${m}m`
    return `${m}m`
}

// When an episode airs, from its day (TMDB only has air dates): "today",
// "tomorrow", "in 3 days", or the date.
export function airsIn(airingAt: number) {
    const day = new Date(airingAt * 1000)
    const start = new Date()
    start.setHours(0, 0, 0, 0)
    const local = new Date(day.getUTCFullYear(), day.getUTCMonth(), day.getUTCDate())
    const days = Math.round((local.getTime() - start.getTime()) / 86400_000)
    if (days <= 0) return "today"
    if (days === 1) return "tomorrow"
    if (days < 7) return `in ${days} days`
    return local.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

export function relativeTime(unix: number) {
    const diff = Date.now() / 1000 - unix
    if (diff < 60) return "just now"
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    if (diff < 86400 * 30) return `${Math.floor(diff / 86400)}d ago`
    return new Date(unix * 1000).toLocaleDateString()
}

// Compares two Moviko versions (2.0.<commits since 2.0.0's>, 1.0.<number
// of commits> before): null when either isn't one, or is 1.0.0 (built by
// hand: its version is unknown).
export function compareVersions(a?: string, b?: string): number | null {
    const parse = (v?: string) => {
        const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(v ?? "")
        return m && v !== "1.0.0" ? m.slice(1).map(Number) : null
    }
    const x = parse(a)
    const y = parse(b)
    if (!x || !y) return null
    for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1
    return 0
}

// Overviews can carry a little HTML (<br>, <i>, <b>) or entities.
export function cleanDescription(html?: string) {
    if (!html) return ""
    return html
        .replace(/<br\s*\/?>(\s*<br\s*\/?>)*/gi, "\n")
        .replace(/<\/?(i|b|em|strong)>/gi, "")
        .replace(/<[^>]+>/g, "")
        .replace(/&quot;/g, '"')
        .replace(/&amp;/g, "&")
        .replace(/&#039;/g, "'")
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .trim()
}

export function scoreColor(score?: number | null) {
    if (!score) return "text-muted"
    if (score >= 75) return "text-emerald-400"
    if (score >= 65) return "text-lime-300"
    if (score >= 50) return "text-amber-300"
    return "text-rose-400"
}

export function hexToRgb(hex: string) {
    const m = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex)
    return m ? `${parseInt(m[1], 16)} ${parseInt(m[2], 16)} ${parseInt(m[3], 16)}` : "124 108 242"
}

// Copies text to the clipboard. The Clipboard API only exists on secure pages
// (https, localhost), so plain-http LAN addresses fall back to a hidden
// textarea. Resolves false when nothing worked.
export async function copyText(text: string) {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(text)
            return true
        }
    } catch {
        /* blocked: try the fallback */
    }
    const prev = document.activeElement as HTMLElement | null
    const ta = document.createElement("textarea")
    ta.value = text
    ta.setAttribute("readonly", "")
    ta.style.position = "fixed"
    ta.style.top = "0"
    ta.style.opacity = "0"
    document.body.appendChild(ta)
    ta.focus({ preventScroll: true })
    ta.select()
    ta.setSelectionRange(0, text.length) // iOS ignores select()
    try {
        return document.execCommand("copy")
    } catch {
        return false
    } finally {
        ta.remove()
        prev?.focus({ preventScroll: true })
    }
}

// "1 file", "2 files".
export const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`

// The value that occurs most often (the first one on a tie), or "".
export function mostCommon(xs: string[]) {
    const counts = new Map<string, number>()
    let best = ""
    for (const x of xs) {
        const n = (counts.get(x) ?? 0) + 1
        counts.set(x, n)
        if (n > (counts.get(best) ?? 0)) best = x
    }
    return best
}

export function entryUrl(m: { id: number }) {
    return `/entry?id=${m.id}`
}

// Links to the same title elsewhere: TMDB, IMDb and, for movies, Letterboxd
// (which finds films by their TMDB id).
export function externalLinks(m: Media) {
    const show = m.type === "TV"
    const out: { label: string; url: string }[] = []
    out.push({ label: "TMDB", url: m.siteUrl || `https://www.themoviedb.org/${show ? "tv" : "movie"}/${m.tmdbId}` })
    if (m.imdbId) out.push({ label: "IMDb", url: `https://www.imdb.com/title/${m.imdbId}/` })
    if (!show && m.tmdbId) out.push({ label: "Letterboxd", url: `https://letterboxd.com/tmdb/${m.tmdbId}/` })
    return out
}

// This browser's storage, read and written without failing when it can't be
// (site data blocked, a private window): null, and nothing kept, then.
export function readLocal(key: string): string | null {
    try {
        return localStorage.getItem(key)
    } catch {
        return null
    }
}
export function writeLocal(key: string, value: string) {
    try {
        localStorage.setItem(key, value)
    } catch {
        /* not kept */
    }
}
