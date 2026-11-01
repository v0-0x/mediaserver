import { useQuery, useQueryClient } from "@tanstack/react-query"
import { KeyRound, Plus, ShieldCheck, X } from "lucide-react"
import { useEffect, useState } from "react"
import { api } from "@/lib/api"
import { toast } from "@/lib/toast"
import type { DeviceParental, Media, ParentalRules } from "@/lib/types"
import { cn, cover, GENRES, img, relativeTime, title } from "@/lib/utils"
import { MediaPicker } from "./MediaPicker"
import { Badge, Button, Dialog, EmptyState, Input, Progress, Switch, Tabs } from "./ui"

// A device's parental controls, set on the computer (or Aniko Hub) that
// shares with it: what it may watch, and what it watched. Its Aniko keeps
// them, also away from home, until they're lifted here (or with the PIN on
// the device).

type Named = { id: number; title: string; cover: string }

// host: what sets them, "this computer" or "the hub".
export function ParentalDialog({ device, host = "this computer", open, onOpenChange }: { device: { id: string; name: string }; host?: string; open: boolean; onOpenChange: (v: boolean) => void }) {
    const qc = useQueryClient()
    const key = ["parental", device.id]
    const { data } = useQuery({
        queryKey: key,
        queryFn: () => api.get<DeviceParental>(`/api/sharing/peers/${encodeURIComponent(device.id)}/parental`),
        enabled: open,
        refetchInterval: open ? 15000 : false,
    })
    const [tab, setTab] = useState<"rules" | "watched">("rules")
    const [rules, setRules] = useState<ParentalRules>({})
    const [names, setNames] = useState<Record<number, Named>>({})
    const [pin, setPin] = useState("")
    const [removePin, setRemovePin] = useState(false)
    const [picking, setPicking] = useState<"" | "allowed" | "blocked">("")
    const [busy, setBusy] = useState(false)
    // Each time it opens: as set now.
    useEffect(() => {
        if (!open || !data) return
        setRules(data.rules ?? {})
        setNames(Object.fromEntries(data.anime.map(a => [a.id, a])))
        setPin("")
        setRemovePin(false)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, data?.rules?.setAt, !!data])
    const genres = rules.genres ?? []
    const toggleGenre = (g: string) => setRules(r => ({ ...r, genres: genres.includes(g) ? genres.filter(x => x !== g) : [...genres, g] }))
    const add = (which: "allowed" | "blocked", m: Media) => {
        setNames(n => ({ ...n, [m.id]: { id: m.id, title: title(m), cover: cover(m, "thumb") } }))
        setRules(r => ({ ...r, [which]: [...(r[which] ?? []).filter(id => id !== m.id), m.id] }))
    }
    const remove = (which: "allowed" | "blocked", id: number) => setRules(r => ({ ...r, [which]: (r[which] ?? []).filter(x => x !== id) }))
    const save = async (next: ParentalRules, newPin: string) => {
        if (newPin && newPin !== "-" && (newPin.length < 4 || newPin.length > 12)) {
            toast.error("A PIN of 4 to 12 characters")
            return
        }
        setBusy(true)
        try {
            await api.post(`/api/sharing/peers/${encodeURIComponent(device.id)}/parental`, { ...next, newPin })
            await qc.invalidateQueries({ queryKey: key })
            qc.invalidateQueries({ queryKey: ["sharing"] })
            qc.invalidateQueries({ queryKey: ["hub", "overview"] })
            toast.success(active(next) ? `Parental controls saved: ${device.name} gets them in a few seconds` : `Parental controls off for ${device.name}`)
            onOpenChange(false)
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setBusy(false)
        }
    }
    const on = active(data?.rules ?? null)
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title={`Parental controls · ${device.name}`}
            description={`What it may watch. Its Aniko hides the rest and won't open or play it, also away from home; ${host} shares nothing blocked with it.`}
            className="w-[min(94vw,640px)]"
            footer={
                tab === "rules" ? (
                    <>
                        {on && (
                            <Button variant="ghost" disabled={busy} onClick={() => save({}, "-")}>
                                Turn off
                            </Button>
                        )}
                        <Button variant="primary" loading={busy} onClick={() => save(rules, removePin ? "-" : pin.trim())}>
                            Save
                        </Button>
                    </>
                ) : undefined
            }
        >
            <Tabs
                variant="underline"
                className="-mt-1 mb-4"
                value={tab}
                onChange={setTab}
                tabs={[
                    { value: "rules", label: "What it may watch", icon: <ShieldCheck /> },
                    { value: "watched", label: "What it watched", count: data?.watched.length || undefined },
                ]}
            />
            {tab === "rules" ? (
                <div className="flex max-h-[60vh] flex-col gap-5 overflow-y-auto pr-1">
                    <Row label="Block 18+ anime" help="AniList's adult anime and manga, Hentai and Ecchi, and anything AniList tags with sexual content (nudity and the like): hidden, and never played.">
                        <Switch label="Block 18+ anime" checked={!!rules.noAdult} onChange={v => setRules(r => ({ ...r, noAdult: v }))} />
                    </Row>
                    <div>
                        <p className="text-sm font-medium">Blocked genres</p>
                        <p className="mt-0.5 mb-2 text-[12.5px] text-muted">Anime with any of these are blocked.</p>
                        <div className="flex flex-wrap gap-1.5">
                            {GENRES.map(g => {
                                // Ecchi goes with 18+ anyway.
                                const with18 = !!rules.noAdult && ADULT_GENRES.includes(g)
                                const on = with18 || genres.includes(g)
                                return (
                                    <button
                                        key={g}
                                        onClick={() => toggleGenre(g)}
                                        disabled={with18}
                                        title={with18 ? "Blocked with 18+" : undefined}
                                        aria-pressed={on}
                                        className={cn(
                                            "focus-ring rounded-full border px-3 py-1 text-[13px] transition-colors disabled:cursor-default",
                                            on ? "border-rose-400/60 bg-rose-500/15 text-rose-200" : "border-line text-muted hover:border-line-strong hover:text-fg",
                                        )}
                                    >
                                        {g}
                                        {with18 && <span className="ml-1 text-[11px] text-rose-300/80">18+</span>}
                                    </button>
                                )
                            })}
                        </div>
                    </div>
                    <Row label="Only these anime" help="Nothing else at all: the genres and 18+ don't matter then.">
                        <Switch label="Only these anime" checked={!!rules.onlyAllowed} onChange={v => setRules(r => ({ ...r, onlyAllowed: v }))} />
                    </Row>
                    {rules.onlyAllowed && <AnimeList ids={rules.allowed ?? []} names={names} onRemove={id => remove("allowed", id)} onAdd={() => setPicking("allowed")} addLabel="Allow an anime" />}
                    <div>
                        <p className="text-sm font-medium">Always blocked</p>
                        <p className="mt-0.5 mb-2 text-[12.5px] text-muted">These anime, whatever else is allowed.</p>
                        <AnimeList ids={rules.blocked ?? []} names={names} onRemove={id => remove("blocked", id)} onAdd={() => setPicking("blocked")} addLabel="Block an anime" />
                    </div>
                    <div>
                        <p className="flex items-center gap-2 text-sm font-medium">
                            PIN on the device
                            {data?.hasPin && !removePin && <Badge tone="green">Set</Badge>}
                        </p>
                        <p className="mt-0.5 mb-2 text-[12.5px] text-muted">
                            Lifts them on the device itself, for when {host} is gone for good (on the device: Settings › Other devices). While {host} is around, it sets them again.
                        </p>
                        <div className="flex items-center gap-2">
                            <Input
                                type="password"
                                inputMode="numeric"
                                autoComplete="new-password"
                                icon={<KeyRound className="size-4" />}
                                placeholder={data?.hasPin ? "A new PIN (or leave it)" : "4 to 12 digits (optional)"}
                                value={pin}
                                disabled={removePin}
                                onChange={e => setPin(e.target.value)}
                            />
                            {data?.hasPin && (
                                <Button size="sm" variant="ghost" onClick={() => setRemovePin(v => !v)}>
                                    {removePin ? "Keep the PIN" : "Remove the PIN"}
                                </Button>
                            )}
                        </div>
                    </div>
                </div>
            ) : (
                <Watched list={data?.watched ?? []} on={on} />
            )}
            <MediaPicker
                open={!!picking}
                onOpenChange={v => !v && setPicking("")}
                heading={picking === "allowed" ? "Allow an anime" : "Block an anime"}
                onPick={m => {
                    if (picking) add(picking, m)
                    setPicking("")
                }}
            />
        </Dialog>
    )
}

function active(r: ParentalRules | null) {
    return !!r && (!!r.noAdult || !!r.genres?.length || !!r.blocked?.length || !!r.onlyAllowed)
}

function Row({ label, help, children }: { label: string; help: string; children: React.ReactNode }) {
    return (
        <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
                <p className="text-sm font-medium">{label}</p>
                <p className="mt-0.5 text-[12.5px] text-muted">{help}</p>
            </div>
            {children}
        </div>
    )
}

function AnimeList({ ids, names, onRemove, onAdd, addLabel }: { ids: number[]; names: Record<number, Named>; onRemove: (id: number) => void; onAdd: () => void; addLabel: string }) {
    return (
        <div className="flex flex-col gap-1.5">
            {ids.map(id => {
                const n = names[id]
                return (
                    <div key={id} className="flex items-center gap-3 rounded-lg bg-white/[0.03] px-2 py-1.5">
                        {n?.cover ? <img src={img(n.cover)} alt="" className="h-10 w-7 shrink-0 rounded object-cover" /> : <div className="h-10 w-7 shrink-0 rounded bg-surface-3" />}
                        <span className="min-w-0 flex-1 truncate text-sm">{n?.title || `Anime ${id}`}</span>
                        <button onClick={() => onRemove(id)} className="focus-ring grid size-7 place-items-center rounded-md text-muted hover:bg-white/[0.06] hover:text-fg" aria-label={`Remove ${n?.title || id}`}>
                            <X className="size-4" />
                        </button>
                    </div>
                )
            })}
            <Button size="sm" variant="subtle" icon={<Plus className="size-4" />} className="self-start" onClick={onAdd}>
                {addLabel}
            </Button>
        </div>
    )
}

// AniList's genres that "Block 18+" covers (Hentai isn't in GENRES).
const ADULT_GENRES = ["Ecchi", "Hentai"]

const SOURCES: Record<string, string> = { local: "From a library", anicli: "Streamed", stream: "Streamed", torrent: "Torrent" }

function Watched({ list, on }: { list: DeviceParental["watched"]; on: boolean }) {
    if (!list.length)
        return (
            <EmptyState icon={<ShieldCheck />} title="Nothing yet">
                {on ? "What it watches shows here, a few seconds after it does." : "Turn parental controls on, and what it watches shows here."}
            </EmptyState>
        )
    return (
        <ul className="flex max-h-[60vh] flex-col divide-y divide-line overflow-y-auto pr-1">
            {list.map(w => (
                <li key={`${w.mediaId}-${w.episode}`} className="flex items-center gap-3 py-2.5">
                    {w.cover ? <img src={img(w.cover)} alt="" className="h-14 w-10 shrink-0 rounded object-cover" /> : <div className="h-14 w-10 shrink-0 rounded bg-surface-3" />}
                    <div className="min-w-0 flex-1">
                        <p className="flex items-center gap-2 text-sm font-medium">
                            <span className="truncate">{w.title || `Anime ${w.mediaId}`}</span>
                            {w.adult && <Badge tone="red">18+</Badge>}
                        </p>
                        <p className="text-[12.5px] text-muted">
                            Episode {w.episode} · {relativeTime(w.at)} · {SOURCES[w.source] ?? w.source}
                        </p>
                        {w.duration > 0 && <Progress value={Math.min(1, w.position / w.duration)} className="mt-1.5 h-1" />}
                    </div>
                </li>
            ))}
        </ul>
    )
}
