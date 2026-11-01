import { useQueryClient } from "@tanstack/react-query"
import { ArrowUpCircle, MonitorSmartphone, Tv } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui"
import { api } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { DevicePrompt } from "@/lib/types"

// The version notes put away ("id@version"): shown again for the next one.
const HIDDEN_KEY = "aniko.devicePromptsHidden"

function readHidden(): string[] {
    try {
        const v = JSON.parse(localStorage.getItem(HIDDEN_KEY) ?? "[]")
        return Array.isArray(v) ? v.filter(x => typeof x === "string") : []
    } catch {
        return []
    }
}

// Home, on the computer: an Aniko new on the network (a TV, say), to share
// this computer's library with; and a paired one on another version than
// this one, to update.
export function DevicePrompts() {
    const { data: status } = useStatus()
    const qc = useQueryClient()
    const [busy, setBusy] = useState("")
    const [hidden, setHidden] = useState(readHidden)
    if (!status || status.client === "lan") return null
    // A TV has no library to share, nor does a computer without a folder
    // (or with its library off).
    const canShare = status.platform !== "android" && !!status.settings.library.dir && !status.settings.library.off
    const prompts = (status.devicePrompts ?? []).filter(p => (p.kind === "new" ? canShare : !hidden.includes(`${p.id}@${p.version}`)))
    if (!prompts.length) return null

    const answer = async (p: DevicePrompt, share: boolean) => {
        setBusy(p.id)
        try {
            await api.post(`/api/sharing/peers/${encodeURIComponent(p.id)}`, share ? { allowed: true } : { declined: true })
            if (share) toast.success(`Sharing this library with ${p.name}. Your AniList account and downloads can go with it, in Settings › Other devices.`)
            await qc.invalidateQueries({ queryKey: ["status"] })
            qc.invalidateQueries({ queryKey: ["sharing"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy("")
        }
    }
    const hide = (p: DevicePrompt) => {
        const next = [...hidden.filter(h => !h.startsWith(`${p.id}@`)), `${p.id}@${p.version}`]
        setHidden(next)
        try {
            localStorage.setItem(HIDDEN_KEY, JSON.stringify(next))
        } catch {
            /* private window: hidden until the page is reloaded */
        }
    }

    return (
        <div className="flex flex-col gap-3 px-6 pt-6 md:px-8 xl:px-10">
            {prompts.map(p => {
                const tvApp = p.platform === "android"
                const isNew = p.kind === "new"
                return (
                    <div key={`${p.kind}:${p.id}`} className="card flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4 rise-in">
                        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-fg/80 [&>svg]:size-[18px]">
                            {isNew ? tvApp ? <Tv /> : <MonitorSmartphone /> : <ArrowUpCircle />}
                        </span>
                        <div className="min-w-0 flex-1">
                            <p className="font-medium">
                                {isNew ? `Share this library with ${p.name}?` : p.kind === "older" ? `${p.name} runs an older Aniko` : `${p.name} runs a newer Aniko`}
                            </p>
                            <p className="mt-0.5 text-sm text-muted">
                                {isNew
                                    ? `Aniko${tvApp ? "'s TV app" : ""} on ${p.name} is on this network. Once shared, it can play this computer's anime.`
                                    : p.kind === "older"
                                      ? `Aniko ${p.version} there, ${status.version} here: update it on ${p.name} (from its Home page) so everything works together.`
                                      : `Aniko ${p.version} there, ${status.version} here: update this one (Settings › Updates & about) so everything works together.`}
                            </p>
                        </div>
                        {isNew ? (
                            <div className="flex gap-2">
                                <Button size="sm" variant="ghost" disabled={busy === p.id} onClick={() => answer(p, false)}>
                                    Not now
                                </Button>
                                <Button size="sm" variant="primary" loading={busy === p.id} onClick={() => answer(p, true)}>
                                    Share my library
                                </Button>
                            </div>
                        ) : (
                            <Button size="sm" variant="ghost" onClick={() => hide(p)}>
                                OK
                            </Button>
                        )}
                    </div>
                )
            })}
        </div>
    )
}
