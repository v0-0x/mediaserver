import { useEffect, useRef } from "react"
import { useLocation } from "react-router-dom"
import { makeScene } from "@/lib/effects"
import { makeScene as makeClassicScene } from "@/lib/effects-classic"
import { useMediaQuery } from "@/lib/hooks"
import { playerStore, useStore } from "@/lib/store"
import { useClassic, type Effect } from "@/lib/themes"
import { tv } from "@/lib/tv"

// The holiday theme's animation over the pages (snow, bats...): never over a
// video or the manga reader, stopped while the window is hidden, and not
// with Reduce motion (this device's or Aniko's).
export function HolidayEffects({ effect, reduceMotion }: { effect: Effect | null; reduceMotion: boolean }) {
    const canvasRef = useRef<HTMLCanvasElement>(null)
    const playing = useStore(playerStore, p => !!p)
    const { pathname } = useLocation()
    const systemReduce = useMediaQuery("(prefers-reduced-motion: reduce)")
    const on = !!effect && !reduceMotion && !systemReduce && !playing && pathname !== "/manga/read"
    // The classic look's are the ones from before 2.0.
    const classic = useClassic()

    useEffect(() => {
        const canvas = canvasRef.current
        if (!on || !effect || !canvas) return
        const ctx = canvas.getContext("2d")
        if (!ctx) return
        // A TV is slower at it: fewer, at its own pixels.
        const density = tv ? 0.45 : 1
        const dpr = Math.min(window.devicePixelRatio || 1, tv ? 1 : 1.5)
        let w = window.innerWidth
        let h = window.innerHeight
        const scene = (classic ? makeClassicScene : makeScene)(effect, w, h, density)
        const size = () => {
            w = window.innerWidth
            h = window.innerHeight
            canvas.width = Math.round(w * dpr)
            canvas.height = Math.round(h * dpr)
            scene.resize(w, h)
        }
        size()
        let raf = 0
        let last = 0
        // 30 frames a second is plenty for them, and half the work of 60.
        const frame = (now: number) => {
            raf = requestAnimationFrame(frame)
            if (now - last < 1000 / 31) return
            // Back after a pause (hidden, a long frame): no jump.
            const dt = last ? Math.min(0.1, (now - last) / 1000) : 0
            last = now
            scene.step(dt)
            ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
            ctx.clearRect(0, 0, w, h)
            scene.draw(ctx)
        }
        const start = () => {
            if (!raf && !document.hidden) {
                last = 0
                raf = requestAnimationFrame(frame)
            }
        }
        const stop = () => {
            cancelAnimationFrame(raf)
            raf = 0
        }
        const onVisible = () => (document.hidden ? stop() : start())
        start()
        window.addEventListener("resize", size)
        document.addEventListener("visibilitychange", onVisible)
        return () => {
            stop()
            window.removeEventListener("resize", size)
            document.removeEventListener("visibilitychange", onVisible)
            ctx.clearRect(0, 0, canvas.width, canvas.height)
        }
    }, [on, effect, classic])

    if (!on) return null
    return <canvas ref={canvasRef} aria-hidden className="pointer-events-none fixed inset-0 z-[45] size-full" />
}
