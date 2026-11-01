import { useQueryClient } from "@tanstack/react-query"
import { Clapperboard, Gauge, KeyRound, Library, LogOut, ScrollText, Settings as SettingsIcon, Sparkles, Users, Wifi } from "lucide-react"
import { lazy, Suspense, useEffect, useMemo, useState } from "react"
import { Toaster } from "sonner"
import { AnikoLogo } from "@/components/Sidebar"
import { Badge, Button, Input, Spinner, Tabs, TooltipProvider } from "@/components/ui"
import { api } from "@/lib/api"
import { applyTheme, resolveTheme } from "@/lib/themes"
import type { Status } from "@/lib/types"
import { span, useHubOverview } from "./parts"
import Overview from "./Overview"

// mediaserver's dashboard, its anime side's (Aniko's server, in place of the
// app): what it streams to whom, its devices and AniList logins, its
// library, and the computer it runs on (a Raspberry Pi). Its owner's: signed
// in with its password, set the first time with its setup code. The movies
// side has its own pages (KumoBox's), on its own port, behind the same
// sign-in: the switch at the top goes there.

const Devices = lazy(() => import("./Devices"))
const LibraryPage = lazy(() => import("./Library"))
const Accounts = lazy(() => import("./Accounts"))
const AuditLogPage = lazy(() => import("./AuditLog"))
const HubSettings = lazy(() => import("./HubSettings"))

export type HubTab = "overview" | "devices" | "library" | "accounts" | "activity" | "settings"
const TABS: HubTab[] = ["overview", "devices", "library", "accounts", "activity", "settings"]

// The tab in the address (#devices), so a reload stays on it.
function tabFromHash(): HubTab {
    const t = window.location.hash.slice(1) as HubTab
    return TABS.includes(t) ? t : "overview"
}

export default function HubApp({ status }: { status: Status }) {
    const ui = status.settings?.ui
    // The hub's own colors (its Settings), not a device's.
    useEffect(() => {
        if (ui) applyTheme(resolveTheme(ui))
    }, [ui])
    useEffect(() => {
        document.title = "mediaserver"
    }, [])
    const setup = !!status.hub?.setup && status.client === "lan"
    return (
        <TooltipProvider>
            {setup ? <Setup /> : <Dashboard status={status} />}
            <Toaster
                theme="dark"
                position="top-right"
                richColors
                closeButton
                toastOptions={{ style: { background: "var(--surface-2)", border: "1px solid var(--line-strong)", borderRadius: 10 } }}
            />
        </TooltipProvider>
    )
}

function Dashboard({ status }: { status: Status }) {
    const [tab, setTab] = useState<HubTab>(tabFromHash)
    useEffect(() => {
        const on = () => setTab(tabFromHash())
        window.addEventListener("hashchange", on)
        return () => window.removeEventListener("hashchange", on)
    }, [])
    const go = (t: HubTab) => {
        setTab(t)
        history.replaceState(null, "", t === "overview" ? window.location.pathname : `#${t}`)
        document.getElementById("hub-scroll")?.scrollTo({ top: 0 })
    }
    const { data: o, error } = useHubOverview()
    const waiting = o?.devices.filter(d => d.online && !d.allowed).length ?? 0
    const sys = o?.system
    // Signed out from the hub (its password changed elsewhere): the
    // overview's 401 brings back the sign-in (lib/api).
    const qc = useQueryClient()
    const signOut = async () => {
        await api.post("/api/hub/signout").catch(() => {})
        qc.clear()
        window.location.reload()
    }
    const machine = useMemo(() => {
        if (!sys) return ""
        return [sys.model || sys.hostname, sys.arch, sys.cores ? `${sys.cores} cores` : "", sys.uptime ? `on for ${span(sys.uptime)}` : ""].filter(Boolean).join(" · ")
    }, [sys])
    return (
        <div className="flex h-full flex-col bg-frame sm:p-2">
            <div id="hub-scroll" className="relative min-h-0 flex-1 overflow-y-auto border-line bg-bg sm:rounded-[14px] sm:border sm:shadow-[0_12px_36px_-20px_rgb(0_0_0/0.75)]">
                <header className="sticky top-0 z-20 border-b border-line bg-bg/95 backdrop-blur">
                    <div className="mx-auto flex max-w-6xl items-center gap-3 px-4 pt-4 sm:px-6">
                        <AnikoLogo className="size-9 shrink-0" />
                        <div className="min-w-0 flex-1">
                            <h1 className="flex items-center gap-2 text-[17px] leading-tight font-semibold">
                                <span className="truncate">{o?.name || status.hostname || "mediaserver"}</span>
                                <Badge tone="brand" className="shrink-0">
                                    mediaserver
                                </Badge>
                            </h1>
                            <p className="truncate text-[12.5px] text-muted">{machine || `mediaserver ${o?.server ?? ""}`}</p>
                        </div>
                        <SideSwitch moviesPort={o?.moviesPort} />
                        {status.client === "owner" && (
                            <Button size="sm" variant="ghost" icon={<LogOut className="size-4" />} onClick={signOut}>
                                <span className="max-sm:hidden">Sign out</span>
                            </Button>
                        )}
                    </div>
                    <div className="mx-auto max-w-6xl px-4 sm:px-6">
                        <Tabs
                            variant="underline"
                            className="mt-3 border-b-0"
                            value={tab}
                            onChange={go}
                            tabs={[
                                { value: "overview", label: "Overview", icon: <Gauge /> },
                                { value: "devices", label: "Devices", icon: <Wifi />, count: waiting ? `${waiting} new` : o ? o.devices.filter(d => d.allowed).length : undefined },
                                { value: "library", label: "Library", icon: <Library /> },
                                { value: "accounts", label: "AniList", icon: <Users />, count: o?.accounts.length },
                                { value: "activity", label: "Audit log", icon: <ScrollText /> },
                                { value: "settings", label: "Settings", icon: <SettingsIcon /> },
                            ]}
                        />
                    </div>
                </header>
                <main className="mx-auto max-w-6xl px-4 py-6 sm:px-6">
                    {!o ? (
                        error ? (
                            <p className="py-20 text-center text-sm text-rose-300">{(error as Error).message}</p>
                        ) : (
                            <div className="grid place-items-center py-24">
                                <Spinner className="size-7" />
                            </div>
                        )
                    ) : (
                        <Suspense
                            fallback={
                                <div className="grid place-items-center py-24">
                                    <Spinner className="size-7" />
                                </div>
                            }
                        >
                            <div key={tab} className="fade-in">
                                {tab === "overview" && <Overview o={o} go={go} />}
                                {tab === "devices" && <Devices o={o} />}
                                {tab === "library" && <LibraryPage o={o} status={status} />}
                                {tab === "accounts" && <Accounts o={o} />}
                                {tab === "activity" && <AuditLogPage o={o} />}
                                {tab === "settings" && <HubSettings o={o} status={status} />}
                            </div>
                        </Suspense>
                    )}
                </main>
            </div>
        </div>
    )
}

// The first time: the setup code (in mediaserver's log, and from
// mediaserver --setup-code) sets the dashboard's password.
function Setup() {
    const qc = useQueryClient()
    const [code, setCode] = useState("")
    const [pw, setPw] = useState("")
    const [again, setAgain] = useState("")
    const [busy, setBusy] = useState(false)
    const [err, setErr] = useState("")
    const short = pw.length > 0 && pw.length < 6
    const differ = again.length > 0 && again !== pw
    const ready = code.replace(/\D/g, "").length === 8 && pw.length >= 6 && again === pw
    const submit = async () => {
        if (!ready) return
        setBusy(true)
        setErr("")
        try {
            await api.post("/api/hub/setup", { code, password: pw })
            await qc.invalidateQueries()
        } catch (e) {
            setErr((e as Error).message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <div className="grid h-full place-items-center overflow-y-auto bg-bg p-6">
            <form
                className="card w-full max-w-md p-8 rise-in"
                onSubmit={e => {
                    e.preventDefault()
                    submit()
                }}
            >
                <AnikoLogo className="size-11" />
                <h1 className="mt-4 text-xl font-semibold">Set up your mediaserver</h1>
                <p className="mt-1.5 text-sm leading-relaxed text-muted">
                    Choose the password for this dashboard. To prove the server is yours, type its setup code: the install printed it, and on the server
                    <code className="mx-1 rounded bg-white/[0.06] px-1.5 py-0.5 text-[12.5px] text-fg">mediaserver --setup-code</code>
                    shows it again.
                </p>
                <div className="mt-6 flex flex-col gap-4">
                    <Field id="setup-code" label="Setup code">
                        <Input id="setup-code" autoFocus inputMode="numeric" autoComplete="one-time-code" placeholder="1234-5678" value={code} onChange={e => setCode(e.target.value)} className="tracking-widest tabular-nums" />
                    </Field>
                    <Field id="setup-password" label="Password" hint={<span className={short ? "text-amber-300" : undefined}>At least 6 characters.</span>}>
                        <Input id="setup-password" aria-describedby="setup-password-hint" type="password" autoComplete="new-password" icon={<KeyRound className="size-4" />} value={pw} onChange={e => setPw(e.target.value)} />
                    </Field>
                    <Field id="setup-again" label="The password again" hint={differ ? <span className="text-amber-300">The two aren't the same.</span> : undefined}>
                        <Input id="setup-again" aria-describedby={differ ? "setup-again-hint" : undefined} type="password" autoComplete="new-password" icon={<KeyRound className="size-4" />} value={again} onChange={e => setAgain(e.target.value)} />
                    </Field>
                    {err && (
                        <p role="alert" className="text-sm text-rose-300">
                            {err}
                        </p>
                    )}
                    <Button type="submit" variant="primary" loading={busy} disabled={!ready}>
                        Set the password
                    </Button>
                </div>
            </form>
        </div>
    )
}

// Which side of mediaserver: this one (anime, for the Aniko apps) or the
// movies side (KumoBox's pages, for the KumoBox apps), on its own port of
// the same computer: the sign-in carries over.
function SideSwitch({ moviesPort }: { moviesPort?: number }) {
    const item = "focus-ring flex h-8 items-center gap-1.5 rounded-md px-2.5 text-[13px] font-medium [&>svg]:size-4"
    return (
        <nav aria-label="mediaserver's sides" className="flex shrink-0 items-center gap-0.5 rounded-lg bg-white/[0.04] p-0.5">
            <span aria-current="page" className={`${item} bg-white/[0.08] text-fg`}>
                <Sparkles />
                <span className="max-sm:hidden">Anime</span>
            </span>
            {moviesPort ? (
                <a href={`${window.location.protocol}//${window.location.hostname}:${moviesPort}/`} className={`${item} text-muted transition-colors hover:text-fg`}>
                    <Clapperboard />
                    <span className="max-sm:hidden">Movies &amp; TV</span>
                </a>
            ) : null}
        </nav>
    )
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: React.ReactNode; children: React.ReactNode }) {
    return (
        <div className="flex flex-col gap-1.5">
            <label htmlFor={id} className="text-sm font-medium">
                {label}
            </label>
            {children}
            {hint && (
                <p id={`${id}-hint`} className="text-xs text-subtle">
                    {hint}
                </p>
            )}
        </div>
    )
}
