import { formatKey, isShow } from "./ids"
import type { LocalFile } from "./types"

// How a library file is labelled: S02E05 (S02E05–E06 for a file holding
// both), Movie, Part 2 (a movie split across files), Special, Trailer,
// Featurette...
export function fileLabel(f: LocalFile): string {
    switch (f.kind) {
        case "extra": {
            const x = f.parsed?.extra
            return x ? x[0].toUpperCase() + x.slice(1) : "Extra"
        }
        case "part":
            return f.parsed?.part ? `Part ${f.parsed.part}` : "Part"
    }
    if (!f.episode) return f.kind === "special" ? "Special" : "—"
    if (!isShow(f.mediaId)) return "Movie"
    const end = f.episodeEnd && f.episodeEnd > f.episode ? `–E${String(f.episodeEnd % 1000).padStart(2, "0")}` : ""
    return formatKey(f.episode) + end
}

// The episode keys a file holds: its episode, and the next ones up to
// episodeEnd for a multi-episode file. None for an extra.
export function fileKeys(f: LocalFile): number[] {
    if (!f.episode || f.kind === "extra") return []
    const out = [f.episode]
    const end = f.episodeEnd ?? 0
    if (end > f.episode && Math.floor(end / 1000) === Math.floor(f.episode / 1000)) for (let k = f.episode + 1; k <= end; k++) out.push(k)
    return out
}
