import { useQuery } from "@tanstack/react-query"
import { Plus, X } from "lucide-react"
import { Link } from "react-router-dom"
import { Button, Input, Select, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { useProviders, useStatus } from "@/lib/queries"
import { DEFAULT_DECRYPT_URL, SITE_OFF, STREAM_SITES, type StreamSiteKey } from "@/lib/streamSites"
import type { StreamSite, StreamSiteSettings, TorrentSource, TorrentSourceSettings, TorznabIndexer } from "@/lib/types"
import { Ext, Group, LanOnly, ListInput, Password, PathInput, Row, type SectionProps, Stack, useLan } from "./parts"
import { MediaServersGroup } from "./MediaServers"
import { ProviderHealthGroup } from "./ProviderHealth"

const DEFAULT_STREMIO_CATALOG = "https://api.strem.io/addonscollection.json"

// Where movies and episodes stream from, and where downloads of streams go.
export function ProvidersSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    const { data: providers } = useProviders()
    const p = draft.providers
    const dl = draft.downloads
    const sites = p?.sites
    const setSite = (key: StreamSiteKey, v: StreamSite) => sites && set("providers", { sites: { ...sites, [key]: v } as StreamSiteSettings })
    return (
        <>
            <Group title="Online streaming">
                <Row label="Watch online" help="Stream movies and episodes from the providers below.">
                    <Switch checked={draft.onlineStream.enabled} onChange={v => set("onlineStream", { enabled: v })} />
                </Row>
                <Row label="Default provider" help="All providers: every source is listed, ranked together. A provider: its sources come first, and Play (on something not in your library) plays its best link.">
                    <Select
                        className="w-56"
                        value={draft.onlineStream.defaultProvider}
                        onChange={v => set("onlineStream", { defaultProvider: v })}
                        options={[{ value: "", label: "All providers" }, ...(providers ?? []).filter(x => x.enabled).map(x => ({ value: x.id, label: x.name }))]}
                    />
                </Row>
                <Row label="Prefer dubbed versions" help="For extensions that have titles dubbed as well as in their original version.">
                    <Switch checked={draft.onlineStream.preferDub} onChange={v => set("onlineStream", { preferDub: v })} />
                </Row>
                <Row label="Preferred quality" help="Sources are listed best first otherwise.">
                    <Select
                        className="w-40"
                        value={p?.preferredQuality ?? ""}
                        onChange={v => set("providers", { preferredQuality: v })}
                        options={[
                            { value: "", label: "Best available" },
                            { value: "2160p", label: "2160p (4K)" },
                            { value: "1080p", label: "1080p" },
                            { value: "720p", label: "720p" },
                            { value: "480p", label: "480p" },
                        ]}
                    />
                </Row>
                <Row label="Provider timeout (seconds)" help="How long each provider may take to find sources (5–120).">
                    <Input type="number" min={5} max={120} className="w-24" value={p?.timeoutSeconds ?? 20} onChange={e => set("providers", { timeoutSeconds: Number(e.target.value) })} />
                </Row>
            </Group>

            <ProviderHealthGroup lan={lan} />

            <MediaServersGroup servers={p?.mediaServers ?? []} onChange={v => set("providers", { mediaServers: v })} lan={lan} />

            <Group title="Internet Archive" description="Built in: public-domain movies and TV from archive.org.">
                <Row label="Use the Internet Archive">
                    <Switch checked={p?.archive ?? false} onChange={v => set("providers", { archive: v })} />
                </Row>
            </Group>

            <Group
                title="luffy and FreeFlix sites"
                description="The sites the luffy and FreeFlix command-line apps stream from, built in. They're unofficial sites, off until you turn them on; each can point to a mirror when it moves."
            >
                {STREAM_SITES.map(site => {
                    const cur = sites?.[site.key] ?? SITE_OFF
                    return (
                        <div key={site.key} className="flex flex-col gap-2.5 px-4 py-3.5">
                            <div className="flex items-center justify-between gap-6">
                                <div className="min-w-0">
                                    <p className="text-sm font-medium">{site.name}</p>
                                    <p className="mt-0.5 text-xs text-subtle">
                                        From {site.app} · {site.help}
                                    </p>
                                </div>
                                <Switch checked={cur.enabled} disabled={lan || !sites} onChange={v => setSite(site.key, { ...cur, enabled: v })} />
                            </div>
                            {cur.enabled && (
                                <Input
                                    value={cur.url}
                                    disabled={lan}
                                    placeholder={`Mirror address (optional, default ${site.url})`}
                                    onChange={e => setSite(site.key, { ...cur, url: e.target.value.trim() })}
                                />
                            )}
                        </div>
                    )
                })}
                {STREAM_SITES.some(x => x.decrypts && sites?.[x.key]?.enabled) && (
                    <Stack label="Decryption service" help="Vidlink, Hexa and Videasy have enc-dec.app work out their keys, as FreeFlix does: it's sent what they're asked for.">
                        <Input
                            value={sites?.decryptUrl ?? ""}
                            disabled={lan}
                            placeholder={DEFAULT_DECRYPT_URL}
                            onChange={e => sites && set("providers", { sites: { ...sites, decryptUrl: e.target.value.trim() } })}
                        />
                    </Stack>
                )}
            </Group>

            <Group
                title="Stremio addons"
                description="Addon collections the Providers page lists to install from: JSON lists of addons, like Stremio's own. Addons can also be installed by their manifest URL there."
            >
                <Stack label="Addon collections" help={`Stremio's official collection is ${DEFAULT_STREMIO_CATALOG}.`}>
                    <ListInput value={p?.stremioCatalogs ?? []} onChange={v => set("providers", { stremioCatalogs: v })} placeholder={DEFAULT_STREMIO_CATALOG} addLabel="Add collection" />
                </Stack>
                <Row label="Manage addons">
                    <Link to="/extensions?tab=stremio">
                        <Button size="sm" variant="subtle">
                            Providers & extensions
                        </Button>
                    </Link>
                </Row>
            </Group>

            <Group title="Nuvio providers" description="Repositories of Nuvio scrapers (manifest.json URLs). Their providers run in KumoBox's extension sandbox.">
                <Stack label="Repositories">
                    <ListInput value={p?.nuvioRepos ?? []} onChange={v => set("providers", { nuvioRepos: v })} placeholder="https://…/manifest.json" addLabel="Add repository" />
                </Stack>
            </Group>

            <Group title="Downloads" description="Movies and episodes downloaded from streaming providers.">
                <Stack
                    label="Download folder"
                    help={`Empty: your library folder (${status?.settings.library.dir || "not set"}). Downloads are matched automatically.${lan ? " Changed on the computer KumoBox runs on." : ""}`}
                >
                    <PathInput value={dl?.dir ?? ""} disabled={lan} onChange={v => set("downloads", { dir: v })} placeholder="Library folder" />
                </Stack>
                <Row label="Downloader" help="yt-dlp is faster and more reliable for most streams.">
                    <Select
                        className="w-40"
                        value={dl?.downloader || "auto"}
                        onChange={v => set("downloads", { downloader: v })}
                        options={[
                            { value: "auto", label: "Automatic" },
                            { value: "yt-dlp", label: "yt-dlp" },
                            { value: "ffmpeg", label: "ffmpeg" },
                        ]}
                    />
                </Row>
            </Group>
        </>
    )
}

type BuiltIn = { key: Exclude<keyof TorrentSourceSettings, "torznab">; name: string; help: string; site: string }

const BUILT_IN: BuiltIn[] = [
    { key: "yts", name: "YTS", help: "Movies, small encodes", site: "yts.mx" },
    { key: "eztv", name: "EZTV", help: "TV episodes and seasons", site: "eztvx.to" },
    { key: "pirateBay", name: "The Pirate Bay", help: "Movies and TV, through apibay", site: "apibay.org" },
    { key: "torrentsCsv", name: "Torrents-CSV", help: "An open database of torrents", site: "torrents-csv.com" },
    { key: "archive", name: "Internet Archive", help: "Public-domain movies and TV", site: "archive.org" },
]

// Where torrents are searched: Torznab indexers (Jackett, Prowlarr) and the
// built-in sources.
export function TorrentSourcesSection({ draft, set }: SectionProps) {
    const { data: providers } = useQuery({ queryKey: ["torrent-providers"], queryFn: () => api.get<{ id: string; name: string; kind: string }[]>("/api/torrents/providers") })
    // Devices on the network don't see the indexers' API keys, nor change
    // the indexers.
    const lan = useLan()
    const ts = draft.torrentSources
    const indexers = ts?.torznab ?? []
    const setIndexers = (v: TorznabIndexer[]) => set("torrentSources", { torznab: v })
    const edit = (i: number, v: Partial<TorznabIndexer>) => setIndexers(indexers.map((x, j) => (j === i ? { ...x, ...v } : x)))
    const source = (key: BuiltIn["key"]): TorrentSource => ts?.[key] ?? { enabled: false, url: "" }
    return (
        <>
            <Group title="Search">
                <Row label="Default source" help="Where the Torrents tab of a movie or show searches first.">
                    <Select
                        className="w-56"
                        value={draft.torrent.defaultProvider}
                        onChange={v => set("torrent", { defaultProvider: v })}
                        options={[{ value: "all", label: "All sources" }, ...(providers ?? []).map(x => ({ value: x.id, label: x.name + (x.kind === "extension" ? " (extension)" : "") }))]}
                    />
                </Row>
                <Row label="Preferred resolution">
                    <Select
                        className="w-40"
                        value={draft.torrent.preferredResolution}
                        onChange={v => set("torrent", { preferredResolution: v })}
                        options={[
                            { value: "", label: "Any" },
                            { value: "2160", label: "2160p" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                        ]}
                    />
                </Row>
            </Group>

            <Group
                title="Torznab indexers"
                description="Your own indexers, through Jackett or Prowlarr. In Prowlarr, an indexer's Torznab URL looks like http://127.0.0.1:9696/1/api, with Prowlarr's API key (Settings › General). In Jackett, use “Copy Torznab Feed” and Jackett's API key."
            >
                {indexers.map((x, i) => (
                    <div key={i} className="flex flex-col gap-3 px-4 py-3.5">
                        <div className="flex items-center gap-3">
                            <Switch checked={x.enabled} disabled={lan} onChange={v => edit(i, { enabled: v })} />
                            <Input value={x.name} placeholder="Name" disabled={lan} onChange={e => edit(i, { name: e.target.value })} />
                            {!lan && (
                                <button
                                    onClick={() => setIndexers(indexers.filter((_, j) => j !== i))}
                                    className="grid size-9 shrink-0 place-items-center rounded-lg text-muted transition-colors hover:bg-white/[0.06] hover:text-rose-300"
                                    aria-label={`Remove ${x.name || "indexer"}`}
                                >
                                    <X className="size-4" />
                                </button>
                            )}
                        </div>
                        <div className="grid gap-3 sm:grid-cols-[2fr_1fr]">
                            <Input value={x.url} placeholder="http://127.0.0.1:9696/1/api" disabled={lan} onChange={e => edit(i, { url: e.target.value.trim() })} />
                            <Password value={x.apiKey} placeholder={lan ? "" : "API key"} disabled={lan} onChange={v => edit(i, { apiKey: v.trim() })} />
                        </div>
                    </div>
                ))}
                {lan ? (
                    <LanOnly />
                ) : (
                    <div className="px-4 py-3.5">
                        <Button size="sm" variant="subtle" icon={<Plus className="size-4" />} onClick={() => setIndexers([...indexers, { name: "", url: "", apiKey: "", enabled: true }])}>
                            Add indexer
                        </Button>
                    </div>
                )}
            </Group>

            <Group title="Built-in sources" description="Public sites searched without an indexer. Each can point to a mirror when the site moves.">
                {BUILT_IN.map(b => {
                    const s = source(b.key)
                    return (
                        <div key={b.key} className="flex flex-col gap-2.5 px-4 py-3.5">
                            <div className="flex items-center justify-between gap-6">
                                <div className="min-w-0">
                                    <p className="text-sm font-medium">{b.name}</p>
                                    <p className="mt-0.5 text-xs text-subtle">
                                        {b.help} · <Ext href={`https://${b.site}`}>{b.site}</Ext>
                                    </p>
                                </div>
                                <Switch checked={s.enabled} onChange={v => set("torrentSources", { [b.key]: { ...s, enabled: v } } as Partial<TorrentSourceSettings>)} />
                            </div>
                            {s.enabled && (
                                <Input
                                    value={s.url}
                                    placeholder={`Mirror address (optional, default https://${b.site})`}
                                    onChange={e => set("torrentSources", { [b.key]: { ...s, url: e.target.value.trim() } } as Partial<TorrentSourceSettings>)}
                                />
                            )}
                        </div>
                    )
                })}
            </Group>
            <p className="text-xs text-subtle">
                Torrent providers from extensions are searched too. Only download what you have the right to.
            </p>
        </>
    )
}
