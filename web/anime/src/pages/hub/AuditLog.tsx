import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import {
    Activity,
    CircleAlert,
    CircleArrowUp,
    CircleCheck,
    Download,
    FolderCog,
    FolderPlus,
    Import,
    KeyRound,
    LogIn,
    LogOut,
    Palette,
    Pencil,
    Play,
    Power,
    PowerOff,
    RefreshCw,
    RotateCcw,
    ScrollText,
    Search,
    Settings,
    ShieldAlert,
    Terminal,
    Trash2,
    Wifi,
} from "lucide-react"
import { useEffect, useState } from "react"
import { Badge, Button, EmptyState, IconButton, Input, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import type { HubEvent, HubOverview } from "@/lib/types"
import { cn, formatBytes } from "@/lib/utils"
import { Panel } from "./parts"

// The hub's audit log: everything that happened on it (kept 90 days): what
// the devices did through it, every change made from this dashboard and
// who made it, sign-ins and wrong passwords, downloads, files deleted
// (Download again gets them back), the hub starting and stopping. And the
// server's own log, every line.

const ICONS: Record<string, React.ReactNode> = {
    playing: <Play />,
    finished: <CircleCheck />,
    account: <KeyRound />,
    prefs: <Palette />,
    import: <Import />,
    download: <Download />,
    downloaded: <Download />,
    "download-failed": <CircleAlert />,
    delete: <Trash2 />,
    again: <RotateCcw />,
    scan: <RefreshCw />,
    library: <FolderCog />,
    folders: <FolderPlus />,
    device: <Wifi />,
    signin: <LogIn />,
    "signin-failed": <ShieldAlert />,
    signout: <LogOut />,
    password: <KeyRound />,
    settings: <Settings />,
    change: <Pencil />,
    update: <CircleArrowUp />,
    start: <Power />,
    stop: <PowerOff />,
}

type Group = "" | "library" | "downloads" | "watching" | "devices" | "dashboard" | "hub"

export default function AuditLog({ o }: { o: HubOverview }) {
    const [view, setView] = useState<"events" | "server">("events")
    return (
        <div className="flex flex-col gap-5">
            <Tabs
                variant="underline"
                value={view}
                onChange={setView}
                tabs={[
                    { value: "events", label: "What happened", icon: <ScrollText /> },
                    { value: "server", label: "Server log", icon: <Terminal /> },
                ]}
            />
            {view === "events" ? <Events o={o} /> : <ServerLog />}
        </div>
    )
}

// What's typed, a moment after the typing stops.
function useSettled(value: string, ms = 300) {
    const [v, setV] = useState(value)
    useEffect(() => {
        const t = setTimeout(() => setV(value), ms)
        return () => clearTimeout(t)
    }, [value, ms])
    return v
}

function Events({ o }: { o: HubOverview }) {
    const [group, setGroup] = useState<Group>("")
    const [typed, setTyped] = useState("")
    const q = useSettled(typed.trim())
    const log = useInfiniteQuery({
        queryKey: ["hub", "audit", group, q],
        queryFn: ({ pageParam }) => {
            const p = new URLSearchParams()
            if (group) p.set("group", group)
            if (q) p.set("q", q)
            if (pageParam) p.set("before", String(pageParam))
            return api.get<{ events: HubEvent[]; more: boolean }>(`/api/hub/audit?${p}`)
        },
        initialPageParam: 0,
        getNextPageParam: last => (last.more && last.events.length ? last.events[last.events.length - 1].id : undefined),
        refetchInterval: 5000,
    })
    const events = log.data?.pages.flatMap(p => p.events) ?? []
    // By day, the latest first.
    const days: { day: string; items: HubEvent[] }[] = []
    for (const e of events) {
        const day = dayName(e.at)
        if (days[days.length - 1]?.day !== day) days.push({ day, items: [] })
        days[days.length - 1].items.push(e)
    }
    const account = (user?: number) => o.accounts.find(a => a.user === user)?.name
    return (
        <div className="flex flex-col gap-5">
            <div className="flex flex-wrap items-center gap-3">
                <div className="no-scrollbar max-w-full overflow-x-auto">
                    <Tabs
                        value={group}
                        onChange={setGroup}
                        tabs={[
                            { value: "", label: "Everything" },
                            { value: "library", label: "Library" },
                            { value: "downloads", label: "Downloads" },
                            { value: "watching", label: "Watching" },
                            { value: "devices", label: "Devices" },
                            { value: "dashboard", label: "Dashboard" },
                            { value: "hub", label: "The hub" },
                        ]}
                    />
                </div>
                <div className="min-w-0 flex-1 basis-56">
                    <Input
                        icon={<Search className="size-4" />}
                        placeholder="Find: an anime, a device, a file, an address…"
                        aria-label="Find in the audit log"
                        value={typed}
                        onChange={e => setTyped(e.target.value)}
                    />
                </div>
            </div>
            {log.isLoading ? (
                <div className="h-48 rounded-xl shimmer" />
            ) : events.length === 0 ? (
                <EmptyState icon={<Activity />} title={q || group ? "Nothing found" : "Nothing yet"}>
                    {q || group
                        ? "Nothing in the audit log matches. It keeps the last 90 days."
                        : "Everything that happens on the hub shows here: what the devices watch and send it, every change made here and who made it, sign-ins, downloads and deleted files."}
                </EmptyState>
            ) : (
                days.map(d => (
                    <Panel key={d.day} title={d.day}>
                        <ul className="divide-y divide-line">
                            {d.items.map(e => (
                                <Row key={e.id} e={e} account={account(e.user)} />
                            ))}
                        </ul>
                    </Panel>
                ))
            )}
            {log.hasNextPage && (
                <div className="text-center">
                    <Button variant="ghost" size="sm" loading={log.isFetchingNextPage} onClick={() => log.fetchNextPage()}>
                        Show older
                    </Button>
                </div>
            )}
        </div>
    )
}

// Who did it: a device, you (signed in here), the hub, or someone at an
// address who isn't signed in.
function who(e: HubEvent) {
    if (e.device) return e.device
    if (e.by === "dashboard") return "You"
    if (e.by === "hub") return "The hub"
    if (e.ip) return `Someone at ${e.ip}`
    return ""
}

function Row({ e, account }: { e: HubEvent; account?: string }) {
    const subject = who(e)
    return (
        <li className="flex items-start gap-3 px-4 py-3">
            <span
                className={cn(
                    "mt-0.5 grid size-7 shrink-0 place-items-center rounded-full [&>svg]:size-3.5",
                    e.failed ? "bg-rose-500/[0.12] text-rose-300" : e.kind === "delete" ? "bg-amber-400/[0.1] text-amber-300" : "bg-white/[0.05] text-muted",
                )}
            >
                {ICONS[e.kind] ?? <Activity />}
            </span>
            <div className="min-w-0 flex-1 text-sm">
                <p className="break-words">
                    {subject && <span className="font-medium">{subject} </span>}
                    <span className={e.failed ? "text-rose-300" : "text-muted"}>{e.text}</span>
                    {e.title && <span> {e.title}</span>}
                    {e.episode ? <span className="text-muted"> · Episode {e.episode}</span> : null}
                    {e.kind === "delete" && e.size ? <span className="text-muted"> · {formatBytes(e.size)}</span> : null}
                </p>
                {e.detail && <p className={cn("mt-0.5 text-xs break-words", e.failed ? "text-rose-300/90" : "text-subtle")}>{e.detail}</p>}
                <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-subtle">
                    {e.by === "dashboard" && e.ip && <span>from {e.ip}</span>}
                    {account && <span>{account}'s account</span>}
                    {e.failed && <Badge tone="red">{e.kind === "signin-failed" ? "Kept out" : "Didn't work"}</Badge>}
                </p>
                {e.kind === "delete" && <Deleted e={e} />}
                {e.kind !== "delete" && (e.files?.length ?? 0) > 0 && <Files files={e.files!} />}
            </div>
            <time className="shrink-0 text-xs text-subtle tabular-nums" dateTime={new Date(e.at).toISOString()} title={new Date(e.at).toLocaleString()}>
                {new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
            </time>
        </li>
    )
}

function Files({ files }: { files: NonNullable<HubEvent["files"]> }) {
    const [all, setAll] = useState(false)
    const shown = all ? files : files.slice(0, 3)
    return (
        <ul className="mt-2 flex flex-col gap-0.5 text-xs text-subtle">
            {shown.map(f => (
                <li key={f.path} className="truncate" title={f.path}>
                    {f.name}
                    {f.size ? ` · ${formatBytes(f.size)}` : ""}
                </li>
            ))}
            {files.length > 3 && (
                <li>
                    <button className="focus-ring rounded text-muted hover:text-fg" onClick={() => setAll(v => !v)}>
                        {all ? "Show fewer" : `Show all ${files.length} files`}
                    </button>
                </li>
            )}
        </ul>
    )
}

// Files deleted: which, and Download again (subbed or dubbed) for their
// episodes, onto the hub where the anime's others are.
function Deleted({ e }: { e: HubEvent }) {
    const qc = useQueryClient()
    const again = e.again
    const [dub, setDub] = useState(again?.dub ? "dub" : "sub")
    const [busy, setBusy] = useState(false)
    const run = async () => {
        setBusy(true)
        try {
            const res = await api.post<{ queued: number }>(`/api/hub/audit/${e.id}/again`, { dub: dub === "dub" })
            toast.success(`Downloading ${res.queued} ${res.queued === 1 ? "episode" : "episodes"} again: see Library › Downloading onto the hub.`)
            await qc.invalidateQueries({ queryKey: ["hub"] })
        } catch (err) {
            toast.error((err as Error).message)
        } finally {
            setBusy(false)
        }
    }
    const skipped = (e.files?.length ?? 0) - (again?.episodes.length ?? 0)
    return (
        <div className="mt-2.5 rounded-lg border border-line bg-white/[0.02] p-3">
            <Files files={e.files ?? []} />
            {again ? (
                <div className="mt-3 flex flex-wrap items-center gap-2.5">
                    <Button size="sm" icon={<RotateCcw className="size-4" />} loading={busy} onClick={run}>
                        Download again
                    </Button>
                    <Tabs
                        value={dub}
                        onChange={setDub}
                        tabs={[
                            { value: "sub", label: "Sub" },
                            { value: "dub", label: "Dub" },
                        ]}
                    />
                    <span className="text-xs text-subtle">
                        {again.episodes.length === 1 ? "Episode" : "Episodes"} {episodeList(again.episodes)}
                        {again.queued ? ` · downloading again since ${new Date(again.queued).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}` : ""}
                    </span>
                    {skipped > 0 && <span className="basis-full text-xs text-subtle">Openings, endings and files without an episode number can't be downloaded again.</span>}
                </div>
            ) : (
                <p className="mt-2 text-xs text-subtle">These can't be downloaded again: they aren't numbered episodes of an anime (openings, endings, unmatched files).</p>
            )}
        </div>
    )
}

// 1, 2, 3, 7 → "1–3, 7"
function episodeList(eps: number[]) {
    const out: string[] = []
    for (let i = 0; i < eps.length; i++) {
        let j = i
        while (j + 1 < eps.length && eps[j + 1] === eps[j] + 1) j++
        out.push(j > i ? `${eps[i]}–${eps[j]}` : String(eps[i]))
        i = j
    }
    return out.join(", ")
}

// The server's log, every line, the newest first.
function ServerLog() {
    const [typed, setTyped] = useState("")
    const q = useSettled(typed.trim())
    const [only, setOnly] = useState<"all" | "problems">("all")
    const log = useInfiniteQuery({
        queryKey: ["hub", "log", q, only],
        queryFn: ({ pageParam }) => {
            const p = new URLSearchParams({ skip: String(pageParam) })
            if (q) p.set("q", q)
            if (only === "problems") p.set("errors", "1")
            return api.get<{ lines: string[]; more: boolean; onDisk: boolean }>(`/api/hub/log?${p}`)
        },
        initialPageParam: 0,
        getNextPageParam: (last, pages) => (last.more ? pages.reduce((n, p) => n + p.lines.length, 0) : undefined),
        // Not asked again by itself: reading it all is work for a Pi (Look again).
        staleTime: Infinity,
    })
    const lines = log.data?.pages.flatMap(p => p.lines) ?? []
    const onDisk = log.data?.pages[0]?.onDisk ?? true
    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-3">
                <Tabs
                    value={only}
                    onChange={setOnly}
                    tabs={[
                        { value: "all", label: "Every line" },
                        { value: "problems", label: "Only problems" },
                    ]}
                />
                <div className="min-w-0 flex-1 basis-56">
                    <Input icon={<Search className="size-4" />} placeholder="Find in the log…" aria-label="Find in the server log" value={typed} onChange={e => setTyped(e.target.value)} />
                </div>
                <IconButton label="Look again" onClick={() => log.refetch()}>
                    <RefreshCw className={cn("size-4", log.isFetching && "animate-spin")} />
                </IconButton>
                <a href="/api/hub/log/download" download className="focus-ring inline-flex h-9 items-center gap-2 rounded-lg px-3 text-sm text-muted hover:bg-white/[0.06] hover:text-fg">
                    <Download className="size-4" /> Save the log
                </a>
            </div>
            <p className="text-xs text-subtle">
                {onDisk
                    ? "Every line the hub's server writes, kept on its disk (the last 20 MB or so), the newest first."
                    : "The last 1,000 lines the hub's server wrote, the newest first (this hub doesn't keep its log on disk)."}
            </p>
            <Panel title={`${lines.length.toLocaleString()}${log.hasNextPage ? "+" : ""} lines`}>
                {log.isLoading ? (
                    <div className="m-4 h-40 rounded-lg shimmer" />
                ) : lines.length === 0 ? (
                    <p className="px-4 py-8 text-center text-sm text-muted">{q || only === "problems" ? "No line matches." : "Nothing in the log yet."}</p>
                ) : (
                    <ol className="max-h-[70vh] overflow-y-auto px-4 py-3 font-mono text-[12px] leading-relaxed">
                        {lines.map((l, i) => (
                            <li key={i} className={cn("break-all whitespace-pre-wrap", problem(l) ? "text-amber-300" : "text-muted")}>
                                {l}
                            </li>
                        ))}
                    </ol>
                )}
            </Panel>
            {log.hasNextPage && (
                <div className="text-center">
                    <Button variant="ghost" size="sm" loading={log.isFetchingNextPage} onClick={() => log.fetchNextPage()}>
                        Show older lines
                    </Button>
                </div>
            )}
        </div>
    )
}

// A line that says something went wrong (as the server picks them: its
// "Only problems").
function problem(line: string) {
    return /error|fail|couldn't|can't|cannot|panic|refused|denied|forbidden|timeout|timed out|kept out|wrong password/i.test(line)
}

function dayName(ms: number) {
    const d = new Date(ms)
    const today = new Date()
    const yesterday = new Date(Date.now() - 86400_000)
    if (d.toDateString() === today.toDateString()) return "Today"
    if (d.toDateString() === yesterday.toDateString()) return "Yesterday"
    return d.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" })
}
