import { Check, ChevronDown, EyeOff, Heart, Minus, NotebookPen, Plus, Star, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
import { episodeIndex, formatKey, lastEpisode, latestAired, MOVIE_EPISODE, mediaIsShow, nextEpisode, totalEpisodes } from "@/lib/ids"
import { useDeleteEntry, useEpisodeMarker, useUpdateEntry } from "@/lib/queries"
import type { EntryUpdate, FuzzyDate, ListEntry, Media } from "@/lib/types"
import { cn, formatDate, fuzzyDate, LIST_STATUS, scoreToStars, starsToScore, title, today } from "@/lib/utils"
import { Button, Dialog, Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger, Field, Input, Textarea, Tooltip } from "../ui"

type Entry = Pick<ListEntry, "status" | "progress" | "score" | "repeat"> & Partial<Pick<ListEntry, "notes" | "liked" | "startedAt" | "completedAt">>

// The statuses in the order a movie or a show goes through them.
const ORDER = ["PLANNING", "CURRENT", "COMPLETED", "REPEATING", "PAUSED", "DROPPED"]

// Rating or liking something that isn't on the list puts it there: a movie
// as watched (as Letterboxd does), a show as being watched.
function firstStatus(media: Media): EntryUpdate {
    return mediaIsShow(media) ? { status: "CURRENT" } : { status: "COMPLETED", progress: MOVIE_EPISODE }
}

export function ListStatusButton({ media, entry }: { media: Media; entry: Entry | null | undefined }) {
    const update = useUpdateEntry(media.id)
    const del = useDeleteEntry(media.id)
    const marker = useEpisodeMarker(media)
    const show = mediaIsShow(media)
    const pick = (status: string) => {
        // A movie marked watched goes through the episode marker, like
        // playing it to the end: it's logged in the diary.
        if (status === "COMPLETED" && !show) return marker.mark(MOVIE_EPISODE, true)
        const body: EntryUpdate = { status }
        // Watched up to the last episode that aired: those still to come
        // stay unwatched (they're next once they air).
        const last = latestAired(media) || lastEpisode(media)
        if (status === "COMPLETED" && show && last) body.progress = last
        update.mutate(body)
    }
    return (
        <Dropdown>
            <DropdownTrigger asChild>
                <Button variant="subtle" size="lg" loading={update.isPending || del.isPending || marker.pending}>
                    {entry ? (
                        <>
                            <Check className="size-4 text-brand-strong" />
                            {LIST_STATUS[entry.status] ?? entry.status}
                        </>
                    ) : (
                        <>
                            <Plus className="size-4" />
                            Add to list
                        </>
                    )}
                    <ChevronDown className="size-4 opacity-60" />
                </Button>
            </DropdownTrigger>
            <DropdownContent align="start">
                {ORDER.map(k => (
                    <DropdownItem key={k} onSelect={() => pick(k)}>
                        {entry?.status === k ? "● " : ""}
                        {LIST_STATUS[k]}
                    </DropdownItem>
                ))}
                {entry && (
                    <>
                        <DropdownSeparator />
                        {!show && entry.status === "COMPLETED" && (
                            <DropdownItem icon={<EyeOff />} onSelect={() => marker.mark(MOVIE_EPISODE, false)}>
                                Mark as unwatched
                            </DropdownItem>
                        )}
                        <DropdownItem danger icon={<Trash2 />} onSelect={() => confirm(`Remove ${title(media)} from your list? Its progress, rating and notes go with it.`) && del.mutate()}>
                            Remove from list
                        </DropdownItem>
                    </>
                )}
            </DropdownContent>
        </Dropdown>
    )
}

// How far into a show you are: the last episode watched and "5 / 24". The
// buttons mark the next episode watched, or the last one unwatched (the
// server keeps the status in step: the last episode completes the show).
export function ProgressEditor({ media, entry }: { media: Media; entry: Entry | null | undefined }) {
    const marker = useEpisodeMarker(media)
    const progress = entry?.progress ?? 0
    const total = totalEpisodes(media)
    const next = nextEpisode(media, progress)
    return (
        <div className="flex h-11 items-center gap-1 rounded-lg bg-white/[0.07] px-1">
            <Tooltip content={progress ? `Mark ${formatKey(progress)} as unwatched` : "Nothing watched yet"}>
                <button
                    className="grid size-9 place-items-center rounded-md text-muted transition-colors hover:bg-white/10 hover:text-fg disabled:opacity-40"
                    disabled={!progress || marker.pending}
                    onClick={() => marker.mark(progress, false)}
                    aria-label="Mark the last episode unwatched"
                >
                    <Minus className="size-4" />
                </button>
            </Tooltip>
            <div className="min-w-20 px-1 text-center leading-tight">
                <p className="text-sm font-semibold tabular-nums">
                    {episodeIndex(media, progress)}
                    <span className="font-normal text-subtle"> / {total || "?"}</span>
                </p>
                <p className="text-[11px] text-subtle tabular-nums">{progress ? formatKey(progress) : "Not started"}</p>
            </div>
            <Tooltip content={next ? `Mark ${formatKey(next)} as watched` : "All caught up"}>
                <button
                    className="grid size-9 place-items-center rounded-md text-muted transition-colors hover:bg-white/10 hover:text-fg disabled:opacity-40"
                    disabled={!next || marker.pending}
                    onClick={() => marker.mark(next, true)}
                    aria-label="Mark the next episode watched"
                >
                    <Plus className="size-4" />
                </button>
            </Tooltip>
        </div>
    )
}

// Letterboxd-style stars: 0.5 to 5 by halves, for a 0-100 score (a half
// star is 10 points). Picking the rating it has clears it.
export function StarRating({ value, onChange, size = "md", disabled }: { value: number; onChange?: (score: number) => void; size?: "sm" | "md"; disabled?: boolean }) {
    const [hover, setHover] = useState<number | null>(null)
    const stars = hover ?? scoreToStars(value)
    const dim = size === "sm" ? "size-3.5" : "size-6"
    const pick = (s: number) => onChange?.(starsToScore(s) === value ? 0 : starsToScore(s))
    return (
        <div className={cn("flex items-center", size === "md" && "gap-0.5")} onMouseLeave={() => setHover(null)} role={onChange ? "radiogroup" : undefined} aria-label="Rating">
            {[1, 2, 3, 4, 5].map(i => {
                const fill = Math.max(0, Math.min(1, stars - (i - 1)))
                return (
                    <span key={i} className={cn("relative block shrink-0", dim)}>
                        <Star className={cn("absolute inset-0 text-white/20", dim)} strokeWidth={1.75} />
                        <span className="absolute inset-y-0 left-0 overflow-hidden" style={{ width: `${fill * 100}%` }}>
                            <Star className={cn("max-w-none fill-[#00e054] text-[#00e054]", dim)} strokeWidth={1.75} />
                        </span>
                        {onChange &&
                            [i - 0.5, i].map((s, half) => (
                                <button
                                    key={s}
                                    type="button"
                                    role="radio"
                                    aria-checked={scoreToStars(value) === s}
                                    aria-label={`${s} ${s === 1 ? "star" : "stars"}`}
                                    disabled={disabled}
                                    onMouseEnter={() => setHover(s)}
                                    onFocus={() => setHover(s)}
                                    onBlur={() => setHover(null)}
                                    onClick={() => pick(s)}
                                    className={cn("absolute inset-y-0 w-1/2 cursor-pointer outline-none disabled:cursor-not-allowed", half ? "right-0" : "left-0")}
                                />
                            ))}
                    </span>
                )
            })}
        </div>
    )
}

// The rating, the heart and the review of a movie or show, as on
// Letterboxd.
export function RatingBar({ media, entry }: { media: Media; entry: Entry | null | undefined }) {
    const update = useUpdateEntry(media.id)
    const [reviewing, setReviewing] = useState(false)
    const save = (body: EntryUpdate) => update.mutate(entry ? body : { ...firstStatus(media), ...body })
    const liked = !!entry?.liked
    const watched = fuzzyDate(entry?.completedAt)
    return (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg bg-white/[0.04] px-4 py-2.5">
            <div className="flex items-center gap-3">
                <span className="text-xs font-medium text-subtle">{entry?.score ? "Rated" : "Rate"}</span>
                <StarRating value={entry?.score ?? 0} onChange={score => save({ score })} disabled={update.isPending} />
            </div>
            <Tooltip content={liked ? "Unlike" : "Like"}>
                <button
                    onClick={() => save({ liked: !liked })}
                    disabled={update.isPending}
                    aria-pressed={liked}
                    className={cn("flex items-center gap-1.5 text-[13px] font-medium transition-colors", liked ? "text-[#ff8000]" : "text-muted hover:text-fg")}
                >
                    <Heart className={cn("size-5", liked && "fill-current")} />
                    {liked ? "Liked" : "Like"}
                </button>
            </Tooltip>
            <button onClick={() => setReviewing(true)} className="flex items-center gap-1.5 text-[13px] font-medium text-muted transition-colors hover:text-fg">
                <NotebookPen className="size-4" />
                {entry?.notes ? "Edit review" : "Review"}
            </button>
            {watched && entry?.status === "COMPLETED" && <span className="text-xs text-subtle">Watched {formatDate(watched)}</span>}
            <ReviewDialog open={reviewing} onOpenChange={setReviewing} media={media} entry={entry} onSave={save} saving={update.isPending} />
        </div>
    )
}

function toFuzzy(iso: string): FuzzyDate {
    if (!iso) return { year: null, month: null, day: null }
    const [y, m, d] = iso.split("-").map(Number)
    return { year: y, month: m, day: d }
}

// The review (the list entry's notes), when it was watched and how many
// times it was watched again.
function ReviewDialog({
    open,
    onOpenChange,
    media,
    entry,
    onSave,
    saving,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    media: Media
    entry: Entry | null | undefined
    onSave: (body: EntryUpdate) => void
    saving: boolean
}) {
    const show = mediaIsShow(media)
    const [notes, setNotes] = useState("")
    const [started, setStarted] = useState("")
    const [completed, setCompleted] = useState("")
    const [repeat, setRepeat] = useState(0)
    useEffect(() => {
        if (!open) return
        setNotes(entry?.notes ?? "")
        setStarted(fuzzyDate(entry?.startedAt))
        setCompleted(fuzzyDate(entry?.completedAt) || (!show && !entry ? today() : ""))
        setRepeat(entry?.repeat ?? 0)
    }, [open, entry, show])
    const submit = () => {
        const body: EntryUpdate = { notes, completedAt: toFuzzy(completed), repeat: Math.max(0, repeat) }
        if (show) body.startedAt = toFuzzy(started)
        onSave(body)
        onOpenChange(false)
    }
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Your review"
            description={show ? "Notes about the show, and when you watched it." : "Your review, and when you watched it."}
            footer={
                <>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button variant="primary" loading={saving} onClick={submit}>
                        Save
                    </Button>
                </>
            }
        >
            <div className="flex flex-col gap-4">
                <Field label="Review">
                    <Textarea value={notes} onChange={e => setNotes(e.target.value)} placeholder="Add a review…" className="min-h-36" />
                </Field>
                <div className="grid gap-4 sm:grid-cols-3">
                    {show && (
                        <Field label="Started">
                            <Input type="date" value={started} max={today()} onChange={e => setStarted(e.target.value)} />
                        </Field>
                    )}
                    <Field label={show ? "Finished" : "Watched on"}>
                        <Input type="date" value={completed} max={today()} onChange={e => setCompleted(e.target.value)} />
                    </Field>
                    <Field label="Rewatches">
                        <Input type="number" min={0} value={repeat} onChange={e => setRepeat(Number(e.target.value) || 0)} />
                    </Field>
                </div>
            </div>
        </Dialog>
    )
}
