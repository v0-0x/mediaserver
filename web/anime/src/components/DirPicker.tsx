import { useQuery } from "@tanstack/react-query"
import { Folder, FolderOpen, HardDrive } from "lucide-react"
import { useEffect, useState } from "react"
import { api } from "@/lib/api"
import { Button, Dialog, Input } from "./ui"

// A folder on the computer Aniko runs on, chosen by browsing it (Settings,
// an Aniko Hub's dashboard).
export function DirPicker({ open, onOpenChange, onPick, start }: { open: boolean; onOpenChange: (v: boolean) => void; onPick: (p: string) => void; start?: string }) {
    const [path, setPath] = useState(start ?? "")
    const { data, error } = useQuery({
        queryKey: ["dirs", path],
        // roots: the drives on Windows.
        queryFn: () => api.get<{ path: string; parent: string; dirs: string[]; sep?: string; roots?: string[] | null }>(`/api/fs/dirs?path=${encodeURIComponent(path)}`),
        enabled: open,
    })
    useEffect(() => {
        if (open) setPath(start ?? "")
    }, [open, start])
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Choose a folder"
            className="w-[min(94vw,620px)]"
            footer={
                <Button
                    variant="primary"
                    onClick={() => {
                        if (data) onPick(data.path)
                        onOpenChange(false)
                    }}
                >
                    Use this folder
                </Button>
            }
        >
            <Input value={data?.path ?? path} onChange={e => setPath(e.target.value)} onKeyDown={e => e.key === "Enter" && setPath((e.target as HTMLInputElement).value)} icon={<Folder className="size-4" />} />
            {error && <p className="mt-2 text-sm text-rose-300">{(error as Error).message}</p>}
            {!!data?.roots?.length && (
                <div className="mt-3 flex flex-wrap gap-1.5">
                    {data.roots.map(r => (
                        <Button key={r} size="xs" variant={data.path.toLowerCase().startsWith(r.toLowerCase()) ? "primary" : "subtle"} icon={<HardDrive className="size-3.5" />} onClick={() => setPath(r)}>
                            {r.replace(/\\$/, "")}
                        </Button>
                    ))}
                </div>
            )}
            <div className="mt-3 flex max-h-80 flex-col overflow-y-auto">
                {data && data.parent !== data.path && (
                    <button onClick={() => setPath(data.parent)} className="flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted hover:bg-white/[0.05]">
                        <FolderOpen className="size-4" /> ..
                    </button>
                )}
                {data?.dirs.map(d => (
                    <button
                        key={d}
                        onClick={() => setPath(`${data.path.replace(/[\\/]$/, "")}${data.sep ?? "/"}${d}`)}
                        className="flex items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-white/[0.05]"
                    >
                        <Folder className="size-4 text-brand-strong" /> {d}
                    </button>
                ))}
            </div>
        </Dialog>
    )
}
