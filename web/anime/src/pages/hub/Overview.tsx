import { useQueryClient } from "@tanstack/react-query"
import { ArrowDownUp, ChevronRight, CircleCheck, Cpu, Flame, HardDrive, MemoryStick, Monitor, Play, Smartphone, Thermometer, TriangleAlert, Tv } from "lucide-react"
import { Badge, EmptyState } from "@/components/ui"
import type { HubDevice, HubOverview, HubStream } from "@/lib/types"
import { cn, formatDuration, img } from "@/lib/utils"
import type { HubTab } from "./HubApp"
import { ago, bits, bytes, levelOf, Meter, OnlineDot, Panel, shortPath, since, Sparkline, StatTile, useSamples } from "./parts"

// The hub at a glance: the computer's numbers, what it's streaming and to
// whom, what's being watched, the devices and the disks.

export default function Overview({ o, go }: { o: HubOverview; go: (t: HubTab) => void }) {
    const qc = useQueryClient()
    const updatedAt = qc.getQueryState(["hub", "overview"])?.dataUpdatedAt ?? 0
    const samples = useSamples(o, updatedAt)
    const times = samples.map(s => s.at)
    const s = o.system
    const temp = tempLevel(s.temp, s.throttled)
    const online = o.devices.filter(d => d.online)
    const waiting = o.devices.filter(d => d.online && !d.allowed)
    return (
        <div className="flex flex-col gap-5">
            {waiting.length > 0 && (
                <button onClick={() => go("devices")} className="card flex items-center gap-3 border-brand/40 bg-brand-soft p-4 text-left transition-colors hover:border-brand/70">
                    <NewDevicesIcon />
                    <span className="min-w-0 flex-1 text-sm">
                        <span className="font-medium">
                            {waiting.length === 1 ? `${waiting[0].name} wants the library` : `${waiting.length} devices want the library`}
                        </span>
                        <span className="text-muted"> — share it with {waiting.length === 1 ? "it" : "them"} in Devices.</span>
                    </span>
                    <ChevronRight className="size-4 text-muted" />
                </button>
            )}

            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <StatTile label="CPU" icon={<Cpu />} value={`${Math.round(s.cpu)}%`} sub={`Aniko ${Math.round(s.anikoCpu)}% · load ${s.load.toFixed(2)}`}>
                    <Sparkline values={samples.map(x => x.cpu)} times={times} max={100} format={v => `${Math.round(v)}%`} label="CPU use" />
                </StatTile>
                <StatTile label="Memory" icon={<MemoryStick />} value={bytes(s.memUsed)} sub={`of ${bytes(s.memTotal)} · Aniko ${bytes(s.anikoMem)}`}>
                    <Meter value={s.memUsed} max={s.memTotal} label="Memory in use" className="mt-auto mb-1" />
                </StatTile>
                <StatTile
                    label="Temperature"
                    icon={<Thermometer />}
                    value={s.temp ? `${Math.round(s.temp)}°C` : "—"}
                    sub={
                        <span className={cn("inline-flex items-center gap-1", temp.className)}>
                            {temp.icon}
                            {temp.label}
                        </span>
                    }
                >
                    {s.temp > 0 && <Sparkline values={samples.map(x => x.temp)} times={times} max={85} format={v => `${v.toFixed(1)}°C`} label="Temperature" />}
                </StatTile>
                <StatTile label="Network" icon={<ArrowDownUp />} value={bits(s.netTx)} sub={`sent · ${bits(s.netRx)} received`}>
                    <Sparkline values={samples.map(x => x.tx)} times={times} format={bits} label="Sent on the network" />
                </StatTile>
            </div>

            <div className="grid gap-5 lg:grid-cols-3">
                <div className="flex min-w-0 flex-col gap-5 lg:col-span-2">
                    <Panel
                        title={
                            <span className="flex items-center gap-2">
                                Streaming now
                                {o.streams.length > 0 && <Badge tone="green">{o.streams.length}</Badge>}
                            </span>
                        }
                        action={<span className="text-xs text-muted">{bytes(o.sent)} sent since the hub started</span>}
                    >
                        {o.streams.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">Nothing is streaming. When a device plays an episode from the hub, it shows here, with how fast it goes.</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {o.streams.map(st => (
                                    <StreamRow key={st.id} st={st} />
                                ))}
                            </ul>
                        )}
                    </Panel>

                    <Panel title="Watching now">
                        {o.playing.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">No one is watching. The episodes the devices play (from the hub or their own files) show here.</p>
                        ) : (
                            <ul className="grid gap-3 p-3 sm:grid-cols-2">
                                {o.playing.map(p => {
                                    const acct = o.accounts.find(a => a.user === p.user)
                                    return (
                                        <li key={p.device} className="flex min-w-0 gap-3 rounded-xl bg-white/[0.03] p-2.5">
                                            {p.cover ? (
                                                <img src={img(p.cover)} alt="" className="h-20 w-14 shrink-0 rounded-md object-cover" />
                                            ) : (
                                                <div className="grid h-20 w-14 shrink-0 place-items-center rounded-md bg-surface-3">
                                                    <Play className="size-4 text-muted" />
                                                </div>
                                            )}
                                            <div className="flex min-w-0 flex-1 flex-col">
                                                <p className="truncate text-sm font-medium">{p.title || `Anime ${p.mediaId}`}</p>
                                                <p className="text-[12.5px] text-muted">
                                                    Episode {p.episode} · {formatDuration(p.position)}
                                                    {p.duration > 0 && ` of ${formatDuration(p.duration)}`}
                                                </p>
                                                <p className="mt-auto truncate text-[12px] text-subtle">
                                                    {p.name}
                                                    {acct && ` · ${acct.name}`}
                                                </p>
                                                {p.duration > 0 && <Meter value={p.position} max={p.duration} level="ok" label="Watched" className="mt-1.5 h-1" />}
                                            </div>
                                        </li>
                                    )
                                })}
                            </ul>
                        )}
                    </Panel>

                    <Panel title="Lately" action={<More onClick={() => go("activity")}>Audit log</More>}>
                        {!o.activity?.length ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">Nothing yet.</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {o.activity.slice(0, 6).map(e => (
                                    <li key={e.id} className="flex items-baseline gap-3 px-4 py-2.5 text-sm">
                                        <span className="min-w-0 flex-1 truncate">
                                            {(e.device || e.by) && <span className="font-medium">{e.device || (e.by === "dashboard" ? "You" : "The hub")} </span>}
                                            <span className="text-muted">{e.text}</span>
                                            {e.title && <span> {e.title}</span>}
                                            {e.episode ? <span className="text-muted"> · Episode {e.episode}</span> : null}
                                        </span>
                                        <span className="shrink-0 text-xs text-subtle">{ago(e.at)}</span>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>
                </div>

                <div className="flex min-w-0 flex-col gap-5">
                    <Panel title={`Devices · ${online.length} online`} action={<More onClick={() => go("devices")}>Manage</More>}>
                        {o.devices.length === 0 ? (
                            <EmptyState title="No devices yet">Open Aniko on a computer or TV on this network and turn on Settings › Other devices: it shows up here.</EmptyState>
                        ) : (
                            <ul className="divide-y divide-line">
                                {o.devices.slice(0, 8).map(d => (
                                    <li key={d.id} className="flex items-center gap-3 px-4 py-2.5">
                                        <DeviceIcon d={d} className="size-4 text-muted" />
                                        <span className="min-w-0 flex-1 truncate text-sm">{d.name}</span>
                                        {!d.allowed && d.online && <Badge tone="amber">New</Badge>}
                                        <OnlineDot online={d.online} />
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>

                    <Panel title="Library" action={<More onClick={() => go("library")}>Open</More>}>
                        <dl className="grid grid-cols-3 gap-2 p-4 text-center">
                            <Figure n={o.library.anime} label="anime" />
                            <Figure n={o.library.files} label="episodes" />
                            <Figure n={bytes(o.library.size)} label="in all" />
                        </dl>
                        {(o.library.scanning || o.downloads > 0 || o.library.unmatched > 0) && (
                            <p className="border-t border-line px-4 py-2.5 text-[12.5px] text-muted">
                                {[o.library.scanning && "Scanning…", o.downloads > 0 && `${o.downloads} downloading onto the hub`, o.library.unmatched > 0 && `${o.library.unmatched} not matched`].filter(Boolean).join(" · ")}
                            </p>
                        )}
                    </Panel>

                    <Panel title="Storage">
                        {!s.disks?.length ? (
                            <p className="px-4 py-6 text-center text-sm text-muted">No library folder yet.</p>
                        ) : (
                            <ul className="flex flex-col gap-3.5 p-4">
                                {s.disks.map(d => {
                                    const used = d.total - d.free
                                    return (
                                        <li key={d.path} className="flex flex-col gap-1.5">
                                            <p className="flex items-center gap-2 text-sm">
                                                <HardDrive className="size-4 shrink-0 text-muted" />
                                                <span className="min-w-0 flex-1 truncate" title={d.path}>
                                                    {shortPath(d.path, 2)}
                                                </span>
                                                <span className="shrink-0 text-xs text-muted">{bytes(d.free)} free</span>
                                            </p>
                                            <Meter value={used} max={d.total} level={levelOf(used / Math.max(1, d.total), 0.85, 0.95)} label={`${d.path}: used`} />
                                        </li>
                                    )
                                })}
                            </ul>
                        )}
                    </Panel>

                    {o.recent.length > 0 && (
                        <Panel title="Streamed lately">
                            <ul className="divide-y divide-line">
                                {o.recent.slice(0, 5).map(st => (
                                    <li key={st.id} className="px-4 py-2.5">
                                        <p className="truncate text-sm" title={st.file}>
                                            {st.file}
                                        </p>
                                        <p className="text-xs text-muted">
                                            {st.device} · {bytes(st.bytes)} · {ago(st.ended ?? st.started)}
                                        </p>
                                    </li>
                                ))}
                            </ul>
                        </Panel>
                    )}
                </div>
            </div>
        </div>
    )
}

const KIND: Record<HubStream["kind"], { label: string; tone: "green" | "blue" | "amber" }> = {
    file: { label: "Direct", tone: "green" },
    remux: { label: "Repackaged", tone: "blue" },
    transcode: { label: "Converted", tone: "amber" },
    hls: { label: "Converted", tone: "amber" },
}

function StreamRow({ st }: { st: HubStream }) {
    const k = KIND[st.kind] ?? KIND.file
    return (
        <li className="flex items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium" title={st.file}>
                    {st.file || "A video"}
                </p>
                <p className="mt-0.5 text-[12.5px] text-muted">
                    to {st.device || "a device"} · for {since(st.started)} · {bytes(st.bytes)}
                </p>
            </div>
            <div className="flex shrink-0 flex-col items-end gap-1">
                <span className="text-sm font-medium tabular-nums">{bits(st.rate)}</span>
                <Badge tone={k.tone}>{k.label}</Badge>
            </div>
        </li>
    )
}

export function DeviceIcon({ d, className }: { d: HubDevice; className?: string }) {
    const p = d.platform ?? ""
    if (p === "android") return <Tv className={className} />
    if (p === "ios") return <Smartphone className={className} />
    return <Monitor className={className} />
}

// How warm the computer is (a Raspberry Pi slows itself down from 80°C, or
// when its power supply is too weak: throttled).
function tempLevel(t: number, throttled: boolean) {
    if (throttled) return { label: "Slowed down (too hot, or too little power)", className: "text-rose-300", icon: <Flame className="size-3.5" /> }
    if (!t) return { label: "Not known on this computer", className: "text-muted", icon: null }
    if (t >= 75) return { label: "Hot", className: "text-rose-300", icon: <Flame className="size-3.5" /> }
    if (t >= 65) return { label: "Warm", className: "text-amber-300", icon: <TriangleAlert className="size-3.5" /> }
    return { label: "Cool", className: "text-emerald-300", icon: <CircleCheck className="size-3.5" /> }
}

function Figure({ n, label }: { n: number | string; label: string }) {
    return (
        <div>
            <dd className="text-xl font-semibold">{typeof n === "number" ? n.toLocaleString() : n}</dd>
            <dt className="text-xs text-muted">{label}</dt>
        </div>
    )
}

function More({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
    return (
        <button onClick={onClick} className="focus-ring flex items-center gap-0.5 rounded text-xs font-medium text-muted hover:text-fg">
            {children}
            <ChevronRight className="size-3.5" />
        </button>
    )
}

function NewDevicesIcon() {
    return (
        <span className="grid size-9 shrink-0 place-items-center rounded-full bg-brand/20 text-brand-strong">
            <Monitor className="size-4" />
        </span>
    )
}
