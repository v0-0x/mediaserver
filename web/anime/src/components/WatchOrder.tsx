import { useQueryClient } from "@tanstack/react-query"
import { ArrowDown, ArrowUp, CalendarClock, Check, Eye, EyeOff, ListOrdered, Pencil, RefreshCw, TriangleAlert, Undo2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { Badge, Button, IconButton, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { useCollection, useWatchOrder } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { CollectionItem, WatchOrder as Order, WatchOrderEntry } from "@/lib/types"
import { cn, cover, formatLabel, title } from "@/lib/utils"

// A franchise's watch order (experimental): the story's, from AniList's
// prequel and sequel links, and the release order. The story order can be
// changed; the change is kept on this Aniko (server/internal/watchorder).

type View = "story" | "release"

export function WatchOrder({ mediaId }: { mediaId: number }) {
    const { data, isLoading, error, refetch, isFetching } = useWatchOrder(mediaId)
    const [view, setView] = useState<View>("story")
    const [editing, setEditing] = useState(false)
    const [extras, setExtras] = useState(false)
    const qc = useQueryClient()
    const status = useStatusOf()

    if (isLoading) return <div className="h-40 rounded-xl shimmer" />
    if (error || !data) {
        return (
            <div className="flex items-center gap-3 text-sm text-muted">
                <TriangleAlert className="size-4 text-amber-300" />
                Couldn't find the watch order: {(error as Error)?.message ?? "no answer"}
                <Button size="sm" variant="ghost" onClick={() => refetch()} loading={isFetching}>
                    Try again
                </Button>
            </div>
        )
    }
    // Nothing to put in order.
    if (data.story.length < 2 && data.extras.length === 0) return null

    const lookAgain = async () => {
        try {
            const fresh = await api.get<Order>(`/api/anime/${mediaId}/watch-order?refresh=1`)
            qc.setQueryData(["watch-order", mediaId], fresh)
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    return (
        <section className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2.5">
                    <ListOrdered className="size-5 text-brand-strong" />
                    <h3 className="text-lg font-semibold tracking-tight">Watch order</h3>
                    <Badge tone="amber">Experimental</Badge>
                </div>
                <div className="flex items-center gap-2">
                    {!editing && (
                        <Tabs
                            value={view}
                            onChange={setView}
                            tabs={[
                                { value: "story", label: "Story" },
                                { value: "release", label: "Release" },
                            ]}
                        />
                    )}
                    {view === "story" && !editing && (
                        <Button size="sm" variant="ghost" icon={<Pencil className="size-4" />} onClick={() => setEditing(true)}>
                            Edit
                        </Button>
                    )}
                    {!editing && (
                        <IconButton size="sm" label="Ask AniList again" onClick={lookAgain}>
                            <RefreshCw className="size-4" />
                        </IconButton>
                    )}
                </div>
            </div>
            {editing ? <Editor order={data} current={mediaId} status={status} onDone={() => setEditing(false)} /> : <Entries order={data} view={view} current={mediaId} status={status} />}
            {data.partial && <p className="text-xs text-subtle">A big franchise: only part of it is shown.</p>}
            {data.extras.length > 0 && !editing && (
                <div>
                    <button onClick={() => setExtras(v => !v)} className="focus-ring rounded text-sm text-muted hover:text-fg">
                        {extras ? "Hide" : "Show"} spin-offs and other versions ({data.extras.length})
                    </button>
                    {extras && (
                        <ul className="mt-3 flex flex-col divide-y divide-line rounded-xl border border-line">
                            {data.extras.map(e => (
                                <Row key={e.media.id} entry={e} status={status(e.media.id)} note={e.relation === "ALTERNATIVE" ? "Another version" : "Spin-off"} />
                            ))}
                        </ul>
                    )}
                </div>
            )}
        </section>
    )
}

type StatusOf = (id: number) => string

function Entries({ order, view, current, status }: { order: Order; view: View; current: number; status: StatusOf }) {
    const all = view === "story" ? order.story : order.release
    const shown = all.filter(e => !e.hidden)
    const hidden = all.length - shown.length
    const conflicts = view === "story" && !order.edited && shown.some(e => e.conflict)
    return (
        <>
            <p className="text-sm text-muted">
                {view === "release" ? "The order it came out in." : order.edited ? "Your order, kept on this device." : "The order the story happens in."}
                {hidden > 0 && ` ${hidden} hidden.`}
            </p>
            {conflicts && (
                <p className="flex items-center gap-2 text-sm text-amber-300">
                    <TriangleAlert className="size-4 shrink-0" />
                    Part of this order is a guess. Edit it if it's wrong.
                </p>
            )}
            <ol className="flex flex-col divide-y divide-line rounded-xl border border-line">
                {shown.map((e, i) => (
                    <Row key={e.media.id} entry={e} n={i + 1} current={e.media.id === current} story={view === "story" && !order.edited} status={status(e.media.id)} />
                ))}
            </ol>
        </>
    )
}

// An anime in the order: what it is, and where you are with it.
function Row({
    entry,
    n,
    current,
    story,
    note,
    status,
    actions,
}: {
    entry: WatchOrderEntry
    n?: number
    current?: boolean
    story?: boolean
    note?: string
    status: string
    actions?: React.ReactNode
}) {
    const m = entry.media
    const facts = [formatLabel(m.format), m.startDate?.year ?? m.seasonYear ?? "TBA", m.episodes && m.format !== "MOVIE" ? `${m.episodes} ${m.episodes === 1 ? "episode" : "episodes"}` : ""]
        .filter(Boolean)
        .join(" · ")
    return (
        <li className={cn("flex items-center gap-3 px-3 py-2.5", current && "bg-brand-soft/60", entry.hidden && "opacity-45")}>
            {n !== undefined && <span className="w-6 shrink-0 text-right text-sm text-subtle tabular-nums">{n}</span>}
            <img src={cover(m, "thumb")} alt="" loading="lazy" className="h-14 w-10 shrink-0 rounded object-cover" />
            <div className="min-w-0 flex-1">
                <Link to={`/entry?id=${m.id}`} className="focus-ring block truncate rounded text-sm font-medium hover:text-brand-strong">
                    {title(m)}
                </Link>
                <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
                    <span>{facts}</span>
                    {current && <Badge tone="brand">You're here</Badge>}
                    {entry.kind === "recap" && <Badge>Recap, skippable</Badge>}
                    {entry.kind === "side" && <Badge>Side story</Badge>}
                    {note && <Badge>{note}</Badge>}
                    {story && entry.conflict && (
                        <span className="flex items-center gap-1 text-amber-300">
                            <TriangleAlert className="size-3.5" /> order unsure
                        </span>
                    )}
                    {story && entry.byDate && (
                        <span className="flex items-center gap-1 text-subtle">
                            <CalendarClock className="size-3.5" /> by release date
                        </span>
                    )}
                </div>
            </div>
            {status && <span className="shrink-0 text-xs text-muted">{status}</span>}
            {actions}
        </li>
    )
}

// Where you are with each anime: your list, your library.
function useStatusOf(): StatusOf {
    const { data: coll } = useCollection()
    return useMemo(() => {
        const byId = new Map<number, CollectionItem>()
        for (const l of coll?.lists ?? []) for (const it of l.items) byId.set(it.media.id, it)
        for (const it of coll?.localOnly ?? []) byId.set(it.media.id, it)
        return (id: number) => {
            const it = byId.get(id)
            if (!it) return ""
            const e = it.listEntry
            const parts: string[] = []
            if (e?.status === "COMPLETED") parts.push("Watched")
            else if (e?.status === "CURRENT" || e?.status === "REPEATING") parts.push(`Watching · ${e.progress}${it.media.episodes ? `/${it.media.episodes}` : ""}`)
            else if (e?.status === "PLANNING") parts.push("Planning")
            else if (e?.status === "PAUSED") parts.push("Paused")
            else if (e?.status === "DROPPED") parts.push("Dropped")
            if (it.localFiles > 0) parts.push("In your library")
            return parts.join(" · ")
        }
    }, [coll])
}

// Your own story order: move anime up and down, hide what you skip.
function Editor({ order, current, status, onDone }: { order: Order; current: number; status: StatusOf; onDone: () => void }) {
    const qc = useQueryClient()
    const [ids, setIds] = useState(() => order.story.map(e => e.media.id))
    const [hidden, setHidden] = useState(() => new Set(order.story.filter(e => e.hidden).map(e => e.media.id)))
    const [saving, setSaving] = useState(false)
    const byId = useMemo(() => new Map(order.story.map(e => [e.media.id, e])), [order])

    const move = (i: number, by: number) =>
        setIds(cur => {
            const j = i + by
            if (j < 0 || j >= cur.length) return cur
            const next = [...cur]
            ;[next[i], next[j]] = [next[j], next[i]]
            return next
        })
    const toggle = (id: number) =>
        setHidden(cur => {
            const next = new Set(cur)
            if (!next.delete(id)) next.add(id)
            return next
        })
    const done = async (p: Promise<unknown>) => {
        setSaving(true)
        try {
            await p
            await qc.invalidateQueries({ queryKey: ["watch-order"] })
            onDone()
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setSaving(false)
        }
    }

    return (
        <>
            <p className="text-sm text-muted">Move anime up or down and hide what you'd skip. Saved on this device, for every anime of this franchise.</p>
            <ol className="flex flex-col divide-y divide-line rounded-xl border border-line">
                {ids.map((id, i) => {
                    const e = byId.get(id)!
                    return (
                        <Row
                            key={id}
                            entry={{ ...e, hidden: hidden.has(id) }}
                            n={i + 1}
                            current={id === current}
                            status={status(id)}
                            actions={
                                <div className="flex shrink-0 items-center gap-0.5">
                                    <IconButton size="sm" label="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
                                        <ArrowUp className="size-4" />
                                    </IconButton>
                                    <IconButton size="sm" label="Move down" disabled={i === ids.length - 1} onClick={() => move(i, 1)}>
                                        <ArrowDown className="size-4" />
                                    </IconButton>
                                    <IconButton size="sm" label={hidden.has(id) ? "Show it" : "Hide it"} onClick={() => toggle(id)}>
                                        {hidden.has(id) ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
                                    </IconButton>
                                </div>
                            }
                        />
                    )
                })}
            </ol>
            <div className="flex flex-wrap items-center gap-2">
                <Button
                    variant="primary"
                    size="sm"
                    icon={<Check className="size-4" />}
                    loading={saving}
                    onClick={() => done(api.put(`/api/watch-order/${order.key}`, { order: ids, hidden: [...hidden] }))}
                >
                    Save
                </Button>
                <Button size="sm" variant="ghost" onClick={onDone} disabled={saving}>
                    Cancel
                </Button>
                {order.edited && (
                    <Button size="sm" variant="ghost" icon={<Undo2 className="size-4" />} disabled={saving} onClick={() => done(api.del(`/api/watch-order/${order.key}`))}>
                        Back to Aniko's order
                    </Button>
                )}
            </div>
        </>
    )
}
