import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
    AppWindow,
    Bell,
    BookOpen,
    Check,
    ChevronDown,
    ChevronRight,
    Clapperboard,
    Copy,
    Cpu,
    DatabaseBackup,
    Eye,
    EyeOff,
    FileClock,
    Folder,
    FolderOpen,
    Gamepad2,
    HardDrive,
    LibraryBig,
    Magnet,
    MonitorPlay,
    MonitorSmartphone,
    Moon,
    ShieldCheck,
    Network,
    Palette,
    Plus,
    Puzzle,
    RefreshCw,
    Save,
    Search as SearchIcon,
    Terminal,
    Trash2,
    Tv,
    UserRound,
    Wifi,
    X,
    Upload,
} from "lucide-react"
import { createContext, isValidElement, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { toast } from "@/lib/toast"
import { DirPicker } from "@/components/DirPicker"
import { ParentalDialog } from "@/components/ParentalDialog"
import { LoginDialog } from "@/components/LoginDialog"
import { Badge, Button, EmptyState, Input, Select, Switch, Textarea } from "@/components/ui"
import { UpdateBanner, versionName } from "@/components/UpdateBanner"
import { whatsNewStore } from "@/components/WhatsNew"
import { api, desktop, type DesktopPrefs } from "@/lib/api"
import { refreshSharing, useCheckUpdate, useOnlineProviders, useParental, useSaveSettings, useSharing, useStatus, useUpdateStatus } from "@/lib/queries"
import { exampleMpvPath, installHint, installSource, platformName } from "@/lib/platform"
import type { Tool } from "@/lib/platform"
import { canInstallPrograms, useInstallPrograms, useWatchSetup } from "@/lib/programs"
import { useStore } from "@/lib/store"
import { useFadeBelow } from "@/lib/hooks"
import { activeHoliday, COLOR_THEMES, HOLIDAYS, themePreviewStore, useClassic } from "@/lib/themes"
import { setSubStyle, SUB_BACKGROUNDS, SUB_POSITIONS, SUB_SIZES, subStyleStore, subStyleVars, type SubStyle } from "@/lib/subtitles"
import { systemNotifications } from "@/lib/alerts"
import { androidApp, setTvWidth, TV_WIDTHS, tv, tvWidth } from "@/lib/tv"
import { UploadDialog } from "@/components/UploadDialog"
import type { Settings, SharingPeer, Status } from "@/lib/types"
import { cn, compareVersions, copyText, formatBytes, img, relativeTime } from "@/lib/utils"

type SectionId =
    | "account"
    | "appearance"
    | "notifications"
    | "updates"
    | "playback"
    | "mpv"
    | "transcode"
    | "discord"
    | "library"
    | "streaming"
    | "torrents"
    | "manga"
    | "extensions"
    | "devices"
    | "network"
    | "desktop"
    | "backup"
    | "logs"

type NavItem = {
    id: SectionId
    label: string
    icon: React.ReactNode
    // What it's for, under its title.
    description: string
    // Other words people look for it by (search).
    keywords?: string
}

// Settings in five groups, each page about one thing.
const NAV: { title: string; items: NavItem[] }[] = [
    {
        title: "General",
        items: [
            { id: "account", label: "Account", icon: <UserRound />, description: "Your AniList account", keywords: "login log in anilist sign" },
            { id: "appearance", label: "Appearance", icon: <Palette />, description: "Themes, colors, holidays, the classic look and what's shown", keywords: "ui theme color dark classic look titles spoilers card size" },
            { id: "notifications", label: "Notifications", icon: <Bell />, description: "Alerts for new episodes and finished downloads", keywords: "alerts notify" },
            { id: "updates", label: "Updates & about", icon: <RefreshCw />, description: "Aniko's version, updates and beta updates", keywords: "version beta about what's new" },
        ],
    },
    {
        title: "Watching",
        items: [
            { id: "playback", label: "Playback", icon: <Clapperboard />, description: "How episodes play, progress, audio and subtitles", keywords: "player video subtitles audio dub sub skip" },
            { id: "mpv", label: "mpv player", icon: <MonitorPlay />, description: "The desktop media player Aniko can play in", keywords: "external player" },
            { id: "transcode", label: "Video conversion", icon: <Cpu />, description: "So the in-app player plays every format", keywords: "transcoding direct play ffmpeg hardware gpu hevc" },
            { id: "discord", label: "Discord", icon: <Gamepad2 />, description: "Show what you're watching on Discord", keywords: "rich presence status" },
        ],
    },
    {
        title: "Finding anime",
        items: [
            { id: "library", label: "Local library", icon: <LibraryBig />, description: "Your anime folders and how they're scanned", keywords: "folder files scan matching downloads" },
            { id: "streaming", label: "Online streaming", icon: <Tv />, description: "ani-cli and streaming extensions", keywords: "ani-cli anicli sub dub stream" },
            { id: "torrents", label: "Torrents", icon: <Magnet />, description: "Torrent search, your torrent client and the auto downloader", keywords: "qbittorrent transmission nyaa rss provider client" },
            { id: "manga", label: "Manga", icon: <BookOpen />, description: "Reading manga with provider extensions", keywords: "reader" },
            { id: "extensions", label: "Extensions", icon: <Puzzle />, description: "Where extensions come from", keywords: "marketplace plugins" },
        ],
    },
    {
        title: "Devices & network",
        items: [
            { id: "devices", label: "Other devices", icon: <MonitorSmartphone />, description: "Aniko on your other computers and TVs: sharing, waking, syncing", keywords: "share sharing tv fire library wake sync" },
            { id: "network", label: "Network & web UI", icon: <Wifi />, description: "Using Aniko from a browser or other devices at home", keywords: "lan port password browser web" },
        ],
    },
    {
        title: "This computer",
        items: [
            { id: "desktop", label: "Desktop app & programs", icon: <AppWindow />, description: "The window, the tray, starting at login, and the programs Aniko uses", keywords: "tray background startup login ffmpeg yt-dlp mpv install" },
            { id: "backup", label: "Backup & data", icon: <DatabaseBackup />, description: "Back up and restore, and where Aniko keeps its data", keywords: "restore export import folder" },
            { id: "logs", label: "Logs & cache", icon: <FileClock />, description: "Server logs and cached data", keywords: "debug clear" },
        ],
    },
]

// Not in Aniko's Android app (a TV's): it has no mpv, Discord, ffmpeg or
// desktop window.
const NOT_ON_ANDROID: SectionId[] = ["mpv", "transcode", "discord", "desktop"]
const SHOWN_NAV = androidApp ? NAV.map(g => ({ ...g, items: g.items.filter(i => !NOT_ON_ANDROID.includes(i.id)) })) : NAV
const NAV_ITEMS = SHOWN_NAV.flatMap(g => g.items)

// Older links (and the names before the regrouping).
const ALIASES: Record<string, SectionId> = {
    app: "account",
    ui: "appearance",
    about: "updates",
    "torrent-provider": "torrents",
    "torrent-client": "torrents",
    anicli: "streaming",
    "online-streaming": "streaming",
}
const SECTION_IDS: string[] = NAV_ITEMS.map(i => i.id)

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

// Puts the fields edited in draft (compared with base, the server copy the
// draft was made from) on top of a newer server copy.
// The UI settings the Themes menu changes (saved at once).
const THEME_KEYS = ["colorTheme", "accentColor", "seasonal", "seasonalEffects", "classicLook"] as const

function rebase(draft: Settings, base: Settings, server: Settings): Settings {
    const out: Record<string, any> = structuredClone(server)
    const old: Record<string, any> = base
    for (const [key, value] of Object.entries(draft) as [string, any][]) {
        if (same(value, old[key])) continue
        if (value && typeof value === "object" && !Array.isArray(value) && old[key] && typeof old[key] === "object") {
            for (const field of Object.keys(value)) if (!same(value[field], old[key][field])) out[key] = { ...out[key], [field]: value[field] }
        } else out[key] = value
    }
    return out as Settings
}

export default function SettingsPage() {
    const { data: status } = useStatus()
    const [params, setParams] = useSearchParams()
    const raw = params.get("tab") ?? "app"
    const tab = ALIASES[raw] ?? raw
    // Unknown tabs (old links, typos) open the first one instead of nothing.
    const section = (SECTION_IDS.includes(tab) ? tab : "account") as SectionId
    const save = useSaveSettings()
    const server = status?.settings
    // draft is what the form edits, base the server copy it was made from.
    const [draft, setDraft] = useState<Settings | null>(null)
    // Search settings: every section's that match, at once.
    const [query, setQuery] = useState("")
    // The sections' list, when it's taller than the window.
    const asideFade = useFadeBelow()
    const [base, setBase] = useState<Settings | null>(null)
    const reset = (s: Settings) => {
        setDraft(structuredClone(s))
        setBase(s)
    }
    useEffect(() => {
        if (!server) return
        if (!draft || !base) {
            reset(server)
            return
        }
        if (same(server, base)) return
        // Saved somewhere else meanwhile (another tab or device, the auto
        // downloader switch…): follow it, keeping what was edited here so a
        // save doesn't put the old values back.
        const next = rebase(draft, base, server)
        // A theme picked in the sidebar's Themes menu is the latest choice:
        // it's shown here too, over one being tried here.
        const picked: string[] = THEME_KEYS.filter(k => !same(server.ui[k], base.ui[k]))
        for (const k of picked) (next.ui as Record<string, unknown>)[k] = (server.ui as Record<string, unknown>)[k]
        const onlyThemes = (Object.keys(server) as (keyof Settings)[]).every(k =>
            k === "ui" ? (Object.keys(server.ui) as (keyof Settings["ui"])[]).every(f => picked.includes(f) || same(server.ui[f], base.ui[f])) : same(server[k], base[k]),
        )
        if (!onlyThemes && !same(draft, base) && !same(next, draft)) toast.info("Settings were changed somewhere else. Your unsaved changes here were kept.")
        setDraft(next)
        setBase(server)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [server])
    const dirty = useMemo(() => !!draft && !!server && !same(draft, server), [draft, server])
    const classic = useClassic()

    // Preview the colors and holiday theme being edited; App shows the saved
    // ones again once the preview ends (Discard, leaving the page).
    const ui = draft?.ui
    useEffect(() => {
        themePreviewStore.set(ui ? { colorTheme: ui.colorTheme, accentColor: ui.accentColor, seasonal: ui.seasonal, seasonalEffects: ui.seasonalEffects, classicLook: ui.classicLook } : null)
    }, [ui?.colorTheme, ui?.accentColor, ui?.seasonal, ui?.seasonalEffects, ui?.classicLook])
    useEffect(() => () => themePreviewStore.set(null), [])

    if (!draft || !status) return null
    const set = <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => setDraft(d => (d ? { ...d, [k]: { ...d[k], ...v } } : d))
    const current = NAV_ITEMS.find(i => i.id === section) ?? NAV_ITEMS[0]

    const onSave = () => {
        const oldPort = status.settings.server.port
        save.mutate(draft, {
            onSuccess: s => {
                reset(s)
                // The server moves to the new port; follow it.
                if (s.server.port !== oldPort && ["127.0.0.1", "localhost"].includes(location.hostname)) {
                    setTimeout(() => {
                        location.href = `${location.protocol}//${location.hostname}:${s.server.port}${location.pathname}${location.search}`
                    }, 1500)
                }
            },
        })
    }

    const sectionProps = { draft, set }
    const searching = query.trim() !== ""
    const navButton = (it: NavItem) => (
        <button
            key={it.id}
            onClick={() => {
                setQuery("")
                setParams({ tab: it.id }, { replace: true })
            }}
            aria-current={!searching && section === it.id ? "page" : undefined}
            // A TV's remote starts on the open section.
            data-tv-autofocus={(!searching && section === it.id) || undefined}
            className={cn(
                "focus-ring flex h-8 items-center gap-2.5 px-2.5 text-[13.5px] font-medium transition-colors [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:stroke-[1.75]",
                classic
                    ? cn("rounded-md", !searching && section === it.id ? "bg-white/[0.08] text-fg" : "text-muted hover:bg-white/[0.04] hover:text-fg")
                    : cn("rounded-lg", !searching && section === it.id ? "bg-white/[0.07] text-fg [&>svg]:text-brand-strong" : "text-muted hover:bg-white/[0.05] hover:text-fg"),
            )}
        >
            {it.icon}
            <span className="truncate">{it.label}</span>
        </button>
    )

    return (
        <div className="flex min-h-full flex-col gap-6 px-6 pt-8 pb-24 md:flex-row md:gap-8 md:px-8 lg:gap-12 xl:px-10">
            <aside ref={asideFade.ref} style={asideFade.style} className="no-scrollbar w-full shrink-0 md:sticky md:top-8 md:max-h-[calc(100vh-5rem)] md:w-56 md:self-start md:overflow-y-auto">
                <h1 className="mb-4 px-2.5 text-[1.75rem] font-semibold tracking-tight">Settings</h1>
                <div className="mb-4 md:px-0.5">
                    <Input
                        value={query}
                        onChange={e => setQuery(e.target.value)}
                        onKeyDown={e => e.key === "Escape" && setQuery("")}
                        placeholder="Search settings"
                        aria-label="Search settings"
                        icon={<SearchIcon className="size-4" />}
                    />
                </div>
                {/* A short list on a phone: the sections in a menu. */}
                <div className="md:hidden">
                    <Select
                        value={section}
                        onChange={v => {
                            setQuery("")
                            setParams({ tab: v }, { replace: true })
                        }}
                        options={SHOWN_NAV.flatMap(g => g.items.map(it => ({ value: it.id, label: `${g.title} › ${it.label}` })))}
                    />
                </div>
                <nav aria-label="Settings sections" className="hidden flex-col gap-5 md:flex">
                    {SHOWN_NAV.map(g => (
                        <div key={g.title} className="flex flex-col gap-0.5">
                            <p className="px-2.5 pb-1 text-[11px] font-medium text-subtle">{g.title}</p>
                            {g.items.map(navButton)}
                        </div>
                    ))}
                </nav>
                <p className="mt-6 hidden px-2.5 text-xs text-subtle md:block">
                    Aniko {status.version} · {platformName(status.platform)} · {status.client === "desktop" ? "Desktop" : status.client === "lan" ? "LAN" : "Web UI"}
                </p>
            </aside>

            <section className="min-w-0 flex-1 lg:max-w-4xl">
                {status.client === "lan" && (
                    <p className="mb-6 rounded-lg bg-white/[0.04] px-3 py-2 text-xs text-muted lg:mt-[3.75rem]">
                        You're using Aniko from another device: dimmed settings (folders, programs, network, logins) can only be changed on the computer it runs on.
                    </p>
                )}
                {searching ? (
                    <SearchResults query={query} sectionProps={sectionProps} onOpen={id => {
                        setQuery("")
                        setParams({ tab: id }, { replace: true })
                    }} />
                ) : (
                    <>
                        <div className={cn("mb-7", status.client !== "lan" && "lg:mt-[3.75rem]")}>
                            <h2 className="text-xl font-semibold tracking-tight">{current.label}</h2>
                            <p className="mt-1 text-sm text-muted">{current.description}</p>
                        </div>
                        <div key={section} className="flex flex-col gap-8 fade-in">
                            <SectionBody id={section} {...sectionProps} />
                        </div>
                    </>
                )}
                {/* Only while there's something to save. */}
                {dirty && (
                    <div className="sticky bottom-6 z-20 mt-8 flex justify-center">
                        <div className="flex items-center gap-2 rounded-full border border-line-strong bg-surface-2 py-1.5 pr-1.5 pl-4 shadow-[0_12px_32px_-12px_rgb(0_0_0/0.7)] pop-in">
                            <span className="mr-1 text-[13px] text-muted">Unsaved changes</span>
                            <Button variant="ghost" size="sm" className="rounded-full" onClick={() => reset(status.settings)}>
                                Discard
                            </Button>
                            <Button variant="primary" size="sm" className="rounded-full" icon={<Save className="size-4" />} loading={save.isPending} onClick={onSave}>
                                Save
                            </Button>
                        </div>
                    </div>
                )}
            </section>
        </div>
    )
}

// One section's settings.
function SectionBody({ id, draft, set }: { id: SectionId } & SectionProps) {
    const props = { draft, set }
    switch (id) {
        case "account":
            return <AccountSection {...props} />
        case "appearance":
            return <UISection {...props} />
        case "notifications":
            return <AlertsGroup {...props} />
        case "updates":
            return <AboutSection />
        case "playback":
            return <PlaybackSection {...props} />
        case "mpv":
            return <MpvSection {...props} />
        case "transcode":
            return <TranscodeSection {...props} />
        case "discord":
            return <DiscordSection {...props} />
        case "library":
            return <LibrarySection {...props} />
        case "streaming":
            return <StreamingSection {...props} />
        case "torrents":
            return (
                <>
                    <TorrentProviderSection {...props} />
                    <TorrentClientSection {...props} />
                </>
            )
        case "manga":
            return <MangaSection {...props} />
        case "extensions":
            return <ExtensionsSection {...props} />
        case "devices":
            return <DevicesSection {...props} />
        case "network":
            return <NetworkSection {...props} />
        case "desktop":
            return <DesktopSection />
        case "backup":
            return <BackupSection />
        case "logs":
            return <LogsSection />
    }
}

// ---------------------------------------------------------------------------
// Search: every section at once, showing only the settings that match (by
// their names and descriptions, their group's or their section's). What's
// found can be changed right there.

type SearchState = { words: string[]; all: boolean }
const SearchContext = createContext<SearchState | null>(null)

// The words of a node's text (labels can be elements).
function textOf(n: React.ReactNode): string {
    if (n == null || typeof n === "boolean") return ""
    if (typeof n === "string" || typeof n === "number") return String(n)
    if (Array.isArray(n)) return n.map(textOf).join(" ")
    if (isValidElement(n)) return textOf((n.props as { children?: React.ReactNode }).children)
    return ""
}

const fold = (s: string) => s.toLowerCase().normalize("NFKD").replace(/[\u0300-\u036f]/g, "")
// Each word starts a word of the texts ("port" finds Port, not import).
const matches = (words: string[], ...texts: React.ReactNode[]) => {
    const found = fold(texts.map(textOf).join(" ")).split(/[^\p{L}\p{N}]+/u)
    return words.every(w => found.some(t => t.startsWith(w)))
}

// Whether a setting shows: always, unless searching and it doesn't match.
function useShown(...texts: React.ReactNode[]) {
    const search = useContext(SearchContext)
    return !search || search.all || matches(search.words, ...texts)
}

// A section's matching settings; when none match but the section itself
// does (its name, what it's for, its other words), all of it.
function SearchSection({ item, words, sectionProps, onOpen }: { item: NavItem; words: string[]; sectionProps: SectionProps; onOpen: (id: SectionId) => void }) {
    const ref = useRef<HTMLDivElement>(null)
    const [all, setAll] = useState(false)
    useLayoutEffect(() => {
        if (!all && !ref.current?.querySelector("[data-setting]") && matches(words, item.label, item.description, item.keywords)) setAll(true)
    }, [all, words, item])
    return (
        <SearchContext.Provider value={{ words, all }}>
            <div ref={ref} data-section className="flex flex-col gap-6">
                <button onClick={() => onOpen(item.id)} className="focus-ring group -mb-2 flex items-center gap-2 self-start rounded-md text-left">
                    <span className="text-muted [&>svg]:size-4">{item.icon}</span>
                    <span className="text-lg font-semibold tracking-tight group-hover:underline">{item.label}</span>
                    <ChevronRight className="size-4 text-subtle" />
                </button>
                <SectionBody id={item.id} {...sectionProps} />
            </div>
        </SearchContext.Provider>
    )
}

function SearchResults({ query, sectionProps, onOpen }: { query: string; sectionProps: SectionProps; onOpen: (id: SectionId) => void }) {
    const words = useMemo(() => fold(query).split(/[^\p{L}\p{N}]+/u).filter(Boolean), [query])
    const ref = useRef<HTMLDivElement>(null)
    const [found, setFound] = useState(true)
    // Found anything? Counted once drawn (each setting decides for itself).
    useLayoutEffect(() => {
        setFound(!!ref.current?.querySelector("[data-setting]"))
    })
    const sections = NAV_ITEMS.filter(it => it.id !== "logs")
    return (
        <div ref={ref} className="settings-search flex flex-col gap-10 lg:mt-[3.75rem]">
            {sections.map(it => (
                <SearchSection key={it.id + query} item={it} words={words} sectionProps={sectionProps} onOpen={onOpen} />
            ))}
            {!found && <EmptyState icon={<SearchIcon />} title="No settings match">Try other words, or pick a section on the left.</EmptyState>}
        </div>
    )
}

type SectionProps = { draft: Settings; set: <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => void }

// ---------------------------------------------------------------------------
// Layout helpers

function Group({ title, children, description }: { title?: string; description?: string; children: React.ReactNode }) {
    const search = useContext(SearchContext)
    // The group's own name or words found: all of it.
    const all = !!search && (search.all || (!!title && matches(search.words, title, description)))
    const body = (
        <div data-group data-all={all || undefined}>
            {title && <h3 className="mb-1 text-[15px] font-semibold">{title}</h3>}
            {description && <p className="mb-3 text-[13px] text-muted">{description}</p>}
            {!description && title && <div className="mb-3" />}
            <div data-group-body className="card divide-y divide-line">
                {children}
            </div>
        </div>
    )
    return search ? <SearchContext.Provider value={{ ...search, all }}>{body}</SearchContext.Provider> : body
}

// wide: the value (e.g. a long path) takes the remaining space and wraps
// instead of pushing out of the card. keywords: other words it's found by.
function Row({ label, help, children, wide, keywords }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; wide?: boolean; keywords?: string }) {
    if (!useShown(label, help, keywords)) return null
    return (
        // On a phone, a control that doesn't fit beside its label goes under it.
        <div data-setting className="flex items-center justify-between gap-x-6 gap-y-2.5 px-4 py-3.5 max-sm:flex-wrap">
            <div className="min-w-0 max-sm:flex-1 max-sm:basis-48">
                <p className="text-sm font-medium">{label}</p>
                {help && <p className="mt-0.5 text-[12.5px] leading-snug text-muted">{help}</p>}
            </div>
            <div className={wide ? "flex min-w-0 flex-1 justify-end" : "shrink-0"}>{children}</div>
        </div>
    )
}

function Stack({ label, help, children, keywords }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; keywords?: string }) {
    if (!useShown(label, help, keywords)) return null
    return (
        <div data-setting className="flex flex-col gap-2 px-4 py-3.5">
            <p className="text-sm font-medium">{label}</p>
            {children}
            {help && <p className="text-[12.5px] leading-snug text-muted">{help}</p>}
        </div>
    )
}

// Something other than a Row or a Stack that searching should find by text.
function Shown({ text, children }: { text: string; children: React.ReactNode }) {
    if (!useShown(text)) return null
    return <div data-setting>{children}</div>
}

function Password({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
    const [show, setShow] = useState(false)
    return (
        <div className="relative">
            <Input type={show ? "text" : "password"} value={value} placeholder={placeholder} onChange={e => onChange(e.target.value)} className="pr-10" />
            <button type="button" onClick={() => setShow(s => !s)} className="absolute top-1/2 right-3 -translate-y-1/2 text-subtle hover:text-fg">
                {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </button>
        </div>
    )
}

function Collapsible({ title, children }: { title: string; children: React.ReactNode }) {
    const [open, setOpen] = useState(false)
    // Searching: open, so what's found in it shows.
    const search = useContext(SearchContext)
    const all = !!search && (search.all || matches(search.words, title))
    const shown = open || !!search
    const body = (
        <div data-group data-all={all || undefined} className="card">
            <button onClick={() => setOpen(o => !o)} className="flex w-full items-center justify-between px-4 py-3.5 text-[15px] font-medium">
                {title}
                <ChevronDown className={cn("size-4 text-muted transition-transform duration-200", shown && "rotate-180")} />
            </button>
            {shown && (
                <div data-group-body className="divide-y divide-line border-t border-line">
                    {children}
                </div>
            )}
        </div>
    )
    return search ? <SearchContext.Provider value={{ ...search, all }}>{body}</SearchContext.Provider> : body
}

// Settings only the computer Aniko runs on may change (from a device on the
// network the server keeps them as they are): shown, not editable there.
function Locked({ children }: { children: React.ReactNode }) {
    const { data: status } = useStatus()
    if (status?.client !== "lan") return <>{children}</>
    return (
        <fieldset disabled title="Only the computer Aniko runs on can change this" className="m-0 min-w-0 border-0 p-0 opacity-55">
            {children}
        </fieldset>
    )
}

function PathInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
    const [open, setOpen] = useState(false)
    const { data: status } = useStatus()
    return (
        <div className="flex items-center gap-2">
            <div className="relative flex-1">
                <Input value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder} icon={<Folder className="size-4 text-amber-300" />} />
                {value && <Check className="absolute top-1/2 right-3 size-4 -translate-y-1/2 text-emerald-400" />}
            </div>
            {status?.client !== "lan" && (
                <button onClick={() => setOpen(true)} className="grid size-10 place-items-center rounded-xl text-muted hover:bg-white/[0.06] hover:text-fg" aria-label="Browse">
                    <FolderOpen className="size-5" />
                </button>
            )}
            <DirPicker open={open} onOpenChange={setOpen} onPick={onChange} start={value} />
        </div>
    )
}

const toLines = (text: string) => text.split("\n").map(s => s.trim()).filter(Boolean)

// One entry per line. The box keeps the text as typed (blank lines, spaces)
// and only the cleaned-up list goes into the draft, so typing is never
// rewritten under the cursor; leaving the box tidies it.
function LinesInput({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
    const joined = value.join("\n")
    const [text, setText] = useState(joined)
    // The list was replaced from outside (Discard, a save, a change made elsewhere).
    useEffect(() => setText(t => (toLines(t).join("\n") === joined ? t : joined)), [joined])
    return (
        <Textarea
            value={text}
            onChange={e => {
                setText(e.target.value)
                onChange(toLines(e.target.value))
            }}
            onBlur={() => setText(joined)}
        />
    )
}

function Detect({ ok, label }: { ok?: boolean; label: string }) {
    return ok ? <Badge tone="green">{label} found</Badge> : <Badge tone="red">{label} not found</Badge>
}

// Where other devices open Aniko, once LAN access is saved.
function LanAddresses({ status }: { status?: Status }) {
    const urls = status?.lanUrls ?? []
    if (!status?.settings.server.allowLan) return <p className="px-4 py-3.5 text-sm text-muted">Save, and the address to open on your other devices shows up here.</p>
    if (!urls.length)
        return (
            <p className="px-4 py-3.5 text-sm text-amber-200/80">
                Aniko can't find this computer's address on your network. Is it connected to your router (by cable or Wi-Fi)?
            </p>
        )
    return (
        <div className="px-4 py-3.5">
            <p className="text-sm font-medium">On your other devices, open</p>
            <div className="mt-2 flex flex-wrap gap-2">
                {urls.map(u => (
                    <button
                        key={u}
                        onClick={() => copyText(u).then(ok => (ok ? toast.success("Address copied") : toast.error("Couldn't copy it")))}
                        className="flex items-center gap-2 rounded-lg bg-black/30 px-3 py-1.5 font-mono text-sm transition hover:bg-black/45"
                        title="Copy"
                    >
                        {u}
                        <Copy className="size-3.5 text-subtle" />
                    </button>
                ))}
            </div>
            <p className="mt-2 text-xs text-subtle">
                Type it with http://, not https://. On a Mac, Chrome, Brave and Firefox first need Local Network access (System Settings › Privacy & Security ›
                Local Network); Safari works right away. A VPN on this computer can block other devices too: in Mullvad, turn on Local network sharing.
            </p>
        </div>
    )
}

// ---------------------------------------------------------------------------
// Sections

function AccountSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const [loginOpen, setLoginOpen] = useState(false)
    const qc = useQueryClient()
    const lan = status?.client === "lan"
    return (
        <>
            <Group title="AniList">
                <Shown text="AniList account log in log out connected">
                <div className="flex items-center justify-between gap-4 px-4 py-4">
                    {status?.user ? (
                        <div className="flex items-center gap-3">
                            <img src={img(status.user.avatar.large)} alt="" className="size-11 rounded-full" />
                            <div>
                                <p className="font-semibold">{status.user.name}</p>
                                <p className="text-xs text-subtle">Lists, progress and scores sync with AniList</p>
                            </div>
                        </div>
                    ) : (
                        <div>
                            <p className="font-medium">Not connected</p>
                            <p className="text-xs text-subtle">Your list is stored locally until you log in.</p>
                        </div>
                    )}
                    {status?.user && lan ? (
                        <p className="max-w-56 text-right text-xs text-subtle">The computer Aniko runs on is logged in: log out there.</p>
                    ) : status?.user ? (
                        <Button
                            variant="danger"
                            size="sm"
                            onClick={async () => {
                                try {
                                    await api.post("/api/auth/logout")
                                    qc.invalidateQueries()
                                } catch (e: any) {
                                    toast.error(e.message)
                                }
                            }}
                        >
                            Log out
                        </Button>
                    ) : (
                        <Button variant="primary" size="sm" onClick={() => setLoginOpen(true)}>
                            Log in with AniList
                        </Button>
                    )}
                </div>
                </Shown>
                <Locked>
                    <Stack label="AniList client ID" help="Aniko opens anilist.co/api/v2/oauth/authorize?client_id=…&response_type=token">
                        <Input value={draft.anilist.clientId} onChange={e => set("anilist", { clientId: e.target.value })} />
                    </Stack>
                </Locked>
            </Group>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
        </>
    )
}

function NetworkSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const isDesktop = status?.client === "desktop"
    const lan = status?.client === "lan"
    return (
        <>
            <Group title="Web UI & network" description="Aniko only ever talks to this computer and — if you allow it — your home network. Connections from the internet are always refused.">
                {/* The Android app is its own window, on its own port. */}
                {!androidApp && (
                    <Row
                        label="Web UI"
                        help={
                            status?.webUiForced
                                ? "Running without the desktop app, so the browser UI is always on."
                                : `Use Aniko from any browser at http://127.0.0.1:${draft.server.port}`
                        }
                    >
                        <Switch checked={draft.server.webUi || !!status?.webUiForced} disabled={!isDesktop || status?.webUiForced} onChange={v => set("server", { webUi: v })} />
                    </Row>
                )}
                <Row label="Allow devices on my network" help="Phones, tablets, TVs and other computers on your home network can open Aniko in a browser.">
                    <Switch checked={draft.server.allowLan} disabled={lan} onChange={v => set("server", { allowLan: v, webUi: v ? true : draft.server.webUi })} />
                </Row>
                {draft.server.allowLan && <LanAddresses status={status} />}
                {draft.server.allowLan && (
                    <Stack label="Network password" help="Asked once per device on your network. This computer never needs it.">
                        <Password value={draft.server.password} onChange={v => set("server", { password: v })} placeholder="Leave empty for no password" />
                    </Stack>
                )}
                {!androidApp && (
                    <Row label="Port" help="Restart-free: Aniko moves to the new port when you save.">
                        <Input type="number" className="w-28" disabled={lan} value={draft.server.port} onChange={e => set("server", { port: Number(e.target.value) })} />
                    </Row>
                )}
                {!isDesktop && !lan && !androidApp && <p className="px-4 py-3 text-xs text-amber-200/80">Turning the Web UI off is only possible from the desktop app, so you can't lock yourself out.</p>}
            </Group>
        </>
    )
}

function ExtensionsSection({ draft, set }: SectionProps) {
    const navigate = useNavigate()
    return (
        <>
            <Group title="Extensions">
                <Locked>
                    <Stack label="Marketplace URL" help="Any compatible extension index works.">
                        <Input value={draft.extensions.marketplaceUrl} onChange={e => set("extensions", { marketplaceUrl: e.target.value })} />
                    </Stack>
                </Locked>
            </Group>
            <Group title="Your extensions">
                <Row label="Installed extensions and the marketplace" help="Install, update, set up and remove extensions on the Extensions page." keywords="install update marketplace">
                    <Button size="sm" variant="ghost" onClick={() => navigate("/extensions")}>
                        Open
                    </Button>
                </Row>
            </Group>
        </>
    )
}

function DesktopSection() {
    return (
        <>
            <DesktopGroup />
            <ProgramsGroup />
        </>
    )
}

function BackupSection() {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    return (
        <>
            {!androidApp && !lan && <BackupGroup />}
            <Group title="Data">
                <Row label="Data folder" help="Database, cache and extension storage" wide>
                    <code title={status?.dataDir} className="min-w-0 rounded-lg bg-black/30 px-2 py-1 text-xs break-all">
                        {status?.dataDir}
                    </code>
                </Row>
            </Group>
        </>
    )
}

// The desktop app's own settings, saved by the app itself (they take effect
// at once, no Save).
function DesktopGroup() {
    const bridge = desktop()
    const [prefs, setPrefs] = useState<DesktopPrefs | null>(null)
    useEffect(() => {
        bridge?.getPrefs?.().then(setPrefs, () => {})
    }, [bridge])
    if (!bridge?.setPref || !prefs) return null
    const mac = bridge.platform === "darwin"
    const win = bridge.platform === "win32"
    const change = async (name: keyof DesktopPrefs, value: boolean) => {
        setPrefs({ ...prefs, [name]: value })
        const res = await bridge.setPref!(name, value).catch(() => false as const)
        if (res !== true) {
            setPrefs(prefs)
            toast.error(typeof res === "object" && res.error ? res.error : "Couldn't change that setting")
        }
    }
    return (
        <Group title="Desktop app">
            <Row
                label="Keep running when the window is closed"
                help={
                    mac
                        ? "Off: closing the window quits Aniko. On: streaming to your other devices, downloads and the auto downloader keep going, without the window's memory; click Aniko in the Dock to open it again, quit with Cmd+Q or the button below."
                        : `Off: closing the window quits Aniko. On: streaming to your other devices, downloads and the auto downloader keep going, without the window's memory; open or quit Aniko from its tray icon${win ? " (at the right of the taskbar, under ^ when hidden)" : ""}, or open it again from ${win ? "the Start menu" : "your app menu"} and quit with the button below.`
                }
            >
                <Switch checked={prefs.keepRunning} onChange={v => change("keepRunning", v)} />
            </Row>
            {prefs.startAtLogin !== undefined && (
                <Row
                    label="Start Aniko when you log in"
                    help={
                        prefs.keepRunning
                            ? `In the background, ${mac ? "in the Dock" : "with its tray icon"}: ready for your other devices, downloads and the auto downloader without opening its window.`
                            : "It opens its window. Turn on keeping it running to start it in the background instead."
                    }
                >
                    <Switch checked={prefs.startAtLogin} onChange={v => change("startAtLogin", v)} />
                </Row>
            )}
            {/* Linux doesn't tell apps the computer was locked. */}
            {(mac || win) && (
                <Row
                    label="Free memory while the computer is locked"
                    help="After 5 minutes locked, the window lets go of the page and loads it again when you unlock. Never while a video, the manga reader or Settings is open."
                >
                    <Switch checked={prefs.freeWhenLocked} onChange={v => change("freeWhenLocked", v)} />
                </Row>
            )}
            {bridge.quit && (
                <Row label="Quit Aniko" help="Closes the window and stops the server: streaming to other devices, downloads and library sharing stop until Aniko is opened again.">
                    <Button size="sm" variant="ghost" onClick={() => bridge.quit!()}>
                        Quit
                    </Button>
                </Row>
            )}
        </Group>
    )
}

function InstallProgramsButton() {
    const { data: status } = useStatus()
    const install = useInstallPrograms(status)
    return (
        <Button variant="primary" size="sm" loading={install.isPending} disabled={status?.setupRunning} onClick={() => install.mutate()}>
            {status?.setupRunning ? "Installing…" : "Install"}
        </Button>
    )
}

// What Aniko found of the programs it uses and, on Windows and macOS, the
// setup that installs them.
function ProgramsGroup() {
    const { data: status } = useStatus()
    useWatchSetup(status)
    const f = status?.features
    const windows = status?.platform === "windows"
    const programs: { tool: Tool; ok?: boolean; use: string }[] = [
        { tool: "ffmpeg", ok: f?.ffmpeg, use: "The in-app player (most files) and episode downloads" },
        { tool: "ani-cli", ok: f?.aniCli, use: windows ? "Sub/dub streaming and downloads (it runs in Git's bash)" : "Sub/dub streaming and downloads" },
        { tool: "mpv", ok: f?.mpv, use: "The external player" },
        { tool: "yt-dlp", ok: f?.ytDlp, use: "Faster, more reliable episode downloads" },
    ]
    return (
        <Group title="Programs" description="Aniko uses these programs for some of what it does.">
            {programs.map(p => (
                <Row
                    key={p.tool}
                    label={p.tool}
                    help={
                        p.ok || canInstallPrograms(status) ? (
                            p.use
                        ) : (
                            <>
                                {p.use}. {installSource(status?.platform, p.tool)} <code className="rounded bg-black/30 px-1.5 py-0.5">{installHint(status?.platform, p.tool)}</code>
                            </>
                        )
                    }
                >
                    <Badge tone={p.ok ? "green" : "red"}>{p.ok ? "Found" : "Not found"}</Badge>
                </Row>
            ))}
            {canInstallPrograms(status) && (
                <Row
                    label="Install missing programs"
                    help={
                        status?.platform === "darwin"
                            ? "Downloads ffmpeg, yt-dlp and ani-cli for this Mac (Apple silicon or Intel) into Aniko's folder, in a Terminal window: no Homebrew or password needed. mpv, the external player, comes from Homebrew on Apple silicon Macs that have it. Programs you already have are skipped."
                            : "Installs Git, ani-cli, ffmpeg, mpv, yt-dlp and the rest of what Aniko uses with Scoop, for your Windows user (no administrator rights), in a PowerShell window. Programs you already have are skipped. It also offers to sign you in to GitHub, for Aniko's updates."
                    }
                >
                    <InstallProgramsButton />
                </Row>
            )}
        </Group>
    )
}

// Backup: the data of this Aniko as one file (settings, watch history, list,
// choices, matches made by hand, auto downloader rules, extensions), and
// back. A restore merges, then Aniko starts again.
function BackupGroup() {
    const [logins, setLogins] = useState(false)
    const [restoring, setRestoring] = useState(false)
    const restore = async (file: File) => {
        let text: string
        let info: { aniko?: number; version?: string; createdAt?: number; computer?: string; logins?: boolean; history?: unknown[] }
        try {
            text = await file.text()
            info = JSON.parse(text)
        } catch {
            toast.error("That isn't an Aniko backup")
            return
        }
        if (!info?.aniko) {
            toast.error("That isn't an Aniko backup")
            return
        }
        const when = info.createdAt ? new Date(info.createdAt * 1000).toLocaleString() : "an unknown date"
        if (
            !confirm(
                `Restore the backup of ${info.computer || "a computer"} from ${when}?\n\n` +
                    "It's merged with what's here: positions and list entries newer here stay, and so do this computer's network and sharing settings. " +
                    "What's here now is saved first, in Aniko's data folder. Aniko starts again after.",
            )
        )
            return
        setRestoring(true)
        try {
            const res = await fetch("/api/backup/restore", { method: "POST", headers: { "Content-Type": "application/json" }, body: text, credentials: "same-origin" })
            const sum = await res.json().catch(() => null)
            if (!res.ok) throw new Error(sum?.error || res.statusText)
            const parts = [sum.history && `${sum.history} positions`, sum.list && `${sum.list} list entries`, sum.extensions && `${sum.extensions} extensions`, sum.rules && `${sum.rules} auto downloader rules`].filter(Boolean)
            toast.success(`Restored${parts.length ? `: ${parts.join(", ")}` : ""}. Aniko is starting again…`, { duration: 15_000 })
            // Back once it answers again, with everything read afresh.
            await waitForRestart()
            location.reload()
        } catch (e: any) {
            toast.error(e.message)
            setRestoring(false)
        }
    }
    return (
        <Group title="Backup" description="Your settings, watch history, list, audio and subtitle choices, matches made by hand, auto downloader rules and extensions, as one file. Put it back here, or on another computer.">
            <Row label="Include logins and passwords" help="Your AniList login and the torrent clients' passwords. Keep a backup with them private.">
                <Switch checked={logins} onChange={setLogins} />
            </Row>
            <Row label="Make a backup" help="Downloads the file.">
                <a href={`/api/backup?logins=${logins ? 1 : 0}`} download>
                    <Button size="sm">Download backup</Button>
                </a>
            </Row>
            <Row label="Restore a backup" help="Merged with what's here; Aniko starts again after.">
                <label>
                    <input type="file" accept=".json,application/json" className="sr-only" disabled={restoring} onChange={e => e.target.files?.[0] && restore(e.target.files[0])} />
                    <span className={cn("inline-flex h-8 cursor-pointer items-center rounded-lg bg-white/[0.07] px-3 text-[13px] font-medium hover:bg-white/[0.12]", restoring && "pointer-events-none opacity-60")}>
                        {restoring ? "Restoring…" : "Choose a backup…"}
                    </span>
                </label>
            </Row>
        </Group>
    )
}

// waitForRestart waits for Aniko to go and answer again (a minute at most).
async function waitForRestart() {
    const answers = () =>
        fetch("/api/status", { cache: "no-store", credentials: "same-origin" })
            .then(r => r.ok)
            .catch(() => false)
    const until = Date.now() + 60_000
    let wentAway = false
    while (Date.now() < until) {
        await new Promise(r => setTimeout(r, 1000))
        const up = await answers()
        if (!up) wentAway = true
        else if (wentAway || Date.now() > until - 50_000) return
    }
}

// Alerts: an episode of what's being watched aired, one is ready in the
// library. A toast, or a notification of the system when Aniko isn't in front
// (the desktop app's, also with its window closed; a browser's once allowed).
function AlertsGroup({ draft, set }: SectionProps) {
    const notify = draft.notify ?? { newEpisodes: true, downloads: true }
    const [permission, setPermission] = useState(systemNotifications)
    const desktopApp = !!desktop()
    return (
        <Group title="Alerts" description="Muted with the bell next to Settings, like Aniko's other messages.">
            <Row label="New episodes" help="When an episode of an anime you're watching comes out.">
                <Switch checked={notify.newEpisodes} onChange={v => set("notify", { ...notify, newEpisodes: v })} />
            </Row>
            <Row label="Episodes ready to watch" help="When a download finishes, or new episodes show up in your library (a torrent that finished).">
                <Switch checked={notify.downloads} onChange={v => set("notify", { ...notify, downloads: v })} />
            </Row>
            {!desktopApp && !androidApp && permission === "default" && (
                <Row label="Notifications of this browser" help="So alerts show while Aniko's tab isn't in front.">
                    <Button size="sm" onClick={() => Notification.requestPermission().then(() => setPermission(systemNotifications()))}>
                        Allow
                    </Button>
                </Row>
            )}
            {!desktopApp && !androidApp && permission === "denied" && <p className="px-4 py-3 text-xs text-subtle">This browser blocks Aniko's notifications: alerts show in Aniko only (allow them in the site's settings).</p>}
        </Group>
    )
}

// How the in-app player shows subtitles: this device's own choice (a TV
// across the room needs them bigger), applied at once.
function SubtitlesGroup() {
    const style = useStore(subStyleStore)
    return (
        <Group title="How subtitles look" description="In the in-app player, on this device. Their timing: z and x while playing, or the player's subtitles menu (kept for each anime).">
            <div className="px-4 py-4">
                <div className="relative flex h-28 items-end justify-center overflow-hidden rounded-lg bg-gradient-to-br from-sky-900 via-slate-700 to-amber-800 pb-3" aria-hidden>
                    <span
                        className="rounded px-1.5 font-semibold text-white"
                        style={{
                            fontSize: `${17 * (SUB_SIZES.find(o => o.value === style.size)?.scale ?? 1)}px`,
                            background: (subStyleVars(style) as Record<string, string>)["--sub-bg"],
                            textShadow: (subStyleVars(style) as Record<string, string>)["--sub-shadow"],
                            marginBottom: style.position === "raised" ? "1.75rem" : 0,
                        }}
                    >
                        This is how subtitles look.
                    </span>
                </div>
            </div>
            <Row label="Size">
                <Select className="w-40" value={style.size} onChange={v => setSubStyle({ size: v as SubStyle["size"] })} options={SUB_SIZES.map(o => ({ value: o.value, label: o.label }))} />
            </Row>
            <Row label="Background">
                <Select className="w-48" value={style.background} onChange={v => setSubStyle({ background: v as SubStyle["background"] })} options={SUB_BACKGROUNDS} />
            </Row>
            <Row label="Position" help="Higher up keeps them clear of a TV's edge or a video's own captions.">
                <Select className="w-40" value={style.position} onChange={v => setSubStyle({ position: v as SubStyle["position"] })} options={SUB_POSITIONS} />
            </Row>
        </Group>
    )
}

// How big everything is on the TV (this TV's own choice, see lib/tv.ts).
function TvSizeRow() {
    const [width, setWidth] = useState(tvWidth)
    return (
        <Row label="Size on the TV" help="Smaller fits more on the screen">
            <Select
                className="w-36"
                value={String(width)}
                onChange={v => {
                    setWidth(Number(v))
                    setTvWidth(Number(v))
                }}
                options={TV_WIDTHS.map(o => ({ value: String(o.value), label: o.label }))}
            />
        </Row>
    )
}

const ACCENTS = ["#7c6cf2", "#5b8cff", "#22c3a6", "#f2557a", "#f59e0b", "#e879f9", "#38bdf8", "#a3e635"]
const ACCENT_NAMES: Record<string, string> = {
    "#7c6cf2": "violet",
    "#5b8cff": "blue",
    "#22c3a6": "teal",
    "#f2557a": "pink",
    "#f59e0b": "amber",
    "#e879f9": "orchid",
    "#38bdf8": "sky",
    "#a3e635": "lime",
}

function UISection({ draft, set }: SectionProps) {
    const seasonal = draft.ui.seasonal ?? "auto"
    // The one on now with these settings (or that would be, for the hint).
    const holiday = activeHoliday(seasonal)
    return (
        <>
            <Group title="Theme">
                <Row label="Classic look" help="Aniko as it looked before 2.0: its sidebar, cards, menus and holiday themes. The colors below work in both.">
                    <Switch checked={!!draft.ui.classicLook} onChange={v => set("ui", { classicLook: v })} />
                </Row>
                <Stack label="Colors">
                    <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
                        {COLOR_THEMES.map(t => {
                            const on = (draft.ui.colorTheme ?? "aniko") === t.id
                            return (
                                <button
                                    key={t.id}
                                    onClick={() => set("ui", { colorTheme: t.id, accentColor: t.accent })}
                                    aria-pressed={on}
                                    className={cn("focus-ring flex flex-col gap-2 rounded-lg border p-2 text-left transition", on ? "border-white/70" : "border-line hover:border-white/25")}
                                    style={{ background: t.palette.frame }}
                                >
                                    {/* The window in small: the sidebar, a page with panels, the accent. */}
                                    <span className="flex h-12 gap-1.5 overflow-hidden rounded-md" aria-hidden>
                                        <span className="flex w-4 flex-col gap-1 pt-1.5">
                                            <span className="h-1 rounded-full" style={{ background: t.accent }} />
                                            <span className="h-1 rounded-full" style={{ background: t.palette.s3 }} />
                                            <span className="h-1 rounded-full" style={{ background: t.palette.s3 }} />
                                        </span>
                                        <span className="flex flex-1 flex-col gap-1 rounded p-1.5" style={{ background: t.palette.bg }}>
                                            <span className="h-2 w-2/3 rounded-sm" style={{ background: t.palette.s3 }} />
                                            <span className="flex flex-1 gap-1">
                                                <span className="flex-1 rounded-sm" style={{ background: t.palette.s1 }} />
                                                <span className="flex-1 rounded-sm" style={{ background: t.palette.s2 }} />
                                                <span className="w-2 rounded-sm" style={{ background: t.accent }} />
                                            </span>
                                        </span>
                                    </span>
                                    <span className="px-0.5 text-[13px] font-medium" style={{ color: "#ececee" }}>
                                        {t.name}
                                    </span>
                                </button>
                            )
                        })}
                    </div>
                </Stack>
                <Stack label="Accent color" help={holiday ? `While the ${holiday.name} theme is on, its color is used instead.` : undefined}>
                    <div className="flex flex-wrap items-center gap-2.5">
                        {ACCENTS.map(c => (
                            <button
                                key={c}
                                onClick={() => set("ui", { accentColor: c })}
                                className={cn("size-9 rounded-full ring-2 ring-offset-2 ring-offset-[var(--surface-1)] transition", draft.ui.accentColor === c ? "ring-white" : "ring-transparent hover:ring-white/30")}
                                style={{ background: c }}
                                aria-label={`Accent ${ACCENT_NAMES[c] ?? c}`}
                                aria-pressed={draft.ui.accentColor === c}
                            />
                        ))}
                        <input
                            type="color"
                            value={draft.ui.accentColor}
                            onChange={e => set("ui", { accentColor: e.target.value })}
                            className="size-9 cursor-pointer rounded-full border border-line bg-transparent"
                            aria-label="Another color"
                        />
                    </div>
                </Stack>
                <Row
                    label="Holiday themes"
                    help={
                        seasonal === "auto"
                            ? `Each in its time: ${HOLIDAYS.map(h => `${h.name} ${h.when.replace(/^In /, "in ")}`).join(", ")}.${holiday ? ` ${holiday.name} is on now.` : ""}`
                            : seasonal === "off"
                              ? "No holiday colors, decorations or animations."
                              : `${holiday?.name ?? "It"}, whatever the date.`
                    }
                >
                    <Select
                        className="w-44"
                        value={seasonal}
                        onChange={v => set("ui", { seasonal: v })}
                        options={[{ value: "auto", label: "By the date" }, { value: "off", label: "Off" }, ...HOLIDAYS.map(h => ({ value: h.id, label: `Always ${h.name}` }))]}
                    />
                </Row>
                <Row label="Holiday animations" help={`${holiday ? holiday.effectName : "Snow, bats, hearts"} over the pages (not over videos). Off with Reduce motion.`}>
                    <Switch checked={draft.ui.seasonalEffects ?? true} disabled={seasonal === "off"} onChange={v => set("ui", { seasonalEffects: v })} />
                </Row>
                <Row label="Card size">
                    <Select
                        className="w-36"
                        value={draft.ui.cardSize}
                        onChange={v => set("ui", { cardSize: v })}
                        options={[
                            { value: "sm", label: "Compact" },
                            { value: "md", label: "Comfortable" },
                            { value: "lg", label: "Large" },
                        ]}
                    />
                </Row>
                <Row label="Reduce motion" help="Turn off animations">
                    <Switch checked={draft.ui.reducedMotion} onChange={v => set("ui", { reducedMotion: v })} />
                </Row>
                {tv && androidApp && <TvSizeRow />}
            </Group>
            <Group title="Content">
                <Row label="Anime titles" help="English where AniList has an English title, or Japanese (romaji, in Latin letters)">
                    <Select
                        className="w-36"
                        value={draft.ui.titleLanguage === "romaji" ? "romaji" : "english"}
                        onChange={v => set("ui", { titleLanguage: v as "english" | "romaji" })}
                        options={[
                            { value: "english", label: "English" },
                            { value: "romaji", label: "Japanese" },
                        ]}
                    />
                </Row>
                <Row label="Hide spoilers" help="Blur thumbnails of episodes you haven't watched yet">
                    <Switch checked={draft.ui.blurUnwatched} onChange={v => set("ui", { blurUnwatched: v })} />
                </Row>
                <Row label="Show adult content" help="Include 18+ entries in search and discover">
                    <Switch checked={draft.ui.showAdult} onChange={v => set("ui", { showAdult: v })} />
                </Row>
            </Group>
        </>
    )
}

// Library sharing with the Aniko apps on the other computers at home. Turning
// it on or off and the name are saved with the rest; who gets this
// computer's library changes at once.
function SharingGroup({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const saved = status?.settings.sharing
    const trusted = status?.client !== "lan"
    const { data: sharing } = useSharing(!!saved?.enabled)
    const qc = useQueryClient()
    const [address, setAddress] = useState("")
    const [adding, setAdding] = useState(false)
    // The computer files are being uploaded to (UploadDialog), or none.
    const [uploadTo, setUploadTo] = useState<string | null>(null)
    const call = async (fn: () => Promise<unknown>) => {
        try {
            await fn()
            qc.invalidateQueries({ queryKey: ["sharing"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const add = async () => {
        if (!address.trim()) return
        setAdding(true)
        await call(async () => {
            const p = await api.post<SharingPeer>("/api/sharing/connect", { address })
            toast.success(`Found ${p.name}`)
            setAddress("")
        })
        setAdding(false)
    }
    const peers = sharing?.peers ?? []
    return (
        <Group
            title="Library sharing"
            description="Watch the libraries of Aniko on your other computers at home, and share yours with them. Each computer decides who gets its library. Watching updates the AniList account (or local list) of the computer you watch on, never the other's. On the same AniList account, Continue watching carries over to the second: to the computer whose files you watch as you watch, and back."
        >
            <ParentalHere />
            <Row label="Library sharing" help="Find the Aniko apps on this network, and let them find this one. Turn it on on both computers.">
                <Switch checked={draft.sharing.enabled} disabled={!trusted} onChange={v => set("sharing", { enabled: v })} />
            </Row>
            {draft.sharing.enabled && (
                <Stack label="Name on the network" help="What the other Aniko apps call this one.">
                    <Input value={draft.sharing.name} disabled={!trusted} placeholder={sharing?.name || "This computer's name"} onChange={e => set("sharing", { name: e.target.value })} />
                </Stack>
            )}
            {draft.sharing.enabled && (
                <Row label="Sync watch history by itself" help="When a device this one is paired with comes back on the network, or Aniko's TV app is opened again, Continue watching and the positions are brought up to date. Sync on Home still does it at once.">
                    <Switch checked={draft.sharing.autoSync ?? true} disabled={!trusted} onChange={v => set("sharing", { autoSync: v })} />
                </Row>
            )}
            {draft.sharing.enabled && peers.some(p => p.role === "hub") && (
                <Row label="Same theme on all my devices" help="Through the Aniko Hub: the colors, the look and the player settings stay the same on the devices on your AniList account. Changed on one, they change on the others.">
                    <Switch checked={draft.sharing.syncPrefs ?? true} disabled={!trusted} onChange={v => set("sharing", { syncPrefs: v })} />
                </Row>
            )}
            {draft.sharing.enabled !== !!saved?.enabled && <p className="px-4 py-3 text-xs text-amber-200/80">Save to turn library sharing {draft.sharing.enabled ? "on" : "off"}.</p>}
            {saved?.enabled && sharing && (
                <>
                    <div className="flex items-start justify-between gap-3 px-4 pt-3.5 pb-1">
                        <div>
                        <p className="text-sm font-medium">Aniko apps on this network</p>
                        <p className="mt-0.5 text-xs text-subtle">
                            Turn on <b>Share my library</b> for those that may watch this computer's anime.
                            {sharing.addresses.length > 0 && <> This computer is at {sharing.addresses.join(", ")}.</>}
                        </p>
                        </div>
                        <Button size="sm" variant="ghost" icon={<RefreshCw className="size-4" />} onClick={() => refreshSharing(qc)}>
                            Refresh
                        </Button>
                    </div>
                    {peers.length === 0 && (
                        <p className="px-4 py-3 text-sm text-muted">
                            {sharing.listening ? "Looking for Aniko apps… Turn on library sharing on the other computer too." : "This computer can't listen for the others (another program uses the network port): add them by address below."}
                        </p>
                    )}
                    {peers.map(p => (
                        <div key={p.id} className="px-4 py-3.5">
                        <div className="flex items-center justify-between gap-4">
                            <div className="min-w-0">
                                <p className="flex items-center gap-2 text-sm font-medium">
                                    <span className={cn("size-2 shrink-0 rounded-full", p.online ? "bg-emerald-400" : "bg-white/20")} />
                                    <span className="truncate">{p.name}</span>
                                    {p.role === "hub" && <Badge tone="brand">Aniko Hub</Badge>}
                                </p>
                                <p className="mt-0.5 text-xs text-subtle">
                                    {[
                                        p.address,
                                        p.version && `Aniko ${p.version}${p.platform === "android" ? " for Android" : ""}`,
                                        !p.online && p.lastSeen > 0 && `seen ${relativeTime(p.lastSeen)}`,
                                        p.shares && `shares its library with you${p.files ? ` (${p.files} ${p.files === 1 ? "file" : "files"})` : ""}`,
                                        p.error,
                                        p.noBeacons && "found by its address only: its announcements aren't getting through (a firewall, or a network set to Public on it)",
                                    ]
                                        .filter(Boolean)
                                        .join(" · ")}
                                </p>
                                <VersionNote peer={p} mine={status?.version} />
                            </div>
                            <div className="flex shrink-0 items-center gap-3">
                                {p.shares && p.takesDownloads && trusted && !tv && (
                                    <Button size="sm" variant="ghost" icon={<Upload className="size-4" />} onClick={() => setUploadTo(p.id)}>
                                        Upload
                                    </Button>
                                )}
                                {!p.online && p.canWake && (
                                    <Button size="sm" variant="ghost" icon={<Moon className="size-4" />} loading={p.waking} onClick={() => call(() => api.post(`/api/sharing/libraries/${p.id}/wake`))}>
                                        {p.waking ? "Waking…" : "Wake"}
                                    </Button>
                                )}
                                {p.role !== "hub" && (
                                    <>
                                        <span className="text-xs text-muted">Share my library</span>
                                        <Switch checked={p.allowed} disabled={!trusted} onChange={v => call(() => api.post(`/api/sharing/peers/${p.id}`, { allowed: v }))} />
                                    </>
                                )}
                                {!p.online && trusted && (
                                    <button
                                        onClick={() => confirm(`Remove ${p.name} from the list? It shows up again when it's back on the network.`) && call(() => api.del(`/api/sharing/peers/${p.id}`))}
                                        className="grid size-8 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-rose-300"
                                        aria-label={`Remove ${p.name}`}
                                        title="Remove"
                                    >
                                        <X className="size-4" />
                                    </button>
                                )}
                            </div>
                        </div>
                        {p.role === "hub" && <HubPeer hub={p} trusted={trusted} loggedIn={!!status?.loggedIn} />}
                        {/* With the library: this computer's AniList account,
                            and downloading here (a TV has neither). */}
                        {p.allowed && p.role !== "hub" && (
                            <div className="mt-3 ml-1 flex flex-col gap-3 border-l border-line pl-4">
                                <PeerGrant
                                    label="Share my AniList account"
                                    help="It uses your AniList account: your list, and what it watches updates your progress."
                                    checked={p.account}
                                    disabled={!trusted}
                                    onChange={v => call(() => api.post(`/api/sharing/peers/${p.id}`, { account: v }))}
                                />
                                <PeerGrant
                                    label="Let it download here"
                                    help="Its downloads and torrents go to this computer's library, and come back shared, and it can upload files and folders into it. It downloads here by default from then on."
                                    checked={p.downloads}
                                    disabled={!trusted}
                                    onChange={v => call(() => api.post(`/api/sharing/peers/${p.id}`, { downloads: v }))}
                                />
                                {trusted && <ParentalRow peer={p} />}
                            </div>
                        )}
                        {p.shares && p.sharesAccount && (
                            <div className="mt-3 ml-1 flex items-center justify-between gap-4 border-l border-line pl-4">
                                <div className="min-w-0">
                                    <p className="text-sm">
                                        {p.usingAccount
                                            ? `Using ${p.role === "hub" ? "the AniList login" : `${p.name}'s AniList account`} ${p.role === "hub" ? `${p.name} gives this device` : ""}`.trim()
                                            : p.role === "hub"
                                              ? `${p.name} gives this device an AniList login`
                                              : `${p.name} shares its AniList account with you`}
                                    </p>
                                    <p className="mt-0.5 text-xs text-subtle">
                                        {p.usingAccount ? "Your list and progress here are that account's." : "Use it here instead of this device's own login."}
                                    </p>
                                </div>
                                {trusted && (
                                    <Button size="sm" variant={p.usingAccount ? "ghost" : "primary"} onClick={() => call(() => api.post(`/api/sharing/libraries/${p.id}/account`, { use: !p.usingAccount }))}>
                                        {p.usingAccount ? "Stop using it" : "Use it"}
                                    </Button>
                                )}
                            </div>
                        )}
                        </div>
                    ))}
                    {peers.some(p => p.shares && p.takesDownloads) && (
                        <Row label="Downloads from this device go to" help="Episodes and torrents you download here: to this device, or to a computer that lets it download there.">
                            <Select
                                className="w-48"
                                value={draft.sharing.downloadTo ?? ""}
                                disabled={!trusted}
                                onChange={v => set("sharing", { downloadTo: v })}
                                options={[{ value: "", label: "This device" }, ...peers.filter(p => p.shares && p.takesDownloads).map(p => ({ value: p.id, label: p.name }))]}
                            />
                        </Row>
                    )}
                    {trusted && (
                        <Stack label="Add by address" help="When the computers don't find each other by themselves (some routers block it): the other computer's address, shown in its settings here.">
                            <div className="flex gap-2">
                                <Input value={address} onChange={e => setAddress(e.target.value)} onKeyDown={e => e.key === "Enter" && add()} placeholder="192.168.1.20:43211" />
                                <Button onClick={add} loading={adding} disabled={!address.trim()}>
                                    Add
                                </Button>
                            </div>
                        </Stack>
                    )}
                </>
            )}
            {uploadTo !== null && <UploadDialog open onOpenChange={v => !v && setUploadTo(null)} target={uploadTo} />}
        </Group>
    )
}

// A device's parental controls, set here.
function ParentalRow({ peer }: { peer: SharingPeer }) {
    const [open, setOpen] = useState(false)
    return (
        <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
                <p className="flex items-center gap-2 text-sm">
                    Parental controls
                    {peer.parental && <Badge tone="green">On</Badge>}
                </p>
                <p className="mt-0.5 text-xs text-subtle">Block 18+ anime, genres or titles on it, or allow only some; see what it watched.</p>
            </div>
            <Button size="sm" icon={<ShieldCheck className="size-4" />} onClick={() => setOpen(true)}>
                {peer.parental ? "Change" : "Set up"}
            </Button>
            <ParentalDialog device={peer} open={open} onOpenChange={setOpen} />
        </div>
    )
}

// Parental controls on this device, set by a computer or hub that shares
// with it: who set them, and lifting them with their PIN.
function ParentalHere() {
    const qc = useQueryClient()
    const { data } = useParental()
    const [pin, setPin] = useState("")
    const [busy, setBusy] = useState(false)
    if (!data?.active) return null
    const names = data.from.map(f => f.name).join(" and ")
    const withPin = data.from.some(f => f.hasPin)
    const unlock = async () => {
        setBusy(true)
        try {
            await api.post("/api/parental/unlock", { pin })
            toast.success("Parental controls lifted on this device")
            setPin("")
            qc.invalidateQueries()
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <div className="flex flex-col gap-3 border-b border-line bg-emerald-500/[0.06] px-4 py-3.5">
            <p className="flex items-center gap-2 text-sm font-medium">
                <ShieldCheck className="size-4 text-emerald-300" />
                Parental controls are on, set by {names}
            </p>
            <p className="text-xs text-subtle">
                {[data.rules?.noAdult && "18+ anime are blocked", data.rules?.genres?.length && `blocked genres: ${data.rules.genres.join(", ")}`, data.rules?.onlyAllowed && "only some anime are allowed", data.rules?.blocked?.length && `${data.rules.blocked.length} anime blocked`]
                    .filter(Boolean)
                    .join(" · ")}
                . They're changed on {names}. Library sharing stays on while they are.
            </p>
            {withPin && (
                <div className="flex gap-2">
                    <Input type="password" inputMode="numeric" placeholder="PIN" value={pin} onChange={e => setPin(e.target.value)} onKeyDown={e => e.key === "Enter" && pin && unlock()} className="max-w-40" />
                    <Button size="sm" loading={busy} disabled={!pin} onClick={unlock}>
                        Lift with the PIN
                    </Button>
                </div>
            )}
        </div>
    )
}

// An Aniko Hub around: its owner shares its library with this device (on its
// dashboard), and this device can give it its AniList login, settings and
// watch history, for the account's other devices.
function HubPeer({ hub, trusted, loggedIn }: { hub: SharingPeer; trusted: boolean; loggedIn: boolean }) {
    const [busy, setBusy] = useState<"" | "send" | "account">("")
    const qc = useQueryClient()
    const run = async (what: "send" | "account") => {
        setBusy(what)
        try {
            if (what === "send") {
                const r = await api.post<{ message: string }>(`/api/sharing/hubs/${hub.id}/send`)
                toast.success(r.message)
            } else {
                await api.post(`/api/sharing/hubs/${hub.id}/account`)
                toast.success(`${hub.name} has your AniList login`)
            }
            qc.invalidateQueries({ queryKey: ["sharing"] })
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setBusy("")
        }
    }
    if (!hub.shares)
        return (
            <p className="mt-3 ml-1 border-l border-line pl-4 text-xs leading-relaxed text-subtle">
                {hub.online ? (
                    <>
                        Waiting for the hub to share with this device: on its dashboard (<span className="text-muted">http://{hub.address}</span>), turn on Devices › this device › Share the library.
                    </>
                ) : (
                    "The hub is off or away."
                )}
            </p>
        )
    if (!trusted || !loggedIn)
        return (
            <p className="mt-3 ml-1 border-l border-line pl-4 text-xs leading-relaxed text-subtle">
                It keeps this device in step with the others on the same AniList account.
            </p>
        )
    return (
        <div className="mt-3 ml-1 flex flex-col gap-3 border-l border-line pl-4">
            <div className="flex items-center justify-between gap-4 max-sm:flex-wrap">
                <div className="min-w-0">
                    <p className="text-sm">Send it everything from this device</p>
                    <p className="mt-0.5 text-xs text-subtle">Your AniList login, your theme and player settings, and every position you've watched: your other devices start where this one is.</p>
                </div>
                <Button size="sm" variant="primary" loading={busy === "send"} disabled={!!busy && busy !== "send"} onClick={() => run("send")}>
                    Send
                </Button>
            </div>
            <div className="flex items-center justify-between gap-4 max-sm:flex-wrap">
                <div className="min-w-0">
                    <p className="text-sm">Give it my AniList login</p>
                    <p className="mt-0.5 text-xs text-subtle">The hub can then give your account to devices that aren't logged in (a TV), when you choose so on its dashboard.</p>
                </div>
                <Button size="sm" loading={busy === "account"} disabled={!!busy && busy !== "account"} onClick={() => run("account")}>
                    Give
                </Button>
            </div>
        </div>
    )
}

// The other Aniko runs another version than this one: some things (Sync,
// waking it up) need both up to date.
function VersionNote({ peer, mine }: { peer: SharingPeer; mine?: string }) {
    const c = compareVersions(peer.version, mine)
    if (!c) return null
    return (
        <p className="mt-1 text-xs text-amber-200/80">
            {c < 0
                ? `Older than this one (Aniko ${mine}): update Aniko on ${peer.name} so everything works together. Its Home page has the update button.`
                : `Newer than this one (Aniko ${mine}): update Aniko here so everything works together (Settings › Updates & about).`}
        </p>
    )
}

function DevicesSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const trusted = status?.client !== "lan"
    return (
        <>
            <SharingGroup draft={draft} set={set} />
            {/* A TV doesn't serve the others (and has nothing to keep awake). */}
            {!androidApp && (
                <Group title="While other devices watch">
                    <Row
                        label="Keep this computer awake"
                        help={
                            status?.keptAwake
                                ? "Staying awake now: another device is playing from it. It can sleep again 15 minutes after the last episode."
                                : "While a TV, phone or another computer plays an episode from this one, it doesn't go to sleep (the screen can still turn off). It can sleep again 15 minutes after."
                        }
                    >
                        <Switch checked={draft.sharing.keepAwake ?? true} disabled={!trusted} onChange={v => set("sharing", { keepAwake: v })} />
                    </Row>
                </Group>
            )}
        </>
    )
}

function PeerGrant({ label, help, checked, disabled, onChange }: { label: string; help: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
    return (
        <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
                <p className="text-sm">{label}</p>
                <p className="mt-0.5 text-xs text-subtle">{help}</p>
            </div>
            <Switch checked={checked} disabled={disabled} onChange={onChange} />
        </div>
    )
}

function LibrarySection({ draft, set }: SectionProps) {
    const [, setSearchParams] = useSearchParams()
    const extra = draft.library.extraDirs ?? []
    const folders = [draft.library.dir, ...extra].filter(Boolean)
    const off = !!draft.library.off
    return (
        <>
            <Group title="Local library">
                <Locked>
                    <Row
                        label="A library on this computer"
                        help={
                            off
                                ? "Off: this computer plays the libraries your Aniko Hub and other computers share with it (Settings › Other devices). Its folders are kept for when you turn it back on."
                                : "Turn it off to use only your Aniko Hub's library (or another computer's). Aniko forgets this computer's files; nothing is deleted from the disk."
                        }
                    >
                        <Switch label="A library on this computer" checked={!off} onChange={v => set("library", { off: !v })} />
                    </Row>
                </Locked>
            </Group>
            {!off && (
                <>
                    <Group title="Folders">
                        <Locked>
                            <Stack label="Library directory" help="Path of the directory where your media files are located.">
                                <PathInput value={draft.library.dir} onChange={v => set("library", { dir: v })} placeholder="/mnt/big/Media/Anime" />
                            </Stack>
                            <Stack label="Additional library directories" help="Include additional directory paths if your library is spread across multiple locations.">
                                <div className="flex flex-col gap-2">
                                    {extra.map((d, i) => (
                                        <div key={i} className="flex items-center gap-2">
                                            <div className="flex-1">
                                                <PathInput value={d} onChange={v => set("library", { extraDirs: extra.map((x, j) => (j === i ? v : x)) })} />
                                            </div>
                                            <button onClick={() => set("library", { extraDirs: extra.filter((_, j) => j !== i) })} aria-label="Remove this folder" className="grid size-10 place-items-center rounded-xl text-muted hover:bg-white/[0.06] hover:text-rose-300">
                                                <X className="size-4" />
                                            </button>
                                        </div>
                                    ))}
                                    <button onClick={() => set("library", { extraDirs: [...extra, ""] })} className="grid size-9 place-items-center rounded-full bg-white/[0.07] text-fg hover:bg-white/[0.12]" aria-label="Add directory">
                                        <Plus className="size-4" />
                                    </button>
                                </div>
                            </Stack>
                            {folders.length > 1 && (
                                <Stack label="Downloads go to" help="The folder new anime are downloaded into (streams and torrents). An anime that already has a folder keeps it.">
                                    <Select
                                        value={folders.includes(draft.library.downloadDir ?? "") ? draft.library.downloadDir! : ""}
                                        onChange={v => set("library", { downloadDir: v })}
                                        options={[{ value: "", label: `${folders[0]} (the first folder)` }, ...folders.slice(1).map(f => ({ value: f, label: f }))]}
                                    />
                                </Stack>
                            )}
                        </Locked>
                    </Group>
                    <Group title="Scanning">
                        <Row label="Automatically refresh library" help="Watch the folders and pick up new or removed files on their own">
                            <Switch checked={draft.library.autoRefresh} onChange={v => set("library", { autoRefresh: v })} />
                        </Row>
                        <Row label="Refresh library on startup">
                            <Switch checked={draft.library.refreshOnStartup} onChange={v => set("library", { refreshOnStartup: v })} />
                        </Row>
                    </Group>
                </>
            )}
            <Group title="Library sharing">
                <Row label="Share this library with your other computers and TVs" help="Who gets it, and the libraries shared with this computer, are in Devices.">
                    <Button size="sm" variant="ghost" onClick={() => setSearchParams({ tab: "devices" })}>
                        Open
                    </Button>
                </Row>
            </Group>
            <Collapsible title="Advanced">
                <Stack label={`Matching threshold — ${Math.round(draft.library.matchThreshold * 100)}%`} help="How similar a file name must be to an AniList title to match automatically.">
                    <input type="range" min={0.3} max={0.95} step={0.05} value={draft.library.matchThreshold} onChange={e => set("library", { matchThreshold: Number(e.target.value) })} className="accent-[var(--brand)]" />
                </Stack>
                <Row label="Match anime that aren't in my list" help="Search all of AniList when a title isn't on your list">
                    <Switch checked={draft.library.matchOutsideList} onChange={v => set("library", { matchOutsideList: v })} />
                </Row>
                <Stack label="Ignore patterns" help="One glob per line, matched against file and folder names (e.g. *sample*).">
                    <LinesInput value={draft.library.ignorePatterns ?? []} onChange={v => set("library", { ignorePatterns: v })} />
                </Stack>
            </Collapsible>
        </>
    )
}

const LANGS = [
    { value: "jpn,ja", label: "Japanese" },
    { value: "eng,en", label: "English" },
    { value: "spa,es", label: "Spanish" },
    { value: "por,pt", label: "Portuguese" },
    { value: "fre,fra,fr", label: "French" },
    { value: "ger,deu,de", label: "German" },
    { value: "ita,it", label: "Italian" },
    { value: "ara,ar", label: "Arabic" },
    { value: "rus,ru", label: "Russian" },
    { value: "", label: "File default" },
]

function PlaybackSection({ draft, set }: SectionProps) {
    const p = draft.playback
    return (
        <>
            <Group title="Player">
                {!androidApp && (
                    <Row label="Default player for local files" help="mpv opens in its own window; the in-app player works everywhere (and on other devices).">
                        <Select
                            className="w-48"
                            value={p.defaultPlayer}
                            onChange={v => set("playback", { defaultPlayer: v as "mpv" })}
                            options={[
                                { value: "builtin", label: "In-app player" },
                                { value: "mpv", label: "mpv" },
                            ]}
                        />
                    </Row>
                )}
                <Row label="Resume where I left off" help="Episodes continue from the saved position in mpv and the in-app player">
                    <Switch checked={p.resumePlayback} onChange={v => set("playback", { resumePlayback: v })} />
                </Row>
                <Row label="Auto play next episode">
                    <Switch checked={p.autoPlayNext} onChange={v => set("playback", { autoPlayNext: v })} />
                </Row>
                <Row label="Skip openings automatically" help="Uses AniSkip timestamps (the same source as ani-cli --skip)">
                    <Switch checked={p.skipIntroAniSkip} onChange={v => set("playback", { skipIntroAniSkip: v })} />
                </Row>
                <Row label="Skip endings automatically" help="Jumps past the ending song, also with AniSkip timestamps">
                    <Switch checked={p.skipOutroAniSkip} onChange={v => set("playback", { skipOutroAniSkip: v })} />
                </Row>
                <Row label="Skip recaps and previews automatically" help="The story so far at the start of an episode, and the next one's preview after the ending, where AniSkip has them">
                    <Switch checked={!!p.skipRecapAniSkip} onChange={v => set("playback", { skipRecapAniSkip: v })} />
                </Row>
            </Group>
            <Group title="Progress">
                <Row label="Update progress automatically" help="Mark the episode as watched on AniList after you finish it">
                    <Switch checked={p.autoUpdateProgress} onChange={v => set("playback", { autoUpdateProgress: v })} />
                </Row>
                <Stack label={`Count as watched at ${Math.round(p.completionThreshold * 100)}%`}>
                    <input type="range" min={0.5} max={1} step={0.01} value={p.completionThreshold} onChange={e => set("playback", { completionThreshold: Number(e.target.value) })} className="accent-[var(--brand)]" />
                </Stack>
            </Group>
            <Group title="Audio & subtitles" description="Aniko remembers the audio and subtitle track you pick for each anime and uses it for the next episodes.">
                <Row label="Remember my track choice per anime">
                    <Switch checked={p.rememberTracks} onChange={v => set("playback", { rememberTracks: v })} />
                </Row>
                <Row label="Preferred audio language">
                    <Select className="w-44" value={p.preferredAudioLang} onChange={v => set("playback", { preferredAudioLang: v })} options={LANGS} />
                </Row>
                <Row label="Preferred subtitle language">
                    <Select className="w-44" value={p.preferredSubLang} onChange={v => set("playback", { preferredSubLang: v })} options={LANGS} />
                </Row>
            </Group>
            <SubtitlesGroup />
        </>
    )
}

function MpvSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    return (
        <Group title="mpv">
            <Row label="Status">
                <Detect ok={status?.features.mpv} label="mpv" />
            </Row>
            <Locked>
                <Stack
                    label="Executable"
                    help={`Name or full path, e.g. mpv or ${exampleMpvPath(status?.platform)} (install with: ${installHint(status?.platform, "mpv")})`}
                >
                    <Input value={draft.mpv.path} onChange={e => set("mpv", { path: e.target.value })} icon={<Terminal className="size-4" />} />
                </Stack>
                <Stack label="Extra arguments" help="Passed to every mpv launch, e.g. --profile=gpu-hq --hwdec=auto">
                    <Input value={draft.mpv.extraArgs} onChange={e => set("mpv", { extraArgs: e.target.value })} />
                </Stack>
            </Locked>
            <Row label="Start in fullscreen">
                <Switch checked={draft.mpv.fullscreen} onChange={v => set("mpv", { fullscreen: v })} />
            </Row>
        </Group>
    )
}

function TranscodeSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const t = draft.transcode
    return (
        <>
            <Group title="In-app player" description="Browsers can't decode every format (HEVC, AC3/DTS audio, MKV with multiple tracks…). Aniko repackages or converts those with ffmpeg on the fly so every file plays.">
                <Row label="Status">
                    <div className="flex gap-2">
                        <Detect ok={status?.features.ffmpeg} label="ffmpeg" />
                        <Detect ok={status?.features.ffprobe} label="ffprobe" />
                    </div>
                </Row>
                <Row label="Mode">
                    <Select
                        className="w-56"
                        value={t.mode}
                        onChange={v => set("transcode", { mode: v })}
                        options={[
                            { value: "auto", label: "Automatic (recommended)" },
                            { value: "direct", label: "Always direct play" },
                            { value: "transcode", label: "Always transcode" },
                        ]}
                    />
                </Row>
                <Row label="Hardware acceleration">
                    <Select
                        className="w-56"
                        value={t.hwAccel}
                        onChange={v => set("transcode", { hwAccel: v })}
                        options={[
                            { value: "", label: "None (CPU, libx264)" },
                            { value: "vaapi", label: "VA-API (AMD / Intel)" },
                            { value: "nvenc", label: "NVENC (NVIDIA)" },
                            { value: "qsv", label: "Quick Sync (Intel)" },
                            { value: "v4l2m2m", label: "V4L2 (Raspberry Pi)" },
                        ]}
                    />
                </Row>
                {t.hwAccel === "vaapi" && (
                    <Locked>
                        <Stack label="VA-API device">
                            <Input value={t.vaapiNode} onChange={e => set("transcode", { vaapiNode: e.target.value })} />
                        </Stack>
                    </Locked>
                )}
                <Row label="x264 preset" help="Faster presets use less CPU">
                    <Select className="w-40" value={t.preset} onChange={v => set("transcode", { preset: v })} options={["ultrafast", "superfast", "veryfast", "faster", "fast", "medium"].map(p => ({ value: p, label: p }))} />
                </Row>
            </Group>
            <Group title="Paths">
                <Locked>
                    <Stack label="ffmpeg">
                        <Input value={t.ffmpegPath} onChange={e => set("transcode", { ffmpegPath: e.target.value })} />
                    </Stack>
                    <Stack label="ffprobe">
                        <Input value={t.ffprobePath} onChange={e => set("transcode", { ffprobePath: e.target.value })} />
                    </Stack>
                </Locked>
            </Group>
        </>
    )
}

function TorrentProviderSection({ draft, set }: SectionProps) {
    const { data: providers } = useQuery({ queryKey: ["torrent-providers"], queryFn: () => api.get<{ id: string; name: string; extension: boolean }[]>("/api/torrents/providers") })
    return (
        <Group title="Torrent search">
            <Row label="Default torrent provider" help="Install more from Extensions › Marketplace">
                <Select className="w-56" value={draft.torrent.defaultProvider} onChange={v => set("torrent", { defaultProvider: v })} options={[{ value: "all", label: "All providers" }, ...(providers ?? []).map(p => ({ value: p.id, label: p.name + (p.extension ? " (extension)" : "") }))]} />
            </Row>
            <Row label="Preferred resolution">
                <Select
                    className="w-40"
                    value={draft.torrent.preferredResolution}
                    onChange={v => set("torrent", { preferredResolution: v })}
                    options={[
                        { value: "", label: "Any" },
                        { value: "2160", label: "2160p" },
                        { value: "1080", label: "1080p" },
                        { value: "720", label: "720p" },
                        { value: "480", label: "480p" },
                    ]}
                />
            </Row>
        </Group>
    )
}

function ClientFields({ value, onChange, showTags }: { value: Settings["qbittorrent"]; onChange: (v: Partial<Settings["qbittorrent"]>) => void; showTags?: boolean }) {
    return (
        <div className="flex flex-col gap-4 p-4">
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Host</span>
                <Input value={value.host} onChange={e => onChange({ host: e.target.value })} />
            </label>
            <div className="grid gap-4 md:grid-cols-3">
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Username</span>
                    <Input value={value.username} onChange={e => onChange({ username: e.target.value })} />
                </label>
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Password</span>
                    <Password value={value.password} onChange={v => onChange({ password: v })} />
                </label>
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Port</span>
                    <Input type="number" value={value.port} onChange={e => onChange({ port: Number(e.target.value) })} />
                </label>
            </div>
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Executable</span>
                <Input value={value.executable} onChange={e => onChange({ executable: e.target.value })} placeholder="/usr/bin/qbittorrent" />
            </label>
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Tags</span>
                <Input value={value.tags} onChange={e => onChange({ tags: e.target.value })} />
                <span className="text-xs text-subtle">Comma separated tags to apply to downloaded torrents. e.g. aniko,anime</span>
            </label>
            {showTags && (
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Category</span>
                    <Input value={value.category} onChange={e => onChange({ category: e.target.value })} />
                    <span className="text-xs text-subtle">Category to apply to downloaded torrents.</span>
                </label>
            )}
            <label className="flex items-center gap-3 text-sm">
                <Switch checked={value.useHttps} onChange={v => onChange({ useHttps: v })} /> Use HTTPS
            </label>
        </div>
    )
}

function TorrentClientSection({ draft, set }: SectionProps) {
    const [open, setOpen] = useState<string>(draft.torrent.defaultClient === "transmission" ? "transmission" : "qbittorrent")
    const [testing, setTesting] = useState(false)
    const save = useSaveSettings()
    const test = async () => {
        setTesting(true)
        try {
            await save.mutateAsync(draft)
            const s = await api.get<{ connected: boolean; version: string; error?: string }>("/api/torrent-client/status")
            s.connected ? toast.success(`Connected — version ${s.version}`) : toast.error(s.error ?? "Not reachable")
        } finally {
            setTesting(false)
        }
    }
    return (
        <>
            <Group title="Torrent client" description="The program that downloads torrents. Aniko talks to its web interface.">
                <Row label="Use" help="Where Aniko sends torrents." keywords="default client qbittorrent transmission">
                    <Select
                        className="w-44"
                        value={draft.torrent.defaultClient}
                        onChange={v => set("torrent", { defaultClient: v })}
                        options={[
                            { value: "qbittorrent", label: "qBittorrent" },
                            { value: "transmission", label: "Transmission" },
                            { value: "none", label: "None" },
                        ]}
                    />
                </Row>
                {(["qbittorrent", "transmission"] as const).map(c => (
                    <Shown key={c} text={`${c === "qbittorrent" ? "qBittorrent" : "Transmission"} host port username password address connection`}>
                        <button onClick={() => setOpen(o => (o === c ? "" : c))} aria-expanded={open === c} className="flex w-full items-center justify-between px-4 py-3.5">
                            <span className="flex items-center gap-2.5 text-sm font-medium">
                                <HardDrive className="size-4 text-muted" />
                                {c === "qbittorrent" ? "qBittorrent" : "Transmission"} connection
                                {draft.torrent.defaultClient === c && <Badge tone="brand">In use</Badge>}
                            </span>
                            <ChevronDown className={cn("size-4 text-muted transition-transform duration-200", open === c && "rotate-180")} />
                        </button>
                        <Locked>
                            {open === c && (
                                <div className="divide-y divide-line border-t border-line">
                                    <ClientFields value={draft[c]} onChange={v => set(c, v)} showTags={c === "qbittorrent"} />
                                </div>
                            )}
                        </Locked>
                    </Shown>
                ))}
                <Row label="Check the connection" help="Saves your changes, then asks the client.">
                    <Button size="sm" icon={<Network className="size-4" />} loading={testing} onClick={test} disabled={draft.torrent.defaultClient === "none"}>
                        Save & test
                    </Button>
                </Row>
            </Group>
            <Group title="Integration">
                <Row label="Show active torrent count" help="Show the number of active torrents in the sidebar.">
                    <Switch checked={draft.torrent.showActiveCount} onChange={v => set("torrent", { showActiveCount: v })} />
                </Row>
                <Row label="Create a folder per anime" help="Save each anime into its own folder inside the library">
                    <Switch checked={draft.torrent.createSubfolder} onChange={v => set("torrent", { createSubfolder: v })} />
                </Row>
                <Row label="Auto downloader" help="Check RSS rules periodically">
                    <Switch checked={draft.torrent.autoDownloader} onChange={v => set("torrent", { autoDownloader: v })} />
                </Row>
                <Row label="Check every (minutes)">
                    <Input type="number" min={5} className="w-24" value={draft.torrent.autoDownloadMinutes} onChange={e => set("torrent", { autoDownloadMinutes: Number(e.target.value) })} />
                </Row>
            </Group>
        </>
    )
}

function StreamingSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const { data: providers } = useOnlineProviders()
    const { data: ani } = useQuery({ queryKey: ["anicli-status"], queryFn: () => api.get<{ installed: boolean; path: string; version: string; ytDlp: boolean; ffmpeg: boolean }>("/api/anicli/status") })
    const a = draft.aniCli
    return (
        <>
            <Group title="Online streaming">
                <Row label="Enable online streaming">
                    <Switch checked={draft.onlineStream.enabled} onChange={v => set("onlineStream", { enabled: v })} />
                </Row>
                <Row label="Default provider" help="ani-cli is built in; extensions come from the marketplace">
                    <Select className="w-56" value={draft.onlineStream.defaultProvider} onChange={v => set("onlineStream", { defaultProvider: v })} options={(providers ?? [{ id: "ani-cli", name: "ani-cli", builtin: true } as any]).map(p => ({ value: p.id, label: p.builtin ? "ani-cli" : p.name }))} />
                </Row>
                <Row label="Play streams in" help="Other devices on your network always use the in-app player">
                    <Select
                        className="w-44"
                        value={a.player}
                        onChange={v => set("aniCli", { player: v as "mpv" })}
                        options={[
                            { value: "builtin", label: "In-app player" },
                            { value: "mpv", label: "mpv" },
                        ]}
                    />
                </Row>
            </Group>
            <Group title="ani-cli" description="Aniko drives your installed ani-cli to find sub and dub streams and downloads, so updating ani-cli keeps everything working.">
                <Row label="Status">
                    <div className="flex flex-wrap justify-end gap-2">
                        {ani?.installed ? <Badge tone="green">ani-cli {ani.version}</Badge> : <Badge tone="red">ani-cli not found</Badge>}
                        <Badge tone={ani?.ytDlp ? "green" : "gray"}>yt-dlp {ani?.ytDlp ? "✓" : "✗"}</Badge>
                        <Badge tone={ani?.ffmpeg ? "green" : "gray"}>ffmpeg {ani?.ffmpeg ? "✓" : "✗"}</Badge>
                    </div>
                </Row>
                {!ani?.installed && !canInstallPrograms(status) && (
                    <p className="px-4 py-3 text-sm text-muted">
                        {installSource(status?.platform, "ani-cli")}: <code className="rounded bg-black/30 px-1.5 py-0.5">{installHint(status?.platform, "ani-cli")}</code>
                    </p>
                )}
                {!ani?.installed && canInstallPrograms(status) && (
                    <Row
                        label="Install ani-cli"
                        help={
                            status?.platform === "darwin"
                                ? "With the other programs Aniko uses, downloaded into Aniko's folder: Settings › Desktop app & programs."
                                : "With Git, whose bash runs it, and the other programs Aniko uses: Settings › Desktop app & programs."
                        }
                    >
                        <InstallProgramsButton />
                    </Row>
                )}
                <Locked>
                    <Stack label="Executable">
                        <Input value={a.path} onChange={e => set("aniCli", { path: e.target.value })} icon={<Terminal className="size-4" />} />
                    </Stack>
                </Locked>
                <Row label="Default audio">
                    <div className="flex rounded-lg bg-white/[0.04] p-0.5">
                        {(["sub", "dub"] as const).map(m => (
                            <button key={m} onClick={() => set("aniCli", { defaultMode: m })} className={cn("h-7 rounded-md px-3 text-xs font-medium uppercase transition-colors", a.defaultMode === m ? "bg-white/[0.1] text-fg" : "text-muted hover:text-fg")}>
                                {m}
                            </button>
                        ))}
                    </div>
                </Row>
                <Row label="Quality">
                    <Select
                        className="w-40"
                        value={a.quality}
                        onChange={v => set("aniCli", { quality: v })}
                        options={[
                            { value: "best", label: "Best" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                            { value: "worst", label: "Worst" },
                        ]}
                    />
                </Row>
                <Row label="Downloader">
                    <Select
                        className="w-40"
                        value={a.downloader}
                        onChange={v => set("aniCli", { downloader: v })}
                        options={[
                            { value: "auto", label: "Automatic" },
                            { value: "yt-dlp", label: "yt-dlp" },
                            { value: "ffmpeg", label: "ffmpeg" },
                        ]}
                    />
                </Row>
                <Locked>
                    <Stack label="Download folder" help={`Empty = your library folder (${status?.settings.library.dir || "not set"}). Downloads are matched automatically.`}>
                        <PathInput value={a.downloadDir} onChange={v => set("aniCli", { downloadDir: v })} placeholder="Library folder" />
                    </Stack>
                </Locked>
            </Group>
        </>
    )
}

function MangaSection({ draft, set }: SectionProps) {
    const { data } = useQuery({ queryKey: ["manga-providers"], queryFn: () => api.get<{ id: string; name: string }[]>("/api/manga/providers") })
    return (
        <Group title="Manga">
            <Row label="Enable manga">
                <Switch checked={draft.manga.enabled} onChange={v => set("manga", { enabled: v })} />
            </Row>
            <Row label="Default provider" help="Install manga providers from the marketplace">
                <Select className="w-56" value={draft.manga.defaultProvider} onChange={v => set("manga", { defaultProvider: v })} options={[{ value: "", label: "First installed" }, ...(data ?? []).map(p => ({ value: p.id, label: p.name }))]} />
            </Row>
            <Row label="Download folder" help="Where downloaded chapters go on this device, one folder per manga with its details (empty: Manga in your home folder)">
                <PathInput value={draft.manga.dir ?? ""} onChange={v => set("manga", { dir: v })} placeholder="~/Manga" />
            </Row>
            <Row label="Reading mode">
                <Select
                    className="w-44"
                    value={draft.manga.readingMode}
                    onChange={v => set("manga", { readingMode: v as "double" })}
                    options={[
                        { value: "double", label: "Two pages" },
                        { value: "paged", label: "Single page" },
                        { value: "long-strip", label: "Long strip" },
                    ]}
                />
            </Row>
            <Row label="Reading direction">
                <Select
                    className="w-44"
                    value={draft.manga.direction}
                    onChange={v => set("manga", { direction: v })}
                    options={[
                        { value: "rtl", label: "Right to left (manga)" },
                        { value: "ltr", label: "Left to right" },
                    ]}
                />
            </Row>
        </Group>
    )
}

function DiscordSection({ draft, set }: SectionProps) {
    return (
        <Group title="Rich presence" description="Talks to the Discord app on this computer — nothing is sent anywhere else.">
            <Row label="Show what I'm watching">
                <Switch checked={draft.discord.richPresence} onChange={v => set("discord", { richPresence: v })} />
            </Row>
            <Locked>
                <Stack label="Discord application ID" help="Create an application at discord.com/developers and paste its Application ID.">
                    <Input value={draft.discord.clientId} onChange={e => set("discord", { clientId: e.target.value })} />
                </Stack>
            </Locked>
        </Group>
    )
}

function LogsSection() {
    const { data: logs, refetch } = useQuery({ queryKey: ["logs"], queryFn: () => api.get<string[]>("/api/logs"), refetchInterval: 4000 })
    const { data: cache, refetch: refetchCache } = useQuery({ queryKey: ["cache-size"], queryFn: () => api.get<{ entries: number }>("/api/cache/size") })
    const { data: imgs, refetch: refetchImgs } = useQuery({ queryKey: ["image-cache"], queryFn: () => api.get<{ files: number; bytes: number }>("/api/images/stats") })
    const clear = async () => {
        try {
            await api.post("/api/cache/clear")
            toast.success("Cache cleared")
            refetchCache()
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const clearImages = async () => {
        try {
            await api.post("/api/images/clear")
            toast.success("Artwork cache cleared — covers will be downloaded again")
            refetchImgs()
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <>
            <Group title="Cache" description="Cached AniList data, episode metadata, search results and probes.">
                <Row label={`${cache?.entries ?? 0} cached entries`}>
                    <Button variant="danger" size="sm" icon={<Trash2 className="size-4" />} onClick={clear}>
                        Clear cache
                    </Button>
                </Row>
                <Row label={`Artwork: ${imgs?.files ?? 0} images (${formatBytes(imgs?.bytes ?? 0)})`} help="Covers, banners and episode thumbnails downloaded for offline use">
                    <Button variant="danger" size="sm" icon={<Trash2 className="size-4" />} onClick={clearImages}>
                        Clear artwork
                    </Button>
                </Row>
            </Group>
            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h3 className="font-semibold">Server logs</h3>
                    <Button size="sm" variant="ghost" onClick={() => refetch()}>
                        Refresh
                    </Button>
                </div>
                <pre className="card max-h-[60vh] overflow-auto p-4 text-xs leading-relaxed text-fg/80">{(logs ?? []).slice(-400).join("\n") || "No logs yet."}</pre>
            </div>
        </>
    )
}

// The version, and Aniko's own updates: the Home page offers and installs
// them, this says where things stand.
function AboutSection() {
    const { data: status } = useStatus()
    const { data: u } = useUpdateStatus()
    const check = useCheckUpdate()
    const save = useSaveSettings()
    const beta = status?.settings.updates?.channel === "beta"
    const lan = status?.client === "lan"
    const commit = u?.current.commit
    const latest = versionName(u?.latest)
    const busy = ["downloading", "building", "installing"].includes(u?.state ?? "")
    let state: { tone: "green" | "brand" | "amber" | "gray"; label: string; help?: string }
    if (!u || u.checking) state = { tone: "gray", label: "Checking…" }
    else if (busy) state = { tone: "brand", label: `Updating to ${latest}`, help: u.message }
    else if (u.state === "ready") state = { tone: "green", label: `${latest} installed`, help: u.message }
    else if (u.available) state = { tone: "brand", label: `${latest} available`, help: lan || u.canApply ? undefined : u.applyNote }
    else if (u.latest) state = { tone: "green", label: "Up to date", help: u.note }
    else if (u.error) state = { tone: "amber", label: "Couldn't check" }
    else state = { tone: "gray", label: "Not checked yet", help: "Aniko checks shortly after it starts, then every 6 hours." }
    if (u?.state === "failed" && !busy) state.help = [u.message, state.help].filter(Boolean).join(" ")
    return (
        <>
            <Group title="Aniko">
                <Row label="Version" help={`${platformName(status?.platform)} · ${status?.client === "desktop" ? "Desktop" : status?.client === "lan" ? "LAN" : "Web UI"}`}>
                    <span className="flex items-center gap-2 text-sm">
                        {status?.version}
                        {commit && (
                            <code className="rounded bg-black/30 px-1.5 py-0.5 text-xs" title={commit}>
                                {commit.slice(0, 7)}
                            </code>
                        )}
                    </span>
                </Row>
                <Row label="What's new" help="What came with the latest versions.">
                    <Button size="sm" variant="ghost" onClick={() => whatsNewStore.set("all")}>
                        Show
                    </Button>
                </Row>
            </Group>
            {/* Installing it, from here too (as on Home). */}
            {!lan && <UpdateBanner inSettings />}
            <Group title="Updates" description="Aniko looks for a newer version on GitHub and offers it here and on the Home page.">
                <Row
                    label="Beta updates"
                    help={
                        beta
                            ? "On: each new version comes here first, as a beta, before everyone gets it. Betas can have rough edges."
                            : "Off: only the stable releases everyone gets. Turn on to get each new version first, as a beta."
                    }
                >
                    <Locked>
                        <Switch
                            checked={beta}
                            disabled={!status || save.isPending}
                            onChange={v => status && save.mutate({ ...status.settings, updates: { channel: v ? "beta" : "stable" } })}
                        />
                    </Locked>
                </Row>
                <Row label="Status" help={state.help}>
                    <Badge tone={state.tone}>{state.label}</Badge>
                </Row>
                {u?.latest && (
                    <Row label="Newest version">
                        <a href={u.latest.url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 text-sm hover:underline">
                            {latest}
                            {u.latest.commit && u.latest.version && <code className="rounded bg-black/30 px-1.5 py-0.5 text-xs">{u.latest.commit.slice(0, 7)}</code>}
                        </a>
                    </Row>
                )}
                <Row label="Last check" help={u?.checking ? "Checking now…" : u?.checkedAt ? `${relativeTime(u.checkedAt)}. Aniko checks each time it opens, and every 6 hours.` : "Never"}>
                    {!lan && (
                        <Button size="sm" icon={<RefreshCw className="size-4" />} loading={check.isPending || u?.checking} onClick={() => check.mutate()}>
                            Check for updates
                        </Button>
                    )}
                </Row>
                {u?.error && (
                    <div className="px-4 py-3.5 text-sm">
                        <p className="text-rose-300">{u.error}</p>
                        {u.hint && <p className={cn("mt-1 break-words", u.hint.startsWith("Details: ") ? "text-xs text-subtle" : "text-muted")}>{u.hint}</p>}
                    </div>
                )}
            </Group>
        </>
    )
}
