import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Download } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { api } from "@/lib/api"
import { formatRanges, type Missing } from "@/lib/missing"
import { useDownloads, useLanguageMode, useStatus, useStreamEpisodes, useStreamModes } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { Media } from "@/lib/types"
import { cn, title } from "@/lib/utils"
import { Button, Dialog, Field, Select, Skeleton, Tooltip } from "../ui"

const PROVIDER = "ani-cli"
const ACTIVE = new Set(["queued", "resolving", "downloading"])

// Whether ani-cli can download here: on this device, or on the computer
// its downloads go to (a TV's).
export function useAniCliDownloads() {
    const { data: status } = useStatus()
    const enabled = status?.settings.onlineStream.enabled !== false
    const installed = !!status?.features.aniCli || !!status?.features.aniCliHost
    return { enabled, installed, loaded: !!status }
}

// The episodes of an anime being downloaded now (any way: these aren't
// asked for again).
export function useDownloading(mediaId: number) {
    const { data } = useDownloads()
    return useMemo(() => new Set((data ?? []).filter(d => d.mediaId === mediaId && ACTIVE.has(d.status)).map(d => d.episode)), [data, mediaId])
}

// Downloading an anime's missing episodes with ani-cli, subbed or dubbed:
// the gaps, the ones aired since, or both; only those ani-cli has in that
// version, and not those already downloading.
export function MissingDownloadDialog({ media, missing, open, onOpenChange }: { media: Media; missing: Missing; open: boolean; onOpenChange: (v: boolean) => void }) {
    const qc = useQueryClient()
    const { data: status } = useStatus()
    const language = useLanguageMode(open ? media.id : 0)
    const remembered = language.mode ?? (status?.settings.aniCli.defaultMode === "dub" || status?.settings.onlineStream.preferDub ? "dub" : "sub")
    const [mode, setMode] = useState<"sub" | "dub">(remembered)
    // The anime's own choice, once it's known (until it's changed here).
    const [touched, setTouched] = useState(false)
    useEffect(() => {
        if (!touched) setMode(remembered)
    }, [remembered, touched])
    useEffect(() => {
        if (!open) setTouched(false)
    }, [open])

    const { data: modes } = useStreamModes(PROVIDER, media.id, open)
    const hasSub = !modes || modes.sub
    const hasDub = !modes || modes.dub
    // A version ani-cli doesn't have isn't offered.
    const dub = mode === "dub" ? hasDub || !hasSub : !hasSub && hasDub
    const [gaps, setGaps] = useState(true)
    const [newer, setNewer] = useState(true)
    const [quality, setQuality] = useState("best")
    const [busy, setBusy] = useState(false)

    const episodes = useStreamEpisodes(PROVIDER, media.id, dub, open && language.loaded, dub !== (remembered === "dub"))
    const downloading = useDownloading(media.id)
    const wanted = [...(gaps ? missing.gaps : []), ...(newer ? missing.newer : [])].sort((a, b) => a - b)
    const has = useMemo(() => new Set((episodes.data?.episodes ?? []).map(e => e.number)), [episodes.data])
    const already = wanted.filter(n => downloading.has(n))
    const notThere = episodes.data ? wanted.filter(n => !downloading.has(n) && !has.has(n)) : []
    const toGet = episodes.data ? wanted.filter(n => !downloading.has(n) && has.has(n)) : []

    const go = async () => {
        setBusy(true)
        try {
            await api.post("/api/onlinestream/download", {
                provider: PROVIDER,
                mediaId: media.id,
                episodes: toGet,
                dub,
                quality,
            })
            qc.invalidateQueries({ queryKey: ["downloads"] })
            onOpenChange(false)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }

    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title={`Download ${title(media)}`}
            description="With ani-cli, into your library folder, matched automatically."
            footer={
                <>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button variant="primary" loading={busy} disabled={!toGet.length} icon={<Download className="size-4" />} onClick={go}>
                        {toGet.length ? `Download ${toGet.length === 1 ? "1 episode" : `${toGet.length} episodes`} (${dub ? "dub" : "sub"})` : "Download"}
                    </Button>
                </>
            }
        >
            <div className="flex flex-col gap-5">
                <Field label="Version">
                    <div className="flex w-fit rounded-lg bg-white/[0.04] p-0.5" role="group" aria-label="Sub or dub">
                        {(["sub", "dub"] as const).map(m => {
                            const there = m === "dub" ? hasDub : hasSub
                            const button = (
                                <button
                                    key={m}
                                    disabled={!there}
                                    aria-pressed={(m === "dub") === dub}
                                    onClick={() => {
                                        setTouched(true)
                                        setMode(m)
                                    }}
                                    className={cn(
                                        "focus-ring h-8 rounded-md px-4 text-[13px] font-medium uppercase transition-colors",
                                        (m === "dub") === dub ? "bg-white/[0.1] text-fg" : "text-muted hover:text-fg",
                                        !there && "cursor-not-allowed opacity-40 hover:text-muted",
                                    )}
                                >
                                    {m}
                                </button>
                            )
                            return there ? (
                                button
                            ) : (
                                <Tooltip key={m} content={`ani-cli only has it ${m === "dub" ? "with subtitles" : "dubbed"}`}>
                                    <span>{button}</span>
                                </Tooltip>
                            )
                        })}
                    </div>
                </Field>

                <Field label="Episodes">
                    <div className="flex flex-col gap-2">
                        {missing.gaps.length > 0 && <Choice on={gaps} onChange={setGaps} label={`Missing ${missing.gaps.length === 1 ? "episode" : "episodes"}`} detail={formatRanges(missing.gaps)} />}
                        {missing.newer.length > 0 && <Choice on={newer} onChange={setNewer} label="Aired since your last one" detail={formatRanges(missing.newer)} />}
                    </div>
                </Field>

                <Field label="Quality">
                    <Select
                        value={quality}
                        onChange={setQuality}
                        options={[
                            { value: "best", label: "Best available" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                            { value: "worst", label: "Smallest" },
                        ]}
                    />
                </Field>

                <div className="text-[13px] leading-relaxed" aria-live="polite">
                    {episodes.isLoading || !language.loaded ? (
                        <Skeleton className="h-5 w-2/3" />
                    ) : episodes.error ? (
                        <p className="flex gap-2 text-amber-200/90">
                            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                            <span>
                                ani-cli couldn't list this anime {dub ? "dubbed" : "subbed"}: {(episodes.error as Error).message}.{" "}
                                <Link to={`/entry?id=${media.id}&tab=stream`} className="underline" onClick={() => onOpenChange(false)}>
                                    Match it by hand
                                </Link>{" "}
                                on its page, then try again.
                            </span>
                        </p>
                    ) : !wanted.length ? (
                        <p className="text-muted">Pick which episodes to download.</p>
                    ) : (
                        <div className="flex flex-col gap-1 text-muted">
                            {toGet.length > 0 && (
                                <p>
                                    <span className="text-fg">{formatRanges(toGet)}</span> will be downloaded {dub ? "dubbed" : "with subtitles"}.
                                </p>
                            )}
                            {already.length > 0 && <p>Already downloading: {formatRanges(already)}.</p>}
                            {notThere.length > 0 && (
                                <p className="text-amber-200/85">
                                    ani-cli doesn't have {formatRanges(notThere)} {dub ? "dubbed" : "subbed"}
                                    {dub && hasSub ? ": try Sub, or torrents" : ": try torrents"}.
                                </p>
                            )}
                        </div>
                    )}
                </div>
            </div>
        </Dialog>
    )
}

function Choice({ on, onChange, label, detail }: { on: boolean; onChange: (v: boolean) => void; label: string; detail: string }) {
    return (
        <label className={cn("flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5 transition-colors", on ? "border-white/15 bg-white/[0.05]" : "border-line hover:bg-white/[0.03]")}>
            <input type="checkbox" checked={on} onChange={e => onChange(e.target.checked)} className="size-4 shrink-0 accent-[var(--brand)]" />
            <span className="min-w-0">
                <span className="block text-sm font-medium">{label}</span>
                <span className="block truncate text-xs text-muted">{detail}</span>
            </span>
        </label>
    )
}
