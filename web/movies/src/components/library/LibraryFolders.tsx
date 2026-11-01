import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ChevronDown, EyeOff, Folder, FolderOpen, Link2, Link2Off, Search } from "lucide-react"
import { useMemo, useRef, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { fileLabel } from "@/lib/files"
import { mediaRef } from "@/lib/ids"
import { useCollection, useLibraryFiles, useLibraryFolders, useStatus } from "@/lib/queries"
import type { FolderInfo, LocalFile, Media } from "@/lib/types"
import { cn, formatBytes, plural, poster, title } from "@/lib/utils"
import { Badge, Button, EmptyState, IconButton, Input, Tooltip } from "../ui"
import { cardRow, VirtualList } from "../VirtualList"
import { MatchDialog, naturalCompare } from "./MatchDialog"

export type LibraryFolder = {
    dir: string
    label: string // path below its library folder
    files: LocalFile[]
    size: number
    matches: { mediaId: number; count: number }[] // most files first
    unmatched: number
    query: string // best guess of the title, for searching TMDB
    notIndexed: number // videos on disk that no scan has added yet
    problem?: string // why it has nothing to match (still downloading, unreadable…)
    // Several titles share the folder (a flat "Movies" folder): matching or
    // unmatching it as a whole would give every film the same title, so
    // its files are matched by selection instead.
    several: boolean
}

// Whether a folder has files to match (once its new videos are added).
const usable = (f: LibraryFolder) => f.files.length > 0 || f.notIndexed > 0

// "[Group] Title [1080p]" → "Title"
function titleFromFolder(label: string) {
    const name = label.split(/[\\/]/).pop() ?? label
    return (
        name
            .replace(/[[({][^\])}]*[\])}]/g, " ")
            .replace(/[._]+/g, " ")
            .replace(/\s+/g, " ")
            .trim() || name
    )
}

// The folders of the index, plus the ones found on disk that the index
// doesn't have (not scanned yet, or nothing in them can be used).
function buildFolders(files: LocalFile[], roots: string[], disk: FolderInfo[]): LibraryFolder[] {
    const byDir = new Map<string, LocalFile[]>()
    for (const f of files) {
        if (f.ignored) continue
        const list = byDir.get(f.dir)
        if (list) list.push(f)
        else byDir.set(f.dir, [f])
    }
    const onDisk = new Map(disk.map(d => [d.dir, d]))
    const out: LibraryFolder[] = []
    for (const dir of new Set([...byDir.keys(), ...onDisk.keys()])) {
        const fs = byDir.get(dir) ?? []
        const d = onDisk.get(dir)
        // Windows paths use backslashes.
        const root = roots.filter(r => dir === r || dir.startsWith(r + "/") || dir.startsWith(r + "\\")).sort((a, b) => b.length - a.length)[0]
        const label = d?.label ?? (root ? dir.slice(root.length).replace(/^[\\/]+/, "") || dir.split(/[\\/]/).pop() || dir : dir)
        const counts = new Map<number, number>()
        let unmatched = 0
        for (const f of fs) {
            if (f.mediaId > 0) counts.set(f.mediaId, (counts.get(f.mediaId) ?? 0) + 1)
            else unmatched++
        }
        fs.sort((a, b) => naturalCompare(a.name, b.name))
        // Matched files count by their title, unmatched ones by the title
        // read from their name (a show's files share the show folder's).
        const titles = new Set(
            fs.filter(f => f.kind !== "extra").map(f => (f.mediaId > 0 ? `#${f.mediaId}` : (f.parsed?.folderTitle || f.parsed?.title || "").toLowerCase())),
        )
        out.push({
            dir,
            label,
            files: fs,
            size: fs.reduce((n, f) => n + f.size, 0),
            matches: [...counts].map(([mediaId, count]) => ({ mediaId, count })).sort((a, b) => b.count - a.count),
            unmatched,
            query: fs.find(f => f.parsed?.folderTitle)?.parsed.folderTitle || fs[0]?.parsed?.title || titleFromFolder(label),
            notIndexed: d?.notIndexed ?? 0,
            problem: d?.problem,
            several: titles.size > 1,
        })
    }
    return out
}

function words(s: string) {
    return s
        .toLowerCase()
        .replace(/[^\p{L}\p{N}]+/gu, " ")
        .split(" ")
        .filter(w => w.length > 1)
}

// How much a folder looks like a title (0-1), to list likely folders first.
function likeness(folder: LibraryFolder, media: Media) {
    const fw = new Set(words(`${folder.label} ${folder.query}`))
    let best = 0
    for (const t of [media.title.romaji, media.title.english, media.title.userPreferred, ...(media.synonyms ?? [])]) {
        const tw = words(t ?? "")
        if (!tw.length) continue
        best = Math.max(best, tw.filter(w => fw.has(w)).length / tw.length)
    }
    return best
}

function useMediaIndex() {
    const { data: coll } = useCollection()
    return useMemo(() => {
        const m = new Map<number, Media>()
        for (const l of coll?.lists ?? []) for (const i of l.items) m.set(i.media.id, i.media)
        for (const i of coll?.localOnly ?? []) m.set(i.media.id, i.media)
        return m
    }, [coll])
}

function useRoots() {
    const { data: status } = useStatus()
    return useMemo(() => (status?.settings.library.off ? [] : [status?.settings.library.dir ?? "", ...(status?.settings.library.extraDirs ?? [])].filter(Boolean)), [status])
}

// Adds the videos of folders that no scan has added yet to the index, and
// returns every file of those folders.
function useIndexFolders() {
    const qc = useQueryClient()
    return async (folders: LibraryFolder[]) => {
        const dirs = folders.filter(f => f.notIndexed > 0).map(f => f.dir)
        const known = folders.flatMap(f => f.files)
        if (dirs.length === 0) return known
        const added = (await api.post<LocalFile[] | null>("/api/library/index", { dirs })) ?? []
        qc.invalidateQueries({ queryKey: ["library"] })
        const seen = new Set(known.map(f => f.path))
        return [...known, ...added.filter(f => !seen.has(f.path))]
    }
}

// A row of the folder list: a folder, the bar of its selected files, one of
// its files (when it's open), the heading of the folders with nothing to
// match, or one of those. Folders and their files are rows of their own so
// that a folder of thousands of files only renders those on screen.
type FolderListRow =
    | { kind: "folder"; f: LibraryFolder; open: boolean }
    | { kind: "selection"; f: LibraryFolder; selected: LocalFile[] }
    | { kind: "file"; f: LibraryFolder; x: LocalFile; first: boolean; last: boolean }
    | { kind: "heading" }
    | { kind: "problem"; f: LibraryFolder }

const rowKey = (r: FolderListRow) =>
    r.kind === "heading" ? "heading" : r.kind === "file" ? `file:${r.x.path}` : `${r.kind}:${r.f.dir}`

// Library tools › Folders: every folder with what it's matched to, and
// buttons to match it (or some of its files) to another movie or show by hand.
export function LibraryFolders() {
    const { data: status } = useStatus()
    const { data: files, isLoading } = useLibraryFiles()
    const { data: disk, error: diskError } = useLibraryFolders()
    const media = useMediaIndex()
    const roots = useRoots()
    const qc = useQueryClient()
    const indexFolders = useIndexFolders()
    const [q, setQ] = useState("")
    const [matching, setMatching] = useState<{ files: LocalFile[]; query: string } | null>(null)
    const [preparing, setPreparing] = useState<string | null>(null)
    // The folders showing their files, and the files picked (by path), for
    // every folder: a row scrolled far away isn't rendered.
    const [openDirs, setOpenDirs] = useState<Set<string>>(() => new Set())
    const [selected, setSelected] = useState<Set<string>>(() => new Set())
    const toggle = <K,>(set: (fn: (s: Set<K>) => Set<K>) => void, k: K, on?: boolean) =>
        set(s => {
            const n = new Set(s)
            if (on ?? !n.has(k)) n.add(k)
            else n.delete(k)
            return n
        })
    const trusted = status?.client !== "lan"

    const folders = useMemo(() => {
        const all = buildFolders(files ?? [], roots, disk ?? [])
        const needle = q.trim().toLowerCase()
        const shown = needle
            ? all.filter(f => [f.label, f.query, ...f.matches.map(m => title(media.get(m.mediaId)))].some(t => t?.toLowerCase().includes(needle)))
            : all
        // Unmatched and mixed folders first: they're the ones to fix.
        const rank = (f: LibraryFolder) => (f.matches.length === 0 ? 0 : f.matches.length > 1 || f.unmatched > 0 || f.notIndexed > 0 ? 1 : 2)
        return shown.sort((a, b) => rank(a) - rank(b) || naturalCompare(a.label, b.label))
    }, [files, disk, roots, q, media])
    const rows = useMemo(() => {
        const out: FolderListRow[] = []
        for (const f of folders.filter(usable)) {
            const open = openDirs.has(f.dir) && f.files.length > 0
            out.push({ kind: "folder", f, open })
            if (!open) continue
            const sel = f.files.filter(x => selected.has(x.path))
            if (sel.length > 0) out.push({ kind: "selection", f, selected: sel })
            f.files.forEach((x, i) => out.push({ kind: "file", f, x, first: i === 0, last: i === f.files.length - 1 }))
        }
        const unusable = folders.filter(f => !usable(f))
        if (unusable.length > 0) out.push({ kind: "heading" }, ...unusable.map(f => ({ kind: "problem" as const, f })))
        return out
    }, [folders, openDirs, selected])

    const post = async (path: string, body: object, done: string) => {
        try {
            await api.post(path, body)
            toast.success(done)
            qc.invalidateQueries({ queryKey: ["library"] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }

    const matchFolder = async (f: LibraryFolder) => {
        setPreparing(f.dir)
        try {
            const fs = await indexFolders([f])
            if (fs.length === 0) return void toast.error("No video files left in this folder")
            setMatching({ files: fs, query: fs.find(x => x.parsed?.folderTitle)?.parsed.folderTitle || f.query })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setPreparing(null)
        }
    }

    if (isLoading) return <div className="card h-40 shimmer" />
    if (!files?.length && !disk?.length)
        return (
            <EmptyState icon={<Folder className="size-6" />} title="No files in your library yet">
                Set your library folder in Settings and run a scan.
            </EmptyState>
        )
    return (
        <div className="flex flex-col gap-4">
            <p className="text-sm text-muted">
                Wrong or missing match? Use <b>Match…</b> or <b>Change match…</b> to pick the right movie or show. Matches you pick are locked, so later scans keep them.
            </p>
            <div className="w-full max-w-md">
                <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter folders or titles…" icon={<Search className="size-4" />} />
            </div>
            {diskError && <p className="text-sm text-amber-300">Couldn't look through your library folders: {(diskError as Error).message}</p>}
            {folders.length === 0 && <p className="py-10 text-center text-sm text-muted">No folder matches “{q}”.</p>}
            <VirtualList
                items={rows}
                getKey={rowKey}
                estimateSize={r => (r.kind === "folder" || r.kind === "problem" ? 74 : r.kind === "heading" ? 64 : 37)}
                renderItem={(r, i) => {
                    // A card ends with its folder (closed) or its last file:
                    // the next card comes a little lower.
                    const next = rows[i + 1]
                    const ends = !next || next.kind === "folder" || next.kind === "heading" || next.kind === "problem"
                    return (
                        <div className={cn(ends && "pb-2")}>
                            {r.kind === "folder" ? (
                                <FolderRow
                                    folder={r.f}
                                    media={media}
                                    trusted={trusted}
                                    open={r.open}
                                    preparing={preparing === r.f.dir}
                                    onToggle={() => toggle(setOpenDirs, r.f.dir)}
                                    onOpenFiles={() => toggle(setOpenDirs, r.f.dir, true)}
                                    onMatchFolder={() => matchFolder(r.f)}
                                    onUnmatch={fs => post("/api/library/unmatch", { paths: fs.map(x => x.path) }, `Unmatched ${plural(fs.length, "file")}`)}
                                    onIgnore={fs => post("/api/library/ignore", { paths: fs.map(x => x.path), ignored: true }, `Ignoring ${plural(fs.length, "file")}`)}
                                />
                            ) : r.kind === "selection" ? (
                                <SelectionBar
                                    selected={r.selected}
                                    onMatch={() => setMatching({ files: r.selected, query: r.f.query })}
                                    onUnmatch={() => post("/api/library/unmatch", { paths: r.selected.map(x => x.path) }, `Unmatched ${plural(r.selected.length, "file")}`)}
                                    onIgnore={() => post("/api/library/ignore", { paths: r.selected.map(x => x.path), ignored: true }, `Ignoring ${plural(r.selected.length, "file")}`)}
                                />
                            ) : r.kind === "file" ? (
                                <FileRow file={r.x} media={media} first={r.first} last={r.last} checked={selected.has(r.x.path)} onCheck={() => toggle(setSelected, r.x.path)} />
                            ) : r.kind === "heading" ? (
                                <div className="pt-2">
                                    <h3 className="font-semibold">Nothing to match yet</h3>
                                    <p className="text-sm text-muted">KumoBox found these folders, but nothing in them it can play.</p>
                                </div>
                            ) : (
                                <ProblemRow folder={r.f} trusted={trusted} />
                            )}
                        </div>
                    )
                }}
            />
            <MatchDialog open={!!matching} onOpenChange={v => !v && setMatching(null)} files={matching?.files ?? []} initialQuery={matching?.query} />
        </div>
    )
}

function openFolder(dir: string) {
    api.post("/api/open", { path: dir }).catch(e => toast.error(e.message))
}

// A folder's row: what it's matched to and its buttons. Its files are rows
// of their own (FileRow) when it's open.
function FolderRow({
    folder: f,
    media,
    trusted,
    open,
    preparing,
    onToggle,
    onOpenFiles,
    onMatchFolder,
    onUnmatch,
    onIgnore,
}: {
    folder: LibraryFolder
    media: Map<number, Media>
    trusted: boolean
    open: boolean
    preparing: boolean
    onToggle: () => void
    onOpenFiles: () => void
    onMatchFolder: () => void
    onUnmatch: (files: LocalFile[]) => void
    onIgnore: (files: LocalFile[]) => void
}) {
    const primary = f.matches.length === 1 ? media.get(f.matches[0].mediaId) : undefined
    const hasFiles = f.files.length > 0

    return (
        <div className={cardRow(true, !open)}>
            <div className="flex flex-wrap items-center gap-3 p-3 pr-4">
                <button onClick={() => hasFiles && onToggle()} className={cn("flex min-w-0 flex-1 items-center gap-3 text-left", !hasFiles && "cursor-default")}>
                    <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.05] text-muted">
                        <Folder className="size-5" />
                    </span>
                    <span className="min-w-0">
                        <span className="block truncate font-semibold" title={f.dir}>
                            {f.label}
                        </span>
                        <span className="text-xs text-subtle">
                            {hasFiles ? `${plural(f.files.length, "file")} · ${formatBytes(f.size)}` : `${plural(f.notIndexed, "video")} · not scanned yet`}
                        </span>
                    </span>
                </button>

                <div className="flex min-w-0 items-center gap-2">
                    {f.matches.length === 0 ? (
                        <Badge tone="amber">Unmatched</Badge>
                    ) : primary || f.matches.length === 1 ? (
                        <Link to={`/entry?id=${f.matches[0].mediaId}`} className="flex min-w-0 items-center gap-2 rounded-lg px-1.5 py-1 hover:bg-white/[0.05]">
                            {primary && <img {...poster(primary, 24)} alt="" className="h-9 w-6 rounded object-cover" />}
                            <span className="max-w-64 truncate text-sm text-fg/85">{primary ? title(primary) : mediaRef(f.matches[0].mediaId)}</span>
                        </Link>
                    ) : (
                        <div className="flex flex-wrap gap-1.5">
                            {f.matches.map(m => (
                                <Link key={m.mediaId} to={`/entry?id=${m.mediaId}`}>
                                    <Badge tone="blue">
                                        {title(media.get(m.mediaId)) || mediaRef(m.mediaId)} · {m.count}
                                    </Badge>
                                </Link>
                            ))}
                        </div>
                    )}
                    {f.matches.length > 0 && f.unmatched > 0 && <Badge tone="amber">{f.unmatched} unmatched</Badge>}
                    {hasFiles && f.notIndexed > 0 && (
                        <Tooltip content="Added since the last scan. Match… includes them.">
                            <Badge tone="blue">{f.notIndexed} new</Badge>
                        </Tooltip>
                    )}
                </div>

                <div className="flex shrink-0 items-center gap-1.5">
                    {f.several ? (
                        <Tooltip content="Several titles are in this folder: pick the files of one title to match them.">
                            <Button size="sm" variant={f.unmatched > 0 ? "primary" : "subtle"} icon={<Link2 className="size-4" />} onClick={onOpenFiles}>
                                Match files…
                            </Button>
                        </Tooltip>
                    ) : (
                        <Button
                            size="sm"
                            variant={f.matches.length === 1 && f.unmatched === 0 && f.notIndexed === 0 ? "subtle" : "primary"}
                            icon={<Link2 className="size-4" />}
                            loading={preparing}
                            onClick={onMatchFolder}
                        >
                            {f.matches.length === 0 ? "Match…" : "Change match…"}
                        </Button>
                    )}
                    {f.matches.length > 0 && !f.several && (
                        <IconButton size="sm" label="Unmatch folder" onClick={() => onUnmatch(f.files)}>
                            <Link2Off className="size-4" />
                        </IconButton>
                    )}
                    {hasFiles && (
                        <IconButton size="sm" label="Ignore folder (never match or show it)" onClick={() => onIgnore(f.files)}>
                            <EyeOff className="size-4" />
                        </IconButton>
                    )}
                    {trusted && (
                        <IconButton size="sm" label="Open folder" onClick={() => openFolder(f.dir)}>
                            <FolderOpen className="size-4" />
                        </IconButton>
                    )}
                    {hasFiles && (
                        <IconButton size="sm" label={open ? "Hide files" : "Show files"} onClick={onToggle}>
                            <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
                        </IconButton>
                    )}
                </div>
            </div>
        </div>
    )
}

// What to do with the files picked in an open folder.
function SelectionBar({ selected, onMatch, onUnmatch, onIgnore }: { selected: LocalFile[]; onMatch: () => void; onUnmatch: () => void; onIgnore: () => void }) {
    return (
        <div className={cn(cardRow(false, false), "flex items-center gap-2 px-4 py-2")}>
            <span className="flex-1 text-xs text-muted">{selected.length} selected</span>
            <Button size="xs" variant="primary" icon={<Link2 className="size-3.5" />} onClick={onMatch}>
                Match selected…
            </Button>
            <Button size="xs" icon={<Link2Off className="size-3.5" />} onClick={onUnmatch}>
                Unmatch
            </Button>
            <Button size="xs" icon={<EyeOff className="size-3.5" />} onClick={onIgnore}>
                Ignore
            </Button>
        </div>
    )
}

// A file of an open folder, to pick.
function FileRow({ file: x, media, first, last, checked, onCheck }: { file: LocalFile; media: Map<number, Media>; first: boolean; last: boolean; checked: boolean; onCheck: () => void }) {
    const m = x.mediaId > 0 ? media.get(x.mediaId) : undefined
    return (
        <label className={cn(cardRow(false, last, first), "flex cursor-pointer items-center gap-3 px-4 py-2 text-sm transition hover:bg-white/[0.03]")}>
            <input type="checkbox" checked={checked} onChange={onCheck} className="size-4 accent-[var(--brand)]" />
            <span className="min-w-0 flex-1 truncate text-fg/85" title={x.path}>
                {x.name}
            </span>
            <span className="hidden max-w-56 truncate text-xs text-subtle sm:inline">{x.mediaId > 0 ? title(m) || mediaRef(x.mediaId) : "Unmatched"}</span>
            <Tooltip content={x.locked ? "Matched by hand — scans keep it" : "Matched automatically"}>
                <span className="w-24 shrink-0 text-right text-xs font-semibold tabular-nums">{x.mediaId > 0 ? fileLabel(x) : "—"}</span>
            </Tooltip>
        </label>
    )
}

// A folder with nothing to match, and why.
function ProblemRow({ folder: f, trusted }: { folder: LibraryFolder; trusted: boolean }) {
    return (
        <div className="card flex items-center gap-3 p-3 pr-4">
            <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-amber-500/10 text-amber-300">
                <AlertTriangle className="size-5" />
            </span>
            <span className="min-w-0 flex-1">
                <span className="block truncate font-semibold" title={f.dir}>
                    {f.label}
                </span>
                <span className="block text-xs text-amber-200/80">{f.problem ?? "Nothing in it can be played"}</span>
            </span>
            {trusted && (
                <IconButton size="sm" label="Open folder" onClick={() => openFolder(f.dir)}>
                    <FolderOpen className="size-4" />
                </IconButton>
            )}
        </div>
    )
}

// Picks library folders (or files) to match to a given movie or show,
// likeliest first. Used from its page when its files weren't found. Folders a
// scan hasn't reached yet are added when picked; ones with nothing to match
// say why.
export function FolderPicker({ target, onPicked }: { target: Media; onPicked: (files: LocalFile[]) => void }) {
    const { data: files, isLoading } = useLibraryFiles()
    const { data: disk, isLoading: diskLoading } = useLibraryFolders()
    const media = useMediaIndex()
    const roots = useRoots()
    const indexFolders = useIndexFolders()
    const [q, setQ] = useState("")
    const [sel, setSel] = useState<Set<string>>(new Set())
    const [busy, setBusy] = useState(false)
    const scroller = useRef<HTMLDivElement>(null)
    const folders = useMemo(() => {
        const all = buildFolders(files ?? [], roots, disk ?? []).filter(
            f => !(f.matches.length === 1 && f.matches[0].mediaId === target.id && f.unmatched === 0 && f.notIndexed === 0),
        )
        const needle = q.trim().toLowerCase()
        const shown = needle ? all.filter(f => [f.label, f.query].some(t => t.toLowerCase().includes(needle))) : all
        return shown
            .map(f => ({ f, ok: usable(f), score: likeness(f, target) }))
            .sort((a, b) => Number(b.ok) - Number(a.ok) || b.score - a.score || naturalCompare(a.f.label, b.f.label))
            .map(x => x.f)
    }, [files, disk, roots, q, target])
    const chosen = folders.filter(f => sel.has(f.dir) && usable(f))
    const count = chosen.reduce((n, f) => n + f.files.length + f.notIndexed, 0)

    const next = async () => {
        setBusy(true)
        try {
            const picked = await indexFolders(chosen)
            if (picked.length === 0) return void toast.error("No video files found in those folders")
            onPicked(picked)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }

    if (isLoading || (diskLoading && !files?.length)) return <div className="h-40 shimmer rounded-xl" />
    return (
        <div className="flex flex-col gap-3">
            <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter folders…" icon={<Search className="size-4" />} />
            <div ref={scroller} className="max-h-[50vh] overflow-y-auto rounded-xl border border-line">
                <VirtualList
                    items={folders}
                    getKey={f => f.dir}
                    estimateSize={58}
                    scrollerRef={scroller}
                    renderItem={(f, i) => {
                        const ok = usable(f)
                        return (
                            <label
                                className={cn(
                                    "flex items-center gap-3 px-3 py-2.5 text-sm",
                                    i < folders.length - 1 && "border-b border-line",
                                    ok ? "cursor-pointer hover:bg-white/[0.03]" : "cursor-default",
                                )}
                            >
                                <input
                                    type="checkbox"
                                    disabled={!ok}
                                    checked={ok && sel.has(f.dir)}
                                    onChange={() =>
                                        setSel(s => {
                                            const n = new Set(s)
                                            n.has(f.dir) ? n.delete(f.dir) : n.add(f.dir)
                                            return n
                                        })
                                    }
                                    className="size-4 accent-[var(--brand)] disabled:opacity-30"
                                />
                                {ok ? <Folder className="size-4 shrink-0 text-subtle" /> : <AlertTriangle className="size-4 shrink-0 text-amber-300" />}
                                <span className="min-w-0 flex-1">
                                    <span className={cn("block truncate font-medium", !ok && "text-muted")} title={f.dir}>
                                        {f.label}
                                    </span>
                                    <span className={cn("block text-xs", ok ? "text-subtle" : "text-amber-200/80")}>
                                        {!ok
                                            ? (f.problem ?? "Nothing in it can be played")
                                            : f.files.length === 0
                                              ? `${plural(f.notIndexed, "video")} · not scanned yet`
                                              : `${plural(f.files.length, "file")} · ${f.matches.length === 0 ? "unmatched" : f.matches.map(m => title(media.get(m.mediaId)) || mediaRef(m.mediaId)).join(", ")}`}
                                    </span>
                                </span>
                                {ok && f.files.length > 0 && f.notIndexed > 0 && <Badge tone="blue">{f.notIndexed} new</Badge>}
                            </label>
                        )
                    }}
                />
                {folders.length === 0 && (
                    <p className="py-8 text-center text-sm text-muted">{q.trim() ? `No folder matches “${q.trim()}”.` : "No other folders in your library."}</p>
                )}
            </div>
            <div className="flex items-center justify-end gap-3">
                {diskLoading && <span className="text-xs text-subtle">Looking through your library folders…</span>}
                <Button variant="primary" disabled={count === 0} loading={busy} icon={<Link2 className="size-4" />} onClick={next}>
                    Continue with {plural(count, "file")}
                </Button>
            </div>
        </div>
    )
}
