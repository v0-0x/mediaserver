import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ChevronRight, Clapperboard, ExternalLink, Folder, FolderPlus, HardDrive, KeyRound, Monitor, RefreshCw, Search, Trash2, Tv, Upload, Wifi } from "lucide-react"
import { useMemo, useState } from "react"
import { DirPicker } from "@/components/DirPicker"
import { UploadDialog } from "@/components/UploadDialog"
import { Badge, Button, EmptyState, IconButton, Input, Spinner, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import type { HubOverview } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SystemTiles } from "../Overview"
import { ago, bytes, OnlineDot, Panel, shortPath } from "../parts"
import {
    foldersOf,
    M,
    type MediaKind,
    mediaTitle,
    mediaYear,
    mImg,
    type MPeer,
    type MTitle,
    ofKind,
    useMSettings,
    useMSharing,
    useMStatus,
    useMTitles,
    useMUnmatched,
    useSaveMSettings,
} from "./data"

// mediaserver's Movies and TV pages: its movies side (KumoBox's server, for
// the KumoBox apps), on the dashboard. Movies and TV are the same side,
// seen apart: their own titles, the same devices, folders and settings.

export type MediaTab = "overview" | "devices" | "library" | "settings"

const noun = (k: MediaKind) => (k === "movies" ? "movies" : "shows")
const Noun = (k: MediaKind) => (k === "movies" ? "Movies" : "Shows")

// The movies side's own pages (for what the dashboard doesn't do: fixing a
// match, say), on its port of this computer, with the same sign-in.
export function moviesPagesURL(port: number | undefined, path = "") {
    return `${window.location.protocol}//${window.location.hostname}:${port ?? 43311}${path}`
}

function kindCounts(titles: MTitle[], kind: MediaKind) {
    const mine = ofKind(titles, kind)
    return {
        titles: mine.length,
        episodes: kind === "tv" ? mine.reduce((n, t) => n + t.episodes.length, 0) : 0,
        size: mine.reduce((n, t) => n + t.size, 0),
    }
}

// ------------------------------------------------------------------ overview

export function MediaOverview({ kind, o, go }: { kind: MediaKind; o: HubOverview; go: (t: MediaTab) => void }) {
    const { data: titles } = useMTitles()
    const { data: sharing } = useMSharing()
    const { data: status } = useMStatus()
    const all = titles ?? []
    const c = kindCounts(all, kind)
    const peers = sharing?.peers ?? []
    const online = peers.filter(p => p.online)
    const waiting = peers.filter(p => p.online && !p.allowed && !p.declined)
    const recent = useMemo(
        () =>
            ofKind(all, kind)
                .slice()
                .sort((a, b) => b.addedAt - a.addedAt)
                .slice(0, 12),
        [all, kind],
    )
    return (
        <div className="flex flex-col gap-5">
            {status && !status.tmdbConfigured && <TmdbBanner go={go} />}
            {waiting.length > 0 && (
                <button onClick={() => go("devices")} className="card flex items-center gap-3 border-brand/40 bg-brand-soft p-4 text-left transition-colors hover:border-brand/70">
                    <Wifi className="size-5 text-brand-strong" />
                    <span className="min-w-0 flex-1 text-sm">
                        <span className="font-medium">{waiting.length === 1 ? `${waiting[0].name} wants the library` : `${waiting.length} KumoBox apps want the library`}</span>
                        <span className="text-muted"> — share it with {waiting.length === 1 ? "it" : "them"} in Devices.</span>
                    </span>
                    <ChevronRight className="size-4 text-muted" />
                </button>
            )}
            <SystemTiles o={o} />
            <div className="grid gap-5 lg:grid-cols-3">
                <div className="flex min-w-0 flex-col gap-5 lg:col-span-2">
                    <Panel
                        title={`Recently added ${noun(kind)}`}
                        action={
                            <button className="flex items-center gap-1 text-[13px] text-muted hover:text-fg" onClick={() => go("library")}>
                                Library <ChevronRight className="size-4" />
                            </button>
                        }
                    >
                        {recent.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">
                                No {noun(kind)} yet. Add the folder they're in, or upload them, in Library.
                            </p>
                        ) : (
                            <div className="grid grid-cols-3 gap-3 p-4 sm:grid-cols-4 xl:grid-cols-6">
                                {recent.map(t => (
                                    <PosterCard key={t.media.id} t={t} kind={kind} small />
                                ))}
                            </div>
                        )}
                    </Panel>
                </div>
                <div className="flex min-w-0 flex-col gap-5">
                    <Panel title={`In the library`}>
                        <dl className="divide-y divide-line">
                            <Row label={Noun(kind)} value={c.titles.toLocaleString()} />
                            {kind === "tv" && <Row label="Episodes" value={c.episodes.toLocaleString()} />}
                            <Row label="Size" value={bytes(c.size)} />
                        </dl>
                    </Panel>
                    <Panel
                        title={`KumoBox apps · ${online.length} online`}
                        action={
                            <button className="flex items-center gap-1 text-[13px] text-muted hover:text-fg" onClick={() => go("devices")}>
                                Manage <ChevronRight className="size-4" />
                            </button>
                        }
                    >
                        {peers.length === 0 ? (
                            <p className="px-4 py-6 text-center text-sm leading-relaxed text-muted">
                                No devices yet. Open KumoBox on a computer, TV or phone on this network and turn on Settings › Other devices › Library sharing.
                            </p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {peers.slice(0, 6).map(p => (
                                    <li key={p.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                                        <PeerIcon p={p} className="size-4 text-muted" />
                                        <span className="min-w-0 flex-1 truncate">{p.name}</span>
                                        {p.allowed ? <Badge tone="green">Shared</Badge> : <Badge>Not shared</Badge>}
                                        <OnlineDot online={p.online} />
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>
                </div>
            </div>
        </div>
    )
}

function Row({ label, value, tone }: { label: string; value: React.ReactNode; tone?: string }) {
    return (
        <div className="flex items-center justify-between px-4 py-2.5 text-sm">
            <dt className="text-muted">{label}</dt>
            <dd className={cn("font-medium tabular-nums", tone)}>{value}</dd>
        </div>
    )
}

function TmdbBanner({ go }: { go: (t: MediaTab) => void }) {
    return (
        <button onClick={() => go("settings")} className="card flex items-center gap-3 border-amber-400/30 bg-amber-400/[0.06] p-4 text-left transition-colors hover:border-amber-400/60">
            <KeyRound className="size-5 text-amber-300" />
            <span className="min-w-0 flex-1 text-sm">
                <span className="font-medium">Add a TMDB key</span>
                <span className="text-muted"> — movies and shows are matched and get their posters from The Movie Database. A key is free (Settings).</span>
            </span>
            <ChevronRight className="size-4 text-muted" />
        </button>
    )
}

function PeerIcon({ p, className }: { p: MPeer; className?: string }) {
    return p.platform === "android" ? <Tv className={className} /> : <Monitor className={className} />
}

function PosterCard({ t, kind, small }: { t: MTitle; kind: MediaKind; small?: boolean }) {
    const c = t.media.coverImage
    const src = mImg(small ? c?.medium || c?.large : c?.large || c?.medium)
    const year = mediaYear(t.media)
    return (
        <div className="min-w-0" title={t.dir}>
            <div className="aspect-[2/3] overflow-hidden rounded-lg bg-white/[0.04] ring-1 ring-white/[0.06]">
                {src ? (
                    <img src={src} alt="" loading="lazy" className="size-full object-cover" />
                ) : (
                    <span className="grid size-full place-items-center text-subtle">{kind === "tv" ? <Tv className="size-6" /> : <Clapperboard className="size-6" />}</span>
                )}
            </div>
            <p className="mt-1.5 truncate text-[13px] font-medium">{mediaTitle(t.media)}</p>
            <p className="truncate text-[12px] text-muted">
                {[year, kind === "tv" ? `${t.episodes.length} ${t.episodes.length === 1 ? "episode" : "episodes"}` : null, bytes(t.size)].filter(Boolean).join(" · ")}
            </p>
        </div>
    )
}

// ------------------------------------------------------------------- devices

export function MediaDevices() {
    const qc = useQueryClient()
    const { data: sharing, isLoading } = useMSharing()
    const save = useMutation({
        mutationFn: ({ id, change }: { id: string; change: { allowed?: boolean; downloads?: boolean } }) => api.post(`${M}/sharing/peers/${encodeURIComponent(id)}`, change),
        onSuccess: () => qc.invalidateQueries({ queryKey: ["movies", "sharing"] }),
        onError: (e: Error) => toast.error(e.message),
    })
    const forget = useMutation({
        mutationFn: (id: string) => api.del(`${M}/sharing/peers/${encodeURIComponent(id)}`),
        onSuccess: () => qc.invalidateQueries({ queryKey: ["movies", "sharing"] }),
        onError: (e: Error) => toast.error(e.message),
    })
    if (isLoading) return <Loading />
    const peers = sharing?.peers ?? []
    const waiting = peers.filter(p => !p.allowed)
    const shared = peers.filter(p => p.allowed)
    return (
        <div className="flex flex-col gap-6">
            <p className="max-w-3xl text-sm leading-relaxed text-muted">
                The KumoBox apps on this network: the same for Movies and TV. A device shows up here when KumoBox is open on it with{" "}
                <span className="text-fg">Settings › Other devices › Library sharing</span> on. Share the library with it, and it plays the server's movies and shows.
            </p>
            {peers.length === 0 && (
                <EmptyState icon={<Wifi />} title="No devices yet">
                    Open KumoBox on a computer, TV or phone on this network and turn on Settings › Other devices › Library sharing.
                    {sharing?.addresses?.length ? ` If it doesn't show up, add this server by address there: ${sharing.addresses[0]}.` : ""}
                </EmptyState>
            )}
            {[
                ["Not sharing with", waiting],
                ["Sharing with", shared],
            ].map(([title, list]) =>
                (list as MPeer[]).length ? (
                    <section key={title as string} className="flex flex-col gap-3">
                        <h2 className="text-[15px] font-semibold">{title as string}</h2>
                        {(list as MPeer[]).map(p => (
                            <div key={p.id} className={cn("card overflow-hidden", !p.allowed && p.online && "border-brand/40")}>
                                <div className="flex items-center gap-3 px-4 py-3.5">
                                    <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.05]">
                                        <PeerIcon p={p} className="size-5 text-muted" />
                                    </span>
                                    <div className="min-w-0 flex-1">
                                        <p className="flex items-center gap-2 text-[15px] font-medium">
                                            <span className="truncate">{p.name}</span>
                                            <OnlineDot online={p.online} />
                                        </p>
                                        <p className="truncate text-[12.5px] text-muted">
                                            {p.online ? "Online" : p.lastSeen ? `Last seen ${ago(p.lastSeen * 1000)}` : "Not seen yet"}
                                            {p.address && ` · ${p.address}`}
                                            {p.version && ` · KumoBox ${p.version}`}
                                        </p>
                                    </div>
                                    {p.error && p.allowed && <Badge tone="red">Not answering</Badge>}
                                    {!p.online && (
                                        <IconButton size="sm" label={`Remove ${p.name}`} onClick={() => confirm(`Remove ${p.name}? It shows up again when it's back on the network.`) && forget.mutate(p.id)}>
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    )}
                                </div>
                                <div className="divide-y divide-line border-t border-line">
                                    <Setting label="Share the library" help="It plays the server's movies and shows.">
                                        <Switch label={`Share the library with ${p.name}`} checked={p.allowed} onChange={v => save.mutate({ id: p.id, change: { allowed: v } })} />
                                    </Setting>
                                    {p.allowed && (
                                        <Setting label="Let it download here" help="Its downloads and torrents go onto the server, into its library, and it can upload files and folders into it.">
                                            <Switch label={`Let ${p.name} download here`} checked={p.downloads} onChange={v => save.mutate({ id: p.id, change: { downloads: v } })} />
                                        </Setting>
                                    )}
                                </div>
                            </div>
                        ))}
                    </section>
                ) : null,
            )}
        </div>
    )
}

function Setting({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
    return (
        <div className="flex items-center justify-between gap-x-6 gap-y-2 px-4 py-3 max-sm:flex-wrap">
            <div className="min-w-0 max-sm:flex-1 max-sm:basis-48">
                <p className="text-sm font-medium">{label}</p>
                {help && <p className="mt-0.5 text-[12.5px] leading-snug text-muted">{help}</p>}
            </div>
            <div className="shrink-0">{children}</div>
        </div>
    )
}

function Loading() {
    return (
        <div className="grid place-items-center py-24">
            <Spinner className="size-7" />
        </div>
    )
}

// ------------------------------------------------------------------- library

export function MediaLibrary({ kind, moviesPort }: { kind: MediaKind; moviesPort?: number }) {
    const qc = useQueryClient()
    const { data: settings } = useMSettings()
    const { data: titles, isLoading } = useMTitles()
    const { data: unmatched } = useMUnmatched()
    const { data: status } = useMStatus()
    const saveSettings = useSaveMSettings()
    const [picking, setPicking] = useState(false)
    const [uploading, setUploading] = useState(false)
    const [q, setQ] = useState("")
    const folders = foldersOf(settings)
    const setFolders = (next: string[]) => saveSettings.mutate(s => ({ ...s, library: { ...s.library, dir: next[0] ?? "", extraDirs: next.slice(1) } }))
    const scan = useMutation({
        mutationFn: (full: boolean) => api.post(`${M}/library/scan`, { full }),
        onSuccess: () => {
            toast.info("Scanning the library…")
            qc.invalidateQueries({ queryKey: ["movies"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const all = titles ?? []
    const c = kindCounts(all, kind)
    const shown = useMemo(() => {
        const words = q.trim().toLowerCase()
        return ofKind(all, kind)
            .filter(t => !words || mediaTitle(t.media).toLowerCase().includes(words))
            .sort((a, b) => b.addedAt - a.addedAt)
    }, [all, kind, q])
    const unmatchedFiles = (unmatched ?? []).reduce((n, g) => n + g.files.length, 0)
    return (
        <div className="flex flex-col gap-5">
            <UploadDialog
                open={uploading}
                onOpenChange={setUploading}
                base={M}
                description={`${Noun(kind)} and their subtitles from this computer, into the server's library. Folders keep their names and what's in them: ${
                    kind === "tv" ? "a show's folder with its seasons" : "a movie's folder"
                } is best.`}
            />
            <div className="grid gap-5 lg:grid-cols-3">
                <div className="flex min-w-0 flex-col gap-5 lg:col-span-2">
                    <Panel
                        title="Folders"
                        action={
                            <div className="flex items-center gap-2">
                                {folders.length > 0 && (
                                    <Button size="sm" icon={<Upload className="size-4" />} onClick={() => setUploading(true)}>
                                        Upload
                                    </Button>
                                )}
                                <Button size="sm" variant="primary" icon={<FolderPlus className="size-4" />} onClick={() => setPicking(true)}>
                                    Add a folder
                                </Button>
                            </div>
                        }
                    >
                        <p className="border-b border-line px-4 py-2.5 text-[12.5px] text-muted">The same folders for Movies and TV: a folder may have both, KumoBox tells them apart.</p>
                        {folders.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">No folder yet. Add the folder your movies and shows are in (on a Raspberry Pi, the USB drive's, like /media/pi/Movies).</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {folders.map(f => (
                                    <li key={f} className="flex items-center gap-3 px-4 py-3">
                                        <Folder className="size-4 shrink-0 text-amber-300" />
                                        <span className="min-w-0 flex-1 truncate font-mono text-[13px]" title={f}>
                                            {shortPath(f)}
                                        </span>
                                        <IconButton size="sm" label="Stop sharing this folder" onClick={() => setFolders(folders.filter(x => x !== f))}>
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>
                </div>
                <div className="flex min-w-0 flex-col gap-5">
                    <Panel title="In the library">
                        <dl className="divide-y divide-line">
                            <Row label={Noun(kind)} value={c.titles.toLocaleString()} />
                            {kind === "tv" && <Row label="Episodes" value={c.episodes.toLocaleString()} />}
                            <Row label="Size" value={bytes(c.size)} />
                            <Row label="Not matched (movies and shows)" value={unmatchedFiles.toLocaleString()} tone={unmatchedFiles ? "text-amber-300" : undefined} />
                        </dl>
                        <div className="flex flex-wrap items-center gap-2 border-t border-line p-4">
                            <Button size="sm" icon={<RefreshCw className={cn("size-4", status?.scanning && "animate-spin")} />} loading={scan.isPending} disabled={status?.scanning} onClick={() => scan.mutate(false)}>
                                {status?.scanning ? "Scanning…" : "Scan for new files"}
                            </Button>
                            <Button size="sm" variant="ghost" disabled={status?.scanning} onClick={() => scan.mutate(true)}>
                                Scan everything again
                            </Button>
                        </div>
                    </Panel>
                    {unmatchedFiles > 0 && (
                        <a href={moviesPagesURL(moviesPort, "/library")} target="_blank" rel="noreferrer" className="card flex items-center gap-3 p-4 text-sm transition-colors hover:border-line-strong">
                            <ExternalLink className="size-4 shrink-0 text-muted" />
                            <span className="min-w-0 flex-1">
                                <span className="font-medium">Fix what isn't matched</span>
                                <span className="block text-[12.5px] text-muted">In KumoBox's Library tools, on this server.</span>
                            </span>
                        </a>
                    )}
                </div>
            </div>

            <Panel
                title={`${Noun(kind)} · ${shown.length.toLocaleString()}`}
                action={
                    <div className="w-full sm:w-56">
                        <Input icon={<Search className="size-4" />} placeholder={`Find ${kind === "tv" ? "a show" : "a movie"}`} value={q} onChange={e => setQ(e.target.value)} />
                    </div>
                }
            >
                {isLoading ? (
                    <Loading />
                ) : shown.length === 0 ? (
                    <div className="p-4">
                        <EmptyState icon={kind === "tv" ? <Tv /> : <Clapperboard />} title={q ? "Nothing found" : `No ${noun(kind)} yet`}>
                            {q ? "No title has those words." : `Add a folder with ${noun(kind)}, or upload them: once scanned (and matched, with a TMDB key), they show here.`}
                        </EmptyState>
                    </div>
                ) : (
                    <div className="grid grid-cols-3 gap-4 p-4 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-7">
                        {shown.map(t => (
                            <PosterCard key={t.media.id} t={t} kind={kind} />
                        ))}
                    </div>
                )}
            </Panel>
            <DirPicker open={picking} onOpenChange={setPicking} start={folders[0]} onPick={p => !folders.includes(p) && setFolders([...folders, p])} />
        </div>
    )
}

// ------------------------------------------------------------------ settings

export function MediaSettings({ moviesPort }: { moviesPort?: number }) {
    const { data: settings } = useMSettings()
    const { data: sharing } = useMSharing()
    const save = useSaveMSettings()
    const [key, setKey] = useState<string | null>(null)
    const [name, setName] = useState<string | null>(null)
    if (!settings) return <Loading />
    const apiKey = key ?? settings.tmdb.apiKey ?? ""
    const shownName = name ?? settings.sharing.name ?? ""
    return (
        <div className="grid gap-5 lg:grid-cols-2">
            <p className="text-sm leading-relaxed text-muted lg:col-span-2">The movies side's settings: the same for Movies and TV. The server's password, updates and the anime side's settings are under Anime › Settings.</p>
            <Panel title="The Movie Database (TMDB)">
                <form
                    className="flex flex-col gap-2 p-4"
                    onSubmit={e => {
                        e.preventDefault()
                        save.mutate(s => ({ ...s, tmdb: { ...s.tmdb, apiKey: apiKey.trim() } }), { onSuccess: () => toast.success("Saved: the library is matched with it") })
                    }}
                >
                    <p className="text-[12.5px] leading-relaxed text-muted">
                        Movies and shows are matched, and get their posters and episodes, from TMDB. A key is free: themoviedb.org › Settings › API (the "API Key").
                    </p>
                    <div className="flex gap-2">
                        <Input type="password" autoComplete="off" icon={<KeyRound className="size-4" />} placeholder="API key" value={apiKey} onChange={e => setKey(e.target.value)} />
                        <Button type="submit" loading={save.isPending} disabled={apiKey.trim() === (settings.tmdb.apiKey ?? "")}>
                            Save
                        </Button>
                    </div>
                </form>
            </Panel>
            <Panel title="On the network">
                <form
                    className="flex flex-col gap-2 p-4"
                    onSubmit={e => {
                        e.preventDefault()
                        save.mutate(s => ({ ...s, sharing: { ...s.sharing, name: shownName.trim() } }), { onSuccess: () => toast.success("Saved") })
                    }}
                >
                    <p className="text-[12.5px] leading-relaxed text-muted">What the KumoBox apps call this server.{sharing?.addresses?.length ? ` It's at ${sharing.addresses.join(", ")}.` : ""}</p>
                    <div className="flex gap-2">
                        <Input placeholder={sharing?.name || "This computer's name"} value={shownName} onChange={e => setName(e.target.value)} />
                        <Button type="submit" loading={save.isPending} disabled={shownName.trim() === (settings.sharing.name ?? "")}>
                            Save
                        </Button>
                    </div>
                </form>
            </Panel>
            <a href={moviesPagesURL(moviesPort, "/settings")} target="_blank" rel="noreferrer" className="card flex items-center gap-3 p-4 text-sm transition-colors hover:border-line-strong lg:col-span-2">
                <HardDrive className="size-4 shrink-0 text-muted" />
                <span className="min-w-0 flex-1">
                    <span className="font-medium">Everything else</span>
                    <span className="block text-[12.5px] text-muted">Torrent clients, streaming providers, languages: KumoBox's own settings, on this server.</span>
                </span>
                <ExternalLink className="size-4 text-muted" />
            </a>
        </div>
    )
}
