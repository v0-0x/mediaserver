import { defaultRangeExtractor, type Range, useVirtualizer } from "@tanstack/react-virtual"
import { Fragment, useCallback, useLayoutEffect, useMemo, useRef, useState } from "react"
import { cn } from "@/lib/utils"

// Long lists (a library of thousands of files) render only the rows on
// screen and a few around them, so their pages stay smooth whatever their
// size. Rows are measured once rendered: they may have any height and change
// it (a row opening its file list). The row holding the focus and its
// neighbours stay rendered even when scrolled away, so typing in it or
// tabbing on works as in a plain list. A row scrolled far away is
// unmounted: what must survive that (open rows, selections) belongs to the
// list's owner, not the row.

type Key = string | number

// The pages' scroll container (App.tsx).
const pageScroller = () => document.getElementById("main-scroll")

// useScrollMargin is where the list starts in its scroller's content (the
// virtualizer counts in the scroller's coordinates). It moves when what's
// above the list changes size: a banner, a carousel loading, filters.
function useScrollMargin(list: React.RefObject<HTMLElement | null>, scroller: () => HTMLElement | null) {
    const [margin, setMargin] = useState(0)
    const update = useCallback(() => {
        const el = list.current
        const sc = scroller()
        if (!el || !sc) return
        const m = Math.round(el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop)
        setMargin(prev => (prev === m ? prev : m))
    }, [list, scroller])
    // After every render: what's above may have changed with it.
    useLayoutEffect(update)
    useLayoutEffect(() => {
        const el = list.current
        const sc = scroller()
        if (!el || !sc) return
        // The scroller's child holding the list changes size with anything in
        // it, above the list too.
        let top: HTMLElement = el
        while (top.parentElement && top.parentElement !== sc) top = top.parentElement
        const ro = new ResizeObserver(update)
        ro.observe(top)
        return () => ro.disconnect()
    }, [list, scroller, update])
    return margin
}

export type VirtualListProps<T> = {
    items: T[]
    getKey: (item: T) => Key
    // A row's height before it's measured (pixels).
    estimateSize: number | ((item: T) => number)
    renderItem: (item: T, index: number) => React.ReactNode
    gap?: number
    overscan?: number
    // The element that scrolls (a dialog's list); the page by default.
    scrollerRef?: React.RefObject<HTMLElement | null>
    className?: string
}

export function VirtualList<T>({ items, getKey, estimateSize, renderItem, gap = 0, overscan = 8, scrollerRef, className }: VirtualListProps<T>) {
    const listRef = useRef<HTMLDivElement>(null)
    const getScroller = useCallback(() => (scrollerRef ? scrollerRef.current : pageScroller()), [scrollerRef])
    const scrollMargin = useScrollMargin(listRef, getScroller)
    const keyOf = useRef(getKey)
    keyOf.current = getKey
    const size = useRef(estimateSize)
    size.current = estimateSize

    // The focused row, by key: it stays rendered (rangeExtractor below).
    const [focusedKey, setFocusedKey] = useState<Key | null>(null)
    const focusedIndex = useMemo(() => (focusedKey === null ? -1 : items.findIndex(it => keyOf.current(it) === focusedKey)), [items, focusedKey])
    const rangeExtractor = useCallback(
        (range: Range) => {
            const out = defaultRangeExtractor(range)
            if (focusedIndex < 0) return out
            // With its neighbours, for Tab and Shift+Tab to have somewhere
            // to go (the browser then scrolls there, and the rows around
            // it are rendered).
            const shown = new Set(out)
            for (let i = Math.max(0, focusedIndex - 2); i <= Math.min(range.count - 1, focusedIndex + 2); i++) shown.add(i)
            return [...shown].sort((a, b) => a - b)
        },
        [focusedIndex],
    )

    const v = useVirtualizer({
        count: items.length,
        getScrollElement: getScroller,
        estimateSize: i => (typeof size.current === "number" ? size.current : size.current(items[i])),
        getItemKey: i => keyOf.current(items[i]),
        rangeExtractor,
        overscan,
        gap,
        scrollMargin,
    })

    return (
        <div
            ref={listRef}
            className={cn("relative w-full", className)}
            style={{ height: v.getTotalSize() }}
            onFocus={e => {
                const row = (e.target as HTMLElement).closest<HTMLElement>("[data-index]")
                const i = row ? Number(row.dataset.index) : -1
                setFocusedKey(i >= 0 && i < items.length ? keyOf.current(items[i]) : null)
            }}
            onBlur={e => {
                if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setFocusedKey(null)
            }}
        >
            {v.getVirtualItems().map(row => (
                <div
                    key={row.key}
                    data-index={row.index}
                    ref={v.measureElement}
                    className="absolute top-0 left-0 w-full"
                    style={{ transform: `translateY(${row.start - v.options.scrollMargin}px)` }}
                >
                    {renderItem(items[row.index], row.index)}
                </div>
            ))}
        </div>
    )
}

// useWidth is an element's width, kept up to date.
function useWidth(ref: React.RefObject<HTMLElement | null>) {
    const [width, setWidth] = useState(0)
    useLayoutEffect(() => {
        const el = ref.current
        if (!el) return
        setWidth(el.clientWidth)
        const ro = new ResizeObserver(() => setWidth(el.clientWidth))
        ro.observe(el)
        return () => ro.disconnect()
    }, [ref])
    return width
}

// VirtualGrid lays cards out as CSS's repeat(auto-fill, minmax(minItemWidth,
// 1fr)) does, a row of them at a time in a VirtualList.
export function VirtualGrid<T>({
    items,
    getKey,
    minItemWidth,
    columnGap = 16,
    rowGap = 24,
    extraHeight,
    renderItem,
}: {
    items: T[]
    getKey: (item: T) => Key
    minItemWidth: number
    columnGap?: number
    rowGap?: number
    // A card's height beyond its width-proportional artwork (a row's
    // estimate before it's measured is width * 1.5 + extraHeight).
    extraHeight: number
    renderItem: (item: T) => React.ReactNode
}) {
    const ref = useRef<HTMLDivElement>(null)
    const width = useWidth(ref)
    const columns = Math.max(1, Math.floor((width + columnGap) / (minItemWidth + columnGap)))
    const rows = useMemo(() => {
        const out: T[][] = []
        for (let i = 0; i < items.length; i += columns) out.push(items.slice(i, i + columns))
        return out
    }, [items, columns])
    const cardWidth = width > 0 ? (width - columnGap * (columns - 1)) / columns : minItemWidth
    return (
        <div ref={ref}>
            <VirtualList
                items={rows}
                // A row is known by its first card, and how many there are a row.
                getKey={row => `${columns}:${getKey(row[0])}`}
                estimateSize={cardWidth * 1.5 + extraHeight}
                gap={rowGap}
                overscan={3}
                renderItem={row => (
                    <div className="grid" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, columnGap }}>
                        {row.map(item => (
                            <Fragment key={getKey(item)}>{renderItem(item)}</Fragment>
                        ))}
                    </div>
                )}
            />
        </div>
    )
}

// cardRow is the look of the rows of one card split over several rows of a
// VirtualList (a folder and its files): the first row has the card's top
// edge, the last its bottom one; line draws a line above a row (as between
// a card's header and its list).
export function cardRow(first: boolean, last: boolean, line = !first) {
    return cn(
        "overflow-hidden border-x border-line bg-surface-1",
        (first || line) && "border-t",
        first && "rounded-t-[0.625rem]",
        last && "rounded-b-[0.625rem] border-b",
    )
}
