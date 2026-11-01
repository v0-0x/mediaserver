import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Copy, HardDrive, Trash2, Undo2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { Button, EmptyState, Select } from "@/components/ui"
import { api } from "@/lib/api"
import { formatRanges } from "@/lib/missing"
import { useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"
import { cn, formatBytes, relativeTime } from "@/lib/utils"

type CleanupFile = { path: string; name: string; size: number; mediaId: number; episode: number; title: string; resolution?: string; watchedAt?: number }
type TrashItem = { id: string; path: string; name: string; size: number; mediaId: number; episode: number; deletedAt: number }
type CleanupView = {
    disk: { total: number; free: number } | null
    libraryBytes: number
    watched: CleanupFile[]
    duplicates: CleanupFile[][]
    trash: TrashItem[] | null
    trashBytes: number
}

const DAY = 86400
const AGES = [
    { value: "7", label: "over a week ago" },
    { value: "30", label: "over a month ago" },
    { value: "90", label: "over 3 months ago" },
    { value: "365", label: "over a year ago" },
    { value: "0", label: "any time" },
]

// Library tools › Clean up: the space the library takes, and episodes to
// put away: those watched a while ago, and second copies of an episode. They
// go to Aniko's trash (back with one click), which frees the space when it's
// emptied, or by itself after 30 days.
export function Cleanup() {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({ queryKey: ["library", "cleanup"], queryFn: () => api.get<CleanupView>("/api/library/cleanup") })
    const [busy, setBusy] = useState(false)
    const refresh = () => {
        qc.invalidateQueries({ queryKey: ["library"] })
        qc.invalidateQueries({ queryKey: ["collection"] })
        qc.invalidateQueries({ queryKey: ["entry"] })
    }
    const toTrash = async (paths: string[]) => {
        setBusy(true)
        try {
            const res = await api.post<{ moved: number; failed: Record<string, string> }>("/api/library/trash", { paths })
            const failed = Object.entries(res.failed ?? {})
            if (res.moved) toast.success(`${res.moved} ${res.moved === 1 ? "file" : "files"} moved to Aniko's trash. Put them back from it below, or empty it to free the space.`)
            if (failed.length) toast.error(`${failed.length} couldn't be moved: ${failed[0][1]}`)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
            refresh()
        }
    }
    if (isLoading) return <div className="card h-40 shimmer" />
    if (error || !data) return <EmptyState title="Couldn't look at the library">{(error as Error)?.message}</EmptyState>
    return (
        <div className="flex flex-col gap-10">
            <DiskUsage data={data} />
            {lan && <p className="rounded-lg bg-white/[0.04] px-3 py-2 text-xs text-muted">Files can only be put away on the computer Aniko runs on.</p>}
            <WatchedFiles files={data.watched} busy={busy} readOnly={lan} onTrash={toTrash} />
            <Duplicates groups={data.duplicates} busy={busy} readOnly={lan} onTrash={toTrash} />
            <TrashList items={data.trash ?? []} bytes={data.trashBytes} readOnly={lan} onChange={refresh} />
        </div>
    )
}

// How full the library's drive is: a meter, its fill amber then red as the
// drive fills up, with the numbers written out.
function DiskUsage({ data }: { data: CleanupView }) {
    const d = data.disk
    const used = d ? d.total - d.free : 0
    const share = d && d.total > 0 ? used / d.total : 0
    const tone = share > 0.95 ? "bg-rose-400" : share > 0.85 ? "bg-amber-300" : "bg-brand"
    const track = share > 0.95 ? "bg-rose-400/20" : share > 0.85 ? "bg-amber-300/20" : "bg-brand/20"
    return (
        <section className="card flex flex-col gap-3 px-5 py-4">
            <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
                <p className="flex items-center gap-2 font-medium">
                    <HardDrive className="size-4 text-muted" /> Library: {formatBytes(data.libraryBytes)}
                </p>
                {d && (
                    <p className="text-sm text-muted">
                        {formatBytes(d.free)} free of {formatBytes(d.total)} on its drive
                    </p>
                )}
            </div>
            {d && (
                <div
                    className={cn("h-2 overflow-hidden rounded-full", track)}
                    role="meter"
                    aria-label="Drive space used"
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={Math.round(share * 100)}
                >
                    <div className={cn("h-full rounded-full", tone)} style={{ width: `${Math.min(100, share * 100)}%` }} />
                </div>
            )}
            {data.trashBytes > 0 && <p className="text-xs text-subtle">Aniko's trash takes {formatBytes(data.trashBytes)} of it until it's emptied (below).</p>}
        </section>
    )
}

type Props = { busy: boolean; readOnly: boolean; onTrash: (paths: string[]) => void }

// Episodes watched a while ago, by anime: each anime's are put away together.
function WatchedFiles({ files, busy, readOnly, onTrash }: Props & { files: CleanupFile[] }) {
    const [age, setAge] = useState("30")
    const [selected, setSelected] = useState<Set<number>>(new Set())
    const groups = useMemo(() => {
        const days = Number(age)
        const before = Date.now() / 1000 - days * DAY
        const out = new Map<number, { title: string; files: CleanupFile[] }>()
        for (const f of files) {
            // Unknown when (watched on AniList only): only with "any time".
            const at = f.watchedAt ?? 0
            if (days > 0 && (at <= 0 || at > before)) continue
            const g = out.get(f.mediaId) ?? { title: f.title || `Anime ${f.mediaId}`, files: [] }
            g.files.push(f)
            out.set(f.mediaId, g)
        }
        return [...out.entries()].map(([id, g]) => ({
            id,
            ...g,
            episodes: [...new Set(g.files.map(f => f.episode))].sort((a, b) => a - b),
            size: g.files.reduce((s, f) => s + f.size, 0),
        }))
    }, [files, age])
    const chosen = groups.filter(g => selected.has(g.id))
    const chosenSize = chosen.reduce((s, g) => s + g.size, 0)
    const chosenFiles = chosen.flatMap(g => g.files)
    return (
        <section className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                    <h3 className="text-[15px] font-semibold">Watched episodes</h3>
                    <p className="text-[13px] text-muted">Watched to the end here, or marked watched on your list.</p>
                </div>
                <div className="flex items-center gap-2 text-sm text-muted">
                    Watched
                    <Select className="w-44" value={age} onChange={v => (setAge(v), setSelected(new Set()))} options={AGES} />
                </div>
            </div>
            {groups.length === 0 ? (
                <p className="card px-4 py-6 text-center text-sm text-muted">No episodes watched {AGES.find(a => a.value === age)?.label} are in the library.</p>
            ) : (
                <div className="card divide-y divide-line">
                    {groups.map(g => (
                        <label key={g.id} className={cn("flex cursor-pointer items-center gap-4 px-4 py-3 hover:bg-white/[0.03]", selected.has(g.id) && "bg-brand-soft")}>
                            <input
                                type="checkbox"
                                checked={selected.has(g.id)}
                                disabled={readOnly}
                                onChange={() =>
                                    setSelected(s => {
                                        const n = new Set(s)
                                        n.has(g.id) ? n.delete(g.id) : n.add(g.id)
                                        return n
                                    })
                                }
                                className="size-4 shrink-0 accent-[var(--brand)]"
                            />
                            <div className="min-w-0 flex-1">
                                <Link to={`/entry?id=${g.id}`} onClick={e => e.stopPropagation()} className="block truncate text-sm font-medium hover:underline">
                                    {g.title}
                                </Link>
                                <p className="mt-0.5 text-xs text-subtle">
                                    {g.episodes.length === 1 ? "Episode" : "Episodes"} {formatRanges(g.episodes)}
                                    {g.files.length > g.episodes.length && ` (${g.files.length} files)`}
                                </p>
                            </div>
                            <span className="shrink-0 text-sm text-muted tabular-nums">{formatBytes(g.size)}</span>
                        </label>
                    ))}
                </div>
            )}
            {!readOnly && groups.length > 0 && (
                <div className="flex flex-wrap items-center gap-3">
                    <Button size="sm" variant="ghost" onClick={() => setSelected(selected.size === groups.length ? new Set() : new Set(groups.map(g => g.id)))}>
                        {selected.size === groups.length ? "Select none" : "Select all"}
                    </Button>
                    <Button
                        size="sm"
                        icon={<Trash2 className="size-4" />}
                        disabled={!chosen.length}
                        loading={busy}
                        onClick={() => (onTrash(chosenFiles.map(f => f.path)), setSelected(new Set()))}
                    >
                        {chosen.length ? `Move ${chosenFiles.length} ${chosenFiles.length === 1 ? "file" : "files"} (${formatBytes(chosenSize)}) to the trash` : "Move to the trash"}
                    </Button>
                </div>
            )}
        </section>
    )
}

// An episode there more than once (two releases, two qualities): the copies
// to put away are picked.
function Duplicates({ groups, busy, readOnly, onTrash }: Props & { groups: CleanupFile[][] }) {
    const [selected, setSelected] = useState<Set<string>>(new Set())
    if (!groups.length) return null
    const size = groups.flat().filter(f => selected.has(f.path)).reduce((s, f) => s + f.size, 0)
    // Each episode keeps one copy: its biggest (they come biggest first).
    const others = new Set(groups.flatMap(g => g.slice(1).map(f => f.path)))
    return (
        <section className="flex flex-col gap-3">
            <div>
                <h3 className="flex items-center gap-2 text-[15px] font-semibold">
                    <Copy className="size-4 text-muted" /> Episodes there twice
                </h3>
                <p className="text-[13px] text-muted">The same episode in more than one file. Pick the copies to put away.</p>
            </div>
            <div className="card divide-y divide-line">
                {groups.map(g => (
                    <div key={g[0].path} className="px-4 py-3">
                        <p className="text-sm font-medium">
                            {g[0].title || `Anime ${g[0].mediaId}`} · Episode {g[0].episode}
                        </p>
                        <div className="mt-2 flex flex-col gap-1.5">
                            {g.map(f => (
                                <label key={f.path} className="flex cursor-pointer items-center gap-3 text-sm">
                                    <input
                                        type="checkbox"
                                        checked={selected.has(f.path)}
                                        disabled={readOnly}
                                        onChange={() =>
                                            setSelected(s => {
                                                const n = new Set(s)
                                                n.has(f.path) ? n.delete(f.path) : n.add(f.path)
                                                return n
                                            })
                                        }
                                        className="size-4 shrink-0 accent-[var(--brand)]"
                                    />
                                    <span className="min-w-0 flex-1 truncate text-muted" title={f.path}>
                                        {f.name}
                                    </span>
                                    <span className="shrink-0 text-xs text-subtle tabular-nums">
                                        {f.resolution ? `${f.resolution} · ` : ""}
                                        {formatBytes(f.size)}
                                    </span>
                                </label>
                            ))}
                        </div>
                    </div>
                ))}
            </div>
            {!readOnly && (
                <div className="flex flex-wrap items-center gap-3">
                    <Button size="sm" variant="ghost" onClick={() => setSelected(others)}>
                        Select all but the biggest of each
                    </Button>
                    <Button
                        size="sm"
                        icon={<Trash2 className="size-4" />}
                        disabled={!selected.size}
                        loading={busy}
                        onClick={() => (onTrash([...selected]), setSelected(new Set()))}
                    >
                        {selected.size ? `Move ${selected.size} ${selected.size === 1 ? "file" : "files"} (${formatBytes(size)}) to the trash` : "Move to the trash"}
                    </Button>
                </div>
            )}
        </section>
    )
}

// Aniko's trash: put back, or deleted for good (the space freed).
function TrashList({ items, bytes, readOnly, onChange }: { items: TrashItem[]; bytes: number; readOnly: boolean; onChange: () => void }) {
    const [busy, setBusy] = useState("")
    const restore = async (ids: string[]) => {
        setBusy(ids.length === 1 ? ids[0] : "all")
        try {
            const res = await api.post<{ restored: number; failed: Record<string, string> }>("/api/library/trash/restore", { ids })
            const failed = Object.values(res.failed ?? {})
            if (res.restored) toast.success(`${res.restored === 1 ? "It's" : `${res.restored} files are`} back in the library`)
            if (failed.length) toast.error(failed[0])
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy("")
            onChange()
        }
    }
    const empty = async () => {
        if (!confirm(`Delete the ${items.length} ${items.length === 1 ? "file" : "files"} in Aniko's trash for good? This frees ${formatBytes(bytes)} and can't be undone.`)) return
        setBusy("empty")
        try {
            const res = await api.post<{ freed: number }>("/api/library/trash/empty", {})
            toast.success(`Deleted: ${formatBytes(res.freed)} freed`)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy("")
            onChange()
        }
    }
    return (
        <section className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                    <h3 className="flex items-center gap-2 text-[15px] font-semibold">
                        <Trash2 className="size-4 text-muted" /> Aniko's trash
                    </h3>
                    <p className="text-[13px] text-muted">Files put away here stay on their drive until it's emptied, and 30 days at most.</p>
                </div>
                {!readOnly && items.length > 0 && (
                    <Button size="sm" variant="danger" loading={busy === "empty"} onClick={empty}>
                        Empty the trash ({formatBytes(bytes)})
                    </Button>
                )}
            </div>
            {items.length === 0 ? (
                <p className="card px-4 py-6 text-center text-sm text-muted">The trash is empty.</p>
            ) : (
                <div className="card divide-y divide-line">
                    {items.map(it => (
                        <div key={it.id} className="flex items-center gap-4 px-4 py-2.5 text-sm">
                            <span className="min-w-0 flex-1 truncate" title={it.path}>
                                {it.name}
                            </span>
                            <span className="shrink-0 text-xs text-subtle">
                                {formatBytes(it.size)} · {relativeTime(it.deletedAt)}
                            </span>
                            {!readOnly && (
                                <Button size="xs" variant="ghost" icon={<Undo2 className="size-3.5" />} loading={busy === it.id} onClick={() => restore([it.id])}>
                                    Put back
                                </Button>
                            )}
                        </div>
                    ))}
                </div>
            )}
        </section>
    )
}
