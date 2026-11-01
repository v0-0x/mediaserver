import type { QueryClient } from "@tanstack/react-query"
import { toast } from "@/lib/toast"
import { api } from "./api"
import { notificationsMutedStore, playerStore } from "./store"
import type { Status } from "./types"
import { img } from "./utils"

// An alert from the server (package notify): an episode aired, or is ready
// in the library.
export type Alert = { kind: "aired" | "ready"; title: string; body: string; mediaId?: number; episode?: number; path?: string; image?: string }

// Shown as a toast while the app is in front, else as a notification of the
// system where the browser allows them (the desktop app always does). Muted
// with the bell in the sidebar, like the other toasts.
export function showAlert(qc: QueryClient, a: Alert, navigate: ((to: string) => void) | null) {
    if (notificationsMutedStore.get()) return
    const open = () => {
        if (a.path && a.mediaId) playAlert(qc, a)
        else if (a.mediaId) navigate?.(`/entry?id=${a.mediaId}`)
        else navigate?.("/local")
    }
    if (document.visibilityState !== "visible" && systemNotifications() === "granted") {
        try {
            const n = new Notification(a.title, { body: a.body, icon: a.image ? img(a.image) : "/aniko.svg", tag: `aniko-${a.kind}-${a.mediaId ?? ""}` })
            n.onclick = () => {
                window.focus()
                open()
                n.close()
            }
            return
        } catch {
            // Some browsers only make them from a service worker: a toast.
        }
    }
    toast(a.title, {
        description: a.body,
        duration: 10_000,
        action: { label: a.path ? "Play" : a.mediaId ? "Open" : "Library", onClick: open },
    })
}

// Plays the episode an alert is about, with the player the settings say.
function playAlert(qc: QueryClient, a: Alert) {
    const status = qc.getQueryData<Status>(["status"])
    const mpv = status?.client !== "lan" && status?.settings.playback.defaultPlayer === "mpv"
    if (!mpv) {
        playerStore.set({ kind: "local", path: a.path!, mediaId: a.mediaId!, episode: a.episode ?? 0 })
        return
    }
    api.post("/api/playback/local", { path: a.path, mediaId: a.mediaId, episode: a.episode, player: "mpv" }).catch((e: Error) => toast.error(e.message))
}

// Whether this browser shows Aniko's notifications: "unsupported" where it
// can't (not over https, the TV app), else the permission.
export function systemNotifications(): NotificationPermission | "unsupported" {
    if (typeof Notification === "undefined" || !window.isSecureContext) return "unsupported"
    return Notification.permission
}
