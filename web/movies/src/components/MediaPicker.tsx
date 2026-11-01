import { Search } from "lucide-react"
import { useEffect, useState } from "react"
import { useCollection, useSearch } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { cn, mediaSummary, originalTitle, poster, title } from "@/lib/utils"
import { Dialog, ErrorState, Input, Spinner, Tabs } from "./ui"

// Search TMDB (and the user's list) to pick a movie or show. type limits
// it to movies (MOVIE) or shows (TV); typeFilter, when set, lets the user
// switch between movies, shows and both, starting with it.
export function MediaPicker({
    open,
    onOpenChange,
    onPick,
    initialQuery = "",
    heading = "Choose a movie or show",
    description,
    type = "",
    typeFilter,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    onPick: (m: Media) => void
    initialQuery?: string
    heading?: string
    description?: React.ReactNode
    type?: "" | "MOVIE" | "TV"
    typeFilter?: "" | "MOVIE" | "TV"
}) {
    const [kind, setKind] = useState<"" | "MOVIE" | "TV">(type || typeFilter || "")
    useEffect(() => {
        if (open) setKind(type || typeFilter || "")
    }, [open, type, typeFilter])
    const [q, setQ] = useState(initialQuery)
    const [dq, setDq] = useState(initialQuery)
    useEffect(() => {
        if (open) {
            setQ(initialQuery)
            setDq(initialQuery)
        }
    }, [open, initialQuery])
    useEffect(() => {
        const t = setTimeout(() => setDq(q), 300)
        return () => clearTimeout(t)
    }, [q])
    const { data, isFetching, error, refetch } = useSearch({ search: dq, type: kind, perPage: 20 }, open && dq.trim().length > 1)
    const { data: coll } = useCollection()
    const fromList = (coll?.lists ?? [])
        .flatMap(l => l.items)
        .filter(i => !kind || i.media.type === kind)
        .filter(i => dq.trim().length > 1 && [title(i.media), i.media.title.english ?? "", i.media.title.native ?? ""].some(t => t.toLowerCase().includes(dq.toLowerCase())))
        .map(i => i.media)
        .slice(0, 6)
    const results = [...fromList, ...(data?.media ?? []).filter(m => !fromList.some(f => f.id === m.id))]

    return (
        <Dialog open={open} onOpenChange={onOpenChange} title={heading} description={description} className="w-[min(94vw,680px)]">
            <Input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder={kind === "TV" ? "Search TV shows…" : kind === "MOVIE" ? "Search movies…" : "Search movies and TV shows…"} icon={<Search className="size-4" />} />
            {typeFilter !== undefined && !type && (
                <Tabs
                    className="mt-3"
                    value={kind}
                    onChange={setKind}
                    tabs={[
                        { value: "", label: "All" },
                        { value: "MOVIE", label: "Movies" },
                        { value: "TV", label: "Shows" },
                    ]}
                />
            )}
            <div className="mt-4 flex min-h-40 flex-col gap-1">
                {isFetching && results.length === 0 && (
                    <div className="grid h-40 place-items-center">
                        <Spinner />
                    </div>
                )}
                {results.map((m, i) => (
                    <button
                        key={m.id}
                        onClick={() => {
                            onPick(m)
                            onOpenChange(false)
                        }}
                        className="flex items-center gap-4 rounded-xl p-2 text-left transition hover:bg-white/[0.06]"
                    >
                        <img {...poster(m, 44)} alt="" className="h-16 w-11 rounded-lg object-cover" />
                        <div className="min-w-0 flex-1">
                            <p className="truncate font-semibold">{title(m)}</p>
                            <p className="truncate text-sm text-muted">
                                {mediaSummary(m)}
                                {originalTitle(m) ? ` · ${originalTitle(m)}` : ""}
                            </p>
                        </div>
                        {i < fromList.length && <span className={cn("rounded-md bg-brand-soft px-2 py-0.5 text-[11px] font-semibold text-brand-strong")}>On your list</span>}
                    </button>
                ))}
                {!isFetching && dq.trim().length > 1 && error && <ErrorState compact title="Couldn't search TMDB" error={error} onRetry={() => refetch()} />}
                {!isFetching && dq.trim().length > 1 && !error && results.length === 0 && <p className="py-10 text-center text-sm text-muted">No results</p>}
            </div>
        </Dialog>
    )
}
