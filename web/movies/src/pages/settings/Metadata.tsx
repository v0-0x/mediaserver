import { useQueryClient } from "@tanstack/react-query"
import { Trash2 } from "lucide-react"
import { toast } from "@/lib/toast"
import { Badge, Button, Select } from "@/components/ui"
import { api } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { Ext, Group, LanOnly, Password, Row, type SectionProps, Stack } from "./parts"

const LANGUAGES = [
    ["en-US", "English (US)"],
    ["en-GB", "English (UK)"],
    ["fr-FR", "French"],
    ["fr-CA", "French (Canada)"],
    ["de-DE", "German"],
    ["es-ES", "Spanish (Spain)"],
    ["es-MX", "Spanish (Latin America)"],
    ["it-IT", "Italian"],
    ["pt-BR", "Portuguese (Brazil)"],
    ["pt-PT", "Portuguese (Portugal)"],
    ["nl-NL", "Dutch"],
    ["sv-SE", "Swedish"],
    ["da-DK", "Danish"],
    ["nb-NO", "Norwegian"],
    ["fi-FI", "Finnish"],
    ["pl-PL", "Polish"],
    ["cs-CZ", "Czech"],
    ["ru-RU", "Russian"],
    ["uk-UA", "Ukrainian"],
    ["tr-TR", "Turkish"],
    ["ja-JP", "Japanese"],
    ["ko-KR", "Korean"],
    ["zh-CN", "Chinese (Simplified)"],
    ["zh-TW", "Chinese (Traditional)"],
    ["hi-IN", "Hindi"],
].map(([value, label]) => ({ value, label }))

const REGIONS = [
    ["US", "United States"],
    ["GB", "United Kingdom"],
    ["CA", "Canada"],
    ["AU", "Australia"],
    ["NZ", "New Zealand"],
    ["IE", "Ireland"],
    ["FR", "France"],
    ["BE", "Belgium"],
    ["CH", "Switzerland"],
    ["DE", "Germany"],
    ["AT", "Austria"],
    ["NL", "Netherlands"],
    ["ES", "Spain"],
    ["IT", "Italy"],
    ["PT", "Portugal"],
    ["SE", "Sweden"],
    ["NO", "Norway"],
    ["DK", "Denmark"],
    ["FI", "Finland"],
    ["PL", "Poland"],
    ["BR", "Brazil"],
    ["MX", "Mexico"],
    ["AR", "Argentina"],
    ["JP", "Japan"],
    ["KR", "South Korea"],
    ["IN", "India"],
].map(([value, label]) => ({ value, label }))

// A value not in the list (typed in the settings file) stays selectable.
const withCurrent = (options: { value: string; label: string }[], current: string) =>
    current && !options.some(o => o.value === current) ? [{ value: current, label: current }, ...options] : options

// Where every movie's and show's data comes from: The Movie Database.
export function MetadataSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    const qc = useQueryClient()
    const t = draft.tmdb
    const savedKey = status?.settings.tmdb?.apiKey ?? ""
    const key = t?.apiKey ?? ""
    const looksLike = !key ? "" : key.startsWith("eyJ") ? "API Read Access Token (v4)" : /^[0-9a-f]{32}$/i.test(key) ? "API key (v3)" : "not a TMDB key or token"
    const clearCache = async () => {
        try {
            await api.post("/api/cache/clear")
            qc.invalidateQueries()
            toast.success("Cached metadata cleared")
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <>
            <Group title="TMDB" description="KumoBox gets every title, poster, season, episode and where-to-watch listing from The Movie Database (themoviedb.org). Its API is free for personal use.">
                <Row label="Status" help={status?.tmdbConfigured ? "Search, Discover and the calendar work." : "Without a key, only your library and players work."}>
                    {status?.tmdbConfigured ? <Badge tone="green">Connected</Badge> : <Badge tone="amber">No key yet</Badge>}
                </Row>
                <Stack
                    label="API key or read access token"
                    help={
                        <>
                            Get one at <Ext href="https://www.themoviedb.org/settings/api">themoviedb.org › Settings › API</Ext> (create a free account, then request a
                            developer key). Either the <b>API Key</b> (v3, 32 characters) or the <b>API Read Access Token</b> (v4, a long token starting with eyJ) works.
                            {looksLike && <> This looks like {looksLike.startsWith("not") ? "" : "an "}{looksLike}.</>}
                            {key !== savedKey && " Save to use it."}
                        </>
                    }
                >
                    {/* Devices on the network don't see the key, nor change it. */}
                    <Password value={key} disabled={lan} onChange={v => set("tmdb", { apiKey: v.trim() })} placeholder={lan ? "" : "Paste your TMDB API key or token"} />
                </Stack>
                {lan && <LanOnly />}
            </Group>
            <Group title="Language & region">
                <Row label="Language" help="Titles, overviews and episode names, when TMDB has them in this language (English otherwise).">
                    <Select className="w-56" value={t?.language ?? "en-US"} onChange={v => set("tmdb", { language: v })} options={withCurrent(LANGUAGES, t?.language ?? "")} />
                </Row>
                <Row label="Region" help="Release dates, age ratings and where to watch are for this country.">
                    <Select className="w-56" value={t?.region ?? "US"} onChange={v => set("tmdb", { region: v })} options={withCurrent(REGIONS, t?.region ?? "")} />
                </Row>
                <Row label="Cached metadata" help="Changes apply to what's fetched from now on. Clear the cache to fetch everything again in the new language.">
                    <Button variant="subtle" size="sm" icon={<Trash2 className="size-4" />} onClick={clearCache}>
                        Clear cache
                    </Button>
                </Row>
            </Group>
            <p className="text-xs text-subtle">This product uses the TMDB API but is not endorsed or certified by TMDB. Where-to-watch data is provided by JustWatch.</p>
        </>
    )
}
