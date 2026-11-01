import { ExternalLink } from "lucide-react"
import { openExternal } from "@/lib/play"
import type { WatchProvider, WatchProviders } from "@/lib/types"
import { img } from "@/lib/utils"
import { Tooltip } from "../ui"

// Where else the title can be watched in the region of Settings › Metadata
// (TMDB's "where to watch", data from JustWatch): free services, with or
// without ads, then the subscriptions that have it. Each logo opens TMDB's
// watch page for the title (it links to the services) in the browser.
export function WatchServices({ wp }: { wp?: WatchProviders | null }) {
    if (!wp) return null
    // A service can be in both free tiers: listed once, as free.
    const free: (WatchProvider & { ads: boolean })[] = []
    for (const p of wp.free ?? []) free.push({ ...p, ads: false })
    for (const p of wp.ads ?? []) if (!free.some(x => x.id === p.id)) free.push({ ...p, ads: true })
    const flatrate = wp.flatrate ?? []
    if (!free.length && !flatrate.length) return null
    const open = () => wp.link && openExternal(wp.link)
    return (
        <div className="flex flex-col gap-3 rounded-xl bg-white/[0.025] px-4 py-3.5">
            {free.length > 0 && (
                <Row label="Watch free" hint={`Free${free.some(p => p.ads) ? " or with ads" : ""}${wp.region ? ` in ${wp.region}` : ""}`}>
                    {free.map(p => (
                        <Logo key={p.id} p={p} note={p.ads ? "with ads" : "free"} onOpen={wp.link ? open : undefined} />
                    ))}
                </Row>
            )}
            {flatrate.length > 0 && (
                <Row label="On streaming services" hint={`With a subscription${wp.region ? ` in ${wp.region}` : ""}`}>
                    {flatrate.map(p => (
                        <Logo key={p.id} p={p} note="subscription" onOpen={wp.link ? open : undefined} />
                    ))}
                </Row>
            )}
            {wp.link && (
                <button onClick={open} className="flex items-center gap-1 self-start text-[11px] text-subtle transition-colors hover:text-fg">
                    Data from JustWatch, via TMDB <ExternalLink className="size-3" />
                </button>
            )}
        </div>
    )
}

function Row({ label, hint, children }: { label: string; hint: string; children: React.ReactNode }) {
    return (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <div className="w-44 shrink-0">
                <p className="text-sm font-medium">{label}</p>
                <p className="text-[11px] text-subtle">{hint}</p>
            </div>
            <div className="flex flex-wrap gap-2">{children}</div>
        </div>
    )
}

function Logo({ p, note, onOpen }: { p: WatchProvider; note: string; onOpen?: () => void }) {
    const face = p.logo ? (
        <img src={img(p.logo)} alt={p.name} loading="lazy" className="size-9 rounded-lg object-cover ring-1 ring-white/10 transition-[filter] hover:brightness-110" />
    ) : (
        <span className="grid size-9 place-items-center rounded-lg bg-surface-3 text-[10px] font-medium text-muted">{p.name.slice(0, 3)}</span>
    )
    return (
        <Tooltip content={`${p.name} (${note})`}>
            {onOpen ? (
                <button onClick={onOpen} aria-label={`${p.name}: where to watch`}>
                    {face}
                </button>
            ) : (
                <span>{face}</span>
            )}
        </Tooltip>
    )
}
