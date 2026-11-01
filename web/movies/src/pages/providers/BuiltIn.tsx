import { Clapperboard, Landmark, Settings2 } from "lucide-react"
import { Link } from "react-router-dom"
import { Badge, Button, Switch } from "@/components/ui"
import { useProviders, useSaveSettings, useStatus } from "@/lib/queries"
import { SITE_OFF, STREAM_SITES } from "@/lib/streamSites"
import type { ProviderInfo } from "@/lib/types"
import { ExtIcon, TabSection, TypeBadges } from "./parts"

const kindName: Record<ProviderInfo["kind"], string> = { mediaserver: "Media server", builtin: "Built in", stremio: "Stremio addon", nuvio: "Nuvio provider", extension: "Extension" }

// The Internet Archive, the sites of the luffy and FreeFlix apps, and every
// provider in the order they're asked.
export function BuiltInTab() {
    const { data: status } = useStatus()
    const settings = status?.settings
    const save = useSaveSettings()
    const { data: providers } = useProviders()
    const archive = settings?.providers?.archive ?? false
    const sites = settings?.providers?.sites
    // Devices on the network can't turn providers on or off.
    const lan = status?.client === "lan"
    return (
        <div className="flex flex-col gap-10">
            <TabSection title="Built in">
                <div className="card flex items-start gap-4 p-4">
                    <span className="grid size-12 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-fg/80">
                        <Landmark className="size-6" />
                    </span>
                    <div className="min-w-0 flex-1">
                        <p className="font-semibold">Internet Archive</p>
                        <p className="mt-0.5 text-sm text-muted">Public-domain and freely licensed films hosted by archive.org: classics, silent films, film noir, cartoons… Found by title and year.</p>
                        <div className="mt-2 flex gap-1.5">
                            <Badge>Movies</Badge>
                            <Badge tone="green">Direct links</Badge>
                        </div>
                    </div>
                    <Switch
                        checked={archive}
                        disabled={!settings || save.isPending}
                        onChange={v => settings && save.mutate({ ...settings, providers: { ...settings.providers, archive: v } })}
                    />
                </div>
            </TabSection>

            <TabSection
                title="From luffy and FreeFlix"
                description="The sites the luffy and FreeFlix command-line apps stream from, built in. They're unofficial sites, off until you turn them on; each finds movies and episodes by their TMDB id."
                actions={
                    <Link to="/settings?tab=providers">
                        <Button size="sm" icon={<Settings2 className="size-4" />}>
                            Mirrors
                        </Button>
                    </Link>
                }
            >
                <div className="grid gap-3 lg:grid-cols-2">
                    {STREAM_SITES.map(site => {
                        const cur = sites?.[site.key] ?? SITE_OFF
                        return (
                            <div key={site.key} className="card flex items-start gap-4 p-4">
                                <span className="grid size-12 shrink-0 place-items-center rounded-lg bg-white/[0.06] text-fg/80">
                                    <Clapperboard className="size-6" />
                                </span>
                                <div className="min-w-0 flex-1">
                                    <p className="font-semibold">{site.name}</p>
                                    <p className="mt-0.5 text-sm text-muted">{site.help}</p>
                                    <div className="mt-2 flex flex-wrap gap-1.5">
                                        <Badge>Movies</Badge>
                                        <Badge>Shows</Badge>
                                        <Badge tone="blue">{site.app}</Badge>
                                        {site.decrypts && <Badge tone="amber">Through enc-dec.app</Badge>}
                                    </div>
                                </div>
                                <Switch
                                    checked={cur.enabled}
                                    disabled={!settings || !sites || lan || save.isPending}
                                    onChange={v => settings && sites && save.mutate({ ...settings, providers: { ...settings.providers, sites: { ...sites, [site.key]: { ...cur, enabled: v } } } })}
                                />
                            </div>
                        )
                    })}
                </div>
            </TabSection>

            <TabSection
                title="All providers"
                description="Every source of streams, in the order they're asked (Stremio addons in your order). Streams are listed best first: direct links, then torrents; your preferred quality first."
                actions={
                    <Link to="/settings?tab=providers">
                        <Button size="sm" icon={<Settings2 className="size-4" />}>
                            Default provider, quality, timeout
                        </Button>
                    </Link>
                }
            >
                <div className="card divide-y divide-line overflow-hidden">
                    {(providers ?? []).map(p => (
                        <div key={p.id} className="flex items-center gap-3 px-4 py-2.5">
                            <ExtIcon src={p.icon} className="size-8" />
                            <div className="min-w-0 flex-1">
                                <p className="truncate text-sm font-medium">{p.name}</p>
                                <p className="truncate text-xs text-subtle">{p.id}</p>
                            </div>
                            <div className="flex shrink-0 flex-wrap justify-end gap-1.5">
                                <TypeBadges types={p.types} />
                                <Badge tone={p.kind === "builtin" || p.kind === "mediaserver" ? "brand" : p.kind === "stremio" ? "blue" : "gray"}>{kindName[p.kind]}</Badge>
                                {p.enabled ? <Badge tone="green">On</Badge> : <Badge>Off</Badge>}
                            </div>
                        </div>
                    ))}
                </div>
            </TabSection>
        </div>
    )
}
