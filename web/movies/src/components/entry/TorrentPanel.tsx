import { AlertTriangle, ArrowDownToLine, ChevronDown, Copy, ExternalLink, Magnet, Search, Users } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { episodeKey, firstEpisode, formatKey, latestAired, mediaIsShow, nextEpisode, seasonEpisodeCount, splitKey } from "@/lib/ids"
import { type TorrentSearchBody, useStatus, useTorrentProviders, useTorrentSearch } from "@/lib/queries"
import type { EntryView, Media, TorrentProviderInfo, TorrentResult } from "@/lib/types"
import { cn, copyText, formatBytes, relativeTime } from "@/lib/utils"
import { Badge, Button, EmptyState, ErrorState, Input, Select, Tabs } from "../ui"

// What a show's search looks for: one episode, a season pack, the whole show.
type Scope = "episode" | "season" | "show"

const RESOLUTIONS = [
    { value: "", label: "Any resolution" },
    { value: "2160", label: "2160p (4K)" },
    { value: "1080", label: "1080p" },
    { value: "720", label: "720p" },
    { value: "480", label: "480p" },
]

type SortKey = "best" | "seeders" | "size" | "date"

// The Torrents tab of a movie or show: search every torrent source (or one)
// and send what's picked to the torrent client.
// episode (a key), when given, is the episode to look for first: the one
// "Find torrents" was picked for on the Episodes tab.
export function TorrentPanel({ entry, episode }: { entry: EntryView; episode?: number }) {
    const { data: status } = useStatus()
    const settings = status?.settings
    const media = entry.media
    const show = mediaIsShow(media)
    const { data: providers } = useTorrentProviders()
    const usable = useMemo(() => (providers ?? []).filter(p => p.supports.includes(show ? "tv" : "movie")), [providers, show])

    const [provider, setProvider] = useState(() => settings?.torrent.defaultProvider || "all")
    // The episode after the last one watched (the last aired when all are).
    const start = episode || nextEpisode(media, entry.listEntry?.progress ?? 0) || latestAired(media) || firstEpisode(media)
    const [scope, setScope] = useState<Scope>(() => (show && !episode && media.status !== "RELEASING" && (entry.listEntry?.progress ?? 0) === 0 ? "season" : "episode"))
    // A special (S00E03, from the Episodes tab) is looked for in season 0.
    const [season, setSeason] = useState(() => (show ? splitKey(start)[0] : 1))
    const [ep, setEp] = useState(() => splitKey(start)[1] || 1)
    const [resolution, setResolution] = useState(() => settings?.torrent.preferredResolution ?? "")
    const [query, setQuery] = useState("")
    const [selected, setSelected] = useState<Set<string>>(new Set())
    const [sending, setSending] = useState<string | null>(null)
    const [sort, setSort] = useState<SortKey>("best")
    const [searched, setSearched] = useState<TorrentSearchBody | null>(null)
    const search = useTorrentSearch()

    const run = () => {
        const body: TorrentSearchBody = { provider, mediaId: media.id, query: query.trim() || undefined, resolution }
        if (show) {
            if (scope === "episode") Object.assign(body, { episode: episodeKey(season, ep), season })
            else Object.assign(body, { batch: true, season: scope === "season" ? season : 0 })
        }
        setSelected(new Set())
        setSearched(body)
        search.mutate(body)
    }
    useEffect(() => {
        run()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])
    // A source turned off in the settings: back to all of them.
    useEffect(() => {
        if (providers && provider !== "all" && !providers.some(p => p.id === provider)) setProvider("all")
    }, [providers, provider])

    const all = search.data?.results ?? []
    const errors = search.data?.errors ?? []
    const results = useMemo(() => {
        const want = resolution ? `${resolution}p` : ""
        const list = want ? all.filter(r => r.resolution === want) : [...all]
        const time = (r: TorrentResult) => (r.date ? Date.parse(r.date) || 0 : 0)
        if (sort === "seeders") list.sort((a, b) => b.seeders - a.seeders)
        if (sort === "size") list.sort((a, b) => b.size - a.size)
        if (sort === "date") list.sort((a, b) => time(b) - time(a))
        return list
    }, [all, resolution, sort])
    const hidden = all.length - results.length
    const key = (r: TorrentResult) => r.infoHash || r.magnetLink || r.downloadUrl || r.link || r.name
    const chosen = useMemo(() => results.filter(r => selected.has(key(r))), [results, selected])
    const clientName = settings?.torrent.defaultClient === "transmission" ? "Transmission" : "qBittorrent"
    const noClient = !settings?.torrent.defaultClient || settings.torrent.defaultClient === "none"
    const wide = useWide()
    const rowProps = (r: TorrentResult): RowProps => ({
        show,
        providers: providers ?? [],
        selected: selected.has(key(r)),
        onSelect: () =>
            setSelected(s => {
                const n = new Set(s)
                if (n.has(key(r))) n.delete(key(r))
                else n.add(key(r))
                return n
            }),
        sending: sending === key(r),
        disabled: noClient || sending !== null,
        onDownload: () => download([r], key(r)),
    })

    const download = async (items: TorrentResult[], what: string) => {
        setSending(what)
        try {
            // The season searched, for results whose names don't say theirs.
            const s = searched && show && !(searched.batch && !searched.season) ? (searched.season ?? null) : null
            // The server says where they're saved (a toast through the events).
            await api.post("/api/torrents/download", { mediaId: media.id, season: s, results: items })
            setSelected(new Set())
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setSending(null)
        }
    }

    return (
        <div className="flex flex-col gap-5">
            <div className="card flex flex-col gap-4 p-4">
                {show && (
                    <div className="flex flex-wrap items-end gap-3">
                        <Tabs
                            value={scope}
                            onChange={setScope}
                            tabs={[
                                { value: "episode", label: "Episode" },
                                { value: "season", label: "Season pack" },
                                { value: "show", label: "Whole show" },
                            ]}
                        />
                        {scope !== "show" && (
                            <SeasonPicker
                                media={media}
                                season={season}
                                onChange={s => {
                                    setSeason(s)
                                    setEp(1)
                                }}
                            />
                        )}
                        {scope === "episode" && <EpisodePicker count={seasonEpisodeCount(media, season)} value={ep} onChange={setEp} />}
                    </div>
                )}
                <div className="flex flex-wrap items-end gap-3">
                    <label className="flex w-52 flex-col gap-1.5">
                        <span className="text-xs font-medium text-subtle">Source</span>
                        <Select
                            value={provider}
                            onChange={setProvider}
                            options={[
                                { value: "all", label: `All sources${providers ? ` (${usable.length})` : ""}` },
                                ...usable.map(p => ({ value: p.id, label: p.name + (p.kind === "extension" ? " (extension)" : "") })),
                            ]}
                        />
                    </label>
                    <label className="flex w-40 flex-col gap-1.5">
                        <span className="text-xs font-medium text-subtle">Resolution</span>
                        <Select value={resolution} onChange={setResolution} options={RESOLUTIONS} />
                    </label>
                    <div className="min-w-56 flex-1">
                        <span className="mb-1.5 block text-xs font-medium text-subtle">Search for (optional)</span>
                        <Input
                            value={query}
                            onChange={e => setQuery(e.target.value)}
                            onKeyDown={e => e.key === "Enter" && run()}
                            placeholder={show ? "Leave empty to search by title, season and episode" : "Leave empty to search by title and year"}
                            icon={<Search className="size-4" />}
                        />
                    </div>
                    <Button variant="primary" loading={search.isPending} onClick={run} icon={<Search className="size-4" />}>
                        Search
                    </Button>
                </div>
                {providers && usable.length === 0 && (
                    <p className="text-sm text-amber-200">
                        No torrent source searches {show ? "shows" : "movies"}.{" "}
                        <Link to="/settings?tab=torrent-sources" className="underline">
                            Turn some on
                        </Link>
                    </p>
                )}
            </div>

            {noClient && (
                <div className="rounded-xl border border-amber-500/25 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
                    No torrent client is set up, so nothing can be downloaded yet —{" "}
                    <Link to="/settings?tab=torrent-client" className="underline">
                        choose qBittorrent or Transmission
                    </Link>
                    .
                </div>
            )}

            {errors.length > 0 && !search.isPending && <SourceErrors errors={errors} failedAll={all.length === 0} />}

            {chosen.length > 0 && (
                <div className="sticky top-3 z-20 flex items-center justify-between rounded-xl border border-line-strong bg-surface-2/95 px-4 py-3 shadow-lg shadow-black/40 backdrop-blur rise-in">
                    <span className="text-sm">{chosen.length} selected</span>
                    <Button variant="primary" size="sm" loading={sending === "selected"} disabled={noClient} icon={<ArrowDownToLine className="size-4" />} onClick={() => download(chosen, "selected")}>
                        Send to {clientName}
                    </Button>
                </div>
            )}

            {search.isPending && <div className="card h-40 shimmer" />}
            {search.isError && <ErrorState title="Couldn't search for torrents" error={search.error} onRetry={run} />}
            {search.isSuccess && results.length === 0 && (
                <EmptyState icon={<Magnet className="size-6" />} title={hidden > 0 ? `No ${resolution}p torrents` : "No torrents found"}>
                    {hidden > 0 ? (
                        <button className="underline" onClick={() => setResolution("")}>
                            Show the {hidden} in other resolutions
                        </button>
                    ) : show && scope === "episode" ? (
                        "Try the season pack, another source, or your own search words."
                    ) : (
                        "Try another source, or your own search words."
                    )}
                </EmptyState>
            )}

            {!search.isPending && results.length > 0 && (
                <div className="card overflow-hidden">
                    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-2.5">
                        <span className="text-sm text-muted">
                            {results.length} torrent{results.length === 1 ? "" : "s"}
                            {hidden > 0 && (
                                <>
                                    {" · "}
                                    <button className="underline hover:text-fg" onClick={() => setResolution("")}>
                                        {hidden} in other resolutions
                                    </button>
                                </>
                            )}
                        </span>
                        <Tabs
                            value={sort}
                            onChange={setSort}
                            tabs={[
                                { value: "best", label: "Best" },
                                { value: "seeders", label: "Seeders" },
                                { value: "size", label: "Size" },
                                { value: "date", label: "Newest" },
                            ]}
                        />
                    </div>
                    {wide ? (
                        <table className="w-full table-fixed text-sm">
                            <colgroup>
                                <col className="w-10" />
                                <col />
                                <col className="w-44" />
                                <col className="w-24" />
                                <col className="w-32" />
                                <col className="w-24" />
                                <col className="w-24" />
                                <col className="w-48" />
                            </colgroup>
                            <thead>
                                <tr className="border-b border-line text-left text-xs font-medium text-subtle">
                                    <th className="py-2 pl-4" />
                                    <th className="py-2 pr-3 font-medium">Name</th>
                                    <th className="py-2 pr-3 font-medium">Sources</th>
                                    <th className="py-2 pr-3 text-right font-medium">Size</th>
                                    <th className="py-2 pr-3 text-right font-medium">Seeds / Peers</th>
                                    <th className="py-2 pr-3 font-medium">Quality</th>
                                    <th className="py-2 pr-3 font-medium">Added</th>
                                    <th className="py-2 pr-4" />
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-line">
                                {results.map(r => (
                                    <ResultRow key={key(r)} r={r} {...rowProps(r)} />
                                ))}
                            </tbody>
                        </table>
                    ) : (
                        <div className="divide-y divide-line">
                            {results.map(r => (
                                <ResultItem key={key(r)} r={r} {...rowProps(r)} />
                            ))}
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}

function SeasonPicker({ media, season, onChange }: { media: Media; season: number; onChange: (s: number) => void }) {
    const seasons = [...(media.seasons ?? [])].sort((a, b) => (a.seasonNumber === 0 ? 1 : b.seasonNumber === 0 ? -1 : a.seasonNumber - b.seasonNumber))
    if (seasons.length === 0)
        return (
            <label className="flex w-24 flex-col gap-1.5">
                <span className="text-xs font-medium text-subtle">Season</span>
                <Input type="number" min={0} value={season} onChange={e => onChange(Math.max(0, Number(e.target.value)))} />
            </label>
        )
    return (
        <label className="flex w-52 flex-col gap-1.5">
            <span className="text-xs font-medium text-subtle">Season</span>
            <Select
                value={String(season)}
                onChange={v => onChange(Number(v))}
                options={seasons.map(s => ({
                    value: String(s.seasonNumber),
                    label: `${s.seasonNumber === 0 ? "Specials" : `Season ${s.seasonNumber}`}${s.episodeCount ? ` · ${s.episodeCount} ep.` : ""}`,
                }))}
            />
        </label>
    )
}

function EpisodePicker({ count, value, onChange }: { count: number; value: number; onChange: (e: number) => void }) {
    if (count <= 0)
        return (
            <label className="flex w-24 flex-col gap-1.5">
                <span className="text-xs font-medium text-subtle">Episode</span>
                <Input type="number" min={1} value={value} onChange={e => onChange(Math.max(1, Number(e.target.value)))} />
            </label>
        )
    return (
        <label className="flex w-32 flex-col gap-1.5">
            <span className="text-xs font-medium text-subtle">Episode</span>
            <Select
                value={String(value)}
                onChange={v => onChange(Number(v))}
                options={Array.from({ length: Math.max(count, value) }, (_, i) => ({ value: String(i + 1), label: `Episode ${i + 1}` }))}
            />
        </label>
    )
}

// The sources that failed, folded: a broken site shouldn't hide the results.
function SourceErrors({ errors, failedAll }: { errors: string[]; failedAll: boolean }) {
    const [open, setOpen] = useState(failedAll)
    return (
        <div className={cn("rounded-xl border px-4 py-3 text-sm", failedAll ? "border-rose-500/25 bg-rose-500/10 text-rose-200" : "border-line bg-white/[0.02] text-muted")}>
            <button className="flex w-full items-center gap-2 text-left" onClick={() => setOpen(o => !o)}>
                <AlertTriangle className="size-4 shrink-0" />
                <span className="flex-1">{errors.length === 1 ? "1 source couldn't be searched" : `${errors.length} sources couldn't be searched`}</span>
                <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
            </button>
            {open && (
                <ul className="mt-2 flex flex-col gap-1 pl-6 text-xs">
                    {errors.map(e => (
                        <li key={e} className="break-words">
                            {e}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    )
}

// useWide reports a window wide enough for the results table; narrower
// ones list the results as rows of their own.
function useWide() {
    const query = "(min-width: 1280px)"
    const [wide, setWide] = useState(() => typeof window === "undefined" || window.matchMedia(query).matches)
    useEffect(() => {
        const m = window.matchMedia(query)
        const on = () => setWide(m.matches)
        m.addEventListener("change", on)
        return () => m.removeEventListener("change", on)
    }, [])
    return wide
}

type RowProps = {
    show: boolean
    providers: TorrentProviderInfo[]
    selected: boolean
    onSelect: () => void
    sending: boolean
    disabled: boolean
    onDownload: () => void
}

// What the torrent holds: an episode (S02E05) or a pack, and its group.
function KindBadges({ r, show }: { r: TorrentResult; show: boolean }) {
    return (
        <>
            {show && r.isBatch && <Badge tone="blue">Pack</Badge>}
            {show && !r.isBatch && r.episodeNumber > 0 && <Badge tone="brand">{formatKey(r.episodeNumber)}</Badge>}
            {!show && r.isBatch && <Badge tone="blue">Collection</Badge>}
            {r.releaseGroup && <Badge>{r.releaseGroup}</Badge>}
            {r.trusted && <Badge tone="green">Trusted</Badge>}
        </>
    )
}

// The sources that found it (SearchAll lists them comma-separated).
function SourceBadges({ r, providers }: { r: TorrentResult; providers: TorrentProviderInfo[] }) {
    const name = (id: string) => providers.find(p => p.id === id)?.name ?? id
    return (
        <>
            {r.provider
                .split(",")
                .map(s => s.trim())
                .filter(Boolean)
                .map(s => (
                    <Badge key={s}>{name(s)}</Badge>
                ))}
        </>
    )
}

function Seeds({ r }: { r: TorrentResult }) {
    return (
        <span className="inline-flex items-center gap-1 whitespace-nowrap tabular-nums">
            <Users className="size-3.5 text-subtle" />
            <span className={cn("font-semibold", r.seeders > 20 ? "text-emerald-300" : r.seeders > 0 ? "text-amber-300" : "text-rose-300")}>{r.seeders}</span>
            <span className="text-subtle">/ {r.leechers}</span>
        </span>
    )
}

const sizeOf = (r: TorrentResult) => r.formattedSize || (r.size > 0 ? formatBytes(r.size) : "")
const addedAt = (r: TorrentResult) => (r.date ? Date.parse(r.date) / 1000 || 0 : 0)

function Actions({ r, sending, disabled, onDownload }: { r: TorrentResult } & Pick<RowProps, "sending" | "disabled" | "onDownload">) {
    return (
        <div className="flex items-center justify-end gap-1">
            {r.link && (
                <a
                    href={r.link}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="grid size-8 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg"
                    title="Open the torrent's page"
                >
                    <ExternalLink className="size-4" />
                </a>
            )}
            {r.magnetLink && (
                <button
                    className="grid size-8 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg"
                    title="Copy the magnet link"
                    onClick={async () => {
                        if (await copyText(r.magnetLink)) toast.success("Magnet link copied")
                        else toast.error("Couldn't copy the magnet link: the browser blocked the clipboard")
                    }}
                >
                    <Copy className="size-4" />
                </button>
            )}
            <Button size="sm" variant="white" loading={sending} disabled={disabled} onClick={onDownload} icon={<ArrowDownToLine className="size-4" />}>
                Download
            </Button>
        </div>
    )
}

function ResultRow({ r, show, providers, selected, onSelect, ...actions }: { r: TorrentResult } & RowProps) {
    const time = addedAt(r)
    return (
        <tr className={cn("align-middle transition hover:bg-white/[0.03]", selected && "bg-brand-soft")}>
            <td className="py-3 pl-4">
                <input type="checkbox" checked={selected} onChange={onSelect} className="size-4 accent-[var(--brand)]" aria-label="Select" />
            </td>
            <td className="py-3 pr-3">
                <p className="line-clamp-2 font-medium [overflow-wrap:anywhere]" title={r.name}>
                    {r.name}
                </p>
                <div className="mt-1 flex flex-wrap items-center gap-1.5">
                    <KindBadges r={r} show={show} />
                </div>
            </td>
            <td className="py-3 pr-3">
                <div className="flex flex-wrap gap-1">
                    <SourceBadges r={r} providers={providers} />
                </div>
            </td>
            <td className="py-3 pr-3 text-right whitespace-nowrap tabular-nums text-muted">{sizeOf(r) || "—"}</td>
            <td className="py-3 pr-3 text-right">
                <Seeds r={r} />
            </td>
            <td className="py-3 pr-3 whitespace-nowrap">{r.resolution ? <Badge tone={r.resolution === "2160p" ? "amber" : "gray"}>{r.resolution}</Badge> : <span className="text-subtle">—</span>}</td>
            <td className="py-3 pr-3 whitespace-nowrap text-muted" title={r.date ? new Date(r.date).toLocaleString() : undefined}>
                {time > 0 ? relativeTime(time) : "—"}
            </td>
            <td className="py-3 pr-4">
                <Actions r={r} {...actions} />
            </td>
        </tr>
    )
}

// A result as a row of its own, for narrow windows.
function ResultItem({ r, show, providers, selected, onSelect, ...actions }: { r: TorrentResult } & RowProps) {
    const time = addedAt(r)
    return (
        <div className={cn("flex items-start gap-3 px-4 py-3 transition hover:bg-white/[0.03]", selected && "bg-brand-soft")}>
            <input type="checkbox" checked={selected} onChange={onSelect} className="mt-1 size-4 shrink-0 accent-[var(--brand)]" aria-label="Select" />
            <div className="min-w-0 flex-1">
                <p className="line-clamp-2 text-sm font-medium [overflow-wrap:anywhere]" title={r.name}>
                    {r.name}
                </p>
                <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    <KindBadges r={r} show={show} />
                    {r.resolution && <Badge tone={r.resolution === "2160p" ? "amber" : "gray"}>{r.resolution}</Badge>}
                    <SourceBadges r={r} providers={providers} />
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
                    <Seeds r={r} />
                    {sizeOf(r) && <span className="tabular-nums">{sizeOf(r)}</span>}
                    {time > 0 && <span>{relativeTime(time)}</span>}
                </div>
                <div className="mt-2 flex justify-start">
                    <Actions r={r} {...actions} />
                </div>
            </div>
        </div>
    )
}
