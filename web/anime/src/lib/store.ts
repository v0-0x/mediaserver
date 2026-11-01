import { useSyncExternalStore } from "react"
import type { PlaybackSession, PluginState } from "./types"

export type Store<T> = {
    get: () => T
    set: (next: T | ((prev: T) => T)) => void
    subscribe: (fn: () => void) => () => void
}

export function createStore<T>(initial: T): Store<T> {
    let state = initial
    const listeners = new Set<() => void>()
    return {
        get: () => state,
        set: next => {
            state = typeof next === "function" ? (next as (p: T) => T)(state) : next
            listeners.forEach(l => l())
        },
        subscribe: fn => {
            listeners.add(fn)
            return () => listeners.delete(fn)
        },
    }
}

export function useStore<T, S = T>(store: Store<T>, selector: (s: T) => S = s => s as unknown as S): S {
    return useSyncExternalStore(store.subscribe, () => selector(store.get()))
}

// ---------------------------------------------------------------------------
// App-wide stores

export const playbackStore = createStore<PlaybackSession | null>(null)

export type ScanState = { running: boolean; stage: string; done: number; total: number; message: string }
export const scanStore = createStore<ScanState>({ running: false, stage: "", done: 0, total: 0, message: "" })

export const torrentCountStore = createStore<number>(0)

export const pluginStore = createStore<Record<string, PluginState>>({})

// Requests from plugins to open a tray (tray.open()).
export const trayOpenStore = createStore<{ pluginId: string; trayId: string } | null>(null)

// In-app player. Handed from one player to another (picture in picture and
// back, lib/pip.ts): where it was (start), and the tracks picked there.
export type HandedTracks = { audio?: number; sub?: string; source?: number }
export type PlayerRequest =
    | { kind: "local"; path: string; mediaId: number; episode: number; start?: number; tracks?: HandedTracks }
    | { kind: "stream"; provider: string; mediaId: number; episode: number; dub: boolean; server?: string; start?: number; tracks?: HandedTracks }

export const playerStore = createStore<PlayerRequest | null>(null)

// The server asks for its password ("hub": an Aniko Hub's dashboard does).
export const passwordStore = createStore<boolean | "hub">(false)

export const searchOpenStore = createStore<boolean>(false)

// Notifications muted with the bell in the sidebar (lib/toast.ts): only
// errors show. Remembered by this browser.
const MUTED_KEY = "aniko-notifications-muted"
export const notificationsMutedStore = createStore<boolean>(
    (() => {
        try {
            return localStorage.getItem(MUTED_KEY) === "1"
        } catch {
            return false
        }
    })(),
)
notificationsMutedStore.subscribe(() => {
    try {
        localStorage.setItem(MUTED_KEY, notificationsMutedStore.get() ? "1" : "0")
    } catch {
        /* storage unavailable: muted until reload */
    }
})

// The version whose update banner was put off ("Later") on Home; checking
// for updates by hand shows it again.
const LATER_KEY = "aniko-update-later"
function readLater(): string {
    try {
        return JSON.parse(localStorage.getItem(LATER_KEY) ?? '""') || ""
    } catch {
        return ""
    }
}
export const updateLaterStore = createStore<string>(readLater())
export function setUpdateLater(version: string) {
    updateLaterStore.set(version)
    try {
        localStorage.setItem(LATER_KEY, JSON.stringify(version))
    } catch {
        /* for this run only */
    }
}
