import { ChevronLeft, ChevronRight, Ellipsis, HardDrive, Star } from "lucide-react"
import { Children, isValidElement, useEffect, useRef, useState } from "react"
import { Link } from "react-router-dom"
import { airedEpisodes, episodeIndex, formatKey, mediaIsShow, totalEpisodes } from "@/lib/ids"
import type { ListEntry, Media } from "@/lib/types"
import { airsIn, cn, cover, entryUrl, formatLabel, poster, starText, title, year } from "@/lib/utils"
import { useClassic } from "@/lib/themes"
import { tv } from "@/lib/tv"
import { Dropdown, DropdownContent, DropdownTrigger, IconButton } from "./ui"

type Props = {
    media: Media
    listEntry?: Pick<ListEntry, "progress" | "status" | "score"> | null
    localCount?: number
    downloaded?: number[] | null
    className?: string
    showProgress?: boolean
    size?: "sm" | "md" | "lg"
    menu?: React.ReactNode // items of a menu opened with a "⋯" button or a right-click
}

const statusDot: Record<string, string> = {
    CURRENT: "bg-sky-400",
    REPEATING: "bg-sky-400",
    PLANNING: "bg-violet-400",
    COMPLETED: "bg-emerald-400",
    PAUSED: "bg-amber-400",
    DROPPED: "bg-rose-400",
}

export function MediaCard({ media, listEntry, localCount, downloaded, className, showProgress = true, menu }: Props) {
    const show = mediaIsShow(media)
    const total = totalEpisodes(media)
    // Progress is an episode key: "12 / 24" counts episodes.
    const watched = listEntry ? episodeIndex(media, listEntry.progress ?? 0) : 0
    // "new" = aired episodes of an airing show the user hasn't watched yet
    const behind = show && media.status === "RELEASING" && listEntry && (listEntry.status === "CURRENT" || listEntry.status === "REPEATING") ? Math.max(0, airedEpisodes(media) - watched) : 0
    const nextAir = show ? media.nextAiringEpisode : null
    const score = media.averageScore ?? media.meanScore
    // The user's rating, Letterboxd-style, for movies and finished shows.
    const stars = listEntry && (!show || listEntry.status === "COMPLETED") ? starText(listEntry.score) : ""
    // Before 2.0: flatter, squarer, a lighter hairline on hover, no lift.
    const classic = useClassic()
    const label = classic ? "rounded bg-black/70" : "rounded-md bg-black/65 backdrop-blur-sm"

    const card = (
        <Link to={entryUrl(media)} data-tv-card className={cn("group/card focus-ring relative flex flex-col gap-2.5 outline-none", classic ? "rounded-lg" : "rounded-[10px]", !menu && className)}>
            {/* Lifted a little under the pointer (not on a TV: it's slow at it). */}
            <div
                className={cn(
                    "relative aspect-[2/3] overflow-hidden bg-surface-2",
                    classic ? "rounded-lg" : "rounded-[10px] shadow-[0_6px_18px_-12px_rgb(0_0_0/0.8)] transition-[transform,box-shadow] duration-300 ease-out",
                    !tv && !classic && "group-hover/card:-translate-y-0.5 group-hover/card:shadow-[0_12px_28px_-14px_rgb(0_0_0/0.9)]",
                )}
            >
                {cover(media) && (
                    <img
                        {...poster(media, 220)}
                        alt=""
                        loading="lazy"
                        decoding="async"
                        className="size-full object-cover transition-[filter] duration-300 group-hover/card:brightness-110"
                        style={{ backgroundColor: media.coverImage?.color || undefined }}
                    />
                )}
                {/* A hairline, so dark covers don't melt into the page. */}
                <div
                    data-tv-ring
                    className={cn(
                        "pointer-events-none absolute inset-0 ring-1 transition-[box-shadow] duration-300 ring-inset",
                        classic ? "rounded-lg ring-white/[0.07] group-hover/card:ring-white/20" : "rounded-[10px] ring-white/[0.07] group-hover/card:ring-white/[0.22]",
                    )}
                />

                <div className="absolute top-2 left-2 flex flex-col items-start gap-1">
                    {behind > 0 && (
                        <span className={cn("bg-brand px-1.5 py-0.5 text-[11px] font-semibold text-white", classic ? "rounded" : "rounded-md")}>{behind} new</span>
                    )}
                    {!!localCount && localCount > 0 && (
                        <span className={cn("flex items-center gap-1 px-1.5 py-0.5 text-[11px] font-medium text-white tabular-nums", label)}>
                            <HardDrive className="size-3" />
                            {show && (downloaded?.length ?? localCount)}
                        </span>
                    )}
                </div>
                {score ? (
                    <span
                        className={cn(
                            "absolute top-2 right-2 flex items-center gap-1 px-1.5 py-0.5 text-[11px] font-medium text-white tabular-nums opacity-0 transition-opacity group-hover/card:opacity-100 group-focus-within/card:opacity-100 [@media(pointer:coarse)]:opacity-100",
                            label,
                            menu && "right-10", // next to the menu button
                        )}
                    >
                        <Star className="size-3 fill-amber-300 text-amber-300" />
                        {(score / 10).toFixed(1)}
                    </span>
                ) : null}
                {nextAir && (
                    <span className={cn("absolute bottom-2.5 left-2 px-1.5 py-0.5 text-[11px] font-medium text-white", label)}>
                        {formatKey(nextAir.episode)} {airsIn(nextAir.airingAt)}
                    </span>
                )}

                {showProgress && show && listEntry && total > 0 && watched > 0 && watched < total && (
                    <div className="absolute inset-x-0 bottom-0 h-[3px] bg-black/50">
                        <div className={cn("h-full bg-brand", !classic && "rounded-r-full")} style={{ width: `${Math.min(100, (watched / total) * 100)}%` }} />
                    </div>
                )}
            </div>
            <div className="px-0.5">
                <p className="line-clamp-2 text-[13px] leading-snug font-medium text-fg/90 transition-colors group-hover/card:text-fg">{title(media)}</p>
                <div className="mt-1 flex items-center gap-1.5 text-xs text-subtle">
                    {listEntry && <span className={cn("size-1.5 shrink-0 rounded-full", statusDot[listEntry.status] ?? "bg-white/50")} />}
                    <span className="truncate">
                        {formatLabel(media.format)}
                        {year(media) ? ` · ${year(media)}` : ""}
                    </span>
                    {show && listEntry && total > 0 && !stars && (
                        <span className="ml-auto shrink-0 tabular-nums">
                            {watched}/{total}
                        </span>
                    )}
                    {stars && <span className="ml-auto shrink-0 tracking-tight text-[#00e054]/85">{stars}</span>}
                </div>
            </div>
        </Link>
    )
    return menu ? (
        <CardMenu menu={menu} className={className}>
            {card}
        </CardMenu>
    ) : (
        card
    )
}

// A card with a menu, opened with a "⋯" button (on hover, always shown on
// touch screens, which can't hover) or by right-clicking the card, at the
// pointer. Both are next to the card's link, not in it, so using them never
// opens the movie or show.
function CardMenu({ menu, className, children }: { menu: React.ReactNode; className?: string; children: React.ReactNode }) {
    const [at, setAt] = useState<{ x: number; y: number } | null>(null) // in the card
    const focused = useRef<Element | null>(null)
    const classic = useClassic()
    return (
        <div
            className={cn("group/card relative", className)}
            onContextMenu={e => {
                e.preventDefault()
                // React also brings right-clicks in the open menu (a portal) here.
                if (!e.currentTarget.contains(e.target as Node)) return
                const r = e.currentTarget.getBoundingClientRect()
                const x = e.clientX - r.left
                const y = e.clientY - r.top
                focused.current = document.activeElement
                // Opened with the menu key there may be no pointer in the card.
                setAt(x >= 0 && y >= 0 && x <= r.width && y <= r.height ? { x, y } : { x: r.width / 2, y: r.height / 3 })
            }}
        >
            {children}
            <Dropdown open={!!at} onOpenChange={v => !v && setAt(null)} modal={false}>
                <DropdownTrigger asChild>
                    <span aria-hidden className="pointer-events-none absolute" style={{ left: at?.x, top: at?.y }} />
                </DropdownTrigger>
                <DropdownContent
                    align="start"
                    sideOffset={2}
                    // Focus goes back where it was (the card, after the menu
                    // key), unless something else took it (a dialog, a click).
                    onCloseAutoFocus={e => {
                        e.preventDefault()
                        if (document.activeElement === document.body && focused.current instanceof HTMLElement) focused.current.focus()
                    }}
                >
                    {menu}
                </DropdownContent>
            </Dropdown>
            <Dropdown>
                <DropdownTrigger asChild>
                    <IconButton
                        label="More options"
                        size="xs"
                        className={cn(
                            "absolute top-1.5 right-1.5 text-white opacity-0 group-hover/card:opacity-100 hover:bg-black/85 hover:text-white focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100",
                            classic ? "rounded-md bg-black/70" : "rounded-lg bg-black/65 backdrop-blur-sm",
                        )}
                    >
                        <Ellipsis className="size-4" />
                    </IconButton>
                </DropdownTrigger>
                <DropdownContent>{menu}</DropdownContent>
            </Dropdown>
        </div>
    )
}

// useProgressive renders big collections of cards (a diary of hundreds of
// films, a whole library) a step at a time: the first `step`, then more as
// the end comes within a screen or so of being seen. Cards nobody scrolls to
// cost no memory (their elements, their artwork). The returned sentinel
// marks the end: put it after the last card shown.
function useProgressive(total: number, step: number, axis: "x" | "y") {
    const [shown, setShown] = useState(step)
    const ref = useRef<HTMLDivElement>(null)
    const more = shown < total
    useEffect(() => {
        const el = ref.current
        if (!more || !el) return
        // The element that scrolls: the carousel, else the page.
        const root = axis === "x" ? el.parentElement : el.closest("#main-scroll")
        const io = new IntersectionObserver(entries => entries.some(e => e.isIntersecting) && setShown(n => n + step), {
            root,
            rootMargin: axis === "x" ? "0px 1500px" : "1500px 0px",
        })
        io.observe(el)
        return () => io.disconnect()
    }, [more, shown, step, axis])
    return { shown, sentinel: more ? ref : null }
}

// cardMinWidth is the narrowest a MediaGrid card gets for a card size (its
// grid columns below), for grids laid out in JavaScript (VirtualGrid).
export function cardMinWidth(size?: string) {
    return size === "sm" ? 130 : size === "lg" ? 200 : 160
}

// cardTextHeight is the height of a card's text under its artwork (title
// on up to two lines, then the year and status line).
export const cardTextHeight = 66

// start: a TV's remote starts on its first card when the page opens.
export function MediaGrid({ children, className, size = "md", start }: { children: React.ReactNode; className?: string; size?: string; start?: boolean }) {
    const cols =
        size === "sm"
            ? "grid-cols-[repeat(auto-fill,minmax(130px,1fr))]"
            : size === "lg"
              ? "grid-cols-[repeat(auto-fill,minmax(200px,1fr))]"
              : "grid-cols-[repeat(auto-fill,minmax(160px,1fr))]"
    const items = Children.toArray(children)
    const { shown, sentinel } = useProgressive(items.length, 60, "y")
    return (
        <div className={cn("relative grid gap-x-4 gap-y-6", cols, className)} data-tv-autofocus={start || undefined}>
            {items.slice(0, shown)}
            {sentinel && <div ref={sentinel} aria-hidden className="pointer-events-none absolute bottom-0 left-0 h-px w-px" />}
        </div>
    )
}

export function MediaCardSkeleton() {
    const classic = useClassic()
    return (
        <div className="flex flex-col gap-2.5">
            <div className={cn("shimmer aspect-[2/3]", classic ? "rounded-lg" : "rounded-[10px]")} />
            <div className="shimmer h-3.5 w-3/4 rounded" />
        </div>
    )
}

export function Carousel({ children, className, itemClassName }: { children: React.ReactNode[]; className?: string; itemClassName?: string }) {
    const ref = useRef<HTMLDivElement>(null)
    const { shown, sentinel } = useProgressive(children.length, 24, "x")
    const [edges, setEdges] = useState({ left: false, right: true })
    const update = () => {
        const el = ref.current
        if (!el) return
        setEdges({ left: el.scrollLeft > 8, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 8 })
    }
    const scroll = (dir: number) => ref.current?.scrollBy({ left: dir * ref.current.clientWidth * 0.85, behavior: "smooth" })
    return (
        <div className={cn("group/carousel relative", className)}>
            <div ref={ref} onScroll={update} className="no-scrollbar -mx-2 flex snap-x scroll-px-2 gap-4 overflow-x-auto scroll-smooth px-2 pt-1 pb-3">
                {children.slice(0, shown).map((c, i) => (
                    // The card's own key: a row that changes order (sorting,
                    // filters) keeps each card's state with its card.
                    <div key={(isValidElement(c) && c.key) || i} className={cn("w-[160px] shrink-0 snap-start", itemClassName)}>
                        {c}
                    </div>
                ))}
                {sentinel && <div ref={sentinel} aria-hidden className="w-px shrink-0" />}
            </div>
            {edges.left && (
                <button
                    aria-label="Scroll left"
                    onClick={() => scroll(-1)}
                    className="absolute top-[38%] -left-3 grid size-9 -translate-y-1/2 place-items-center rounded-full bg-surface-3 text-fg opacity-0 shadow-lg shadow-black/50 transition group-hover/carousel:opacity-100 hover:bg-surface-4"
                >
                    <ChevronLeft className="size-5" />
                </button>
            )}
            {edges.right && children.length > 4 && (
                <button
                    aria-label="Scroll right"
                    onClick={() => scroll(1)}
                    className="absolute top-[38%] -right-3 grid size-9 -translate-y-1/2 place-items-center rounded-full bg-surface-3 text-fg opacity-0 shadow-lg shadow-black/50 transition group-hover/carousel:opacity-100 hover:bg-surface-4"
                >
                    <ChevronRight className="size-5" />
                </button>
            )}
        </div>
    )
}
