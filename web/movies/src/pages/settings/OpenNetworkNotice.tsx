import { ShieldAlert } from "lucide-react"
import { Button } from "@/components/ui"
import type { Status } from "@/lib/types"

// The id of Settings › App's network password field, which the notice's
// button takes the user to.
export const NETWORK_PASSWORD_ID = "network-password"

// Whether the saved settings let devices on the network in without a
// password. Only this computer can tell (devices on the LAN never get the
// password) and set one.
export function networkIsOpen(status?: Status) {
    const server = status?.settings.server
    return !!server && status?.client !== "lan" && server.allowLan && !server.password
}

// Shown at the top of Settings while LAN access is on with no password:
// anyone on the network can then use KumoBox as if they were at this
// computer.
export function OpenNetworkNotice({ onSetPassword }: { onSetPassword: () => void }) {
    return (
        <div role="alert" className="mb-7 flex flex-wrap items-center gap-x-4 gap-y-3 rounded-xl border border-amber-500/25 bg-amber-500/10 px-5 py-4 lg:mt-[3.25rem]">
            <ShieldAlert className="size-5 shrink-0 text-amber-300" />
            <div className="min-w-0 flex-1 text-sm">
                <p className="font-semibold text-amber-200">Devices on your network don't need a password</p>
                <p className="mt-0.5 text-muted">
                    Anyone on your network — guests on your Wi-Fi, a shared flat, a smart TV — can open KumoBox and use it without signing in: browse your library, play and
                    download, change your list and settings. Set a network password: each device asks for it once.
                </p>
            </div>
            <Button variant="primary" size="sm" onClick={onSetPassword}>
                Set a password
            </Button>
        </div>
    )
}
