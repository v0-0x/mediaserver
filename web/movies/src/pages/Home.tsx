import { useQueryClient } from "@tanstack/react-query"
import {
    ArrowDownUp,
    ArrowLeftRight,
    ChevronLeft,
    ChevronRight,
    Dices,
    Download,
    FolderOpen,
    FolderSearch,
    FolderSync,
    HardDrive,
    KeyRound,
    LibraryBig,
    ListFilter,
    MoreVertical,
    Plug,
    Puzzle,
    RefreshCw,
    Settings2,
    X,
} from "lucide-react"
import { useMemo, useRef, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { DevicePrompts } from "@/components/DevicePrompts"
import { EpisodeCard } from "@/components/EpisodeCard"
import { LetterboxdDialog, LetterboxdMark } from "@/components/LetterboxdDialog"
import { ListCardMenu } from "@/components/ListCardMenu"
import { Carousel, MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { useLibraryCardMenu } from "@/components/library/LibraryCardMenu"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { ProgramsNotice } from "@/components/ProgramsNotice"
import { SleepingHosts } from "@/components/SleepingHosts"
import { Button, ChipRow, Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, EmptyState, IconButton, SectionHeader, Skeleton } from "@/components/ui"
import { UpdateBanner } from "@/components/UpdateBanner"
import { WatchPicker } from "@/components/WatchPicker"
import { api } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { episodeIndex, formatKey, mediaIsShow, totalEpisodes } from "@/lib/ids"
import { usePlay } from "@/lib/play"
import { resultSummary, useCollection, useDiscover, useLetterboxd, useScan, useStatus } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import { toast } from "@/lib/toast"
import type { CollectionItem, CollectionView, ContinueItem, Media } from "@/lib/types"
import { banner, cn, cover, formatDuration, HOME_SECTIONS, img as imgUrl, title, year } from "@/lib/utils"

type SortKey = "activity" | "title" | "score" | "progress" | "airing"

const KNOWN = new Set(HOME_SECTIONS.map(s => s.id))
const CARD_WIDTH: Record<string, string> = { sm: "w-[130px]", md: "w-[160px]", lg: "w-[200px]" }

export default function HomePage() {
    const { data: status } = useStatus()
    const { data, isLoading, error } = useCollection()
    const tmdb = status?.tmdbConfigured !== false
    const { data: discover } = useDiscover(tmdb)
    const scan = useScan()
    const scanning = useStore(scanStore, s => s.running)
    const letterboxd = useLetterboxd()
    const [genre, setGenre] = useState<string | null>(null)
    const [onlyLocal, setOnlyLocal] = usePersisted("kumobox-home-only-local", false)
    const [sort, setSort] = usePersisted<SortKey>("kumobox-home-sort", "activity")
    const [letterboxdOpen, setLetterboxdOpen] = useState(false)
    const navigate = useNavigate()
    const libraryMenu = useLibraryCardMenu()
    const sections = (status?.settings.ui.homeSections ?? HOME_SECTIONS.map(s => s.id)).filter(id => KNOWN.has(id))
    const cardWidth = CARD_WIDTH[status?.settings.ui.cardSize ?? "md"] ?? CARD_WIDTH.md

    const continueItems = sections.includes("continue") ? (data?.continueWatching ?? []) : []
    const [heroIdx, setHeroIdx] = useState(0)
    // Removing items can leave the index past the end.
    const heroAt = Math.min(heroIdx, Math.max(0, continueItems.length - 1))
    const hero = continueItems[heroAt] ?? null
    const trending = (discover?.trending ?? []).find(m => m.bannerImage) ?? null

    const rows = useMemo(() => {
        const filter = (items: CollectionItem[]) => {
            let out = items
            if (genre) out = out.filter(i => i.media.genres?.includes(genre))
            if (onlyLocal) out = out.filter(i => i.localFiles > 0)
            const sorted = [...out]
            switch (sort) {
                case "title":
                    sorted.sort((a, b) => title(a.media).localeCompare(title(b.media)))
                    break
                case "score":
                    sorted.sort((a, b) => (b.listEntry?.score || b.media.averageScore || 0) - (a.listEntry?.score || a.media.averageScore || 0))
                    break
                case "progress":
                    sorted.sort((a, b) => episodeIndex(b.media, b.listEntry?.progress ?? 0) - episodeIndex(a.media, a.listEntry?.progress ?? 0))
                    break
                case "airing":
                    sorted.sort((a, b) => (a.media.nextAiringEpisode?.airingAt ?? Infinity) - (b.media.nextAiringEpisode?.airingAt ?? Infinity))
                    break
            }
            return sorted
        }
        const ls = data?.lists ?? []
        const of = (...st: string[]) => ls.filter(l => st.includes(l.status)).flatMap(l => l.items)
        const recent = (items: CollectionItem[]) => [...items].sort((a, b) => (b.listEntry?.updatedAt ?? 0) - (a.listEntry?.updatedAt ?? 0))
        const airing = of("CURRENT", "REPEATING", "PLANNING", "PAUSED")
            .filter(i => mediaIsShow(i.media) && i.media.status === "RELEASING")
            .sort((a, b) => (a.media.nextAiringEpisode?.airingAt ?? Infinity) - (b.media.nextAiringEpisode?.airingAt ?? Infinity))
        const all: Record<string, { name: string; subtitle?: string; items: CollectionItem[]; more: string }> = {
            watching: { name: "Watching", items: filter(of("CURRENT", "REPEATING")), more: "/lists?status=CURRENT" },
            airing: { name: "Airing shows", subtitle: "Shows on your list with new episodes", items: genre || onlyLocal ? filter(airing) : airing, more: "/schedule" },
            planning: { name: "Watchlist", items: filter(of("PLANNING")), more: "/lists?status=PLANNING" },
            completed: { name: "Recently watched", items: sort === "activity" ? recent(filter(of("COMPLETED"))) : filter(of("COMPLETED")), more: "/lists?status=COMPLETED" },
        }
        return all
    }, [data, genre, onlyLocal, sort])

    const sync = () => {
        if (!status?.letterboxd) {
            setLetterboxdOpen(true)
            return
        }
        const id = toast.loading("Syncing with Letterboxd…")
        letterboxd.sync.mutate(undefined, { onSuccess: r => toast.success(`Synced with Letterboxd: ${resultSummary(r)}`, { id }), onError: () => toast.dismiss(id) })
    }

    // Paired with another KumoBox (a TV, a computer): Sync brings the watch
    // history from each to the other now, so Continue watching goes on from
    // where it stopped on any of them.
    const paired = status?.paired ?? []
    const [picking, setPicking] = useState(false)
    const [syncing, setSyncing] = useState(false)
    const qc = useQueryClient()
    const syncDevices = async () => {
        setSyncing(true)
        const id = toast.loading(`Syncing with ${paired.join(", ")}…`)
        try {
            const res = await api.post<{ devices: string[]; unreachable: string[] | null; skipped?: string[] | null; updated: number }>("/api/sharing/sync")
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
            // What didn't go as it should, each with why (the server says).
            const notes = [...(res.unreachable ?? []).map(d => `Couldn't sync with ${d}.`), ...(res.skipped ?? []).map(d => `Not synced with ${d}.`)].join(" ")
            if (!res.devices?.length) {
                if (notes) toast.error(notes, { id })
                else toast.success("Nothing to sync", { id })
            } else {
                const what = res.updated > 0 ? `${res.updated} position${res.updated === 1 ? "" : "s"} updated` : "already up to date"
                toast.success(`Synced with ${res.devices.join(", ")}: ${what}.${notes ? " " + notes : ""}`, { id })
            }
        } catch (e: any) {
            toast.error(e.message, { id })
        } finally {
            setSyncing(false)
        }
    }

    const isEmpty = !isLoading && !error && (data?.lists ?? []).every(l => l.items.length === 0) && (data?.localOnly?.length ?? 0) === 0
    const filtered = !!genre || onlyLocal
    const listRowsEmpty = sections.filter(id => rows[id]).every(id => rows[id].items.length === 0)
    const clearFilters = () => {
        setGenre(null)
        setOnlyLocal(false)
    }
    const local = data?.localOnly ?? []

    return (
        <div className="relative min-h-full pb-24">
            <WatchPicker open={picking} onOpenChange={setPicking} />
            <UpdateBanner />
            <SleepingHosts />
            <DevicePrompts />
            <Hero
                item={hero}
                trending={hero ? null : trending}
                count={continueItems.length}
                index={heroAt}
                onIndex={setHeroIdx}
                loading={isLoading}
                toolbar={
                    <div className="glass flex items-center gap-0.5 rounded-lg p-1 [&_button]:text-white/80 [&_button:hover]:bg-white/10 [&_button:hover]:text-white [&_button[data-state=open]]:bg-white/10">
                        {/* On a phone, the buttons' icons only: they fit across. */}
                        <Button size="sm" variant="ghost" icon={<HardDrive className="size-4" />} onClick={() => navigate("/local")} aria-label="Local library">
                            <span className="max-sm:hidden">Local library</span>
                        </Button>
                        <IconButton label="Library tools" size="sm" onClick={() => navigate("/library")}>
                            <LibraryBig className="size-4" />
                        </IconButton>
                        <IconButton label={`Unmatched files (${data?.unmatchedCount ?? 0})`} size="sm" className="relative" onClick={() => navigate("/library?tab=unmatched")}>
                            <FolderSearch className="size-4" />
                            {!!data?.unmatchedCount && <span className="absolute top-1 right-1 size-2 rounded-full bg-amber-400" />}
                        </IconButton>
                        <IconButton label="Downloads" size="sm" onClick={() => navigate("/downloads")}>
                            <Download className="size-4" />
                        </IconButton>
                        <IconButton label="What should I watch?" size="sm" onClick={() => setPicking(true)}>
                            <Dices className="size-4" />
                        </IconButton>
                        <Button size="sm" variant="ghost" loading={scanning || scan.isPending} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)} aria-label="Refresh">
                            <span className="max-sm:hidden">Refresh</span>
                        </Button>
                        {paired.length > 0 && (
                            <Button size="sm" variant="ghost" loading={syncing} icon={<ArrowLeftRight className="size-4" />} onClick={syncDevices} title={`Continue watching from ${paired.join(", ")}`} aria-label="Sync">
                                <span className="max-sm:hidden">Sync</span>
                            </Button>
                        )}
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <IconButton label="Display" size="sm">
                                    <Settings2 className="size-4" />
                                </IconButton>
                            </DropdownTrigger>
                            <DropdownContent>
                                <DropdownLabel>Show</DropdownLabel>
                                <DropdownItem icon={<HardDrive />} onSelect={() => setOnlyLocal(!onlyLocal)}>
                                    {onlyLocal ? "● " : ""}Only what's in my library
                                </DropdownItem>
                                <DropdownItem icon={<ListFilter />} onSelect={() => setOnlyLocal(false)}>
                                    {!onlyLocal ? "● " : ""}Everything on my list
                                </DropdownItem>
                                <DropdownSeparator />
                                <DropdownLabel>Sort by</DropdownLabel>
                                {(
                                    [
                                        ["activity", "Recent activity"],
                                        ["title", "Title"],
                                        ["score", "Rating"],
                                        ["progress", "Progress"],
                                        ["airing", "Next episode"],
                                    ] as [SortKey, string][]
                                ).map(([k, l]) => (
                                    <DropdownItem key={k} icon={<ArrowDownUp />} onSelect={() => setSort(k)}>
                                        {sort === k ? "● " : ""}
                                        {l}
                                    </DropdownItem>
                                ))}
                                <DropdownSeparator />
                                <DropdownItem icon={<Settings2 />} onSelect={() => navigate("/settings?tab=ui")}>
                                    Choose sections…
                                </DropdownItem>
                            </DropdownContent>
                        </Dropdown>
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <IconButton label="More" size="sm">
                                    <MoreVertical className="size-4" />
                                </IconButton>
                            </DropdownTrigger>
                            <DropdownContent>
                                <DropdownItem icon={<RefreshCw />} onSelect={() => scan.mutate(true)}>
                                    Full rescan (re-match everything)
                                </DropdownItem>
                                <DropdownItem icon={<LetterboxdMark className="w-4" />} onSelect={sync}>
                                    {status?.letterboxd ? "Sync with Letterboxd" : "Connect Letterboxd"}
                                </DropdownItem>
                                {status?.client !== "lan" && (
                                    <DropdownItem icon={<FolderOpen />} onSelect={() => api.post("/api/open", { path: status?.settings.library.dir }).catch(e => toast.error(e.message))}>
                                        Open library folder
                                    </DropdownItem>
                                )}
                            </DropdownContent>
                        </Dropdown>
                    </div>
                }
            />

            <div className="relative z-10 -mt-20 flex flex-col gap-10 px-6 md:px-8 xl:px-10">
                <PluginSlot slot="after-home-screen-toolbar" />
                <ProgramsNotice />

                {isEmpty && (
                    <Welcome
                        tmdb={!!status?.tmdbConfigured}
                        letterboxd={!!status?.letterboxd}
                        hasDir={!!status?.settings.library.dir}
                        onLetterboxd={() => setLetterboxdOpen(true)}
                    />
                )}
                {error && (
                    <EmptyState icon={<RefreshCw className="size-6" />} title="Couldn't load your library">
                        {(error as Error).message}
                    </EmptyState>
                )}
                {isLoading && (
                    <>
                        <div className="flex gap-5">
                            {Array.from({ length: 3 }).map((_, i) => (
                                <Skeleton key={i} className="aspect-video w-[400px]" />
                            ))}
                        </div>
                        <MediaGrid>
                            {Array.from({ length: 12 }).map((_, i) => (
                                <MediaCardSkeleton key={i} />
                            ))}
                        </MediaGrid>
                    </>
                )}

                {!isLoading &&
                    sections.map(id => {
                        if (id === "continue") return continueItems.length > 0 ? <ContinueRow key={id} items={continueItems} onFocus={setHeroIdx} /> : null
                        if (id === "genres") return (data?.genres?.length ?? 0) > 0 ? <GenreBar key={id} genres={data!.genres!} value={genre} onChange={setGenre} /> : null
                        const row = rows[id]
                        if (!row || row.items.length === 0) return null
                        return (
                            <section key={id} className="fade-in">
                                <SectionHeader
                                    title={
                                        <span className="flex items-baseline gap-2.5">
                                            {row.name}
                                            <span className="text-sm font-normal text-subtle tabular-nums">{row.items.length}</span>
                                        </span>
                                    }
                                    subtitle={row.subtitle}
                                    action={
                                        <Link to={row.more} className="text-[13px] font-medium text-muted transition-colors hover:text-fg">
                                            See all
                                        </Link>
                                    }
                                />
                                <Carousel itemClassName={cardWidth}>
                                    {row.items.map(it => (
                                        <MediaCard key={it.media.id} media={it.media} listEntry={it.listEntry} localCount={it.localFiles} downloaded={it.downloaded} />
                                    ))}
                                </Carousel>
                            </section>
                        )
                    })}

                {!isLoading && !error && !isEmpty && filtered && listRowsEmpty && (
                    <EmptyState
                        icon={<ListFilter className="size-6" />}
                        title="Nothing matches these filters"
                        action={
                            <Button icon={<X className="size-4" />} onClick={clearFilters}>
                                Clear filters
                            </Button>
                        }
                    >
                        {genre ? `Nothing ${onlyLocal ? "in your library" : "on your list"} is tagged ${genre}.` : "Nothing on your list is in your library yet."}
                    </EmptyState>
                )}

                {local.length > 0 && (
                    <section className="fade-in">
                        <SectionHeader title="In your library" subtitle="Files matched to movies and shows that aren't on your list" />
                        <MediaGrid size={status?.settings.ui.cardSize}>
                            {local.map(it => (
                                // Here only because of local files, which the menu acts on.
                                <MediaCard key={it.media.id} media={it.media} listEntry={it.listEntry} localCount={it.localFiles} downloaded={it.downloaded} menu={libraryMenu.menu(it.media)} />
                            ))}
                        </MediaGrid>
                    </section>
                )}

                {tmdb && discover && <DiscoverRows discover={[
                    { name: "Trending this week", items: discover.trending, more: "/discover" },
                    { name: "Popular movies", items: discover.popularMovies, more: "/search?type=MOVIE&sort=POPULARITY_DESC" },
                    { name: "Popular shows", items: discover.popularShows, more: "/search?type=TV&sort=POPULARITY_DESC" },
                ]} cardWidth={cardWidth} />}

                <PluginSlot slot="home-screen-bottom" />
            </div>
            <LetterboxdDialog open={letterboxdOpen} onOpenChange={setLetterboxdOpen} />
            {libraryMenu.dialog}
        </div>
    )
}

function DiscoverRows({ discover, cardWidth }: { discover: { name: string; items: Media[] | null; more: string }[]; cardWidth: string }) {
    return (
        <>
            {discover
                .filter(r => (r.items?.length ?? 0) > 0)
                .map(r => (
                    <section key={r.name} className="fade-in">
                        <SectionHeader
                            title={r.name}
                            action={
                                <Link to={r.more} className="text-[13px] font-medium text-muted transition-colors hover:text-fg">
                                    See more
                                </Link>
                            }
                        />
                        <Carousel itemClassName={cardWidth}>
                            {r.items!.map(m => (
                                <MediaCard key={m.id} media={m} listEntry={m.mediaListEntry} menu={<ListCardMenu media={m} />} />
                            ))}
                        </Carousel>
                    </section>
                ))}
        </>
    )
}

function Hero({
    item,
    trending,
    count,
    index,
    onIndex,
    loading,
    toolbar,
}: {
    item: ContinueItem | null
    trending: Media | null
    count: number
    index: number
    onIndex: (i: number) => void
    loading: boolean
    toolbar: React.ReactNode
}) {
    // The sharp backdrop, like the entry page: episode stills can be small.
    const img = item ? (item.media.bannerImage ? banner(item.media) : imgUrl(item.image) || cover(item.media)) : trending ? banner(trending) : ""
    const show = item && mediaIsShow(item.media)
    return (
        <div className="relative h-[clamp(300px,44vh,480px)] w-full overflow-hidden">
            {img ? <img key={img} src={img} alt="" className="absolute inset-0 size-full object-cover fade-in" /> : <div className="absolute inset-0 bg-surface-1" />}
            <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/60 to-bg/5" />
            <div className="absolute inset-0 bg-gradient-to-r from-bg/80 via-bg/25 to-transparent" />
            <div className="absolute top-4 right-4 z-20">{toolbar}</div>
            <div className="absolute inset-x-0 bottom-28 px-6 md:px-8 xl:px-10">
                {loading ? (
                    <Skeleton className="h-11 w-96" />
                ) : item ? (
                    // Clear of the arrows at its right on a phone.
                    <div key={item.media.id} className="max-w-3xl rise-in max-sm:pr-20">
                        <p className="text-sm font-medium text-white/65">Continue watching{show ? ` · ${formatKey(item.episode)}` : ""}</p>
                        {/* On a TV the cards below are what's picked (the banner follows them). */}
                        <Link to={`/entry?id=${item.media.id}`} data-tv-skip-nav className="mt-1.5 block text-4xl leading-[1.1] font-semibold tracking-tight text-white transition-colors hover:text-white/85 md:text-[2.75rem]">
                            {title(item.media)}
                        </Link>
                        {show && item.title && <p className="mt-2 text-[15px] text-white/65">{item.title}</p>}
                    </div>
                ) : trending ? (
                    <div key={trending.id} className="max-w-3xl rise-in">
                        <p className="text-sm font-medium text-white/65">Trending this week</p>
                        <Link to={`/entry?id=${trending.id}`} className="mt-1.5 block text-4xl leading-[1.1] font-semibold tracking-tight text-white transition-colors hover:text-white/85 md:text-[2.75rem]">
                            {title(trending)}
                            {year(trending) > 0 && <span className="ml-3 text-[0.55em] font-normal text-white/50">{year(trending)}</span>}
                        </Link>
                    </div>
                ) : (
                    <div className="rise-in">
                        <p className="text-sm font-medium text-white/65">Welcome to</p>
                        <h1 className="mt-1 text-4xl font-semibold tracking-tight">KumoBox</h1>
                    </div>
                )}
            </div>
            {count > 1 && (
                <div data-tv-skip-nav className="absolute right-4 bottom-28 z-20 flex items-center gap-3 md:right-8 xl:right-10">
                    <div className="flex gap-1.5 max-sm:hidden">
                        {Array.from({ length: Math.min(count, 8) }).map((_, i) => (
                            <button
                                key={i}
                                aria-label={`Show ${i + 1}`}
                                onClick={() => onIndex(i)}
                                className={cn("h-1 rounded-full transition-all duration-300", i === index ? "w-5 bg-white" : "w-2.5 bg-white/30 hover:bg-white/50")}
                            />
                        ))}
                    </div>
                    <div className="flex gap-1">
                        <button aria-label="Previous" className="glass grid size-8 place-items-center rounded-full transition-colors hover:bg-black/70" onClick={() => onIndex((index - 1 + count) % count)}>
                            <ChevronLeft className="size-4" />
                        </button>
                        <button aria-label="Next" className="glass grid size-8 place-items-center rounded-full transition-colors hover:bg-black/70" onClick={() => onIndex((index + 1) % count)}>
                            <ChevronRight className="size-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    )
}

function ContinueRow({ items, onFocus }: { items: ContinueItem[]; onFocus: (i: number) => void }) {
    const { watch } = usePlay()
    const navigate = useNavigate()
    const qc = useQueryClient()
    const ref = useRef<HTMLDivElement>(null)
    // The server hides it until it offers another episode or it's watched
    // again. This row and the hero both show the collection's list, so it is
    // edited right away (a refetch in flight would undo that).
    const remove = async (it: ContinueItem, at: number) => {
        const edit = (fn: (list: ContinueItem[]) => ContinueItem[]) =>
            qc.setQueryData<CollectionView>(["collection"], old => (old ? { ...old, continueWatching: fn(old.continueWatching ?? []) } : old))
        const refresh = () => qc.invalidateQueries({ queryKey: ["collection"] })
        await qc.cancelQueries({ queryKey: ["collection"] })
        edit(list => list.filter(x => x.media.id !== it.media.id))
        const hidden = api.post("/api/continue/hide", { mediaId: it.media.id, episode: it.episode })
        hidden.catch(e => toast.error(e.message)).finally(refresh)
        toast.success("Removed from Continue watching", {
            action: {
                label: "Undo",
                onClick: async () => {
                    await qc.cancelQueries({ queryKey: ["collection"] })
                    edit(list => (list.some(x => x.media.id === it.media.id) ? list : [...list.slice(0, at), it, ...list.slice(at)]))
                    await hidden.catch(() => {}) // unhide after the hide, never before
                    api.post("/api/continue/unhide", { mediaId: it.media.id })
                        .catch(e => toast.error(e.message))
                        .finally(refresh)
                },
            },
        })
    }
    return (
        <div ref={ref} className="no-scrollbar -mx-2 flex snap-x scroll-px-2 gap-5 overflow-x-auto px-2 pb-2">
            {items.map((it, i) => {
                const show = mediaIsShow(it.media)
                const total = totalEpisodes(it.media) || it.total
                const left = it.duration > 0 && it.resumeAt > 0 ? it.duration - it.resumeAt : 0
                return (
                    <div key={it.media.id} className="w-[min(82vw,400px)] shrink-0 snap-start" onMouseEnter={() => onFocus(i)} onFocus={() => onFocus(i)} data-tv-autofocus={i === 0 || undefined}>
                        <EpisodeCard
                            large
                            image={it.image || (show ? "" : it.media.bannerImage)}
                            label={show ? formatKey(it.episode) : "Movie"}
                            title={show ? it.title || formatKey(it.episode) : title(it.media)}
                            subtitle={
                                show ? (
                                    <span>
                                        <span className="text-fg/80">{formatKey(it.episode)}</span>
                                        {total > 0 && (
                                            <span className="text-subtle">
                                                {" "}
                                                · {it.index || episodeIndex(it.media, it.episode)} of {total}
                                            </span>
                                        )}
                                        <span className="text-subtle"> · {title(it.media)}</span>
                                    </span>
                                ) : (
                                    <span className="text-subtle">{left > 0 ? `${formatDuration(left)} left` : year(it.media) || "Movie"}</span>
                                )
                            }
                            runtime={it.runtime}
                            hasFile={it.hasFile}
                            progress={it.duration > 0 ? it.resumeAt / it.duration : 0}
                            resumeAt={it.resumeAt}
                            onClick={() => watch(it.media.id, it.episode, it.hasFile ? it.filePath : null)}
                            onRemove={() => remove(it, i)}
                            onInfo={() => navigate(`/entry?id=${it.media.id}`)}
                        />
                    </div>
                )
            })}
        </div>
    )
}

function GenreBar({ genres, value, onChange }: { genres: string[]; value: string | null; onChange: (g: string | null) => void }) {
    return (
        <ChipRow label="More genres">
            <button
                onClick={() => onChange(null)}
                className={cn("h-8 shrink-0 rounded-full px-3.5 text-[13px] font-medium transition-colors", !value ? "bg-white text-neutral-950" : "bg-white/[0.05] text-muted hover:bg-white/[0.09] hover:text-fg")}
            >
                All
            </button>
            {genres.map(g => (
                <button
                    key={g}
                    onClick={() => onChange(value === g ? null : g)}
                    className={cn("h-8 shrink-0 rounded-full px-3.5 text-[13px] font-medium transition-colors", value === g ? "bg-white text-neutral-950" : "bg-white/[0.05] text-muted hover:bg-white/[0.09] hover:text-fg")}
                >
                    {g}
                </button>
            ))}
        </ChipRow>
    )
}

function Welcome({ tmdb, letterboxd, hasDir, onLetterboxd }: { tmdb: boolean; letterboxd: boolean; hasDir: boolean; onLetterboxd: () => void }) {
    const steps = [
        {
            done: tmdb,
            icon: <KeyRound className="size-5" />,
            title: "Add a TMDB key",
            text: "Free from themoviedb.org: posters, seasons, episodes and where to watch for every movie and show.",
            action: (
                <Link to="/settings?tab=metadata">
                    <Button size="sm" variant="primary">
                        Set up TMDB
                    </Button>
                </Link>
            ),
        },
        {
            done: hasDir,
            icon: <FolderOpen className="size-5" />,
            title: "Choose your library folder",
            text: "KumoBox scans it, matches every movie and episode, and grabs artwork and descriptions.",
            action: (
                <Link to="/settings?tab=library">
                    <Button size="sm">Open settings</Button>
                </Link>
            ),
        },
        {
            done: letterboxd,
            icon: <Plug className="size-5" />,
            title: "Connect Letterboxd",
            text: "Optional: bring your diary, ratings and watchlist, and send back what you watch here.",
            action: (
                <Button size="sm" onClick={onLetterboxd}>
                    Connect
                </Button>
            ),
        },
        {
            done: false,
            icon: <Puzzle className="size-5" />,
            title: "Add streaming providers",
            text: "Turn on the Internet Archive or the luffy and FreeFlix sites, or add Stremio addons, Nuvio repositories and extensions.",
            action: (
                <Link to="/extensions">
                    <Button size="sm">Providers & extensions</Button>
                </Link>
            ),
        },
    ]
    return (
        <div className="card overflow-hidden rise-in">
            <div className="p-7">
                <h2 className="text-xl font-semibold tracking-tight">Let’s set things up</h2>
                <p className="mt-1 text-sm text-muted">A few steps and your movies and shows are ready.</p>
                <div className="mt-6 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                    {steps.map((s, i) => (
                        <div key={i} className={cn("flex flex-col gap-3 rounded-lg bg-white/[0.03] p-5", s.done && "opacity-60")}>
                            <div className="flex items-center gap-3">
                                <span className={cn("grid size-9 place-items-center rounded-lg [&_svg]:size-[18px]", s.done ? "bg-emerald-500/15 text-emerald-300" : "bg-white/[0.06] text-fg/80")}>{s.icon}</span>
                                <span className="text-xs text-subtle">
                                    Step {i + 1}
                                    {s.done ? " · done" : ""}
                                </span>
                            </div>
                            <p className="font-medium">{s.title}</p>
                            <p className="flex-1 text-sm text-muted">{s.text}</p>
                            {!s.done && <div>{s.action}</div>}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    )
}
