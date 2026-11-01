import { useQuery } from "@tanstack/react-query"
import { ChartColumn, Table } from "lucide-react"
import { useMemo, useState } from "react"
import { EmptyState } from "@/components/ui"
import { api } from "@/lib/api"
import { useCollection } from "@/lib/queries"
import type { CollectionItem } from "@/lib/types"
import { cn } from "@/lib/utils"

// Your anime in numbers: from your list (AniList's, or Aniko's own), and
// what you watched in Aniko month by month.

type Month = { month: string; episodes: number; minutes: number }

// The list's statuses in a fixed order, each with its categorical color (the
// order is what keeps neighbors apart for colorblind readers; a color stays
// with its status when others are missing). Dark steps, validated on the
// card surface.
const STATUSES: { status: string; label: string; color: string }[] = [
    { status: "CURRENT", label: "Watching", color: "#3987e5" },
    { status: "COMPLETED", label: "Completed", color: "#d95926" },
    { status: "PAUSED", label: "Paused", color: "#199e70" },
    { status: "DROPPED", label: "Dropped", color: "#c98500" },
    { status: "PLANNING", label: "Planning", color: "#d55181" },
    { status: "REPEATING", label: "Rewatching", color: "#008300" },
]

export default function StatsPage() {
    const { data: coll, isLoading } = useCollection()
    const { data: months } = useQuery({ queryKey: ["stats", "monthly"], queryFn: () => api.get<Month[]>("/api/stats/monthly?months=12") })

    const stats = useMemo(() => {
        const items: { it: CollectionItem; status: string }[] = (coll?.lists ?? []).flatMap(l => l.items.map(it => ({ it, status: l.status })))
        let anime = 0
        let episodes = 0
        let minutes = 0
        let completed = 0
        const genres = new Map<string, number>()
        const byStatus = new Map<string, number>()
        for (const { it, status } of items) {
            byStatus.set(status, (byStatus.get(status) ?? 0) + 1)
            if (status === "PLANNING") continue
            anime++
            if (status === "COMPLETED") completed++
            const e = it.listEntry
            const watched = (e?.progress ?? 0) + (e?.repeat ?? 0) * (it.media.episodes ?? e?.progress ?? 0)
            episodes += watched
            minutes += watched * (it.media.duration ?? 24)
            for (const g of it.media.genres ?? []) genres.set(g, (genres.get(g) ?? 0) + 1)
        }
        const topGenres = [...genres.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 8)
        return { anime, episodes, minutes, completed, topGenres, byStatus }
    }, [coll])

    if (isLoading) return <div className="m-10 card h-60 shimmer" />
    if (!coll) return <EmptyState title="Couldn't load your list" />

    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8">
                <h1 className="text-[1.75rem] font-semibold tracking-tight">Stats</h1>
                <p className="mt-1 text-muted">Your anime in numbers: your list, and what you watched in Aniko.</p>
            </div>
            <div className="flex flex-col gap-8">
                <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                    <StatTile label="Anime on your list" value={stats.anime.toLocaleString()} sub="not counting planned ones" />
                    <StatTile label="Completed" value={stats.completed.toLocaleString()} />
                    <StatTile label="Episodes watched" value={compact(stats.episodes)} sub="rewatches included" />
                    {/* In days and hours, and (from a day on) all in hours. */}
                    <StatTile label="Time watched" value={duration(stats.minutes)} sub={stats.minutes >= 24 * 60 ? `${Math.round(stats.minutes / 60).toLocaleString()} hours` : undefined} />
                </div>
                <MonthlyChart months={months ?? []} />
                <div className="grid gap-8 lg:grid-cols-2">
                    <GenreBars genres={stats.topGenres} />
                    <StatusBar counts={stats.byStatus} />
                </div>
            </div>
        </div>
    )
}

function StatTile({ label, value, sub }: { label: string; value: string; sub?: string }) {
    return (
        <div className="card px-5 py-4">
            <p className="text-sm text-muted">{label}</p>
            <p className="mt-1 text-3xl font-semibold tracking-tight">{value}</p>
            {sub && <p className="mt-0.5 text-xs text-subtle">{sub}</p>}
        </div>
    )
}

// A chart's frame: its title, and a button between the chart and its table.
function ChartCard({ title, note, table, children }: { title: string; note?: string; table: React.ReactNode; children: React.ReactNode }) {
    const [asTable, setAsTable] = useState(false)
    return (
        <section className="card px-5 pt-4 pb-5">
            <div className="mb-4 flex items-start justify-between gap-4">
                <div>
                    <h2 className="text-[15px] font-semibold">{title}</h2>
                    {note && <p className="mt-0.5 text-xs text-subtle">{note}</p>}
                </div>
                <button
                    onClick={() => setAsTable(t => !t)}
                    className="focus-ring flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-muted hover:bg-white/[0.05] hover:text-fg"
                    aria-pressed={asTable}
                >
                    {asTable ? <ChartColumn className="size-3.5" /> : <Table className="size-3.5" />}
                    {asTable ? "Chart" : "Table"}
                </button>
            </div>
            {asTable ? table : children}
        </section>
    )
}

// Episodes finished in Aniko, month by month: one series, in the accent.
function MonthlyChart({ months }: { months: Month[] }) {
    const [hover, setHover] = useState<number | null>(null)
    const max = Math.max(0, ...months.map(m => m.episodes))
    const ticks = niceTicks(max)
    const top = ticks[ticks.length - 1] || 1
    // No bar to draw (minutes of episodes not finished don't make one).
    const empty = months.every(m => m.episodes === 0)
    const table = (
        <table className="w-full text-sm">
            <thead className="text-left text-xs text-subtle">
                <tr>
                    <th className="pb-2 font-medium">Month</th>
                    <th className="pb-2 text-right font-medium">Episodes</th>
                    <th className="pb-2 text-right font-medium">Time watched</th>
                </tr>
            </thead>
            <tbody className="divide-y divide-line">
                {months.map(m => (
                    <tr key={m.month}>
                        <td className="py-1.5">{monthName(m.month, true)}</td>
                        <td className="py-1.5 text-right tabular-nums">{m.episodes}</td>
                        <td className="py-1.5 text-right text-muted tabular-nums">{hoursMinutes(m.minutes)}</td>
                    </tr>
                ))}
            </tbody>
        </table>
    )
    return (
        <ChartCard title="Episodes watched in Aniko" note="By the month you last watched each one, the last 12 months" table={table}>
            {empty ? (
                <p className="py-10 text-center text-sm text-muted">No episodes finished in Aniko these 12 months yet. Each one you watch to the end here shows up by month.</p>
            ) : (
                // Headroom above the plot: the hover readout over the tallest
                // month stays clear of the title.
                <div className="mt-12 flex gap-3">
                    {/* y axis: clean ticks, tabular so they line up */}
                    <div className="relative h-48 w-8 shrink-0 text-right text-[11px] text-subtle tabular-nums">
                        {ticks.map(t => (
                            <span key={t} className="absolute right-0 -translate-y-1/2" style={{ bottom: `${(t / top) * 100}%` }}>
                                {t}
                            </span>
                        ))}
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="relative h-48">
                            {/* hairline gridlines, recessive */}
                            {ticks.map(t => (
                                <div key={t} className={cn("absolute inset-x-0 h-px", t === 0 ? "bg-white/20" : "bg-white/[0.06]")} style={{ bottom: `${(t / top) * 100}%` }} />
                            ))}
                            <div className="absolute inset-0 flex">
                                {months.map((m, i) => (
                                    <div
                                        key={m.month}
                                        tabIndex={0}
                                        aria-label={`${monthName(m.month, true)}: ${m.episodes} episodes, ${hoursMinutes(m.minutes)}`}
                                        className="group relative flex h-full flex-1 cursor-default items-end justify-center outline-none"
                                        onPointerEnter={() => setHover(i)}
                                        onPointerLeave={() => setHover(h => (h === i ? null : h))}
                                        onFocus={() => setHover(i)}
                                        onBlur={() => setHover(h => (h === i ? null : h))}
                                    >
                                        <div
                                            className={cn("w-full max-w-6 rounded-t-[4px] bg-brand transition-opacity", hover !== null && hover !== i && "opacity-60")}
                                            style={{ height: `${(m.episodes / top) * 100}%` }}
                                        />
                                        {hover === i && (
                                            <div
                                                className={cn(
                                                    "pointer-events-none absolute z-10 rounded-lg bg-surface-3 px-3 py-2 text-xs whitespace-nowrap shadow-xl ring-1 ring-white/10",
                                                    i < 2 ? "left-0" : i > months.length - 3 ? "right-0" : "left-1/2 -translate-x-1/2",
                                                )}
                                                style={{ bottom: `calc(${(m.episodes / top) * 100}% + 8px)` }}
                                            >
                                                <p className="text-sm font-semibold text-fg">{m.episodes} episodes</p>
                                                <p className="text-muted">
                                                    {monthName(m.month, true)} · {hoursMinutes(m.minutes)}
                                                </p>
                                            </div>
                                        )}
                                    </div>
                                ))}
                            </div>
                        </div>
                        <div className="mt-2 flex text-[11px] text-subtle">
                            {months.map((m, i) => (
                                <span key={m.month} className="flex-1 text-center">
                                    {monthName(m.month, i === 0 || m.month.endsWith("-01"))}
                                </span>
                            ))}
                        </div>
                    </div>
                </div>
            )}
        </ChartCard>
    )
}

// The genres of the anime on the list (not planned): one series, bars with
// their count at the tip.
function GenreBars({ genres }: { genres: [string, number][] }) {
    const max = Math.max(1, ...genres.map(g => g[1]))
    const table = (
        <table className="w-full text-sm">
            <tbody className="divide-y divide-line">
                {genres.map(([g, n]) => (
                    <tr key={g}>
                        <td className="py-1.5">{g}</td>
                        <td className="py-1.5 text-right tabular-nums">{n}</td>
                    </tr>
                ))}
            </tbody>
        </table>
    )
    return (
        <ChartCard title="Top genres" note="Anime on your list in each genre (one can have several)" table={table}>
            {genres.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted">No anime on your list yet.</p>
            ) : (
                <div className="flex flex-col gap-2.5">
                    {genres.map(([g, n]) => (
                        <div key={g} className="grid grid-cols-[7rem_1fr] items-center gap-3 text-sm" title={`${g}: ${n} anime`}>
                            <span className="truncate text-muted">{g}</span>
                            <div className="flex items-center gap-2">
                                <div className="h-4 rounded-r-[4px] bg-brand" style={{ width: `${(n / max) * 85}%` }} />
                                <span className="text-xs text-muted tabular-nums">{n}</span>
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </ChartCard>
    )
}

// The list by status: one bar of its parts, with a legend (and the counts in
// it, so identity never rests on color alone).
function StatusBar({ counts }: { counts: Map<string, number> }) {
    const parts = STATUSES.filter(s => (counts.get(s.status) ?? 0) > 0).map(s => ({ ...s, n: counts.get(s.status)! }))
    const total = parts.reduce((s, p) => s + p.n, 0)
    const [hover, setHover] = useState<string | null>(null)
    const table = (
        <table className="w-full text-sm">
            <tbody className="divide-y divide-line">
                {parts.map(p => (
                    <tr key={p.status}>
                        <td className="py-1.5">{p.label}</td>
                        <td className="py-1.5 text-right tabular-nums">{p.n}</td>
                    </tr>
                ))}
            </tbody>
        </table>
    )
    return (
        <ChartCard title="Your list" note={`${total.toLocaleString()} anime by status`} table={table}>
            {total === 0 ? (
                <p className="py-10 text-center text-sm text-muted">No anime on your list yet.</p>
            ) : (
                <div className="flex flex-col gap-5">
                    <div className="flex h-6 gap-[2px]">
                        {parts.map((p, i) => (
                            <div
                                key={p.status}
                                tabIndex={0}
                                aria-label={`${p.label}: ${p.n}`}
                                className={cn("outline-none transition-opacity", i === 0 && "rounded-l-[4px]", i === parts.length - 1 && "rounded-r-[4px]", hover && hover !== p.status && "opacity-50")}
                                style={{ flexGrow: p.n, flexBasis: 0, minWidth: 3, background: p.color }}
                                onPointerEnter={() => setHover(p.status)}
                                onPointerLeave={() => setHover(null)}
                                onFocus={() => setHover(p.status)}
                                onBlur={() => setHover(null)}
                            />
                        ))}
                    </div>
                    <ul className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
                        {parts.map(p => (
                            <li key={p.status} className={cn("flex items-center gap-2 transition-opacity", hover && hover !== p.status && "opacity-50")}>
                                <span className="size-2.5 shrink-0 rounded-[2px]" style={{ background: p.color }} />
                                <span className="text-muted">{p.label}</span>
                                <span className="ml-auto font-medium tabular-nums">{p.n}</span>
                            </li>
                        ))}
                    </ul>
                </div>
            )}
        </ChartCard>
    )
}

// Clean ticks from 0 up to at least max: 0, then steps of 1, 2, 5, 10, 20…
function niceTicks(max: number): number[] {
    if (max <= 0) return [0, 1]
    const raw = max / 3
    const mag = 10 ** Math.floor(Math.log10(raw))
    const step = [1, 2, 2.5, 5, 10].map(f => f * mag).find(s => s >= raw) ?? 10 * mag
    const whole = Math.max(1, Math.round(step))
    const out: number[] = []
    for (let t = 0; ; t += whole) {
        out.push(t)
        if (t >= max) return out
    }
}

function monthName(m: string, withYear: boolean) {
    const [y, mo] = m.split("-").map(Number)
    const name = new Date(y, mo - 1, 1).toLocaleString(undefined, { month: "short" })
    return withYear ? `${name} ${y}` : name
}

function hoursMinutes(min: number) {
    const h = Math.floor(min / 60)
    return h ? `${h} h ${min % 60} min` : `${min} min`
}

function compact(n: number) {
    return n >= 10_000 ? `${(n / 1000).toFixed(1).replace(/\.0$/, "")}K` : n.toLocaleString()
}

function duration(min: number) {
    const days = min / 60 / 24
    return days >= 1 ? `${days.toFixed(days >= 10 ? 0 : 1)} days` : `${Math.round(min / 60)} hours`
}
