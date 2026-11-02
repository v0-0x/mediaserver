// Tiny fetch wrapper for the Moviko API.

export class ApiError extends Error {
    status: number
    data: any
    constructor(message: string, status: number, data: any) {
        super(message)
        this.status = status
        this.data = data
    }
}

// Told when the server asks for its password.
type Listener = () => void
const authListeners = new Set<Listener>()
export function onPasswordRequired(fn: Listener) {
    authListeners.add(fn)
    return () => authListeners.delete(fn)
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    // FormData (file uploads) goes as multipart: the browser sets the type.
    const form = body instanceof FormData
    const res = await fetch(path, {
        method,
        headers: body !== undefined && !form ? { "Content-Type": "application/json" } : undefined,
        body: body === undefined ? undefined : form ? body : JSON.stringify(body),
        credentials: "same-origin",
    })
    const text = await res.text()
    let data: any = null
    try {
        data = text ? JSON.parse(text) : null
    } catch {
        data = text
    }
    if (!res.ok) {
        if (res.status === 401 && data?.passwordRequired) authListeners.forEach(fn => fn())
        const msg = (data && typeof data === "object" && data.error) || (typeof data === "string" && data) || res.statusText
        throw new ApiError(msg, res.status, data)
    }
    return data as T
}

export const api = {
    get: <T>(path: string) => request<T>("GET", path),
    post: <T>(path: string, body: unknown = {}) => request<T>("POST", path, body),
    put: <T>(path: string, body: unknown) => request<T>("PUT", path, body),
    patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
    del: <T>(path: string) => request<T>("DELETE", path),
    // A multipart upload (e.g. a Letterboxd export).
    upload: <T>(path: string, form: FormData) => request<T>("POST", path, form),
}

// Reads an answer of one JSON object a line (NDJSON), calling onLine with
// each as it comes in: a search showing its results as they're found. A
// failed request throws an ApiError, as the calls above do.
export async function readLines(path: string, onLine: (line: any) => void, signal?: AbortSignal, body?: unknown) {
    const res = await fetch(path, {
        credentials: "same-origin",
        signal,
        ...(body !== undefined
            ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }
            : {}),
    })
    if (!res.ok) {
        let data: any = null
        try {
            data = await res.json()
        } catch {
            /* not JSON */
        }
        if (res.status === 401 && data?.passwordRequired) authListeners.forEach(fn => fn())
        throw new ApiError(data?.error || res.statusText, res.status, data)
    }
    if (!res.body) {
        for (const l of (await res.text()).split("\n")) if (l.trim()) onLine(JSON.parse(l))
        return
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ""
    for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let nl: number
        while ((nl = buf.indexOf("\n")) >= 0) {
            const l = buf.slice(0, nl).trim()
            buf = buf.slice(nl + 1)
            if (l) onLine(JSON.parse(l))
        }
    }
    if (buf.trim()) onLine(JSON.parse(buf))
}

// Saves a file the API makes (a CSV) the way the browser saves downloads,
// and returns the response's headers (which entries a CSV has…).
export async function download(path: string): Promise<Headers> {
    const res = await fetch(path, { credentials: "same-origin" })
    if (!res.ok) {
        let data: any = null
        try {
            data = await res.json()
        } catch {
            /* not JSON */
        }
        if (res.status === 401 && data?.passwordRequired) authListeners.forEach(fn => fn())
        throw new ApiError(data?.error || res.statusText, res.status, data)
    }
    const blob = await res.blob()
    const name = /filename="?([^";]+)"?/i.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? "download.csv"
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 60_000)
    return res.headers
}

export function qs(params: Record<string, string | number | boolean | undefined | null>) {
    const sp = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) {
        if (v === undefined || v === null || v === "") continue
        sp.set(k, typeof v === "boolean" ? (v ? "1" : "0") : String(v))
    }
    const s = sp.toString()
    return s ? "?" + s : ""
}

// Desktop (Electron) bridge, exposed by the preload script.
export type DesktopBridge = {
    openExternal: (url: string) => void
    platform: string
    // Missing in desktop apps older than these settings.
    getPrefs?: () => Promise<DesktopPrefs>
    // true, or what went wrong (older apps: false).
    setPref?: (name: keyof DesktopPrefs, value: boolean) => Promise<true | false | { error: string }>
    quit?: () => void
    // Picture in picture (lib/pip.ts): the small player window. Missing in
    // older desktop apps. The requests are PlayerRequests (lib/store).
    openMiniPlayer?: (page: string) => Promise<boolean>
    closeMiniPlayer?: () => void
    expandMiniPlayer?: (req: unknown) => void
    // An episode handed back by the small window, for the big one to play.
    takePendingPlay?: () => Promise<unknown>
    onPlay?: (fn: (req: unknown) => void) => void
}

export type DesktopPrefs = { keepRunning: boolean; freeWhenLocked: boolean; startAtLogin?: boolean }

export function desktop(): DesktopBridge | null {
    return (window as any).movikoDesktop ?? null
}
