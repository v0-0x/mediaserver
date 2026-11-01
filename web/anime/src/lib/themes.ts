// The app's colors (Settings › Appearance › Theme) and the holiday
// themes that come on by themselves in their month: Christmas in December,
// Halloween in October... (or always one, or none). The server keeps the
// choice (ui.colorTheme, ui.seasonal, ui.seasonalEffects) and the names
// (config.ColorThemes, config.Holidays).

import type { SearchParams } from "./queries"
import { createStore, useStore } from "./store"

// The window (frame, where the sidebar is), the pages (bg), panels on them
// (surface 1-4) and the grey text, as in index.css.
type Palette = { frame: string; bg: string; s1: string; s2: string; s3: string; s4: string; muted: string; subtle: string }

export type ColorTheme = { id: string; name: string; accent: string; palette: Palette }

export const COLOR_THEMES: ColorTheme[] = [
    { id: "aniko", name: "Aniko", accent: "#7c6cf2", palette: { frame: "#0a0a0b", bg: "#111113", s1: "#161618", s2: "#1c1c1f", s3: "#232326", s4: "#2b2b2f", muted: "#a1a1a8", subtle: "#707077" } },
    { id: "midnight", name: "Midnight", accent: "#5b8cff", palette: { frame: "#070a12", bg: "#0c111c", s1: "#111827", s2: "#172033", s3: "#1e2940", s4: "#26334d", muted: "#9aa4b8", subtle: "#6b7488" } },
    { id: "forest", name: "Forest", accent: "#34d399", palette: { frame: "#070b09", bg: "#0c120f", s1: "#111a15", s2: "#16211b", s3: "#1c2a22", s4: "#23342a", muted: "#9fb0a6", subtle: "#6d7f74" } },
    { id: "sakura", name: "Sakura", accent: "#f472b6", palette: { frame: "#0d0709", bg: "#140c10", s1: "#1b1116", s2: "#22161c", s3: "#2a1c23", s4: "#33222b", muted: "#b39aa6", subtle: "#7f6872" } },
    { id: "ember", name: "Ember", accent: "#f59e0b", palette: { frame: "#0d0906", bg: "#14100b", s1: "#1b1610", s2: "#221c14", s3: "#2a2219", s4: "#332a1f", muted: "#b3a691", subtle: "#7f7461" } },
    { id: "ocean", name: "Ocean", accent: "#22d3ee", palette: { frame: "#050c0e", bg: "#0a1416", s1: "#0f1c1f", s2: "#142428", s3: "#1a2d32", s4: "#21373d", muted: "#93aeb3", subtle: "#647d82" } },
    { id: "dusk", name: "Dusk", accent: "#a78bfa", palette: { frame: "#0a0810", bg: "#100d18", s1: "#161220", s2: "#1c1728", s3: "#231d31", s4: "#2b243b", muted: "#a79fb8", subtle: "#766e88" } },
    // True black: OLED screens (and TVs) switch those pixels off.
    { id: "black", name: "Black", accent: "#7c6cf2", palette: { frame: "#000000", bg: "#000000", s1: "#0b0b0c", s2: "#121214", s3: "#19191c", s4: "#212124", muted: "#a1a1a8", subtle: "#6e6e75" } },
]

export const colorTheme = (id?: string) => COLOR_THEMES.find(t => t.id === id) ?? COLOR_THEMES[0]

export type Effect = "snow" | "bats" | "hearts" | "petals" | "fireworks" | "fireflies"

export type Holiday = {
    id: string
    name: string
    // When it's on by itself ("In December").
    when: string
    accent: string
    // The color the whole app leans to while it's on (a little of it in the
    // window and the pages' colors): Halloween's purple night...
    tint: string
    effect: Effect
    effectName: string // "Falling snow"
    // Month 1-12, and the days of it (all of it without).
    month: number
    days?: [number, number]
    // A row of anime for it on Discover.
    picks?: { title: string; params: SearchParams; more?: string }
}

export const HOLIDAYS: Holiday[] = [
    { id: "newyear", name: "New Year", when: "January 1–7", accent: "#f5c542", tint: "#1e3a8a", effect: "fireworks", effectName: "Fireworks", month: 1, days: [1, 7] },
    {
        id: "valentine",
        name: "Valentine's Day",
        when: "In February",
        accent: "#f2557a",
        tint: "#9f1239",
        effect: "hearts",
        effectName: "Floating hearts",
        month: 2,
        picks: { title: "For Valentine's Day", params: { genres: ["Romance"], sort: ["POPULARITY_DESC"] }, more: "/search?genre=Romance&sort=POPULARITY_DESC" },
    },
    { id: "sakura", name: "Cherry blossoms", when: "In April", accent: "#f9a8d4", tint: "#be185d", effect: "petals", effectName: "Falling petals", month: 4 },
    { id: "summer", name: "Summer festival", when: "In August", accent: "#2dd4bf", tint: "#155e75", effect: "fireflies", effectName: "Fireflies", month: 8 },
    {
        id: "halloween",
        name: "Halloween",
        when: "In October",
        accent: "#f97316",
        tint: "#6b21a8",
        effect: "bats",
        effectName: "Bats",
        month: 10,
        picks: { title: "Halloween picks", params: { genres: ["Horror"], sort: ["POPULARITY_DESC"] }, more: "/search?genre=Horror&sort=POPULARITY_DESC" },
    },
    {
        id: "christmas",
        name: "Christmas",
        when: "In December",
        accent: "#ef4444",
        tint: "#166534",
        effect: "snow",
        effectName: "Falling snow",
        month: 12,
        picks: { title: "Christmas anime", params: { tags: ["Christmas"], sort: ["POPULARITY_DESC"] } },
    },
]

// The holiday theme on now: the one asked for always, or by the date
// ("auto"), or none ("off").
export function activeHoliday(setting: string | undefined, now = new Date()): Holiday | null {
    if (setting === "off") return null
    if (setting && setting !== "auto") return HOLIDAYS.find(h => h.id === setting) ?? null
    const m = now.getMonth() + 1
    const d = now.getDate()
    return HOLIDAYS.find(h => h.month === m && (!h.days || (d >= h.days[0] && d <= h.days[1]))) ?? null
}

export type ThemeChoice = { colorTheme?: string; accentColor?: string; seasonal?: string; seasonalEffects?: boolean; classicLook?: boolean }

// What's drawn: the colors, the accent (the holiday's while one is on), the
// holiday (its glow, the sidebar's decoration and its animation), and
// whether in the classic look (before 2.0).
export function resolveTheme(ui: ThemeChoice | undefined, now = new Date()) {
    const theme = colorTheme(ui?.colorTheme)
    const holiday = activeHoliday(ui?.seasonal ?? "auto", now)
    return { theme, holiday, accent: holiday?.accent ?? (ui?.accentColor || theme.accent), effects: !!holiday && (ui?.seasonalEffects ?? true), classic: !!ui?.classicLook }
}

const CACHE_KEY = "aniko-theme"

// Sets the colors on the page, and remembers them for the next start (drawn
// before the settings come from the server: no flash of the default ones).
// cache: remembered for the next start (not colors only being tried).
export function applyTheme(t: { theme: ColorTheme; holiday: Holiday | null; accent: string; classic: boolean }, cache = true) {
    const p = t.theme.palette
    // A holiday leans the colors toward its own, a little (the window most;
    // not in the classic look, whose holidays only brought their accent).
    const lean = (color: string, pct: number) => (t.holiday && !t.classic ? `color-mix(in oklab, ${color} ${100 - pct}%, ${t.holiday.tint})` : color)
    const vars: Record<string, string> = {
        "--brand": t.accent,
        "--frame": lean(p.frame, 12),
        "--bg": lean(p.bg, 8),
        "--surface-1": lean(p.s1, 7),
        "--surface-2": lean(p.s2, 7),
        "--surface-3": lean(p.s3, 6),
        "--surface-4": lean(p.s4, 6),
        "--muted": p.muted,
        "--subtle": p.subtle,
    }
    setVars(vars, t.holiday?.id ?? "", t.classic)
    // The browser's own bars (phones) in the window's color.
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", p.frame)
    if (!cache) return
    try {
        localStorage.setItem(CACHE_KEY, JSON.stringify({ vars, holiday: t.holiday?.id ?? "", classic: t.classic }))
    } catch {
        /* drawn with the default colors at the next start */
    }
}

function setVars(vars: Record<string, string>, holiday: string, classic: boolean) {
    const el = document.documentElement
    for (const [k, v] of Object.entries(vars)) el.style.setProperty(k, v)
    if (holiday) el.dataset.holiday = holiday
    else delete el.dataset.holiday
    // For index.css (data-look="classic"); the components read classicStore.
    if (classic) el.dataset.look = "classic"
    else delete el.dataset.look
    classicStore.set(classic)
}

// The colors used last time, at once (main.tsx, before the first render).
export function applyCachedTheme() {
    try {
        const c = JSON.parse(localStorage.getItem(CACHE_KEY) ?? "null") as { vars?: Record<string, string>; holiday?: string; classic?: boolean } | null
        if (c?.vars) setVars(c.vars, HOLIDAYS.some(h => h.id === c.holiday) ? c.holiday! : "", c.classic === true)
    } catch {
        /* the default colors */
    }
}

// Changed in Settings and not saved yet (null: the saved ones).
export const themePreviewStore = createStore<ThemeChoice | null>(null)

// The holiday theme drawn now (App sets it): the sidebar decorates for it.
export const holidayStore = createStore<Holiday | null>(null)

// The classic look (before 2.0) is on: the components draw themselves as
// they were then (ui.tsx, the sidebar, cards, the holiday themes).
export const classicStore = createStore(false)
export const useClassic = () => useStore(classicStore)
