import { CalendarClock, Clapperboard, ExternalLink, Globe, Languages, Play, Tv, UserRound, Users } from "lucide-react"
import { useState } from "react"
import { Carousel, MediaCard } from "@/components/MediaCard"
import { ListCardMenu } from "@/components/ListCardMenu"
import { mediaIsShow, totalEpisodes } from "@/lib/ids"
import type { CharacterEdge, CrewMember, Media, WatchProvider } from "@/lib/types"
import { cn, externalLinks, formatDate, fuzzyDate, img, languageName, runtimeLabel } from "@/lib/utils"
import { Tooltip } from "../ui"

// Everything about a movie or show that isn't its episodes: cast and crew,
// where to watch it, the trailer, its collection, recommendations.
export function DetailsPanel({ media, before }: { media: Media; before?: React.ReactNode }) {
    const show = mediaIsShow(media)
    const cast = media.characters?.edges ?? []
    const crew = media.crew ?? []
    const relations = (media.relations?.edges ?? []).filter(e => e.node && e.node.id !== media.id)
    const recs = (media.recommendations?.nodes ?? []).map(n => n.mediaRecommendation).filter((m): m is Media => !!m)
    const tags = (media.tags ?? []).filter(t => !t.isMediaSpoiler).slice(0, 20)
    const seasons = (media.seasons ?? []).filter(s => s.seasonNumber > 0).length
    const studios = media.studios?.nodes ?? []
    const released = fuzzyDate(media.startDate)
    const ended = fuzzyDate(media.endDate)

    return (
        <div className="flex flex-col gap-10">
            {before}
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <InfoCard
                    label={show ? "First aired" : "Released"}
                    value={released ? formatDate(released) + (show && ended && media.status !== "RELEASING" ? ` → ${formatDate(ended)}` : "") : ""}
                    icon={<CalendarClock />}
                />
                <InfoCard
                    label={show ? "Seasons" : "Runtime"}
                    value={show ? [seasons ? `${seasons} ${seasons === 1 ? "season" : "seasons"}` : "", totalEpisodes(media) ? `${totalEpisodes(media)} episodes` : ""].filter(Boolean).join(" · ") : runtimeLabel(media.duration)}
                    icon={show ? <Tv /> : <Clapperboard />}
                />
                <InfoCard label="Original language" value={languageName(media.originalLanguage)} icon={<Languages />} />
                <InfoCard label={show ? "Network" : "Studio"} value={studios.map(s => s.name).slice(0, 2).join(", ")} icon={<Globe />} />
            </div>

            <WhereToWatch media={media} />

            {cast.length > 0 && <Cast cast={cast} />}
            {crew.length > 0 && <Crew crew={crew} />}

            {media.trailer?.site === "youtube" && <Trailer id={media.trailer.id} thumbnail={media.trailer.thumbnail} />}

            {relations.length > 0 && (
                <section>
                    <h3 className="mb-4 text-lg font-semibold tracking-tight">{media.belongsTo?.name || "Related"}</h3>
                    <Carousel>
                        {relations.map(r => (
                            <div key={r.node!.id}>
                                <p className="mb-2 text-xs font-medium text-subtle capitalize">{r.relationType === "OTHER" ? "In the collection" : r.relationType.toLowerCase()}</p>
                                <MediaCard media={r.node!} listEntry={r.node!.mediaListEntry} menu={<ListCardMenu media={r.node!} />} />
                            </div>
                        ))}
                    </Carousel>
                </section>
            )}

            {recs.length > 0 && (
                <section>
                    <h3 className="mb-4 text-lg font-semibold tracking-tight">More like this</h3>
                    <Carousel>
                        {recs.map(m => (
                            <MediaCard key={m.id} media={m} listEntry={m.mediaListEntry} menu={<ListCardMenu media={m} />} />
                        ))}
                    </Carousel>
                </section>
            )}

            {tags.length > 0 && (
                <section>
                    <h3 className="mb-3 text-lg font-semibold tracking-tight">Keywords</h3>
                    <div className="flex flex-wrap gap-2">
                        {tags.map(t => (
                            <span key={t.name} className="rounded-md border border-line bg-surface-1 px-2.5 py-1 text-[13px] text-fg/85">
                                {t.name}
                            </span>
                        ))}
                    </div>
                </section>
            )}

            <div className="flex flex-wrap gap-2">
                {externalLinks(media).map(l => (
                    <a
                        key={l.label}
                        href={l.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex h-8 items-center gap-1.5 rounded-md bg-white/[0.06] px-3 text-[13px] font-medium text-fg/80 transition-colors hover:bg-white/[0.1] hover:text-fg"
                    >
                        {l.label}
                        <ExternalLink className="size-3.5 opacity-60" />
                    </a>
                ))}
            </div>
        </div>
    )
}

function InfoCard({ label, value, icon }: { label: string; value: string; icon: React.ReactNode }) {
    return (
        <div className="card flex items-center gap-4 p-4">
            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-white/[0.05] text-muted [&_svg]:size-[18px]">{icon}</span>
            <div className="min-w-0">
                <p className="text-xs text-subtle">{label}</p>
                <p className="truncate font-medium">{value || "—"}</p>
            </div>
        </div>
    )
}

const WATCH_GROUPS: { key: "flatrate" | "free" | "ads" | "rent" | "buy"; label: string }[] = [
    { key: "flatrate", label: "Stream" },
    { key: "free", label: "Free" },
    { key: "ads", label: "With ads" },
    { key: "rent", label: "Rent" },
    { key: "buy", label: "Buy" },
]

// TMDB's "where to watch" (data from JustWatch), for the region in Settings ›
// Metadata.
function WhereToWatch({ media }: { media: Media }) {
    const wp = media.watchProviders
    const groups = WATCH_GROUPS.map(g => ({ ...g, providers: wp?.[g.key] ?? [] })).filter(g => g.providers.length > 0)
    if (!wp || groups.length === 0) return null
    return (
        <section>
            <div className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="text-lg font-semibold tracking-tight">Where to watch</h3>
                <span className="text-xs text-subtle">
                    {wp.region && `In ${wp.region} · `}
                    {wp.link ? (
                        <a href={wp.link} target="_blank" rel="noopener noreferrer" className="hover:text-fg hover:underline">
                            data from JustWatch
                        </a>
                    ) : (
                        "data from JustWatch"
                    )}
                </span>
            </div>
            <div className="flex flex-wrap gap-x-8 gap-y-5">
                {groups.map(g => (
                    <div key={g.key}>
                        <p className="mb-2 text-xs font-medium text-subtle">{g.label}</p>
                        <div className="flex flex-wrap gap-2">
                            {g.providers.map(p => (
                                <ProviderLogo key={p.id} provider={p} href={wp.link} />
                            ))}
                        </div>
                    </div>
                ))}
            </div>
        </section>
    )
}

function ProviderLogo({ provider, href }: { provider: WatchProvider; href?: string }) {
    const logo = provider.logo ? (
        <img src={img(provider.logo)} alt={provider.name} loading="lazy" className="size-11 rounded-lg object-cover ring-1 ring-white/10 transition-[filter] hover:brightness-110" />
    ) : (
        <span className="grid size-11 place-items-center rounded-lg bg-surface-3 text-[10px] font-medium text-muted">{provider.name.slice(0, 3)}</span>
    )
    return (
        <Tooltip content={provider.name}>
            {href ? (
                <a href={href} target="_blank" rel="noopener noreferrer" aria-label={provider.name}>
                    {logo}
                </a>
            ) : (
                <span>{logo}</span>
            )}
        </Tooltip>
    )
}

// The cast: the actor's photo and name, and the character they play.
function Cast({ cast }: { cast: CharacterEdge[] }) {
    const [all, setAll] = useState(false)
    const shown = all ? cast : cast.slice(0, 12)
    return (
        <section>
            <div className="mb-4 flex items-baseline justify-between gap-3">
                <h3 className="text-lg font-semibold tracking-tight">Cast</h3>
                {cast.length > 12 && (
                    <button onClick={() => setAll(a => !a)} className="text-[13px] font-medium text-muted transition-colors hover:text-fg">
                        {all ? "Show less" : `Show all ${cast.length}`}
                    </button>
                )}
            </div>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-3">
                {shown.map((c, i) => {
                    const actor = c.voiceActors?.[0]
                    const photo = actor?.image?.large || c.node.image?.large
                    return (
                        <div key={`${c.node.id}-${i}`} className="card flex items-center gap-3 overflow-hidden p-0">
                            {photo ? (
                                <img src={img(photo)} alt="" loading="lazy" className="h-[72px] w-12 shrink-0 object-cover" />
                            ) : (
                                <span className="grid h-[72px] w-12 shrink-0 place-items-center bg-surface-3 text-subtle">
                                    <UserRound className="size-5" />
                                </span>
                            )}
                            <div className="min-w-0 py-2 pr-3">
                                <p className="truncate text-sm font-semibold">{actor?.name.full || c.node.name.full}</p>
                                {actor && c.node.name.full && <p className="truncate text-xs text-muted">{c.node.name.full}</p>}
                            </div>
                        </div>
                    )
                })}
            </div>
        </section>
    )
}

// Directors, creators and writers, one entry per person with all their jobs.
function Crew({ crew }: { crew: CrewMember[] }) {
    const people = new Map<number, { name: string; image?: string; jobs: string[] }>()
    for (const c of crew) {
        const p = people.get(c.id) ?? { name: c.name, image: c.image, jobs: [] }
        if (!p.jobs.includes(c.job)) p.jobs.push(c.job)
        people.set(c.id, p)
    }
    return (
        <section>
            <h3 className="mb-4 flex items-center gap-2 text-lg font-semibold tracking-tight">
                <Users className="size-4 text-subtle" /> Crew
            </h3>
            <div className="flex flex-wrap gap-3">
                {[...people.entries()].map(([id, p]) => (
                    <div key={id} className="flex items-center gap-3 rounded-lg bg-white/[0.04] py-1.5 pr-4 pl-1.5">
                        {p.image ? (
                            <img src={img(p.image)} alt="" loading="lazy" className="size-9 rounded-full object-cover" />
                        ) : (
                            <span className="grid size-9 place-items-center rounded-full bg-surface-3 text-subtle">
                                <UserRound className="size-4" />
                            </span>
                        )}
                        <div>
                            <p className="text-sm font-medium">{p.name}</p>
                            <p className="text-xs text-subtle">{p.jobs.join(", ")}</p>
                        </div>
                    </div>
                ))}
            </div>
        </section>
    )
}

// The YouTube trailer, played in the page once clicked (nothing loads from
// YouTube before that).
function Trailer({ id, thumbnail }: { id: string; thumbnail?: string }) {
    const [playing, setPlaying] = useState(false)
    const thumb = thumbnail || `https://i.ytimg.com/vi/${id}/hqdefault.jpg`
    return (
        <section>
            <div className="mb-4 flex items-baseline justify-between gap-3">
                <h3 className="text-lg font-semibold tracking-tight">Trailer</h3>
                <a href={`https://www.youtube.com/watch?v=${id}`} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-[13px] font-medium text-muted hover:text-fg">
                    YouTube <ExternalLink className="size-3.5" />
                </a>
            </div>
            <div className="relative w-full max-w-3xl overflow-hidden rounded-lg bg-black ring-1 ring-white/10">
                {playing ? (
                    <iframe
                        src={`https://www.youtube-nocookie.com/embed/${encodeURIComponent(id)}?autoplay=1&rel=0`}
                        title="Trailer"
                        allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
                        allowFullScreen
                        className="aspect-video w-full"
                    />
                ) : (
                    <button onClick={() => setPlaying(true)} className="group relative block w-full" aria-label="Play the trailer">
                        <img src={img(thumb)} alt="" className="aspect-video w-full object-cover transition-[filter] duration-300 group-hover:brightness-110" />
                        <span className="absolute inset-0 grid place-items-center bg-black/35">
                            <span className={cn("flex items-center gap-2 rounded-full bg-white px-4 py-2 text-sm font-medium text-neutral-950 shadow-lg shadow-black/40")}>
                                <Play className="size-4 fill-current" /> Play trailer
                            </span>
                        </span>
                    </button>
                )}
            </div>
        </section>
    )
}
