import { useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ChevronDown, Pencil, Play, Plus, RotateCcw, Rss, Search, Trash2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "@/lib/toast"
import { MediaPicker } from "@/components/MediaPicker"
import { Badge, Button, Dialog, EmptyState, Field, IconButton, Input, Select, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { formatKey, mediaIsShow, mediaRef, seasonEpisodeCount, splitKey } from "@/lib/ids"
import { fetchEntry, useAutoItems, useAutoRules, useCollection, useRuleWanted, useSaveSettings, useStatus } from "@/lib/queries"
import type { AutoItem, AutoRule, AutoRunReport, Media } from "@/lib/types"
import { cn, entryUrl, poster, relativeTime, title } from "@/lib/utils"
import { PathInput } from "./settings/parts"

const RESOLUTIONS = [
    { value: "", label: "Any resolution" },
    { value: "2160", label: "2160p (4K)" },
    { value: "1080", label: "1080p" },
    { value: "720", label: "720p" },
    { value: "480", label: "480p" },
]

const newRule = (resolution: string): AutoRule => ({
    mediaId: 0,
    enabled: true,
    title: "",
    resolution,
    releaseGroups: [],
    mustContain: [],
    mustNotContain: [],
    minSeeders: 1,
    packs: true,
    destination: "",
    startFrom: 0,
})

// The shows of the list the auto downloader is for: those being watched
// and those on the watchlist.
function useListShows() {
    const { data } = useCollection()
    return useMemo(() => {
        const out: Media[] = []
        for (const l of data?.lists ?? []) {
            if (l.status !== "CURRENT" && l.status !== "PLANNING") continue
            for (const it of l.items) if (mediaIsShow(it.media) && !out.some(m => m.id === it.media.id)) out.push(it.media)
        }
        return out.sort((a, b) => title(a).localeCompare(title(b)))
    }, [data])
}

// A show by id: from the list, else its page (the title page's own query,
// which this shares).
function useShow(id: number, list: Media[]) {
    const inList = list.find(m => m.id === id)
    const { data } = useQuery({
        queryKey: ["entry", id],
        queryFn: () => fetchEntry(id),
        enabled: id > 0 && !inList,
        staleTime: 3600_000,
        retry: false,
    })
    return inList ?? data?.media
}

export default function AutoDownloaderPage() {
    const { data: rules, isLoading } = useAutoRules()
    const { data: items } = useAutoItems()
    const { data: status } = useStatus()
    const save = useSaveSettings()
    const shows = useListShows()
    const [editing, setEditing] = useState<AutoRule | null>(null)
    const [running, setRunning] = useState(false)
    const [report, setReport] = useState<AutoRunReport | null>(null)
    const qc = useQueryClient()
    const settings = status?.settings
    const noClient = !settings?.torrent.defaultClient || settings.torrent.defaultClient === "none"

    const run = async () => {
        setRunning(true)
        try {
            const r = await api.post<AutoRunReport>("/api/autodownloader/run")
            setReport(r)
            if (r.busy) toast.info("The auto downloader is already checking")
            else toast.success(r.added ? `Sent ${r.added} torrent(s) to the torrent client` : r.wanted ? `Looked for ${r.wanted} episode(s), nothing found yet` : "Nothing to look for right now")
            qc.invalidateQueries({ queryKey: ["auto-items"] })
            qc.invalidateQueries({ queryKey: ["auto-rules"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setRunning(false)
        }
    }

    const titles = useMemo(() => {
        const m = new Map<number, string>()
        for (const r of rules ?? []) if (r.title) m.set(r.mediaId, r.title)
        for (const s of shows) m.set(s.id, title(s))
        return m
    }, [rules, shows])

    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Auto downloader</h1>
                    <p className="mt-1 max-w-2xl text-muted">
                        Keeps the shows you follow downloaded: new episodes (and the ones you haven't got yet) are found on your torrent sources and sent to your torrent client, saved where your
                        library finds them.
                    </p>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                    {settings && (
                        <label className="flex items-center gap-3 rounded-xl border border-line bg-surface-1 px-4 py-2">
                            <span className="text-sm font-medium">Check every {settings.torrent.autoDownloadMinutes} min</span>
                            <Switch checked={settings.torrent.autoDownloader} onChange={v => save.mutate({ ...settings, torrent: { ...settings.torrent, autoDownloader: v } })} />
                        </label>
                    )}
                    <Button icon={<Play className="size-4" />} loading={running} onClick={run} disabled={!rules?.length}>
                        Check now
                    </Button>
                    <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEditing(newRule(settings?.torrent.preferredResolution ?? "1080"))}>
                        New rule
                    </Button>
                </div>
            </div>

            {noClient && (
                <div className="mb-6 rounded-xl border border-amber-500/25 bg-amber-500/10 p-4 text-sm text-amber-200">
                    No torrent client is configured — <Link to="/settings?tab=torrents" className="underline">set one up</Link> first.
                </div>
            )}
            {settings && !settings.torrent.autoDownloader && (rules ?? []).some(r => r.enabled) && (
                <div className="mb-6 rounded-xl border border-line bg-white/[0.02] p-4 text-sm text-muted">The auto downloader is off: your rules only run when you press Check now.</div>
            )}
            {report && <RunSummary report={report} onClose={() => setReport(null)} />}

            {!isLoading && (rules ?? []).length === 0 && (
                <EmptyState
                    icon={<Rss className="size-6" />}
                    title="No rules yet"
                    action={
                        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEditing(newRule(settings?.torrent.preferredResolution ?? "1080"))}>
                            New rule
                        </Button>
                    }
                >
                    Add a rule for each show you want to keep downloaded, e.g. “1080p, NTb or FLUX first, no HDTV”.
                </EmptyState>
            )}
            <div className="grid gap-3 xl:grid-cols-2">
                {(rules ?? []).map(r => (
                    <RuleCard key={r.id} rule={r} shows={shows} onEdit={() => setEditing(r)} />
                ))}
            </div>

            {(items ?? []).length > 0 && <History items={items!} titles={titles} />}
            {editing && <RuleDialog rule={editing} shows={shows} canPickFolder={status?.client !== "lan"} onClose={() => setEditing(null)} />}
        </div>
    )
}

function RunSummary({ report, onClose }: { report: AutoRunReport; onClose: () => void }) {
    const [open, setOpen] = useState(true)
    const bad = report.errors.length > 0
    return (
        <div className={cn("mb-6 rounded-xl border px-4 py-3 text-sm", bad ? "border-amber-500/25 bg-amber-500/10 text-amber-100" : "border-line bg-white/[0.02] text-muted")}>
            <div className="flex items-center gap-3">
                {bad && <AlertTriangle className="size-4 shrink-0 text-amber-300" />}
                <span className="flex-1">
                    {report.busy
                        ? "A check was already running."
                        : `Looked for ${report.wanted} episode${report.wanted === 1 ? "" : "s"}, sent ${report.added} torrent${report.added === 1 ? "" : "s"} to the client${bad ? `, ${report.errors.length} problem${report.errors.length === 1 ? "" : "s"}` : ""}.`}
                </span>
                {bad && (
                    <button className="flex items-center gap-1 text-xs hover:text-fg" onClick={() => setOpen(o => !o)}>
                        Details <ChevronDown className={cn("size-3.5 transition-transform", open && "rotate-180")} />
                    </button>
                )}
                <button className="text-xs text-subtle hover:text-fg" onClick={onClose}>
                    Dismiss
                </button>
            </div>
            {bad && open && (
                <ul className="mt-2 flex flex-col gap-1 pl-7 text-xs">
                    {report.errors.map(e => (
                        <li key={e} className="break-words">
                            {e}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    )
}

// "S02E03, S02E04 and 5 more".
function keysText(keys: number[], max = 4) {
    const shown = keys.slice(0, max).map(formatKey).join(", ")
    return keys.length > max ? `${shown} and ${keys.length - max} more` : shown
}

function RuleCard({ rule, shows, onEdit }: { rule: AutoRule; shows: Media[]; onEdit: () => void }) {
    const qc = useQueryClient()
    const media = useShow(rule.mediaId, shows)
    const { data: wanted, isLoading } = useRuleWanted(rule.id, rule.enabled)
    const toggle = async (enabled: boolean) => {
        try {
            await api.post("/api/autodownloader/rules", { ...rule, enabled })
            qc.invalidateQueries({ queryKey: ["auto-rules"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const del = async () => {
        if (!confirm(`Delete the rule for ${media ? title(media) : rule.title}?`)) return
        try {
            await api.del(`/api/autodownloader/rules/${rule.id}`)
            qc.invalidateQueries({ queryKey: ["auto-rules"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const name = media ? title(media) : rule.title || mediaRef(rule.mediaId)
    return (
        <div className={cn("card flex gap-4 p-3 pr-4", !rule.enabled && "opacity-70")}>
            <Link to={entryUrl({ id: rule.mediaId })} className="shrink-0">
                {media ? <img {...poster(media, 64)} alt="" className="h-24 w-16 rounded-lg object-cover" /> : <div className="h-24 w-16 rounded-lg bg-white/[0.05]" />}
            </Link>
            <div className="min-w-0 flex-1">
                <div className="flex items-start gap-2">
                    <Link to={entryUrl({ id: rule.mediaId })} className="min-w-0 flex-1 truncate font-semibold hover:underline">
                        {name}
                    </Link>
                    <Switch checked={rule.enabled} onChange={toggle} />
                    <IconButton label="Edit" onClick={onEdit} className="size-8">
                        <Pencil className="size-4" />
                    </IconButton>
                    <IconButton label="Delete" onClick={del} className="size-8 hover:text-rose-300">
                        <Trash2 className="size-4" />
                    </IconButton>
                </div>
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                    <Badge>{rule.resolution ? `${rule.resolution}p` : "Any resolution"}</Badge>
                    {rule.releaseGroups.map((g, i) => (
                        <Badge key={g} tone="brand">
                            {i === 0 ? "Prefers " : ""}
                            {g}
                        </Badge>
                    ))}
                    {rule.packs && <Badge tone="blue">Season packs</Badge>}
                    {rule.minSeeders > 1 && <Badge>≥ {rule.minSeeders} seeders</Badge>}
                    {rule.mustContain.map(w => (
                        <Badge key={"+" + w} tone="green">
                            +{w}
                        </Badge>
                    ))}
                    {rule.mustNotContain.map(w => (
                        <Badge key={"-" + w} tone="red">
                            −{w}
                        </Badge>
                    ))}
                    {rule.startFrom > 0 && <Badge>From {formatKey(rule.startFrom)}</Badge>}
                </div>
                <p className="mt-2 truncate text-xs text-subtle">
                    {!rule.enabled
                        ? "Paused"
                        : isLoading
                          ? "…"
                          : wanted
                            ? wanted.episodes.length
                                ? `Looking for ${keysText(wanted.episodes)}`
                                : "Up to date: nothing aired that you don't have"
                            : "Couldn't work out what's missing (is the show's data loaded?)"}
                </p>
                {rule.destination && <p className="mt-0.5 truncate text-xs text-subtle">Saves to {rule.destination}</p>}
            </div>
        </div>
    )
}

function History({ items, titles }: { items: AutoItem[]; titles: Map<number, string> }) {
    const qc = useQueryClient()
    const [all, setAll] = useState(false)
    const forget = async (it: AutoItem) => {
        try {
            await api.del(`/api/autodownloader/items/${encodeURIComponent(it.key)}`)
            qc.invalidateQueries({ queryKey: ["auto-items"] })
            qc.invalidateQueries({ queryKey: ["auto-rules"] })
            toast.success(`${it.pack ? `Season ${splitKey(it.episode)[0]}` : formatKey(it.episode)} can be grabbed again`)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const shown = all ? items : items.slice(0, 30)
    return (
        <section className="mt-12">
            <h2 className="mb-4 text-lg font-semibold tracking-tight">Grabbed</h2>
            <div className="card divide-y divide-line">
                {shown.map(it => (
                    <div key={it.key} className="flex items-center gap-4 px-4 py-3 text-sm">
                        <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                                <Link to={entryUrl({ id: it.mediaId })} className="truncate font-medium hover:underline">
                                    {titles.get(it.mediaId) ?? mediaRef(it.mediaId)}
                                </Link>
                                <Badge tone={it.pack ? "blue" : "brand"}>{it.pack ? `Season ${splitKey(it.episode)[0]} pack` : formatKey(it.episode)}</Badge>
                            </div>
                            <p className="mt-0.5 truncate text-xs text-subtle" title={it.title}>
                                {it.title}
                            </p>
                        </div>
                        <span className="shrink-0 text-xs text-subtle">{relativeTime(it.createdAt)}</span>
                        <IconButton label="Forget (grab it again)" onClick={() => forget(it)} className="size-8">
                            <RotateCcw className="size-4" />
                        </IconButton>
                    </div>
                ))}
            </div>
            {items.length > 30 && !all && (
                <button className="mt-3 text-sm text-muted underline hover:text-fg" onClick={() => setAll(true)}>
                    Show all {items.length}
                </button>
            )}
        </section>
    )
}

// A comma-separated list as typed; only the cleaned-up list goes to the rule.
function WordsInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
    const [text, setText] = useState(value.join(", "))
    return (
        <Input
            value={text}
            placeholder={placeholder}
            onChange={e => {
                setText(e.target.value)
                onChange(
                    e.target.value
                        .split(",")
                        .map(x => x.trim())
                        .filter(Boolean),
                )
            }}
            onBlur={() => setText(value.join(", "))}
        />
    )
}

function RuleDialog({ rule, shows, canPickFolder, onClose }: { rule: AutoRule; shows: Media[]; canPickFolder: boolean; onClose: () => void }) {
    const [r, setR] = useState<AutoRule>(rule)
    const [pick, setPick] = useState(false)
    const [picked, setPicked] = useState<Media | null>(null)
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    const listed = useShow(r.mediaId, picked ? [...shows, picked] : shows)
    const media = picked?.id === r.mediaId ? picked : listed
    const options = useMemo(() => {
        const out = shows.map(m => ({ value: String(m.id), label: title(m) }))
        if (r.mediaId && !out.some(o => o.value === String(r.mediaId))) out.unshift({ value: String(r.mediaId), label: media ? title(media) : r.title || mediaRef(r.mediaId) })
        return [{ value: "0", label: shows.length ? "Choose a show…" : "No shows on your list yet" }, ...out]
    }, [shows, r.mediaId, r.title, media])
    const seasons = (media?.seasons ?? []).filter(s => s.seasonNumber > 0).sort((a, b) => a.seasonNumber - b.seasonNumber)
    const [startSeason, startEpisode] = r.startFrom ? splitKey(r.startFrom) : [0, 0]

    const save = async () => {
        if (!r.mediaId) return toast.error("Pick a show first")
        setBusy(true)
        try {
            await api.post("/api/autodownloader/rules", { ...r, title: media ? title(media) : r.title })
            qc.invalidateQueries({ queryKey: ["auto-rules"] })
            toast.success("Rule saved")
            onClose()
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Dialog
            open
            onOpenChange={v => !v && onClose()}
            title={rule.id ? "Edit rule" : "New rule"}
            description="Which show to keep downloaded, and which releases to take."
            className="w-[min(94vw,640px)]"
            footer={
                <>
                    <Button variant="ghost" onClick={onClose}>
                        Cancel
                    </Button>
                    <Button variant="primary" loading={busy} onClick={save}>
                        Save rule
                    </Button>
                </>
            }
        >
            <div className="flex flex-col gap-5">
                <Field label="Show" help="Shows you're watching or have on your watchlist.">
                    <div className="flex gap-2">
                        <Select className="flex-1" value={String(r.mediaId)} onChange={v => setR({ ...r, mediaId: Number(v), startFrom: 0 })} options={options} />
                        <Button icon={<Search className="size-4" />} onClick={() => setPick(true)}>
                            Other…
                        </Button>
                    </div>
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Resolution">
                        <Select value={r.resolution} onChange={v => setR({ ...r, resolution: v })} options={RESOLUTIONS} />
                    </Field>
                    <Field label="Minimum seeders">
                        <Input type="number" min={1} value={r.minSeeders} onChange={e => setR({ ...r, minSeeders: Math.max(0, Number(e.target.value)) })} />
                    </Field>
                    <Field label="Preferred release groups" help="In order, comma-separated. Taken over better-seeded releases of other groups.">
                        <WordsInput value={r.releaseGroups} onChange={v => setR({ ...r, releaseGroups: v })} placeholder="NTb, FLUX" />
                    </Field>
                    <Field label="Must contain" help="Words every release name must have, e.g. WEB-DL.">
                        <WordsInput value={r.mustContain} onChange={v => setR({ ...r, mustContain: v })} />
                    </Field>
                    <Field label="Must not contain" help="Releases with any of these are skipped, e.g. HDTV, x265.">
                        <WordsInput value={r.mustNotContain} onChange={v => setR({ ...r, mustNotContain: v })} />
                    </Field>
                    <Field label="Start from" help="Episodes before it (and the ones you've watched) are never downloaded.">
                        <div className="flex gap-2">
                            <Select
                                className="flex-1"
                                value={String(startSeason)}
                                onChange={v => setR({ ...r, startFrom: Number(v) ? Number(v) * 1000 + 1 : 0 })}
                                options={[{ value: "0", label: "After what I've watched" }, ...seasons.map(s => ({ value: String(s.seasonNumber), label: `Season ${s.seasonNumber}` }))]}
                            />
                            {startSeason > 0 && (
                                <Select
                                    className="w-28"
                                    value={String(startEpisode)}
                                    onChange={v => setR({ ...r, startFrom: startSeason * 1000 + Number(v) })}
                                    options={Array.from({ length: Math.max(seasonEpisodeCount(media, startSeason), startEpisode, 1) }, (_, i) => ({ value: String(i + 1), label: `Episode ${i + 1}` }))}
                                />
                            )}
                        </div>
                    </Field>
                </div>
                <label className="flex items-center justify-between gap-6">
                    <span>
                        <span className="block text-[13px] font-medium text-fg/90">Season packs</span>
                        <span className="text-xs text-subtle">When several episodes of a season that has finished airing are missing, download the whole season at once.</span>
                    </span>
                    <Switch checked={r.packs} onChange={v => setR({ ...r, packs: v })} />
                </label>
                {canPickFolder && (
                    <Field label="Save to" help="Empty: the show's folder in your library (Show (Year)/Season NN).">
                        <PathInput value={r.destination} onChange={v => setR({ ...r, destination: v })} placeholder="Library folder" />
                    </Field>
                )}
            </div>
            <MediaPicker
                open={pick}
                onOpenChange={setPick}
                type="TV"
                heading="Choose a show"
                onPick={m => {
                    setPicked(m)
                    setR(x => ({ ...x, mediaId: m.id, title: title(m), startFrom: 0 }))
                }}
            />
        </Dialog>
    )
}
