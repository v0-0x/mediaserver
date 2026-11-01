import { ChevronRight, Disc3, Folder, FolderPlus, KeyRound, Monitor, Music2, RefreshCw, Search, Trash2, TriangleAlert, Upload, Wifi } from "lucide-react"
import { useState } from "react"
import { DirPicker } from "@/components/DirPicker"
import { UploadDialog } from "@/components/UploadDialog"
import { Badge, Button, EmptyState, IconButton, Input, Spinner, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"
import { ago, OnlineDot, Panel, shortPath } from "../parts"
import { artURL, length, U, type UDevice, type UTrack, useUChange, useUDevices, useUFolders, useUStatus, useUTracks } from "./data"

// mediaserver's Music pages: its music side (Utako's server, for the Utako
// apps), on the dashboard. Fewer than the other sides': what's playing, the
// devices let in, and the library's folders and songs.

export type MusicTab = "overview" | "devices" | "library"

// ------------------------------------------------------------------ overview

export function MusicOverview({ go }: { go: (t: MusicTab) => void }) {
    const { data: s } = useUStatus()
    const { data: devices } = useUDevices()
    const forget = useUChange(() => api.del(`${U}/kept`), "The settings and logins are gone from the server.")
    if (!s) return <Loading />
    const list = devices ?? []
    const waiting = list.filter(d => d.online && !d.allowed)
    return (
        <div className="flex flex-col gap-5">
            {waiting.length > 0 && (
                <Banner onClick={() => go("devices")} icon={<Wifi className="size-5 text-brand-strong" />} className="border-brand/40 bg-brand-soft hover:border-brand/70">
                    <span className="font-medium">{waiting.length === 1 ? `${waiting[0].name} wants the music` : `${waiting.length} Utako apps want the music`}</span>
                    <span className="text-muted"> — let {waiting.length === 1 ? "it" : "them"} in, in Devices.</span>
                </Banner>
            )}
            {s.folders === 0 && (
                <Banner onClick={() => go("library")} icon={<FolderPlus className="size-5 text-amber-300" />} className="border-amber-400/30 bg-amber-400/[0.06] hover:border-amber-400/60">
                    <span className="font-medium">Add your music</span>
                    <span className="text-muted"> — the folder it's in, in Library. Utako's songs can be uploaded there too.</span>
                </Banner>
            )}
            <div className="grid grid-cols-3 gap-3">
                <Count label="Songs" value={s.songs} />
                <Count label="Albums" value={s.albums} />
                <Count label="Artists" value={s.artists} />
            </div>
            <div className="grid gap-5 lg:grid-cols-3">
                <Panel title="Listening now" className="lg:col-span-2">
                    {s.listening.length === 0 ? (
                        <p className="px-4 py-8 text-center text-sm text-muted">Nobody is playing the server's music right now.</p>
                    ) : (
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
                    )}
                </Panel>
                <div className="flex min-w-0 flex-col gap-5">
                    <Panel
                        title={`Utako apps · ${s.online} online`}
                        action={
                            <button className="flex items-center gap-1 text-[13px] text-muted hover:text-fg" onClick={() => go("devices")}>
                                Manage <ChevronRight className="size-4" />
                            </button>
                        }
                    >
                        {list.length === 0 ? (
                            <p className="px-4 py-6 text-center text-sm leading-relaxed text-muted">No devices yet. In Utako, turn on Settings › Media server › Use a media server.</p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {list.slice(0, 6).map(d => (
                                    <li key={d.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                                        <Monitor className="size-4 shrink-0 text-muted" />
                                        <span className="min-w-0 flex-1 truncate">{d.name || d.id}</span>
                                        {d.allowed ? <Badge tone="green">Let in</Badge> : <Badge>Not let in</Badge>}
                                        <OnlineDot online={d.online} />
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>
                    <Panel title="Settings and logins">
                        {s.kept ? (
                            <div className="flex items-center gap-3 px-4 py-3 text-sm">
                                <KeyRound className="size-4 shrink-0 text-muted" />
                                <span className="min-w-0 flex-1">
                                    <span className="block truncate">From {s.kept.from || "a device"}</span>
                                    <span className="text-[12.5px] text-muted">Sent {ago(s.kept.received * 1000)}: the devices let in can take them.</span>
                                </span>
                                <IconButton size="sm" label="Forget them" onClick={() => confirm("Remove the settings and logins kept on the server? The devices keep their own.") && forget.mutate(undefined)}>
                                    <Trash2 className="size-4" />
                                </IconButton>
                            </div>
                        ) : (
                            <p className="px-4 py-4 text-[13px] leading-relaxed text-muted">None kept. Utako's Settings › Media server › Send my settings and logins puts them here, sealed, for your other devices.</p>
                        )}
                    </Panel>
                </div>
            </div>
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

function Count({ label, value }: { label: string; value: number }) {
    return (
        <div className="card px-4 py-3.5">
            <p className="text-[12.5px] text-muted">{label}</p>
            <p className="mt-0.5 text-2xl font-semibold tabular-nums">{value.toLocaleString()}</p>
        </div>
    )
}

function Cover({ path, has, className }: { path: string; has?: boolean; className?: string }) {
    const [broken, setBroken] = useState(false)
    return (
        <span className={cn("grid shrink-0 place-items-center overflow-hidden rounded-md bg-white/[0.05] text-subtle ring-1 ring-white/[0.06]", className)}>
            {has && !broken ? <img src={artURL(path)} alt="" loading="lazy" className="size-full object-cover" onError={() => setBroken(true)} /> : <Music2 className="size-4" />}
        </span>
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
    const [limit, setLimit] = useState(PAGE)
    const tracks = useUTracks(q.trim(), limit)
    const add = useUChange((path: string) => api.post(`${U}/folders`, { path }), "Reading the folder…")
    const remove = useUChange((path: string) => api.del(`${U}/folders?path=${encodeURIComponent(path)}`))
    const scan = useUChange(() => api.post(`${U}/scan`, {}), "Looking at the folders again…")
    const list = folders ?? []
    const shown = tracks.data?.tracks ?? []
    const total = tracks.data?.total ?? 0
    return (
        <div className="flex flex-col gap-5">
            <UploadDialog open={uploading} onOpenChange={setUploading} base={U} description="Songs and albums from this computer, into the server's music. Folders keep their names: an artist's or an album's folder is best." />
            <Panel
                title="Folders"
                action={
                    <div className="flex items-center gap-2">
                        <Button size="sm" variant="ghost" icon={<RefreshCw className={cn("size-4", s?.scanning && "animate-spin")} />} disabled={!list.length || s?.scanning} onClick={() => scan.mutate(undefined)}>
                            <span className="max-sm:hidden">{s?.scanning ? "Scanning…" : "Scan"}</span>
                        </Button>
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
                {s && !s.lengths && list.length > 0 && <p className="border-t border-line px-4 py-2.5 text-[12.5px] text-amber-300">ffprobe isn't on the server: songs have no length until it is (it comes with ffmpeg).</p>}
            </Panel>

            <Panel
                title={`Songs · ${total.toLocaleString()}`}
                action={
                    <div className="w-full sm:w-64">
                        <Input
                            icon={<Search className="size-4" />}
                            placeholder="Find a song, artist or album"
                            value={q}
                            onChange={e => {
                                setQ(e.target.value)
                                setLimit(PAGE)
                            }}
                        />
                    </div>
                }
            >
                {tracks.isLoading ? (
                    <Loading />
                ) : shown.length === 0 ? (
                    <div className="p-4">
                        <EmptyState icon={<Disc3 />} title={q ? "Nothing found" : "No songs yet"}>
                            {q ? "No song has those words." : "Add a folder with music, or upload some: once read, the songs show here."}
                        </EmptyState>
                    </div>
                ) : (
                    <>
                        <ul className="divide-y divide-line">
                            {shown.map(t => (
                                <SongRow key={t.path} t={t} />
                            ))}
                        </ul>
                        {total > shown.length && (
                            <div className="border-t border-line p-3 text-center">
                                <Button size="sm" variant="ghost" loading={tracks.isFetching} onClick={() => setLimit(l => l + PAGE * 2)}>
                                    Show more ({(total - shown.length).toLocaleString()} left)
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
                <p className="truncate text-sm">{t.title}</p>
                <p className="truncate text-[12.5px] text-muted">{t.artist || "Unknown artist"}</p>
            </div>
            <p className="min-w-0 flex-1 truncate text-[13px] text-muted max-md:hidden">{t.album}</p>
            <span className="w-12 shrink-0 text-right text-[12.5px] text-muted tabular-nums">{length(t.durationMs)}</span>
        </li>
    )
}

function Loading() {
    return (
        <div className="grid place-items-center py-24">
            <Spinner className="size-7" />
        </div>
    )
}
