// The streams of a movie or episode as the providers find them
// (/api/providers/streams/live): each provider's answer shows as soon as
// it comes, the whole result (ranked by the server) replaces them at the
// end and is kept like any query.
import { useQuery } from "@tanstack/react-query"
import { useCallback, useRef, useState } from "react"
import { qs, readLines } from "./api"
import type { LiveAnswer, LiveAsked, LiveLine, ProviderStream, StreamsResult } from "./types"

export type LiveProgress = { asking: LiveAsked[]; answers: LiveAnswer[] }

// useLiveStreams is the streams of a movie (episode 1) or of a show's episode
// (a key) from every enabled provider. progress is the search under way
// (null when the list comes from the cache), refresh asks the providers
// again.
export function useLiveStreams(mediaId: number, episode: number, enabled = true) {
    const [progress, setProgress] = useState<LiveProgress | null>(null)
    const refreshNext = useRef(false)
    const query = useQuery({
        queryKey: ["provider-streams", mediaId, episode, ""],
        queryFn: async ({ signal }) => {
            const refresh = refreshNext.current
            refreshNext.current = false
            setProgress({ asking: [], answers: [] })
            let result: StreamsResult | null = null
            let failed = ""
            // Asking the providers again is a POST (GETs only read).
            await readLines(
                refresh ? "/api/providers/streams/live" : `/api/providers/streams/live${qs({ mediaId, episode })}`,
                (line: LiveLine) => {
                    if ("asking" in line) setProgress(p => ({ asking: line.asking ?? [], answers: p?.answers ?? [] }))
                    else if ("done" in line) {
                        if (line.result) result = line.result
                        else failed = line.error || "the search failed"
                    } else setProgress(p => ({ asking: p?.asking ?? [], answers: [...(p?.answers ?? []), line] }))
                },
                signal,
                refresh ? { mediaId, episode, refresh: true } : undefined,
            )
            if (!result) throw new Error(failed || "the search ended early")
            return result as StreamsResult
        },
        enabled: enabled && mediaId > 0 && episode > 0,
        retry: false,
        staleTime: 4 * 60_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
    })
    const refetch = query.refetch
    const refresh = useCallback(() => {
        refreshNext.current = true
        return refetch()
    }, [refetch])
    // The search under way shows; once over, the result does.
    return { ...query, progress: query.isFetching ? progress : null, refresh }
}

// What the providers found so far, as one list: each link once (the first
// provider to answer with it keeps it), ranked as the server ranks (see
// providers.Rank) until the whole result comes.
export function mergeAnswers(answers: LiveAnswer[], preferredQuality: string): ProviderStream[] {
    const seen = new Set<string>()
    const out: ProviderStream[] = []
    for (const a of answers)
        for (const s of a.streams) {
            const k = linkKey(s)
            if (k && seen.has(k)) continue
            if (k) seen.add(k)
            out.push(s)
        }
    return rankStreams(out, preferredQuality)
}

function linkKey(s: ProviderStream) {
    if (s.url) return "url:" + s.url
    if (s.infoHash) return `bt:${s.infoHash.toLowerCase()}:${s.fileIdx}`
    if (s.magnet) return "magnet:" + s.magnet
    if (s.youTubeId) return "yt:" + s.youTubeId
    return s.externalUrl ? "ext:" + s.externalUrl : ""
}

const tier = (s: ProviderStream) => ({ direct: 0, torrent: 1, youtube: 2, external: 3 })[s.kind] ?? 4
const qualityRank = (q: string) => parseInt(q, 10) || 0

// rankStreams sorts best first: direct links, then torrents, YouTube and
// web pages; the preferred quality, then higher ones; links a browser plays
// as they are; the most seeded.
export function rankStreams(streams: ProviderStream[], preferredQuality: string) {
    const pref = qualityRank(preferredQuality.toLowerCase() === "4k" ? "2160p" : preferredQuality)
    return streams
        .map((s, i) => ({ s, i }))
        .sort((x, y) => {
            const a = x.s
            const b = y.s
            if (tier(a) !== tier(b)) return tier(a) - tier(b)
            const qa = qualityRank(a.quality)
            const qb = qualityRank(b.quality)
            if (pref > 0 && (qa === pref) !== (qb === pref)) return qa === pref ? -1 : 1
            if (qa !== qb) return qb - qa
            if (!!a.notWebReady !== !!b.notWebReady) return a.notWebReady ? 1 : -1
            if ((a.seeders ?? 0) !== (b.seeders ?? 0)) return (b.seeders ?? 0) - (a.seeders ?? 0)
            return x.i - y.i
        })
        .map(x => x.s)
}
