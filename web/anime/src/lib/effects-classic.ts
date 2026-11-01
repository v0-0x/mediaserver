// The holiday themes' animations as they were before 2.0, for the classic
// look (lib/effects.ts has 2.0's): falling snow, bats flying across, hearts
// rising, cherry blossom petals, fireworks and fireflies. Each effect makes
// its particles and moves them by the time passed (dt, in seconds), so a
// slow frame doesn't slow them down.

import type { Scene } from "./effects"
import type { Effect } from "./themes"

const rand = (a: number, b: number) => a + Math.random() * (b - a)
const pick = <T,>(xs: T[]) => xs[Math.floor(Math.random() * xs.length)]

// density: 1 on a computer, less on a TV (it's slower at drawing them).
export function makeScene(effect: Effect, w: number, h: number, density = 1): Scene {
    const scene = { snow, bats, hearts, petals, fireworks, fireflies }[effect](density)
    scene.resize(w, h)
    return scene
}

// How many for a screen this big: a 1920×1080 one gets about max.
const count = (w: number, h: number, max: number, density: number) => Math.max(4, Math.round(Math.min(1, (w * h) / (1920 * 1080)) * max * density))

function snow(density: number): Scene {
    type Flake = { x: number; y: number; r: number; vy: number; sway: number; phase: number; a: number }
    let w = 0
    let h = 0
    let flakes: Flake[] = []
    const make = (y: number): Flake => {
        const r = rand(0.8, 3.2)
        return { x: rand(0, w), y, r, vy: 18 + r * 14 + rand(0, 10), sway: rand(8, 26), phase: rand(0, Math.PI * 2), a: rand(0.35, 0.85) }
    }
    let t = 0
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 110, density)
            while (flakes.length < n) flakes.push(make(rand(-h, h)))
            flakes = flakes.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const f of flakes) {
                f.y += f.vy * dt
                if (f.y > h + 6) Object.assign(f, make(-6))
            }
        },
        draw(ctx) {
            for (const f of flakes) {
                const x = f.x + Math.sin(t * 0.8 + f.phase) * f.sway
                ctx.globalAlpha = f.a
                ctx.fillStyle = "#ffffff"
                ctx.beginPath()
                ctx.arc(x, f.y, f.r, 0, Math.PI * 2)
                ctx.fill()
            }
            ctx.globalAlpha = 1
        },
    }
}

function bats(density: number): Scene {
    type Bat = { x: number; y: number; vx: number; size: number; flap: number; speed: number; bob: number; phase: number; wait: number }
    let w = 0
    let h = 0
    let list: Bat[] = []
    // Off one side, after a while (a few at a time, not a flock).
    const make = (first = false): Bat => {
        const right = Math.random() < 0.5
        const size = rand(9, 22)
        return {
            x: right ? w + 40 : -40,
            y: rand(h * 0.06, h * 0.7),
            vx: (right ? -1 : 1) * rand(70, 150),
            size,
            flap: rand(7, 11),
            speed: 1,
            bob: rand(10, 34),
            phase: rand(0, Math.PI * 2),
            wait: first ? rand(0, 8) : rand(2, 12),
        }
    }
    let t = 0
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = Math.max(3, Math.round(6 * density))
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
                if (b.x < -60 || b.x > w + 60) Object.assign(b, make())
            }
        },
        draw(ctx) {
            ctx.fillStyle = "#8b7aa8"
            for (const b of list) {
                if (b.wait > 0) continue
                const y = b.y + Math.sin(t * 1.6 + b.phase) * b.bob
                const f = Math.sin(t * b.flap * Math.PI + b.phase) // wings: -1 down, 1 up
                const s = b.size
                ctx.globalAlpha = 0.85
                ctx.save()
                ctx.translate(b.x, y)
                if (b.vx < 0) ctx.scale(-1, 1)
                ctx.beginPath()
                // A body, ears, and two wings whose tips go up and down.
                ctx.ellipse(0, 0, s * 0.16, s * 0.28, 0, 0, Math.PI * 2)
                ctx.moveTo(-s * 0.1, -s * 0.22)
                ctx.lineTo(-s * 0.06, -s * 0.38)
                ctx.lineTo(0, -s * 0.24)
                ctx.lineTo(s * 0.06, -s * 0.38)
                ctx.lineTo(s * 0.1, -s * 0.22)
                for (const side of [-1, 1]) {
                    const tipY = -f * s * 0.5
                    ctx.moveTo(side * s * 0.12, -s * 0.08)
                    ctx.quadraticCurveTo(side * s * 0.55, tipY - s * 0.2, side * s, tipY)
                    ctx.quadraticCurveTo(side * s * 0.8, tipY * 0.4 + s * 0.12, side * s * 0.62, s * 0.08 + tipY * 0.2)
                    ctx.quadraticCurveTo(side * s * 0.45, s * 0.02, side * s * 0.35, s * 0.16)
                    ctx.quadraticCurveTo(side * s * 0.25, s * 0.05, side * s * 0.12, s * 0.12)
                    ctx.closePath()
                }
                ctx.fill()
                ctx.restore()
            }
            ctx.globalAlpha = 1
        },
    }
}

function heartPath(ctx: CanvasRenderingContext2D, s: number) {
    ctx.beginPath()
    ctx.moveTo(0, s * 0.3)
    ctx.bezierCurveTo(-s * 0.05, s * 0.25, -s * 0.5, 0, -s * 0.5, -s * 0.25)
    ctx.bezierCurveTo(-s * 0.5, -s * 0.5, -s * 0.15, -s * 0.6, 0, -s * 0.32)
    ctx.bezierCurveTo(s * 0.15, -s * 0.6, s * 0.5, -s * 0.5, s * 0.5, -s * 0.25)
    ctx.bezierCurveTo(s * 0.5, 0, s * 0.05, s * 0.25, 0, s * 0.3)
    ctx.closePath()
}

function hearts(density: number): Scene {
    type Heart = { x: number; y: number; s: number; vy: number; sway: number; phase: number; color: string; a: number }
    const colors = ["#f2557a", "#fb7185", "#f9a8d4", "#e11d48"]
    let w = 0
    let h = 0
    let list: Heart[] = []
    const make = (y: number): Heart => ({ x: rand(0, w), y, s: rand(9, 22), vy: rand(16, 38), sway: rand(10, 30), phase: rand(0, Math.PI * 2), color: pick(colors), a: rand(0.25, 0.6) })
    let t = 0
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 26, density)
            while (list.length < n) list.push(make(rand(0, h + 40)))
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const p of list) {
                p.y -= p.vy * dt
                if (p.y < -30) Object.assign(p, make(h + 30))
            }
        },
        draw(ctx) {
            for (const p of list) {
                // Fading out near the top.
                ctx.globalAlpha = p.a * Math.min(1, Math.max(0, p.y / (h * 0.35)))
                ctx.fillStyle = p.color
                ctx.save()
                ctx.translate(p.x + Math.sin(t + p.phase) * p.sway, p.y)
                ctx.rotate(Math.sin(t * 0.9 + p.phase) * 0.25)
                heartPath(ctx, p.s)
                ctx.fill()
                ctx.restore()
            }
            ctx.globalAlpha = 1
        },
    }
}

function petals(density: number): Scene {
    type Petal = { x: number; y: number; s: number; vx: number; vy: number; rot: number; vr: number; flip: number; vf: number; color: string; a: number }
    const colors = ["#fbcfe8", "#f9a8d4", "#fda4af", "#ffe4ec"]
    let w = 0
    let h = 0
    let list: Petal[] = []
    const make = (y: number): Petal => ({
        x: rand(-w * 0.2, w),
        y,
        s: rand(6, 12),
        vx: rand(14, 40),
        vy: rand(24, 50),
        rot: rand(0, Math.PI * 2),
        vr: rand(-1.4, 1.4),
        flip: rand(0, Math.PI * 2),
        vf: rand(1.5, 3.5),
        color: pick(colors),
        a: rand(0.55, 0.9),
    })
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 46, density)
            while (list.length < n) list.push(make(rand(-h, h)))
            list = list.slice(0, n)
        },
        step(dt) {
            for (const p of list) {
                p.x += p.vx * dt
                p.y += p.vy * dt
                p.rot += p.vr * dt
                p.flip += p.vf * dt
                if (p.y > h + 12 || p.x > w + 20) Object.assign(p, make(-12))
            }
        },
        draw(ctx) {
            for (const p of list) {
                ctx.globalAlpha = p.a
                ctx.fillStyle = p.color
                ctx.save()
                ctx.translate(p.x, p.y)
                ctx.rotate(p.rot)
                // Turning over as it falls.
                ctx.scale(Math.max(0.15, Math.abs(Math.cos(p.flip))), 1)
                const s = p.s
                ctx.beginPath()
                ctx.moveTo(0, s * 0.5)
                ctx.quadraticCurveTo(-s * 0.6, 0, -s * 0.18, -s * 0.5)
                ctx.lineTo(0, -s * 0.34) // the notch at its tip
                ctx.lineTo(s * 0.18, -s * 0.5)
                ctx.quadraticCurveTo(s * 0.6, 0, 0, s * 0.5)
                ctx.fill()
                ctx.restore()
            }
            ctx.globalAlpha = 1
        },
    }
}

function fireworks(density: number): Scene {
    type Rocket = { x: number; y: number; vy: number; top: number; hue: string }
    type Spark = { x: number; y: number; vx: number; vy: number; life: number; max: number; color: string }
    const colors = ["#f5c542", "#fde68a", "#f472b6", "#60a5fa", "#34d399", "#f97316"]
    let w = 0
    let h = 0
    const rockets: Rocket[] = []
    const sparks: Spark[] = []
    let next = 0.6
    const burst = (r: Rocket) => {
        const n = Math.round(rand(36, 60) * Math.max(0.5, density))
        const speed = rand(110, 190)
        for (let i = 0; i < n; i++) {
            const a = (i / n) * Math.PI * 2 + rand(-0.05, 0.05)
            const v = speed * rand(0.55, 1)
            sparks.push({ x: r.x, y: r.y, vx: Math.cos(a) * v, vy: Math.sin(a) * v, life: 0, max: rand(1.1, 1.7), color: Math.random() < 0.8 ? r.hue : pick(colors) })
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
                next = rand(1.6, 3.6) / Math.max(0.5, density)
                rockets.push({ x: rand(w * 0.15, w * 0.85), y: h + 10, vy: rand(-520, -420), top: rand(h * 0.12, h * 0.42), hue: pick(colors) })
            }
            for (let i = rockets.length - 1; i >= 0; i--) {
                const r = rockets[i]
                r.y += r.vy * dt
                if (r.y <= r.top) {
                    burst(r)
                    rockets.splice(i, 1)
                }
            }
            for (let i = sparks.length - 1; i >= 0; i--) {
                const s = sparks[i]
                s.life += dt
                s.vx *= 1 - 1.4 * dt
                s.vy = s.vy * (1 - 1.4 * dt) + 90 * dt // gravity
                s.x += s.vx * dt
                s.y += s.vy * dt
                if (s.life >= s.max) sparks.splice(i, 1)
            }
        },
        draw(ctx) {
            ctx.lineCap = "round"
            for (const r of rockets) {
                ctx.globalAlpha = 0.7
                ctx.strokeStyle = r.hue
                ctx.lineWidth = 2
                ctx.beginPath()
                ctx.moveTo(r.x, r.y)
                ctx.lineTo(r.x, r.y + 16)
                ctx.stroke()
            }
            ctx.lineWidth = 2
            for (const s of sparks) {
                ctx.globalAlpha = Math.max(0, 1 - s.life / s.max) * 0.9
                ctx.strokeStyle = s.color
                ctx.beginPath()
                // A short streak behind it.
                ctx.moveTo(s.x - s.vx * 0.04, s.y - s.vy * 0.04)
                ctx.lineTo(s.x, s.y)
                ctx.stroke()
            }
            ctx.globalAlpha = 1
        },
    }
}

function fireflies(density: number): Scene {
    type Fly = { x: number; y: number; vx: number; vy: number; phase: number; speed: number; r: number }
    let w = 0
    let h = 0
    let list: Fly[] = []
    const make = (): Fly => ({ x: rand(0, w), y: rand(h * 0.35, h), vx: rand(-12, 12), vy: rand(-8, 8), phase: rand(0, Math.PI * 2), speed: rand(1.2, 2.6), r: rand(1.4, 2.6) })
    let t = 0
    return {
        resize(nw, nh) {
            w = nw
            h = nh
            const n = count(w, h, 34, density)
            while (list.length < n) list.push(make())
            list = list.slice(0, n)
        },
        step(dt) {
            t += dt
            for (const f of list) {
                // Wandering: turning a little now and then.
                f.vx += rand(-20, 20) * dt
                f.vy += rand(-20, 20) * dt
                f.vx = Math.max(-18, Math.min(18, f.vx))
                f.vy = Math.max(-12, Math.min(12, f.vy))
                f.x += f.vx * dt
                f.y += f.vy * dt
                if (f.x < -10) f.x = w + 10
                if (f.x > w + 10) f.x = -10
                if (f.y < h * 0.3) f.vy = Math.abs(f.vy)
                if (f.y > h + 10) f.vy = -Math.abs(f.vy)
            }
        },
        draw(ctx) {
            for (const f of list) {
                const glow = 0.5 + 0.5 * Math.sin(t * f.speed + f.phase)
                ctx.globalAlpha = 0.12 * glow
                ctx.fillStyle = "#d9f99d"
                ctx.beginPath()
                ctx.arc(f.x, f.y, f.r * 5, 0, Math.PI * 2)
                ctx.fill()
                ctx.globalAlpha = 0.25 + 0.65 * glow
                ctx.fillStyle = "#fef9c3"
                ctx.beginPath()
                ctx.arc(f.x, f.y, f.r, 0, Math.PI * 2)
                ctx.fill()
            }
            ctx.globalAlpha = 1
        },
    }
}
