// The AniList login started from Aniko's login button, which the callback page
// (AuthCallback) checks: another website could open that page with its own
// AniList token, and log Aniko into its account.

const key = "aniko:anilist-login"
// How long a login may take.
const maxAge = 30 * 60 * 1000

export function loginStarted() {
    try {
        localStorage.setItem(key, String(Date.now()))
    } catch {
        /* no storage: the callback refuses */
    }
}

// Whether a login was started from Aniko lately; it's used up.
export function takeLogin(): boolean {
    try {
        const at = Number(localStorage.getItem(key))
        localStorage.removeItem(key)
        return at > 0 && Date.now() - at < maxAge
    } catch {
        return false
    }
}
