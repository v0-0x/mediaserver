// Picture in picture, in the desktop app: the episode goes on in a small
// window that stays on top of the others (desktop/main.js), with Aniko's
// player in it (its controls, subtitles and Up next), while Aniko or anything
// else is used. Not in browsers (they have their own, and extensions), nor
// on the TV.
//
// The small player is the /mini page, the episode in its address (with
// where it's at, and the tracks). Its button to go back hands the same to
// Aniko's window.

import { desktop } from "./api"
import { playerStore, type PlayerRequest } from "./store"
import { tv } from "./tv"

// The desktop app can (older ones can't).
export const canPip = () => !tv && !!desktop()?.openMiniPlayer

export const miniPage = (req: PlayerRequest) => `/mini#${encodeURIComponent(JSON.stringify(req))}`

// An episode handed over (the /mini page's address, Aniko's window): null if
// it isn't one.
export function asRequest(x: unknown): PlayerRequest | null {
    const r = x as PlayerRequest | null
    if (!r || typeof r !== "object" || typeof r.mediaId !== "number" || typeof r.episode !== "number") return null
    if (r.kind === "local") return typeof r.path === "string" ? r : null
    if (r.kind === "stream") return typeof r.provider === "string" ? r : null
    return null
}

// The episode the /mini page was opened with.
export function miniRequest(): PlayerRequest | null {
    try {
        return asRequest(JSON.parse(decodeURIComponent(location.hash.slice(1))))
    } catch {
        return null
    }
}

// Opens the small window with req. The big player closes after.
export async function openMini(req: PlayerRequest) {
    if (!(await desktop()?.openMiniPlayer?.(miniPage(req)))) throw new Error("the small window didn't open")
}

// The big player plays something: the small one goes (one episode at a time).
export function closeMini() {
    desktop()?.closeMiniPlayer?.()
}

// In the small window: back to Aniko's window with the episode, or closed.
export const expandMini = (req: PlayerRequest) => desktop()?.expandMiniPlayer?.(req)

// Aniko's window: the episode its small window hands back, played here.
export function takeHandedPlay() {
    const d = desktop()
    const play = (x: unknown) => {
        const r = asRequest(x)
        if (r) playerStore.set(r)
    }
    d?.onPlay?.(play)
    d?.takePendingPlay?.().then(play, () => {})
}
