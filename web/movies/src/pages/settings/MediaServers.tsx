import { Plus, Server, X } from "lucide-react"
import { useState } from "react"
import { Button, Input, Select } from "@/components/ui"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import type { MediaServer, MediaServerInfo, MediaServerKind } from "@/lib/types"
import { Group, LanOnly, Password } from "./parts"

const KINDS: { value: MediaServerKind; label: string; url: string; token: string }[] = [
    { value: "jellyfin", label: "Jellyfin", url: "http://192.168.1.10:8096", token: "API key (Dashboard › API Keys)" },
    { value: "emby", label: "Emby", url: "http://192.168.1.10:8096", token: "API key (Settings › API Keys)" },
    { value: "plex", label: "Plex", url: "http://192.168.1.10:32400", token: "X-Plex-Token" },
]

// The user's own Jellyfin, Emby and Plex servers: their movies and episodes
// are found by their TMDB (or IMDb) ids and played as the files they are.
// Tokens never leave this computer: devices on the network see the servers
// without them and can't change them.
export function MediaServersGroup({ servers, onChange, lan }: { servers: MediaServer[]; onChange: (v: MediaServer[]) => void; lan: boolean }) {
    const edit = (i: number, v: Partial<MediaServer>) => onChange(servers.map((x, j) => (j === i ? { ...x, ...v } : x)))
    return (
        <Group
            title="Media servers"
            description="Your own Jellyfin, Emby or Plex servers: their movies and episodes show in Watch online, matched by their TMDB or IMDb ids, and play as the files they are. Save after adding one."
        >
            {servers.map((s, i) => (
                <ServerRow key={i} s={s} lan={lan} onEdit={v => edit(i, v)} onRemove={() => onChange(servers.filter((_, j) => j !== i))} />
            ))}
            {lan ? (
                <LanOnly />
            ) : (
                <div className="px-4 py-3.5">
                    <Button size="sm" variant="subtle" icon={<Plus className="size-4" />} onClick={() => onChange([...servers, { kind: "jellyfin", name: "", url: "", token: "" }])}>
                        Add server
                    </Button>
                </div>
            )}
        </Group>
    )
}

function ServerRow({ s, lan, onEdit, onRemove }: { s: MediaServer; lan: boolean; onEdit: (v: Partial<MediaServer>) => void; onRemove: () => void }) {
    const [busy, setBusy] = useState(false)
    const kind = KINDS.find(k => k.value === s.kind) ?? KINDS[0]
    const test = async () => {
        setBusy(true)
        try {
            const info = await api.post<MediaServerInfo>("/api/providers/mediaservers/test", s)
            const who = info.userName ? ` as ${info.userName}` : info.libraries ? `, ${info.libraries} libraries` : ""
            toast.success(`Connected to ${info.name || kind.label}${info.version ? ` (${info.version})` : ""}${who}`)
            if (!s.name && info.name) onEdit({ name: info.name })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <div className="flex flex-col gap-3 px-4 py-3.5">
            <div className="flex items-center gap-3">
                <Server className="size-4 shrink-0 text-muted" />
                <Select className="w-36" value={s.kind} disabled={lan} onChange={v => onEdit({ kind: v as MediaServerKind })} options={KINDS.map(k => ({ value: k.value, label: k.label }))} />
                <Input value={s.name} placeholder="Name (optional)" disabled={lan} onChange={e => onEdit({ name: e.target.value })} />
                {!lan && (
                    <>
                        <Button size="sm" variant="subtle" loading={busy} disabled={!s.url.trim()} onClick={test}>
                            Test
                        </Button>
                        <button
                            onClick={onRemove}
                            className="grid size-9 shrink-0 place-items-center rounded-lg text-muted transition-colors hover:bg-white/[0.06] hover:text-rose-300"
                            aria-label={`Remove ${s.name || kind.label}`}
                        >
                            <X className="size-4" />
                        </button>
                    </>
                )}
            </div>
            <div className={s.kind === "plex" ? "grid gap-3 sm:grid-cols-[2fr_1fr]" : "grid gap-3 sm:grid-cols-[2fr_1fr_1fr]"}>
                <Input value={s.url} placeholder={kind.url} disabled={lan} onChange={e => onEdit({ url: e.target.value.trim() })} />
                <Password value={s.token} placeholder={lan ? "" : kind.token} disabled={lan} onChange={v => onEdit({ token: v.trim() })} />
                {s.kind !== "plex" && <Input value={s.userId ?? ""} placeholder="User id (optional)" disabled={lan} onChange={e => onEdit({ userId: e.target.value.trim() })} />}
            </div>
        </div>
    )
}
