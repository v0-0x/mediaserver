import type { StreamSite, StreamSiteSettings } from "./types"

// The streaming sites of the luffy and FreeFlix command-line apps, built in
// (server: providers/cinejoy and providers/freeflix), in the order they're
// asked. They find titles by their TMDB id.

export type StreamSiteKey = Exclude<keyof StreamSiteSettings, "decryptUrl">

export type StreamSiteInfo = {
    key: StreamSiteKey
    name: string
    // The app it comes from.
    app: "luffy" | "FreeFlix"
    help: string
    // Its own address (a mirror can replace it).
    url: string
    // Has enc-dec.app work out its keys.
    decrypts?: boolean
}

export const STREAM_SITES: StreamSiteInfo[] = [
    { key: "cinejoy", name: "Cinejoy", app: "luffy", help: "luffy's own source: HLS from several servers, each checked before it's listed.", url: "https://api.shegu.st" },
    { key: "vidlink", name: "Vidlink", app: "FreeFlix", help: "HLS, or MP4 files in several qualities, with subtitles.", url: "https://vidlink.pro", decrypts: true },
    { key: "hexa", name: "Hexa", app: "FreeFlix", help: "HLS from several servers.", url: "https://themoviedb.hexa.su", decrypts: true },
    { key: "xpass", name: "Xpass", app: "FreeFlix", help: "HLS and MP4 from several playlists.", url: "https://play.xpass.top" },
    { key: "mapple", name: "Mapple", app: "FreeFlix", help: "HLS from up to eight sources.", url: "https://mapple.uk" },
    { key: "videasy", name: "Videasy", app: "FreeFlix", help: "Cineby's player: HLS and files from eight servers, with subtitles.", url: "https://api.videasy.net", decrypts: true },
]

export const DEFAULT_DECRYPT_URL = "https://enc-dec.app/api"

export const SITE_OFF: StreamSite = { enabled: false, url: "" }
