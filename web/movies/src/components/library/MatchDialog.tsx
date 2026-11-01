import { useQueryClient } from "@tanstack/react-query"
import { ArrowRight, Link2 } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { episodeKey, episodeKeys, formatKey, keyAtIndex, MOVIE_EPISODE, mediaIsShow, splitKey } from "@/lib/ids"
import type { LocalFile, Media } from "@/lib/types"
import { cn, mediaSummary, poster, title } from "@/lib/utils"
import { MediaPicker } from "../MediaPicker"
import { Button, Dialog, Input } from "../ui"
import { VirtualList } from "../VirtualList"

type Numbering = "names" | "offset" | "season" | "order"

// Natural order for file names ("Ep 2" before "Ep 10"), like the server.
export function naturalCompare(a: string, b: string) {
    return a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" })
}

// What a file's name says it is (main, special or extra), whatever it was
// matched as before.
const nameKind = (f: LocalFile) => f.parsed?.kind || f.kind

type Planned = { key: number; end: number; part: boolean }

// The episode key each file gets with the chosen numbering (mirrors the
// server's library.AssignManual): from the names, the season and episode
// they carry (a number without a season counts from the first episode);
// counted, the number in the name (or the file's place in name order) is
// the n-th episode of the show, so a show numbered 1-24 across two seasons
// lands on S01-S02; or the numbers are episodes of one season. A movie's
// files are the movie (its later parts when it's split in parts). Extras
// get no episode.
function episodesFor(files: LocalFile[], target: Media | null, numbering: Numbering, offset: number, season: number) {
    const order = new Map<string, number>()
    if (numbering === "order") {
        files
            .filter(f => nameKind(f) === "main")
            .slice()
            .sort((a, b) => naturalCompare(a.name, b.name))
            .forEach((f, i) => order.set(f.path, i + 1))
    }
    const movie = !!target && !mediaIsShow(target)
    // The server's absolute numbering: the n-th episode, else season 1's.
    const counted = (n: number) => (n > 0 ? keyAtIndex(target, n) || (n < 1000 ? episodeKey(1, n) : 0) : 0)
    const sameSeason = (key: number, end: number) => (end > key && splitKey(end)[0] === splitKey(key)[0] ? end : 0)
    // A movie split in parts plays from its first part: the others are parts.
    const firstPart = new Map<string, number>()
    for (const f of files) {
        const p = f.parsed?.part ?? 0
        if (p > 0 && nameKind(f) === "main") firstPart.set(f.dir, Math.min(firstPart.get(f.dir) ?? p, p))
    }
    return new Map<string, Planned>(
        files.map(f => {
            const none = { key: 0, end: 0, part: false }
            if (nameKind(f) === "extra") return [f.path, none]
            if (movie) return [f.path, { key: MOVIE_EPISODE, end: 0, part: (f.parsed?.part ?? 0) > (firstPart.get(f.dir) ?? Infinity) }]
            let first = numbering === "order" ? (order.get(f.path) ?? 0) : f.parsed.episode
            let last = numbering === "order" ? 0 : (f.parsed.episodeEnd ?? 0)
            if (first <= 0) return [f.path, none]
            if (numbering !== "names") {
                first += offset
                if (last) last += offset
            }
            if (numbering === "season") {
                const key = first < 1000 ? episodeKey(season, first) : 0
                return [f.path, { key, end: last > first && last < 1000 ? episodeKey(season, last) : 0, part: false }]
            }
            if (numbering === "names" && f.parsed.season >= 0) {
                const key = first < 1000 ? episodeKey(f.parsed.season, first) : counted(f.parsed.absolute ?? 0)
                return [f.path, { key, end: last > first && last < 1000 ? episodeKey(f.parsed.season, last) : 0, part: false }]
            }
            const key = counted(first)
            return [f.path, { key, end: sameSeason(key, last > first ? counted(last) : 0), part: false }]
        }),
    )
}

// Matches local files to a movie or show by hand (the match is locked, so
// later scans keep it). Without a target it's searched on TMDB first.
export function MatchDialog({
    open,
    onOpenChange,
    files,
    target: fixedTarget,
    initialQuery = "",
    onDone,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    files: LocalFile[]
    target?: Media | null
    initialQuery?: string
    onDone?: () => void
}) {
    const qc = useQueryClient()
    const [picked, setPicked] = useState<Media | null>(null)
    const [numbering, setNumbering] = useState<Numbering>("names")
    const [offset, setOffset] = useState(0)
    const [season, setSeason] = useState(1)
    const [saving, setSaving] = useState(false)
    const justPicked = useRef(false)
    const target = fixedTarget ?? picked

    const main = files.filter(f => nameKind(f) === "main")
    const parsedEps = main.map(f => f.parsed.episode).filter(e => e > 0)
    const unnumbered = main.length > 0 && parsedEps.length < main.length && !main.some(f => f.parsed.date)
    // Files with episodes or in season folders are a show's: search shows.
    const looksLikeShow = files.some(f => f.parsed?.show)

    // Reset when opened for another set of files, and suggest a numbering:
    // files without numbers -> file order; numbers without a season past the
    // end of season 1 (13-24 for a 12-episode first season) -> counted from
    // the first episode.
    useEffect(() => {
        if (!open) return
        setPicked(null)
        setSaving(false)
        setNumbering(unnumbered ? "order" : "names")
        setOffset(0)
        setSeason(1)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, files])
    useEffect(() => {
        if (!target || unnumbered || !mediaIsShow(target)) return
        const first = target.seasons?.find(x => x.seasonNumber === 1)?.episodeCount ?? 0
        const seasonless = main.filter(f => f.parsed.season < 0 && f.parsed.episode > 0)
        if (first > 0 && seasonless.some(f => f.parsed.episode > first)) {
            setNumbering("offset")
            setOffset(0)
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [target?.id])

    const eps = useMemo(() => episodesFor(files, target, numbering, offset, season), [files, target, numbering, offset, season])
    const sorted = useMemo(() => files.slice().sort((a, b) => naturalCompare(a.name, b.name)), [files])
    const scroller = useRef<HTMLDivElement>(null)
    const show = !!target && mediaIsShow(target)
    const known = useMemo(() => new Set(target && show ? episodeKeys(target) : []), [target, show])
    // Episodes the show doesn't have (when its seasons are known).
    const unknown = (key: number) => show && known.size > 0 && key >= 1000 && !known.has(key)
    const outOfRange = main.filter(f => unknown(eps.get(f.path)?.key ?? 0)).length
    const label = (f: LocalFile) => {
        const x = f.parsed?.extra
        if (nameKind(f) === "extra") return x ? x[0].toUpperCase() + x.slice(1) : "Extra"
        const p = eps.get(f.path)
        if (!show) return p?.part ? `Part ${f.parsed.part}` : "Movie"
        if (!p?.key) return f.parsed?.date ? f.parsed.date : nameKind(f) === "special" ? "Special" : "No number"
        return formatKey(p.key) + (p.end ? `–E${String(splitKey(p.end)[1]).padStart(2, "0")}` : "")
    }

    const save = async () => {
        if (!target) return
        setSaving(true)
        try {
            await api.post("/api/library/match", {
                paths: files.map(f => f.path),
                mediaId: target.id,
                // Shows only: numbers counted from the first episode
                // (absolute) or episodes of one season, shifted by
                // episodeOffset, or the files' order.
                absolute: show && numbering === "offset",
                season: show && numbering === "season" ? season : undefined,
                episodeOffset: show && (numbering === "offset" || numbering === "season") ? offset : 0,
                renumber: show && numbering === "order",
            })
            toast.success(`Matched ${files.length} ${files.length === 1 ? "file" : "files"} to ${title(target)}`)
            qc.invalidateQueries({ queryKey: ["library"] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
            onOpenChange(false)
            onDone?.()
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setSaving(false)
        }
    }

    // Step 1: choose the movie or show.
    if (open && !target) {
        return (
            <MediaPicker
                open
                // The picker closes itself right after a pick: that isn't a cancel.
                onOpenChange={v => {
                    if (!v && !justPicked.current) onOpenChange(false)
                    justPicked.current = false
                }}
                onPick={m => {
                    justPicked.current = true
                    setPicked(m)
                }}
                initialQuery={initialQuery}
                heading={`Match ${files.length} ${files.length === 1 ? "file" : "files"} to…`}
                description="Search TMDB for the right movie or show. The match is locked, so later scans keep it."
                typeFilter={looksLikeShow ? "TV" : ""}
            />
        )
    }

    const shift = (
        <span className="flex items-center gap-2 text-xs text-muted">
            shifted by
            <div className="w-20">
                <Input type="number" value={offset} onChange={e => setOffset(Number(e.target.value) || 0)} />
            </div>
        </span>
    )

    // Step 2: episode numbering and confirm.
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Confirm match"
            description="Check the episode numbers before saving."
            className="w-[min(94vw,720px)]"
            footer={
                <>
                    {!fixedTarget && (
                        <Button variant="ghost" onClick={() => setPicked(null)}>
                            Pick another title
                        </Button>
                    )}
                    <Button variant="primary" loading={saving} icon={<Link2 className="size-4" />} onClick={save}>
                        Match {files.length} {files.length === 1 ? "file" : "files"}
                    </Button>
                </>
            }
        >
            {target && (
                <div className="flex items-center gap-4 rounded-xl border border-line bg-surface-2/60 p-3">
                    <img {...poster(target, 56)} alt="" className="h-20 w-14 rounded-lg object-cover" />
                    <div className="min-w-0">
                        <p className="truncate font-semibold">{title(target)}</p>
                        <p className="text-sm text-muted">{mediaSummary(target)}</p>
                    </div>
                </div>
            )}

            {show && (
                <div className="mt-5 flex flex-col gap-2">
                    <p className="text-sm font-medium">Episode numbers</p>
                    {(
                        [
                            ["names", "From the file names (S01E02)"],
                            ["offset", "Counted from the first episode (absolute numbers)"],
                            ["season", "Episodes of one season"],
                            ["order", "1, 2, 3… in file-name order"],
                        ] as [Numbering, string][]
                    ).map(([v, text]) => (
                        <label key={v} className={cn("flex cursor-pointer flex-wrap items-center gap-3 rounded-xl border px-3 py-2.5 text-sm transition", numbering === v ? "border-brand/50 bg-brand-soft" : "border-line hover:bg-white/[0.03]")}>
                            <input type="radio" name="numbering" checked={numbering === v} onChange={() => setNumbering(v)} className="accent-[var(--brand)]" />
                            <span className="flex-1">{text}</span>
                            {v === "offset" && numbering === "offset" && shift}
                            {v === "season" && numbering === "season" && (
                                <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
                                    season
                                    <div className="w-16">
                                        <Input type="number" min={0} value={season} onChange={e => setSeason(Math.max(0, Number(e.target.value) || 0))} />
                                    </div>
                                    {shift}
                                </span>
                            )}
                        </label>
                    ))}
                    {outOfRange > 0 && (
                        <p className="text-xs text-amber-300">
                            {outOfRange} {outOfRange === 1 ? "file gets an episode" : "files get episodes"} {title(target)} doesn't have. If the files are numbered from the first
                            episode of the show, count from the first episode.
                        </p>
                    )}
                </div>
            )}

            {/* A flat folder can hold thousands of files: only those in view are rendered. */}
            <div ref={scroller} className="mt-5 max-h-64 overflow-y-auto rounded-xl border border-line">
                <VirtualList
                    items={sorted}
                    getKey={f => f.path}
                    estimateSize={37}
                    scrollerRef={scroller}
                    renderItem={(f, i) => (
                        <div className={cn("flex items-center gap-3 px-3 py-2 text-sm", i < sorted.length - 1 && "border-b border-line")}>
                            <span className="min-w-0 flex-1 truncate text-fg/85" title={f.path}>
                                {f.name}
                            </span>
                            <ArrowRight className="size-3.5 shrink-0 text-subtle" />
                            <span className={cn("w-28 shrink-0 text-right font-semibold tabular-nums", unknown(eps.get(f.path)?.key ?? 0) && "text-amber-300")}>{label(f)}</span>
                        </div>
                    )}
                />
            </div>
        </Dialog>
    )
}
