// Tiny fetch wrapper for the Aniko API.

export class ApiError extends Error {
    status: number
    data: any
    constructor(message: string, status: number, data: any) {
        super(message)
        this.status = status
        this.data = data
    }
}

// Told what the server said (hub: it's an Aniko Hub's dashboard).
type Listener = (data: { hub?: boolean } | null) => void
const authListeners = new Set<Listener>()
export function onPasswordRequired(fn: Listener) {
    authListeners.add(fn)
    return () => authListeners.delete(fn)
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(path, {
        method,
        headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
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
        if (res.status === 401 && data?.passwordRequired) authListeners.forEach(fn => fn(data))
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
    anilistLogin: (url: string) => Promise<string | null>
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
    return (window as any).kumoDesktop ?? null
}
