import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowUpCircle, Download, FileText, FolderGit, Link2, Plus, RefreshCw, Trash2, X } from "lucide-react"
import { useState } from "react"
import { Badge, Button, EmptyState, IconButton, Input, Switch } from "@/components/ui"
import { api } from "@/lib/api"
import { invalidateProviders, useExtensions, useNuvioRepo, useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { ExtensionInfo, NuvioEntry, NuvioRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ExtIcon, LanNote, LogsDialog, TabSection, TypeBadges } from "./parts"

// Nuvio provider repositories (Settings › Providers keeps their URLs) and
// their providers, installed as extensions (nuvio.<id>).
export function NuvioTab() {
    const { data: status } = useStatus()
    const lan = status?.client === "lan"
    const repos = status?.settings.providers?.nuvioRepos ?? []
    const qc = useQueryClient()
    const [url, setUrl] = useState("")
    const [adding, setAdding] = useState(false)
    const add = async () => {
        setAdding(true)
        try {
            await api.post<NuvioRepo>("/api/providers/nuvio/repos", { url: url.trim() })
            setUrl("")
            qc.invalidateQueries({ queryKey: ["status"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setAdding(false)
        }
    }
    return (
        <div className="flex flex-col gap-10">
            {lan ? (
                <LanNote />
            ) : (
                <div className="card flex flex-col gap-3 p-4">
                    <p className="text-sm font-medium">Add a repository</p>
                    <div className="flex gap-2">
                        <Input
                            value={url}
                            onChange={e => setUrl(e.target.value)}
                            onKeyDown={e => e.key === "Enter" && url.trim() && add()}
                            placeholder="https://raw.githubusercontent.com/<user>/<repo>/refs/heads/main/manifest.json"
                            icon={<Link2 className="size-4" />}
                        />
                        <Button variant="primary" icon={<Plus className="size-4" />} loading={adding} disabled={!url.trim()} onClick={add}>
                            Add
                        </Button>
                    </div>
                    <p className="text-xs text-subtle">Nuvio providers are JavaScript scrapers; KumoBox runs them in its extension sandbox, which can't reach this computer or your network.</p>
                </div>
            )}
            {repos.length === 0 ? (
                <EmptyState icon={<FolderGit className="size-6" />} title="No Nuvio repositories">
                    Add a repository's manifest URL to list its providers.
                </EmptyState>
            ) : (
                repos.map(r => <Repo key={r} url={r} lan={lan} />)
            )}
        </div>
    )
}

function Repo({ url, lan }: { url: string; lan: boolean }) {
    const { data: repo, isLoading, error, refetch, isFetching } = useNuvioRepo(url)
    const { data: exts } = useExtensions()
    const qc = useQueryClient()
    const [busy, setBusy] = useState("")
    const run = async (id: string, p: () => Promise<unknown>, ok?: string) => {
        setBusy(id)
        try {
            await p()
            if (ok) toast.success(ok)
            invalidateProviders(qc)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy("")
        }
    }
    const remove = () =>
        run("remove", async () => {
            await api.del(`/api/providers/nuvio/repos?url=${encodeURIComponent(url)}`)
            qc.invalidateQueries({ queryKey: ["status"] })
        })
    const installAll = () =>
        run("all", async () => {
            const res = await api.post<{ installed: unknown[]; errors: string[] }>("/api/providers/nuvio/install-all", { repo: url })
            if (res.errors?.length) toast.warning(res.errors.join("\n"))
        })
    const ext = (e: NuvioEntry) => exts?.find(x => x.manifest.id === e.extensionId)
    return (
        <TabSection
            title={repo?.name || "Nuvio repository"}
            description={<span className="break-all">{url}</span>}
            actions={
                <>
                    <IconButton size="sm" label="Reload" onClick={() => refetch()}>
                        <RefreshCw className={cn("size-4", isFetching && "animate-spin")} />
                    </IconButton>
                    {!lan && repo && repo.providers.length > 0 && (
                        <Button size="sm" variant="primary" icon={<Download className="size-4" />} loading={busy === "all"} onClick={installAll}>
                            Install all
                        </Button>
                    )}
                    {!lan && (
                        <Button size="sm" variant="ghost" icon={<X className="size-4" />} loading={busy === "remove"} onClick={remove}>
                            Remove repository
                        </Button>
                    )}
                </>
            }
        >
            {isLoading && <div className="card h-24 shimmer" />}
            {error && (
                <EmptyState icon={<AlertTriangle className="size-6" />} title="Couldn't read the repository" action={<Button onClick={() => refetch()}>Retry</Button>}>
                    {(error as Error).message}
                </EmptyState>
            )}
            {repo && repo.providers.length === 0 && <p className="text-sm text-muted">This repository lists no provider.</p>}
            {repo && repo.providers.length > 0 && (
                <div className="card divide-y divide-line overflow-hidden">
                    {repo.providers.map(e => (
                        <EntryRow key={e.extensionId} e={e} ext={ext(e)} repo={url} lan={lan} busy={busy === e.extensionId} run={run} />
                    ))}
                </div>
            )}
        </TabSection>
    )
}

function EntryRow({
    e,
    ext,
    repo,
    lan,
    busy,
    run,
}: {
    e: NuvioEntry
    ext?: ExtensionInfo
    repo: string
    lan: boolean
    busy: boolean
    run: (id: string, p: () => Promise<unknown>, ok?: string) => void
}) {
    const [logs, setLogs] = useState(false)
    const installed = e.installed || !!ext
    const install = () => run(e.extensionId, () => api.post("/api/providers/nuvio/install", { repo, id: e.id }))
    return (
        <div className={cn("flex items-center gap-4 px-4 py-3", installed && ext && !ext.enabled && "opacity-60")}>
            <ExtIcon src={e.logo} className="size-10" />
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                    <p className="truncate font-semibold">{e.name}</p>
                    <span className="text-xs text-subtle">v{e.installedVersion || e.version}</span>
                    {e.author && <span className="truncate text-xs text-subtle">by {e.author}</span>}
                </div>
                {e.description && <p className="line-clamp-1 text-[13px] text-muted">{e.description}</p>}
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                    <TypeBadges types={e.supportedTypes} />
                    {(e.contentLanguage ?? []).slice(0, 4).map(l => (
                        <Badge key={l} className="uppercase">
                            {l}
                        </Badge>
                    ))}
                    {(e.formats ?? []).slice(0, 3).map(f => (
                        <Badge key={f}>{f}</Badge>
                    ))}
                    {!e.enabled && !installed && <Badge>Off by default</Badge>}
                    {ext?.error && <Badge tone="red">Error</Badge>}
                </div>
                {ext?.error && <p className="mt-1.5 text-xs text-rose-300">{ext.error}</p>}
            </div>
            <div className="flex shrink-0 items-center gap-1">
                {installed ? (
                    <>
                        {e.hasUpdate && !lan && (
                            <Button size="sm" variant="primary" icon={<ArrowUpCircle className="size-4" />} loading={busy} onClick={install}>
                                Update to {e.version}
                            </Button>
                        )}
                        {!lan && (
                            <IconButton size="sm" label="Logs" onClick={() => setLogs(true)}>
                                <FileText className="size-4" />
                            </IconButton>
                        )}
                        {!lan && (
                            <IconButton
                                size="sm"
                                label="Uninstall"
                                className="hover:text-rose-300"
                                onClick={() => confirm(`Uninstall ${e.name}?`) && run(e.extensionId, () => api.del(`/api/extensions/${e.extensionId}`), `Uninstalled ${e.name}`)}
                            >
                                <Trash2 className="size-4" />
                            </IconButton>
                        )}
                        {ext && (
                            <span className="ml-2">
                                <Switch checked={ext.enabled} disabled={lan} onChange={v => run(e.extensionId, () => api.post(`/api/extensions/${e.extensionId}/enable`, { enabled: v }))} />
                            </span>
                        )}
                    </>
                ) : (
                    !lan && (
                        <Button size="sm" variant="white" icon={<Download className="size-4" />} loading={busy} onClick={install}>
                            Install
                        </Button>
                    )
                )}
            </div>
            {logs && <LogsDialog open onOpenChange={setLogs} id={e.extensionId} title={`${e.name} logs`} />}
        </div>
    )
}
