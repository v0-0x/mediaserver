import { Sparkles } from "lucide-react"
import { useEffect, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { Button, Dialog } from "@/components/ui"
import { RELEASES } from "@/lib/changes"
import { useStatus } from "@/lib/queries"
import { createStore, playerStore, useStore } from "@/lib/store"

// The newest entry of RELEASES this device has shown (it's shown once, on
// Home, after an update).
const SEEN_KEY = "kumobox-whats-new"

function readSeen(): number | null {
    try {
        const v = localStorage.getItem(SEEN_KEY)
        return v === null ? null : Number(v) || 0
    } catch {
        return -1 // no storage: never shown by itself (it would be each time)
    }
}

function markSeen() {
    try {
        localStorage.setItem(SEEN_KEY, String(RELEASES[0]?.id ?? 0))
    } catch {
        /* nothing to remember it in */
    }
}

// Opened from Settings › Updates & about too: "all" shows every entry.
export const whatsNewStore = createStore<"new" | "all" | null>(null)

export function WhatsNew() {
    const open = useStore(whatsNewStore)
    const { data: status } = useStatus()
    const playing = useStore(playerStore, p => !!p)
    const location = useLocation()
    const navigate = useNavigate()
    const [since, setSince] = useState(0)

    // After an update, on Home, not over a video. A device seeing it the
    // first time shows the newest entry if KumoBox was in use already (an
    // update), and nothing on a new install.
    useEffect(() => {
        if (!status || open || playing || location.pathname !== "/") return
        const seen = readSeen()
        const newest = RELEASES[0]?.id ?? 0
        if (seen === -1 || (seen !== null && seen >= newest)) return
        if (seen === null) {
            // (Not by the library's folder: a new install has one already.)
            const used = status.tmdbConfigured || !!status.letterboxd || (status.paired?.length ?? 0) > 0
            if (!used) {
                markSeen()
                return
            }
            setSince(newest - 1)
        } else setSince(seen)
        whatsNewStore.set("new")
    }, [status, open, playing, location.pathname])

    const close = () => {
        markSeen()
        whatsNewStore.set(null)
    }
    const releases = open === "all" ? RELEASES : RELEASES.filter(r => r.id > since)
    // Nothing new since the last time: nothing to say.
    useEffect(() => {
        if (open === "new" && releases.length === 0) close()
    })
    if (!open || releases.length === 0) return null
    return (
        <Dialog open onOpenChange={v => !v && close()} title="What's new in KumoBox" className="w-[min(94vw,640px)]" footer={<Button variant="primary" onClick={close}>Got it</Button>}>
            <div className="flex flex-col gap-6">
                {releases.map(r => (
                    <section key={r.id}>
                        <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-muted">
                            <Sparkles className="size-4 text-brand-strong" /> {r.title}
                        </h3>
                        <ul className="flex flex-col gap-3">
                            {r.changes.map(c => (
                                <li key={c.title} className="text-sm">
                                    {c.to ? (
                                        <button
                                            onClick={() => {
                                                close()
                                                navigate(c.to!)
                                            }}
                                            className="text-left font-medium hover:underline"
                                        >
                                            {c.title}
                                        </button>
                                    ) : (
                                        <p className="font-medium">{c.title}</p>
                                    )}
                                    <p className="mt-0.5 text-muted">{c.text}</p>
                                </li>
                            ))}
                        </ul>
                    </section>
                ))}
            </div>
        </Dialog>
    )
}
