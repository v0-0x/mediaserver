import type { CSSProperties } from "react"
import { createStore } from "./store"

// How the in-app player shows subtitles on this device (a TV across the room
// isn't a computer's screen), and how much earlier or later they come, per
// anime (a release whose subtitles are off is usually off throughout).

export type SubStyle = { size: "s" | "m" | "l" | "xl"; background: "none" | "box" | "solid"; position: "bottom" | "raised" }

export const SUB_SIZES: { value: SubStyle["size"]; label: string; scale: number }[] = [
    { value: "s", label: "Small", scale: 0.8 },
    { value: "m", label: "Medium", scale: 1 },
    { value: "l", label: "Large", scale: 1.25 },
    { value: "xl", label: "Extra large", scale: 1.5 },
]
export const SUB_BACKGROUNDS: { value: SubStyle["background"]; label: string }[] = [
    { value: "none", label: "None (outlined text)" },
    { value: "box", label: "See-through box" },
    { value: "solid", label: "Solid box" },
]
export const SUB_POSITIONS: { value: SubStyle["position"]; label: string }[] = [
    { value: "bottom", label: "Bottom" },
    { value: "raised", label: "Higher up" },
]

const STYLE_KEY = "aniko-sub-style"
const DELAY_KEY = "aniko-sub-delay"
const DEFAULT_STYLE: SubStyle = { size: "m", background: "box", position: "bottom" }

function readStyle(): SubStyle {
    try {
        const v = JSON.parse(localStorage.getItem(STYLE_KEY) ?? "{}")
        return {
            size: SUB_SIZES.some(o => o.value === v.size) ? v.size : DEFAULT_STYLE.size,
            background: SUB_BACKGROUNDS.some(o => o.value === v.background) ? v.background : DEFAULT_STYLE.background,
            position: SUB_POSITIONS.some(o => o.value === v.position) ? v.position : DEFAULT_STYLE.position,
        }
    } catch {
        return DEFAULT_STYLE
    }
}

export const subStyleStore = createStore<SubStyle>(readStyle())

export function setSubStyle(next: Partial<SubStyle>) {
    const s = { ...subStyleStore.get(), ...next }
    subStyleStore.set(s)
    try {
        localStorage.setItem(STYLE_KEY, JSON.stringify(s))
    } catch {
        /* private window: until the page is reloaded */
    }
}

// The video element's variables for its cues (index.css, video::cue).
export function subStyleVars(s: SubStyle): CSSProperties {
    const scale = SUB_SIZES.find(o => o.value === s.size)?.scale ?? 1
    return {
        "--sub-scale": String(scale),
        "--sub-bg": s.background === "none" ? "transparent" : s.background === "solid" ? "rgb(0 0 0 / 0.92)" : "rgb(0 0 0 / 0.55)",
        "--sub-shadow": s.background === "none" ? "0 0 3px #000, 0 0 3px #000, 0 0 2px #000, 0 2px 4px #000" : "0 2px 4px rgb(0 0 0 / 0.8)",
    } as CSSProperties
}

// The line a cue goes on, counted from the bottom (-1 is the last one).
export const subLine = (s: SubStyle) => (s.position === "raised" ? -4 : -2)

// Seconds the subtitles of an anime are moved: later when positive.
export function subDelay(mediaId: number): number {
    try {
        const all = JSON.parse(localStorage.getItem(DELAY_KEY) ?? "{}")
        const d = Number(all?.[mediaId])
        return Number.isFinite(d) ? d : 0
    } catch {
        return 0
    }
}

export function setSubDelay(mediaId: number, delay: number) {
    try {
        const all = JSON.parse(localStorage.getItem(DELAY_KEY) ?? "{}") ?? {}
        if (Math.abs(delay) < 0.05) delete all[mediaId]
        else all[mediaId] = Math.round(delay * 10) / 10
        localStorage.setItem(DELAY_KEY, JSON.stringify(all))
    } catch {
        /* private window: for this episode only */
    }
}

// "+0.3s", "-1.2s", "0s".
export const formatDelay = (d: number) => (Math.abs(d) < 0.05 ? "0s" : `${d > 0 ? "+" : "−"}${Math.abs(d).toFixed(1)}s`)
