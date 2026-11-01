import { episodeKeys, formatKey, latestAired, mediaIsShow, splitKey } from "./ids"
import type { Media } from "./types"

// Episodes missing from the library, for a show with some of them (by their
// keys, S01E05): gaps between the first and the last one there (a library
// can start mid-way, at season 3, say), and the ones aired after the last.
// Specials (season 0) don't count.
export type Missing = { gaps: number[]; newer: number[] }

export function missingEpisodes(have: number[], media: Media): Missing {
    const none = { gaps: [], newer: [] }
    if (!mediaIsShow(media)) return none
    const set = new Set(have.filter(k => splitKey(k)[0] > 0))
    const keys = episodeKeys(media)
    const at = keys.flatMap((k, i) => (set.has(k) ? [i] : []))
    if (!at.length) return none
    const lo = at[0]
    const hi = at[at.length - 1]
    const gaps = keys.slice(lo + 1, hi).filter(k => !set.has(k))
    const aired = keys.indexOf(latestAired(media))
    const newer = aired > hi ? keys.slice(hi + 1, aired + 1) : []
    return { gaps, newer }
}

// "S01E03, S01E07–E09, S02E01": the episodes, runs in a season joined; a
// long list ends with "and N more".
export function formatRanges(keys: number[], max = 8): string {
    const runs: [number, number][] = []
    for (const k of keys) {
        const last = runs[runs.length - 1]
        if (last && k === last[1] + 1 && splitKey(k)[0] === splitKey(last[1])[0]) last[1] = k
        else runs.push([k, k])
    }
    const text = (r: [number, number]) => (r[0] === r[1] ? formatKey(r[0]) : `${formatKey(r[0])}–E${String(splitKey(r[1])[1]).padStart(2, "0")}`)
    if (runs.length <= max) return runs.map(text).join(", ")
    const rest = runs.slice(max).reduce((sum, r) => sum + r[1] - r[0] + 1, 0)
    return `${runs.slice(0, max).map(text).join(", ")} and ${rest} more`
}
