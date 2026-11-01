import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect } from "react"
import { toast } from "@/lib/toast"
import { api, qs } from "./api"
import { setUpdateLater } from "./store"
import { installIfWanted, wantInstall } from "./tv"
import type {
    ActivityPage,
    AutoRule,
    CollectionView,
    DownloadItem,
    EntryView,
    ExtensionInfo,
    FolderInfo,
    ListEntry,
    LocalFile,
    MangaChapter,
    MarketEntry,
    Media,
    OnlineProvider,
    ParentalState,
    PluginState,
    ScheduleItem,
    Settings,
    SharedLibrary,
    SharingStatus,
    Status,
    StreamEpisodes,
    Torrent,
    TorrentResult,
    UpdateStatus,
    Profile,
    WatchOrder,
} from "./types"

export function useStatus() {
    return useQuery({ queryKey: ["status"], queryFn: () => api.get<Status>("/api/status"), staleTime: 30_000 })
}

export function useSettings() {
    const q = useStatus()
    return q.data?.settings
}

export function useSaveSettings() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (s: Settings) => api.put<Settings>("/api/settings", s),
        onSuccess: s => {
            qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: s } : old))
            qc.invalidateQueries({ queryKey: ["status"] })
            toast.success("Settings saved")
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// Changes UI settings at once, without a toast: the sidebar's Themes
// menu, where colors are tried one after another. The page changes before
// the server answers; an error puts the saved ones back.
// Saves one after another quickly (themes tried in a row): only the last
// one's answer is put in (an earlier one's, coming later, would show a theme
// tried before).
let uiSaves = 0
export function useSaveUI() {
    const qc = useQueryClient()
    return (ui: Partial<Settings["ui"]>) => {
        const s = qc.getQueryData<Status>(["status"])?.settings
        if (!s) return
        const next = { ...s, ui: { ...s.ui, ...ui } }
        qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: next } : old))
        const n = ++uiSaves
        api.put<Settings>("/api/settings", next).then(
            saved => n === uiSaves && qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: saved } : old)),
            (e: Error) => {
                toast.error(e.message)
                qc.invalidateQueries({ queryKey: ["status"] })
            },
        )
    }
}

// The list as AniList has it (at most a minute old), asked again when Aniko's
// window comes back to the front: what was watched on another device, on
// the same account, shows up in Continue watching.
export function useCollection() {
    return useQuery({
        queryKey: ["collection"],
        queryFn: () => api.get<CollectionView>("/api/anime/collection?fresh=1"),
        staleTime: 60_000,
        refetchOnWindowFocus: true,
        // Anime in the library left out for now (AniList didn't say what
        // they are, the app just opened): asked again until they're in.
        refetchInterval: q => (q.state.data?.incomplete ? 10_000 : false),
    })
}

export function useEntry(id: number) {
    return useQuery({
        queryKey: ["entry", id],
        queryFn: () => api.get<EntryView>(`/api/anime/${id}`),
        enabled: id > 0,
        staleTime: 30_000,
    })
}

export function useUpdateEntry(mediaId: number) {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (body: { status?: string; progress?: number; score?: number; repeat?: number }) => api.post(`/api/anime/${mediaId}/entry`, body),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ["entry", mediaId] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
            qc.invalidateQueries({ queryKey: ["manga"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// Marks single episodes as watched or unwatched. AniList only stores how far
// you got, so marking episode 7 watched raises progress to 7 (1-6 count as
// watched too) and unwatched lowers it to 6. The server works it out from the
// current list entry: watched never lowers progress, unwatched never raises
// it; the last episode completes the show and an earlier one reopens it.
export function useEpisodeMarker(media: Media | undefined, _listEntry?: Pick<ListEntry, "status" | "progress" | "repeat"> | null) {
    const qc = useQueryClient()
    const m = useMutation({
        mutationFn: (v: { episode: number; watched: boolean }) => api.post<{ changed: boolean; progress: number; status: string }>(`/api/anime/${media?.id ?? 0}/episode`, v),
        onSuccess: (res, v) => {
            qc.invalidateQueries({ queryKey: ["entry", media?.id] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
            const what = media?.format === "MOVIE" ? "Movie" : `Episode ${v.episode}`
            if (!res.changed) toast.info(`${what} is already ${v.watched ? "watched" : "unwatched"}`)
            else toast.success(`${what} marked as ${v.watched ? "watched" : "unwatched"}${res.status === "COMPLETED" ? " — completed!" : ""}`)
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const mark = (episode: number, watched: boolean) => {
        if (media) m.mutate({ episode, watched })
    }
    return { mark, pending: m.isPending }
}

// The sub/dub choice remembered for an anime (server-side, so every device
// and every way of starting an episode uses it).
export type LanguageMode = { mode: "sub" | "dub"; saved: boolean }

export function useLanguageMode(mediaId: number) {
    const qc = useQueryClient()
    const query = useQuery({
        queryKey: ["language", mediaId],
        queryFn: () => api.get<LanguageMode>(`/api/anime/${mediaId}/language`),
        enabled: mediaId > 0,
        staleTime: 60_000,
    })
    const set = useMutation({
        mutationFn: (mode: "sub" | "dub") => api.put<LanguageMode>(`/api/anime/${mediaId}/language`, { mode }),
        onMutate: mode => qc.setQueryData<LanguageMode>(["language", mediaId], { mode, saved: true }),
        onSettled: () => {
            qc.invalidateQueries({ queryKey: ["language", mediaId] })
            // It's also the default now, for the anime without a choice.
            qc.invalidateQueries({ queryKey: ["language"] })
            qc.invalidateQueries({ queryKey: ["status"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
    return { mode: query.data?.mode, saved: query.data?.saved ?? false, loaded: query.isSuccess || query.isError, set: set.mutate }
}

// For click handlers outside a component that has the hook.
export async function fetchLanguageMode(mediaId: number, fallbackDub: boolean): Promise<boolean> {
    try {
        return (await api.get<LanguageMode>(`/api/anime/${mediaId}/language`)).mode === "dub"
    } catch {
        return fallbackDub
    }
}

export function useDeleteEntry(mediaId: number) {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: () => api.del(`/api/anime/${mediaId}/entry`),
        onSuccess: () => {
            toast.success("Removed from your list")
            qc.invalidateQueries({ queryKey: ["entry", mediaId] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

export type SearchParams = {
    page?: number
    perPage?: number
    search?: string
    type?: string
    genres?: string[]
    tags?: string[]
    season?: string
    year?: number
    formats?: string[]
    status?: string
    sort?: string[]
}

export function useSearch(params: SearchParams, enabled = true) {
    return useQuery({
        queryKey: ["search", params],
        queryFn: () => api.post<{ pageInfo: { hasNextPage: boolean; total: number; currentPage: number }; media: Media[] | null }>("/api/anilist/search", params),
        enabled,
        placeholderData: keepPreviousData,
        staleTime: 5 * 60_000,
    })
}

type Discover = { trending: Media[]; thisSeason: Media[]; nextSeason: Media[]; popular: Media[]; topRated: Media[]; season: string; year: number; fetchedAt: number }

export function useDiscover() {
    return useQuery({
        queryKey: ["discover"],
        queryFn: () => api.get<Discover>("/api/anilist/discover"),
        staleTime: 10 * 60_000,
        // The server answers at once with the page it saved last, and fetches
        // a fresh one when that's over 30 minutes old: pick it up shortly
        // (a few tries, in case AniList can't be reached).
        refetchInterval: q => {
            const d = q.state.data
            return d && Date.now() / 1000 - d.fetchedAt > 30 * 60 && q.state.dataUpdateCount < 4 ? 4000 : false
        },
    })
}

// Library sharing: the Aniko apps on the network (asked again while shown).
export function useSharing(enabled = true) {
    return useQuery({ queryKey: ["sharing", "status"], queryFn: () => api.get<SharingStatus>("/api/sharing"), enabled, refetchInterval: 4000 })
}

// Parental controls on this device (set by a computer or hub that shares
// with it).
export function useParental() {
    return useQuery({ queryKey: ["parental"], queryFn: () => api.get<ParentalState>("/api/parental"), staleTime: 60_000 })
}

// The genres to offer as filters: not the ones parental controls block
// (Ecchi and Hentai go with 18+).
export function useGenres(all: readonly string[]) {
    const rules = useParental().data?.rules
    const blocked = [...(rules?.genres ?? []), ...(rules?.noAdult ? ["Ecchi", "Hentai"] : [])]
    if (!blocked.length) return all
    const b = new Set(blocked.map(g => g.toLowerCase()))
    return all.filter(g => !b.has(g.toLowerCase()))
}

// The libraries other Aniko apps share with this one.
export function useSharedLibraries() {
    return useQuery({ queryKey: ["sharing", "libraries"], queryFn: () => api.get<SharedLibrary[]>("/api/sharing/libraries"), refetchInterval: 30_000 })
}

export function useSchedule(days = 7) {
    return useQuery({ queryKey: ["schedule", days], queryFn: () => api.get<ScheduleItem[]>(`/api/anilist/schedule?days=${days}`), staleTime: 10 * 60_000 })
}

export function useRawList(type: "ANIME" | "MANGA") {
    return useQuery({
        queryKey: ["list", type],
        queryFn: () => api.get<{ lists: { name: string; status: string; isCustomList: boolean; entries: any[] }[] | null }>(`/api/anilist/list?type=${type}`),
        staleTime: 60_000,
    })
}

export function useScan() {
    return useMutation({
        mutationFn: (full: boolean) => api.post("/api/library/scan", { full }),
        onSuccess: () => toast.info("Scanning your library…"),
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useUnmatched() {
    return useQuery({
        queryKey: ["library", "unmatched"],
        queryFn: () => api.get<{ dir: string; title: string; files: LocalFile[] }[]>("/api/library/unmatched"),
    })
}

export function useLibraryFolders(enabled = true) {
    return useQuery({ queryKey: ["library", "folders"], queryFn: () => api.get<FolderInfo[] | null>("/api/library/folders"), enabled })
}

// Also for reading the files outside a component (qc.ensureQueryData).
export const libraryFilesQuery = { queryKey: ["library", "files"], queryFn: () => api.get<LocalFile[] | null>("/api/library/files") }

export function useLibraryFiles() {
    return useQuery(libraryFilesQuery)
}

export function useOnlineProviders() {
    return useQuery({ queryKey: ["os-providers"], queryFn: () => api.get<OnlineProvider[]>("/api/onlinestream/providers"), staleTime: 5 * 60_000 })
}

// only: the version listed is the only one there is, not a choice to remember.
export function useStreamEpisodes(provider: string, mediaId: number, dub: boolean, enabled = true, only = false) {
    return useQuery({
        queryKey: ["os-episodes", provider, mediaId, dub],
        queryFn: () => api.get<StreamEpisodes>(`/api/onlinestream/episodes${qs({ provider, mediaId, dub, only: only ? 1 : undefined })}`),
        enabled: enabled && mediaId > 0 && !!provider,
        retry: false,
        staleTime: 10 * 60_000,
    })
}

// Whether the provider has the anime subtitled and dubbed (both while it's
// not known).
export function useStreamModes(provider: string, mediaId: number, enabled = true) {
    return useQuery({
        queryKey: ["os-modes", provider, mediaId],
        queryFn: () => api.get<{ sub: boolean; dub: boolean }>(`/api/onlinestream/modes${qs({ provider, mediaId })}`),
        enabled: enabled && mediaId > 0 && !!provider,
        retry: false,
        staleTime: 30 * 60_000,
    })
}

export function useWatchOrder(id: number) {
    return useQuery({ queryKey: ["watch-order", id], queryFn: () => api.get<WatchOrder>(`/api/anime/${id}/watch-order`), enabled: id > 0, staleTime: 10 * 60_000 })
}

export function useDownloads() {
    return useQuery({ queryKey: ["downloads"], queryFn: () => api.get<DownloadItem[]>("/api/downloads") })
}

export function useTorrentList(enabled = true) {
    return useQuery({
        queryKey: ["torrents"],
        queryFn: () => api.get<Torrent[]>("/api/torrent-client/list"),
        refetchInterval: 3000,
        retry: false,
        enabled,
    })
}

export function useTorrentSearch() {
    return useMutation({
        mutationFn: (body: { provider: string; mediaId?: number; query?: string; episode?: number; batch?: boolean; resolution?: string }) =>
            api.post<TorrentResult[]>("/api/torrents/search", body),
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useExtensions() {
    return useQuery({ queryKey: ["extensions"], queryFn: () => api.get<ExtensionInfo[]>("/api/extensions") })
}

export function useMarketplace(url: string) {
    return useQuery({
        queryKey: ["marketplace", url],
        queryFn: () => api.get<MarketEntry[]>(`/api/extensions/marketplace${qs({ url })}`),
        staleTime: 30 * 60_000,
        retry: false,
    })
}

export function usePluginStates() {
    return useQuery({ queryKey: ["plugins-ui"], queryFn: () => api.get<PluginState[]>("/api/plugins/ui") })
}

export function useAutoRules() {
    return useQuery({ queryKey: ["auto-rules"], queryFn: () => api.get<AutoRule[]>("/api/autodownloader/rules") })
}

export function useMangaChapters(mediaId: number, provider: string) {
    return useQuery({
        queryKey: ["manga", "chapters", mediaId, provider],
        queryFn: () =>
            api.get<{ provider: string; mapping: { id: string; title: string } | null; chapters: MangaChapter[] | null }>(
                `/api/manga/${mediaId}/chapters${qs({ provider })}`,
            ),
        enabled: mediaId > 0,
        retry: false,
        staleTime: 10 * 60_000,
    })
}

// Aniko's own updates. The "update-status" event keeps ["update"] current
// (lib/events.ts), also while one is being installed.
export function useUpdateStatus() {
    return useQuery({ queryKey: ["update"], queryFn: () => api.get<UpdateStatus>("/api/update"), staleTime: 5 * 60_000 })
}

// Aniko opened (started, its window opened again, back in front, the TV app
// reopened): a check for updates, unless one ran in the last 10 minutes.
// Unlike Check for updates, an update put off with Later stays put off.
export function useCheckOnOpen(enabled: boolean) {
    const qc = useQueryClient()
    useEffect(() => {
        if (!enabled) return
        let running = false
        const check = async () => {
            if (running || document.hidden) return
            running = true
            try {
                const u = await api.get<UpdateStatus>("/api/update")
                // Never checked yet: Aniko has just started and checks by
                // itself in a moment.
                if (u.checking || !u.checkedAt || Date.now() / 1000 - u.checkedAt < 600) return
                qc.setQueryData(["update"], await api.post<UpdateStatus>("/api/update/check"))
            } catch {
                /* the next opening tries again */
            } finally {
                running = false
            }
        }
        check()
        document.addEventListener("visibilitychange", check)
        return () => document.removeEventListener("visibilitychange", check)
    }, [enabled, qc])
}

export function useCheckUpdate() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: () => api.post<UpdateStatus>("/api/update/check"),
        onSuccess: s => {
            qc.setQueryData(["update"], s)
            // Asked for: an update put off with Later shows again.
            setUpdateLater("")
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useApplyUpdate() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: () => {
            wantInstall()
            return api.post<UpdateStatus>("/api/update/apply")
        },
        onSuccess: s => {
            qc.setQueryData(["update"], s)
            installIfWanted(s)
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// The profile page: a user's profile ("" the logged-in one's), and a feed
// (the people followed, or one user's), a page at a time.
export function useProfile(user: string, enabled = true) {
    return useQuery({
        queryKey: ["profile", user.toLowerCase()],
        queryFn: () => api.get<Profile>(`/api/anilist/profile${user ? `?user=${encodeURIComponent(user)}` : ""}`),
        staleTime: 60_000,
        enabled,
    })
}

export function useActivity(feed: "following" | "user", userId: number, enabled = true) {
    return useInfiniteQuery({
        queryKey: ["activity", feed, feed === "user" ? userId : 0],
        queryFn: ({ pageParam }) => api.get<ActivityPage>(`/api/anilist/activity?feed=${feed}&user=${userId}&page=${pageParam}`),
        initialPageParam: 1,
        getNextPageParam: (last, pages) => (last.hasMore ? pages.length + 1 : undefined),
        staleTime: 60_000,
        enabled,
    })
}
