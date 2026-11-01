import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ShieldCheck, Wifi } from "lucide-react"
import { useState } from "react"
import { ParentalDialog } from "@/components/ParentalDialog"
import { Badge, Button, EmptyState, Select, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import type { HubDevice, HubOverview } from "@/lib/types"
import { cn, compareVersions, img } from "@/lib/utils"
import { DeviceIcon } from "./Overview"
import { ago, OnlineDot } from "./parts"

// The Aniko apps around the hub: which get its library, which AniList login
// each is given, which may download onto it.

type Change = { allowed?: boolean; account?: boolean; downloads?: boolean; accountUser?: number }

export default function Devices({ o }: { o: HubOverview }) {
    const qc = useQueryClient()
    const save = useMutation({
        mutationFn: ({ id, change }: { id: string; change: Change }) => api.post<HubDevice[]>(`/api/hub/devices/${encodeURIComponent(id)}`, change),
        onSuccess: devices => {
            qc.setQueryData<HubOverview>(["hub", "overview"], old => (old ? { ...old, devices } : old))
            qc.invalidateQueries({ queryKey: ["hub", "overview"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const change = (d: HubDevice, c: Change) => save.mutate({ id: d.id, change: c })
    const waiting = o.devices.filter(d => !d.allowed)
    const shared = o.devices.filter(d => d.allowed)
    return (
        <div className="flex flex-col gap-6">
            <p className="max-w-3xl text-sm leading-relaxed text-muted">
                A device shows up here when Aniko is open on it with <span className="text-fg">Settings › Other devices</span> on. Share the library with it, and it plays the hub's episodes and
                keeps its progress, AniList list and theme in step with the other devices on the same AniList account, as they change.
            </p>
            {o.devices.length === 0 && (
                <EmptyState icon={<Wifi />} title="No devices yet">
                    Open Aniko on a computer, TV or phone on this network and turn on Settings › Other devices.
                </EmptyState>
            )}
            {waiting.length > 0 && (
                <section className="flex flex-col gap-3">
                    <h2 className="text-[15px] font-semibold">Not sharing with</h2>
                    {waiting.map(d => (
                        <DeviceCard key={d.id} d={d} o={o} change={change} />
                    ))}
                </section>
            )}
            {shared.length > 0 && (
                <section className="flex flex-col gap-3">
                    <h2 className="text-[15px] font-semibold">Sharing with</h2>
                    {shared.map(d => (
                        <DeviceCard key={d.id} d={d} o={o} change={change} />
                    ))}
                </section>
            )}
        </div>
    )
}

function DeviceCard({ d, o, change }: { d: HubDevice; o: HubOverview; change: (d: HubDevice, c: Change) => void }) {
    const on = o.accounts.find(a => a.user === d.user)
    const given = o.accounts.find(a => a.user === d.accountUser) ?? o.accounts[0]
    const older = compareVersions(d.version, o.version) === -1
    return (
        <div className={cn("card overflow-hidden", !d.allowed && d.online && "border-brand/40")}>
            <div className="flex items-center gap-3 px-4 py-3.5">
                <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.05]">
                    <DeviceIcon d={d} className="size-5 text-muted" />
                </span>
                <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 text-[15px] font-medium">
                        <span className="truncate">{d.name}</span>
                        <OnlineDot online={d.online} />
                    </p>
                    <p className="truncate text-[12.5px] text-muted">
                        {d.online ? "Online" : `Last seen ${ago(d.lastSeen * 1000)}`}
                        {d.address && ` · ${d.address}`}
                        {d.version && ` · Aniko ${d.version}`}
                    </p>
                </div>
                {older && <Badge tone="amber">Older Aniko</Badge>}
                {d.error && d.allowed && (
                    <Badge tone="red" className="max-sm:hidden">
                        Not answering
                    </Badge>
                )}
            </div>
            <div className="divide-y divide-line border-t border-line">
                <Setting label="Share the library" help="It plays the hub's episodes, and its progress and list stay in step through the hub.">
                    <Switch label={`Share the library with ${d.name}`} checked={d.allowed} onChange={v => change(d, { allowed: v })} />
                </Setting>
                {d.allowed && (
                    <>
                        <Setting
                            label="Give it an AniList login"
                            help={
                                d.account && given
                                    ? `It uses ${given.name}'s login, from the hub.`
                                    : o.accounts.length === 0
                                      ? "Give the hub an AniList login first (from a device that's logged in: Settings › Other devices › this hub)."
                                      : on
                                        ? `It's logged in to ${on.name}'s account itself.`
                                        : d.user
                                          ? "It's logged in to an AniList account the hub has no login for."
                                          : "It's not logged in to AniList: with this, it uses the login you pick, without logging in on it."
                            }
                        >
                            <div className="flex items-center gap-2">
                                {d.account && o.accounts.length > 1 && (
                                    <Select
                                        className="w-40"
                                        value={String(given?.user ?? o.accounts[0].user)}
                                        onChange={v => change(d, { accountUser: Number(v) })}
                                        options={o.accounts.map(a => ({ value: String(a.user), label: a.name }))}
                                    />
                                )}
                                {d.account && o.accounts.length === 1 && (
                                    <span className="flex items-center gap-1.5 text-sm text-muted">
                                        {o.accounts[0].avatar && <img src={img(o.accounts[0].avatar)} alt="" className="size-5 rounded-full" />}
                                        {o.accounts[0].name}
                                    </span>
                                )}
                                <Switch label={`Give ${d.name} an AniList login`} checked={d.account} disabled={o.accounts.length === 0} onChange={v => change(d, { account: v })} />
                            </div>
                        </Setting>
                        <Setting label="Let it download here" help="Its downloads and torrents go onto the hub, into its library, and it can upload files and folders into it.">
                            <Switch label={`Let ${d.name} download here`} checked={d.downloads} onChange={v => change(d, { downloads: v })} />
                        </Setting>
                        <ParentalSetting d={d} />
                    </>
                )}
            </div>
        </div>
    )
}

function ParentalSetting({ d }: { d: HubDevice }) {
    const [open, setOpen] = useState(false)
    return (
        <Setting label="Parental controls" help={d.parental ? "On: what it may watch is limited, and what it watches is kept here." : "Block 18+ anime, genres or titles on it, or allow only some; see what it watched."}>
            <div className="flex items-center gap-2">
                {d.parental && <Badge tone="green">On</Badge>}
                <Button size="sm" icon={<ShieldCheck className="size-4" />} onClick={() => setOpen(true)}>
                    {d.parental ? "Change" : "Set up"}
                </Button>
            </div>
            <ParentalDialog device={d} host="the hub" open={open} onOpenChange={setOpen} />
        </Setting>
    )
}

function Setting({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
    return (
        <div className="flex items-center justify-between gap-x-6 gap-y-2 px-4 py-3 max-sm:flex-wrap">
            <div className="min-w-0 max-sm:flex-1 max-sm:basis-48">
                <p className="text-sm font-medium">{label}</p>
                {help && <p className="mt-0.5 text-[12.5px] leading-snug text-muted">{help}</p>}
            </div>
            <div className="shrink-0">{children}</div>
        </div>
    )
}
