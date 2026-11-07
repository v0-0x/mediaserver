import { ChevronRight, Clapperboard, Disc3, Folder, FolderPlus, KeyRound, Monitor, Music2, RefreshCw, Search, Server, Trash2, TriangleAlert, Upload, Wifi } from "lucide-react"
import { useState } from "react"
import { DirPicker } from "@/components/DirPicker"
import { UploadDialog } from "@/components/UploadDialog"
import { Badge, Button, EmptyState, IconButton, Input, Spinner, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import type { HubOverview } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SystemTiles } from "../Overview"
import { ago, bytes, OnlineDot, Panel, shortPath } from "../parts"
import { artURL, length, U, type UAlbum, type UDevice, type UTrack, useUAlbums, useUChange, useUDevices, useUFolders, useUStatus, useUTracks } from "./data"

// mediaserver's Music pages: its music side (Utako's server, for the Utako
// apps), on the dashboard, laid out like the Anime and Movies pages: an
// overview (the Pi, what's playing, the newest albums, the devices), the
// devices let in, the library's folders and albums, and its settings.

export type MusicTab = "overview" | "devices" | "library" | "settings"

// ------------------------------------------------------------------ overview

export function MusicOverview({ o, go }: { o: HubOverview; go: (t: MusicTab) => void }) {
    const { data: s } = useUStatus()
    const { data: devices } = useUDevices()
    const recent = useUAlbums("", 12)
    if (!s) return <Loading />
    const list = devices ?? []
    const online = list.filter(d => d.online)
    const waiting = list.filter(d => d.online && !d.allowed)
    const albums = recent.data?.albums ?? []
    return (
        <div className="flex flex-col gap-5">
            {s.folders === 0 && (
                <Banner onClick={() => go("library")} icon={<FolderPlus className="size-5 text-amber-300" />} className="border-amber-400/30 bg-amber-400/[0.06] hover:border-amber-400/60">
                    <span className="font-medium">Add your music</span>
                    <span className="text-muted"> — the folder it's in, in Library. Songs can be uploaded there too, from Utako or this page.</span>
                </Banner>
            )}
            {waiting.length > 0 && (
                <Banner onClick={() => go("devices")} icon={<Wifi className="size-5 text-brand-strong" />} className="border-brand/40 bg-brand-soft hover:border-brand/70">
                    <span className="font-medium">{waiting.length === 1 ? `${waiting[0].name} wants the music` : `${waiting.length} Utako apps want the music`}</span>
                    <span className="text-muted"> — share it with {waiting.length === 1 ? "it" : "them"} in Devices.</span>
                </Banner>
            )}
            <SystemTiles o={o} />
            <div className="grid gap-5 lg:grid-cols-3">
                <div className="flex min-w-0 flex-col gap-5 lg:col-span-2">
                    {s.listening.length > 0 && (
                        <Panel title="Listening now">
                            <ul className="divide-y divide-line">
                                {s.listening.map(d => (
                                    <li key={d.id} className="flex items-center gap-3 px-4 py-3">
                                        <Cover path={d.playing!.path} has={d.playing!.hasArt} className="size-11" />
                                        <div className="min-w-0 flex-1">
                                            <p className="truncate text-sm font-medium">{d.playing!.title}</p>
                                            <p className="truncate text-[12.5px] text-muted">{[d.playing!.artist, d.playing!.album].filter(Boolean).join(" · ")}</p>
                                        </div>
                                        <span className="shrink-0 text-right text-[12.5px] text-muted">
                                            <span className="block text-fg">{d.name}</span>
                                            started {ago(d.playing!.at * 1000)}
                                        </span>
                                    </li>
                                ))}
                            </ul>
                        </Panel>
                    )}
                    <Panel
                        title="Recently added albums"
                        action={
                            <button className="flex items-center gap-1 text-[13px] text-muted hover:text-fg" onClick={() => go("library")}>
                                Library <ChevronRight className="size-4" />
                            </button>
                        }
                    >
                        {albums.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">No music yet. Add the folder it's in, or upload some, in Library.</p>
                        ) : (
                            <div className="grid grid-cols-3 gap-3 p-4 sm:grid-cols-4 xl:grid-cols-6">
                                {albums.map(a => (
                                    <AlbumCard key={a.artist + "/" + a.album} a={a} small />
                                ))}
                            </div>
                        )}
                    </Panel>
                </div>
                <div className="flex min-w-0 flex-col gap-5">
                    <Panel title="In the library">
                        <LibraryRows s={s} />
                    </Panel>
                    <Panel
                        title={`Utako apps · ${online.length} online`}
                        action={
                            <button className="flex items-center gap-1 text-[13px] text-muted hover:text-fg" onClick={() => go("devices")}>
                                Manage <ChevronRight className="size-4" />
                            </button>
                        }
                    >
                        {list.length === 0 ? (
                            <p className="px-4 py-6 text-center text-sm leading-relaxed text-muted">No devices yet. Open Utako on a computer or phone on this network and turn on Settings › Media server › Use a media server.</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {list.slice(0, 6).map(d => (
                                    <li key={d.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                                        <Monitor className="size-4 shrink-0 text-muted" />
                                        <span className="min-w-0 flex-1 truncate">{d.name || d.id}</span>
                                        {d.allowed ? <Badge tone="green">Shared</Badge> : <Badge>Not shared</Badge>}
                                        <OnlineDot online={d.online} />
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

function LibraryRows({ s }: { s: { songs: number; albums: number; artists: number; size: number } }) {
    return (
        <dl className="divide-y divide-line">
            <Row label="Songs" value={s.songs.toLocaleString()} />
            <Row label="Albums" value={s.albums.toLocaleString()} />
            <Row label="Artists" value={s.artists.toLocaleString()} />
            <Row label="Size" value={bytes(s.size)} />
        </dl>
    )
}

function Row({ label, value, tone }: { label: string; value: React.ReactNode; tone?: string }) {
    return (
        <div className="flex items-center justify-between gap-4 px-4 py-2.5 text-sm">
            <dt className="text-muted">{label}</dt>
            <dd className={cn("min-w-0 truncate text-right font-medium tabular-nums", tone)}>{value}</dd>
        </div>
    )
}

function Banner({ onClick, icon, className, children }: { onClick: () => void; icon: React.ReactNode; className?: string; children: React.ReactNode }) {
    return (
        <button onClick={onClick} className={cn("card flex items-center gap-3 p-4 text-left transition-colors", className)}>
            {icon}
            <span className="min-w-0 flex-1 text-sm">{children}</span>
            <ChevronRight className="size-4 text-muted" />
        </button>
    )
}

function Cover({ path, has, className }: { path?: string; has?: boolean; className?: string }) {
    const [broken, setBroken] = useState(false)
    return (
        <span className={cn("grid shrink-0 place-items-center overflow-hidden rounded-md bg-white/[0.05] text-subtle ring-1 ring-white/[0.06]", className)}>
            {path && has && !broken ? <img src={artURL(path)} alt="" loading="lazy" className="size-full object-cover" onError={() => setBroken(true)} /> : <Music2 className="size-4" />}
        </span>
    )
}

// An album, as the Movies pages show a poster: the cover, the name, the
// artist and how many songs.
function AlbumCard({ a, small }: { a: UAlbum; small?: boolean }) {
    const [broken, setBroken] = useState(false)
    return (
        <div className="min-w-0" title={`${a.album} — ${a.artist}`}>
            <div className="aspect-square overflow-hidden rounded-lg bg-white/[0.04] ring-1 ring-white/[0.06]">
                {a.cover && !broken ? (
                    <img src={artURL(a.cover)} alt="" loading="lazy" className="size-full object-cover" onError={() => setBroken(true)} />
                ) : (
                    <span className="grid size-full place-items-center text-subtle">
                        <Disc3 className={small ? "size-6" : "size-8"} />
                    </span>
                )}
            </div>
            <p className="mt-1.5 truncate text-[13px] font-medium">{a.album}</p>
            <p className="truncate text-[12px] text-muted">{[a.artist || "Unknown artist", `${a.songs} ${a.songs === 1 ? "song" : "songs"}`].join(" · ")}</p>
        </div>
    )
}

// ------------------------------------------------------------------- devices

export function MusicDevices() {
    const { data: devices, isLoading } = useUDevices()
    const { data: s } = useUStatus()
    const save = useUChange(({ id, change }: { id: string; change: { allowed?: boolean; uploads?: boolean } }) => api.post(`${U}/devices/${encodeURIComponent(id)}`, change))
    const forget = useUChange((id: string) => api.del(`${U}/devices/${encodeURIComponent(id)}`))
    if (isLoading) return <Loading />
    const list = devices ?? []
    return (
        <div className="flex flex-col gap-4">
            <p className="max-w-3xl text-sm leading-relaxed text-muted">
                The Utako apps that asked for the server's music. A device shows up here once <span className="text-fg">Settings › Media server › Use a media server</span> is on in Utako. Let it in and it plays the
                server's songs, as a playlist named after it.
            </p>
            {list.length === 0 && (
                <EmptyState icon={<Wifi />} title="No devices yet">
                    Open Utako on a computer or phone on this network and turn on Settings › Media server › Use a media server.
                    {s ? ` If it doesn't find the server, type its address there: ${window.location.hostname}:${s.port}.` : ""}
                </EmptyState>
            )}
            {list.map(d => (
                <DeviceCard key={d.id} d={d} onChange={change => save.mutate({ id: d.id, change })} onForget={() => confirm(`Remove ${d.name}? It shows up again, not let in, the next time it asks.`) && forget.mutate(d.id)} />
            ))}
        </div>
    )
}

function DeviceCard({ d, onChange, onForget }: { d: UDevice; onChange: (c: { allowed?: boolean; uploads?: boolean }) => void; onForget: () => void }) {
    return (
        <div className={cn("card overflow-hidden", !d.allowed && d.online && "border-brand/40")}>
            <div className="flex items-center gap-3 px-4 py-3.5">
                <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.05]">
                    <Monitor className="size-5 text-muted" />
                </span>
                <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 text-[15px] font-medium">
                        <span className="truncate">{d.name || "Utako"}</span>
                        <OnlineDot online={d.online} />
                    </p>
                    <p className="truncate text-[12.5px] text-muted">
                        {d.online ? "Online" : `Last seen ${ago(d.lastSeen * 1000)}`}
                        {d.ip && ` · ${d.ip}`}
                        {` · ${d.id}`}
                    </p>
                </div>
                {!d.online && (
                    <IconButton size="sm" label={`Remove ${d.name}`} onClick={onForget}>
                        <Trash2 className="size-4" />
                    </IconButton>
                )}
            </div>
            <div className="divide-y divide-line border-t border-line">
                <Setting label="Share the music" help="It plays the server's songs, and can take the settings and logins kept here.">
                    <Switch label={`Share the music with ${d.name}`} checked={d.allowed} onChange={v => onChange({ allowed: v })} />
                </Setting>
                {d.allowed && (
                    <Setting label="Let it upload here" help="It can send songs and folders from its computer into the library.">
                        <Switch label={`Let ${d.name} upload here`} checked={d.uploads} onChange={v => onChange({ uploads: v })} />
                    </Setting>
                )}
            </div>
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

// ------------------------------------------------------------------- library

const PAGE = 100

export function MusicLibrary() {
    const { data: s } = useUStatus()
    const { data: folders } = useUFolders()
    const [picking, setPicking] = useState(false)
    const [uploading, setUploading] = useState(false)
    const [q, setQ] = useState("")
    const [view, setView] = useState<"albums" | "songs">("albums")
    const [limit, setLimit] = useState(PAGE)
    const words = q.trim()
    const albums = useUAlbums(words, limit)
    const tracks = useUTracks(words, limit)
    const add = useUChange((path: string) => api.post(`${U}/folders`, { path }), "Reading the folder…")
    const remove = useUChange((path: string) => api.del(`${U}/folders?path=${encodeURIComponent(path)}`))
    const scan = useUChange(() => api.post(`${U}/scan`, {}), "Looking at the folders again…")
    const list = folders ?? []
    const shownAlbums = albums.data?.albums ?? []
    const shownSongs = tracks.data?.tracks ?? []
    const total = view === "albums" ? (albums.data?.total ?? 0) : (tracks.data?.total ?? 0)
    const shown = view === "albums" ? shownAlbums.length : shownSongs.length
    const loading = view === "albums" ? albums.isLoading : tracks.isLoading
    return (
        <div className="flex flex-col gap-5">
            <UploadDialog open={uploading} onOpenChange={setUploading} base={U} description="Songs and albums from this computer, into the server's music. Folders keep their names and what's in them: an artist's or an album's folder is best." />
            <div className="grid gap-5 lg:grid-cols-3">
                <div className="flex min-w-0 flex-col gap-5 lg:col-span-2">
                    <Panel
                        title="Folders"
                        action={
                            <div className="flex items-center gap-2">
                                {list.length > 0 && (
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
                        <p className="border-b border-line px-4 py-2.5 text-[12.5px] text-muted">Songs keep their own tags; a cover.jpg (or folder, front, album) next to them is their cover. Music videos (mp4, mkv, webm, mov) are indexed with them. Utako's uploads go into the first folder.</p>
                        {list.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">No folder yet. Add the folder your music is in (on a Raspberry Pi, the USB drive's, like /media/pi/Music).</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {list.map(f => (
                                    <li key={f.path} className="flex items-center gap-3 px-4 py-3">
                                        <Folder className="size-4 shrink-0 text-amber-300" />
                                        <span className="min-w-0 flex-1 truncate font-mono text-[13px]" title={f.path}>
                                            {shortPath(f.path)}
                                        </span>
                                        {f.missing ? (
                                            <Badge tone="amber" className="shrink-0">
                                                <TriangleAlert className="size-3" /> Not there
                                            </Badge>
                                        ) : (
                                            <span className="shrink-0 text-[12.5px] text-muted tabular-nums">{f.songs.toLocaleString()} songs</span>
                                        )}
                                        <IconButton size="sm" label="Stop sharing this folder" onClick={() => confirm(`Stop sharing ${f.path}? Its files stay where they are.`) && remove.mutate(f.path)}>
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
                        {s ? <LibraryRows s={s} /> : <Loading />}
                        <div className="flex flex-wrap items-center gap-2 border-t border-line p-4">
                            <Button size="sm" icon={<RefreshCw className={cn("size-4", s?.scanning && "animate-spin")} />} loading={scan.isPending} disabled={!list.length || s?.scanning} onClick={() => scan.mutate(undefined)}>
                                {s?.scanning ? "Scanning…" : "Scan for new songs"}
                            </Button>
                        </div>
                        {s && !s.lengths && list.length > 0 && <p className="border-t border-line px-4 py-2.5 text-[12.5px] text-amber-300">ffprobe isn't on the server: songs have no length until it is (it comes with ffmpeg).</p>}
                    </Panel>
                </div>
            </div>

            <Panel
                title={`${view === "albums" ? "Albums" : "Songs"} · ${total.toLocaleString()}`}
                action={
                    <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
                        <div role="tablist" aria-label="Albums or songs" className="flex shrink-0 rounded-lg bg-white/[0.05] p-0.5">
                            {(["albums", "songs"] as const).map(v => (
                                <button
                                    key={v}
                                    role="tab"
                                    aria-selected={view === v}
                                    onClick={() => {
                                        setView(v)
                                        setLimit(PAGE)
                                    }}
                                    className={cn("focus-ring h-7 rounded-md px-3 text-[12.5px] font-medium", view === v ? "bg-white/[0.1] text-fg" : "text-muted hover:text-fg")}
                                >
                                    {v === "albums" ? "Albums" : "Songs"}
                                </button>
                            ))}
                        </div>
                        <div className="min-w-0 flex-1 sm:w-56 sm:flex-none">
                            <Input
                                icon={<Search className="size-4" />}
                                placeholder={view === "albums" ? "Find an album or artist" : "Find a song, artist or album"}
                                value={q}
                                onChange={e => {
                                    setQ(e.target.value)
                                    setLimit(PAGE)
                                }}
                            />
                        </div>
                    </div>
                }
            >
                {loading ? (
                    <Loading />
                ) : shown === 0 ? (
                    <div className="p-4">
                        <EmptyState icon={<Disc3 />} title={q ? "Nothing found" : "No music yet"}>
                            {q ? "Nothing has those words." : "Add a folder with music, or upload some: once read, it shows here."}
                        </EmptyState>
                    </div>
                ) : (
                    <>
                        {view === "albums" ? (
                            <div className="grid grid-cols-3 gap-4 p-4 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-7">
                                {shownAlbums.map(a => (
                                    <AlbumCard key={a.artist + "/" + a.album} a={a} />
                                ))}
                            </div>
                        ) : (
                            <ul className="divide-y divide-line">
                                {shownSongs.map(t => (
                                    <SongRow key={t.path} t={t} />
                                ))}
                            </ul>
                        )}
                        {total > shown && (
                            <div className="border-t border-line p-3 text-center">
                                <Button size="sm" variant="ghost" loading={view === "albums" ? albums.isFetching : tracks.isFetching} onClick={() => setLimit(l => l + PAGE * 2)}>
                                    Show more ({(total - shown).toLocaleString()} left)
                                </Button>
                            </div>
                        )}
                    </>
                )}
            </Panel>
            <DirPicker open={picking} onOpenChange={setPicking} start={list[0]?.path} onPick={p => add.mutate(p)} />
        </div>
    )
}

function SongRow({ t }: { t: UTrack }) {
    return (
        <li className="flex items-center gap-3 px-4 py-2" title={t.path}>
            <Cover path={t.path} has={t.hasArt} className="size-9" />
            <div className="min-w-0 flex-1">
                <p className="flex items-center gap-1.5 truncate text-sm">
                    {t.video && <Clapperboard className="size-3.5 shrink-0 text-muted" aria-label="Music video" />}
                    <span className="truncate">{t.title}</span>
                </p>
                <p className="truncate text-[12.5px] text-muted">{t.artist || "Unknown artist"}</p>
            </div>
            <p className="min-w-0 flex-1 truncate text-[13px] text-muted max-md:hidden">{t.album}</p>
            <span className="w-12 shrink-0 text-right text-[12.5px] text-muted tabular-nums">{length(t.durationMs)}</span>
        </li>
    )
}

// ------------------------------------------------------------------ settings

export function MusicSettings() {
    const { data: s } = useUStatus()
    const forget = useUChange(() => api.del(`${U}/kept`), "The settings and logins are gone from the server.")
    if (!s) return <Loading />
    const address = `${window.location.hostname}:${s.port}`
    return (
        <div className="grid gap-5 lg:grid-cols-2">
            <Panel title="The music side">
                <dl className="divide-y divide-line">
                    <Row label="Name (as Utako shows it)" value={s.name} />
                    <Row label="Address for Utako" value={<span className="font-mono text-[13px]">{address}</span>} />
                    <Row label="Found on the network" value="by itself (beacon on 43412/udp)" />
                    <Row label="Looks at the folders" value={s.lastScan ? `every 15 minutes · last ${ago(s.lastScan * 1000)}` : "every 15 minutes"} />
                    <Row label="Song lengths" value={s.lengths ? "from ffprobe" : "ffprobe missing"} tone={s.lengths ? undefined : "text-amber-300"} />
                    <Row label="mediaserver" value={s.version} />
                </dl>
                <p className="border-t border-line px-4 py-3 text-[12.5px] leading-relaxed text-muted">
                    Utako finds the server by itself. If it doesn't, type <span className="font-mono text-fg">{address}</span> in Utako's Settings › Media server › Address.
                </p>
            </Panel>
            <Panel title="Settings and logins">
                {s.kept ? (
                    <div className="flex items-center gap-3 px-4 py-3 text-sm">
                        <KeyRound className="size-4 shrink-0 text-muted" />
                        <span className="min-w-0 flex-1">
                            <span className="block truncate">Sent by {s.kept.from || "a device"}</span>
                            <span className="text-[12.5px] text-muted">{ago(s.kept.received * 1000)}, {bytes(s.kept.size)}: the devices you share the music with can take them.</span>
                        </span>
                        <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => confirm("Remove the settings and logins kept on the server? The devices keep their own.") && forget.mutate(undefined)}>
                            Forget
                        </Button>
                    </div>
                ) : (
                    <p className="px-4 py-4 text-[13px] leading-relaxed text-muted">None kept. Utako's Settings › Media server › Send my settings and logins puts them here, sealed, for your other devices.</p>
                )}
                <p className="flex items-start gap-2 border-t border-line px-4 py-3 text-[12.5px] leading-relaxed text-muted">
                    <Server className="mt-0.5 size-4 shrink-0" />
                    They're sealed so only the server can read them, and sealed again for each device that asks.
                </p>
            </Panel>
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
