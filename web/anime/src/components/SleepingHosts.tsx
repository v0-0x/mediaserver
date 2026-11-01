import { useQueryClient } from "@tanstack/react-query"
import { Moon } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui"
import { api } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"

// Home: a computer that shares its library with this device doesn't answer
// (asleep, or off), so its anime aren't here; one that can be woken up from
// here (Wake-on-LAN) gets a button for it.
export function SleepingHosts() {
    const { data: status } = useStatus()
    const qc = useQueryClient()
    const [busy, setBusy] = useState("")
    const hosts = status?.asleep ?? []
    if (!hosts.length || status?.client === "lan") return null
    const wake = async (id: string, name: string) => {
        setBusy(id)
        try {
            await api.post(`/api/sharing/libraries/${encodeURIComponent(id)}/wake`)
            toast.info(`Waking ${name}… its library comes back when it answers.`)
            qc.invalidateQueries({ queryKey: ["status"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy("")
        }
    }
    return (
        <div className="flex flex-col gap-3 px-6 pt-6 md:px-8 xl:px-10">
            {hosts.map(h => (
                <div key={h.id} className="card flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4 rise-in">
                    <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-fg/80 [&>svg]:size-[18px]">
                        <Moon />
                    </span>
                    <div className="min-w-0 flex-1">
                        <p className="font-medium">{h.waking ? `Waking ${h.name}…` : `${h.name} is asleep or off`}</p>
                        <p className="mt-0.5 text-sm text-muted">
                            {h.waking
                                ? "Its library shows up here as soon as it answers."
                                : h.canWake
                                  ? "Its library isn't here until it's on. It can be woken up from here."
                                  : `Its library isn't here until it's on. To wake it from here, update Aniko on ${h.name} and turn on Wake-on-LAN in its settings (BIOS).`}
                        </p>
                    </div>
                    {h.canWake && (
                        <Button size="sm" variant="primary" icon={<Moon className="size-4" />} loading={busy === h.id || h.waking} onClick={() => wake(h.id, h.name)}>
                            Wake {h.name}
                        </Button>
                    )}
                </div>
            ))}
        </div>
    )
}
