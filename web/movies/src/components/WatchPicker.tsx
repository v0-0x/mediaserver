import { Dices, HardDrive } from "lucide-react"
import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Badge, Button, Dialog, Select } from "@/components/ui"
import { formatRanges } from "@/lib/missing"
import { useCollection } from "@/lib/queries"
import type { CollectionItem } from "@/lib/types"
import { formatKey, mediaIsShow, totalEpisodes } from "@/lib/ids"
import { cleanDescription, cover, formatLabel, title, year } from "@/lib/utils"

type From = "PLANNING" | "PAUSED" | "CURRENT" | "library"
type Length = "any" | "movie" | "show" | "short" | "long"

const FROM: { value: From; label: string }[] = [
    { value: "PLANNING", label: "Watchlist" },
    { value: "PAUSED", label: "Paused" },
    { value: "CURRENT", label: "Watching" },
    { value: "library", label: "In my library" },
]
const LENGTHS: { value: Length; label: string }[] = [
    { value: "any", label: "Movie or show" },
    { value: "movie", label: "A movie" },
    { value: "show", label: "A show" },
    { value: "short", label: "A show, up to 10 episodes" },
    { value: "long", label: "A longer show" },
]

// "What should I watch?": a movie or show picked at random from the list
// (the watchlist, by default), another one a click away.
export function WatchPicker({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
    const { data: coll } = useCollection()
    const navigate = useNavigate()
    const [from, setFrom] = useState<From>("PLANNING")
    const [length, setLength] = useState<Length>("any")
    const [genre, setGenre] = useState("")
    const [pickId, setPickId] = useState<number | null>(null)

    const pool = useMemo(() => {
        if (!coll) return []
        const lists = coll.lists ?? []
        let items: CollectionItem[]
        if (from === "library") {
            // Downloaded, and not finished or dropped.
            items = [...lists.filter(l => l.status !== "COMPLETED" && l.status !== "DROPPED").flatMap(l => l.items), ...(coll.localOnly ?? [])].filter(it => (it.downloaded?.length ?? 0) > 0)
        } else {
            items = lists.filter(l => l.status === from || (from === "CURRENT" && l.status === "REPEATING")).flatMap(l => l.items)
        }
        return items.filter(it => {
            const m = it.media
            if (m.status === "NOT_YET_RELEASED") return false
            if (genre && !(m.genres ?? []).includes(genre)) return false
            const show = mediaIsShow(m)
            const eps = totalEpisodes(m)
            if (length === "movie") return !show
            if (length === "show") return show
            if (length === "short") return show && eps > 0 && eps <= 10
            if (length === "long") return show && (eps === 0 || eps > 10)
            return true
        })
    }, [coll, from, length, genre])

    // Another one: never the same twice in a row when there's a choice.
    const roll = (list = pool, not = pickId) => {
        const choices = list.length > 1 ? list.filter(it => it.media.id !== not) : list
        setPickId(choices.length ? choices[Math.floor(Math.random() * choices.length)].media.id : null)
    }
    const pick = pool.find(it => it.media.id === pickId) ?? null
    // A new choice of where from: picked from there at once.
    const change = <T,>(set: (v: T) => void) => (v: T) => {
        set(v)
        setPickId(null)
    }
    // Opened again: a new pick (not the last one).
    const [wasOpen, setWasOpen] = useState(open)
    if (open !== wasOpen) {
        setWasOpen(open)
        if (open) setPickId(null)
    }
    if (open && !pick && pool.length > 0) roll(pool, null)

    const m = pick?.media
    const progress = pick?.listEntry?.progress ?? 0
    const downloaded = pick?.downloaded ?? []
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title="What should I watch?" description="Something picked at random from your list." className="w-[min(94vw,640px)]">
            <div className="flex flex-col gap-5">
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                    <Select className="w-full" value={from} onChange={v => change(setFrom)(v as From)} options={FROM} />
                    <Select className="w-full" value={length} onChange={v => change(setLength)(v as Length)} options={LENGTHS} />
                    <Select className="w-full" value={genre} onChange={change(setGenre)} options={[{ value: "", label: "Any genre" }, ...(coll?.genres ?? []).map(g => ({ value: g, label: g }))]} />
                </div>
                {!m ? (
                    <p className="rounded-lg bg-white/[0.04] px-4 py-8 text-center text-sm text-muted">
                        {coll ? "Nothing on your list fits. Try another list, length or genre." : "Loading your list…"}
                    </p>
                ) : (
                    <div key={m.id} className="flex gap-5 rise-in">
                        <img src={cover(m, "poster")} alt="" className="aspect-[2/3] w-32 shrink-0 rounded-lg object-cover shadow-xl sm:w-40" />
                        <div className="flex min-w-0 flex-1 flex-col gap-2">
                            <h3 className="text-lg leading-snug font-semibold">{title(m)}</h3>
                            <p className="text-sm text-muted">
                                {[formatLabel(m.format), mediaIsShow(m) && totalEpisodes(m) ? `${totalEpisodes(m)} episodes` : "", year(m) ? String(year(m)) : "", m.averageScore ? `★ ${(m.averageScore / 10).toFixed(1)}` : ""].filter(Boolean).join(" · ")}
                            </p>
                            {(m.genres?.length ?? 0) > 0 && (
                                <div className="flex flex-wrap gap-1.5">
                                    {m.genres!.slice(0, 4).map(g => (
                                        <Badge key={g}>{g}</Badge>
                                    ))}
                                </div>
                            )}
                            {mediaIsShow(m) && progress > 0 && <p className="text-sm text-muted">You're at {formatKey(progress)}.</p>}
                            {downloaded.length > 0 && (
                                <p className="flex items-center gap-1.5 text-sm text-muted">
                                    <HardDrive className="size-3.5" /> {mediaIsShow(m) ? `${formatRanges(downloaded, 4)} in your library` : "In your library"}
                                </p>
                            )}
                            {m.description && <p className="line-clamp-4 text-sm text-subtle">{cleanDescription(m.description)}</p>}
                        </div>
                    </div>
                )}
                <div className="flex flex-wrap justify-end gap-2">
                    <Button variant="ghost" icon={<Dices className="size-4" />} disabled={pool.length < 2} onClick={() => roll()}>
                        Another one
                    </Button>
                    <Button
                        variant="primary"
                        disabled={!m}
                        onClick={() => {
                            onOpenChange(false)
                            if (m) navigate(`/entry?id=${m.id}`)
                        }}
                    >
                        Let's watch it
                    </Button>
                </div>
                {pool.length > 0 && <p className="-mt-2 text-right text-xs text-subtle">One of {pool.length}</p>}
            </div>
        </Dialog>
    )
}
