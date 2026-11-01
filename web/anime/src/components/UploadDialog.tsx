import { useQuery } from "@tanstack/react-query"
import { CheckCircle2, FilePlus2, FolderPlus, Server, TriangleAlert, Upload, X } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { ApiError, api } from "@/lib/api"
import { formatBytes } from "@/lib/utils"
import { Button, Dialog, Progress, Select } from "./ui"

// Files and folders from this computer into a server's library: one that
// takes this device's downloads (Settings › Other devices: Let it download
// here), or, on mediaserver's own pages, that server. The files go a piece
// at a time (server: internal/upload), each after the last: one cut short
// carries on where it stopped the next time it's sent.

type Target = { id: string; name: string; online: boolean; here?: boolean }
type Folders = { folders: string[]; chunk: number }
type Answer = { path: string; have: number; done: boolean }

type Item = {
    key: string
    file: File
    path: string // in the folder, with slashes
    sent: number
    state: "waiting" | "sending" | "done" | "there" | "failed"
    error?: string
}

// Not worth sending: the system's own files, and hidden ones (the server
// takes none).
function skipped(path: string) {
    const parts = path.split("/")
    const name = parts[parts.length - 1].toLowerCase()
    return parts.some(p => p.startsWith(".")) || name === "thumbs.db" || name === "desktop.ini"
}

function query(target: Target, folder: number, extra: Record<string, string | number> = {}) {
    const q = new URLSearchParams()
    if (!target.here) q.set("target", target.id)
    q.set("folder", String(folder))
    for (const [k, v] of Object.entries(extra)) q.set(k, String(v))
    return q.toString()
}

export function useUploadTargets(enabled = true) {
    return useQuery({
        queryKey: ["upload", "targets"],
        queryFn: () => api.get<Target[]>("/api/upload/targets"),
        enabled,
        staleTime: 15_000,
    })
}

export function UploadDialog({ open, onOpenChange, target: wanted }: { open: boolean; onOpenChange: (v: boolean) => void; target?: string }) {
    const targets = useUploadTargets(open)
    const list = targets.data ?? []
    const [targetId, setTargetId] = useState<string>("")
    const target = list.find(t => (t.here ? "here" : t.id) === targetId) ?? list[0]
    useEffect(() => {
        if (!list.length) return
        const pick = wanted ? list.find(t => t.id === wanted) : undefined
        setTargetId(prev => (prev && list.some(t => (t.here ? "here" : t.id) === prev) ? prev : pick ? pick.id : list[0].here ? "here" : list[0].id))
    }, [list, wanted])
    const folders = useQuery({
        queryKey: ["upload", "folders", target?.here ? "" : target?.id],
        queryFn: () => api.get<Folders>(`/api/upload?${target!.here ? "" : `target=${encodeURIComponent(target!.id)}`}`),
        enabled: open && !!target,
    })
    const [folder, setFolder] = useState(0)
    useEffect(() => setFolder(0), [target?.id, target?.here])

    const [items, setItems] = useState<Item[]>([])
    const [busy, setBusy] = useState(false)
    const stop = useRef(false)
    const update = (key: string, patch: Partial<Item>) => setItems(xs => xs.map(x => (x.key === key ? { ...x, ...patch } : x)))
    const add = (files: FileList | null) => {
        if (!files) return
        const next: Item[] = []
        for (const file of Array.from(files)) {
            const path = (file.webkitRelativePath || file.name).replace(/\\/g, "/")
            if (skipped(path)) continue
            next.push({ key: `${path}|${file.size}|${file.lastModified}`, file, path, sent: 0, state: "waiting" })
        }
        setItems(xs => {
            const have = new Set(xs.map(x => x.key))
            return [...xs, ...next.filter(x => !have.has(x.key))]
        })
    }
    const filesInput = useRef<HTMLInputElement>(null)
    const folderInput = useRef<HTMLInputElement>(null)

    const total = items.reduce((n, x) => n + x.file.size, 0)
    const sent = items.reduce((n, x) => n + (x.state === "done" || x.state === "there" ? x.file.size : x.sent), 0)
    const left = items.filter(x => x.state === "waiting" || x.state === "failed").length
    const failed = items.filter(x => x.state === "failed").length

    // One file: where it stands on the server, then the rest, a piece at a
    // time. Network trouble: the piece again, a few times.
    const sendOne = async (t: Target, f: number, chunk: number, it: Item) => {
        update(it.key, { state: "sending", error: undefined })
        const size = it.file.size
        let at = (await api.get<Answer>(`/api/upload?${query(t, f, { path: it.path, total: size })}`)).have
        if (at >= size && size > 0) {
            update(it.key, { state: "there", sent: size })
            return
        }
        let tries = 0
        for (;;) {
            if (stop.current) {
                update(it.key, { state: "waiting", sent: at })
                return
            }
            const end = Math.min(at + chunk, size)
            let res: Response
            try {
                res = await fetch(`/api/upload?${query(t, f, { path: it.path, offset: at, total: size })}`, {
                    method: "POST",
                    headers: { "Content-Type": "application/octet-stream" },
                    body: it.file.slice(at, end),
                    credentials: "same-origin",
                })
            } catch (e) {
                if (++tries > 4) throw e
                await new Promise(r => setTimeout(r, 1500 * tries))
                continue
            }
            const data = await res.json().catch(() => null)
            if (res.ok) {
                tries = 0
                at = data?.have ?? end
                update(it.key, { sent: at })
                if (data?.done) {
                    update(it.key, { state: "done", sent: size })
                    return
                }
                continue
            }
            // Not where the server is: carry on from there.
            if (res.status === 409 && typeof data?.have === "number" && data.have !== at && data.have <= size) {
                at = data.have
                continue
            }
            if ((res.status >= 500 && res.status !== 507) && ++tries <= 4) {
                await new Promise(r => setTimeout(r, 1500 * tries))
                continue
            }
            throw new ApiError(data?.error || res.statusText, res.status, data)
        }
    }

    const start = async () => {
        if (!target || !folders.data) return
        stop.current = false
        setBusy(true)
        try {
            for (const it of items) {
                if (stop.current) break
                if (it.state !== "waiting" && it.state !== "failed") continue
                try {
                    await sendOne(target, folder, folders.data.chunk, it)
                } catch (e) {
                    update(it.key, { state: "failed", error: (e as Error).message })
                    // No room on the server: the others won't fit either.
                    if (e instanceof ApiError && e.status === 507) break
                }
            }
        } finally {
            setBusy(false)
        }
    }

    const close = (v: boolean) => {
        if (!v && busy) {
            if (!window.confirm("Stop sending the files? What's sent stays; sending them again carries on from there.")) return
            stop.current = true
        }
        if (!v && !busy) setItems(xs => xs.filter(x => x.state !== "done" && x.state !== "there"))
        onOpenChange(v)
    }

    const folderOptions = useMemo(() => (folders.data?.folders ?? []).map((p, i) => ({ value: String(i), label: p })), [folders.data])
    const ready = !!target && !!folders.data?.folders.length && left > 0 && !busy

    return (
        <Dialog
            open={open}
            onOpenChange={close}
            title="Upload to a server"
            description="Anime episodes and their subtitles from this computer, into the server's library. Folders keep their names and what's in them."
            className="w-[min(94vw,640px)]"
            footer={
                <>
                    {busy ? (
                        <Button variant="ghost" onClick={() => (stop.current = true)}>
                            Stop
                        </Button>
                    ) : (
                        <Button variant="ghost" onClick={() => close(false)}>
                            Close
                        </Button>
                    )}
                    <Button variant="primary" icon={<Upload className="size-4" />} loading={busy} disabled={!ready} onClick={start}>
                        {failed && !busy ? "Try again" : left ? `Upload ${left} ${left === 1 ? "file" : "files"}` : "Upload"}
                    </Button>
                </>
            }
        >
            {targets.isLoading ? null : !list.length ? (
                <p className="text-sm leading-relaxed text-muted">
                    No server takes files from this device yet. On the server (or the computer to send them to), in <b>Settings › Other devices</b> (on mediaserver, its dashboard's <b>Devices</b>), turn on <b>Let it download here</b>
                    for this device.
                </p>
            ) : (
                <div className="flex flex-col gap-4">
                    <div className="grid gap-3 sm:grid-cols-2">
                        <label className="flex flex-col gap-1.5 text-sm font-medium">
                            To
                            <Select
                                value={target ? (target.here ? "here" : target.id) : ""}
                                onChange={setTargetId}
                                disabled={busy}
                                options={list.map(t => ({ value: t.here ? "here" : t.id, label: t.here ? `${t.name} (this server)` : t.online ? t.name : `${t.name} (not on)` }))}
                            />
                        </label>
                        <label className="flex flex-col gap-1.5 text-sm font-medium">
                            Into
                            {folders.error ? (
                                <span className="text-[12.5px] font-normal text-rose-300">{(folders.error as Error).message}</span>
                            ) : folderOptions.length ? (
                                <Select value={String(folder)} onChange={v => setFolder(Number(v))} disabled={busy} options={folderOptions} />
                            ) : folders.data ? (
                                <span className="text-[12.5px] font-normal text-amber-300">It has no library folder yet (its Settings › Local library).</span>
                            ) : (
                                <span className="text-[12.5px] font-normal text-muted">…</span>
                            )}
                        </label>
                    </div>
                    <div className="flex flex-wrap gap-2">
                        <Button size="sm" icon={<FilePlus2 className="size-4" />} disabled={busy} onClick={() => filesInput.current?.click()}>
                            Add files
                        </Button>
                        <Button size="sm" icon={<FolderPlus className="size-4" />} disabled={busy} onClick={() => folderInput.current?.click()}>
                            Add a folder
                        </Button>
                        {items.length > 0 && !busy && (
                            <Button size="sm" variant="ghost" onClick={() => setItems([])}>
                                Clear
                            </Button>
                        )}
                        <input
                            ref={filesInput}
                            type="file"
                            multiple
                            hidden
                            onChange={e => {
                                add(e.target.files)
                                e.target.value = ""
                            }}
                        />
                        <input
                            ref={folderInput}
                            type="file"
                            hidden
                            // A whole folder, what's in it with its path.
                            {...({ webkitdirectory: "", directory: "" } as Record<string, string>)}
                            onChange={e => {
                                add(e.target.files)
                                e.target.value = ""
                            }}
                        />
                    </div>
                    {items.length > 0 && (
                        <div className="flex flex-col gap-2">
                            <div className="flex items-center justify-between text-[12.5px] text-muted">
                                <span>
                                    {items.length} {items.length === 1 ? "file" : "files"} · {formatBytes(total)}
                                </span>
                                <span className="tabular-nums">{total ? Math.floor((sent / total) * 100) : 0}%</span>
                            </div>
                            <Progress value={total ? sent / total : 0} />
                            <ul className="flex max-h-[40vh] flex-col gap-1 overflow-y-auto pr-1">
                                {items.map(it => (
                                    <li key={it.key} className="flex items-center gap-2.5 rounded-lg bg-white/[0.03] px-2.5 py-2 text-[13px]">
                                        {it.state === "done" || it.state === "there" ? (
                                            <CheckCircle2 className="size-4 shrink-0 text-emerald-300" />
                                        ) : it.state === "failed" ? (
                                            <TriangleAlert className="size-4 shrink-0 text-rose-300" />
                                        ) : (
                                            <Server className="size-4 shrink-0 text-subtle" />
                                        )}
                                        <span className="min-w-0 flex-1">
                                            <span className="block truncate">{it.path}</span>
                                            {it.state === "failed" && <span className="block text-[12px] text-rose-300">{it.error}</span>}
                                            {it.state === "there" && <span className="block text-[12px] text-muted">Already there</span>}
                                            {it.state === "sending" && <Progress value={it.file.size ? it.sent / it.file.size : 1} className="mt-1.5 h-1" />}
                                        </span>
                                        <span className="shrink-0 text-[12px] text-subtle tabular-nums">{formatBytes(it.file.size)}</span>
                                        {!busy && it.state !== "done" && it.state !== "there" && (
                                            <button className="focus-ring rounded text-subtle hover:text-fg" aria-label={`Remove ${it.path}`} onClick={() => setItems(xs => xs.filter(x => x.key !== it.key))}>
                                                <X className="size-4" />
                                            </button>
                                        )}
                                    </li>
                                ))}
                            </ul>
                        </div>
                    )}
                </div>
            )}
        </Dialog>
    )
}
