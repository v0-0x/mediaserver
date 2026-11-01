// The holiday themes' animations (lib/themes.ts), drawn on a canvas over the
// pages: snow, bats, hearts, cherry blossom petals, fireflies and fireworks.
// Each effect makes its particles and moves them by the time passed (dt, in
// seconds), so a slow frame doesn't slow them down. What looks soft or glows
// is drawn once into a small picture (a sprite) and stamped: much cheaper
// than gradients and blurs for each particle, each frame.

import type { Effect } from "./themes"

export type Scene = { step: (dt: number) => void; draw: (ctx: CanvasRenderingContext2D) => void; resize: (w: number, h: number) => void }

const rand = (a: number, b: number) => a + Math.random() * (b - a)
const pick = <T>(xs: T[]): T => xs[Math.floor(Math.random() * xs.length)]
const TAU = Math.PI * 2

// density: 1 on a computer, less on a TV (it's slower at drawing them).
export function makeScene(effect: Effect, w: number, h: number, density = 1): Scene {
    const scene = { snow, bats, hearts, petals, fireworks, fireflies }[effect](density)
    scene.resize(w, h)
    return scene
}

// How many for a screen this big: a 1920×1080 one gets about max.
const count = (w: number, h: number, max: number, density: number) => Math.max(4, Math.round(Math.min(1.2, (w * h) / (1920 * 1080)) * max * density))

// A small canvas drawn once (size × size, drawn around its middle).
function sprite(size: number, draw: (ctx: CanvasRenderingContext2D, r: number) => void) {
    const c = document.createElement("canvas")
    c.width = c.height = size
    const ctx = c.getContext("2d")!
    ctx.translate(size / 2, size / 2)
    draw(ctx, size / 2)
    return c
}

// A soft round glow of a color, brightest in the middle.
function glow(color: string, size = 64, core = 0.15) {
    return sprite(size, (ctx, r) => {
        const g = ctx.createRadialGradient(0, 0, 0, 0, 0, r)
        g.addColorStop(0, "#ffffff")
        g.addColorStop(core, color)
        g.addColorStop(1, "transparent")
        ctx.fillStyle = g
        ctx.fillRect(-r, -r, r * 2, r * 2)
    })
}

// The wind: a slow, uneven back and forth (-1.3…1.3), gusts now and then.
const wind = (t: number) => Math.sin(t * 0.13) * 0.6 + Math.sin(t * 0.37 + 1.3) * 0.3 + Math.sin(t * 0.071 + 4) * 0.4

// Stamps sprite s at x, y, size px wide, turned by rot.
function stamp(ctx: CanvasRenderingContext2D, s: HTMLCanvasElement, x: number, y: number, size: number, rot = 0, sx = 1, sy = 1) {
    if (!rot && sx === 1 && sy === 1) {
        ctx.drawImage(s, x - size / 2, y - size / 2, size, size)
        return
    }
    ctx.save()
    ctx.translate(x, y)
    if (rot) ctx.rotate(rot)
    ctx.scale(sx, sy)
    ctx.drawImage(s, -size / 2, -size / 2, size, size)
    ctx.restore()
}

// ------------------------------------------------------------------ snow
// Three depths: far flakes small, slow and faint; near ones big, quicker,
// some of them real six-armed crystals turning as they fall. All drift with
// the wind, the near ones most.
function snow(density: number): Scene {
    type Flake = { x: number; y: number; size: number; vy: number; depth: number; sway: number; phase: number; a: number; rot: number; vr: number; crystal: boolean }
    const soft = sprite(32, (ctx, r) => {
        const g = ctx.createRadialGradient(0, 0, 0, 0, 0, r)
        g.addColorStop(0, "rgba(255,255,255,1)")
        g.addColorStop(0.35, "rgba(255,255,255,0.85)")
        g.addColorStop(1, "rgba(255,255,255,0)")
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(0, 0, r, 0, TAU)
        ctx.fill()
    })
    const crystal = sprite(64, (ctx, r) => {
        ctx.shadowColor = "rgba(200,225,255,0.9)"
        ctx.shadowBlur = 6
        ctx.strokeStyle = "#ffffff"
        ctx.lineCap = "round"
        ctx.lineWidth = r * 0.09
        for (let i = 0; i < 6; i++) {
            ctx.save()
            ctx.rotate((i * TAU) / 6)
            ctx.beginPath()
            ctx.moveTo(0, 0)
            ctx.lineTo(0, -r * 0.82)
            // Two pairs of branches along each arm.
            for (const [at, len] of [
                [0.42, 0.24],
                [0.64, 0.17],
            ]) {
                ctx.moveTo(0, -r * at)
                ctx.lineTo(-r * len, -r * (at + len))
                ctx.moveTo(0, -r * at)
                ctx.lineTo(r * len, -r * (at + len))
            }
            ctx.stroke()
            ctx.restore()
        }
    })
    let w = 0
    let h = 0
    let flakes: Flake[] = []
    let t = 0
    const make = (y: number): Flake => {
        const depth = Math.random() < 0.5 ? 0 : Math.random() < 0.7 ? 1 : 2
        const isCrystal = depth === 2 && Math.random() < 0.45
        const size = depth === 0 ? rand(2, 4) : depth === 1 ? rand(4, 7) : isCrystal ? rand(12, 20) : rand(7, 11)
        return {
            x: rand(-40, w + 40),
            y,
            size,
            vy: depth === 0 ? rand(14, 24) : depth === 1 ? rand(26, 40) : rand(42, 62),
            depth,
            sway: rand(6, 22) * (depth + 1) * 0.6,
            phase: rand(0, TAU),
            a: depth === 0 ? rand(0.3, 0.5) : depth === 1 ? rand(0.55, 0.8) : rand(0.75, 0.95),
            rot: rand(0, TAU),
            vr: rand(-0.8, 0.8),
            crystal: isCrystal,
        }
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 150, density)
            while (flakes.length < n) flakes.push(make(rand(-h, h)))
            flakes = flakes.slice(0, n)
        },
        step(dt) {
            t += dt
            const gust = wind(t)
            for (const f of flakes) {
                f.y += f.vy * dt
                f.x += gust * (10 + f.depth * 16) * dt
                f.rot += f.vr * dt
                if (f.y > h + 20) Object.assign(f, make(-20))
                if (f.x > w + 50) f.x = -40
                if (f.x < -50) f.x = w + 40
            }
        },
        draw(ctx) {
            for (const f of flakes) {
                ctx.globalAlpha = f.a
                const x = f.x + Math.sin(t * 0.7 + f.phase) * f.sway
                if (f.crystal) stamp(ctx, crystal, x, f.y, f.size, f.rot)
                else stamp(ctx, soft, x, f.y, f.size)
            }
            ctx.globalAlpha = 1
        },
    }
}

// ------------------------------------------------------------------ bats
// Bats with scalloped wings, flapping, swooping across: a few at a time,
// some farther away (smaller, fainter, slower).
function bats(density: number): Scene {
    type Bat = { x: number; y: number; vx: number; size: number; flap: number; bob: number; swoop: number; phase: number; wait: number; a: number }
    let w = 0
    let h = 0
    let list: Bat[] = []
    let t = 0
    const make = (first = false): Bat => {
        const near = Math.random()
        const right = Math.random() < 0.5
        return {
            x: right ? w + 60 : -60,
            y: rand(h * 0.05, h * 0.6),
            vx: (right ? -1 : 1) * (60 + near * 120),
            size: 9 + near * 20,
            flap: rand(6, 9) + (1 - near) * 3,
            bob: rand(8, 26),
            swoop: rand(30, 90),
            phase: rand(0, TAU),
            wait: first ? rand(0, 6) : rand(1.5, 9),
            a: 0.55 + near * 0.4,
        }
    }
    // A wing out to the side (+1 right, -1 left), its tip raised by lift
    // (-1 down … 1 up): a curved top edge, three scallops underneath.
    const wing = (ctx: CanvasRenderingContext2D, s: number, side: number, lift: number) => {
        const tipX = side * s * (0.95 - Math.abs(lift) * 0.12)
        const tipY = -lift * s * 0.55
        ctx.moveTo(side * s * 0.1, -s * 0.12)
        ctx.bezierCurveTo(side * s * 0.35, -s * 0.2 + tipY * 0.6, side * s * 0.7, tipY - s * 0.18, tipX, tipY)
        // Scallops back to the body.
        const pts = 3
        let px = tipX
        let py = tipY
        for (let i = 1; i <= pts; i++) {
            const k = i / pts
            const nx = side * s * (0.95 - k * 0.85) * (1 - Math.abs(lift) * 0.1)
            const ny = tipY * (1 - k) + s * 0.12 * k
            ctx.quadraticCurveTo((px + nx) / 2, (py + ny) / 2 - s * 0.05 - s * 0.1 * (1 - Math.abs(lift)), nx, ny)
            px = nx
            py = ny
        }
        ctx.closePath()
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = Math.max(3, Math.round(7 * density))
            while (list.length < n) list.push(make(true))
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const b of list) {
                if (b.wait > 0) {
                    b.wait -= dt
                    continue
                }
                b.x += b.vx * dt
                if (b.x < -80 || b.x > w + 80) Object.assign(b, make())
            }
        },
        draw(ctx) {
            for (const b of list) {
                if (b.wait > 0) continue
                // Bobbing, and now and then a swoop down and up.
                const y = b.y + Math.sin(t * 1.7 + b.phase) * b.bob + Math.max(0, Math.sin(t * 0.45 + b.phase)) ** 3 * b.swoop
                const lift = Math.sin(t * b.flap * Math.PI + b.phase)
                const s = b.size
                ctx.save()
                ctx.translate(b.x, y)
                if (b.vx < 0) ctx.scale(-1, 1)
                ctx.rotate(Math.sin(t * 0.9 + b.phase) * 0.12)
                ctx.globalAlpha = b.a
                ctx.fillStyle = "#7d6a9c"
                ctx.beginPath()
                wing(ctx, s, -1, lift)
                wing(ctx, s, 1, lift)
                ctx.fill()
                ctx.fillStyle = "#5e4f78"
                ctx.beginPath()
                ctx.ellipse(0, s * 0.02, s * 0.14, s * 0.26, 0, 0, TAU)
                // Head and ears.
                ctx.moveTo(-s * 0.1, -s * 0.18)
                ctx.lineTo(-s * 0.08, -s * 0.36)
                ctx.lineTo(-s * 0.02, -s * 0.24)
                ctx.lineTo(s * 0.02, -s * 0.24)
                ctx.lineTo(s * 0.08, -s * 0.36)
                ctx.lineTo(s * 0.1, -s * 0.18)
                ctx.fill()
                // Its eyes catch the light.
                ctx.fillStyle = "#fdba74"
                ctx.globalAlpha = b.a * 0.9
                ctx.fillRect(-s * 0.06, -s * 0.2, s * 0.035, s * 0.035)
                ctx.fillRect(s * 0.025, -s * 0.2, s * 0.035, s * 0.035)
                ctx.restore()
            }
            ctx.globalAlpha = 1
        },
    }
}

// ------------------------------------------------------------------ hearts
// Glossy hearts rising and swaying, with a glow, and a few sparkles that
// twinkle near them.
function heartPath(ctx: CanvasRenderingContext2D, s: number) {
    ctx.beginPath()
    ctx.moveTo(0, s * 0.32)
    ctx.bezierCurveTo(-s * 0.05, s * 0.27, -s * 0.5, 0, -s * 0.5, -s * 0.24)
    ctx.bezierCurveTo(-s * 0.5, -s * 0.5, -s * 0.14, -s * 0.6, 0, -s * 0.32)
    ctx.bezierCurveTo(s * 0.14, -s * 0.6, s * 0.5, -s * 0.5, s * 0.5, -s * 0.24)
    ctx.bezierCurveTo(s * 0.5, 0, s * 0.05, s * 0.27, 0, s * 0.32)
    ctx.closePath()
}

function hearts(density: number): Scene {
    type Heart = { x: number; y: number; size: number; vy: number; sway: number; phase: number; s: HTMLCanvasElement; a: number }
    type Spark = { x: number; y: number; life: number; max: number; size: number }
    const tones: [string, string][] = [
        ["#ffb3c7", "#f2557a"],
        ["#fda4af", "#e11d48"],
        ["#fbcfe8", "#ec4899"],
    ]
    const sprites = tones.map(([top, bottom]) =>
        sprite(96, (ctx, r) => {
            const s = r * 1.35
            ctx.shadowColor = bottom
            ctx.shadowBlur = r * 0.35
            const g = ctx.createLinearGradient(0, -s * 0.6, 0, s * 0.35)
            g.addColorStop(0, top)
            g.addColorStop(1, bottom)
            ctx.fillStyle = g
            heartPath(ctx, s)
            ctx.fill()
            // A shine on its top left.
            ctx.shadowBlur = 0
            ctx.fillStyle = "rgba(255,255,255,0.45)"
            ctx.beginPath()
            ctx.ellipse(-s * 0.24, -s * 0.3, s * 0.11, s * 0.06, -0.6, 0, TAU)
            ctx.fill()
        }),
    )
    const star = sprite(32, (ctx, r) => {
        ctx.fillStyle = "#ffffff"
        ctx.shadowColor = "#fbcfe8"
        ctx.shadowBlur = r * 0.4
        ctx.beginPath()
        ctx.moveTo(0, -r * 0.8)
        ctx.quadraticCurveTo(0, 0, r * 0.8, 0)
        ctx.quadraticCurveTo(0, 0, 0, r * 0.8)
        ctx.quadraticCurveTo(0, 0, -r * 0.8, 0)
        ctx.quadraticCurveTo(0, 0, 0, -r * 0.8)
        ctx.fill()
    })
    let w = 0
    let h = 0
    let list: Heart[] = []
    const sparks: Spark[] = []
    let t = 0
    const make = (y: number): Heart => {
        const near = Math.random()
        return { x: rand(0, w), y, size: 12 + near * 22, vy: 14 + near * 30, sway: rand(10, 30), phase: rand(0, TAU), s: pick(sprites), a: 0.35 + near * 0.45 }
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 28, density)
            while (list.length < n) list.push(make(rand(0, h + 40)))
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const p of list) {
                p.y -= p.vy * dt
                if (p.y < -40) Object.assign(p, make(h + 40))
            }
            if (Math.random() < dt * 3 * density && list.length) {
                const p = pick(list)
                sparks.push({ x: p.x + rand(-30, 30), y: p.y + rand(-30, 30), life: 0, max: rand(0.5, 1), size: rand(6, 12) })
            }
            for (let i = sparks.length - 1; i >= 0; i--) if ((sparks[i].life += dt) >= sparks[i].max) sparks.splice(i, 1)
        },
        draw(ctx) {
            for (const p of list) {
                // Fading out near the top.
                ctx.globalAlpha = p.a * Math.min(1, Math.max(0, p.y / (h * 0.3)))
                const beat = 1 + Math.sin(t * 2.2 + p.phase) * 0.04
                stamp(ctx, p.s, p.x + Math.sin(t * 0.9 + p.phase) * p.sway, p.y, p.size * 1.6 * beat, Math.sin(t * 0.8 + p.phase) * 0.22)
            }
            for (const s of sparks) {
                ctx.globalAlpha = Math.sin((s.life / s.max) * Math.PI) * 0.9
                stamp(ctx, star, s.x, s.y, s.size, s.life * 2)
            }
            ctx.globalAlpha = 1
        },
    }
}

// ------------------------------------------------------------------ petals
// Cherry blossom petals, two-tone with a notch at the tip, tumbling as they
// fall with the wind; now and then a whole blossom.
function petals(density: number): Scene {
    type Petal = { x: number; y: number; size: number; vy: number; vx: number; rot: number; vr: number; flip: number; vf: number; s: HTMLCanvasElement; a: number }
    const petal = (light: string, deep: string) =>
        sprite(64, (ctx, r) => {
            const s = r * 0.9
            const g = ctx.createLinearGradient(0, -s, 0, s)
            g.addColorStop(0, light)
            g.addColorStop(1, deep)
            ctx.fillStyle = g
            ctx.beginPath()
            ctx.moveTo(0, s)
            ctx.bezierCurveTo(-s * 0.95, s * 0.35, -s * 0.6, -s * 0.85, -s * 0.16, -s * 0.92)
            ctx.lineTo(0, -s * 0.66) // the notch
            ctx.lineTo(s * 0.16, -s * 0.92)
            ctx.bezierCurveTo(s * 0.6, -s * 0.85, s * 0.95, s * 0.35, 0, s)
            ctx.fill()
            ctx.strokeStyle = "rgba(255,255,255,0.35)"
            ctx.lineWidth = s * 0.05
            ctx.beginPath()
            ctx.moveTo(0, s * 0.8)
            ctx.quadraticCurveTo(s * 0.06, 0, 0, -s * 0.55)
            ctx.stroke()
        })
    const blossom = sprite(96, (ctx, r) => {
        for (let i = 0; i < 5; i++) {
            ctx.save()
            ctx.rotate((i * TAU) / 5)
            const g = ctx.createLinearGradient(0, 0, 0, -r * 0.9)
            g.addColorStop(0, "#f9a8d4")
            g.addColorStop(1, "#ffe4ec")
            ctx.fillStyle = g
            ctx.beginPath()
            ctx.moveTo(0, 0)
            ctx.bezierCurveTo(-r * 0.5, -r * 0.3, -r * 0.42, -r * 0.88, -r * 0.1, -r * 0.9)
            ctx.lineTo(0, -r * 0.76)
            ctx.lineTo(r * 0.1, -r * 0.9)
            ctx.bezierCurveTo(r * 0.42, -r * 0.88, r * 0.5, -r * 0.3, 0, 0)
            ctx.fill()
            ctx.restore()
        }
        ctx.fillStyle = "#fde047"
        for (let i = 0; i < 7; i++) {
            const a = (i * TAU) / 7
            ctx.beginPath()
            ctx.arc(Math.cos(a) * r * 0.17, Math.sin(a) * r * 0.17, r * 0.045, 0, TAU)
            ctx.fill()
        }
        ctx.fillStyle = "#f472b6"
        ctx.beginPath()
        ctx.arc(0, 0, r * 0.1, 0, TAU)
        ctx.fill()
    })
    const sprites = [petal("#fff1f5", "#f9a8d4"), petal("#ffe4ec", "#f472b6"), petal("#fdf2f8", "#fbcfe8")]
    let w = 0
    let h = 0
    let list: Petal[] = []
    let t = 0
    const make = (y: number): Petal => {
        const whole = Math.random() < 0.07
        const near = Math.random()
        return {
            x: rand(-w * 0.2, w),
            y,
            size: whole ? rand(16, 24) : 8 + near * 9,
            vy: 22 + near * 30,
            vx: rand(8, 26),
            rot: rand(0, TAU),
            vr: rand(-1.6, 1.6),
            flip: rand(0, TAU),
            vf: rand(1.4, 3.4),
            s: whole ? blossom : pick(sprites),
            a: 0.6 + near * 0.35,
        }
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 64, density)
            while (list.length < n) list.push(make(rand(-h, h)))
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            const gust = wind(t)
            for (const p of list) {
                p.x += (p.vx + gust * 30) * dt
                p.y += p.vy * dt
                p.rot += p.vr * dt
                p.flip += p.vf * dt
                if (p.y > h + 20 || p.x > w + 40 || p.x < -w * 0.3) Object.assign(p, make(-20))
            }
        },
        draw(ctx) {
            for (const p of list) {
                ctx.globalAlpha = p.a
                // Tumbling: it turns over as it falls.
                stamp(ctx, p.s, p.x, p.y, p.size * 1.8, p.rot, Math.max(0.12, Math.abs(Math.cos(p.flip))), 0.75 + 0.25 * Math.abs(Math.sin(p.flip * 0.7)))
            }
            ctx.globalAlpha = 1
        },
    }
}

// ------------------------------------------------------------------ fireflies
// Fireflies wandering low on the page, each blinking in its own rhythm:
// a slow glow up, a moment lit, dark again.
function fireflies(density: number): Scene {
    type Fly = { x: number; y: number; dir: number; speed: number; period: number; offset: number; size: number }
    const halo = glow("rgba(190,242,100,0.55)", 96, 0.08)
    const core = glow("rgba(254,249,195,1)", 24, 0.3)
    let w = 0
    let h = 0
    let list: Fly[] = []
    let t = 0
    const make = (): Fly => ({ x: rand(0, w), y: rand(h * 0.3, h), dir: rand(0, TAU), speed: rand(8, 22), period: rand(2.2, 5), offset: rand(0, 5), size: rand(0.7, 1.3) })
    // 0…1: how lit it is.
    const lit = (f: Fly) => {
        const k = ((t + f.offset) % f.period) / f.period
        return k < 0.3 ? Math.sin((k / 0.3) * Math.PI) ** 2 : 0
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 42, density)
            while (list.length < n) list.push(make())
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const f of list) {
                // Wandering: turning a little, all the time.
                f.dir += rand(-2.2, 2.2) * dt
                f.x += Math.cos(f.dir) * f.speed * dt
                f.y += Math.sin(f.dir) * f.speed * 0.6 * dt
                if (f.x < -20) f.x = w + 20
                if (f.x > w + 20) f.x = -20
                if (f.y < h * 0.25) f.dir = Math.abs(f.dir) % Math.PI // turn down
                if (f.y > h + 10) f.dir = -Math.abs(f.dir) % Math.PI // and up
            }
        },
        draw(ctx) {
            ctx.globalCompositeOperation = "lighter"
            for (const f of list) {
                const l = lit(f)
                // Dark, a faint dot remains.
                ctx.globalAlpha = 0.08 + l * 0.6
                stamp(ctx, halo, f.x, f.y, 70 * f.size * (0.6 + l * 0.4))
                ctx.globalAlpha = 0.25 + l * 0.75
                stamp(ctx, core, f.x, f.y, 10 * f.size)
            }
            ctx.globalCompositeOperation = "source-over"
            ctx.globalAlpha = 1
        },
    }
}

// ------------------------------------------------------------------ fireworks
// Rockets that burst in several ways: a round peony, a ring, a gold willow
// whose sparks droop and linger, and crackling glitter. Drawn adding light.
function fireworks(density: number): Scene {
    type Rocket = { x: number; y: number; vx: number; vy: number; top: number; kind: Kind; color: string }
    type Spark = { x: number; y: number; px: number; py: number; vx: number; vy: number; life: number; max: number; color: string; drag: number; gravity: number; size: number; crackle: boolean }
    type Kind = "peony" | "ring" | "willow" | "crackle"
    const colors = ["#f5c542", "#fb7185", "#60a5fa", "#34d399", "#c084fc", "#f97316", "#f472b6"]
    const glows = new Map<string, HTMLCanvasElement>()
    const glowOf = (c: string) => glows.get(c) ?? (glows.set(c, glow(c, 32, 0.2)), glows.get(c)!)
    let w = 0
    let h = 0
    const rockets: Rocket[] = []
    const sparks: Spark[] = []
    let next = 0.5
    const burst = (r: Rocket) => {
        const scale = Math.max(0.6, density)
        const add = (n: number, speed: () => number, o: Partial<Spark>) => {
            for (let i = 0; i < n; i++) {
                const a = (i / n) * TAU + rand(-0.04, 0.04)
                const v = speed()
                sparks.push({ x: r.x, y: r.y, px: r.x, py: r.y, vx: Math.cos(a) * v, vy: Math.sin(a) * v, life: 0, max: rand(1, 1.6), color: r.color, drag: 1.5, gravity: 80, size: 7, crackle: false, ...o })
            }
        }
        if (r.kind === "peony") {
            add(Math.round(70 * scale), () => rand(60, 200), {})
            add(Math.round(20 * scale), () => rand(30, 90), { color: "#fff7ed", max: 0.9 })
        } else if (r.kind === "ring") {
            add(Math.round(48 * scale), () => 170, { max: 1.3 })
            add(Math.round(24 * scale), () => 95, { color: pick(colors), max: 1.1 })
        } else if (r.kind === "willow") {
            add(Math.round(70 * scale), () => rand(70, 170), { color: "#fbbf24", max: rand(2.4, 3.2), drag: 2.2, gravity: 46, size: 6 })
        } else {
            add(Math.round(60 * scale), () => rand(50, 180), { color: "#fef3c7", max: rand(0.9, 1.3), crackle: true, size: 5 })
        }
    }
    return {
        resize(nw, nh) {
            w = nw
            h = nh
        },
        step(dt) {
            next -= dt
            if (next <= 0) {
                next = rand(1.3, 3) / Math.max(0.5, density)
                const n = Math.random() < 0.25 ? 2 : 1 // now and then two together
                for (let i = 0; i < n; i++) {
                    const kind: Kind = pick(["peony", "peony", "ring", "willow", "crackle"])
                    rockets.push({ x: rand(w * 0.12, w * 0.88), y: h + 10, vx: rand(-20, 20), vy: rand(-560, -440), top: rand(h * 0.1, h * 0.42), kind, color: pick(colors) })
                }
            }
            for (let i = rockets.length - 1; i >= 0; i--) {
                const r = rockets[i]
                r.x += r.vx * dt
                r.y += r.vy * dt
                // Its trail of sparks.
                if (Math.random() < 0.7) sparks.push({ x: r.x, y: r.y + 6, px: r.x, py: r.y + 6, vx: rand(-15, 15), vy: rand(20, 60), life: 0, max: rand(0.3, 0.6), color: "#fde68a", drag: 3, gravity: 40, size: 4, crackle: false })
                if (r.y <= r.top) {
                    burst(r)
                    rockets.splice(i, 1)
                }
            }
            for (let i = sparks.length - 1; i >= 0; i--) {
                const s = sparks[i]
                s.life += dt
                s.px = s.x
                s.py = s.y
                s.vx *= 1 - s.drag * dt
                s.vy = s.vy * (1 - s.drag * dt) + s.gravity * dt
                s.x += s.vx * dt
                s.y += s.vy * dt
                if (s.life >= s.max) sparks.splice(i, 1)
            }
        },
        draw(ctx) {
            ctx.globalCompositeOperation = "lighter"
            ctx.lineCap = "round"
            for (const r of rockets) {
                ctx.globalAlpha = 0.9
                stamp(ctx, glowOf("#fde68a"), r.x, r.y, 10)
            }
            for (const s of sparks) {
                const k = s.life / s.max
                // Glitter: flashes on and off near its end.
                if (s.crackle && k > 0.55 && Math.random() < 0.5) continue
                const fade = Math.max(0, 1 - k)
                ctx.globalAlpha = fade * (s.crackle && k > 0.55 ? 1 : 0.85)
                ctx.strokeStyle = s.color
                ctx.lineWidth = 1.6
                ctx.beginPath()
                // A streak back along its way.
                ctx.moveTo(s.x - s.vx * 0.05, s.y - s.vy * 0.05)
                ctx.lineTo(s.x, s.y)
                ctx.stroke()
                stamp(ctx, glowOf(s.color), s.x, s.y, s.size * (0.6 + fade * 0.6))
            }
            ctx.globalCompositeOperation = "source-over"
            ctx.globalAlpha = 1
        },
    }
}
