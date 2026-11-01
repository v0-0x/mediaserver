import { BookOpenCheck, ExternalLink, Heart, Repeat2, Trash2 } from "lucide-react"
import { isMovie, tmdbIdOf } from "@/lib/ids"
import { useDiary, useLetterboxd } from "@/lib/queries"
import { formatDate } from "@/lib/utils"
import { Badge, Button, IconButton, Tooltip } from "../ui"
import { StarRating } from "./ListEditor"

// The times a movie was watched: logged here when it plays to the end (or
// is marked watched), or imported from Letterboxd. Letterboxd finds a film
// by its TMDB id, so the movie can be logged there straight away too.
export function Diary({ mediaId }: { mediaId: number }) {
    const { data, isLoading } = useDiary(mediaId, 50)
    const { deleteDiary } = useLetterboxd()
    const entries = data ?? []
    if (isLoading) return null
    const logUrl = isMovie(mediaId) ? `https://letterboxd.com/tmdb/${tmdbIdOf(mediaId)}/` : null
    return (
        <section>
            <div className="mb-4 flex items-center justify-between gap-4">
                <h3 className="flex items-center gap-2 text-lg font-semibold tracking-tight">
                    <BookOpenCheck className="size-4 text-subtle" /> Diary
                    {entries.length > 0 && <span className="text-sm font-normal text-subtle tabular-nums">{entries.length}</span>}
                </h3>
                {logUrl && (
                    <Tooltip content="Opens the film on letterboxd.com, to log, rate or review it there">
                        <a href={logUrl} target="_blank" rel="noopener noreferrer">
                            <Button size="sm" variant="ghost" icon={<ExternalLink className="size-4" />}>
                                Log on Letterboxd
                            </Button>
                        </a>
                    </Tooltip>
                )}
            </div>
            {entries.length === 0 ? (
                <p className="card px-4 py-3.5 text-sm text-muted">Not logged yet. Watched to the end in KumoBox, the movie gets a diary entry, ready to send to Letterboxd.</p>
            ) : (
                <div className="card divide-y divide-line">
                    {entries.map(d => (
                        <div key={d.id} className="group/diary flex items-start gap-4 px-4 py-3">
                            <div className="w-28 shrink-0 pt-0.5 text-sm font-medium tabular-nums">{formatDate(d.watchedDate)}</div>
                            <div className="min-w-0 flex-1">
                                <div className="flex flex-wrap items-center gap-2.5">
                                    {d.rating > 0 && <StarRating value={d.rating} size="sm" />}
                                    {d.liked && <Heart className="size-3.5 fill-[#ff8000] text-[#ff8000]" aria-label="Liked" />}
                                    {d.rewatch && (
                                        <span className="flex items-center gap-1 text-xs text-muted">
                                            <Repeat2 className="size-3.5" /> Rewatch
                                        </span>
                                    )}
                                    <Badge tone={d.source === "letterboxd" ? "green" : "gray"}>{d.source === "letterboxd" ? "From Letterboxd" : "Watched in KumoBox"}</Badge>
                                    {d.source !== "letterboxd" && (d.exportedAt ? <Badge tone="blue">On Letterboxd</Badge> : <Badge tone="amber">Not on Letterboxd yet</Badge>)}
                                </div>
                                {d.review && <p className="mt-1.5 line-clamp-3 text-sm whitespace-pre-line text-muted">{d.review}</p>}
                            </div>
                            <IconButton
                                label="Delete this diary entry"
                                size="sm"
                                className="opacity-0 group-hover/diary:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
                                disabled={deleteDiary.isPending}
                                onClick={() => deleteDiary.mutate(d.id)}
                            >
                                <Trash2 className="size-4" />
                            </IconButton>
                        </div>
                    ))}
                </div>
            )}
        </section>
    )
}
