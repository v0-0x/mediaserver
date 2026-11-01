import { Puzzle } from "lucide-react"
import { useEffect, useState } from "react"
import { Badge, Dialog } from "@/components/ui"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { LogLineT } from "../extensionTypes"

// What the Providers & extensions page's tabs share.

export function ExtIcon({ src, className }: { src?: string; className?: string }) {
    const [err, setErr] = useState(false)
    if (!src || err)
        return (
            <span className={cn("grid shrink-0 place-items-center rounded-lg bg-white/[0.06] text-muted", className)}>
                <Puzzle className="size-5" />
            </span>
        )
    return <img src={src} alt="" onError={() => setErr(true)} className={cn("shrink-0 rounded-lg bg-white/5 object-cover", className)} />
}

// An extension's console output (Nuvio providers' too).
export function LogsDialog({ open, onOpenChange, id, title = "Extension logs" }: { open: boolean; onOpenChange: (v: boolean) => void; id: string; title?: string }) {
    const [lines, setLines] = useState<LogLineT[]>([])
    useEffect(() => {
        if (open) api.get<LogLineT[] | null>(`/api/extensions/${id}/logs`).then(l => setLines(l ?? [])).catch(e => toast.error(e.message))
    }, [open, id])
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title={title} className="w-[min(94vw,820px)]">
            {lines.length === 0 ? (
                <p className="py-8 text-center text-sm text-subtle">No output yet.</p>
            ) : (
                <pre className="max-h-[60vh] overflow-auto rounded-xl bg-black/40 p-4 text-xs leading-relaxed">
                    {lines.map((l, i) => (
                        <div key={i} className={cn(l.level === "error" ? "text-rose-300" : l.level === "warn" ? "text-amber-300" : "text-fg/80")}>
                            <span className="text-subtle">{new Date(l.time * 1000).toLocaleTimeString()} </span>
                            {l.message}
                        </div>
                    ))}
                </pre>
            )}
        </Dialog>
    )
}

// "Movies", "Shows" badges for a provider's types (movie/tv or Stremio's
// movie/series).
export function TypeBadges({ types }: { types: string[] }) {
    const movies = types.some(t => t === "movie")
    const shows = types.some(t => t === "tv" || t === "series")
    return (
        <>
            {movies && <Badge>Movies</Badge>}
            {shows && <Badge>Shows</Badge>}
        </>
    )
}

// Shown instead of install buttons on devices on the LAN (the server
// refuses them: api/lan.go).
export function LanNote() {
    return (
        <p className="rounded-lg bg-white/[0.03] px-4 py-3 text-sm text-muted">
            Providers and extensions are installed, updated, set up, turned on or off and removed on the computer KumoBox runs on.
        </p>
    )
}

// A section of a tab: title, description and actions.
export function TabSection({ title, description, actions, children }: { title: React.ReactNode; description?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode }) {
    return (
        <section className="flex flex-col gap-4">
            <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                    <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
                    {description && <p className="mt-0.5 max-w-3xl text-[13px] text-muted">{description}</p>}
                </div>
                {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
            </div>
            {children}
        </section>
    )
}
