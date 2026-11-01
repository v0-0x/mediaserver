import { useQueryClient } from "@tanstack/react-query"
import { Lock } from "lucide-react"
import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useState } from "react"
import { Route, Routes, useLocation, useNavigate, useNavigationType } from "react-router-dom"
import { Toaster } from "sonner"
import { NowPlaying, ScanIndicator } from "./components/NowPlaying"
import { PlayerOverlay } from "./components/player/Player"
import { TvMenu } from "./components/TvMenu"
import { QuickSearch } from "./components/QuickSearch"
import { HolidayBackdrop, HolidayGarland } from "./components/HolidayDecor"
import { HolidayEffects } from "./components/HolidayEffects"
import { useHolidayNotice } from "./components/HolidayBadge"
import { WhatsNew } from "./components/WhatsNew"
import { KumoBoxLogo, Sidebar } from "./components/Sidebar"
import { TmdbNotice } from "./components/TmdbNotice"
import { PhoneNav } from "./components/PhoneNav"
import { Button, Input, Spinner, TooltipProvider } from "./components/ui"
import { api, onPasswordRequired } from "./lib/api"
import { connectEvents, setNavigate } from "./lib/events"
import { useCheckOnOpen, useStatus } from "./lib/queries"
import { useMediaQuery } from "./lib/hooks"
import { miniRequest, takeHandedPlay } from "./lib/pip"
import { passwordStore, playerStore, useStore } from "./lib/store"
import { applyTheme, holidayStore, resolveTheme, themePreviewStore, useClassic } from "./lib/themes"
import { keepFocus, openPage } from "./lib/pages"
import { initTV, tv } from "./lib/tv"
import { cn } from "./lib/utils"
import HomePage from "./pages/Home"

// Narrower than this: a phone's layout.
const PHONE = "(max-width: 639px)"

const EntryPage = lazy(() => import("./pages/Entry"))
const DiscoverPage = lazy(() => import("./pages/Discover"))
const SearchPage = lazy(() => import("./pages/Search"))
const SchedulePage = lazy(() => import("./pages/Schedule"))
const ListsPage = lazy(() => import("./pages/Lists"))
const StatsPage = lazy(() => import("./pages/Stats"))
const TorrentsPage = lazy(() => import("./pages/Torrents"))
const DownloadsPage = lazy(() => import("./pages/Downloads"))
const AutoDownloaderPage = lazy(() => import("./pages/AutoDownloader"))
const LibraryPage = lazy(() => import("./pages/Library"))
const LocalLibraryPage = lazy(() => import("./pages/LocalLibrary"))
const ExtensionsPage = lazy(() => import("./pages/Extensions"))
const SettingsPage = lazy(() => import("./pages/Settings"))
const WebviewPage = lazy(() => import("./pages/Webview"))

export default function App() {
    const { data: status, isError, isFetching } = useStatus()
    const needsPassword = useStore(passwordStore)
    useEffect(() => {
        const off = onPasswordRequired(() => passwordStore.set(true))
        return () => {
            off()
        }
    }, [])
    if (needsPassword) return <PasswordGate />
    // Until the server says what it is (a moment): not the app's pages
    // asking it for things.
    if (!status && (!isError || isFetching)) return <div className="h-full bg-frame" />
    return <KumoBoxApp />
}

function KumoBoxApp() {
    const qc = useQueryClient()
    const navigate = useNavigate()
    const location = useLocation()
    const { data: status } = useStatus()
    // The desktop app's small picture-in-picture window (lib/pip.ts): only
    // the player.
    const mini = location.pathname === "/mini"

    // Not in the small picture-in-picture window: alerts and toasts belong
    // in KumoBox's window, not over the video.
    useEffect(() => (mini ? undefined : connectEvents(qc)), [qc, mini])
    // Back finds a page as it was left (scrolled, focused); a new one starts
    // at its top. Before the page is drawn: its scroll isn't the last one's.
    const navType = useNavigationType()
    useLayoutEffect(() => openPage(location.key, navType), [location.key, navType])
    useEffect(() => keepFocus(), [])
    useEffect(() => setNavigate(navigate), [navigate])
    // An episode handed back by the small picture-in-picture window (the
    // desktop app's): played here.
    useEffect(() => {
        if (!mini) takeHandedPlay()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])
    // The remote's Back (TV mode): the page before, then Home, then Home's
    // top (from further down it), then out.
    useEffect(
        () =>
            initTV(() => {
                const m = document.getElementById("main-scroll")
                if (((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0) navigate(-1)
                else if (window.location.pathname !== "/") navigate("/", { replace: true })
                else if (tv && m && m.scrollTop > 120) {
                    m.scrollTo({ top: 0, behavior: "smooth" })
                    m.querySelector<HTMLElement>("[data-tv-autofocus] [tabindex], [data-tv-autofocus] a, [data-tv-autofocus] button")?.focus({ preventScroll: true })
                } else return false
                return true
            }),
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [],
    )
    // Apply UI preferences. Settings previews the colors and holiday theme
    // before they're saved; when that preview ends (Discard, leaving the
    // page) the saved ones come back. The date is looked at again each hour:
    // a holiday theme comes on (and goes) while KumoBox runs.
    const ui = status?.settings.ui
    const preview = useStore(themePreviewStore)
    const [hour, setHour] = useState(() => Math.floor(Date.now() / 3_600_000))
    useEffect(() => {
        const t = setInterval(() => setHour(Math.floor(Date.now() / 3_600_000)), 60_000)
        return () => clearInterval(t)
    }, [])
    // eslint-disable-next-line react-hooks/exhaustive-deps
    const look = useMemo(() => resolveTheme(preview ?? ui), [preview, ui, hour])
    useEffect(() => {
        if (!ui) return
        // Remembered for the next start: the saved colors, not ones tried.
        applyTheme(look, !preview)
        holidayStore.set(look.holiday)
        document.documentElement.classList.toggle("reduce-motion", !!ui.reducedMotion)
    }, [ui, look])
    // A holiday theme that came on by itself: said once.
    // From the saved settings: not a holiday being tried in Settings.
    const saved = useMemo(() => resolveTheme(ui), [ui, hour])
    useHolidayNotice(ui && !mini ? saved.holiday : null, ui?.seasonal ?? "auto")
    // Updates are looked for each time KumoBox opens (on this computer only).
    useCheckOnOpen(!!status && status.client !== "lan" && !mini)
    const playing = useStore(playerStore, p => !!p)
    // Before 2.0's look: its plain panel, no holiday decorations.
    const classic = useClassic()
    // A phone (a narrow window, not a TV): the pages take the whole width,
    // with tabs along the bottom instead of the sidebar. What floats at the
    // bottom (the now playing bar) stays above them.
    const phone = useMediaQuery(PHONE) && !tv
    useEffect(() => {
        document.documentElement.style.setProperty("--phone-bar", phone ? "calc(3.75rem + env(safe-area-inset-bottom))" : "0px")
    }, [phone])

    // Tell plugins where the user is (ctx.screen.onNavigate).
    useEffect(() => {
        const sp = Object.fromEntries(new URLSearchParams(location.search))
        api.post("/api/plugins/navigate", { pathname: location.pathname, searchParams: sp }).catch(() => {})
    }, [location.pathname, location.search])

    if (mini) return <MiniPlayer />

    return (
        <TooltipProvider>
            <div className={cn("app-frame flex h-full bg-frame", phone && "flex-col")}>
                {!phone && <Sidebar />}
                {/* The pages: a panel inset in the window, next to the sidebar
                    (on a phone, the whole width). Its background is behind
                    them (with a holiday's art). */}
                <div className={cn("relative min-h-0 min-w-0 flex-1", !phone && "py-2 pr-2")}>
                    {!classic && <HolidayBackdrop holiday={ui ? look.holiday : null} phone={phone} />}
                    <main
                        id="main-scroll"
                        className={cn(
                            "relative h-full overflow-x-hidden overflow-y-auto",
                            phone ? classic && "bg-bg" : cn("border border-line", classic ? "rounded-xl bg-bg" : "rounded-[14px] shadow-[0_12px_36px_-20px_rgb(0_0_0/0.75)]"),
                        )}
                    >
                        <TmdbNotice />
                        <Suspense
                            fallback={
                                <div className="grid h-full place-items-center">
                                    <Spinner className="size-7" />
                                </div>
                            }
                        >
                            <Routes>
                                <Route path="/" element={<HomePage />} />
                                <Route path="/entry" element={<EntryPage />} />
                                <Route path="/discover" element={<DiscoverPage />} />
                                <Route path="/search" element={<SearchPage />} />
                                <Route path="/schedule" element={<SchedulePage />} />
                                <Route path="/lists" element={<ListsPage />} />
                                <Route path="/stats" element={<StatsPage />} />
                                <Route path="/torrent-list" element={<TorrentsPage />} />
                                <Route path="/downloads" element={<DownloadsPage />} />
                                <Route path="/auto-downloader" element={<AutoDownloaderPage />} />
                                <Route path="/library" element={<LibraryPage />} />
                                <Route path="/local" element={<LocalLibraryPage />} />
                                <Route path="/extensions" element={<ExtensionsPage />} />
                                <Route path="/settings" element={<SettingsPage />} />
                                <Route path="/webview" element={<WebviewPage />} />
                                <Route path="*" element={<HomePage />} />
                            </Routes>
                        </Suspense>
                    </main>
                    {!classic && <HolidayGarland holiday={ui ? look.holiday : null} animate={look.effects && !playing && !ui?.reducedMotion} />}
                </div>
                {phone && <PhoneNav />}
            </div>
            <NowPlaying />
            <ScanIndicator />
            <QuickSearch />
            <PlayerOverlay />
            <WhatsNew />
            <HolidayEffects effect={ui && look.effects ? look.holiday!.effect : null} reduceMotion={!!ui?.reducedMotion} />
            {tv && <TvMenu />}
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

// The /mini page: the episode in its address, in the small window.
function MiniPlayer() {
    const [ok] = useState(() => {
        const r = miniRequest()
        playerStore.set(r)
        return !!r
    })
    useEffect(() => {
        document.title = "KumoBox"
    }, [])
    return (
        <TooltipProvider>
            {ok ? <PlayerOverlay mini /> : <p className="grid h-full place-items-center bg-black p-4 text-center text-sm text-muted">Nothing to play here.</p>}
            <Toaster
                theme="dark"
                position="top-center"
                visibleToasts={1}
                toastOptions={{ style: { background: "var(--surface-2)", border: "1px solid var(--line-strong)", borderRadius: 10, fontSize: 12, padding: "8px 12px" } }}
            />
        </TooltipProvider>
    )
}

function PasswordGate() {
    const [pw, setPw] = useState("")
    const [busy, setBusy] = useState(false)
    const [err, setErr] = useState("")
    const qc = useQueryClient()
    const submit = async () => {
        setBusy(true)
        setErr("")
        try {
            await api.post("/api/auth/server-login", { password: pw })
            passwordStore.set(false)
            qc.invalidateQueries()
        } catch (e: any) {
            setErr(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <div className="grid h-full place-items-center bg-bg p-6">
            <div className="card w-full max-w-sm p-8 text-center rise-in">
                <KumoBoxLogo className="mx-auto size-11" />
                <h1 className="mt-4 text-xl font-semibold">KumoBox</h1>
                <p className="mt-1 text-sm text-muted">This server is password protected.</p>
                <div className="mt-6 flex flex-col gap-3">
                    <Input type="password" autoFocus icon={<Lock className="size-4" />} placeholder="Password" value={pw} onChange={e => setPw(e.target.value)} onKeyDown={e => e.key === "Enter" && submit()} />
                    {err && <p className="text-sm text-rose-300">{err}</p>}
                    <Button variant="primary" loading={busy} onClick={submit}>
                        Unlock
                    </Button>
                </div>
            </div>
        </div>
    )
}
