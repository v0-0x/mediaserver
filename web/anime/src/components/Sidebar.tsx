import {
    ArrowLeft,
    Bell,
    BellOff,
    BookOpen,
    CalendarDays,
    ChartColumn,
    Compass,
    Download,
    ExternalLink,
    FolderSearch,
    HardDrive,
    Home,
    ListChecks,
    LogIn,
    LogOut,
    Magnet,
    Puzzle,
    Rss,
    Search,
    Settings as SettingsIcon,
    UserRound,
} from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { NavLink, useLocation, useNavigate } from "react-router-dom"
import { api, desktop } from "@/lib/api"
import { useMediaQuery } from "@/lib/hooks"
import { useCollection, useDownloads, useStatus } from "@/lib/queries"
import { notificationsMutedStore, torrentCountStore, useStore } from "@/lib/store"
import { holidayStore, useClassic } from "@/lib/themes"
import { tv } from "@/lib/tv"
import { cn, img } from "@/lib/utils"
import { ThemesMenu } from "./ThemesMenu"
import { HolidayOnLogo } from "./HolidayBadge"
import { LoginDialog } from "./LoginDialog"
import { PluginTrays } from "./plugins/PluginTrays"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "./ui"
import { useQueryClient } from "@tanstack/react-query"
import { toast as sonner } from "sonner"
import { toast } from "@/lib/toast"

// The logo: an A and a spark (光, the "ko" of Aniko: light) on a dark tile,
// the same as the app icon (packaging/icons/aniko.svg) and the favicon
// (public/aniko.svg).
export function AnikoLogo({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 64 64" className={className} aria-hidden>
            <path
                d="M59.86 59.86C59.27 60.45 58.63 60.95 57.87 61.4C57.12 61.85 56.31 62.23 55.33 62.56C54.36 62.88 53.35 63.15 52.01 63.36C50.66 63.58 50.57 63.74 47.24 63.84C43.9 63.95 37.08 64 32 64C26.92 64 20.1 63.95 16.76 63.84C13.43 63.74 13.34 63.58 11.99 63.36C10.65 63.15 9.64 62.88 8.67 62.56C7.69 62.23 6.88 61.85 6.13 61.4C5.37 60.95 4.73 60.45 4.14 59.86C3.55 59.27 3.05 58.63 2.6 57.87C2.15 57.12 1.77 56.31 1.44 55.33C1.12 54.36 0.85 53.35 0.64 52.01C0.42 50.66 0.26 50.57 0.16 47.24C0.05 43.9 0 37.08 0 32C0 26.92 0.05 20.1 0.16 16.76C0.26 13.43 0.42 13.34 0.64 11.99C0.85 10.65 1.12 9.64 1.44 8.67C1.77 7.69 2.15 6.88 2.6 6.13C3.05 5.37 3.55 4.73 4.14 4.14C4.73 3.55 5.37 3.05 6.13 2.6C6.88 2.15 7.69 1.77 8.67 1.44C9.64 1.12 10.65 0.85 11.99 0.64C13.34 0.42 13.43 0.26 16.76 0.16C20.1 0.05 26.92 0 32 0C37.08 0 43.9 0.05 47.24 0.16C50.57 0.26 50.66 0.42 52.01 0.64C53.35 0.85 54.36 1.12 55.33 1.44C56.31 1.77 57.12 2.15 57.87 2.6C58.63 3.05 59.27 3.55 59.86 4.14C60.45 4.73 60.95 5.37 61.4 6.13C61.85 6.88 62.23 7.69 62.56 8.67C62.88 9.64 63.15 10.65 63.36 11.99C63.58 13.34 63.74 13.43 63.84 16.76C63.95 20.1 64 26.92 64 32C64 37.08 63.95 43.9 63.84 47.24C63.74 50.57 63.58 50.66 63.36 52.01C63.15 53.35 62.88 54.36 62.56 55.33C62.23 56.31 61.85 57.12 61.4 57.87C60.95 58.63 60.45 59.27 59.86 59.86Z"
                fill="#141416"
            />
            <path
                d="M42.74 43.66Q43.2 44.61 43.24 45.28Q43.28 45.95 42.84 46.47Q42.4 46.98 41.41 47.48Q39.88 48.21 39.04 47.92Q38.2 47.63 37.47 46.1L29.98 30.44L22.53 46.1Q21.77 47.59 20.94 47.88Q20.12 48.17 18.59 47.44Q17.6 46.98 17.16 46.47Q16.72 45.95 16.76 45.24Q16.8 44.54 17.3 43.58L27.34 22.68Q27.73 21.92 28.47 21.46Q29.22 21 30.02 21Q30.59 21 31.09 21.21Q31.59 21.42 31.99 21.8Q32.39 22.18 32.66 22.72ZM23.56 41.48 25.93 36.55H34.07L36.44 41.48Z"
                fill="#ececee"
            />
            <path d="M48 10Q49.05 15.95 55 17Q49.05 18.05 48 24Q46.95 18.05 41 17Q46.95 15.95 48 10Z" fill="#7c6cf2" />
        </svg>
    )
}

// "Aniko" in the logo's typeface (Fredoka); in the text color.
export function AnikoWordmark({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 125.6 38" className={className} aria-hidden>
            <path
                d="M33.26 31.42Q33.85 32.65 33.9 33.5Q33.95 34.36 33.39 35.02Q32.82 35.68 31.55 36.32Q29.6 37.24 28.52 36.88Q27.44 36.51 26.51 34.55L16.93 14.5L7.39 34.55Q6.41 36.46 5.36 36.83Q4.31 37.2 2.35 36.27Q1.08 35.68 0.52 35.02Q-0.04 34.36 0 33.45Q0.05 32.55 0.69 31.33L13.55 4.57Q14.04 3.59 15 3.01Q15.95 2.42 16.98 2.42Q17.71 2.42 18.35 2.69Q18.98 2.96 19.5 3.45Q20.01 3.93 20.35 4.62ZM8.71 28.64 11.74 22.33H22.16L25.19 28.64ZM40.45 36.56Q38.65 36.56 37.89 36.05Q37.13 35.53 36.96 34.68Q36.79 33.82 36.79 32.89V15.67Q36.79 14.74 36.98 13.94Q37.18 13.13 37.94 12.64Q38.69 12.15 40.5 12.15Q42.07 12.15 42.8 12.57Q43.54 12.98 43.76 13.57Q43.98 14.16 43.98 14.67Q43.98 15.18 44.07 15.38L43.78 16.31Q43.88 15.58 44.54 14.82Q45.2 14.06 46.2 13.45Q47.2 12.84 48.35 12.49Q49.5 12.15 50.63 12.15Q52.98 12.15 54.81 13.01Q56.65 13.86 57.92 15.48Q59.19 17.09 59.85 19.27Q60.51 21.45 60.51 24.04V32.94Q60.51 33.87 60.34 34.73Q60.17 35.58 59.41 36.07Q58.65 36.56 56.84 36.56Q54.98 36.56 54.22 36.05Q53.47 35.53 53.27 34.68Q53.07 33.82 53.07 32.89V24.04Q53.07 22.57 52.59 21.54Q52.1 20.52 51.12 19.95Q50.14 19.39 48.72 19.39Q47.3 19.39 46.28 19.98Q45.25 20.57 44.69 21.59Q44.12 22.62 44.12 24.04V32.94Q44.12 33.87 43.95 34.73Q43.78 35.58 43.02 36.07Q42.26 36.56 40.45 36.56ZM68.73 36.61Q66.92 36.61 66.16 36.1Q65.4 35.58 65.23 34.73Q65.06 33.87 65.06 32.94V15.87Q65.06 14.89 65.23 14.06Q65.4 13.23 66.18 12.71Q66.97 12.2 68.78 12.2Q70.54 12.2 71.29 12.74Q72.05 13.28 72.22 14.11Q72.4 14.94 72.4 15.97V32.99Q72.4 33.92 72.22 34.77Q72.05 35.63 71.29 36.12Q70.54 36.61 68.73 36.61ZM68.73 8.63Q66.87 8.63 66.09 8.09Q65.3 7.55 65.13 6.67Q64.96 5.79 64.96 4.82Q64.96 3.79 65.16 2.93Q65.35 2.08 66.13 1.56Q66.92 1.05 68.78 1.05Q70.59 1.05 71.37 1.59Q72.15 2.12 72.32 3.01Q72.49 3.89 72.49 4.86Q72.49 5.84 72.32 6.72Q72.15 7.6 71.37 8.12Q70.59 8.63 68.73 8.63ZM97.34 29.96Q98.86 31.28 98.95 32.48Q99.05 33.67 97.63 35.24Q96.56 36.36 95.75 36.71Q94.94 37.05 94.16 36.73Q93.38 36.41 92.35 35.48L84.62 28.73V32.94Q84.62 33.92 84.45 34.75Q84.28 35.58 83.52 36.1Q82.76 36.61 80.95 36.61Q79.19 36.61 78.41 36.1Q77.63 35.58 77.46 34.73Q77.29 33.87 77.29 32.89V4.67Q77.29 3.69 77.48 2.86Q77.68 2.03 78.46 1.51Q79.24 1 81 1Q82.76 1 83.52 1.51Q84.28 2.03 84.45 2.88Q84.62 3.74 84.62 4.72V19.49L90.1 14.21Q91.08 13.28 91.94 12.93Q92.79 12.59 93.62 12.89Q94.45 13.18 95.38 14.21Q96.95 15.92 96.9 16.97Q96.85 18.02 95.38 19.49L90.1 24.28ZM112.55 37Q110.25 37 107.98 36.1Q105.7 35.19 103.85 33.53Q101.99 31.86 100.91 29.54Q99.84 27.22 99.84 24.38Q99.84 21.74 100.81 19.46Q101.79 17.19 103.55 15.43Q105.31 13.67 107.59 12.67Q109.86 11.66 112.45 11.66Q115.98 11.66 118.84 13.35Q121.7 15.04 123.41 17.92Q125.12 20.81 125.12 24.33Q125.12 27.32 124.02 29.64Q122.92 31.96 121.09 33.63Q119.25 35.29 117.03 36.14Q114.8 37 112.55 37ZM112.45 29.61Q113.63 29.61 114.85 29.03Q116.07 28.44 116.91 27.27Q117.74 26.09 117.74 24.28Q117.74 22.72 117.05 21.54Q116.37 20.37 115.17 19.71Q113.97 19.05 112.41 19.05Q110.94 19.05 109.79 19.73Q108.64 20.42 107.93 21.62Q107.22 22.82 107.22 24.33Q107.22 26.09 108.03 27.29Q108.84 28.49 110.06 29.05Q111.28 29.61 112.45 29.61Z"
                fill="currentColor"
            />
        </svg>
    )
}

type Item = NavItem

export type NavItem = { to: string; label: string; icon: React.ReactNode; badge?: number; hint?: string; match?: (p: string) => boolean }

// The app's pages, in their groups, with the counts shown on them (active
// downloads, unmatched files): the sidebar's, and the phone's tab bar's.
export function useNavGroups(): { label: string; items: NavItem[] }[] {
    const { data: status } = useStatus()
    const { data: coll } = useCollection()
    const { data: downloads } = useDownloads()
    const torrentCount = useStore(torrentCountStore)
    const settings = status?.settings
    const activeDownloads = (downloads ?? []).filter(d => ["queued", "resolving", "downloading"].includes(d.status)).length

    return [
        {
            label: "Watch",
            items: [
                { to: "/", label: "Home", icon: <Home />, match: p => p === "/" },
                { to: "/local", label: "Local library", icon: <HardDrive /> },
                { to: "/schedule", label: "Schedule", icon: <CalendarDays /> },
                ...(settings?.manga.enabled !== false ? [{ to: "/manga", label: "Manga", icon: <BookOpen />, match: (p: string) => p.startsWith("/manga") }] : []),
                { to: "/lists", label: "My lists", icon: <ListChecks /> },
                { to: "/stats", label: "Stats", icon: <ChartColumn /> },
            ],
        },
        {
            label: "Explore",
            items: [
                { to: "/discover", label: "Discover", icon: <Compass /> },
                { to: "/search", label: "Search", icon: <Search />, hint: "Ctrl K" },
            ],
        },
        {
            label: "Downloads",
            items: [
                { to: "/torrent-list", label: "Torrents", icon: <Magnet />, badge: settings?.torrent.showActiveCount ? torrentCount : 0 },
                { to: "/downloads", label: "Downloads", icon: <Download />, badge: activeDownloads },
                { to: "/auto-downloader", label: "Auto downloader", icon: <Rss /> },
            ],
        },
        {
            label: "Library",
            items: [
                { to: "/library", label: "Library tools", icon: <FolderSearch />, badge: coll?.unmatchedCount ?? 0 },
                { to: "/extensions", label: "Extensions", icon: <Puzzle /> },
            ],
        },
    ]
}

// Back to the page before, when there's one in Aniko. Not on a TV: its
// remote has Back. In the desktop app, Alt+Left (⌘[ on a Mac) and a
// mouse's back button too, which a browser does by itself.
function BackButton({ wide }: { wide: boolean }) {
    const location = useLocation()
    const navigate = useNavigate()
    // React Router counts the pages it went through (history.state.idx).
    const canGoBack = ((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0
    useEffect(() => {
        if (!canGoBack || !desktop()) return
        const typing = (t: EventTarget | null) => t instanceof HTMLElement && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(t.tagName))
        const onKey = (e: KeyboardEvent) => {
            const mac = navigator.platform.startsWith("Mac")
            if (((!mac && e.altKey && e.key === "ArrowLeft") || (mac && e.metaKey && e.key === "[")) && !typing(e.target)) {
                e.preventDefault()
                navigate(-1)
            }
        }
        const onMouse = (e: MouseEvent) => {
            if (e.button === 3) {
                e.preventDefault()
                navigate(-1)
            }
        }
        window.addEventListener("keydown", onKey)
        window.addEventListener("mouseup", onMouse)
        return () => {
            window.removeEventListener("keydown", onKey)
            window.removeEventListener("mouseup", onMouse)
        }
    }, [canGoBack, navigate, location.key])
    if (!canGoBack) return null
    return (
        <Tooltip content="Back" side={wide ? "bottom" : "right"}>
            <button
                onClick={() => navigate(-1)}
                aria-label="Back"
                className="focus-ring grid size-8 shrink-0 place-items-center rounded-lg text-muted transition-colors hover:bg-white/[0.06] hover:text-fg"
            >
                <ArrowLeft className="size-[18px] stroke-[1.75]" />
            </button>
        </Tooltip>
    )
}

// Labels from 1024px wide; narrower, only the icons, named by tooltips.
const WIDE = "(min-width: 1024px)"
// A short window (a laptop's): the groups without their names, closer.
const SHORT = "(max-height: 880px)"
const FADE = "linear-gradient(to bottom, #000 calc(100% - 36px), transparent)"

export function Sidebar() {
    const { data: status } = useStatus()
    const [loginOpen, setLoginOpen] = useState(false)
    const location = useLocation()
    const navigate = useNavigate()
    const qc = useQueryClient()
    const wide = useMediaQuery(WIDE)
    const short = useMediaQuery(SHORT)
    // Before 2.0: no group names, the open page in plain white.
    const classic = useClassic()
    // Links that don't fit scroll: they fade out at the bottom while there
    // are more below, so they aren't taken for all of them.
    const navRef = useRef<HTMLElement>(null)
    const [more, setMore] = useState(false)
    useEffect(() => {
        const nav = navRef.current
        if (!nav) return
        const check = () => setMore(nav.scrollTop + nav.clientHeight < nav.scrollHeight - 2)
        const ro = new ResizeObserver(check)
        ro.observe(nav)
        for (const group of nav.children) ro.observe(group)
        nav.addEventListener("scroll", check, { passive: true })
        check()
        return () => {
            ro.disconnect()
            nav.removeEventListener("scroll", check)
        }
    }, [])
    const holiday = useStore(holidayStore)

    const groups = useNavGroups()

    const logout = async () => {
        await api.post("/api/auth/logout")
        qc.invalidateQueries()
        toast.success("Logged out of AniList")
    }

    const link = (it: Item) => {
        const active = it.match ? it.match(location.pathname) : location.pathname.startsWith(it.to)
        const badge = it.badge && it.badge > 0 ? (it.badge > 99 ? "99+" : String(it.badge)) : ""
        const el = (
            <NavLink
                key={it.to}
                to={it.to}
                // Icons only (narrow): named for screen readers too.
                aria-label={wide ? undefined : it.label}
                className={cn(
                    "focus-ring relative flex h-9 shrink-0 items-center text-[13.5px] font-medium transition-colors [&>svg]:size-[18px] [&>svg]:shrink-0 [&>svg]:stroke-[1.75] [&>svg]:transition-colors",
                    classic ? "rounded-md" : "rounded-lg",
                    wide ? "gap-3 px-2.5" : "w-10 justify-center",
                    // The page it's on: tinted with the accent.
                    classic
                        ? active
                            ? "bg-white/[0.08] text-fg"
                            : "text-muted hover:bg-white/[0.04] hover:text-fg"
                        : active
                          ? "bg-white/[0.07] text-fg [&>svg]:text-brand-strong"
                          : "text-muted hover:bg-white/[0.045] hover:text-fg",
                )}
            >
                {it.icon}
                {wide && <span className="truncate">{it.label}</span>}
                {wide && it.hint && !badge && !tv && <kbd className="ml-auto font-sans text-[11px] text-subtle">{it.hint}</kbd>}
                {badge &&
                    (wide ? (
                        <span className={cn("ml-auto bg-white/[0.08] px-1.5 text-[11px] leading-[18px] text-fg/80 tabular-nums", classic ? "rounded" : "rounded-md")}>{badge}</span>
                    ) : (
                        <span className="absolute top-0.5 right-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-brand px-1 text-[10px] font-semibold text-white tabular-nums">{badge}</span>
                    ))}
            </NavLink>
        )
        return wide ? (
            el
        ) : (
            <Tooltip key={it.to} content={it.label} side="right">
                {el}
            </Tooltip>
        )
    }

    return (
        <aside className={cn("relative z-40 flex h-full shrink-0 flex-col py-3", wide ? "w-56 px-3" : "w-16 items-center px-2")}>
            <div className={cn("mb-4 flex", wide ? "items-center gap-1" : "flex-col items-center gap-1")}>
                {/* Home is the first link below: the remote skips this one. */}
                <button
                    onClick={() => navigate("/")}
                    data-tv-skip-nav
                    className={cn("focus-ring flex h-10 items-center gap-2.5 rounded-md", wide ? "min-w-0 flex-1 px-2" : "justify-center")}
                    aria-label="Aniko home"
                >
                    <span className="relative shrink-0">
                        <AnikoLogo className="size-7" />
                        <HolidayOnLogo holiday={holiday} />
                    </span>
                    {wide && <AnikoWordmark className="h-[18px] w-auto text-fg" />}
                </button>
                {!tv && <BackButton wide={wide} />}
            </div>

            <nav
                ref={navRef}
                className={cn("no-scrollbar flex flex-1 flex-col overflow-y-auto", wide ? (classic || short ? "gap-4" : tv ? "gap-3" : "gap-5") : "items-center gap-3")}
                style={more ? { maskImage: FADE, WebkitMaskImage: FADE } : undefined}
            >
                {groups.map((g, i) => (
                    <div key={g.label} className={cn("flex flex-col gap-0.5", !wide && "items-center", (!wide || (tv && !classic)) && i > 0 && "border-t border-line pt-3")}>
                        {/* Their names; not on a TV, whose screen is short. */}
                        {wide && !tv && !classic && !short && <p className="px-2.5 pb-1 text-[11px] font-medium text-subtle">{g.label}</p>}
                        {g.items.map(link)}
                    </div>
                ))}
            </nav>

            <div className={cn("flex flex-col gap-0.5 pt-3", !classic && "border-t border-line", !wide && "items-center")}>
                <ThemesMenu wide={wide} />
                <PluginTrays />
                <div className={cn("flex gap-0.5", wide ? "items-center" : "flex-col-reverse items-center")}>
                    <div className={wide ? "min-w-0 flex-1" : undefined}>{link({ to: "/settings", label: "Settings", icon: <SettingsIcon /> })}</div>
                    <MuteButton side={wide ? "top" : "right"} />
                </div>
                <Dropdown>
                    <DropdownTrigger asChild>
                        <button
                            className={cn(
                                "focus-ring flex items-center gap-2.5 text-left transition-colors",
                                classic
                                    ? cn("mt-1 h-10 rounded-md hover:bg-white/[0.04] data-[state=open]:bg-white/[0.06]", wide ? "px-2" : "w-10 justify-center")
                                    : cn(
                                          "mt-1.5 rounded-[10px] data-[state=open]:bg-white/[0.07]",
                                          wide ? "h-12 bg-white/[0.03] px-2 hover:bg-white/[0.055]" : "size-10 justify-center hover:bg-white/[0.05]",
                                      ),
                            )}
                            aria-label="Account"
                        >
                            {status?.user?.avatar?.large ? (
                                <img src={img(status.user.avatar.large)} alt="" className={cn("shrink-0 rounded-full object-cover", classic ? "size-7" : "size-8 ring-1 ring-white/10")} />
                            ) : (
                                <span className={cn("grid shrink-0 place-items-center rounded-full bg-surface-3 text-muted", classic ? "size-7" : "size-8 ring-1 ring-white/10")}>
                                    <UserRound className="size-4" />
                                </span>
                            )}
                            {wide && (
                                <span className="min-w-0 flex-1">
                                    <span className="block truncate text-[13px] font-medium">{status?.user ? status.user.name : "Not logged in"}</span>
                                    <span className="block truncate text-[11px] text-subtle">{status?.user ? "AniList" : "Log in to sync"}</span>
                                </span>
                            )}
                        </button>
                    </DropdownTrigger>
                    <DropdownContent align="start" side="top" className="w-52">
                        <DropdownLabel>{status?.user ? status.user.name : "Not logged in"}</DropdownLabel>
                        {status?.user ? (
                            <>
                                <DropdownItem icon={<UserRound />} onSelect={() => navigate("/profile")}>
                                    Your profile
                                </DropdownItem>
                                {/* A TV has no browser to open it in. */}
                                {!tv && (
                                    <DropdownItem icon={<ExternalLink />} onSelect={() => window.open(`https://anilist.co/user/${status.user!.name}`, "_blank", "noopener")}>
                                        Open on AniList
                                    </DropdownItem>
                                )}
                                <DropdownSeparator />
                                <DropdownItem icon={<LogOut />} danger onSelect={logout}>
                                    Log out
                                </DropdownItem>
                            </>
                        ) : (
                            <DropdownItem icon={<LogIn />} onSelect={() => setLoginOpen(true)}>
                                Log in with AniList
                            </DropdownItem>
                        )}
                    </DropdownContent>
                </Dropdown>
            </div>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
        </aside>
    )
}

// Mutes notifications (toasts), all but errors (lib/toast.ts), or turns them
// back on.
function MuteButton({ side }: { side: "top" | "right" }) {
    const muted = useStore(notificationsMutedStore)
    const label = muted ? "Unmute notifications" : "Mute notifications"
    const toggle = () => {
        if (!muted) sonner.dismiss() // what's on screen goes too
        notificationsMutedStore.set(!muted)
        // Shown past the mute: it says what just happened.
        sonner.message(muted ? "Notifications on" : "Notifications muted. Errors still show.", { duration: 2500 })
    }
    return (
        <Tooltip content={label} side={side}>
            <button
                onClick={toggle}
                aria-label={label}
                aria-pressed={muted}
                className={cn(
                    "focus-ring grid size-9 shrink-0 place-items-center rounded-md transition-colors hover:bg-white/[0.04] [&>svg]:size-[18px] [&>svg]:stroke-[1.75]",
                    muted ? "text-amber-300" : "text-muted hover:text-fg",
                )}
            >
                {muted ? <BellOff /> : <Bell />}
            </button>
        </Tooltip>
    )
}
