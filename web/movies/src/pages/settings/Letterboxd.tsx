import { AtSign, Check, CheckCheck, Download, ExternalLink, FileUp, ListPlus, LogIn, LogOut, RefreshCw, Send, Unplug } from "lucide-react"
import { useRef, useState } from "react"
import { toast } from "@/lib/toast"
import { LetterboxdMark, parseLetterboxdUsername, ResultDetails, SyncState } from "@/components/LetterboxdDialog"
import { Badge, Button, Input, Switch } from "@/components/ui"
import { type DiaryExport, pushSummary, resultSummary, useLetterboxd, useLetterboxdStatus, useStatus } from "@/lib/queries"
import type { LetterboxdImportResult, LetterboxdResult } from "@/lib/types"
import { cn, img, plural } from "@/lib/utils"
import { Ext, Group, Password, Row, type SectionProps, Stack } from "./parts"

// Letterboxd: the account (connected at once, not with Save), how it syncs,
// importing an export and exporting what's watched here. Devices on the
// network see it all and sync, but the account, imports and the API sign-in
// are managed on the computer KumoBox runs on.
export function LetterboxdSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const { data: lb } = useLetterboxdStatus()
    const actions = useLetterboxd()
    const [username, setUsername] = useState("")
    const [result, setResult] = useState<{ label: string; result: LetterboxdResult } | null>(null)
    const viewer = status?.letterboxd
    const connected = !!viewer || !!lb?.connected
    const lan = status?.client === "lan"
    const l = draft.letterboxd

    const connect = () => {
        const name = parseLetterboxdUsername(username)
        if (!name) return
        actions.connect.mutate(name, {
            onSuccess: s => {
                toast.success(`Connected to Letterboxd as ${s.displayName || s.username || name}`)
                if (s.syncError) toast.warning(`Couldn't read your diary yet: ${s.syncError}`)
                if (s.result) setResult({ label: "First sync", result: s.result })
                setUsername("")
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
        <>
            <Group title="Account">
                {connected ? (
                    <>
                        <div className="flex flex-wrap items-center justify-between gap-4 px-4 py-4">
                            <div className="flex min-w-0 items-center gap-3">
                                {viewer?.avatar?.large ? (
                                    <img src={img(viewer.avatar.large)} alt="" className="size-11 rounded-full object-cover" />
                                ) : (
                                    <span className="grid size-11 shrink-0 place-items-center rounded-full bg-surface-3">
                                        <LetterboxdMark className="w-6" />
                                    </span>
                                )}
                                <div className="min-w-0">
                                    <p className="truncate font-semibold">
                                        {lb?.displayName || viewer?.name || lb?.username}
                                        {(lb?.profileUrl || viewer?.letterboxd) && (
                                            <span className="ml-2 text-xs font-normal">
                                                <Ext href={lb?.profileUrl || viewer?.letterboxd || ""}>@{lb?.username || "profile"}</Ext>
                                            </span>
                                        )}
                                    </p>
                                    <SyncState lb={lb} syncing={actions.sync.isPending} />
                                </div>
                            </div>
                            <div className="flex gap-2">
                                <Button size="sm" variant="primary" icon={<RefreshCw className="size-4" />} loading={actions.sync.isPending} onClick={sync}>
                                    Sync now
                                </Button>
                                {!lan && (
                                    <Button size="sm" variant="danger" icon={<Unplug className="size-4" />} loading={actions.disconnect.isPending} onClick={disconnect}>
                                        Disconnect
                                    </Button>
                                )}
                            </div>
                        </div>
                        {result && (
                            <div className="px-4 py-3.5">
                                <ResultDetails label={result.label} result={result.result} />
                            </div>
                        )}
                    </>
                ) : lan ? (
                    <Row label="Not connected" help="Letterboxd is connected on the computer KumoBox runs on.">
                        <LetterboxdMark className="w-8" />
                    </Row>
                ) : (
                    <Stack
                        label="Connect your Letterboxd account"
                        help="Your username, as in letterboxd.com/username. KumoBox reads your public diary: the films you log there, with your ratings and likes, come to your list. No password needed."
                    >
                        <div className="flex gap-2">
                            <Input icon={<AtSign className="size-4" />} placeholder="username" value={username} onChange={e => setUsername(e.target.value)} onKeyDown={e => e.key === "Enter" && connect()} />
                            <Button variant="primary" loading={actions.connect.isPending} disabled={!parseLetterboxdUsername(username)} onClick={connect}>
                                Connect
                            </Button>
                        </div>
                    </Stack>
                )}
            </Group>

            <Group title="Sync" description="What the diary brings and what KumoBox keeps for Letterboxd.">
                <Row label="Sync automatically" help="Read your Letterboxd diary in the background (its last 50 entries; older ones come with an export, below).">
                    <Switch checked={l?.autoSync ?? false} onChange={v => set("letterboxd", { autoSync: v })} />
                </Row>
                {l?.autoSync && (
                    <Row label="Every (minutes)" help="At least 15.">
                        <Input type="number" min={15} className="w-24" value={l.syncMinutes} onChange={e => set("letterboxd", { syncMinutes: Number(e.target.value) })} />
                    </Row>
                )}
                <Row label="Log the movies I watch" help="A movie watched to the end (or marked watched) in KumoBox gets a diary entry, with your rating, ready to send to Letterboxd.">
                    <Switch checked={l?.logWatched ?? false} onChange={v => set("letterboxd", { logWatched: v })} />
                </Row>
                <Row label="Import my watchlist" help="The films of your Letterboxd watchlist go on your KumoBox watchlist (from an export, below).">
                    <Switch checked={l?.importWatchlist ?? false} onChange={v => set("letterboxd", { importWatchlist: v })} />
                </Row>
            </Group>

            <ImportGroup lan={lan} />
            <ExportGroup pending={lb?.pending ?? 0} apiConnected={!!lb?.apiConnected} />
            <ApiGroup draft={draft} set={set} lan={lan} available={!!lb?.apiAvailable} connected={!!lb?.apiConnected} member={lb?.apiMember} />
        </>
    )
}

// Letterboxd's export (a ZIP of CSV files: diary, ratings, watched,
// watchlist, likes, reviews) brings everything at once; the diary feed only
// has the last entries.
function ImportGroup({ lan }: { lan: boolean }) {
    const { importFiles } = useLetterboxd()
    const input = useRef<HTMLInputElement>(null)
    const [result, setResult] = useState<LetterboxdImportResult | null>(null)
    const pick = (list: FileList | null) => {
        const files = Array.from(list ?? [])
        if (!files.length) return
        setResult(null)
        importFiles.mutate(files, {
            onSuccess: r => {
                setResult(r)
                toast.success(`Letterboxd import: ${resultSummary(r)}`)
            },
        })
    }
    return (
        <Group title="Import from Letterboxd">
            <Stack
                label="Your Letterboxd data"
                help={
                    <>
                        On Letterboxd, open <Ext href="https://letterboxd.com/settings/data/">Settings › Data › Export your data</Ext> and upload the ZIP here (or some of its CSV
                        files): your diary, ratings, likes, reviews{" "}and watchlist come to KumoBox. Importing again only adds what's new.
                    </>
                }
            >
                {lan ? (
                    <p className="text-sm text-muted">Import your data on the computer KumoBox runs on.</p>
                ) : (
                    <div className="flex flex-wrap items-center gap-3">
                        <input
                            ref={input}
                            type="file"
                            multiple
                            accept=".zip,.csv,application/zip,text/csv"
                            className="hidden"
                            onChange={e => {
                                pick(e.target.files)
                                e.target.value = ""
                            }}
                        />
                        <Button icon={<FileUp className="size-4" />} loading={importFiles.isPending} onClick={() => input.current?.click()}>
                            Choose ZIP or CSV files…
                        </Button>
                        {importFiles.isPending && <span className="text-sm text-muted">Importing… finding every film on TMDB can take a minute.</span>}
                    </div>
                )}
            </Stack>
            {result && (
                <div className="px-4 py-3.5">
                    <ResultDetails result={result} files={result.files} />
                </div>
            )}
        </Group>
    )
}

// A step of sending the diary with a CSV.
function Step({ n, title, help, done, children }: { n: number; title: string; help?: React.ReactNode; done?: boolean; children: React.ReactNode }) {
    return (
        <div className="flex items-start gap-3 px-4 py-3.5">
            <span
                className={cn(
                    "mt-px grid size-6 shrink-0 place-items-center rounded-full text-xs font-semibold tabular-nums",
                    done ? "bg-emerald-500/15 text-emerald-300" : "bg-white/[0.07] text-muted",
                )}
            >
                {done ? <Check className="size-3.5" /> : n}
            </span>
            <div className="min-w-0 flex-1">
                <p className="text-sm font-medium">{title}</p>
                {help && <p className="mt-0.5 text-xs text-subtle">{help}</p>}
                <div className="mt-2.5 flex flex-wrap items-center gap-2">{children}</div>
            </div>
        </div>
    )
}

// What's watched in KumoBox goes to Letterboxd as a CSV its importer takes
// (or straight away, with API access): download it, import it on
// letterboxd.com, then mark its entries sent so the next file only has new
// ones.
function ExportGroup({ pending, apiConnected }: { pending: number; apiConnected: boolean }) {
    const { exportCsv, exportWatchlist, markExported, push } = useLetterboxd()
    const [exported, setExported] = useState<DiaryExport | null>(null)
    const [opened, setOpened] = useState(false)
    const exportFile = (all: boolean) =>
        exportCsv.mutate(all, {
            onSuccess: x => {
                setExported(x)
                if (!x.count) toast.info("Nothing to export yet")
            },
        })
    const markSent = () =>
        markExported.mutate(exported?.ids, {
            onSuccess: r => {
                toast.success(r.marked ? `Marked ${plural(r.marked, "diary entry", "diary entries")} as on Letterboxd` : "Nothing left to mark")
                setExported(null)
                setOpened(false)
            },
        })
    return (
        <Group
            title="Send to Letterboxd"
            description="Movies you watch in KumoBox are logged in its diary. Letterboxd takes them as a CSV file: download it, import it on letterboxd.com, then tell KumoBox so the next file only has new entries."
        >
            <Row label="Waiting to be sent" help={pending ? `${plural(pending, "diary entry", "diary entries")} not on Letterboxd yet` : "Everything is on Letterboxd"}>
                <Badge tone={pending ? "amber" : "green"}>{pending}</Badge>
            </Row>
            <Step
                n={1}
                title="Download the CSV"
                done={!!exported?.count}
                help={exported?.count ? `Downloaded ${plural(exported.count, "diary entry", "diary entries")}${exported.all ? " (all of them)" : ""}.` : undefined}
            >
                <Button size="sm" variant="primary" icon={<Download className="size-4" />} disabled={!pending} loading={exportCsv.isPending && exportCsv.variables === false} onClick={() => exportFile(false)}>
                    {pending ? `Download ${plural(pending, "new entry", "new entries")}` : "No new entries"}
                </Button>
                <Button size="sm" variant="ghost" icon={<Download className="size-4" />} loading={exportCsv.isPending && exportCsv.variables === true} onClick={() => exportFile(true)}>
                    Download all
                </Button>
            </Step>
            <Step n={2} title="Import it on Letterboxd" done={opened} help="Choose the file there, check how Letterboxd matched the films, and import.">
                <a href="https://letterboxd.com/import/" target="_blank" rel="noopener noreferrer" onClick={() => setOpened(true)}>
                    <Button size="sm" variant="subtle" icon={<ExternalLink className="size-4" />}>
                        Open letterboxd.com/import
                    </Button>
                </a>
            </Step>
            <Step
                n={3}
                title="Mark them as sent"
                help={
                    exported?.ids.length
                        ? "The entries of the file you downloaded won't be in the next one."
                        : pending
                          ? `Marks the ${plural(pending, "entry", "entries")} waiting as on Letterboxd.`
                          : undefined
                }
            >
                <Button size="sm" variant="success" icon={<CheckCheck className="size-4" />} disabled={!pending} loading={markExported.isPending} onClick={markSent}>
                    I imported it
                </Button>
            </Step>
            {apiConnected && (
                <Row label="Send with the API" help="Logs the waiting entries on your account right away, without a file.">
                    <Button
                        size="sm"
                        icon={<Send className="size-4" />}
                        disabled={!pending}
                        loading={push.isPending}
                        onClick={() => push.mutate(undefined, { onSuccess: r => toast.success(`Letterboxd: ${pushSummary(r)}`) })}
                    >
                        Send now
                    </Button>
                </Row>
            )}
            <Row label="Your watchlist" help="The films of your KumoBox watchlist as a CSV, for the Import button of your Letterboxd watchlist.">
                <Button
                    size="sm"
                    variant="subtle"
                    icon={<ListPlus className="size-4" />}
                    loading={exportWatchlist.isPending}
                    onClick={() => exportWatchlist.mutate(undefined, { onSuccess: n => !n && toast.info("Your watchlist is empty") })}
                >
                    Download watchlist
                </Button>
            </Row>
        </Group>
    )
}

// Letterboxd's API is granted on request: with an app's credentials KumoBox
// logs straight to the account.
function ApiGroup({ draft, set, lan, available, connected, member }: SectionProps & { lan: boolean; available: boolean; connected: boolean; member?: string }) {
    const { apiLogin, apiLogout } = useLetterboxd()
    const { data: status } = useStatus()
    const [user, setUser] = useState("")
    const [password, setPassword] = useState("")
    const l = draft.letterboxd
    const saved = status?.settings.letterboxd
    const changed = (l?.apiClientId ?? "") !== (saved?.apiClientId ?? "") || (l?.apiClientSecret ?? "") !== (saved?.apiClientSecret ?? "")
    const login = () =>
        apiLogin.mutate(
            { username: user.trim(), password },
            {
                onSuccess: () => {
                    toast.success("Signed in to the Letterboxd API")
                    setPassword("")
                },
            },
        )
    const description =
        "Letterboxd gives API access on request (letterboxd.com/api-beta). With an app's client ID and secret, KumoBox logs what you watch to your account directly, without CSV files."
    if (lan)
        return (
            <Group title="API access (optional)" description={description}>
                <Row label={connected ? `Signed in${member ? ` as ${member}` : ""}` : available ? "Not signed in" : "Not set up"} help="API access is set up on the computer KumoBox runs on.">
                    <Badge tone={connected ? "green" : "gray"}>{connected ? "Connected" : "Off"}</Badge>
                </Row>
            </Group>
        )
    return (
        <Group title="API access (optional)" description={description}>
            <Stack label="Client ID">
                <Input value={l?.apiClientId ?? ""} onChange={e => set("letterboxd", { apiClientId: e.target.value.trim() })} />
            </Stack>
            <Stack label="Client secret" help={changed ? "Save, then sign in below." : undefined}>
                <Password value={l?.apiClientSecret ?? ""} onChange={v => set("letterboxd", { apiClientSecret: v.trim() })} />
            </Stack>
            {available &&
                !changed &&
                (connected ? (
                    <Row label={`Signed in${member ? ` as ${member}` : ""}`} help="What you watch is sent to your Letterboxd diary in the background.">
                        <Button
                            size="sm"
                            variant="ghost"
                            icon={<LogOut className="size-4" />}
                            loading={apiLogout.isPending}
                            onClick={() => apiLogout.mutate(undefined, { onSuccess: () => toast.success("Signed out of the Letterboxd API") })}
                        >
                            Sign out
                        </Button>
                    </Row>
                ) : (
                    <Stack label="Sign in to the API" help="Your password goes to Letterboxd only; KumoBox keeps the tokens it gets back, not the password.">
                        <div className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
                            <Input placeholder="Username or email" value={user} onChange={e => setUser(e.target.value)} />
                            <Password value={password} onChange={setPassword} placeholder="Password" />
                            <Button icon={<LogIn className="size-4" />} loading={apiLogin.isPending} disabled={!user.trim() || !password} onClick={login}>
                                Sign in
                            </Button>
                        </div>
                    </Stack>
                ))}
        </Group>
    )
}
