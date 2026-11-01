import { useQueryClient } from "@tanstack/react-query"
import { Activity, AlertTriangle, CheckCircle2, CircleDashed, FlaskConical } from "lucide-react"
import { useState } from "react"
import { Badge, Button, Tooltip } from "@/components/ui"
import { api } from "@/lib/api"
import { useProviderHealth } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { ProviderHealth, ProviderTestResult } from "@/lib/types"
import { relativeTime } from "@/lib/utils"
import { Group } from "./parts"

// How each streaming provider's searches went: answers and failures, the
// last error, how long it takes. A provider failing 3 times in a row is
// listed last and left out of automatic picks until it answers again; its
// Test button asks it right away.
export function ProviderHealthGroup({ lan }: { lan: boolean }) {
    const { data } = useProviderHealth()
    const list = (data ?? []).filter(p => p.enabled || p.successes + p.failures > 0)
    return (
        <Group
            title="Provider health"
            description="How each provider's searches went lately. One failing 3 times in a row is listed last and skipped when KumoBox picks a stream by itself, until it answers again. Test asks it for Night of the Living Dead (1968), a public-domain film."
        >
            {list.length === 0 && <p className="px-4 py-3.5 text-sm text-subtle">No streaming provider is enabled.</p>}
            {list.map(p => (
                <HealthRow key={p.id} p={p} lan={lan} />
            ))}
        </Group>
    )
}

function HealthRow({ p, lan }: { p: ProviderHealth; lan: boolean }) {
    const qc = useQueryClient()
    const [busy, setBusy] = useState(false)
    const asked = p.successes + p.failures
    const test = async () => {
        setBusy(true)
        try {
            const r = await api.post<ProviderTestResult>(`/api/providers/${encodeURIComponent(p.id)}/test`)
            const took = `${(r.ms / 1000).toFixed(1)} s`
            if (r.error) toast.error(`${p.name}: ${r.error} (${took})`)
            else if (r.streams === 0) toast.info(`${p.name} answered in ${took}, with nothing for ${r.title}`)
            else toast.success(`${p.name} found ${r.streams} source${r.streams === 1 ? "" : "s"} (${r.direct} direct) for ${r.title} in ${took}`)
            qc.invalidateQueries({ queryKey: ["provider-health"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    const status = !asked ? (
        <Badge>
            <CircleDashed className="size-3" /> Not asked yet
        </Badge>
    ) : p.demoted ? (
        <Badge tone="red">
            <AlertTriangle className="size-3" /> Failing: listed last
        </Badge>
    ) : p.consecutiveFailures > 0 ? (
        <Badge tone="amber">
            <AlertTriangle className="size-3" /> Failed last time
        </Badge>
    ) : (
        <Badge tone="green">
            <CheckCircle2 className="size-3" /> Working
        </Badge>
    )
    return (
        <div className="flex items-center justify-between gap-6 px-4 py-3.5">
            <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                    <p className="text-sm font-medium">{p.name}</p>
                    {status}
                    {!p.enabled && <Badge>Disabled</Badge>}
                </div>
                <p className="mt-0.5 flex flex-wrap items-center gap-x-3 text-xs text-subtle">
                    {asked > 0 && (
                        <span>
                            {p.successes} answered · {p.failures} failed
                        </span>
                    )}
                    {p.avgMs > 0 && (
                        <span className="flex items-center gap-1">
                            <Activity className="size-3" /> {(p.avgMs / 1000).toFixed(1)} s on average
                        </span>
                    )}
                    {!!p.lastSuccessAt && <span>last answer {relativeTime(p.lastSuccessAt)}</span>}
                </p>
                {p.lastError && p.consecutiveFailures > 0 && (
                    <Tooltip content={p.lastError}>
                        <p className="mt-0.5 line-clamp-1 text-xs break-all text-amber-200/80">{p.lastError}</p>
                    </Tooltip>
                )}
            </div>
            {!lan && p.enabled && (
                <Button size="sm" variant="subtle" loading={busy} icon={<FlaskConical className="size-4" />} onClick={test}>
                    Test
                </Button>
            )}
        </div>
    )
}
