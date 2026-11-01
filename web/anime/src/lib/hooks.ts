import { useEffect, useState, useSyncExternalStore } from "react"

// Whether a CSS media query matches, e.g. "(min-width: 1024px)"; follows
// changes (the window resized).
export function useMediaQuery(query: string) {
    return useSyncExternalStore(
        notify => {
            const m = window.matchMedia(query)
            m.addEventListener("change", notify)
            return () => m.removeEventListener("change", notify)
        },
        () => window.matchMedia(query).matches,
    )
}

// useState that survives reloads (per browser). Storage can be unavailable
// (private windows, blocked site data), so every access is guarded.
export function usePersisted<T>(key: string, initial: T) {
    const [v, setV] = useState<T>(() => {
        try {
            const raw = localStorage.getItem(key)
            return raw ? (JSON.parse(raw) as T) : initial
        } catch {
            return initial
        }
    })
    useEffect(() => {
        try {
            localStorage.setItem(key, JSON.stringify(v))
        } catch {
            /* ignore */
        }
    }, [key, v])
    return [v, setV] as const
}

// A list that scrolls (a menu taller than the window): it fades out at the
// bottom while there's more below, so it isn't taken for all of it. Gives
// the ref and the style to put on it.
export function useFadeBelow() {
    const [el, setEl] = useState<HTMLElement | null>(null)
    const [more, setMore] = useState(false)
    useEffect(() => {
        if (!el) return
        const check = () => setMore(el.scrollHeight > el.clientHeight + 2 && el.scrollTop + el.clientHeight < el.scrollHeight - 2)
        const ro = new ResizeObserver(check)
        ro.observe(el)
        for (const child of el.children) ro.observe(child)
        el.addEventListener("scroll", check, { passive: true })
        check()
        return () => {
            ro.disconnect()
            el.removeEventListener("scroll", check)
        }
    }, [el])
    const fade = "linear-gradient(to bottom, #000 calc(100% - 40px), transparent)"
    return { ref: setEl, style: more ? { maskImage: fade, WebkitMaskImage: fade } : undefined }
}
