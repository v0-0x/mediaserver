import { CircleCheck, FolderSync, Magnet, RefreshCw } from "lucide-react"
import { Link, useSearchParams } from "react-router-dom"
import { Cleanup } from "@/components/library/Cleanup"
import { LibraryFolders } from "@/components/library/LibraryFolders"
import { AllFiles, Ignored, Unmatched } from "@/components/library/LibraryTools"
import { Button, EmptyState, Tabs } from "@/components/ui"
import { useCollection, useScan } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import { formatRanges, missingEpisodes } from "@/lib/missing"
import type { CollectionItem, CollectionView } from "@/lib/types"
import { cover, title } from "@/lib/utils"

type Tab = "folders" | "unmatched" | "missing" | "cleanup" | "files" | "ignored"
const TABS: string[] = ["folders", "unmatched", "missing", "cleanup", "files", "ignored"]

export default function LibraryPage() {
    const [params, setParams] = useSearchParams()
    const raw = params.get("tab") ?? ""
    // Unknown tabs (old links, typos) open the first one instead of nothing.
    const tab = (TABS.includes(raw) ? raw : "folders") as Tab
    const { data: coll } = useCollection()
    const scan = useScan()
    const scanning = useStore(scanStore, s => s.running)
    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Library tools</h1>
                    <p className="mt-1 text-muted">Fix matches and episode numbers, find missing episodes, and make room.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                    <Button icon={<FolderSync className="size-4" />} loading={scanning} onClick={() => scan.mutate(false)}>
                        Scan for new files
                    </Button>
                    <Button icon={<RefreshCw className="size-4" />} onClick={() => scan.mutate(true)} disabled={scanning}>
                        Re-match everything
                    </Button>
                </div>
            </div>
            <Tabs
                className="mb-8"
                value={tab}
                onChange={t => setParams({ tab: t }, { replace: true })}
                tabs={[
                    { value: "folders", label: "Folders & matches" },
                    { value: "unmatched", label: "Unmatched", count: coll?.unmatchedCount ?? 0 },
                    { value: "missing", label: "Missing episodes", count: withMissing(coll).length },
                    { value: "cleanup", label: "Clean up" },
                    { value: "files", label: "All files" },
                    { value: "ignored", label: "Ignored", count: coll?.ignoredCount ?? 0 },
                ]}
            />
            {tab === "folders" && <LibraryFolders />}
            {tab === "unmatched" && <Unmatched />}
            {tab === "missing" && <MissingEpisodes />}
            {tab === "cleanup" && <Cleanup />}
            {tab === "files" && <AllFiles />}
            {tab === "ignored" && <Ignored />}
        </div>
    )
}

// The anime with episodes missing from the library: gaps between those
// there, and ones aired since the last (not for anime dropped or finished
// on the list: those aren't being collected).
function withMissing(coll?: CollectionView) {
    if (!coll) return []
    const items: CollectionItem[] = [...(coll.lists ?? []).flatMap(l => l.items), ...(coll.localOnly ?? [])]
    return items
        .filter(it => (it.downloaded?.length ?? 0) > 0)
        .map(it => {
            const m = missingEpisodes(it.downloaded ?? [], it.media)
            const status = it.listEntry?.status
            // Newer episodes only for those still being watched (or not on the list).
            if (status === "COMPLETED" || status === "DROPPED") m.newer = []
            if (status === "DROPPED") m.gaps = []
            return { item: it, missing: m }
        })
        .filter(x => x.missing.gaps.length > 0 || x.missing.newer.length > 0)
        .sort((a, b) => b.missing.gaps.length - a.missing.gaps.length || title(a.item.media).localeCompare(title(b.item.media)))
}

function MissingEpisodes() {
    const { data: coll, isLoading } = useCollection()
    if (isLoading) return <div className="card h-40 shimmer" />
    const rows = withMissing(coll)
    if (!rows.length)
        return (
            <EmptyState icon={<CircleCheck className="size-6" />} title="Nothing missing">
                Every anime in your library has its episodes from the first one there to the last, and the newest aired.
            </EmptyState>
        )
    return (
        <div className="flex flex-col gap-4">
            <p className="text-sm text-muted">Episodes between the first and the last one in your library that aren't there, and those aired since (for the anime you're watching).</p>
            <div className="card divide-y divide-line">
                {rows.map(({ item, missing }) => {
                    const first = missing.gaps[0] ?? missing.newer[0]
                    return (
                        <div key={item.media.id} className="flex items-center gap-4 px-4 py-3">
                            <img src={cover(item.media, "thumb")} alt="" className="h-16 w-11 shrink-0 rounded-md object-cover" />
                            <div className="min-w-0 flex-1">
                                <Link to={`/entry?id=${item.media.id}`} className="block truncate font-medium hover:underline">
                                    {title(item.media)}
                                </Link>
                                {missing.gaps.length > 0 && (
                                    <p className="mt-0.5 text-sm text-amber-200/85">
                                        Missing {missing.gaps.length === 1 ? "episode" : `${missing.gaps.length} episodes`}: {formatRanges(missing.gaps)}
                                    </p>
                                )}
                                {missing.newer.length > 0 && (
                                    <p className="mt-0.5 text-sm text-muted">
                                        Aired since your last one: {formatRanges(missing.newer)}
                                    </p>
                                )}
                            </div>
                            <Link to={`/entry?id=${item.media.id}&tab=torrents&episode=${first}`}>
                                <Button size="sm" icon={<Magnet className="size-4" />}>
                                    Find torrents
                                </Button>
                            </Link>
                        </div>
                    )
                })}
            </div>
        </div>
    )
}
