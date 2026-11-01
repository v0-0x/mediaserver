import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowRightLeft, Download, Folder, FolderPlus, RefreshCw, Trash2, Upload } from "lucide-react"
import { useRef, useState } from "react"
import { DirPicker } from "@/components/DirPicker"
import { AllFiles, Ignored, Unmatched } from "@/components/library/LibraryTools"
import { UploadDialog } from "@/components/UploadDialog"
import { Badge, Button, IconButton, Progress, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { useCollection, useSaveSettings } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { DownloadItem, HubOverview, Status } from "@/lib/types"
import { img } from "@/lib/utils"
import { LibraryBrowse } from "./LibraryBrowse"
import { M, type MSettings } from "./media/data"
import { bytes, Panel, shortPath } from "./parts"

// The server's library: the folders it shares (on its disks: a USB drive on a
// Pi), scanning them, what devices are downloading onto it, and putting
// its files right (matching, ignoring, episode numbers).

export default function Library({ o, status }: { o: HubOverview; status: Status }) {
    const qc = useQueryClient()
    const settings = status.settings
    const folders = [settings.library.dir, ...(settings.library.extraDirs ?? [])].filter(Boolean)
    const [picking, setPicking] = useState(false)
    // Files from the computer this browser is on, into the library.
    const [uploading, setUploading] = useState(false)
    const saveSettings = useSaveSettings()
    const scan = useMutation({
        mutationFn: (full: boolean) => api.post("/api/library/scan", { full }),
        onSuccess: () => {
            toast.info("Scanning the library…")
            qc.invalidateQueries({ queryKey: ["hub", "overview"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
    // A folder added is scanned by the server at once.
    const setFolders = (next: string[]) => saveSettings.mutate({ ...settings, library: { ...settings.library, dir: next[0] ?? "", extraDirs: next.slice(1) } })
    // A folder of movies or shows: to the movies side's library (Movies and
    // TV), out of this one.
    const toMovies = useMutation({
        mutationFn: async (f: string) => {
            const m = await api.get<MSettings>(`${M}/settings`)
            const have = [m.library.dir, ...(m.library.extraDirs ?? [])].filter(Boolean)
            if (!have.includes(f)) {
                const next = [...have, f]
                await api.put(`${M}/settings`, { ...m, library: { ...m.library, dir: next[0], extraDirs: next.slice(1) } })
            }
            setFolders(folders.filter(x => x !== f))
        },
        onSuccess: () => {
            toast.success("Moved to Movies & TV: its movies and shows show there once scanned")
            qc.invalidateQueries({ queryKey: ["movies"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const downloads = useQuery({
        queryKey: ["hub", "downloads"],
        queryFn: () => api.get<DownloadItem[] | null>("/api/downloads"),
        refetchInterval: 3000,
    })
    const active = (downloads.data ?? []).filter(d => d.status === "queued" || d.status === "resolving" || d.status === "downloading")
    const done = (downloads.data ?? []).filter(d => d.status === "completed" || d.status === "failed").slice(0, 8)
    const lib = o.library
    const { data: coll } = useCollection()
    const [tab, setTab] = useState<"unmatched" | "files" | "ignored">(lib.unmatched > 0 ? "unmatched" : "files")
    const manage = useRef<HTMLDivElement>(null)
    const open = (t: typeof tab) => {
        setTab(t)
        manage.current?.scrollIntoView({ behavior: "smooth", block: "start" })
    }
    return (
        <div className="flex flex-col gap-5">
            <UploadDialog open={uploading} onOpenChange={setUploading} />
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
                        {folders.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">
                                No folder yet. Add the folder your anime are in (on a Raspberry Pi, the USB drive's, like /media/pi/Anime): its episodes are found and shared with the devices.
                            </p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {folders.map(f => (
                                    <li key={f} className="flex items-center gap-3 px-4 py-3">
                                        <Folder className="size-4 shrink-0 text-amber-300" />
                                        <span className="min-w-0 flex-1 truncate font-mono text-[13px]" title={f}>
                                            {shortPath(f)}
                                        </span>
                                        {o.moviesPort && looksLikeMovies(f) && (
                                            <Button size="sm" variant="ghost" icon={<ArrowRightLeft className="size-4" />} loading={toMovies.isPending && toMovies.variables === f} onClick={() => toMovies.mutate(f)}>
                                                <span className="max-sm:hidden">Move to Movies & TV</span>
                                            </Button>
                                        )}
                                        <IconButton size="sm" label="Stop sharing this folder" onClick={() => setFolders(folders.filter(x => x !== f))}>
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>

                    <Panel title="Downloading onto the server">
                        {active.length === 0 && done.length === 0 ? (
                            <p className="px-4 py-8 text-center text-sm text-muted">
                                Nothing. Devices you let download here (Devices › Let it download here) send their downloads and uploads to the server: they land in its library.
                            </p>
                        ) : (
                            <ul className="divide-y divide-line">
                                {[...active, ...done].map(d => (
                                    <li key={d.id} className="flex items-center gap-3 px-4 py-3">
                                        {d.image ? <img src={img(d.image)} alt="" className="h-12 w-9 shrink-0 rounded object-cover" /> : <Download className="size-4 shrink-0 text-muted" />}
                                        <div className="min-w-0 flex-1">
                                            <p className="truncate text-sm font-medium">
                                                {d.animeTitle} <span className="text-muted">· Episode {d.episode}</span>
                                            </p>
                                            {d.status === "downloading" ? (
                                                <div className="mt-1.5 flex items-center gap-2">
                                                    {d.done && !d.progress ? (
                                                        <span className="text-xs text-muted tabular-nums">
                                                            {d.done} downloaded {d.speed && `· ${d.speed}`}
                                                        </span>
                                                    ) : (
                                                        <>
                                                            <Progress value={d.progress} className="h-1 flex-1" />
                                                            <span className="shrink-0 text-xs text-muted tabular-nums">
                                                                {Math.floor(d.progress * 100)}% {d.speed && `· ${d.speed}`} {d.eta && `· ${d.eta} left`}
                                                            </span>
                                                        </>
                                                    )}
                                                </div>
                                            ) : (
                                                <p className="text-xs text-muted">{d.status === "failed" ? d.error || "Failed" : d.status === "completed" ? "Done" : "Waiting"}</p>
                                            )}
                                        </div>
                                        {d.status === "failed" && <Badge tone="red">Failed</Badge>}
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Panel>
                </div>

                <div className="flex min-w-0 flex-col gap-5">
                    <Panel title="In the library">
                        <dl className="divide-y divide-line">
                            <Line label="Anime" value={lib.anime.toLocaleString()} />
                            <Line label="Episodes" value={lib.files.toLocaleString()} />
                            <Line label="Size" value={bytes(lib.size)} />
                            <Line label="Not matched to an anime" value={lib.unmatched.toLocaleString()} onClick={lib.unmatched > 0 ? () => open("unmatched") : undefined} />
                        </dl>
                        <div className="flex flex-wrap gap-2 border-t border-line p-4">
                            <Button
                                size="sm"
                                icon={<RefreshCw className={lib.scanning ? "size-4 animate-spin" : "size-4"} />}
                                loading={scan.isPending}
                                disabled={lib.scanning}
                                onClick={() => scan.mutate(false)}
                            >
                                {lib.scanning ? "Scanning…" : "Scan for new episodes"}
                            </Button>
                            <Button size="sm" variant="ghost" disabled={lib.scanning} onClick={() => scan.mutate(true)}>
                                Scan everything again
                            </Button>
                        </div>
                    </Panel>
                    {settings.library.autoRefresh && (
                        <p className="px-1 text-[12.5px] leading-relaxed text-muted">The server watches its folders: episodes added or removed show on the devices by themselves.</p>
                    )}
                </div>

                <DirPicker open={picking} onOpenChange={setPicking} start={folders[0]} onPick={p => !folders.includes(p) && setFolders([...folders, p])} />
            </div>

            <LibraryBrowse />

            <div ref={manage} className="scroll-mt-28">
                <Panel title="Put the library right">
                    <div className="p-4">
                        <p className="mb-4 text-[13px] text-muted">
                            Match the files the server couldn't place, ignore what isn't an episode, and fix episode numbers or wrong matches. The devices see the changes at once.
                        </p>
                        <Tabs
                            className="mb-5"
                            value={tab}
                            onChange={setTab}
                            tabs={[
                                { value: "unmatched", label: "Not matched", count: lib.unmatched },
                                { value: "files", label: "All files" },
                                { value: "ignored", label: "Ignored", count: coll?.ignoredCount ?? 0 },
                            ]}
                        />
                        {tab === "unmatched" && <Unmatched />}
                        {tab === "files" && <AllFiles links={false} />}
                        {tab === "ignored" && <Ignored />}
                    </div>
                </Panel>
            </div>
        </div>
    )
}

function Line({ label, value, onClick }: { label: string; value: string; onClick?: () => void }) {
    return (
        <div className="flex items-center justify-between px-4 py-2.5 text-sm">
            <dt className="text-muted">{label}</dt>
            <dd className="font-medium">
                {onClick ? (
                    <button onClick={onClick} className="focus-ring rounded text-amber-300 underline-offset-2 hover:underline">
                        {value}
                    </button>
                ) : (
                    value
                )}
            </dd>
        </div>
    )
}

// A folder named like one of movies or shows (Movies, Films, TV, Shows,
// Series): they belong on the movies side, not here.
function looksLikeMovies(path: string) {
    const name = path.split(/[\\/]/).filter(Boolean).pop() ?? ""
    return /^(movies?|films?|tv( shows)?|shows|series)$/i.test(name.trim())
}
