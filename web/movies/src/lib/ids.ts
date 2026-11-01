// Media ids and episode keys, as server/internal/catalog/ids.go and the
// episode helpers of catalog.Media (types.go) define them. Keep the two in
// step.
//
// A movie's media id is its TMDB movie id; a show's is SHOW_BASE plus its
// TMDB TV id. An episode is identified by its key, season*1000 + episode
// (S02E05 is 2005, the special S00E03 is 3): keys sort in watching order, so
// the list's progress (the key of the last episode watched) only moves
// forward. A movie has one "episode", key 1.
import type { Media } from "./types"

export const SHOW_BASE = 1_000_000_000
export const MOVIE_EPISODE = 1
const KEY_SEASON = 1000

export const isShow = (id: number) => id > SHOW_BASE
export const isMovie = (id: number) => id > 0 && id < SHOW_BASE

// The TMDB id (movie or TV) behind a media id.
export const tmdbIdOf = (id: number) => (isShow(id) ? id - SHOW_BASE : id)

export const kindOf = (id: number): "TV" | "MOVIE" => (isShow(id) ? "TV" : "MOVIE")

// A media id as TMDB names it, for when the title isn't known: "TMDB tv/1399".
export const mediaRef = (id: number) => `TMDB ${isShow(id) ? "tv" : "movie"}/${tmdbIdOf(id)}`

export const episodeKey = (season: number, episode: number) => season * KEY_SEASON + episode

// The key of an episode the server lists (GET /api/media/{id} and
// …/episodes): its key ("number" holds the same, for older code).
export const episodeKeyOf = (e: { key?: number; number: number }) => e.key ?? e.number

// The season and episode of a key.
export const splitKey = (key: number): [season: number, episode: number] => [Math.floor(key / KEY_SEASON), key % KEY_SEASON]

const pad = (n: number) => String(n).padStart(2, "0")

// S02E05.
export function formatKey(key: number) {
    const [s, e] = splitKey(key)
    return `S${pad(s)}E${pad(e)}`
}

// ---------------------------------------------------------------------------
// Episodes of a media, from its season list. Specials (season 0) are left
// out of the order: they are never "next", nor the last episode.

type M = Pick<Media, "id" | "type" | "status" | "seasons" | "episodes" | "latestEpisode"> | null | undefined

export const mediaIsShow = (m: M) => !!m && (m.type === "TV" || isShow(m.id))

// The keys of all regular episodes, in order ([1] for a movie).
export function episodeKeys(m: M): number[] {
    if (!m) return []
    if (!mediaIsShow(m)) return [MOVIE_EPISODE]
    const seasons = [...(m.seasons ?? [])].filter(s => s.seasonNumber > 0).sort((a, b) => a.seasonNumber - b.seasonNumber)
    const out: number[] = []
    for (const s of seasons) for (let e = 1; e <= s.episodeCount; e++) out.push(episodeKey(s.seasonNumber, e))
    return out
}

// The number of regular episodes (1 for a movie), 0 when unknown.
export function totalEpisodes(m: M) {
    if (!m) return 0
    if (!mediaIsShow(m)) return 1
    return episodeKeys(m).length || m.episodes || 0
}

// The first regular episode (S01E01 when the seasons aren't known).
export const firstEpisode = (m: M) => episodeKeys(m)[0] ?? episodeKey(1, 1)

// The last regular episode known (aired or not), 0 when unknown.
export const lastEpisode = (m: M) => episodeKeys(m).at(-1) ?? 0

// How many regular episodes have aired (1 for a released movie).
export function airedEpisodes(m: M) {
    if (!m || m.status === "NOT_YET_RELEASED") return 0
    if (!mediaIsShow(m)) return 1
    if (m.latestEpisode) return episodeIndex(m, m.latestEpisode)
    if (m.status === "FINISHED" || m.status === "CANCELLED") return totalEpisodes(m)
    return 0
}

// The last regular episode that aired, 0 if none.
export function latestAired(m: M) {
    if (!m || m.status === "NOT_YET_RELEASED") return 0
    if (!mediaIsShow(m)) return MOVIE_EPISODE
    if (m.latestEpisode && m.latestEpisode >= KEY_SEASON) return m.latestEpisode
    if (m.status === "FINISHED" || m.status === "CANCELLED") return lastEpisode(m)
    return 0
}

// The regular episode after key (the first one for key 0), 0 when there is
// none.
export function nextEpisode(m: M, key: number) {
    if (m && !mediaIsShow(m)) return key < MOVIE_EPISODE ? MOVIE_EPISODE : 0
    const keys = episodeKeys(m)
    const next = keys.find(k => k > key)
    if (next) return next
    return keys.length === 0 && key === 0 ? episodeKey(1, 1) : 0
}

// The regular episode before key, 0 if none.
export function prevEpisode(m: M, key: number) {
    if (m && !mediaIsShow(m)) return 0
    const before = episodeKeys(m).filter(k => k < key)
    return before.at(-1) ?? 0
}

// How many regular episodes come up to and including key: the count shown
// as "13 / 24". 0 before the first episode.
export function episodeIndex(m: M, key: number) {
    if (m && !mediaIsShow(m)) return key >= MOVIE_EPISODE ? 1 : 0
    const keys = episodeKeys(m)
    if (keys.length === 0) return splitKey(key)[1]
    return keys.filter(k => k <= key).length
}

// The key of the n-th regular episode (1-based), 0 when out of range: the
// inverse of episodeIndex, for numbers counted from the first episode.
export function keyAtIndex(m: M, n: number) {
    if (m && !mediaIsShow(m)) return n === 1 ? MOVIE_EPISODE : 0
    const keys = episodeKeys(m)
    return n >= 1 && n <= keys.length ? keys[n - 1] : 0
}

// The number of episodes of season s, 0 if unknown.
export const seasonEpisodeCount = (m: M, s: number) => m?.seasons?.find(x => x.seasonNumber === s)?.episodeCount ?? 0

// How an episode is named in the UI: "S02E05" for a show, "" for a movie.
export const episodeLabel = (m: M, key: number) => (mediaIsShow(m) ? formatKey(key) : "")
