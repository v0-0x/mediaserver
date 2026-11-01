import { Check, CheckCheck, ChevronDown, FolderOpen, FolderSearch, FolderSync, HardDrive, Info, LayoutGrid, List as ListIcon, Play, Search, Settings2, Share2, Upload } from "lucide-react"
import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { toast } from "@/lib/toast"
import { EpisodeCard } from "@/components/EpisodeCard"
import { Carousel, cardMinWidth, cardTextHeight, MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { useLibraryCardMenu } from "@/components/library/LibraryCardMenu"
import { UploadDialog, useUploadTargets } from "@/components/UploadDialog"
import { VirtualGrid, VirtualList } from "@/components/VirtualList"
import { Badge, Button, EmptyState, IconButton, Input, Progress, Select, Tabs, Tooltip } from "@/components/ui"
import { api } from "@/lib/api"
import { fileKeys, fileLabel } from "@/lib/files"
import { usePersisted } from "@/lib/hooks"
import { episodeIndex, formatKey, mediaIsShow, splitKey, totalEpisodes } from "@/lib/ids"
import { usePlay } from "@/lib/play"
import { useCollection, useEpisodeMarker, useLibraryTitles, useScan, useSharedLibraries, useStatus } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import { tv } from "@/lib/tv"
import type { LibraryTitle, LocalFile } from "@/lib/types"
import { cn, cover, entryUrl, formatBytes, formatLabel, LIST_STATUS, poster, relativeTime, title, year } from "@/lib/utils"

// "Local library": only movies and shows that have files on this computer
// (or on another KumoBox sharing its library). The server sums each title's
// files up (GET /api/library/titles): episodes by season, size, quality,
// what's unwatched.

type StatusFilter = "ALL" | "CURRENT" | "PLANNING" | "COMPLETED" | "PAUSED" | "DROPPED" | "NONE"
type SortKey = "added" | "title" | "unwatched" | "episodes" | "size"
type View = "grid" | "list"
type Kind = "" | "MOVIE" | "TV"

const STATUS_TABS: { value: StatusFilter; label: string }[] = [
    { value: "ALL", label: "All" },
    { value: "CURRENT", label: LIST_STATUS.CURRENT },
    { value: "PLANNING", label: LIST_STATUS.PLANNING },
    { value: "COMPLETED", label: LIST_STATUS.COMPLETED },
    { value: "PAUSED", label: LIST_STATUS.PAUSED },
    { value: "DROPPED", label: LIST_STATUS.DROPPED },
    { value: "NONE", label: "Not on my list" },
]

function statusOf(t: LibraryTitle): StatusFilter {
    const s = t.listEntry?.status
    if (!s) return "NONE"
    return s === "REPEATING" ? "CURRENT" : (s as StatusFilter)
}

// Episode keys as runs: [S01E01, S01E02, S01E03, S01E05, S02E01, S02E02]
// -> "S01E01–E03, E05, S02E01–E02" (as the server writes them).
function episodeRanges(keys: number[]) {
    const s = [...new Set(keys)].sort((a, b) => a - b)
    const out: string[] = []
    let season = -1
    for (let i = 0; i < s.length; ) {
        let j = i
        while (j + 1 < s.length && s[j + 1] === s[j] + 1) j++
        const [sn, a] = splitKey(s[i])
        const b = splitKey(s[j])[1]
        const first = sn === season ? `E${String(a).padStart(2, "0")}` : formatKey(s[i])
        out.push(i === j ? first : `${first}–E${String(b).padStart(2, "0")}`)
        season = sn
        i = j + 1
    }
    return out.join(", ")
}

// Watched up to (a key): everything for a title watched to the end.
const watchedUpTo = (t: LibraryTitle) => (t.listEntry?.status === "COMPLETED" ? Infinity : (t.listEntry?.progress ?? 0))

export default function LocalLibraryPage() {
    const { data: status } = useStatus()
    const { data: coll } = useCollection()
    // The libraries other KumoBox apps on the network share with this one.
    const { data: shared } = useSharedLibraries()
    const [source, setSource] = usePersisted<string>("kumobox-local-source", "local")
    // This computer's library off (Settings › Local library): only the
    // shared ones, the first one to begin with.
    const off = !!status?.settings.library.off
    const host = off ? (shared?.find(l => l.host.id === source) ?? shared?.[0]) : source === "local" ? undefined : shared?.find(l => l.host.id === source)
    const { data: titlesData, isLoading: loading, error } = useLibraryTitles(host?.host.id ?? "")
    const scan = useScan()
    const scanning = useStore(scanStore, s => s.running)
    const scanState = useStore(scanStore)
    const { playLocal } = usePlay()
    const navigate = useNavigate()
    const libraryMenu = useLibraryCardMenu()

    const [filter, setFilter] = usePersisted<StatusFilter>("kumobox-local-filter", "ALL")
    const [kind, setKind] = usePersisted<Kind>("kumobox-local-kind", "")
    const [sort, setSort] = usePersisted<SortKey>("kumobox-local-sort", "added")
    const [view, setView] = usePersisted<View>("kumobox-local-view", "grid")
    const [q, setQ] = useState("")
    // The list's rows showing their files (kept here: a row scrolled far
    // away isn't rendered).
    const [openRows, setOpenRows] = useState<Set<number>>(() => new Set())
    const toggleRow = (id: number) =>
        setOpenRows(s => {
            const n = new Set(s)
            if (!n.delete(id)) n.add(id)
            return n
        })

    const titles = useMemo(() => titlesData ?? [], [titlesData])

    const counts = useMemo(() => {
        const c: Record<string, number> = { ALL: titles.length }
        for (const t of titles) c[statusOf(t)] = (c[statusOf(t)] ?? 0) + 1
        return c
    }, [titles])
    // Tabs with nothing in them are hidden, so a remembered filter may have no tab
    // any more: fall back to All.
    const activeFilter: StatusFilter = filter !== "ALL" && STATUS_TABS.some(t => t.value === filter) && counts[filter] > 0 ? filter : "ALL"

    const shown = useMemo(() => {
        const needle = q.trim().toLowerCase()
        const out = titles.filter(t => {
            if (kind && t.media.type !== kind) return false
            if (activeFilter !== "ALL" && statusOf(t) !== activeFilter) return false
            if (!needle) return true
            const m = t.media
            return [m.title.userPreferred, m.title.romaji, m.title.english, m.title.native, ...(m.synonyms ?? [])].some(x => x?.toLowerCase().includes(needle))
        })
        out.sort((a, b) => {
            switch (sort) {
                case "title":
                    return title(a.media).localeCompare(title(b.media))
                case "unwatched":
                    return b.unwatched - a.unwatched || b.addedAt - a.addedAt
                case "episodes":
                    return b.episodes.length - a.episodes.length
                case "size":
                    return b.size - a.size
                default:
                    return b.addedAt - a.addedAt
            }
        })
        return out
    }, [titles, kind, activeFilter, sort, q])

    // Newest files on disk, one card per title and day (a season of 10
    // episodes shows up as "S01E01–E10", not ten cards).
    const recent = useMemo(() => {
        const files = titles.flatMap(t => t.files.filter(f => f.kind === "main" && f.episode > 0).map(f => ({ t, f })))
        files.sort((a, b) => b.f.modTime - a.f.modTime)
        const batches = new Map<string, { title: LibraryTitle; files: LocalFile[]; latest: number }>()
        for (const { t, f } of files) {
            const key = `${t.media.id}:${new Date(f.modTime * 1000).toDateString()}`
            const b = batches.get(key)
            if (b) b.files.push(f)
            else if (batches.size < 16) batches.set(key, { title: t, files: [f], latest: f.modTime })
        }
        return [...batches.values()].map(b => {
            b.files.sort((x, y) => x.episode - y.episode)
            const upTo = watchedUpTo(b.title)
            const keys = b.files.flatMap(fileKeys)
            return { ...b, keys, play: b.files.find(f => f.episode > upTo) ?? b.files[0], allWatched: keys.every(k => k <= upTo) }
        })
    }, [titles])

    const totals = useMemo(
        () => ({
            movies: titles.filter(t => !mediaIsShow(t.media)).length,
            shows: titles.filter(t => mediaIsShow(t.media)).length,
            episodes: titles.reduce((n, t) => n + (mediaIsShow(t.media) ? t.episodes.length : 0), 0),
            size: titles.reduce((n, t) => n + t.size, 0),
        }),
        [titles],
    )

    const libraryDir = off ? undefined : status?.settings.library.dir
    const trusted = status?.client !== "lan"
    const openFolder = (path?: string) => path && api.post("/api/open", { path }).catch(e => toast.error(e.message))
    const hostFiles = host?.files.length ?? 0
    // Files from this computer to a server (one that takes this device's
    // downloads), or, on mediaserver's pages, to it.
    const uploads = useUploadTargets(trusted && !tv)
    const [uploading, setUploading] = useState(false)
    const canUpload = !!uploads.data?.length
    const uploadButton = canUpload && (
        <Button icon={<Upload className="size-4" />} onClick={() => setUploading(true)}>
            Upload
        </Button>
    )

    return (
        <div className="min-h-full pb-24">
            {canUpload && <UploadDialog open={uploading} onOpenChange={setUploading} target={host?.host.id} />}
            <header className="border-b border-line px-6 pt-8 pb-7 md:px-8 xl:px-10">
                <div className="flex flex-wrap items-end justify-between gap-6">
                    <div>
                        <h1 className="text-[1.75rem] font-semibold tracking-tight">Local library</h1>
                        <p className="mt-1 text-muted">
                            {host ? `Shared by ${host.host.name} over your network${host.host.online ? "" : " (not answering right now)"}.` : "The movies and shows you have on this computer."}
                        </p>
                    </div>
                    {host && uploadButton}
                    {!host && off && uploadButton}
                    {!host && !off && (
                        <div className="flex items-center gap-2">
                            {uploadButton}
                            {trusted && libraryDir && (
                                <IconButton label="Open library folder" variant="subtle" onClick={() => openFolder(libraryDir)}>
                                    <FolderOpen className="size-4" />
                                </IconButton>
                            )}
                            <IconButton label="Library tools (fix matches, ignored files)" variant="subtle" onClick={() => navigate("/library")}>
                                <Settings2 className="size-4" />
                            </IconButton>
                            <Button variant="primary" loading={scanning || scan.isPending} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                                {scanning ? "Scanning…" : "Scan for new files"}
                            </Button>
                        </div>
                    )}
                </div>
                {!!shared?.length && (!off || shared.length > 1) && (
                    <Tabs
                        className="mt-6"
                        value={host ? host.host.id : "local"}
                        onChange={setSource}
                        tabs={[
                            ...(off ? [] : [{ value: "local", label: "This computer", icon: <HardDrive className="size-4" /> }]),
                            ...shared.map(l => ({ value: l.host.id, label: l.host.name, icon: <Share2 className="size-4" /> })),
                        ]}
                    />
                )}
                <div className="mt-6 flex flex-wrap items-center gap-x-8 gap-y-3">
                    <Stat label="Movies" value={loading ? "…" : String(totals.movies)} />
                    <Stat label="Shows" value={loading ? "…" : String(totals.shows)} />
                    <Stat label="Episodes" value={loading ? "…" : String(totals.episodes)} />
                    <Stat label={host ? "Size" : "On disk"} value={loading ? "…" : formatBytes(totals.size)} />
                    {!host && !!coll?.unmatchedCount && (
                        <Link to="/library?tab=unmatched" className="flex items-center gap-2 rounded-lg bg-amber-400/10 px-3 py-2 text-sm text-amber-200 transition-colors hover:bg-amber-400/15">
                            <FolderSearch className="size-4" />
                            {coll.unmatchedCount} unmatched {coll.unmatchedCount === 1 ? "file" : "files"} — fix
                        </Link>
                    )}
                </div>
            </header>

            <div className="flex flex-col gap-10 px-6 pt-8 md:px-8 xl:px-10">
                {error && <EmptyState title="Couldn't load your library">{(error as Error).message}</EmptyState>}

                {scanning && titles.length === 0 && (
                    <div className="card mx-auto w-full max-w-xl p-6 rise-in">
                        <div className="flex items-center gap-3">
                            <FolderSync className="size-5 animate-pulse text-brand-strong" />
                            <div className="min-w-0 flex-1">
                                <p className="font-semibold">Scanning your library…</p>
                                <p className="truncate text-sm text-muted">{scanState.message || "Looking for video files…"}</p>
                            </div>
                        </div>
                        <Progress value={scanState.total > 0 ? scanState.done / scanState.total : 0} className={cn("mt-4 h-1.5", scanState.total === 0 && "animate-pulse")} />
                        <p className="mt-3 text-xs text-subtle">
                            Every movie and show is looked up on TMDB, then artwork and episode titles are downloaded. A big first scan can take a few minutes.
                        </p>
                    </div>
                )}

                {host && !loading && titles.length === 0 && (
                    <EmptyState icon={<Share2 className="size-6" />} title="Nothing to show yet">
                        {host.host.name} shares {hostFiles} {hostFiles === 1 ? "file" : "files"}; their movies and shows appear here once TMDB answers.
                    </EmptyState>
                )}

                {!host && off && !loading && (
                    <EmptyState
                        icon={<Share2 className="size-6" />}
                        title="This computer's library is off"
                        action={
                            <Button variant="primary" onClick={() => navigate("/settings?tab=devices")}>
                                Other devices
                            </Button>
                        }
                    >
                        Its movies and shows come from the computers that share their library with it, and none does right now. Turn it back on in Settings › Local library.
                    </EmptyState>
                )}

                {!host && !off && !loading && !scanning && titles.length === 0 && !error && (
                    <EmptyState
                        icon={<HardDrive className="size-6" />}
                        title={libraryDir ? "Nothing in your library yet" : "Choose your library folder"}
                        action={
                            libraryDir ? (
                                <Button loading={scanning} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                                    Scan now
                                </Button>
                            ) : (
                                <Button variant="primary" onClick={() => navigate("/settings?tab=library")}>
                                    Open settings
                                </Button>
                            )
                        }
                    >
                        {libraryDir ? (
                            <>
                                KumoBox looks for video files in <span className="font-mono text-fg/80">{libraryDir}</span>. Download movies and episodes from a provider or with torrents, or copy files there
                                (like <span className="font-mono text-fg/80">Movie (2010)/</span> or <span className="font-mono text-fg/80">Show (2008)/Season 01/</span>).
                            </>
                        ) : (
                            "Pick the folder where your movies and shows live and KumoBox will scan and match them automatically."
                        )}
                    </EmptyState>
                )}

                {recent.length > 0 && !q && (
                    <section className="rise-in" data-tv-autofocus>
                        <h2 className="mb-4 text-xl font-semibold tracking-tight">Recently added</h2>
                        <Carousel itemClassName="w-[300px]">
                            {recent.map(({ title: t, keys, latest, play, allWatched }) => {
                                const m = t.media
                                const show = mediaIsShow(m)
                                return (
                                    <EpisodeCard
                                        key={`${m.id}:${latest}`}
                                        image={m.bannerImage || cover(m)}
                                        label={show ? formatKey(play.episode) : "Movie"}
                                        title={title(m)}
                                        subtitle={`${show ? episodeRanges(keys) : year(m) || "Movie"} · added ${relativeTime(latest)}`}
                                        hasFile
                                        watched={allWatched}
                                        onClick={() => playLocal(play.path, m.id, play.episode)}
                                        actions={
                                            <IconButton label="Open its page" variant="ghost" size="xs" className="bg-black/70 text-white hover:bg-black/85 hover:text-white" onClick={e => (e.stopPropagation(), navigate(entryUrl(m)))}>
                                                <Info className="size-3.5" />
                                            </IconButton>
                                        }
                                    />
                                )
                            })}
                        </Carousel>
                    </section>
                )}

                {(loading || titles.length > 0) && (
                    <section>
                        <div className="mb-6 flex flex-wrap items-center gap-3">
                            <Tabs value={activeFilter} onChange={setFilter} tabs={STATUS_TABS.filter(t => t.value === "ALL" || counts[t.value]).map(t => ({ ...t, count: counts[t.value] ?? 0 }))} />
                            <Tabs
                                value={kind}
                                onChange={setKind}
                                tabs={[
                                    { value: "", label: "All" },
                                    { value: "MOVIE", label: "Movies" },
                                    { value: "TV", label: "Shows" },
                                ]}
                            />
                            <div className="ml-auto flex flex-wrap items-center gap-3 lg:flex-nowrap">
                                <div className="w-60 shrink-0">
                                    <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter by title…" icon={<Search className="size-4" />} />
                                </div>
                                <Select
                                    className="w-48 shrink-0"
                                    value={sort}
                                    onChange={v => setSort(v as SortKey)}
                                    options={[
                                        { value: "added", label: "Recently added" },
                                        { value: "title", label: "Title" },
                                        { value: "unwatched", label: "Most unwatched" },
                                        { value: "episodes", label: "Most episodes" },
                                        { value: "size", label: "Largest on disk" },
                                    ]}
                                />
                                <Tabs
                                    value={view}
                                    onChange={setView}
                                    tabs={[
                                        { value: "grid", label: <LayoutGrid className="size-4" aria-label="Grid" /> },
                                        { value: "list", label: <ListIcon className="size-4" aria-label="List" /> },
                                    ]}
                                />
                            </div>
                        </div>

                        {loading ? (
                            <MediaGrid>
                                {Array.from({ length: 12 }).map((_, i) => (
                                    <MediaCardSkeleton key={i} />
                                ))}
                            </MediaGrid>
                        ) : shown.length === 0 ? (
                            <EmptyState icon={<Search className="size-6" />} title="Nothing matches">
                                Try another filter or search.
                            </EmptyState>
                        ) : view === "grid" ? (
                            // Only the rows of cards near the screen are rendered:
                            // libraries can have thousands of titles.
                            <VirtualGrid
                                items={shown}
                                getKey={t => t.media.id}
                                minItemWidth={cardMinWidth(status?.settings.ui.cardSize)}
                                extraHeight={cardTextHeight}
                                renderItem={t => <MediaCard media={t.media} listEntry={t.listEntry} localCount={t.files.length} downloaded={t.episodes} menu={libraryMenu.menu(t.media)} />}
                            />
                        ) : (
                            <VirtualList
                                items={shown}
                                getKey={t => t.media.id}
                                estimateSize={122}
                                gap={12}
                                renderItem={t => (
                                    <LibraryRow
                                        item={t}
                                        trusted={trusted && !host}
                                        open={openRows.has(t.media.id)}
                                        onToggle={() => toggleRow(t.media.id)}
                                        onOpen={openFolder}
                                        onPlay={f => playLocal(f.path, f.mediaId, f.episode)}
                                    />
                                )}
                            />
                        )}
                    </section>
                )}
            </div>
            {libraryMenu.dialog}
        </div>
    )
}

function Stat({ label, value }: { label: string; value: string }) {
    return (
        <div>
            <p className="text-xs text-subtle">{label}</p>
            <p className="text-lg font-semibold tabular-nums">{value}</p>
        </div>
    )
}

function LibraryRow({
    item: t,
    trusted,
    open,
    onToggle,
    onOpen,
    onPlay,
}: {
    item: LibraryTitle
    trusted: boolean
    open: boolean
    onToggle: () => void
    onOpen: (path: string) => void
    onPlay: (f: LocalFile) => void
}) {
    const m = t.media
    const le = t.listEntry
    const total = totalEpisodes(m)
    const isMovie = !mediaIsShow(m)
    const marker = useEpisodeMarker(m)
    const upTo = watchedUpTo(t)

    return (
        <div className="card overflow-hidden">
            <div className="flex items-center gap-4 p-3 pr-4">
                <Link to={entryUrl(m)} className="shrink-0">
                    <img {...poster(m, 64)} alt="" loading="lazy" decoding="async" className="h-24 w-16 rounded-lg object-cover ring-1 ring-line" style={{ backgroundColor: m.coverImage?.color || undefined }} />
                </Link>
                <div className="min-w-0 flex-1">
                    <Link to={entryUrl(m)} className="line-clamp-1 text-[15px] font-semibold hover:underline">
                        {title(m)}
                    </Link>
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-muted">
                        <span>{formatLabel(m.format)}</span>
                        {year(m) > 0 && <span>· {year(m)}</span>}
                        {le ? <span>· {LIST_STATUS[le.status] ?? le.status}</span> : <span>· Not on your list</span>}
                        {le && total > 0 && !isMovie && (
                            <span>
                                · watched {episodeIndex(m, le.progress)}/{total}
                            </span>
                        )}
                    </div>
                    <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        {isMovie ? (
                            <Badge tone="brand">
                                <HardDrive className="mr-1 size-3" />
                                Movie
                            </Badge>
                        ) : (
                            t.seasons.map(s => (
                                <Tooltip key={s.season} content={s.total ? `${s.count} of ${s.total} episodes of season ${s.season}` : `${s.count} episodes of season ${s.season}`}>
                                    <span>
                                        <Badge tone="brand">
                                            <HardDrive className="mr-1 size-3" />
                                            {s.ranges}
                                            {s.total > 0 && s.count < s.total && <span className="ml-1 opacity-70">({s.count}/{s.total})</span>}
                                        </Badge>
                                    </span>
                                </Tooltip>
                            ))
                        )}
                        {t.specials > 0 && <Badge>+{t.specials} specials</Badge>}
                        {t.extras > 0 && <Badge>+{t.extras} extras</Badge>}
                        {t.unwatched > 0 && <Badge tone="green">{isMovie ? "Unwatched" : `${t.unwatched} unwatched`}</Badge>}
                        {t.resolutions.slice(0, 2).map(r => (
                            <Badge key={r}>{r}</Badge>
                        ))}
                        {t.releaseGroups.slice(0, 2).map(r => (
                            <Badge key={r} tone="blue">
                                {r}
                            </Badge>
                        ))}
                        <span className="text-xs text-subtle">
                            {formatBytes(t.size)} · added {relativeTime(t.addedAt)}
                        </span>
                    </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                    {t.next && (
                        <Button variant="primary" size="sm" icon={<Play className="size-4 fill-current" />} onClick={() => onPlay(t.next!)}>
                            {isMovie ? "Play" : formatKey(t.next.episode)}
                        </Button>
                    )}
                    {trusted && t.dir && (
                        <IconButton label="Open folder" variant="subtle" size="sm" onClick={() => onOpen(t.dir)}>
                            <FolderOpen className="size-4" />
                        </IconButton>
                    )}
                    <IconButton label={open ? "Hide files" : "Show files"} variant="subtle" size="sm" onClick={onToggle}>
                        <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
                    </IconButton>
                </div>
            </div>
            {open && (
                <div className="border-t border-line bg-surface-1/50">
                    {t.files.map(f => {
                        const keys = fileKeys(f)
                        const episode = f.kind === "main" && keys.length > 0
                        const watched = episode && keys.every(k => k <= upTo)
                        return (
                            <div key={f.path} className="group/file flex w-full items-center gap-3 px-4 py-2 text-sm transition hover:bg-white/[0.04]">
                                <button onClick={() => onPlay(f)} title={f.path} className="flex min-w-0 flex-1 items-center gap-3 text-left">
                                    <span className="grid size-7 shrink-0 place-items-center rounded-full bg-white/[0.06] text-muted transition group-hover/file:bg-brand group-hover/file:text-white">
                                        <Play className="ml-0.5 size-3 fill-current" />
                                    </span>
                                    <span className={cn("w-28 shrink-0 font-semibold tabular-nums", watched && "text-subtle")}>{fileLabel(f)}</span>
                                    <span className={cn("min-w-0 flex-1 truncate", watched ? "text-subtle" : "text-fg/85")}>{f.name}</span>
                                </button>
                                {f.parsed?.resolution && <span className="hidden shrink-0 text-xs text-subtle sm:inline">{f.parsed.resolution}</span>}
                                <span className="w-20 shrink-0 text-right text-xs text-subtle tabular-nums">{formatBytes(f.size)}</span>
                                {episode && (
                                    <Tooltip content={watched ? "Mark as unwatched" : "Mark as watched"}>
                                        <button
                                            aria-label={watched ? "Mark as unwatched" : "Mark as watched"}
                                            disabled={marker.pending}
                                            onClick={() => marker.mark(watched ? keys[0] : keys[keys.length - 1], !watched)}
                                            className={cn(
                                                "grid size-7 shrink-0 place-items-center rounded-full transition disabled:opacity-50",
                                                watched ? "bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25" : "text-subtle hover:bg-white/[0.08] hover:text-fg",
                                            )}
                                        >
                                            {watched ? <CheckCheck className="size-3.5" /> : <Check className="size-3.5" />}
                                        </button>
                                    </Tooltip>
                                )}
                            </div>
                        )
                    })}
                    {t.dir && <p className="truncate px-4 py-2 font-mono text-[11px] text-subtle">{t.dir}</p>}
                </div>
            )}
        </div>
    )
}
