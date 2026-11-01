import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"

// mediaserver's music side (Utako's server, for the Utako apps), as the
// dashboard's Music pages see it: its API through the anime side's, at
// /api/music/... (server: anime/internal/api/sides.go, music/internal/api),
// with the dashboard's sign-in.

export const U = "/api/music"

export type UPlaying = { title: string; artist: string; album: string; path: string; hasArt: boolean; at: number }

export type UDevice = {
    id: string
    name: string
    ip?: string
    allowed: boolean
    uploads: boolean
    firstSeen: number
    lastSeen: number
    online: boolean
    playing?: UPlaying
}

export type UStatus = {
    id: string
    name: string
    port: number
    version: string
    songs: number
    albums: number
    artists: number
    folders: number
    scanning: boolean
    lastScan: number
    devices: number
    online: number
    allowed: number
    listening: UDevice[]
    kept: { from: string; updatedAt: number; received: number; size: number } | null
    lengths: boolean
}

export type UTrack = {
    path: string
    title: string
    artist: string
    album: string
    albumArtist: string
    trackNo?: number
    discNo?: number
    year?: number
    durationMs: number
    addedAt: number
    hasArt?: boolean
}

export type UFolder = { path: string; songs: number; missing?: boolean }

export function useUStatus() {
    return useQuery({ queryKey: ["music", "status"], queryFn: () => api.get<UStatus>(`${U}/status`), refetchInterval: 5000 })
}

export function useUDevices() {
    return useQuery({ queryKey: ["music", "devices"], queryFn: () => api.get<UDevice[]>(`${U}/devices`), refetchInterval: 5000 })
}

export function useUFolders() {
    return useQuery({ queryKey: ["music", "folders"], queryFn: () => api.get<UFolder[]>(`${U}/folders`), refetchInterval: 15000 })
}

export function useUTracks(q: string, limit: number) {
    return useQuery({
        queryKey: ["music", "tracks", q, limit],
        queryFn: () => api.get<{ total: number; tracks: UTrack[] }>(`${U}/tracks?q=${encodeURIComponent(q)}&limit=${limit}`),
        placeholderData: prev => prev,
        refetchInterval: 30000,
    })
}

// A change to the music side, then what it shows asked again.
export function useUChange<T>(fn: (v: T) => Promise<unknown>, done?: string) {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: fn,
        onSuccess: () => {
            if (done) toast.info(done)
            qc.invalidateQueries({ queryKey: ["music"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// A song's cover, from the server.
export const artURL = (path: string) => `${U}/art?path=${encodeURIComponent(path)}`

export function length(ms: number) {
    if (!ms) return ""
    const s = Math.round(ms / 1000)
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}
