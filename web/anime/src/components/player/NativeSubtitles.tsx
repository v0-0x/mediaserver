import { useEffect, useState } from "react"
import { subStyleVars, type SubStyle } from "@/lib/subtitles"
import type { Cue } from "@/lib/vtt"

// Subtitles over Android's own player (lib/nativeVideo): the page's video
// element, which draws them otherwise, has no video of its own then. Drawn
// here as it would: the cues showing at the video's time (moved by delay),
// in this device's size, background and position (index.css .native-cue);
// above the controls while they show (lifted).
export function NativeSubtitles({
    video,
    cues,
    base,
    delay,
    style,
    lifted,
}: {
    video: React.RefObject<HTMLVideoElement | null>
    cues: Cue[]
    base: number
    delay: number
    style: SubStyle
    lifted: boolean
}) {
    const [text, setText] = useState("")
    useEffect(() => {
        if (!cues.length) {
            setText("")
            return
        }
        const tick = () => {
            const v = video.current
            if (!v) return
            const t = v.currentTime
            const showing = cues.filter(c => t >= c.start - base + delay && t < c.end - base + delay)
            setText(showing.map(c => c.text).join("\n"))
        }
        tick()
        const timer = setInterval(tick, 100)
        return () => clearInterval(timer)
    }, [video, cues, base, delay])
    if (!text) return null
    return (
        <div
            aria-live="off"
            className="pointer-events-none absolute inset-x-0 px-[6%] text-center transition-[bottom] duration-200"
            style={{ ...subStyleVars(style), bottom: lifted ? (style.position === "raised" ? "26%" : "20%") : style.position === "raised" ? "16%" : "6%" }}
        >
            <span className="native-cue" dangerouslySetInnerHTML={{ __html: cueHtml(text) }} />
        </div>
    )
}

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: "\u00a0", lrm: "\u200e", rlm: "\u200f" }
const escape = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")

// A cue's text as HTML: its italics, bold and underlines, nothing else (any
// other tag dropped, as the video element does with them).
export function cueHtml(text: string): string {
    return text
        .split(/(<\/?[ibu]>)/i)
        .map((part, i) => {
            if (i % 2) return part.toLowerCase()
            const plain = part.replace(/<[^>]*>/g, "").replace(/&(#\d+|#x[0-9a-f]+|[a-z]+);/gi, (m, e: string) => {
                if (e[0] === "#") {
                    const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10)
                    return Number.isFinite(n) && n > 0 && n < 0x110000 ? String.fromCodePoint(n) : m
                }
                return ENTITIES[e.toLowerCase()] ?? m
            })
            return escape(plain)
        })
        .join("")
        .replace(/\n/g, "<br>")
}
