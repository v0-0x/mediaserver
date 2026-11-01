import { Check, ChevronRight, ChevronUp, History, Palette, Settings2, Sparkles } from "lucide-react"
import { useNavigate } from "react-router-dom"
import { useSaveUI, useSettings } from "@/lib/queries"
import { useStore } from "@/lib/store"
import { activeHoliday, COLOR_THEMES, HOLIDAYS, holidayStore, useClassic, type ColorTheme } from "@/lib/themes"
import { tv } from "@/lib/tv"
import { cn } from "@/lib/utils"
import { HolidayIcon } from "./HolidayBadge"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "./ui"

// The sidebar's Themes menu: every color theme, the holiday themes (by the
// date, off, or one always) and their animation, and the classic look,
// changed at once and the menu left open to try another. Settings ›
// Appearance has the same.
export function ThemesMenu({ wide }: { wide: boolean }) {
    const holiday = useStore(holidayStore)
    // Drawn as it is now (one being tried in Settings too).
    const classic = useClassic()
    // In the theme's own colors: its accent (the holiday's while one is on)
    // tints it, and its drawing (or a palette) leads.
    const icon = holiday ? <HolidayIcon id={holiday.id} className="size-4" /> : <Palette className="size-3.5 stroke-2 text-brand-strong" />
    const button = classic ? (
        // Like the other links, as the sidebar was before 2.0.
        <DropdownTrigger asChild>
            <button
                aria-label="Themes"
                className={cn(
                    "focus-ring flex h-9 shrink-0 items-center rounded-md text-[13.5px] font-medium text-muted transition-colors hover:bg-white/[0.04] hover:text-fg data-[state=open]:bg-white/[0.06]",
                    wide ? "gap-3 px-2.5" : "w-10 justify-center",
                )}
            >
                {holiday ? <HolidayIcon id={holiday.id} className="size-[18px] shrink-0" /> : <Palette className="size-[18px] shrink-0 stroke-[1.75]" />}
                {wide && <span className="truncate">Themes</span>}
            </button>
        </DropdownTrigger>
    ) : (
        // A link like the others, led by a small round chip in the accent
        // (the holiday's drawing, or a palette); lit with the accent while
        // open, the chevron turning.
        <DropdownTrigger asChild>
            <button
                aria-label="Themes"
                className={cn(
                    "focus-ring group flex shrink-0 items-center rounded-lg text-[13.5px] font-medium text-muted transition-[background-color,color] duration-200 ease-out",
                    "hover:bg-white/[0.05] hover:text-fg data-[state=open]:bg-brand-soft data-[state=open]:text-fg",
                    wide ? "h-9 gap-2.5 pr-2 pl-1.5" : "size-10 justify-center",
                )}
            >
                <span className="grid size-6 shrink-0 place-items-center rounded-full bg-[color-mix(in_oklab,var(--brand)_16%,transparent)] transition-[transform,background-color] duration-300 ease-out group-hover:scale-105 group-hover:bg-[color-mix(in_oklab,var(--brand)_24%,transparent)]">
                    {icon}
                </span>
                {wide && (
                    <>
                        <span className="min-w-0 flex-1 truncate text-left">Themes</span>
                        <ChevronUp className="size-3.5 shrink-0 text-subtle transition-transform duration-200 ease-out group-data-[state=open]:rotate-180" />
                    </>
                )}
            </button>
        </DropdownTrigger>
    )
    return (
        <Dropdown>
            {wide ? (
                button
            ) : (
                <Tooltip content="Themes" side="right">
                    {button}
                </Tooltip>
            )}
            {classic ? <ListMenu wide={wide} /> : <PanelMenu wide={wide} />}
        </Dropdown>
    )
}

// A switch, drawn: the item it's in is what's pressed.
function MenuSwitch({ on }: { on: boolean }) {
    return (
        <span aria-hidden className={cn("relative h-[18px] w-8 shrink-0 rounded-full transition-colors", on ? "bg-brand" : "bg-white/[0.14]")}>
            <span className={cn("absolute top-[3px] size-3 rounded-full bg-white transition-transform", on ? "translate-x-[17px]" : "translate-x-[3px]")} />
        </span>
    )
}

function Choice({ on, onSelect, icon, children }: { on: boolean; onSelect: () => void; icon: React.ReactNode; children: React.ReactNode }) {
    return (
        <DropdownItem keepOpen onSelect={onSelect} className="h-auto min-h-8 py-1.5">
            <span className="grid size-4 shrink-0 place-items-center">{icon}</span>
            {children}
            <Check className={cn("size-3.5 shrink-0 text-brand-strong", !on && "invisible")} />
        </DropdownItem>
    )
}

// A color theme in small: its window, page and accent.
function Swatch({ theme }: { theme: ColorTheme }) {
    const p = theme.palette
    return (
        <span aria-hidden className="relative size-6 shrink-0 overflow-hidden rounded-md ring-1 ring-white/15" style={{ background: p.frame }}>
            <span className="absolute inset-y-1 right-0 left-1.5 rounded-l-sm" style={{ background: p.s2 }} />
            <span className="absolute right-1 bottom-1 size-2 rounded-full" style={{ background: theme.accent }} />
        </span>
    )
}

function CalendarIcon() {
    return (
        <svg viewBox="0 0 16 16" className="size-4 text-muted" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden>
            <rect x="2" y="3" width="12" height="11" rx="2" />
            <path d="M2 6.5h12M5.5 1.5v3M10.5 1.5v3" strokeLinecap="round" />
        </svg>
    )
}

// What both menus show and change. The switches are the saved settings,
// like the rest.
function useThemes() {
    const ui = useSettings()?.ui
    const saveUI = useSaveUI()
    return {
        saveUI,
        navigate: useNavigate(),
        holiday: useStore(holidayStore),
        theme: ui?.colorTheme ?? "aniko",
        seasonal: ui?.seasonal ?? "auto",
        effects: ui?.seasonalEffects ?? true,
        classicLook: !!ui?.classicLook,
        // What "by the date" is now.
        today: activeHoliday("auto"),
    }
}

// The classic look's menu: a list, as before 2.0.
function ListMenu({ wide }: { wide: boolean }) {
    const { saveUI, navigate, holiday, theme, seasonal, effects, classicLook, today } = useThemes()
    return (
        <DropdownContent
            side={wide ? "top" : "right"}
            align={wide ? "start" : "end"}
            className="max-h-[min(640px,var(--radix-dropdown-menu-content-available-height))] w-72 overflow-y-auto"
        >
            <DropdownLabel>Colors</DropdownLabel>
            {/* Two columns; one on a TV, where Up and Down go through them. */}
            <div className={cn("grid gap-0.5", tv ? "grid-cols-1" : "grid-cols-2")}>
                {COLOR_THEMES.map(t => (
                    <DropdownItem key={t.id} keepOpen onSelect={() => saveUI({ colorTheme: t.id, accentColor: t.accent })} className="h-9 gap-2 px-2">
                        <Swatch theme={t} />
                        <span className="min-w-0 flex-1 truncate">{t.name}</span>
                        {theme === t.id && <Check className="size-3.5 shrink-0 text-brand-strong" />}
                    </DropdownItem>
                ))}
            </div>
            <DropdownSeparator />
            <DropdownLabel>Holiday theme</DropdownLabel>
            <Choice on={seasonal === "auto"} onSelect={() => saveUI({ seasonal: "auto" })} icon={<CalendarIcon />}>
                <span className="min-w-0 flex-1">
                    By the date
                    <span className="block text-[11px] text-subtle">{today ? `${today.name} now` : "None right now"}</span>
                </span>
            </Choice>
            <Choice on={seasonal === "off"} onSelect={() => saveUI({ seasonal: "off" })} icon={<span className="size-4 rounded-full border border-dashed border-white/30" />}>
                Off
            </Choice>
            {HOLIDAYS.map(h => (
                <Choice key={h.id} on={seasonal === h.id} onSelect={() => saveUI({ seasonal: h.id })} icon={<HolidayIcon id={h.id} className="size-4" />}>
                    <span className="min-w-0 flex-1 truncate">{h.name}</span>
                    <span className="text-[11px] text-subtle">{h.when.replace(/^In /, "")}</span>
                </Choice>
            ))}
            <DropdownSeparator />
            <DropdownItem keepOpen disabled={!holiday} onSelect={() => saveUI({ seasonalEffects: !effects })} icon={<Sparkles />} className="h-9">
                <span className="min-w-0 flex-1">{holiday ? `Animation: ${holiday.effectName}` : "Holiday animations"}</span>
                <MenuSwitch on={effects && !!holiday} />
            </DropdownItem>
            <DropdownItem keepOpen onSelect={() => saveUI({ classicLook: !classicLook })} icon={<History />} className="h-9">
                <span className="min-w-0 flex-1">Classic look</span>
                <MenuSwitch on={classicLook} />
            </DropdownItem>
            <DropdownItem icon={<Settings2 />} onSelect={() => navigate("/settings?tab=appearance")}>
                More in Settings
            </DropdownItem>
        </DropdownContent>
    )
}

// The 2.0 menu: a panel. Each color theme is a small window in its colors,
// the holiday themes a grid, and the colors fade from one theme to the next.
function PanelMenu({ wide }: { wide: boolean }) {
    const { saveUI, navigate, holiday, theme, seasonal, effects, classicLook, today } = useThemes()
    const current = COLOR_THEMES.find(t => t.id === theme) ?? COLOR_THEMES[0]
    const pick = (ui: Parameters<typeof saveUI>[0]) => {
        fadeColors()
        saveUI(ui)
    }
    return (
        <DropdownContent
            side={wide ? "top" : "right"}
            align={wide ? "start" : "end"}
            sideOffset={8}
            className="max-h-[min(660px,var(--radix-dropdown-menu-content-available-height))] w-[336px] overflow-y-auto rounded-[14px] p-2"
        >
            <div className="flex items-center gap-3 px-1.5 pt-1 pb-1.5">
                <Window theme={current} accent={holiday?.accent ?? current.accent} className="h-8 w-11 rounded-lg" />
                <div className="min-w-0 flex-1">
                    <p className="text-[14px] font-semibold text-fg">Themes</p>
                    <p className="truncate text-[12px] text-subtle">
                        {current.name}
                        {holiday ? ` with ${holiday.name}` : ""}
                    </p>
                </div>
            </div>

            <Section label="Colors">
                <Grid cols={4}>
                    {COLOR_THEMES.map(t => {
                        const on = theme === t.id
                        return (
                            <DropdownItem
                                key={t.id}
                                keepOpen
                                onSelect={() => pick({ colorTheme: t.id, accentColor: t.accent })}
                                className="group/tile h-auto flex-col items-stretch gap-1.5 rounded-[10px] p-1.5 data-[highlighted]:bg-white/[0.06]"
                            >
                                <span className="relative block">
                                    <Window
                                        theme={t}
                                        accent={t.accent}
                                        className={cn(
                                            "h-9 w-full rounded-lg transition-[box-shadow,transform] duration-200 ease-out group-data-[highlighted]/tile:-translate-y-px",
                                            on ? "shadow-[0_0_0_2px_var(--color-surface-2),0_0_0_3.5px_var(--brand)]" : "shadow-[0_0_0_1px_rgb(255_255_255/0.1)]",
                                        )}
                                    />
                                    <span
                                        className={cn(
                                            "absolute -top-1 -right-1 grid size-4 place-items-center rounded-full bg-brand text-white shadow-[0_0_0_2px_var(--color-surface-2)] transition-[opacity,scale] duration-200 ease-out",
                                            on ? "scale-100 opacity-100" : "scale-50 opacity-0",
                                        )}
                                    >
                                        <Check className="size-2.5 stroke-[3.5]" />
                                    </span>
                                </span>
                                <span className={cn("truncate text-center text-[11.5px] transition-colors", on ? "font-medium text-fg" : "text-muted")}>{t.name}</span>
                            </DropdownItem>
                        )
                    })}
                </Grid>
            </Section>

            <Section label="Holiday theme">
                <Grid cols={2}>
                    <Mode
                        on={seasonal === "auto"}
                        onSelect={() => pick({ seasonal: "auto" })}
                        icon={<CalendarIcon />}
                        name="By the date"
                        note={today ? `${today.name} now` : "None right now"}
                    />
                    <Mode
                        on={seasonal === "off"}
                        onSelect={() => pick({ seasonal: "off" })}
                        icon={<span className="size-4 rounded-full border border-dashed border-white/30" />}
                        name="Off"
                        note="Plain colors all year"
                    />
                </Grid>
                <div className="h-1" />
                <Grid cols={3}>
                    {HOLIDAYS.map(h => (
                        <HolidayTile
                            key={h.id}
                            on={seasonal === h.id}
                            onSelect={() => pick({ seasonal: h.id })}
                            icon={<HolidayIcon id={h.id} className="size-[18px]" />}
                            name={h.name}
                        />
                    ))}
                </Grid>
            </Section>

            <div className="mt-2 rounded-[10px] bg-white/[0.03] p-1">
                <DropdownItem keepOpen disabled={!holiday} onSelect={() => saveUI({ seasonalEffects: !effects })} icon={<Sparkles />} className="h-9 rounded-lg">
                    <span className="min-w-0 flex-1 truncate">{holiday ? `Animation: ${holiday.effectName}` : "Holiday animations"}</span>
                    <MenuSwitch on={effects && !!holiday} />
                </DropdownItem>
                <DropdownItem keepOpen onSelect={() => pick({ classicLook: !classicLook })} icon={<History />} className="h-9 rounded-lg">
                    <span className="min-w-0 flex-1">
                        Classic look
                        <span className="ml-1.5 text-[11.5px] text-subtle">1.0</span>
                    </span>
                    <MenuSwitch on={classicLook} />
                </DropdownItem>
            </div>

            <DropdownItem icon={<Settings2 />} onSelect={() => navigate("/settings?tab=appearance")} className="mt-1 h-9 rounded-lg text-muted data-[highlighted]:text-fg">
                <span className="flex-1">More in Settings</span>
                <ChevronRight className="size-3.5 text-subtle" />
            </DropdownItem>
        </DropdownContent>
    )
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
    return (
        <div className="pt-2">
            <p className="px-1.5 pb-1.5 text-[11.5px] font-medium text-subtle">{label}</p>
            {children}
        </div>
    )
}

// Items in rows: the arrow keys (a TV's too) move across and down them like
// a grid, ahead of the menu's own Up and Down (one item at a time); Up from
// the top row and Down from the bottom one leave it.
function Grid({ cols, children }: { cols: 2 | 3 | 4; children: React.ReactNode }) {
    const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        const step = {
            ArrowLeft: -1,
            ArrowRight: 1,
            ArrowUp: -cols,
            ArrowDown: cols,
        }[e.key]
        if (!step) return
        const items = [...e.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]')]
        const at = items.indexOf(document.activeElement as HTMLElement)
        if (at < 0) return
        let to: HTMLElement | undefined = items[at + step]
        if (!to && (e.key === "ArrowLeft" || e.key === "ArrowRight")) return
        if (!to) {
            // Out of the grid: the menu's item before or after it.
            const all = [...(e.currentTarget.closest('[role="menu"]')?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([data-disabled])') ?? [])]
            const edge = all.indexOf(step < 0 ? items[0] : items[items.length - 1])
            to = all[edge + (step < 0 ? -1 : 1)]
            if (!to) return
        }
        e.preventDefault()
        to.focus()
    }
    return (
        <div onKeyDownCapture={onKeyDown} className={cn("grid gap-1", { 2: "grid-cols-2", 3: "grid-cols-3", 4: "grid-cols-4" }[cols])}>
            {children}
        </div>
    )
}

// A holiday or mode, lit in the accent while it's the one chosen.
const tile = (on: boolean) =>
    cn(
        "rounded-[10px] transition-[background-color,box-shadow] duration-200 ease-out",
        on
            ? "bg-brand-soft shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--brand)_45%,transparent)] data-[highlighted]:bg-brand-soft"
            : "bg-white/[0.025] data-[highlighted]:bg-white/[0.07]",
    )

// By the date or off: what decides the holiday theme.
function Mode({ on, onSelect, icon, name, note }: { on: boolean; onSelect: () => void; icon: React.ReactNode; name: string; note: string }) {
    return (
        <DropdownItem keepOpen onSelect={onSelect} className={cn("h-11 gap-2.5 px-2.5", tile(on))}>
            <span className="grid size-4 shrink-0 place-items-center">{icon}</span>
            <span className="min-w-0 flex-1 leading-tight">
                <span className={cn("block truncate text-[12.5px]", on ? "font-medium text-fg" : "text-muted")}>{name}</span>
                <span className="block truncate text-[11px] text-subtle">{note}</span>
            </span>
        </DropdownItem>
    )
}

// One holiday, on all year.
function HolidayTile({ on, onSelect, icon, name }: { on: boolean; onSelect: () => void; icon: React.ReactNode; name: string }) {
    return (
        <DropdownItem keepOpen onSelect={onSelect} className={cn("h-12 flex-col justify-center gap-1 px-1", tile(on))}>
            <span className="grid size-[18px] place-items-center">{icon}</span>
            <span className={cn("w-full truncate text-center text-[11.5px]", on ? "font-medium text-fg" : "text-muted")}>{name}</span>
        </DropdownItem>
    )
}

// A color theme as a small window: its frame, sidebar, page and accent.
function Window({ theme, accent, className }: { theme: ColorTheme; accent: string; className?: string }) {
    const p = theme.palette
    return (
        <span aria-hidden className={cn("relative block shrink-0 overflow-hidden", className)} style={{ background: p.frame }}>
            <span className="absolute top-[18%] left-[9%] h-[9%] w-[16%] rounded-full" style={{ background: accent }} />
            <span className="absolute top-[38%] left-[9%] h-[7%] w-[14%] rounded-full" style={{ background: p.s4 }} />
            <span className="absolute top-[56%] left-[9%] h-[7%] w-[14%] rounded-full" style={{ background: p.s4 }} />
            <span className="absolute inset-y-[12%] right-[6%] left-[32%] rounded-[3px]" style={{ background: p.bg }}>
                <span className="absolute inset-x-[10%] top-[14%] h-[34%] rounded-[2px]" style={{ background: p.s3 }} />
                <span className="absolute bottom-[16%] left-[10%] h-[12%] w-[38%] rounded-full" style={{ background: accent }} />
            </span>
        </span>
    )
}

// The colors fade from one theme to the next, for a moment (not on a TV,
// whose box is slow, or for those who'd rather nothing moved).
let fading = 0
function fadeColors() {
    if (tv || matchMedia("(prefers-reduced-motion: reduce)").matches) return
    const root = document.documentElement
    root.classList.add("theme-fade")
    clearTimeout(fading)
    fading = window.setTimeout(() => root.classList.remove("theme-fade"), 450)
}
