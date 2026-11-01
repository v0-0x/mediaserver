import { AlertTriangle, AtSign, ExternalLink, Loader2, RefreshCw, Settings2, Unplug } from "lucide-react"
import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "@/lib/toast"
import { resultSummary, useLetterboxd, useLetterboxdStatus, useStatus } from "@/lib/queries"
import type { LetterboxdResult, LetterboxdStatus } from "@/lib/types"
import { img, plural, relativeTime } from "@/lib/utils"
import { Badge, Button, Dialog, Field, Input } from "./ui"

// Letterboxd's orange, green and blue dots.
export function LetterboxdMark({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 30 12" className={className} aria-hidden>
            <circle cx="6" cy="6" r="5" fill="#ff8000" />
            <circle cx="15" cy="6" r="5" fill="#00e054" fillOpacity="0.9" />
            <circle cx="24" cy="6" r="5" fill="#40bcf4" fillOpacity="0.9" />
        </svg>
    )
}

// A username as people paste it: "dave", "@dave" or a link to the profile.
export function parseLetterboxdUsername(s: string) {
    return s
        .trim()
        .replace(/^@/, "")
        .replace(/^(https?:\/\/)?(www\.)?letterboxd\.com\//i, "")
        .replace(/[/?#].*$/, "")
}

// How the sync is doing: when the diary was last read, what's waiting to be
// sent, and why the last sync failed.
export function SyncState({ lb, syncing }: { lb?: LetterboxdStatus; syncing?: boolean }) {
    if (!lb) return null
    return (
        <>
            <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-subtle">
                {syncing || lb.syncing ? (
                    <span className="flex items-center gap-1.5 text-muted">
                        <Loader2 className="size-3 animate-spin" /> Syncing…
                    </span>
                ) : (
                    <span>{lb.lastSync ? `Synced ${relativeTime(lb.lastSync)}` : "Not synced yet"}</span>
                )}
                {lb.pending > 0 && (
                    <>
                        <span aria-hidden>·</span>
                        <span className="text-amber-200/90">{plural(lb.pending, "diary entry", "diary entries")} to send</span>
                    </>
                )}
            </p>
            {lb.lastError && (
                <p className="mt-1 flex items-start gap-1.5 text-xs text-rose-300">
                    <AlertTriangle className="mt-px size-3.5 shrink-0" /> Last sync failed: {lb.lastError.replace(/^letterboxd:\s*/i, "")}
                </p>
            )}
        </>
    )
}

// What a sync or an import did, with the titles that couldn't be found.
export function ResultDetails({ result, label, files }: { result: LetterboxdResult; label?: string; files?: string[] | null }) {
    const unmatched = result.unmatched ?? []
    return (
        <div className="rounded-lg bg-white/[0.03] px-4 py-3">
            <div className="flex flex-wrap items-center gap-2">
                {label && <span className="mr-1 text-xs font-medium text-muted">{label}</span>}
                <Badge tone="green">{result.added} added</Badge>
                <Badge tone="blue">{result.updated} updated</Badge>
                {!!result.diary && <Badge tone="brand">{plural(result.diary, "diary entry", "diary entries")}</Badge>}
                <Badge>{result.skipped} unchanged</Badge>
                {unmatched.length > 0 && <Badge tone="amber">{unmatched.length} not found</Badge>}
            </div>
            {!!files?.length && <p className="mt-2 text-xs text-subtle">Read {files.join(", ")}</p>}
            {unmatched.length > 0 && (
                <details className="mt-2.5 text-sm">
                    <summary className="cursor-pointer text-muted hover:text-fg">{plural(unmatched.length, "title")} couldn't be found on TMDB</summary>
                    <ul className="mt-2 max-h-56 overflow-y-auto rounded-lg bg-black/25 p-3 text-xs leading-relaxed text-fg/80">
                        {unmatched.map((t, i) => (
                            <li key={i}>{t}</li>
                        ))}
                    </ul>
                </details>
            )}
        </div>
    )
}

// Connecting a Letterboxd account takes only its username: KumoBox reads the
// member's public diary and exports what's watched here for Letterboxd's
// importer. Connected, the dialog shows the account and syncs it. Devices on
// the network see the account, but it's connected on the computer KumoBox
// runs on.
export function LetterboxdDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
    const { data: status } = useStatus()
    const { data: lb } = useLetterboxdStatus(open)
    const actions = useLetterboxd()
    const [username, setUsername] = useState("")
    const [result, setResult] = useState<{ label: string; result: LetterboxdResult } | null>(null)
    useEffect(() => {
        if (open) {
            setUsername("")
            setResult(null)
        }
    }, [open])
    const viewer = status?.letterboxd
    const connected = !!viewer || !!lb?.connected
    const lan = status?.client === "lan"

    const connect = () => {
        const name = parseLetterboxdUsername(username)
        if (!name) return
        actions.connect.mutate(name, {
            onSuccess: s => {
                toast.success(`Connected to Letterboxd as ${s.displayName || s.username || name}`)
                if (s.syncError) toast.warning(`Couldn't read your diary yet: ${s.syncError}`)
                if (s.result) setResult({ label: "First sync", result: s.result })
            },
        })
    }
    const sync = () =>
        actions.sync.mutate(undefined, {
            onSuccess: r => {
                toast.success(`Synced with Letterboxd: ${resultSummary(r)}`)
                setResult({ label: "Last sync", result: r })
            },
        })
    const disconnect = () =>
        actions.disconnect.mutate(undefined, {
            onSuccess: () => {
                toast.success("Disconnected from Letterboxd")
                setResult(null)
            },
        })

    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title={connected ? "Letterboxd" : "Connect Letterboxd"}
            description={connected ? undefined : "Bring your diary, ratings and watchlist to KumoBox, and send back what you watch here."}
        >
            {connected ? (
                <div className="flex flex-col gap-5">
                    <div className="flex items-center gap-4 rounded-lg bg-white/[0.03] p-4">
                        {viewer?.avatar?.large ? (
                            <img src={img(viewer.avatar.large)} alt="" className="size-12 rounded-full object-cover" />
                        ) : (
                            <span className="grid size-12 shrink-0 place-items-center rounded-full bg-surface-3">
                                <LetterboxdMark className="w-7" />
                            </span>
                        )}
                        <div className="min-w-0 flex-1">
                            <p className="truncate font-semibold">{lb?.displayName || viewer?.name || lb?.username}</p>
                            <SyncState lb={lb} syncing={actions.sync.isPending} />
                        </div>
                        {(lb?.profileUrl || viewer?.letterboxd) && (
                            <a href={lb?.profileUrl || viewer?.letterboxd} target="_blank" rel="noopener noreferrer">
                                <Button size="sm" variant="ghost" icon={<ExternalLink className="size-4" />}>
                                    Profile
                                </Button>
                            </a>
                        )}
                    </div>
                    {result && <ResultDetails label={result.label} result={result.result} />}
                    <div className="flex flex-wrap gap-2">
                        <Button variant="primary" icon={<RefreshCw className="size-4" />} loading={actions.sync.isPending} onClick={sync}>
                            Sync now
                        </Button>
                        <Link to="/settings?tab=letterboxd" onClick={() => onOpenChange(false)}>
                            <Button icon={<Settings2 className="size-4" />}>{lb?.pending ? "Send to Letterboxd…" : "Import, export & options"}</Button>
                        </Link>
                        {!lan && (
                            <Button variant="danger" className="ml-auto" icon={<Unplug className="size-4" />} loading={actions.disconnect.isPending} onClick={disconnect}>
                                Disconnect
                            </Button>
                        )}
                    </div>
                </div>
            ) : (
                <div className="flex flex-col gap-5">
                    <div className="flex items-start gap-4 rounded-lg bg-white/[0.03] p-5">
                        <LetterboxdMark className="mt-1 w-9 shrink-0" />
                        <p className="text-sm text-muted">
                            KumoBox reads your public Letterboxd diary and adds the films you watched, with your ratings and likes, to your list. Films you watch in KumoBox are
                            logged in its diary, ready to export to Letterboxd. No password needed.
                        </p>
                    </div>
                    {lan ? (
                        <p className="rounded-lg border border-line px-4 py-3 text-sm text-muted">Letterboxd is connected on the computer KumoBox runs on, in Settings › Letterboxd.</p>
                    ) : (
                        <>
                            <Field label="Letterboxd username" help="As in letterboxd.com/username. Your profile and diary must be public.">
                                <Input
                                    autoFocus
                                    icon={<AtSign className="size-4" />}
                                    placeholder="username"
                                    value={username}
                                    onChange={e => setUsername(e.target.value)}
                                    onKeyDown={e => e.key === "Enter" && connect()}
                                />
                            </Field>
                            <Button variant="white" loading={actions.connect.isPending} disabled={!parseLetterboxdUsername(username)} onClick={connect}>
                                {actions.connect.isPending ? "Reading your diary…" : "Connect"}
                            </Button>
                        </>
                    )}
                </div>
            )}
        </Dialog>
    )
}
