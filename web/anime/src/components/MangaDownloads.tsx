import { useQuery, useQueryClient } from "@tanstack/react-query"
import { BookOpen, Download, HardDrive, Loader2, RotateCw, Trash2, X } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router-dom"
import { Badge, Button, Dialog, EmptyState, Input, Select } from "@/components/ui"
import { api } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"
import { androidApp } from "@/lib/tv"
import type { LocalMangaChapter, MangaChapter, MangaDownload, MangaSeries } from "@/lib/types"
import { cn } from "@/lib/utils"

// Downloading manga (server/internal/manga: download.go, library.go): a
// chapter as a .cbz with its metadata, in this device's manga folder or the
// media server's; downloaded ones read without the provider.

const active = (j: MangaDownload) => j.state === "queued" || j.state === "downloading" || j.state === "sending"

export function useMangaLocal(mediaId: number) {
    return useQuery({
        queryKey: ["manga-library", mediaId],
        queryFn: () => api.get<{ series: MangaSeries | null; chapters: LocalMangaChapter[] }>(`/api/manga-library/${mediaId}`),
        enabled: mediaId > 0,
    })
}

export function useMangaDownloads() {
    return useQuery({
        queryKey: ["manga-downloads"],
        queryFn: () => api.get<MangaDownload[]>("/api/manga-downloads"),
        refetchInterval: q => (q.state.data?.some(active) ? 2000 : false),
    })
}

type Target = { id?: string; name: string; online: boolean; here?: boolean }

// Where downloads can go: this device, and the media servers (and other
// computers) that take files from it.
function useTargets() {
    const { data: status } = useStatus()
    const { data: targets } = useQuery({
        queryKey: ["upload", "targets"],
        queryFn: () => api.get<Target[]>("/api/upload/targets").catch(() => [] as Target[]),
    })
    const servers = (targets ?? []).filter(t => !t.here && t.id)
    // A TV keeps nothing itself: to the server it downloads to, if any.
    const preferred = servers.find(t => t.id === status?.settings.sharing.downloadTo) ?? (androidApp ? servers[0] : undefined)
    return { servers, preferred: preferred?.id ?? "" }
}

// The chapter's download, on its row: downloaded (read from here), on its
// way, or a button.
export function ChapterDownload({
    mediaId,
    title,
    provider,
    chapter,
    local,
    job,
}: {
    mediaId: number
    title: string
    provider: string
    chapter: MangaChapter
    local?: LocalMangaChapter
    job?: MangaDownload
}) {
    const qc = useQueryClient()
    const { preferred } = useTargets()
    if (local) {
        return (
            <span className="flex shrink-0 items-center gap-1 px-3 text-xs text-emerald-300" title={`Downloaded: ${local.pages} pages`}>
                <HardDrive className="size-3.5" /> Downloaded
            </span>
        )
    }
    if (job && active(job)) {
        return (
            <span className="flex shrink-0 items-center gap-1.5 px-3 text-xs text-muted">
                <Loader2 className="size-3.5 animate-spin" />
                {progressText(job)}
            </span>
        )
    }
    return (
        <button
            className="grid size-10 shrink-0 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg"
            aria-label={`Download chapter ${chapter.chapter}`}
            title={job?.state === "failed" ? `Didn't download: ${job.error}` : `Download chapter ${chapter.chapter}`}
            onClick={() =>
                api
                    .post<{ added: number }>("/api/manga-downloads", { mediaId, title, provider, target: preferred, chapters: [{ id: chapter.id, chapter: chapter.chapter, title: chapter.title }] })
                    .then(() => qc.invalidateQueries({ queryKey: ["manga-downloads"] }))
                    .catch(e => toast.error(e.message))
            }
        >
            {job?.state === "failed" ? <RotateCw className="size-4 text-red-300" /> : <Download className="size-4" />}
        </button>
    )
}

function progressText(j: MangaDownload) {
    if (j.state === "queued") return "Waiting"
    if (j.state === "sending") return `To ${j.targetName || "the server"} ${j.got}%`
    return j.pages ? `${j.got}/${j.pages}` : "Starting"
}

// Downloading many chapters at once: which, and where to.
export function DownloadDialog({
    open,
    onOpenChange,
    mediaId,
    title,
    provider,
    chapters,
    progress,
    local,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    mediaId: number
    title: string
    provider: string
    chapters: MangaChapter[]
    progress: number
    local: LocalMangaChapter[]
}) {
    const qc = useQueryClient()
    const { servers, preferred } = useTargets()
    const [which, setWhich] = useState<"unread" | "all" | "range">("unread")
    const [from, setFrom] = useState("")
    const [to, setTo] = useState("")
    const [target, setTarget] = useState<string | null>(null)
    const [busy, setBusy] = useState(false)
    const where = target ?? preferred
    const have = new Set(local.map(c => c.chapter))
    const num = (c: MangaChapter) => parseFloat(c.chapter)
    const lo = parseFloat(from)
    const hi = parseFloat(to)
    const picked = chapters.filter(c => {
        if (where === "" && have.has(c.chapter)) return false
        if (which === "unread") return num(c) > progress
        if (which === "range") return (isNaN(lo) || num(c) >= lo) && (isNaN(hi) || num(c) <= hi)
        return true
    })
    const unread = chapters.filter(c => num(c) > progress).length
    const go = async () => {
        setBusy(true)
        try {
            const r = await api.post<{ added: number }>("/api/manga-downloads", {
                mediaId,
                title,
                provider,
                target: where,
                chapters: picked.map(c => ({ id: c.id, chapter: c.chapter, title: c.title })),
            })
            const place = where ? (servers.find(s => s.id === where)?.name ?? "the server") : "this device"
            toast.success(r.added ? `Downloading ${r.added} ${r.added === 1 ? "chapter" : "chapters"} to ${place}` : "Those chapters are there already")
            qc.invalidateQueries({ queryKey: ["manga-downloads"] })
            onOpenChange(false)
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Download chapters"
            description="Each chapter is saved as a comic book file (.cbz) with its details from AniList, readable here without the provider, and in other comic readers."
            footer={
                <>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button variant="primary" icon={<Download className="size-4" />} loading={busy} disabled={picked.length === 0} onClick={go}>
                        Download {picked.length} {picked.length === 1 ? "chapter" : "chapters"}
                    </Button>
                </>
            }
        >
            <div className="flex flex-col gap-4">
                <div className="flex flex-col gap-2">
                    {(
                        [
                            ["unread", `Unread chapters (${unread})`],
                            ["all", `All chapters (${chapters.length})`],
                            ["range", "Chapters from… to…"],
                        ] as const
                    ).map(([v, label]) => (
                        <label key={v} className="flex cursor-pointer items-center gap-2.5 text-sm">
                            <input type="radio" name="which" checked={which === v} onChange={() => setWhich(v)} className="accent-[var(--brand)]" />
                            {label}
                        </label>
                    ))}
                    {which === "range" && (
                        <div className="ml-6 flex items-center gap-2">
                            <Input className="w-24" inputMode="decimal" placeholder="From" value={from} onChange={e => setFrom(e.target.value)} />
                            <span className="text-muted">to</span>
                            <Input className="w-24" inputMode="decimal" placeholder="To" value={to} onChange={e => setTo(e.target.value)} />
                        </div>
                    )}
                </div>
                <div className="flex flex-col gap-1.5">
                    <span className="text-sm text-muted">Save on</span>
                    <Select
                        value={where}
                        onChange={setTarget}
                        options={[
                            ...(androidApp && servers.length > 0 ? [] : [{ value: "", label: "This device" }]),
                            ...servers.map(s => ({ value: s.id!, label: `${s.name}${s.online ? "" : " (offline)"}` })),
                        ]}
                    />
                    {where !== "" && <p className="text-xs text-subtle">Sent to the server's manga folder once each chapter is downloaded; its dashboard lists them under Library › Manga.</p>}
                </div>
            </div>
        </Dialog>
    )
}

// This manga's downloads that are on their way or failed.
export function DownloadsPanel({ mediaId }: { mediaId: number }) {
    const qc = useQueryClient()
    const { data } = useMangaDownloads()
    const jobs = (data ?? []).filter(j => j.mediaId === mediaId && j.state !== "done")
    if (jobs.length === 0) return null
    const remove = (id: string) =>
        api
            .del(`/api/manga-downloads/${id}`)
            .then(() => qc.invalidateQueries({ queryKey: ["manga-downloads"] }))
            .catch(e => toast.error(e.message))
    const retry = (id: string) =>
        api
            .post(`/api/manga-downloads/${id}/retry`)
            .then(() => qc.invalidateQueries({ queryKey: ["manga-downloads"] }))
            .catch(e => toast.error(e.message))
    return (
        <div className="card flex flex-col gap-2 p-4">
            <p className="text-sm font-medium">Downloading</p>
            <ul className="flex flex-col gap-1.5 text-sm">
                {jobs.map(j => (
                    <li key={j.id} className="flex items-center gap-3">
                        <span className="w-24 shrink-0 text-muted">Ch {j.chapter}</span>
                        <span className={cn("min-w-0 flex-1 truncate", j.state === "failed" && "text-red-300")}>
                            {j.state === "failed" ? `Didn't download: ${j.error}` : progressText(j)}
                            {j.targetName && j.state !== "failed" && j.state !== "sending" && <span className="text-subtle"> · for {j.targetName}</span>}
                        </span>
                        {j.state === "failed" && (
                            <button className="text-muted hover:text-fg" aria-label="Try again" onClick={() => retry(j.id)}>
                                <RotateCw className="size-4" />
                            </button>
                        )}
                        <button className="text-muted hover:text-fg" aria-label={j.state === "failed" ? "Forget it" : "Cancel"} onClick={() => remove(j.id)}>
                            <X className="size-4" />
                        </button>
                    </li>
                ))}
            </ul>
        </div>
    )
}

// What's downloaded of a manga, when the provider lists nothing (offline,
// or none installed): read from here.
export function LocalChapters({ chapters, progress, read, onDelete }: { chapters: LocalMangaChapter[]; progress: number; read: (c: LocalMangaChapter) => void; onDelete?: (c: LocalMangaChapter) => void }) {
    return (
        <div className="card divide-y divide-line">
            {[...chapters].reverse().map(c => {
                const done = parseFloat(c.chapter) <= progress
                return (
                    <div key={c.file} className={cn("flex items-center", done && "opacity-50")}>
                        <button onClick={() => read(c)} className="flex min-w-0 flex-1 items-center justify-between gap-4 px-4 py-3 text-left transition hover:bg-white/[0.03]">
                            <div className="min-w-0">
                                <p className="truncate font-medium">{c.title || `Chapter ${c.chapter}`}</p>
                                <p className="text-xs text-subtle">
                                    {c.pages} pages · {(c.size / 1048576).toFixed(1)} MB
                                </p>
                            </div>
                            {done ? <Badge tone="green">Read</Badge> : <Badge>Ch {c.chapter}</Badge>}
                        </button>
                        {onDelete && (
                            <button className="grid size-10 shrink-0 place-items-center text-muted hover:text-red-300" aria-label={`Delete chapter ${c.chapter}`} onClick={() => onDelete(c)}>
                                <Trash2 className="size-4" />
                            </button>
                        )}
                    </div>
                )
            })}
        </div>
    )
}

// What the Anikos and servers this one uses have downloaded (GET
// /api/manga-shared…): each with its series, or one's chapters.
export type SharedManga = { host: { id: string; name: string; online: boolean }; series?: MangaSeries[]; chapters?: LocalMangaChapter[] }

export function useSharedManga(id = 0) {
    return useQuery({
        queryKey: ["manga-library", "shared", id],
        queryFn: () => api.get<SharedManga[]>(`/api/manga-shared${id > 0 ? `/series/${id}` : ""}`),
        staleTime: 30_000,
    })
}

function SeriesCard({ s, cover }: { s: MangaSeries; cover: string }) {
    return (
        <Link to={`/manga/entry?id=${s.anilistId}`} data-tv-card className="group flex flex-col gap-2 focus-ring rounded-lg">
            <div data-tv-ring className="aspect-[2/3] overflow-hidden rounded-lg bg-surface-2">
                {s.hasCover ? (
                    <img src={cover} alt="" loading="lazy" className="size-full object-cover transition group-hover:scale-[1.03]" />
                ) : (
                    <div className="grid size-full place-items-center text-muted">
                        <BookOpen className="size-8" />
                    </div>
                )}
            </div>
            <div className="min-w-0">
                <p className="truncate text-sm font-medium">{s.title}</p>
                <p className="text-xs text-muted">
                    {s.count} {s.count === 1 ? "chapter" : "chapters"}
                    {s.latest && ` · up to ${s.latest}`}
                </p>
            </div>
        </Link>
    )
}

// The downloaded manga, as covers: on this device, then on each Aniko or
// server this one uses (read from there).
export function DownloadedGrid() {
    const { data, isLoading } = useQuery({
        queryKey: ["manga-library"],
        queryFn: () => api.get<{ dir: string; series: MangaSeries[] }>("/api/manga-library"),
    })
    const { data: shared, isLoading: sharedLoading } = useSharedManga()
    if (isLoading) return null
    const series = data?.series ?? []
    const others = (shared ?? []).filter(o => (o.series?.length ?? 0) > 0)
    const grid = "grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-5"
    return (
        <div className="flex flex-col gap-8">
            {series.length === 0 && others.length === 0 ? (
                <EmptyState icon={<Download className="size-6" />} title={sharedLoading ? "Looking for downloaded manga…" : "No downloaded manga"}>
                    Download chapters from a manga's page (the Download button) to read them without the provider. They're kept in {data?.dir ?? "your manga folder"}, or on your
                    server when you pick it there.
                </EmptyState>
            ) : (
                series.length > 0 && (
                    <section>
                        <p className="mb-4 text-sm text-muted">On this device · {data?.dir}</p>
                        <div className={grid}>
                            {series.map(s => (
                                <SeriesCard key={s.anilistId} s={s} cover={`/api/manga-library/${s.anilistId}/cover`} />
                            ))}
                        </div>
                    </section>
                )
            )}
            {others.map(o => (
                <section key={o.host.id}>
                    <p className="mb-4 flex items-center gap-2 text-sm text-muted">
                        <HardDrive className="size-4" /> On {o.host.name}
                    </p>
                    <div className={grid}>
                        {o.series!.map(s => (
                            <SeriesCard key={s.anilistId} s={s} cover={`/api/manga-shared/cover?host=${encodeURIComponent(o.host.id)}&id=${s.anilistId}`} />
                        ))}
                    </div>
                </section>
            ))}
        </div>
    )
}


