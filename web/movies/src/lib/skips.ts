// The parts of a video the players offer to skip: those its chapter names
// mark as an intro, a recap, the end credits or a preview of the next
// episode (server/internal/player/chapters.go). Intros and recaps follow
// Settings › Playback "Skip intros and recaps", credits and previews "Skip
// end credits". Kumo's older names (op, ed...) count too.
import type { Settings, SkipInterval } from "./types"

const CREDITS = ["credits", "preview", "outro", "ed", "mixed-ed"]
const KNOWN = ["intro", "recap", "op", "mixed-op", ...CREDITS]

export const isSkippable = (s: SkipInterval) => KNOWN.includes(s.type)

// What a part is called on its button: "Skip intro", "Skip recap", "Skip
// credits", "Skip preview".
export function skipName(type: string) {
    if (type === "recap" || type === "preview") return type
    return CREDITS.includes(type) ? "credits" : "intro"
}

// Whether the settings skip a kind of part.
export function skipOn(p: Settings["playback"] | undefined, type: string) {
    return CREDITS.includes(type) ? !!p?.skipCredits : !!p?.skipIntro
}

// The part playing at t (a second before its end it's over).
export const skipAt = (skips: SkipInterval[], t: number) => skips.find(s => isSkippable(s) && t >= s.start - 0.5 && t < s.end - 1)

// The parts skipped from s on: s, and the ones right after it the settings
// skip too (the credits, then the preview). skips are in order.
export function skipRun(skips: SkipInterval[], s: SkipInterval, on: (type: string) => boolean) {
    const run = [s]
    for (const n of skips) {
        const last = run[run.length - 1]
        if (n.start > last.start && Math.abs(n.start - last.end) < 1 && on(n.type)) run.push(n)
    }
    return { end: run[run.length - 1].end, names: [...new Set(run.map(r => skipName(r.type)))].join(" and ") }
}
