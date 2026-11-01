import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowDown, ArrowUp, CheckCircle2, Download, ExternalLink, Link2, RefreshCw, Search, Settings2, ShieldAlert, Trash2, UserRound } from "lucide-react"
import { useMemo, useState } from "react"
import { Badge, Button, Dialog, EmptyState, IconButton, Input, Select, Switch } from "@/components/ui"
import { api, ApiError } from "@/lib/api"
import { openExternal } from "@/lib/play"
import { invalidateProviders, useStatus, useStremioAddons, useStremioCatalog } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { StremioAddon, StremioImportResult, StremioManifest } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ExtIcon, LanNote, TabSection, TypeBadges } from "./parts"

// The resources of an addon KumoBox uses.
function serves(m: StremioManifest) {
    const names = resourceNames(m)
    return { streams: names.includes("stream"), subtitles: names.includes("subtitles") }
}

// Every resource an addon serves (catalog, meta, stream, subtitles…).
const resourceNames = (m: StremioManifest) => [...new Set((m.resources ?? []).map(r => (typeof r === "string" ? r : r.name)).filter(Boolean))]

// The resources KumoBox doesn't use, said quietly next to the ones it does.
function OtherResources({ m }: { m: StremioManifest }) {
    const other = resourceNames(m).filter(r => r !== "stream" && r !== "subtitles")
    if (!other.length) return null
    return <span className="text-[11px] text-subtle">also {other.join(", ")}</span>
}

const baseUrl = (a: StremioAddon) => a.transportUrl.replace(/\/manifest\.json$/, "")

// A 409 of POST /api/providers/stremio/addons: what to ask before
// installing.
type Pending = { url: string; name?: string; code: "local" | "adult" | "configure" | "unsupported"; message: string; configureUrl?: string; allowLocal: boolean; allowAdult: boolean }

// Installing an addon, with the confirmations it may need.
function useInstall() {
    const qc = useQueryClient()
    const [pending, setPending] = useState<Pending | null>(null)
    const [busy, setBusy] = useState("")
    const install = async (url: string, opts: { allowLocal?: boolean; allowAdult?: boolean; name?: string } = {}) => {
        setBusy(url)
        try {
            const a = await api.post<StremioAddon>("/api/providers/stremio/addons", { url, allowLocal: !!opts.allowLocal, allowAdult: !!opts.allowAdult })
            setPending(null)
            invalidateProviders(qc)
            return a
        } catch (e: any) {
            const data = e instanceof ApiError ? e.data : null
            if (e instanceof ApiError && e.status === 409 && data?.code) {
                setPending({ url, name: opts.name, code: data.code, message: data.error, configureUrl: data.configureUrl, allowLocal: !!opts.allowLocal, allowAdult: !!opts.allowAdult })
            } else toast.error(e.message)
        } finally {
            setBusy("")
        }
    }
    return { install, pending, setPending, busy }
}

export function StremioTab() {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    const { install, pending, setPending, busy } = useInstall()
    const [url, setUrl] = useState("")
    return (
        <div className="flex flex-col gap-10">
            {lan ? (
                <LanNote />
            ) : (
                <div className="card flex flex-col gap-3 p-4">
                    <p className="text-sm font-medium">Install from URL</p>
                    <div className="flex gap-2">
                        <Input
                            value={url}
                            onChange={e => setUrl(e.target.value)}
                            onKeyDown={e => e.key === "Enter" && url.trim() && install(url.trim()).then(a => a && setUrl(""))}
                            placeholder="https://addon.example/manifest.json or stremio://…"
                            icon={<Link2 className="size-4" />}
                        />
                        <Button variant="primary" loading={busy === url.trim() && !!url.trim()} disabled={!url.trim()} onClick={() => install(url.trim()).then(a => a && setUrl(""))}>
                            Install
                        </Button>
                    </div>
                    <p className="text-xs text-subtle">The manifest URL, the addon's address or a stremio:// link. Addons configured on their own page keep their settings in the URL they give you.</p>
                </div>
            )}
            {!lan && <ImportAccount />}
            <Installed lan={lan} />
            <Browse lan={lan} install={install} busy={busy} />
            {pending && (
                <ConfirmDialog
                    pending={pending}
                    onClose={() => setPending(null)}
                    onConfirm={p => install(p.url, p).then(a => a && p.url === url.trim() && setUrl(""))}
                    busy={busy === pending.url}
                />
            )}
        </div>
    )
}

// Importing the addons of a Stremio account: KumoBox signs in once, installs
// those it can use and says why it left the others out. The password isn't
// kept (here or on the server).
function ImportAccount() {
    const qc = useQueryClient()
    const [open, setOpen] = useState(false)
    const [email, setEmail] = useState("")
    const [password, setPassword] = useState("")
    const [busy, setBusy] = useState(false)
    const [result, setResult] = useState<StremioImportResult | null>(null)
    const go = async () => {
        setBusy(true)
        try {
            const res = await api.post<StremioImportResult>("/api/providers/stremio/import", { email: email.trim(), password })
            setResult(res)
            setPassword("")
            invalidateProviders(qc)
            if (!res.added.length) toast.info("Nothing new to install from this account")
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    if (!open)
        return (
            <div className="card flex items-center justify-between gap-4 p-4">
                <div>
                    <p className="text-sm font-medium">Import from your Stremio account</p>
                    <p className="text-xs text-subtle">Install the addons you use in Stremio. KumoBox signs in once and doesn't keep your password.</p>
                </div>
                <Button size="sm" icon={<UserRound className="size-4" />} onClick={() => setOpen(true)}>
                    Import
                </Button>
            </div>
        )
    return (
        <div className="card flex flex-col gap-3 p-4">
            <p className="text-sm font-medium">Import from your Stremio account</p>
            <form
                className="flex flex-wrap gap-2"
                onSubmit={e => {
                    e.preventDefault()
                    if (email.trim() && password) go()
                }}
            >
                <Input className="min-w-56 flex-1" type="email" autoComplete="username" value={email} onChange={e => setEmail(e.target.value)} placeholder="Email" />
                <Input className="min-w-56 flex-1" type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} placeholder="Password" />
                <Button type="submit" variant="primary" loading={busy} disabled={!email.trim() || !password}>
                    Import addons
                </Button>
                <Button type="button" variant="ghost" onClick={() => (setOpen(false), setResult(null), setPassword(""))}>
                    Close
                </Button>
            </form>
            <p className="text-xs text-subtle">
                Addons serving streams or subtitles are installed. Those running on your computer or network, adult ones and those to configure are left out: install them by their URL to confirm. Your password is only sent to Stremio.
            </p>
            {result && (
                <div className="flex flex-col gap-2 text-[13px]">
                    {result.added.length > 0 && (
                        <p className="flex flex-wrap items-center gap-1.5">
                            <CheckCircle2 className="size-4 text-emerald-300" /> Installed: {result.added.map(a => a.name).join(", ")}
                        </p>
                    )}
                    {result.skipped.map(a => (
                        <p key={a.transportUrl} className="flex items-start gap-1.5 text-muted">
                            <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-300" />
                            <span>
                                <span className="font-medium text-fg/85">{a.name}</span>: {a.reason}
                            </span>
                        </p>
                    ))}
                </div>
            )}
        </div>
    )
}

function ConfirmDialog({ pending: p, onClose, onConfirm, busy }: { pending: Pending; onClose: () => void; onConfirm: (p: Pending) => void; busy: boolean }) {
    const [ok, setOk] = useState(false)
    const title = { local: "An addon on your network", adult: "An adult addon", configure: "Configure this addon first", unsupported: "KumoBox can't use this addon" }[p.code]
    return (
        <Dialog
            open
            onOpenChange={v => !v && onClose()}
            title={p.name ? `${title}: ${p.name}` : title}
            footer={
                p.code === "local" || p.code === "adult" ? (
                    <>
                        <Button variant="ghost" onClick={onClose}>
                            Cancel
                        </Button>
                        <Button
                            variant="primary"
                            disabled={!ok}
                            loading={busy}
                            onClick={() => onConfirm({ ...p, allowLocal: p.allowLocal || p.code === "local", allowAdult: p.allowAdult || p.code === "adult" })}
                        >
                            Install anyway
                        </Button>
                    </>
                ) : p.code === "configure" && p.configureUrl ? (
                    <Button variant="primary" icon={<ExternalLink className="size-4" />} onClick={() => openExternal(p.configureUrl!)}>
                        Open the configuration page
                    </Button>
                ) : (
                    <Button onClick={onClose}>OK</Button>
                )
            }
        >
            <div className="flex flex-col gap-4 text-sm">
                <p className="flex items-start gap-2 text-muted">
                    <ShieldAlert className="mt-0.5 size-4 shrink-0 text-amber-300" />
                    {p.message.charAt(0).toUpperCase() + p.message.slice(1)}.
                </p>
                {p.code === "configure" && <p className="text-muted">Configure it there, then paste the install URL it gives you in “Install from URL”.</p>}
                {(p.code === "local" || p.code === "adult") && (
                    <label className="flex items-center gap-2.5">
                        <input type="checkbox" checked={ok} onChange={e => setOk(e.target.checked)} className="size-4 accent-[var(--brand)]" />
                        {p.code === "local" ? "I trust this addon running on this computer or my network" : "Install this addon flagged as adult content"}
                    </label>
                )}
                <p className="truncate text-xs text-subtle" title={p.url}>
                    {p.url}
                </p>
            </div>
        </Dialog>
    )
}

// ---------------------------------------------------------------------------

function Installed({ lan }: { lan: boolean }) {
    const { data, isLoading } = useStremioAddons()
    const qc = useQueryClient()
    const [refreshing, setRefreshing] = useState(false)
    const addons = data ?? []
    const call = async (p: Promise<unknown>, ok?: string) => {
        try {
            await p
            if (ok) toast.success(ok)
            invalidateProviders(qc)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const move = (i: number, d: -1 | 1) => {
        const keys = addons.map(a => a.transportUrl)
        ;[keys[i], keys[i + d]] = [keys[i + d], keys[i]]
        call(api.put("/api/providers/stremio/addons/order", { keys }))
    }
    const refresh = async () => {
        setRefreshing(true)
        try {
            const res = await api.post<{ errors: Record<string, string> }>("/api/providers/stremio/refresh")
            const n = Object.keys(res.errors ?? {}).length
            if (n) toast.warning(`${n} addon(s) couldn't be reached: they keep their manifest`)
            else toast.success("Addons refreshed")
            invalidateProviders(qc)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setRefreshing(false)
        }
    }
    // Devices on the LAN see the addons; this computer orders, refreshes,
    // turns on or off and removes them.
    return (
        <TabSection
            title="Installed addons"
            description="Asked in this order, their streams listed in it when they're as good. Addons serving subtitles add theirs to the direct links."
            actions={
                addons.length > 0 &&
                !lan && (
                    <Button size="sm" icon={<RefreshCw className={cn("size-4", refreshing && "animate-spin")} />} disabled={refreshing} onClick={refresh}>
                        Refresh
                    </Button>
                )
            }
        >
            {isLoading ? (
                <div className="card h-24 shimmer" />
            ) : addons.length === 0 ? (
                <EmptyState icon={<Download className="size-6" />} title="No Stremio addons yet">
                    Install one from a collection below, or paste its URL.
                </EmptyState>
            ) : (
                <div className="card divide-y divide-line overflow-hidden">
                    {addons.map((a, i) => {
                        const m = a.manifest
                        const r = serves(m)
                        return (
                            <div key={a.transportUrl} className={cn("flex items-center gap-4 px-4 py-3", !a.enabled && "opacity-60")}>
                                <ExtIcon src={m.logo} className="size-11" />
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-2">
                                        <p className="truncate font-semibold">{m.name}</p>
                                        <span className="text-xs text-subtle">v{m.version}</span>
                                    </div>
                                    {m.description && <p className="line-clamp-1 text-[13px] text-muted">{m.description}</p>}
                                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                                        <TypeBadges types={m.types ?? []} />
                                        {r.streams && <Badge tone="brand">Streams</Badge>}
                                        {r.subtitles && <Badge tone="blue">Subtitles</Badge>}
                                        {m.behaviorHints?.p2p && <Badge>Torrents</Badge>}
                                        {a.local && <Badge tone="amber">On your network</Badge>}
                                        {m.behaviorHints?.adult && <Badge tone="red">Adult</Badge>}
                                        {!a.supported && <Badge tone="amber">Not supported</Badge>}
                                        <OtherResources m={m} />
                                    </div>
                                </div>
                                <div className="flex shrink-0 items-center gap-1">
                                    {!lan && (
                                        <>
                                            <IconButton size="sm" label="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
                                                <ArrowUp className="size-4" />
                                            </IconButton>
                                            <IconButton size="sm" label="Move down" disabled={i === addons.length - 1} onClick={() => move(i, 1)}>
                                                <ArrowDown className="size-4" />
                                            </IconButton>
                                        </>
                                    )}
                                    {m.behaviorHints?.configurable && !lan && (
                                        <IconButton size="sm" label="Configure (then install the URL it gives you)" onClick={() => openExternal(baseUrl(a) + "/configure")}>
                                            <Settings2 className="size-4" />
                                        </IconButton>
                                    )}
                                    {!lan && (
                                        <IconButton
                                            size="sm"
                                            label="Uninstall"
                                            className="hover:text-rose-300"
                                            onClick={() => confirm(`Uninstall ${m.name}?`) && call(api.del(`/api/providers/stremio/addons?key=${encodeURIComponent(a.transportUrl)}`), `Uninstalled ${m.name}`)}
                                        >
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    )}
                                    <span className="ml-2">
                                        <Switch checked={a.enabled} disabled={lan} onChange={v => call(api.put("/api/providers/stremio/addons/enabled", { key: a.transportUrl, enabled: v }))} />
                                    </span>
                                </div>
                            </div>
                        )
                    })}
                </div>
            )}
        </TabSection>
    )
}

// ---------------------------------------------------------------------------

// The addons of the collections of Settings › Providers: search them, see
// what each serves, install or uninstall one, open its configuration page.
function Browse({ lan, install, busy }: { lan: boolean; install: (url: string, opts?: { name?: string }) => Promise<StremioAddon | undefined>; busy: string }) {
    const { data: status } = useStatus()
    const qc = useQueryClient()
    const catalogs = status?.settings.providers?.stremioCatalogs ?? []
    const [url, setUrl] = useState("")
    const effective = url || catalogs[0] || ""
    const { data, isLoading, error, refetch, isFetching } = useStremioCatalog(effective)
    const [reloading, setReloading] = useState(false)
    // Downloaded again on this computer; devices on the LAN read it as it
    // was last downloaded.
    const reload = async () => {
        if (lan) return void refetch()
        setReloading(true)
        try {
            qc.setQueryData(["stremio-catalog", effective], await api.post("/api/providers/stremio/catalog/refresh", { url: effective }))
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setReloading(false)
        }
    }
    const [q, setQ] = useState("")
    const [all, setAll] = useState(false)
    const [removing, setRemoving] = useState("")
    const addons = useMemo(() => {
        let list = data?.addons ?? []
        if (!all) list = list.filter(a => a.supported && serves(a.manifest).streams)
        if (q.trim()) {
            const s = q.toLowerCase()
            list = list.filter(a => a.manifest.name.toLowerCase().includes(s) || a.manifest.description?.toLowerCase().includes(s) || a.manifest.id.toLowerCase().includes(s))
        }
        return list
    }, [data, q, all])
    // Removed by its manifest id: the installed copy may be another
    // configuration of the addon listed.
    const uninstall = async (a: StremioAddon) => {
        if (!confirm(`Uninstall ${a.manifest.name}?`)) return
        setRemoving(a.transportUrl)
        try {
            await api.del(`/api/providers/stremio/addons?key=${encodeURIComponent("stremio:" + a.manifest.id)}`)
            toast.success(`Uninstalled ${a.manifest.name}`)
            invalidateProviders(qc)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setRemoving("")
        }
    }
    return (
        <TabSection
            title="Browse"
            description="Addon collections (Settings › Providers lists them). Addons that only offer catalogs or metadata are of no use to KumoBox."
            actions={
                <Button size="sm" icon={<RefreshCw className={cn("size-4", (isFetching || reloading) && "animate-spin")} />} disabled={reloading} onClick={reload}>
                    Reload
                </Button>
            }
        >
            <div className="flex flex-wrap items-center gap-3">
                {catalogs.length > 1 && <Select className="w-96" value={effective} onChange={setUrl} options={catalogs.map(c => ({ value: c, label: c }))} />}
                <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Search addons…" icon={<Search className="size-4" />} className="w-72" />
                <label className="flex items-center gap-2 text-sm text-muted">
                    <Switch checked={all} onChange={setAll} /> Show all addons
                </label>
            </div>
            {isLoading && (
                <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                    {Array.from({ length: 6 }).map((_, i) => (
                        <div key={i} className="card h-36 shimmer" />
                    ))}
                </div>
            )}
            {error && (
                <EmptyState icon={<AlertTriangle className="size-6" />} title="Couldn't load the collection" action={<Button onClick={() => refetch()}>Retry</Button>}>
                    {(error as Error).message}
                </EmptyState>
            )}
            {data && addons.length === 0 && <p className="py-6 text-sm text-muted">No addon here{q ? " matches your search" : " serves streams"}.</p>}
            <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                {addons.map(a => {
                    const m = a.manifest
                    const r = serves(m)
                    return (
                        <div key={a.transportUrl} className={cn("card flex flex-col gap-3 p-4", !a.supported && "opacity-60")}>
                            <div className="flex items-start gap-3">
                                <ExtIcon src={m.logo} className="size-11" />
                                <div className="min-w-0 flex-1">
                                    <p className="truncate font-semibold">{m.name}</p>
                                    <p className="truncate text-xs text-subtle">v{m.version}</p>
                                </div>
                                {a.flags?.official && <Badge tone="blue">Official</Badge>}
                            </div>
                            <p className="line-clamp-2 min-h-10 text-sm text-muted">{m.description}</p>
                            <div className="flex flex-wrap gap-1.5">
                                <TypeBadges types={m.types ?? []} />
                                {r.streams && <Badge tone="brand">Streams</Badge>}
                                {r.subtitles && <Badge tone="blue">Subtitles</Badge>}
                                {m.behaviorHints?.configurationRequired && <Badge tone="amber">Needs configuring</Badge>}
                                {m.behaviorHints?.adult && <Badge tone="red">Adult</Badge>}
                                {!a.supported && <Badge tone="amber">Not supported</Badge>}
                                <OtherResources m={m} />
                            </div>
                            <div className="mt-auto flex items-center gap-2 pt-1">
                                {a.installed ? (
                                    <>
                                        <Badge tone="green">
                                            <CheckCircle2 className="size-3" /> Installed
                                        </Badge>
                                        {!lan && (
                                            <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} loading={removing === a.transportUrl} onClick={() => uninstall(a)}>
                                                Uninstall
                                            </Button>
                                        )}
                                    </>
                                ) : (
                                    !lan &&
                                    a.supported && (
                                        <Button size="sm" variant="white" icon={<Download className="size-4" />} loading={busy === a.transportUrl} onClick={() => install(a.transportUrl, { name: m.name })}>
                                            Install
                                        </Button>
                                    )
                                )}
                                {m.behaviorHints?.configurable && !lan && (
                                    <Button size="sm" variant="ghost" icon={<Settings2 className="size-4" />} onClick={() => openExternal(baseUrl(a) + "/configure")}>
                                        Configure
                                    </Button>
                                )}
                            </div>
                        </div>
                    )
                })}
            </div>
        </TabSection>
    )
}
