import { useQuery } from "@tanstack/react-query"
import { useEffect, useRef, useState } from "react"
import { api } from "@/lib/api"
import type { HubOverview } from "@/lib/types"
import { cn } from "@/lib/utils"

// The pieces of an Aniko Hub's dashboard (pages/hub): what it's asked every
// 2 seconds, and how its numbers are shown.

export function useHubOverview() {
    return useQuery({
        queryKey: ["hub", "overview"],
        queryFn: () => api.get<HubOverview>("/api/hub/overview"),
        // Not while the tab is in the background (react-query's default).
        refetchInterval: 2000,
        staleTime: 1000,
    })
}

// The last few minutes of the hub's numbers, for the sparklines: kept
// while the dashboard is open (another tab and back keeps them).
export type Sample = { at: number; cpu: number; anikoCpu: number; temp: number; rx: number; tx: number; mem: number }
const KEEP = 90 // 3 minutes, every 2 seconds
const samples: Sample[] = []
let lastAt = 0

export function useSamples(o: HubOverview | undefined, updatedAt: number): Sample[] {
    const [, redraw] = useState(0)
    useEffect(() => {
        if (!o || updatedAt === lastAt) return
        lastAt = updatedAt
        const s = o.system
        samples.push({ at: updatedAt, cpu: s.cpu, anikoCpu: s.anikoCpu, temp: s.temp, rx: s.netRx, tx: s.netTx, mem: s.memUsed })
        if (samples.length > KEEP) samples.splice(0, samples.length - KEEP)
        redraw(n => n + 1)
    }, [o, updatedAt])
    return samples
}

// ------------------------------------------------------------------ numbers

export function bytes(b: number) {
    if (!b || b < 0) return "0 B"
    const u = ["B", "KB", "MB", "GB", "TB"]
    const i = Math.min(Math.floor(Math.log(b) / Math.log(1000)), u.length - 1)
    const v = b / Math.pow(1000, i)
    return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`
}

// A rate in bits a second, as networks are told (a stream's 8 Mb/s).
export function bits(bytesPerSec: number) {
    const b = Math.max(0, bytesPerSec) * 8
    if (b < 1000) return `${Math.round(b)} b/s`
    if (b < 1e6) return `${Math.round(b / 1e3)} kb/s`
    if (b < 1e9) return `${(b / 1e6).toFixed(b < 1e7 ? 1 : 0)} Mb/s`
    return `${(b / 1e9).toFixed(1)} Gb/s`
}

export function since(ms: number, now = Date.now()) {
    const s = Math.max(0, Math.round((now - ms) / 1000))
    return span(s)
}

// A length of time, short: 45s, 12m, 3h 20m, 4d 2h.
export function span(s: number) {
    if (s < 60) return `${s}s`
    if (s < 3600) return `${Math.floor(s / 60)}m`
    if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
    return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`
}

export function ago(ms: number) {
    const s = (Date.now() - ms) / 1000
    if (s < 45) return "just now"
    return `${span(Math.round(s))} ago`
}

// ------------------------------------------------------------------ marks

// Severity of a share of something (memory, a disk): normal, then warning,
// then danger. The fill says it; the track is a lighter step of its color.
export type Level = "ok" | "warn" | "danger"
const LEVEL_COLOR: Record<Level, string> = { ok: "var(--brand)", warn: "#f5b942", danger: "#f0616d" }

export function levelOf(share: number, warn = 0.75, danger = 0.9): Level {
    return share >= danger ? "danger" : share >= warn ? "warn" : "ok"
}

export function Meter({ value, max, level, label, className }: { value: number; max: number; level?: Level; label: string; className?: string }) {
    const share = max > 0 ? Math.min(1, Math.max(0, value / max)) : 0
    const color = LEVEL_COLOR[level ?? levelOf(share)]
    return (
        <div
            role="meter"
            aria-label={label}
            aria-valuemin={0}
            aria-valuemax={max}
            aria-valuenow={value}
            className={cn("h-1.5 overflow-hidden rounded-full", className)}
            style={{ background: `color-mix(in oklab, ${color} 20%, transparent)` }}
        >
            <div className="h-full rounded-full transition-[width] duration-700" style={{ width: `${share * 100}%`, background: color }} />
        </div>
    )
}

// A small line of the last few minutes, in the accent, with what a point
// was on hover (and its time). max: the top of its scale (100 for a
// percentage), else the highest value seen.
export function Sparkline({ values, times, max, format, label, className }: { values: number[]; times: number[]; max?: number; format: (v: number) => string; label: string; className?: string }) {
    const ref = useRef<SVGSVGElement>(null)
    const [hover, setHover] = useState<number | null>(null)
    const W = 120
    const H = 32
    const n = values.length
    if (n < 2) return <div className={cn("h-8", className)} aria-hidden />
    const top = Math.max(max ?? 0, ...values, 1e-9)
    // The whole width: the minutes since the dashboard opened (3 at most).
    const x = (i: number) => (i / (n - 1)) * W
    const y = (v: number) => H - 2 - (Math.min(v, top) / top) * (H - 4)
    const line = values.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(2)} ${y(v).toFixed(2)}`).join("")
    const area = `${line}L${x(n - 1).toFixed(2)} ${H}L${x(0).toFixed(2)} ${H}Z`
    const move = (e: React.PointerEvent) => {
        const r = ref.current?.getBoundingClientRect()
        if (!r) return
        const i = Math.round(((e.clientX - r.left) / r.width) * (n - 1))
        setHover(i >= 0 && i < n ? i : null)
    }
    const h = hover !== null && hover < n ? hover : null
    return (
        <div className={cn("relative", className)}>
            <svg
                ref={ref}
                viewBox={`0 0 ${W} ${H}`}
                preserveAspectRatio="none"
                className="block h-8 w-full touch-none overflow-visible"
                role="img"
                aria-label={`${label}, the last ${Math.round(((times[n - 1] - times[0]) / 60000) * 10) / 10} minutes`}
                onPointerMove={move}
                onPointerDown={move}
                onPointerLeave={() => setHover(null)}
            >
                <path d={area} fill="color-mix(in oklab, var(--brand) 14%, transparent)" />
                <path d={line} fill="none" stroke="var(--brand)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
                {h !== null && <line x1={x(h)} x2={x(h)} y1={0} y2={H} stroke="var(--muted)" strokeWidth={1} vectorEffect="non-scaling-stroke" strokeDasharray="2 2" />}
            </svg>
            {h !== null && (
                <span
                    className="pointer-events-none absolute -top-7 z-10 -translate-x-1/2 rounded-md border border-line-strong bg-surface-3 px-1.5 py-0.5 text-[11px] whitespace-nowrap text-fg shadow-lg"
                    style={{ left: `${Math.min(88, Math.max(12, (x(h) / W) * 100))}%` }}
                >
                    {format(values[h])} <span className="text-muted">· {ago(times[h])}</span>
                </span>
            )}
            {h !== null && (
                <span
                    className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-surface-1 bg-brand"
                    style={{ left: `${(x(h) / W) * 100}%`, top: `${(y(values[h]) / H) * 100}%` }}
                />
            )}
        </div>
    )
}

// A headline number: its name, the value, a line under it.
export function StatTile({ label, icon, value, sub, children, className }: { label: string; icon: React.ReactNode; value: React.ReactNode; sub?: React.ReactNode; children?: React.ReactNode; className?: string }) {
    return (
        <div className={cn("card flex min-w-0 flex-col gap-2 p-4", className)}>
            <p className="flex items-center gap-2 text-[13px] text-muted [&>svg]:size-4">
                {icon}
                {label}
            </p>
            <p className="text-2xl font-semibold tracking-tight">{value}</p>
            {sub && <p className="-mt-1 text-[12.5px] text-muted">{sub}</p>}
            {children}
        </div>
    )
}

// A titled card of the dashboard.
export function Panel({ title, action, children, className }: { title: React.ReactNode; action?: React.ReactNode; children: React.ReactNode; className?: string }) {
    return (
        <section className={cn("card min-w-0", className)}>
            <header className="flex min-h-12 flex-wrap items-center justify-between gap-x-3 gap-y-1 border-b border-line px-4 py-2.5">
                <h2 className="text-[15px] font-semibold">{title}</h2>
                {action}
            </header>
            {children}
        </section>
    )
}

// A dot: online (green) or not (gray), with its word for screen readers.
export function OnlineDot({ online, className }: { online: boolean; className?: string }) {
    return (
        <span
            className={cn("inline-block size-2 shrink-0 rounded-full", online ? "bg-emerald-400 shadow-[0_0_0_3px_rgb(52_211_153/0.15)]" : "bg-white/20", className)}
            role="img"
            aria-label={online ? "online" : "offline"}
        />
    )
}

// A long path, by its end (where its name is): …/Anime/One Piece.
export function shortPath(p: string, keep = 3) {
    const parts = p.split(/[\\/]/).filter(Boolean)
    if (p.length <= 48 || parts.length <= keep) return p
    return "…/" + parts.slice(-keep).join("/")
}
