import type { Media } from "./types"

// Episodes missing from the library, for an anime with some of them: gaps
// between the first and the last one there (a library can start mid-way,
// One Piece's at 1000, say), and the ones aired after the last.
export type Missing = { gaps: number[]; newer: number[] }

export function missingEpisodes(have: number[], media: Media): Missing {
    const set = new Set(have.filter(n => n > 0))
    if (set.size === 0) return { gaps: [], newer: [] }
    const nums = [...set].sort((a, b) => a - b)
    const lo = nums[0]
    const hi = nums[nums.length - 1]
    const gaps: number[] = []
    for (let n = lo + 1; n < hi; n++) if (!set.has(n)) gaps.push(n)
    const aired = airedEpisodes(media)
    const newer: number[] = []
    for (let n = hi + 1; aired !== null && n <= aired; n++) newer.push(n)
    return { gaps, newer }
}

// Episodes out so far: the next one to air less one while it's airing, else
// all of them (null when unknown).
export function airedEpisodes(media: Media): number | null {
    if (media.nextAiringEpisode?.episode) return media.nextAiringEpisode.episode - 1
    if (media.status === "NOT_YET_RELEASED") return 0
    return media.episodes ?? null
}

// "3, 7–9, 12": the numbers, runs joined; a long list ends with "and N
// more" (episodes).
export function formatRanges(nums: number[], max = 8): string {
    const runs: [number, number][] = []
    for (const n of nums) {
        const last = runs[runs.length - 1]
        if (last && n === last[1] + 1) last[1] = n
        else runs.push([n, n])
    }
    const text = (r: [number, number]) => (r[0] === r[1] ? String(r[0]) : `${r[0]}–${r[1]}`)
    if (runs.length <= max) return runs.map(text).join(", ")
    const rest = runs.slice(max).reduce((sum, r) => sum + r[1] - r[0] + 1, 0)
    return `${runs.slice(0, max).map(text).join(", ")} and ${rest} more`
}
