import { useQueryClient } from "@tanstack/react-query"
import { Clapperboard, Gauge, KeyRound, Library, LogOut, Music2, ScrollText, Settings as SettingsIcon, Sparkles, Tv, Users, Wifi } from "lucide-react"
import { lazy, Suspense, useEffect, useMemo, useState } from "react"
import { Toaster } from "sonner"
import { AnikoLogo } from "@/components/Sidebar"
import { Badge, Button, Input, Spinner, Tabs, TooltipProvider } from "@/components/ui"
import { api } from "@/lib/api"
import { applyTheme, resolveTheme } from "@/lib/themes"
import { cn } from "@/lib/utils"
import type { Status } from "@/lib/types"
import { useMSharing } from "./media/data"
import type { MediaTab } from "./media/MediaPages"
import { useUDevices } from "./music/data"
import type { MusicTab } from "./music/MusicPages"
import { span, useHubOverview } from "./parts"
import Overview from "./Overview"

// mediaserver's dashboard, one for all of it, its owner's: signed in with
// its password, set the first time with its setup code. The slider at the
// top picks what it's about: Anime (the anime side, Aniko's server: what it
// streams to whom, its devices and AniList logins, its library, the
// computer it runs on), Movies or TV (the movies side, Moviko's server,
// asked through this one: media/), Music (the music side, Utako's server,
// asked through this one too: music/).

const Devices = lazy(() => import("./Devices"))
const LibraryPage = lazy(() => import("./Library"))
const Accounts = lazy(() => import("./Accounts"))
const AuditLogPage = lazy(() => import("./AuditLog"))
const HubSettings = lazy(() => import("./HubSettings"))
const MediaOverview = lazy(() => import("./media/MediaPages").then(m => ({ default: m.MediaOverview })))
const MediaDevices = lazy(() => import("./media/MediaPages").then(m => ({ default: m.MediaDevices })))
const MediaLibrary = lazy(() => import("./media/MediaPages").then(m => ({ default: m.MediaLibrary })))
const MediaSettings = lazy(() => import("./media/MediaPages").then(m => ({ default: m.MediaSettings })))
const MusicOverview = lazy(() => import("./music/MusicPages").then(m => ({ default: m.MusicOverview })))
const MusicDevices = lazy(() => import("./music/MusicPages").then(m => ({ default: m.MusicDevices })))
const MusicLibrary = lazy(() => import("./music/MusicPages").then(m => ({ default: m.MusicLibrary })))

export type HubTab = "overview" | "devices" | "library" | "accounts" | "activity" | "settings"
const TABS: HubTab[] = ["overview", "devices", "library", "accounts", "activity", "settings"]
const MEDIA_TABS: MediaTab[] = ["overview", "devices", "library", "settings"]
const MUSIC_TABS: MusicTab[] = ["overview", "devices", "library"]

// What the dashboard is about: the anime side, the movies side's movies or
// shows, or the music side.
export type Side = "anime" | "movies" | "tv" | "music"
type Route = { side: Side; tab: string }

// The side and tab in the address (#movies/library; the anime side's tabs
// alone, #devices, as before), so a reload stays on them.
function routeFromHash(): Route {
    const [a, b] = window.location.hash.slice(1).split("/")
    if (a === "movies" || a === "tv") return { side: a, tab: MEDIA_TABS.includes(b as MediaTab) ? b : "overview" }
    if (a === "music") return { side: a, tab: MUSIC_TABS.includes(b as MusicTab) ? b : "overview" }
    if (a === "anime") return { side: "anime", tab: TABS.includes(b as HubTab) ? b : "overview" }
    return { side: "anime", tab: TABS.includes(a as HubTab) ? a : "overview" }
}

function hashOf(r: Route) {
    if (r.side === "anime") return r.tab === "overview" ? "" : `#${r.tab}`
    return `#${r.side}${r.tab === "overview" ? "" : `/${r.tab}`}`
}

export default function HubApp({ status }: { status: Status }) {
    const ui = status.settings?.ui
    // The server's own colors (its Settings), not a device's.
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
    const [route, setRoute] = useState<Route>(routeFromHash)
    useEffect(() => {
        const on = () => setRoute(routeFromHash())
        window.addEventListener("hashchange", on)
        return () => window.removeEventListener("hashchange", on)
    }, [])
    const show = (r: Route) => {
        setRoute(r)
        history.replaceState(null, "", hashOf(r) || window.location.pathname)
        document.getElementById("hub-scroll")?.scrollTo({ top: 0 })
    }
    const { side, tab } = route
    const go = (t: HubTab) => show({ side: "anime", tab: t })
    const goMedia = (t: MediaTab) => show({ side, tab: t })
    const goMusic = (t: MusicTab) => show({ side: "music", tab: t })
    // Another side: the same tab when it has one.
    const pickSide = (next: Side) => {
        if (next === side) return
        const tabs: string[] = next === "anime" ? TABS : next === "music" ? MUSIC_TABS : MEDIA_TABS
        show({ side: next, tab: tabs.includes(tab) ? tab : "overview" })
    }
    const { data: o, error } = useHubOverview()
    const { data: sharing } = useMSharing()
    const waiting = o?.devices.filter(d => d.online && !d.allowed).length ?? 0
    const mPeers = sharing?.peers ?? []
    const mWaiting = mPeers.filter(p => p.online && !p.allowed && !p.declined).length
    const { data: uDevices } = useUDevices()
    const uWaiting = uDevices?.filter(d => d.online && !d.allowed).length ?? 0
    const sys = o?.system
    // Signed out from the server (its password changed elsewhere): the
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
                        {side === "anime" ? (
                            <AnikoLogo className="size-9 shrink-0" />
                        ) : (
                            <span className="grid size-9 shrink-0 place-items-center rounded-[10px] bg-white/[0.06] text-fg [&>svg]:size-5">{side === "tv" ? <Tv /> : side === "music" ? <Music2 /> : <Clapperboard />}</span>
                        )}
                        <div className="min-w-0 flex-1">
                            <h1 className="flex items-center gap-2 text-[17px] leading-tight font-semibold">
                                <span className="truncate">{o?.name || status.hostname || "mediaserver"}</span>
                                <Badge tone="brand" className="shrink-0">
                                    mediaserver
                                </Badge>
                            </h1>
                            <p className="truncate text-[12.5px] text-muted">{machine || `mediaserver ${o?.server ?? ""}`}</p>
                        </div>
                        <SideSlider side={side} onPick={pickSide} />
                        {status.client === "owner" && (
                            <Button size="sm" variant="ghost" icon={<LogOut className="size-4" />} onClick={signOut}>
                                <span className="max-sm:hidden">Sign out</span>
                            </Button>
                        )}
                    </div>
                    <div className="mx-auto max-w-6xl px-4 sm:px-6">
                        {side === "anime" ? (
                            <Tabs
                                variant="underline"
                                className="mt-3 border-b-0"
                                value={tab as HubTab}
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
                        ) : side === "music" ? (
                            <Tabs
                                variant="underline"
                                className="mt-3 border-b-0"
                                value={tab as MusicTab}
                                onChange={goMusic}
                                tabs={[
                                    { value: "overview", label: "Overview", icon: <Gauge /> },
                                    { value: "devices", label: "Devices", icon: <Wifi />, count: uWaiting ? `${uWaiting} new` : uDevices ? uDevices.filter(d => d.allowed).length : undefined },
                                    { value: "library", label: "Library", icon: <Library /> },
                                ]}
                            />
                        ) : (
                            <Tabs
                                variant="underline"
                                className="mt-3 border-b-0"
                                value={tab as MediaTab}
                                onChange={goMedia}
                                tabs={[
                                    { value: "overview", label: "Overview", icon: <Gauge /> },
                                    { value: "devices", label: "Devices", icon: <Wifi />, count: mWaiting ? `${mWaiting} new` : sharing ? mPeers.filter(p => p.allowed).length : undefined },
                                    { value: "library", label: "Library", icon: <Library /> },
                                    { value: "settings", label: "Settings", icon: <SettingsIcon /> },
                                ]}
                            />
                        )}
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
                            <div key={`${side}/${tab}`} className="fade-in">
                                {side === "anime" ? (
                                    <>
                                        {tab === "overview" && <Overview o={o} go={go} />}
                                        {tab === "devices" && <Devices o={o} />}
                                        {tab === "library" && <LibraryPage o={o} status={status} />}
                                        {tab === "accounts" && <Accounts o={o} />}
                                        {tab === "activity" && <AuditLogPage o={o} />}
                                        {tab === "settings" && <HubSettings o={o} status={status} />}
                                    </>
                                ) : side === "music" ? (
                                    <>
                                        {tab === "overview" && <MusicOverview go={goMusic} />}
                                        {tab === "devices" && <MusicDevices />}
                                        {tab === "library" && <MusicLibrary />}
                                    </>
                                ) : (
                                    <>
                                        {tab === "overview" && <MediaOverview kind={side} o={o} go={goMedia} />}
                                        {tab === "devices" && <MediaDevices />}
                                        {tab === "library" && <MediaLibrary kind={side} moviesPort={o.moviesPort} />}
                                        {tab === "settings" && <MediaSettings moviesPort={o.moviesPort} />}
                                    </>
                                )}
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

// What the dashboard is about, a slider: Anime (the anime side, for the
// Aniko apps), Movies or TV (the movies side, for the Moviko apps), Music
// (the music side, for the Utako apps).
const SIDES: { value: Side; label: string; icon: React.ReactNode }[] = [
    { value: "anime", label: "Anime", icon: <Sparkles /> },
    { value: "movies", label: "Movies", icon: <Clapperboard /> },
    { value: "tv", label: "TV", icon: <Tv /> },
    { value: "music", label: "Music", icon: <Music2 /> },
]

function SideSlider({ side, onPick }: { side: Side; onPick: (s: Side) => void }) {
    const at = SIDES.findIndex(s => s.value === side)
    return (
        <div role="tablist" aria-label="What the dashboard shows" className="relative grid shrink-0 grid-cols-4 rounded-[10px] bg-white/[0.05] p-0.5 ring-1 ring-white/[0.06]">
            {/* The thumb, sliding under the side shown. */}
            <span
                aria-hidden
                className="absolute inset-y-0.5 left-0.5 rounded-lg bg-white/[0.1] shadow-[0_1px_0_rgb(255_255_255/0.06)_inset] transition-transform duration-200 ease-out"
                style={{ width: `calc((100% - 4px) / ${SIDES.length})`, transform: `translateX(${at * 100}%)` }}
            />
            {SIDES.map(s => (
                <button
                    key={s.value}
                    role="tab"
                    aria-selected={s.value === side}
                    onClick={() => onPick(s.value)}
                    className={cn(
                        "focus-ring relative z-10 flex h-8 items-center justify-center gap-1.5 rounded-lg px-3 text-[13px] font-medium transition-colors [&>svg]:size-4 sm:min-w-[92px]",
                        s.value === side ? "text-fg" : "text-muted hover:text-fg",
                    )}
                >
                    {s.icon}
                    <span className="max-sm:hidden">{s.label}</span>
                </button>
            ))}
        </div>
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
