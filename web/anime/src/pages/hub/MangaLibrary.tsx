import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { BookOpen, Folder, FolderOpen, RotateCcw, Trash2 } from "lucide-react"
import { useState } from "react"
import { DirPicker } from "@/components/DirPicker"
import { Badge, Button, Dialog, IconButton, Spinner } from "@/components/ui"
import { api } from "@/lib/api"
import { useSaveSettings } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { LocalMangaChapter, MangaSeries, Status } from "@/lib/types"
import { ago, bytes, Panel, shortPath } from "./parts"

// The server's manga: what devices downloaded onto it (Download on a
// manga's page, Save on: the server), each series with its covers and
// chapters (CBZ files with their details inside), and the folder they're in.

type Library = { dir: string; series: MangaSeries[] | null }

export default function MangaLibrary({ status }: { status: Status }) {
    const settings = status.settings
    const saveSettings = useSaveSettings()
    const [picking, setPicking] = useState(false)
    const [open, setOpen] = useState<MangaSeries | null>(null)
    const { data, isLoading, error } = useQuery({
        queryKey: ["manga-library"],
        queryFn: () => api.get<Library>("/api/manga-library"),
        refetchInterval: 15000,
    })
    const series = data?.series ?? []
    const total = series.reduce((n, s) => n + s.size, 0)
    const chapters = series.reduce((n, s) => n + s.count, 0)
    const chosen = (settings.manga.dir ?? "").trim()
    const setDir = (dir: string) => saveSettings.mutate({ ...settings, manga: { ...settings.manga, dir } })

    return (
        <div className="flex flex-col gap-5">
            <div className="grid gap-5 lg:grid-cols-3">
                <Panel
                    className="lg:col-span-2"
                    title="Manga folder"
                    action={
                        <div className="flex items-center gap-2">
                            {chosen && (
                                <Button size="sm" variant="ghost" icon={<RotateCcw className="size-4" />} onClick={() => setDir("")}>
                                    Default
                                </Button>
                            )}
                            <Button size="sm" variant="primary" icon={<FolderOpen className="size-4" />} onClick={() => setPicking(true)}>
                                Change
                            </Button>
                        </div>
                    }
                >
                    <div className="flex items-center gap-3 px-4 py-3">
                        <Folder className="size-4 shrink-0 text-amber-300" />
                        <span className="min-w-0 flex-1 truncate font-mono text-[13px]" title={data?.dir}>
                            {data ? shortPath(data.dir) : "…"}
                        </span>
                        {!chosen && <Badge>Default</Badge>}
                    </div>
                    <p className="border-t border-line px-4 py-3 text-[12.5px] leading-relaxed text-muted">
                        Chapters sent from the devices land here, a folder per series with its cover and details. Manga already in the old folder stay there when you change it: move them over yourself.
                    </p>
                </Panel>
                <Panel title="Manga on the server">
                    <dl className="divide-y divide-line">
                        <Line label="Series" value={series.length.toLocaleString()} />
                        <Line label="Chapters" value={chapters.toLocaleString()} />
                        <Line label="Size" value={bytes(total)} />
                    </dl>
                </Panel>
            </div>

            <Panel title="Series">
                {isLoading ? (
                    <div className="grid place-items-center py-12">
                        <Spinner />
                    </div>
                ) : error ? (
                    <p className="px-4 py-8 text-center text-sm text-red-300">{(error as Error).message}</p>
                ) : series.length === 0 ? (
                    <div className="mx-auto flex max-w-md flex-col items-center gap-3 px-4 py-10 text-center">
                        <BookOpen className="size-8 text-muted" />
                        <p className="font-medium">No manga on the server yet</p>
                        <p className="text-sm text-muted">
                            In the Aniko app on a device that uses this server, open a manga, press Download, set <b>Save on</b> to this server and pick the chapters. They're sent here with their cover and details, and every device can read them.
                        </p>
                    </div>
                ) : (
                    <div className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-5 p-4">
                        {series.map(s => (
                            <button key={s.anilistId} onClick={() => setOpen(s)} className="group focus-ring flex flex-col gap-2 rounded-lg text-left">
                                <div className="aspect-[2/3] overflow-hidden rounded-lg bg-surface-2">
                                    {s.hasCover ? (
                                        <img src={`/api/manga-library/${s.anilistId}/cover`} alt="" loading="lazy" className="size-full object-cover transition group-hover:scale-[1.03]" />
                                    ) : (
                                        <div className="grid size-full place-items-center text-muted">
                                            <BookOpen className="size-8" />
                                        </div>
                                    )}
                                </div>
                                <div className="min-w-0">
                                    <p className="truncate text-sm font-medium" title={s.title}>
                                        {s.title}
                                    </p>
                                    <p className="text-xs text-muted">
                                        {s.count} {s.count === 1 ? "chapter" : "chapters"}
                                        {s.latest && ` · up to ${s.latest}`} · {bytes(s.size)}
                                    </p>
                                </div>
                            </button>
                        ))}
                    </div>
                )}
            </Panel>

            <DirPicker open={picking} onOpenChange={setPicking} start={data?.dir} onPick={setDir} />
            {open && <SeriesDialog series={open} onClose={() => setOpen(null)} />}
        </div>
    )
}

// A series: its details and chapters, each to delete, or all of it.
function SeriesDialog({ series, onClose }: { series: MangaSeries; onClose: () => void }) {
    const qc = useQueryClient()
    const [sure, setSure] = useState(false)
    const { data, isLoading } = useQuery({
        queryKey: ["manga-library", series.anilistId],
        queryFn: () => api.get<{ series: MangaSeries; chapters: LocalMangaChapter[] | null }>(`/api/manga-library/${series.anilistId}`),
    })
    const remove = useMutation({
        mutationFn: (file?: string) => api.del(`/api/manga-library/${series.anilistId}${file ? `?file=${encodeURIComponent(file)}` : ""}`),
        onSuccess: (_, file) => {
            qc.invalidateQueries({ queryKey: ["manga-library"] })
            if (!file) {
                toast.success(`Deleted ${series.title}`)
                onClose()
            }
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const s = data?.series ?? series
    const chapters = [...(data?.chapters ?? [])].reverse()
    const meta = [s.year, s.status && s.status.charAt(0) + s.status.slice(1).toLowerCase().replace(/_/g, " "), s.chapters && `${s.chapters} chapters out`].filter(Boolean).join(" · ")
    return (
        <Dialog
            open
            onOpenChange={v => !v && onClose()}
            title={s.title}
            description={meta || undefined}
            className="max-w-xl"
            footer={
                <div className="flex w-full items-center justify-between gap-2">
                    <span className="text-xs text-muted">
                        {s.count} {s.count === 1 ? "chapter" : "chapters"} · {bytes(s.size)}
                    </span>
                    {sure ? (
                        <div className="flex gap-2">
                            <Button size="sm" variant="ghost" onClick={() => setSure(false)}>
                                Keep it
                            </Button>
                            <Button size="sm" variant="danger" loading={remove.isPending && !remove.variables} onClick={() => remove.mutate(undefined)}>
                                Delete for good
                            </Button>
                        </div>
                    ) : (
                        <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setSure(true)}>
                            Delete the series
                        </Button>
                    )}
                </div>
            }
        >
            {s.genres && s.genres.length > 0 && (
                <div className="mb-3 flex flex-wrap gap-1.5">
                    {s.genres.map(g => (
                        <Badge key={g}>{g}</Badge>
                    ))}
                </div>
            )}
            <p className="mb-3 truncate font-mono text-xs text-muted" title={s.folder}>
                {s.folder}
            </p>
            {isLoading ? (
                <div className="grid place-items-center py-8">
                    <Spinner />
                </div>
            ) : chapters.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted">No chapters left in this folder.</p>
            ) : (
                <ul className="max-h-[50vh] divide-y divide-line overflow-y-auto rounded-lg border border-line">
                    {chapters.map(c => (
                        <li key={c.file} className="flex items-center gap-3 px-3 py-2">
                            <div className="min-w-0 flex-1">
                                <p className="truncate text-sm font-medium">{c.title ? `Ch. ${c.chapter} · ${c.title}` : `Chapter ${c.chapter}`}</p>
                                <p className="text-xs text-muted">
                                    {c.pages} pages · {bytes(c.size)}
                                    {c.added > 0 && ` · ${ago(c.added * 1000)}`}
                                </p>
                            </div>
                            <IconButton size="sm" label={`Delete chapter ${c.chapter}`} disabled={remove.isPending && remove.variables === c.file} onClick={() => remove.mutate(c.file)}>
                                <Trash2 className="size-4" />
                            </IconButton>
                        </li>
                    ))}
                </ul>
            )}
        </Dialog>
    )
}

function Line({ label, value }: { label: string; value: string }) {
    return (
        <div className="flex items-center justify-between px-4 py-2.5 text-sm">
            <dt className="text-muted">{label}</dt>
            <dd className="font-medium">{value}</dd>
        </div>
    )
}
