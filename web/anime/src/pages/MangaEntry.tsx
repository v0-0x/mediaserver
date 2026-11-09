import { useQuery, useQueryClient } from "@tanstack/react-query"
import { BookOpen, Download, RefreshCw, Search, Wand2 } from "lucide-react"
import { useState } from "react"
import { Link, useNavigate, useSearchParams } from "react-router-dom"
import { toast } from "@/lib/toast"
import { ListStatusButton, ProgressEditor, ScoreEditor } from "@/components/entry/ListEditor"
import { ChapterDownload, DownloadDialog, DownloadsPanel, LocalChapters, useMangaDownloads, useMangaLocal, useSharedManga } from "@/components/MangaDownloads"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Badge, Button, Dialog, EmptyState, ErrorState, Input, Select, Skeleton } from "@/components/ui"
import { api, qs } from "@/lib/api"
import { useMangaChapters, useStatus } from "@/lib/queries"
import type { LocalMangaChapter, MangaChapter, MangaPosition, Media } from "@/lib/types"
import { banner, cleanDescription, cn, cover, formatLabel, statusLabel, title } from "@/lib/utils"

export default function MangaEntryPage() {
    const [params] = useSearchParams()
    const id = Number(params.get("id"))
    const { data: status } = useStatus()
    const { data: media, isLoading, error: mediaError, refetch: refetchMedia, isFetching: mediaFetching } = useQuery({ queryKey: ["manga", "media", id], queryFn: () => api.get<Media>(`/api/manga/${id}`), enabled: id > 0 })
    const { data: providers } = useQuery({ queryKey: ["manga-providers"], queryFn: () => api.get<{ id: string; name: string }[]>("/api/manga/providers") })
    const [provider, setProvider] = useState<string>(() => localStorage.getItem(`aniko-manga-provider-${id}`) || status?.settings.manga.defaultProvider || "")
    const effective = provider || providers?.[0]?.id || ""
    const { data: ch, isLoading: chLoading, error, refetch, isFetching } = useMangaChapters(id, effective)
    const { data: position } = useQuery({
        queryKey: ["manga", "position", id],
        queryFn: () => api.get<MangaPosition | null>(`/api/manga/${id}/position`),
        enabled: id > 0,
    })
    const [matchOpen, setMatchOpen] = useState(false)
    const [downloadOpen, setDownloadOpen] = useState(false)
    // Downloaded chapters (read from here, also offline), and those on
    // their way.
    const { data: local } = useMangaLocal(id)
    // And on the Anikos and servers this one uses.
    const { data: onServers } = useSharedManga(id)
    const { data: downloads } = useMangaDownloads()
    const navigate = useNavigate()
    const qc = useQueryClient()

    if (!(id > 0))
        return (
            <div className="p-10">
                <EmptyState
                    icon={<BookOpen className="size-6" />}
                    title="No manga selected"
                    action={
                        <Link to="/manga">
                            <Button>Go to Manga</Button>
                        </Link>
                    }
                >
                    This link doesn't point to a manga.
                </EmptyState>
            </div>
        )
    if (mediaError && !media)
        return (
            <div className="p-10">
                <ErrorState title="Couldn't load this manga" error={mediaError} retrying={mediaFetching} onRetry={() => refetchMedia()} />
            </div>
        )
    if (isLoading || !media) return <Skeleton className="m-10 h-96" />
    const progress = media.mediaListEntry?.progress ?? 0
    const chapters = ch?.chapters ?? []
    // Where reading stopped (the reader saves it): that chapter at that page,
    // or the next chapter once it was read to the end. Continue goes there
    // unless the list's progress is further.
    // (A downloaded chapter read here is saved as the "local" provider's: by
    // its number.)
    const stoppedAt = !position
        ? -1
        : position.provider === effective
          ? chapters.findIndex(c => c.id === position.chapterId)
          : position.provider === "local"
            ? chapters.findIndex(c => c.chapter === position.chapter)
            : -1
    const finished = !!position && position.pages > 0 && position.page >= position.pages - 1
    const stopped = stoppedAt < 0 ? undefined : chapters[finished ? stoppedAt + 1 : stoppedAt]
    const resume = stopped && parseFloat(stopped.chapter) > progress ? stopped : undefined
    const resumePage = resume && !finished ? position!.page : 0
    const next = resume ?? chapters.find(c => parseFloat(c.chapter) > progress) ?? chapters[0]
    const localChapters = local?.chapters ?? []
    const localByNumber = new Map(localChapters.map(c => [c.chapter, c]))
    const jobByNumber = new Map((downloads ?? []).filter(j => j.mediaId === id && j.state !== "done").map(j => [j.chapter, j]))
    const readLocal = (c: LocalMangaChapter, page?: number) => navigate(`/manga/read${qs({ id, provider: "local", chapter: c.file, page: page ? String(page) : undefined })}`)
    // A downloaded chapter reads from here.
    const read = (c: MangaChapter, page?: number) => {
        const l = localByNumber.get(c.chapter)
        if (l) return readLocal(l, page)
        navigate(`/manga/read${qs({ id, provider: effective, chapter: c.id, page: page ? String(page) : undefined })}`)
    }
    const deleteLocal = (c: LocalMangaChapter) =>
        api
            .del(`/api/manga-library/${id}${qs({ file: c.file })}`)
            .then(() => qc.invalidateQueries({ queryKey: ["manga-library"] }))
            .catch(e => toast.error(e.message))

    return (
        <div className="min-h-full pb-24">
            <div className="relative">
                <div className="absolute inset-x-0 top-0 h-[440px] overflow-hidden">
                    {banner(media) && <img src={banner(media)} alt="" className="size-full object-cover opacity-60 fade-in [mask-image:linear-gradient(to_bottom,#000_80%,transparent)]" />}
                    <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/70 to-bg/10" />
                </div>
                <div className="relative z-10 flex flex-col gap-8 px-6 pt-[180px] pb-10 md:flex-row md:px-8 xl:px-10">
                    <img src={cover(media, "poster")} alt="" className="aspect-[2/3] w-40 shrink-0 rounded-lg object-cover shadow-2xl shadow-black/60 ring-1 ring-white/10 md:w-52" />
                    <div className="flex min-w-0 flex-1 flex-col justify-end gap-4">
                        <h1 className="text-3xl leading-[1.1] font-semibold tracking-tight md:text-[2.75rem]">{title(media)}</h1>
                        <div className="flex flex-wrap items-center gap-3 text-sm text-white/80">
                            <span>{formatLabel(media.format)}</span>
                            {media.chapters && <span>{media.chapters} chapters</span>}
                            <Badge>{statusLabel(media.status)}</Badge>
                            {media.genres?.slice(0, 4).map(g => (
                                <span key={g} className="text-white/60">
                                    {g}
                                </span>
                            ))}
                        </div>
                        <p className="line-clamp-3 max-w-4xl text-white/70">{cleanDescription(media.description)}</p>
                        <div className="flex flex-wrap gap-2.5">
                            {next && (
                                <Button variant="white" size="lg" icon={<BookOpen className="size-5" />} onClick={() => read(next, resumePage)}>
                                    {progress || resume ? "Continue" : "Start"} · Ch {next.chapter}
                                    {resumePage > 0 && ` · p. ${resumePage + 1}`}
                                </Button>
                            )}
                            <ListStatusButton media={media} entry={media.mediaListEntry} />
                            {media.mediaListEntry && <ProgressEditor media={media} entry={media.mediaListEntry} />}
                            <ScoreEditor media={media} entry={media.mediaListEntry} />
                        </div>
                    </div>
                </div>
            </div>
            <div className="flex flex-col gap-6 px-6 md:px-8 xl:px-10">
                <div className="flex flex-wrap items-center gap-3">
                    <Select
                        className="w-60"
                        value={effective}
                        onChange={v => {
                            setProvider(v)
                            localStorage.setItem(`aniko-manga-provider-${id}`, v)
                        }}
                        options={(providers ?? []).map(p => ({ value: p.id, label: p.name }))}
                    />
                    <Button variant="subtle" icon={<RefreshCw className={cn("size-4", isFetching && "animate-spin")} />} onClick={() => api.get(`/api/manga/${id}/chapters${qs({ provider: effective, refresh: 1 })}`).then(() => qc.invalidateQueries({ queryKey: ["manga", "chapters", id] })).catch(e => toast.error(e.message))}>
                        Refresh
                    </Button>
                    <Button variant="subtle" icon={<Wand2 className="size-4" />} onClick={() => setMatchOpen(true)} disabled={!effective}>
                        Change match
                    </Button>
                    <Button variant="subtle" icon={<Download className="size-4" />} onClick={() => setDownloadOpen(true)} disabled={chapters.length === 0}>
                        Download
                    </Button>
                    {localChapters.length > 0 && (
                        <span className="text-sm text-muted">
                            {localChapters.length} downloaded
                        </span>
                    )}
                    {ch?.mapping && (
                        <span className="text-sm text-muted">
                            Matched <span className="font-semibold text-fg">{ch.mapping.title}</span>
                        </span>
                    )}
                </div>
                <DownloadsPanel mediaId={id} />
                {!effective && localChapters.length === 0 && !(onServers ?? []).length && (
                    <EmptyState title="No manga provider installed" action={<Link to="/extensions?tab=marketplace"><Button variant="primary">Open marketplace</Button></Link>}>
                        Install a manga provider extension to read chapters.
                    </EmptyState>
                )}
                {chLoading && effective && <Skeleton className="h-64" />}
                {error && localChapters.length === 0 && !(onServers ?? []).length && <EmptyState title="Couldn't load chapters" action={<Button onClick={() => refetch()}>Retry</Button>}>{(error as Error).message}</EmptyState>}
                {/* The provider lists nothing (offline, none installed): what's downloaded. */}
                {chapters.length === 0 && !chLoading && localChapters.length > 0 && (
                    <>
                        <p className="text-sm text-muted">Downloaded chapters, read from this device:</p>
                        <LocalChapters chapters={localChapters} progress={progress} read={c => readLocal(c)} onDelete={deleteLocal} />
                    </>
                )}
                {(onServers ?? []).map(o => (
                    <details key={o.host.id} open={chapters.length === 0} className="group">
                        <summary className="mb-2 cursor-pointer list-none text-sm text-muted hover:text-fg">
                            {o.chapters!.length} {o.chapters!.length === 1 ? "chapter" : "chapters"} on {o.host.name}, read from there
                            <span className="ml-1 text-subtle group-open:hidden">· show</span>
                        </summary>
                        <LocalChapters
                            chapters={o.chapters!}
                            progress={progress}
                            read={c => navigate(`/manga/read${qs({ id, provider: `server:${o.host.id}`, chapter: c.file })}`)}
                        />
                    </details>
                ))}
                {chapters.length > 0 && (
                    <div className="card divide-y divide-line">
                        {[...chapters].reverse().map(c => {
                            const done = parseFloat(c.chapter) <= progress
                            return (
                                <div key={c.id} className={cn("flex items-center pr-1", done && "opacity-50")}>
                                    <button onClick={() => read(c)} className="flex min-w-0 flex-1 items-center justify-between gap-4 px-4 py-3 text-left transition hover:bg-white/[0.03]">
                                        <div className="min-w-0">
                                            <p className="truncate font-medium">{c.title || `Chapter ${c.chapter}`}</p>
                                            <p className="text-xs text-subtle">{[c.scanlator, c.language?.toUpperCase(), c.updatedAt].filter(Boolean).join(" · ")}</p>
                                        </div>
                                        {done ? <Badge tone="green">Read</Badge> : <Badge>Ch {c.chapter}</Badge>}
                                    </button>
                                    <ChapterDownload mediaId={id} title={title(media)} provider={effective} chapter={c} local={localByNumber.get(c.chapter)} job={jobByNumber.get(c.chapter)} />
                                </div>
                            )
                        })}
                    </div>
                )}
                <PluginSlot slot="manga-entry-screen-bottom" />
            </div>
            <DownloadDialog
                open={downloadOpen}
                onOpenChange={setDownloadOpen}
                mediaId={id}
                title={title(media)}
                provider={effective}
                chapters={chapters}
                progress={progress}
                local={localChapters}
            />
            <MangaMatchDialog open={matchOpen} onOpenChange={setMatchOpen} provider={effective} mediaId={id} defaultQuery={media.title.english || media.title.romaji || ""} />
        </div>
    )
}

function MangaMatchDialog({ open, onOpenChange, provider, mediaId, defaultQuery }: { open: boolean; onOpenChange: (v: boolean) => void; provider: string; mediaId: number; defaultQuery: string }) {
    const [q, setQ] = useState(defaultQuery)
    const [res, setRes] = useState<{ id: string; title: string; image?: string; year?: number }[]>([])
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    const search = async () => {
        setBusy(true)
        try {
            setRes(await api.get(`/api/manga/search${qs({ provider, q })}`))
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title="Pick the right manga">
            <div className="flex gap-2">
                <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => e.key === "Enter" && search()} icon={<Search className="size-4" />} />
                <Button variant="primary" loading={busy} onClick={search}>
                    Search
                </Button>
            </div>
            <div className="mt-4 flex flex-col gap-1">
                {res.map(r => (
                    <button
                        key={r.id}
                        onClick={async () => {
                            try {
                                await api.post("/api/manga/mapping", { provider, mediaId, id: r.id, title: r.title })
                                qc.invalidateQueries({ queryKey: ["manga", "chapters", mediaId] })
                                onOpenChange(false)
                            } catch (e: any) {
                                toast.error(e.message)
                            }
                        }}
                        className="flex items-center gap-3 rounded-xl p-2 text-left hover:bg-white/[0.06]"
                    >
                        {r.image && <img src={r.image} alt="" className="h-14 w-10 rounded object-cover" referrerPolicy="no-referrer" />}
                        <span className="font-medium">{r.title}</span>
                        {r.year ? <span className="text-sm text-subtle">{r.year}</span> : null}
                    </button>
                ))}
            </div>
        </Dialog>
    )
}
