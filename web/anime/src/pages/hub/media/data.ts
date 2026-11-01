import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"

// mediaserver's movies side (KumoBox's server), as the dashboard's Movies
// and TV pages see it: its API through this side's, at /api/movies/...
// (server: anime/internal/api/movies.go), with the dashboard's sign-in.

export const M = "/api/movies"

// The two pages of the movies side: its movies, or its shows.
export type MediaKind = "movies" | "tv"

// A show's media id is past this (the movies side's lib/ids.ts).
const SHOW_BASE = 1_000_000_000
export const isShowId = (id: number) => id > SHOW_BASE

export type MPeer = {
    id: string
    name: string
    address: string
    online: boolean
    lastSeen: number
    version: string
    allowed: boolean
    shares: boolean
    files: number
    manual: boolean
    error?: string
    downloads: boolean
    takesDownloads: boolean
    platform?: string
    declined?: boolean
}

export type MSharing = { enabled: boolean; id: string; name: string; listening: boolean; peers: MPeer[]; addresses: string[] }

export type MMedia = {
    id: number
    type?: string
    title: { romaji?: string; english?: string; native?: string; userPreferred?: string }
    coverImage?: { extraLarge?: string; large?: string; medium?: string }
    seasonYear?: number | null
    startDate?: { year?: number | null }
}

export type MTitle = {
    media: MMedia
    files: { path: string; size?: number }[]
    episodes: number[]
    seasons: { season: number; count: number; total: number; ranges: string }[]
    ranges: string
    size: number
    addedAt: number
    dir: string
}

export type MStatus = { version: string; tmdbConfigured: boolean; scanning: boolean; hostname: string }

// Its settings: what the dashboard changes of them (the rest is sent back
// as it came).
export type MSettings = {
    library: { dir: string; extraDirs?: string[] | null; off?: boolean }
    sharing: { enabled: boolean; name: string }
    tmdb: { apiKey: string; language: string; region: string }
    [k: string]: unknown
}

export type MUnmatched = { dir: string; title: string; files: { path: string }[] }[]

export const mediaTitle = (m: MMedia) => m.title.userPreferred || m.title.english || m.title.romaji || m.title.native || ""
export const mediaYear = (m: MMedia) => m.seasonYear || m.startDate?.year || undefined

// An image of the movies side, through its image cache.
export function mImg(url?: string | null) {
    if (!url) return ""
    if (url.startsWith("http://") || url.startsWith("https://")) return `${M}/img?u=${encodeURIComponent(url)}`
    return url.startsWith("/api/") ? M + url.slice(4) : url
}

export function useMStatus() {
    return useQuery({ queryKey: ["movies", "status"], queryFn: () => api.get<MStatus>(`${M}/status`), refetchInterval: 10_000 })
}

export function useMSharing() {
    return useQuery({ queryKey: ["movies", "sharing"], queryFn: () => api.get<MSharing>(`${M}/sharing`), refetchInterval: 5_000 })
}

export function useMTitles() {
    return useQuery({ queryKey: ["movies", "titles"], queryFn: () => api.get<MTitle[] | null>(`${M}/library/titles`), refetchInterval: 30_000 })
}

export function useMUnmatched() {
    return useQuery({ queryKey: ["movies", "unmatched"], queryFn: () => api.get<MUnmatched | null>(`${M}/library/unmatched`), refetchInterval: 60_000 })
}

export function useMSettings() {
    return useQuery({ queryKey: ["movies", "settings"], queryFn: () => api.get<MSettings>(`${M}/settings`) })
}

// Saves a change to the movies side's settings, on the ones it has now.
export function useSaveMSettings() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: async (change: (s: MSettings) => MSettings) => {
            const cur = await api.get<MSettings>(`${M}/settings`)
            return api.put<MSettings>(`${M}/settings`, change(cur))
        },
        onSuccess: s => {
            qc.setQueryData(["movies", "settings"], s)
            qc.invalidateQueries({ queryKey: ["movies"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// The titles of one page: movies, or shows.
export function ofKind(titles: MTitle[] | null | undefined, kind: MediaKind) {
    return (titles ?? []).filter(t => (kind === "tv") === isShowId(t.media.id))
}

// The library folders, the first one first.
export const foldersOf = (s?: MSettings) => (s ? [s.library.dir, ...(s.library.extraDirs ?? [])].filter(Boolean) : [])
