import { KeyRound } from "lucide-react"
import { Link, useLocation } from "react-router-dom"
import { useStatus } from "@/lib/queries"
import { Button } from "./ui"

// First run: without a TMDB key there is no catalog (no search, discover,
// posters or episode lists). Shown above every page but Settings until a
// key is saved.
export function TmdbNotice() {
    const { data: status } = useStatus()
    const { pathname } = useLocation()
    if (status?.tmdbConfigured !== false || pathname.startsWith("/settings")) return null
    return (
        <div className="px-6 pt-6 md:px-8 xl:px-10">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-xl border border-brand/30 bg-brand-soft px-5 py-4 rise-in">
                <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-brand/20 text-brand-strong">
                    <KeyRound className="size-[18px]" />
                </span>
                <div className="min-w-0 flex-1 text-sm">
                    <p className="font-semibold">Add a TMDB API key to get started</p>
                    <p className="mt-0.5 text-muted">
                        KumoBox gets every movie and show — posters, seasons, episodes, where to watch — from The Movie Database. A key is free; your library and players work
                        without one.
                    </p>
                </div>
                <Link to="/settings?tab=metadata">
                    <Button variant="primary" size="sm">
                        Set up TMDB
                    </Button>
                </Link>
            </div>
        </div>
    )
}
