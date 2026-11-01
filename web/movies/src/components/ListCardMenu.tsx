import { type QueryClient, useQueryClient } from "@tanstack/react-query"
import { BookmarkCheck, BookmarkPlus, BookmarkX, Eye } from "lucide-react"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { MOVIE_EPISODE, mediaIsShow } from "@/lib/ids"
import { invalidateList } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { LIST_STATUS, title } from "@/lib/utils"
import { DropdownItem, DropdownLabel, DropdownSeparator } from "./ui"

type Entry = Media["mediaListEntry"]
type Saved = { status: string; progress?: number; score?: number; repeat?: number }

// Discover's and Search's cards show the list status that came with the
// results: they get the new one right away.
function cacheEntry(qc: QueryClient, mediaId: number, entry: Entry) {
    const fix = (m: Media) => (m?.id === mediaId ? { ...m, mediaListEntry: entry } : m)
    qc.setQueriesData({ queryKey: ["discover"] }, (old: Record<string, unknown> | undefined) =>
        old ? Object.fromEntries(Object.entries(old).map(([k, v]) => [k, Array.isArray(v) ? v.map(fix) : v])) : old,
    )
    qc.setQueriesData({ queryKey: ["search"] }, (old: { media?: Media[] | null } | undefined) => (old?.media ? { ...old, media: old.media.map(fix) } : old))
}

// The right-click (or "⋯") menu of movie and show cards (Discover, Search,
// Home): puts one on your watchlist, marks a movie watched, or takes it off
// the list again.
export function ListCardMenu({ media }: { media: Media }) {
    const qc = useQueryClient()
    const entry = media.mediaListEntry
    const show = mediaIsShow(media)
    const save = async (body: Saved) => {
        await api.post(`/api/media/${media.id}/entry`, body)
        cacheEntry(qc, media.id, { id: entry?.id ?? 0, progress: 0, score: 0, repeat: 0, ...body })
        invalidateList(qc, media.id)
    }
    const remove = async () => {
        await api.del(`/api/media/${media.id}/entry`)
        cacheEntry(qc, media.id, null)
        invalidateList(qc, media.id)
    }
    // Back to how it was before: off the list, or as it was on it.
    const restore = () =>
        (entry ? save({ status: entry.status, progress: entry.progress, score: entry.score, repeat: entry.repeat }) : remove()).catch(e => toast.error(e.message))

    const plan = async () => {
        try {
            await save({ status: "PLANNING" })
            toast.success(`${title(media)} is on your watchlist`, { action: { label: "Undo", onClick: restore } })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    // Like playing it to the end: it's logged in the diary too.
    const watched = async () => {
        try {
            await api.post(`/api/media/${media.id}/episode`, { episode: MOVIE_EPISODE, watched: true })
            cacheEntry(qc, media.id, { id: entry?.id ?? 0, progress: MOVIE_EPISODE, score: entry?.score ?? 0, repeat: entry?.repeat ?? 0, status: "COMPLETED" })
            invalidateList(qc, media.id)
            toast.success(`Marked ${title(media)} as watched`, { action: { label: "Undo", onClick: restore } })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const takeOff = async () => {
        try {
            await remove()
            toast.success(`Removed ${title(media)} from your list`, { action: { label: "Undo", onClick: restore } })
        } catch (e: any) {
            toast.error(e.message)
        }
    }

    const planned = entry?.status === "PLANNING"
    return (
        <>
            <DropdownLabel>{entry ? `On your list: ${LIST_STATUS[entry.status] ?? entry.status}` : "Not on your list"}</DropdownLabel>
            <DropdownItem icon={planned ? <BookmarkCheck /> : <BookmarkPlus />} disabled={planned} onSelect={plan}>
                {planned ? "On your watchlist" : entry ? "Move to watchlist" : "Add to watchlist"}
            </DropdownItem>
            {!show && entry?.status !== "COMPLETED" && (
                <DropdownItem icon={<Eye />} onSelect={watched}>
                    Mark as watched
                </DropdownItem>
            )}
            {entry && (
                <>
                    <DropdownSeparator />
                    <DropdownItem icon={<BookmarkX />} onSelect={takeOff}>
                        Remove from your list
                    </DropdownItem>
                </>
            )}
        </>
    )
}
