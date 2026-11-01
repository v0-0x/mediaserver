import { useQuery } from "@tanstack/react-query"
import { Check, Copy, KeyRound, Network, RefreshCw } from "lucide-react"
import { useState } from "react"
import { Badge, Button, Input, Progress, Select, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { useApplyUpdate, useCheckUpdate, useSaveSettings, useUpdateStatus } from "@/lib/queries"
import { COLOR_THEMES } from "@/lib/themes"
import { toast } from "@/lib/toast"
import type { HubOverview, Settings, Status, TorrentClientConfig } from "@/lib/types"
import { copyText, relativeTime } from "@/lib/utils"
import { Panel } from "./parts"

// The hub's own settings: its name, the dashboard's password and colors,
// how it converts videos, its updates.

export default function HubSettings({ o, status }: { o: HubOverview; status: Status }) {
    const settings = status.settings
    const save = useSaveSettings()
    const set = <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => save.mutate({ ...settings, [k]: { ...settings[k], ...v } })
    return (
        <div className="grid gap-5 lg:grid-cols-2">
            <div className="flex min-w-0 flex-col gap-5">
                <Name settings={settings} current={o.name} set={set} />
                <Password />
                <Panel title="Address">
                    <div className="flex flex-col gap-2 p-4">
                        <p className="text-[12.5px] text-muted">Open the dashboard from any browser on the network at:</p>
                        {(o.addresses?.length ? o.addresses : [`http://${status.hostname}.local:${o.port}`]).map(a => (
                            <CopyLine key={a} text={a} />
                        ))}
                    </div>
                </Panel>
                <Panel title="Dashboard colors">
                    <Row label="Colors" help="Of this dashboard only: each device keeps its own, in step with the others on its AniList account.">
                        <Select
                            className="w-40"
                            value={settings.ui.colorTheme || "aniko"}
                            onChange={v => set("ui", { colorTheme: v })}
                            options={COLOR_THEMES.map(t => ({ value: t.id, label: t.name }))}
                        />
                    </Row>
                </Panel>
            </div>
            <div className="flex min-w-0 flex-col gap-5">
                <Updates settings={settings} set={set} />
                <Downloads settings={settings} />
                <Panel title="Video conversion">
                    <p className="px-4 py-3.5 text-[12.5px] leading-relaxed text-muted">
                        Most videos go to the devices as they are, which the hub does with almost no work. One a device can't play (some TVs and phones can't play HEVC or 10-bit
                        video) is converted as it plays, at most 2 at a time.
                    </p>
                    <Row label="Converting with">
                        {o.encoder ? (
                            <Badge tone={o.encoder === "h264_v4l2m2m" ? "green" : "gray"}>{encoderName(o.encoder)}</Badge>
                        ) : (
                            <span className="text-[12.5px] text-amber-300">Nothing: ffmpeg isn't installed (sudo apt install ffmpeg)</span>
                        )}
                    </Row>
                    <Row label="Hardware encoder" help="The Raspberry Pi's (V4L2) uses its video chip: far less CPU than converting on the processor.">
                        <Select
                            className="w-48"
                            value={settings.transcode.hwAccel}
                            onChange={v => set("transcode", { hwAccel: v })}
                            options={[
                                { value: "", label: "None (the processor)" },
                                { value: "v4l2m2m", label: "V4L2 (Raspberry Pi)" },
                                { value: "vaapi", label: "VA-API (AMD / Intel)" },
                                { value: "nvenc", label: "NVENC (NVIDIA)" },
                                { value: "qsv", label: "Quick Sync (Intel)" },
                            ]}
                        />
                    </Row>
                </Panel>
            </div>
        </div>
    )
}

type SetFn = <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => void

function Name({ settings, current, set }: { settings: Settings; current: string; set: SetFn }) {
    // Unset, it's the computer's name.
    const [name, setName] = useState(settings.sharing.name || current)
    const changed = name.trim() !== (settings.sharing.name || current) && name.trim() !== ""
    return (
        <Panel title="Name">
            <form
                className="flex flex-col gap-2 p-4"
                onSubmit={e => {
                    e.preventDefault()
                    if (changed) set("sharing", { name: name.trim() })
                }}
            >
                <p className="text-[12.5px] text-muted">What the devices call the hub (Settings › Other devices).</p>
                <div className="flex gap-2">
                    <Input value={name} maxLength={40} onChange={e => setName(e.target.value)} />
                    <Button type="submit" variant="primary" disabled={!changed}>
                        Save
                    </Button>
                </div>
            </form>
        </Panel>
    )
}

function Password() {
    const [pw, setPw] = useState("")
    const [busy, setBusy] = useState(false)
    const submit = async () => {
        setBusy(true)
        try {
            await api.post("/api/hub/password", { password: pw })
            setPw("")
            toast.success("The password is changed: other browsers sign in again")
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Panel title="Dashboard password">
            <form
                className="flex flex-col gap-2 p-4"
                onSubmit={e => {
                    e.preventDefault()
                    if (pw.length >= 6) submit()
                }}
            >
                <p className="text-[12.5px] text-muted">A new password signs out the other browsers. At least 6 characters.</p>
                <div className="flex gap-2">
                    <Input type="password" autoComplete="new-password" icon={<KeyRound className="size-4" />} placeholder="New password" value={pw} onChange={e => setPw(e.target.value)} />
                    <Button type="submit" loading={busy} disabled={pw.length < 6}>
                        Change
                    </Button>
                </div>
            </form>
        </Panel>
    )
}

function Updates({ settings, set }: { settings: Settings; set: SetFn }) {
    const { data: u } = useUpdateStatus()
    const check = useCheckUpdate()
    const apply = useApplyUpdate()
    const busy = !!u && u.state !== "idle" && u.state !== "failed" && u.state !== "ready"
    const beta = settings.updates?.channel === "beta"
    return (
        <Panel
            title="Updates"
            action={
                <Button size="sm" variant="ghost" icon={<RefreshCw className={u?.checking ? "size-4 animate-spin" : "size-4"} />} disabled={u?.checking || busy} onClick={() => check.mutate()}>
                    Check
                </Button>
            }
        >
            <div className="flex flex-col gap-3 p-4">
                <p className="text-sm">
                    mediaserver <span className="font-medium">{u?.current.version ?? "…"}</span>
                    {u?.current.beta && (
                        <Badge tone="amber" className="ml-2">
                            Beta
                        </Badge>
                    )}
                    {u?.checkedAt ? <span className="text-muted"> · checked {relativeTime(u.checkedAt)}</span> : null}
                </p>
                {u?.error && <p className="text-[12.5px] text-rose-300">{u.error}</p>}
                {u?.available && u.latest && !busy && (
                    <div className="flex flex-wrap items-center gap-3 rounded-lg bg-brand-soft p-3">
                        <p className="min-w-0 flex-1 text-sm">
                            <span className="font-medium">{u.latest.version}</span> {u.latest.beta ? "(beta) " : ""}is out.
                            {u.applyNote && <span className="block text-[12.5px] text-muted">{u.applyNote}</span>}
                        </p>
                        <Button variant="primary" size="sm" disabled={!u.canApply} loading={apply.isPending} onClick={() => apply.mutate()}>
                            Update now
                        </Button>
                    </div>
                )}
                {u && !u.available && !u.error && u.checkedAt > 0 && <p className="text-[12.5px] text-muted">This is the newest version.</p>}
                {busy && (
                    <div className="flex flex-col gap-1.5">
                        <p className="text-[12.5px] text-muted">{u?.message}</p>
                        <Progress value={(u?.progress ?? 0) / 100} />
                    </div>
                )}
                {u?.state === "failed" && u.message && <p className="text-[12.5px] text-rose-300">{u.message}</p>}
            </div>
            <Row label="Beta updates" help="The newest versions as soon as they're out, before they're released for everyone.">
                <Switch label="Beta updates" checked={beta} onChange={v => set("updates", { channel: v ? "beta" : "stable" })} />
            </Row>
        </Panel>
    )
}

// What devices download onto the hub with: ani-cli (episodes) and a
// torrent client on the hub (Transmission, say), each optional.
function Downloads({ settings }: { settings: Settings }) {
    const ani = useQuery({ queryKey: ["anicli", "status"], queryFn: () => api.get<{ installed: boolean; version: string }>("/api/anicli/status") })
    const save = useSaveSettings()
    const [client, setClient] = useState(settings.torrent.defaultClient)
    const [conf, setConf] = useState<TorrentClientConfig | null>(null)
    const kind = client === "qbittorrent" || client === "transmission" ? client : null
    const value = conf ?? (kind ? settings[kind] : null)
    const [testing, setTesting] = useState(false)
    const test = async () => {
        setTesting(true)
        try {
            const next = { ...settings, torrent: { ...settings.torrent, defaultClient: client } }
            if (kind && value) next[kind] = value
            await save.mutateAsync(next)
            if (!kind) return
            const st = await api.get<{ connected: boolean; version: string; error?: string }>("/api/torrent-client/status")
            st.connected ? toast.success(`Connected to ${kind === "qbittorrent" ? "qBittorrent" : "Transmission"} ${st.version}`) : toast.error(st.error ?? "Not reachable")
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setTesting(false)
        }
    }
    const field = (k: keyof TorrentClientConfig) => (e: React.ChangeEvent<HTMLInputElement>) =>
        value && setConf({ ...value, [k]: k === "port" ? Number(e.target.value) : e.target.value })
    return (
        <Panel title="Downloads">
            <p className="px-4 pt-3.5 text-[12.5px] leading-relaxed text-muted">The devices you let download here (Devices) send their downloads to the hub, into its first library folder.</p>
            <Row label="Episodes (ani-cli)" help={ani.data?.installed ? undefined : "Install the hub again (its install script installs ani-cli) for episode downloads."}>
                {ani.data ? <Badge tone={ani.data.installed ? "green" : "amber"}>{ani.data.installed ? `Installed ${ani.data.version}` : "Not installed"}</Badge> : null}
            </Row>
            <Row label="Torrents" help="A torrent client on the hub (Transmission: sudo apt install transmission-daemon).">
                <Select
                    className="w-40"
                    value={client}
                    onChange={v => {
                        setClient(v)
                        setConf(null)
                    }}
                    options={[
                        { value: "none", label: "None" },
                        { value: "transmission", label: "Transmission" },
                        { value: "qbittorrent", label: "qBittorrent" },
                    ]}
                />
            </Row>
            {kind && value && (
                <div className="grid grid-cols-2 gap-2 border-t border-line px-4 py-3.5">
                    <Input aria-label="Host" placeholder="Host" value={value.host} onChange={field("host")} />
                    <Input aria-label="Port" placeholder="Port" type="number" value={value.port || ""} onChange={field("port")} />
                    <Input aria-label="Username" placeholder="Username" value={value.username} onChange={field("username")} />
                    <Input aria-label="Password" placeholder="Password" type="password" value={value.password} onChange={field("password")} />
                </div>
            )}
            <div className="flex justify-end border-t border-line px-4 py-3">
                <Button size="sm" icon={<Network className="size-4" />} loading={testing} onClick={test}>
                    {kind ? "Save & test" : "Save"}
                </Button>
            </div>
        </Panel>
    )
}

function Row({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
    return (
        <div className="flex items-center justify-between gap-x-6 gap-y-2 border-t border-line px-4 py-3 first:border-t-0 max-sm:flex-wrap">
            <div className="min-w-0 max-sm:flex-1 max-sm:basis-48">
                <p className="text-sm font-medium">{label}</p>
                {help && <p className="mt-0.5 text-[12.5px] leading-snug text-muted">{help}</p>}
            </div>
            <div className="shrink-0">{children}</div>
        </div>
    )
}

function CopyLine({ text }: { text: string }) {
    const [copied, setCopied] = useState(false)
    return (
        <button
            onClick={async () => {
                if (await copyText(text)) {
                    setCopied(true)
                    setTimeout(() => setCopied(false), 1500)
                }
            }}
            className="focus-ring flex items-center gap-2 rounded-lg bg-white/[0.04] px-3 py-2 text-left font-mono text-[13px] hover:bg-white/[0.07]"
        >
            <span className="min-w-0 flex-1 truncate">{text}</span>
            {copied ? <Check className="size-4 text-emerald-400" /> : <Copy className="size-4 text-muted" />}
        </button>
    )
}

function encoderName(e: string) {
    return (
        {
            h264_v4l2m2m: "the Raspberry Pi's video chip",
            h264_vaapi: "VA-API",
            h264_nvenc: "NVENC",
            h264_qsv: "Quick Sync",
            libx264: "the processor (libx264)",
        }[e] ?? e
    )
}
