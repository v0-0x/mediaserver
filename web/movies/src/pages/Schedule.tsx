import { CalendarDays, Check, ChevronLeft, ChevronRight } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Badge, Button, EmptyState, ErrorState, IconButton, Skeleton, Tabs } from "@/components/ui"
import { usePersisted } from "@/lib/hooks"
import { formatKey, splitKey } from "@/lib/ids"
import { useCollection, useSchedule } from "@/lib/queries"
import type { ListEntry, ScheduleItem } from "@/lib/types"
import { backdrop, cn, cover, LIST_STATUS, title } from "@/lib/utils"

const DAY = 86400

// The Monday of the week `offset` weeks from this one, as a local date.
function weekStart(offset: number) {
    const d = new Date()
    d.setHours(0, 0, 0, 0)
    d.setDate(d.getDate() - ((d.getDay() + 6) % 7) + offset * 7)
    return d
}

// TMDB only has air dates: an episode's airingAt is its day at 00:00 UTC.
// Days are compared as those UTC midnights.
const utcDay = (d: Date) => Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 1000

export default function SchedulePage() {
    const [offset, setOffset] = useState(0)
    const [filter, setFilter] = usePersisted<"watching" | "all">("kumobox-schedule-filter", "all")
    const monday = useMemo(() => weekStart(offset), [offset])
    const start = utcDay(monday)
    const end = start + 7 * DAY
    const { data, isLoading, error, refetch, isFetching } = useSchedule(start, end)
    // How far each show on the list is (the schedule's shows don't carry
    // their list entry), to tick the episodes already watched.
    const { data: coll } = useCollection()
    const entries = useMemo(() => {
        const m = new Map<number, ListEntry>()
        for (const l of coll?.lists ?? []) for (const it of l.items) if (it.listEntry) m.set(it.media.id, it.listEntry)
        return m
    }, [coll])

    const days = useMemo(() => {
        const items = (data ?? []).filter(i => i.media && (filter === "all" || !i.listStatus || i.listStatus === "CURRENT" || i.listStatus === "REPEATING"))
        return Array.from({ length: 7 }, (_, k) => {
            const date = new Date(monday)
            date.setDate(monday.getDate() + k)
            const day = start + k * DAY
            return {
                date,
                items: items.filter(i => i.airingAt >= day && i.airingAt < day + DAY).sort((a, b) => title(a.media).localeCompare(title(b.media)) || a.episode - b.episode),
            }
        })
    }, [data, filter, monday, start])

    const today = new Date().toDateString()
    const total = days.reduce((n, d) => n + d.items.length, 0)
    const sunday = new Date(monday)
    sunday.setDate(monday.getDate() + 6)
    const range = `${monday.toLocaleDateString(undefined, { month: "short", day: "numeric" })} – ${sunday.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}`

    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <PluginSlot slot="schedule-screen-top" className="mb-8" />
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Calendar</h1>
                    <p className="mt-1 text-muted">New episodes of the shows on your list, by air date.</p>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                    <Tabs
                        value={filter}
                        onChange={setFilter}
                        tabs={[
                            { value: "all", label: "My list" },
                            { value: "watching", label: "Watching" },
                        ]}
                    />
                    <div className="flex items-center gap-1">
                        <IconButton label="Previous week" variant="subtle" size="sm" onClick={() => setOffset(o => o - 1)}>
                            <ChevronLeft className="size-4" />
                        </IconButton>
                        <Button size="sm" variant={offset === 0 ? "ghost" : "subtle"} disabled={offset === 0} onClick={() => setOffset(0)}>
                            This week
                        </Button>
                        <IconButton label="Next week" variant="subtle" size="sm" onClick={() => setOffset(o => o + 1)}>
                            <ChevronRight className="size-4" />
                        </IconButton>
                    </div>
                </div>
            </div>
            <p className="mb-4 text-sm font-medium text-fg/85">
                {range}
                {isFetching && !isLoading && <span className="ml-2 text-xs font-normal text-subtle">Updating…</span>}
            </p>
            {error && !data && <ErrorState title="Couldn't load the calendar" error={error} retrying={isFetching} onRetry={() => refetch()} />}
            {isLoading ? (
                <div className="grid gap-3 xl:grid-cols-7">
                    {Array.from({ length: 7 }).map((_, i) => (
                        <Skeleton key={i} className="h-48" />
                    ))}
                </div>
            ) : (
                data && (
                    <>
                        {total === 0 && (
                            <EmptyState icon={<CalendarDays className="size-6" />} title="Nothing this week">
                                {filter === "watching" ? "None of the shows you're watching air this week." : "None of the shows on your list air this week. Add shows to your list to follow them here."}
                            </EmptyState>
                        )}
                        {total > 0 && (
                            <div className="grid gap-x-3 gap-y-6 xl:grid-cols-7">
                                {days.map(d => {
                                    const isToday = d.date.toDateString() === today
                                    return (
                                        <section key={d.date.toISOString()} className={cn("flex min-w-0 flex-col gap-2", d.items.length === 0 && "max-xl:hidden")}>
                                            <h2 className={cn("flex items-center gap-2 border-b pb-2 text-sm font-semibold", isToday ? "border-brand text-fg" : "border-line text-muted")}>
                                                {d.date.toLocaleDateString(undefined, { weekday: "short" })}
                                                <span className="font-normal text-subtle">{d.date.toLocaleDateString(undefined, { month: "short", day: "numeric" })}</span>
                                                {isToday && <Badge tone="brand">Today</Badge>}
                                            </h2>
                                            <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2 xl:grid-cols-1">
                                                {d.items.map(it => (
                                                    <CalendarCard key={`${it.media.id}-${it.episode}`} item={it} entry={entries.get(it.media.id)} />
                                                ))}
                                                {d.items.length === 0 && <p className="py-2 text-xs text-subtle">—</p>}
                                            </div>
                                        </section>
                                    )
                                })}
                            </div>
                        )}
                    </>
                )
            )}
            <PluginSlot slot="schedule-screen-bottom" className="mt-8" />
        </div>
    )
}

function CalendarCard({ item, entry }: { item: ScheduleItem; entry?: ListEntry }) {
    const m = item.media
    const e = entry ?? m.mediaListEntry
    const watched = e?.status === "COMPLETED" || (e?.progress ?? 0) >= item.episode
    const image = m.bannerImage ? backdrop(m.bannerImage, 220) : { src: cover(m) }
    return (
        <Link
            to={`/entry?id=${m.id}&tab=episodes&season=${splitKey(item.episode)[0]}`}
            className="group card flex flex-col overflow-hidden p-0 transition hover:border-line-strong"
        >
            <div className="relative aspect-video bg-surface-3">
                {image.src && <img {...image} alt="" loading="lazy" decoding="async" className="size-full object-cover transition-[filter] duration-300 group-hover:brightness-110" />}
                <span className="absolute bottom-1.5 left-1.5 rounded bg-black/75 px-1.5 py-0.5 text-[11px] font-semibold text-white tabular-nums">{formatKey(item.episode)}</span>
                {watched && (
                    <span className="absolute top-1.5 left-1.5 grid size-5 place-items-center rounded-full bg-emerald-500 text-white" aria-label="Watched">
                        <Check className="size-3 stroke-[3]" />
                    </span>
                )}
            </div>
            <div className="min-w-0 p-2.5">
                <p className="truncate text-[13px] font-semibold group-hover:text-white">{title(m)}</p>
                {item.title && <p className="mt-0.5 line-clamp-2 text-xs text-muted">{item.title}</p>}
                {item.listStatus && item.listStatus !== "CURRENT" && <p className="mt-1 text-[11px] text-subtle">{LIST_STATUS[item.listStatus] ?? item.listStatus}</p>}
            </div>
        </Link>
    )
}
