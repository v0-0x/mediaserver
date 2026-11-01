import { useEffect, useRef, useState } from "react"
import type { Holiday } from "@/lib/themes"
import { cn } from "@/lib/utils"

// The holiday themes' decorations (lib/themes.ts), around the pages:
// - HolidayBackdrop: the page panel's background, with the holiday's art
//   behind the pages (Halloween's moon, a cherry branch...), still while
//   they scroll. Pages with artwork (Home's banner) cover it.
// - HolidayGarland: what hangs at the panel's top edge, over the pages, so
//   every page shows it the same (Christmas lights, summer lanterns,
//   Halloween's cobweb in the corner). Nothing can be clicked on either.

export function HolidayBackdrop({ holiday, phone }: { holiday: Holiday | null; phone?: boolean }) {
    return (
        <div aria-hidden className={cn("pointer-events-none absolute overflow-hidden bg-bg", phone ? "inset-0" : "inset-y-2 right-2 left-0 rounded-[14px]")}>
            {holiday && (
                <>
                    {/* The holiday's light, from the top. */}
                    <div className="absolute inset-x-0 top-0 h-72" style={{ background: "radial-gradient(1100px 280px at 50% -120px, color-mix(in oklab, var(--brand) 12%, transparent), transparent)" }} />
                    {ART[holiday.id]}
                </>
            )}
        </div>
    )
}

// The lights on their wire, as wide as the panel: a dip between each two
// hooks, a bulb at the bottom of each dip and one on each hook. animate:
// twinkling and swaying (not with the holiday's animation off, nor behind a
// video).
export function HolidayGarland({ holiday, animate }: { holiday: Holiday | null; animate: boolean }) {
    const ref = useRef<HTMLDivElement>(null)
    const [width, setWidth] = useState(0)
    const kind = holiday?.id === "christmas" ? "lights" : holiday?.id === "summer" ? "lanterns" : null
    useEffect(() => {
        const el = ref.current
        if (!el || !kind) return
        const ro = new ResizeObserver(() => setWidth(el.clientWidth))
        ro.observe(el)
        setWidth(el.clientWidth)
        return () => ro.disconnect()
    }, [kind])
    if (holiday?.id === "halloween") return <Cobweb />
    if (!kind) return null
    return (
        <div ref={ref} aria-hidden className="pointer-events-none absolute top-2 right-2 left-0 z-[5] h-16 overflow-hidden rounded-t-[14px]">
            {width > 0 && (kind === "lights" ? <Lights width={width} animate={animate} /> : <Lanterns width={width} animate={animate} />)}
        </div>
    )
}

// A cobweb in the panel's top left corner, its spider down a thread in the
// pages' margin.
function Cobweb() {
    return (
        <div aria-hidden className="pointer-events-none absolute top-2 left-0 z-[5] size-[170px] overflow-hidden rounded-tl-[14px]">
            <svg width="170" height="170">
                <path d={cobweb(120)} fill="none" stroke="#e9d5ff" strokeOpacity="0.2" strokeWidth="0.9" />
                <line x1="12" y1="12" x2="12" y2="136" stroke="#e9d5ff" strokeOpacity="0.24" strokeWidth="0.8" />
                <g transform="translate(12 144)" fill="#a78bfa" opacity="0.6">
                    <ellipse rx="3.5" ry="4.5" />
                    <circle cy="-5.5" r="2.4" />
                    <path d="M-3-1-8-5M-3 1-9 1M-3 3-8 7M3-1 8-5M3 1 9 1M3 3 8 7" stroke="#a78bfa" strokeWidth="1" />
                </g>
            </svg>
        </div>
    )
}

const BULBS = ["#ef4444", "#22c55e", "#facc15", "#60a5fa", "#f97316"]

function Lights({ width, animate }: { width: number; animate: boolean }) {
    const span = 116
    const n = Math.max(2, Math.round(width / span))
    const step = width / n
    const sag = 10
    let wire = `M0 2`
    const bulbs: { x: number; y: number; c: string; d: number }[] = []
    for (let i = 0; i < n; i++) {
        const x0 = i * step
        wire += ` Q${x0 + step / 2} ${2 + sag * 2} ${x0 + step} 2`
        // On the wire: quarter way, the bottom, three quarters.
        for (const [k, y] of [
            [0.25, 2 + sag * 0.75],
            [0.5, 2 + sag],
            [0.75, 2 + sag * 0.75],
        ])
            bulbs.push({ x: x0 + step * k, y, c: BULBS[bulbs.length % BULBS.length], d: (bulbs.length * 0.37) % 2.4 })
    }
    return (
        <svg width={width} height={64} className="absolute top-0 left-0">
            <defs>
                {BULBS.map(c => (
                    <radialGradient key={c} id={`bulb-${c.slice(1)}`}>
                        <stop offset="0" stopColor={c} stopOpacity="0.55" />
                        <stop offset="1" stopColor={c} stopOpacity="0" />
                    </radialGradient>
                ))}
            </defs>
            <path d={wire} fill="none" stroke="#1f2a1f" strokeWidth="1.6" />
            {bulbs.map((b, i) => (
                <g key={i} transform={`translate(${b.x} ${b.y})`}>
                    <circle r="11" cy="7" fill={`url(#bulb-${b.c.slice(1)})`} className={animate ? "aniko-twinkle" : undefined} style={{ animationDelay: `${b.d}s` }} />
                    <rect x="-1.8" y="-1" width="3.6" height="3.4" rx="0.8" fill="#3f3f46" />
                    <ellipse cy="6.8" rx="2.9" ry="4.4" fill={b.c} className={animate ? "aniko-twinkle-bulb" : undefined} style={{ animationDelay: `${b.d}s` }} />
                    <ellipse cx="-1" cy="5.2" rx="0.7" ry="1.5" fill="#fff" opacity="0.55" />
                </g>
            ))}
        </svg>
    )
}

function Lanterns({ width, animate }: { width: number; animate: boolean }) {
    const span = 150
    const n = Math.max(2, Math.round(width / span))
    const step = width / n
    const sag = 7
    let wire = `M0 2`
    const lanterns: { x: number; y: number; red: boolean; d: number }[] = []
    for (let i = 0; i < n; i++) {
        const x0 = i * step
        wire += ` Q${x0 + step / 2} ${2 + sag * 2} ${x0 + step} 2`
        lanterns.push({ x: x0 + step / 2, y: 2 + sag, red: i % 3 !== 2, d: (i * 0.6) % 3 })
    }
    return (
        <svg width={width} height={64} className="absolute top-0 left-0" style={{ overflow: "visible" }}>
            <defs>
                <radialGradient id="lantern-glow">
                    <stop offset="0" stopColor="#fdba74" stopOpacity="0.45" />
                    <stop offset="1" stopColor="#fdba74" stopOpacity="0" />
                </radialGradient>
                <linearGradient id="lantern-red" x1="0" x2="1">
                    <stop offset="0" stopColor="#b91c1c" />
                    <stop offset="0.5" stopColor="#ef4444" />
                    <stop offset="1" stopColor="#b91c1c" />
                </linearGradient>
                <linearGradient id="lantern-white" x1="0" x2="1">
                    <stop offset="0" stopColor="#e7e5e4" />
                    <stop offset="0.5" stopColor="#fffbeb" />
                    <stop offset="1" stopColor="#e7e5e4" />
                </linearGradient>
            </defs>
            <path d={wire} fill="none" stroke="#3f3f46" strokeWidth="1.4" />
            {lanterns.map((l, i) => (
                // Swaying from its hook.
                <g key={i} transform={`translate(${l.x} ${l.y}) scale(0.78)`}>
                    <g className={animate ? "aniko-sway" : undefined} style={{ animationDelay: `${-l.d}s` }}>
                        <circle cy="24" r="26" fill="url(#lantern-glow)" />
                        <line y1="0" y2="6" stroke="#52525b" strokeWidth="1.2" />
                        <rect x="-6" y="5" width="12" height="4" rx="1" fill="#27272a" />
                        <rect x="-11" y="8" width="22" height="30" rx="11" fill={l.red ? "url(#lantern-red)" : "url(#lantern-white)"} />
                        {[15, 22, 29].map(y => (
                            <path key={y} d={`M-10.5 ${y} Q0 ${y + 1.5} 10.5 ${y}`} fill="none" stroke={l.red ? "#7f1d1d" : "#a8a29e"} strokeWidth="0.8" opacity="0.7" />
                        ))}
                        <rect x="-6" y="37" width="12" height="4" rx="1" fill="#27272a" />
                        <path d="M0 41v7M-2 48h4" stroke={l.red ? "#fca5a5" : "#fde68a"} strokeWidth="1" />
                    </g>
                </g>
            ))}
        </svg>
    )
}

// A cobweb in a corner: threads out from it, rings between them that sag.
function cobweb(size: number) {
    const angles = [0, 14, 29, 45, 61, 76, 90].map(a => (a * Math.PI) / 180)
    let d = ""
    for (const a of angles) d += `M0 0L${Math.cos(a) * size} ${Math.sin(a) * size}`
    for (const r of [0.22, 0.4, 0.58, 0.76, 0.94].map(k => k * size)) {
        for (let i = 0; i < angles.length - 1; i++) {
            const [a, b] = [angles[i], angles[i + 1]]
            const m = (a + b) / 2
            d += `M${Math.cos(a) * r} ${Math.sin(a) * r}Q${Math.cos(m) * r * 0.86} ${Math.sin(m) * r * 0.86} ${Math.cos(b) * r} ${Math.sin(b) * r}`
        }
    }
    return d
}

function Blossom({ x, y, r, rot = 0 }: { x: number; y: number; r: number; rot?: number }) {
    return (
        <g transform={`translate(${x} ${y}) rotate(${rot})`}>
            {[0, 72, 144, 216, 288].map(a => (
                <path key={a} transform={`rotate(${a})`} d={`M0 0C${-r * 0.55} ${-r * 0.3} ${-r * 0.45} ${-r} ${-r * 0.1} ${-r}L0 ${-r * 0.82}L${r * 0.1} ${-r}C${r * 0.45} ${-r} ${r * 0.55} ${-r * 0.3} 0 0`} fill="url(#petal)" />
            ))}
            <circle r={r * 0.16} fill="#fde047" />
        </g>
    )
}

function Heart({ x, y, s, o, blur }: { x: number; y: number; s: number; o: number; blur: number }) {
    return (
        <path
            transform={`translate(${x} ${y}) scale(${s / 32})`}
            d="M16 28C14 26 3 19 3 11a6.5 6.5 0 0 1 13-1 6.5 6.5 0 0 1 13-1c0 8-11 15-13 17Z"
            fill="#f472b6"
            opacity={o}
            filter={blur ? `url(#soft-${blur})` : undefined}
        />
    )
}

// A few stars, always in the same places.
const STARS = Array.from({ length: 70 }, (_, i) => {
    const r = (n: number) => ((Math.sin(i * 127.1 + n * 311.7) * 43758.5453) % 1 + 1) % 1
    return { x: r(1) * 100, y: r(2) * 38, s: 0.5 + r(3) * 1.3, o: 0.25 + r(4) * 0.55 }
})

const ART: Record<string, React.ReactNode> = {
    halloween: (
        <>
            {/* A pale moon on the right, below the pages' header buttons, in an
                orange haze. */}
            <svg className="absolute top-28 right-4" width="260" height="220" viewBox="0 0 260 220">
                <defs>
                    <radialGradient id="moon-haze">
                        <stop offset="0" stopColor="#fdba74" stopOpacity="0.16" />
                        <stop offset="1" stopColor="#fdba74" stopOpacity="0" />
                    </radialGradient>
                    <radialGradient id="moon" cx="0.4" cy="0.38">
                        <stop offset="0" stopColor="#fffbeb" stopOpacity="0.26" />
                        <stop offset="1" stopColor="#fde68a" stopOpacity="0.14" />
                    </radialGradient>
                </defs>
                <circle cx="150" cy="95" r="110" fill="url(#moon-haze)" />
                <circle cx="150" cy="95" r="44" fill="url(#moon)" />
                <circle cx="136" cy="84" r="8" fill="#000" opacity="0.06" />
                <circle cx="163" cy="108" r="11" fill="#000" opacity="0.05" />
                <circle cx="158" cy="78" r="5" fill="#000" opacity="0.06" />
            </svg>
        </>
    ),
    sakura: (
        // A cherry branch reaching in from the right, below the pages' header
        // buttons.
        <svg className="absolute top-24 right-0" width="360" height="230" viewBox="0 0 360 230" opacity="0.55">
            <defs>
                <linearGradient id="petal" x1="0" y1="1" x2="0" y2="0">
                    <stop offset="0" stopColor="#f472b6" />
                    <stop offset="1" stopColor="#fdf2f8" />
                </linearGradient>
            </defs>
            <path d="M362 18C300 30 250 60 196 66S110 96 70 130M250 52C238 84 240 112 226 140M196 66C180 46 160 40 140 42M120 104C112 126 116 150 104 170" fill="none" stroke="#5b3a37" strokeWidth="4" strokeLinecap="round" />
            <path d="M226 140c-6 16-4 30-12 42M140 42c-14-2-26 6-36 4" fill="none" stroke="#5b3a37" strokeWidth="2.4" strokeLinecap="round" />
            <Blossom x={300} y={32} r={13} rot={10} />
            <Blossom x={262} y={50} r={10} rot={40} />
            <Blossom x={232} y={96} r={12} rot={-20} />
            <Blossom x={214} y={180} r={10} rot={25} />
            <Blossom x={190} y={70} r={14} rot={0} />
            <Blossom x={140} y={42} r={10} rot={30} />
            <Blossom x={106} y={46} r={8} rot={-10} />
            <Blossom x={120} y={104} r={12} rot={15} />
            <Blossom x={104} y={168} r={9} rot={50} />
            <Blossom x={74} y={128} r={11} rot={-30} />
            {[
                [330, 22],
                [244, 120],
                [160, 86],
                [90, 150],
            ].map(([x, y]) => (
                <ellipse key={x} cx={x} cy={y} rx="3.5" ry="5" fill="#f472b6" />
            ))}
        </svg>
    ),
    valentine: (
        // Soft hearts, out of focus, in two corners.
        <svg className="absolute inset-0 size-full">
            <defs>
                {[2, 5].map(b => (
                    <filter key={b} id={`soft-${b}`} x="-50%" y="-50%" width="200%" height="200%">
                        <feGaussianBlur stdDeviation={b} />
                    </filter>
                ))}
            </defs>
            <svg x="100%" y="0" overflow="visible">
                <Heart x={-180} y={24} s={60} o={0.1} blur={5} />
                <Heart x={-110} y={90} s={34} o={0.14} blur={2} />
                <Heart x={-240} y={110} s={22} o={0.12} blur={0} />
                <Heart x={-70} y={20} s={18} o={0.16} blur={0} />
            </svg>
            <svg x="0" y="100%" overflow="visible">
                <Heart x={30} y={-160} s={70} o={0.08} blur={5} />
                <Heart x={120} y={-90} s={28} o={0.12} blur={2} />
                <Heart x={20} y={-60} s={20} o={0.14} blur={0} />
            </svg>
        </svg>
    ),
    newyear: (
        // A night sky with gold in it.
        <svg className="absolute inset-x-0 top-0 h-[38%] w-full" preserveAspectRatio="none">
            {STARS.map((s, i) => (
                <circle key={i} cx={`${s.x}%`} cy={`${(s.y / 38) * 100}%`} r={s.s} fill={i % 5 === 0 ? "#fde68a" : "#e0e7ff"} opacity={s.o * 0.7} />
            ))}
        </svg>
    ),
    summer: (
        <svg className="absolute inset-x-0 top-0 h-[38%] w-full" preserveAspectRatio="none">
            {STARS.slice(0, 40).map((s, i) => (
                <circle key={i} cx={`${s.x}%`} cy={`${(s.y / 38) * 100}%`} r={s.s * 0.8} fill="#ccfbf1" opacity={s.o * 0.5} />
            ))}
        </svg>
    ),
    christmas: (
        // Snow settled along the bottom.
        <svg className="absolute inset-x-0 bottom-0 h-16 w-full" viewBox="0 0 1000 64" preserveAspectRatio="none">
            <path d="M0 40C80 30 140 46 230 38S380 26 470 36 640 48 730 38 880 28 1000 36V64H0Z" fill="#f8fafc" opacity="0.07" />
            <path d="M0 50C100 42 170 56 270 50S420 40 520 48 700 58 800 50 920 44 1000 48V64H0Z" fill="#f8fafc" opacity="0.08" />
        </svg>
    ),
}
