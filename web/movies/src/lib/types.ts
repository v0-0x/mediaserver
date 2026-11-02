// Shapes returned by the Moviko API (mirrors the Go structs).

export type FuzzyDate = { year?: number | null; month?: number | null; day?: number | null }

// ---------------------------------------------------------------------------
// The catalog (server/internal/catalog/types.go). Media ids and episode keys
// are explained in lib/ids.ts.

export type MediaType = "MOVIE" | "TV"

// An episode of a show and when it airs: episode is its key, airingAt that
// day at 00:00 UTC (TMDB only has air dates).
export type AiringEpisode = {
    id?: number
    airingAt: number
    timeUntilAiring: number
    episode: number
    title?: string
    media?: Media
}

export type Person = { id: number; name: { full: string }; image: { large: string } }

// One cast member: node is the role (named after the character, with the
// actor's photo), voiceActors[0] the actor.
export type CharacterEdge = { role: string; node: Person; voiceActors: Person[] }

export type CrewMember = { id: number; name: string; job: string; image?: string }

// A season as listed with its show; number 0 holds the specials.
export type Season = { seasonNumber: number; name: string; episodeCount: number; airDate?: string; poster?: string }

// An episode from its season's details (GET /api/media/{id}/season/{n}).
export type Episode = {
    key: number
    season: number
    number: number
    title: string
    overview?: string
    still?: string
    airDate?: string // YYYY-MM-DD
    runtime?: number // minutes
    rating?: number // 0-10
}

export type SeasonDetail = {
    mediaId: number
    seasonNumber: number
    name: string
    overview?: string
    poster?: string
    airDate?: string
    episodes: Episode[] | null
}

export type WatchProvider = { id: number; name: string; logo: string }

// Where a title can be watched in one country (TMDB, from JustWatch).
export type WatchProviders = {
    region: string
    link?: string
    flatrate?: WatchProvider[]
    free?: WatchProvider[]
    ads?: WatchProvider[]
    rent?: WatchProvider[]
    buy?: WatchProvider[]
}

export type CollectionRef = { id: number; name: string; poster?: string; backdrop?: string }

export type ListEntryLite = { id: number; status: string; progress: number; score: number; repeat: number }

// A movie or a show.
export type Media = {
    id: number
    tmdbId: number
    imdbId?: string
    tvdbId?: number
    type: MediaType
    format: string // MOVIE, TV or MINISERIES
    status: string // FINISHED, RELEASING, NOT_YET_RELEASED, CANCELLED
    season?: string
    seasonYear?: number | null
    episodes?: number | null // regular episodes of a show, 1 for a movie
    duration?: number | null // minutes (per episode for shows)
    isAdult: boolean
    title: { romaji?: string; english?: string; native?: string; userPreferred?: string }
    synonyms?: string[] | null
    coverImage: { extraLarge?: string; large?: string; medium?: string; color?: string }
    bannerImage?: string
    genres?: string[] | null
    averageScore?: number | null // TMDB rating x 10
    meanScore?: number | null
    popularity?: number
    voteCount?: number
    startDate?: FuzzyDate
    endDate?: FuzzyDate
    tagline?: string
    originalLanguage?: string
    certification?: string
    seasons?: Season[] | null
    latestEpisode?: number // key of the last episode that aired
    nextAiringEpisode?: AiringEpisode | null
    description?: string
    countryOfOrigin?: string
    siteUrl?: string
    belongsTo?: CollectionRef | null
    trailer?: { id: string; site: string; thumbnail: string } | null
    studios?: { nodes: { id: number; name: string; logo?: string }[] | null } | null
    tags?: { name: string; rank: number; isMediaSpoiler: boolean }[] | null
    relations?: { edges: { relationType: string; node: Media | null }[] | null } | null
    recommendations?: { nodes: { mediaRecommendation: Media | null }[] | null } | null
    characters?: { edges: CharacterEdge[] | null } | null
    crew?: CrewMember[] | null
    watchProviders?: WatchProviders | null
    mediaListEntry?: ListEntryLite | null
}

// A media on the user's list. progress is the key of the last episode
// watched (1 for a watched movie); score is 0-100 (a half star is 10);
// notes is the review.
export type ListEntry = {
    id: number
    mediaId: number
    status: string
    progress: number
    score: number
    repeat: number
    notes?: string
    private?: boolean
    liked?: boolean
    startedAt?: FuzzyDate
    completedAt?: FuzzyDate
    updatedAt: number
    letterboxdUri?: string
    letterboxdSynced?: number
    media: Media
}

// POST /api/media/{id}/entry
export type EntryUpdate = {
    status?: string
    progress?: number
    score?: number
    repeat?: number
    notes?: string
    liked?: boolean
    startedAt?: FuzzyDate
    completedAt?: FuzzyDate
}

// GET /api/catalog/list
export type RawCollection = { lists: { name: string; status: string; isCustomList: boolean; entries: ListEntry[] | null }[] | null }

export type MediaPage = { pageInfo: { total: number; currentPage: number; lastPage?: number; hasNextPage: boolean }; media: Media[] | null }

// GET /api/catalog/discover (fetchedAt: unix seconds).
export type Discover = {
    trending: Media[] | null
    popularMovies: Media[] | null
    popularShows: Media[] | null
    topRatedMovies: Media[] | null
    topRatedShows: Media[] | null
    nowPlaying: Media[] | null
    upcoming: Media[] | null
    onTheAir: Media[] | null
    fetchedAt: number
}

// One viewing of a movie: a Letterboxd diary entry. rating is 0-100 (0:
// none); source is "kumobox" (logged here) or "letterboxd" (imported);
// exportedAt is when a Moviko entry went to Letterboxd (0: not yet).
export type DiaryEntry = {
    id: number
    mediaId: number
    watchedDate: string // YYYY-MM-DD
    rating: number
    rewatch: boolean
    liked: boolean
    review?: string
    tags?: string
    source: string
    letterboxdId?: string
    exportedAt?: number
    createdAt: number
    media?: Media | null
}

// The connected Letterboxd member (status.letterboxd).
export type Viewer = {
    id: number
    name: string
    avatar: { large: string; medium: string }
    bannerImage?: string
    letterboxd?: string // profile page
}

// GET /api/letterboxd/status (and the "letterboxd-status" event). lastSync
// is when the diary was last read (unix seconds, 0: never), lastError why
// the last sync failed; pending counts the diary entries logged here that
// aren't on Letterboxd yet. apiAvailable: API client credentials are set;
// apiConnected: a member signed in with them (apiMember).
export type LetterboxdStatus = {
    connected: boolean
    username: string
    displayName: string
    profileUrl?: string
    lastSync: number
    lastError?: string
    pending: number
    syncing: boolean
    apiAvailable: boolean
    apiConnected: boolean
    apiMember?: string
}

// What a sync, an import or a push did: films added to the list, updated or
// skipped (nothing new), diary entries added (a push: entries sent), and the
// titles that couldn't be found.
export type LetterboxdResult = { added: number; updated: number; skipped: number; diary?: number; unmatched: string[] | null }

// POST /api/letterboxd/import: the result, and the export's files read.
export type LetterboxdImportResult = LetterboxdResult & { files: string[] | null }

// POST /api/letterboxd/connect: the status, the member and what the first
// read of the diary did (syncError: it failed, the account is connected
// all the same).
export type LetterboxdConnected = LetterboxdStatus & { viewer: Viewer | null; result: LetterboxdResult | null; syncError?: string }

// What a file's path says about it (server: library.Parsed). season is -1
// when the name has none, episode -1 when it has no number (for a number
// without a season, "Show - 105", it's that number); episodeEnd is the last
// episode of a multi-episode file (S01E01E02: 2). folder is the movie's or
// show's folder, folderTitle the title in its name.
export type Parsed = {
    title: string
    folderTitle: string
    folder?: string
    season: number
    part: number
    episode: number
    episodeEnd?: number
    absolute?: number
    date?: string
    episodeTitle: string
    releaseGroup: string
    resolution: string
    source?: string
    codec?: string
    hdr?: string[]
    audio?: string[]
    edition?: string
    year: number
    show?: boolean
    extra?: string // trailer, featurette, sample...
    kind: string
    tmdbId?: number
    imdbId?: string
    tvdbId?: number
}

// What a library file is: a movie or an episode, a later part of a movie
// split across files (CD2: the movie plays from part 1), a special, bonus
// material (trailers, featurettes, samples...).
export type FileKind = "main" | "part" | "special" | "extra"

// A video file of the library. episode is its episode key (1 for a movie,
// see lib/ids.ts), 0 when unknown and for extras; episodeEnd the key of the
// last episode of a file holding several (S01E01E02: 1001 and 1002).
export type LocalFile = {
    path: string
    dir: string
    name: string
    size: number
    modTime: number
    parsed: Parsed
    mediaId: number
    episode: number
    episodeEnd?: number
    kind: FileKind
    locked: boolean
    ignored: boolean
    matchScore: number
    // Shared by another Moviko app on the network (path kumobox://<host>/…).
    host?: string
    hostName?: string
}

export type HistoryEntry = {
    mediaId: number
    episode: number
    position: number
    duration: number
    source: string
    updatedAt: number
}

// An episode as GET /api/media/{id} (and …/episodes) lists it, with what
// the library and the watch history know. key is the episode key (see
// lib/ids.ts; number holds the same: the server sends it so, and
// lib/queries.ts makes sure of it), season and episode the season and the
// episode's number in it. A file that isn't an episode (an extra, a movie's
// later part) has key 0.
export type EpisodeView = {
    key: number
    number: number
    season?: number
    episode?: number
    title: string
    image: string
    summary: string
    airDate: string
    runtime: number
    kind: string
    file: LocalFile | null
    watched: boolean
    aired: boolean
    history: HistoryEntry | null
    resumeAt: number
    hasFile: boolean
}

// episodes is null for a show whose seasons aren't known and that has no
// episode file (Go's empty list).
export type EntryView = {
    media: Media
    listEntry: ListEntry | null
    episodes: EpisodeView[] | null
    specials: EpisodeView[] | null
    others: EpisodeView[] | null
    nextEpisode: EpisodeView | null
    localCount: number
    sharedCount: number // files other Moviko apps share
    images?: { banner: string; poster: string; fanart: string; clearlogo: string } | null
    // The title's ids on other sites.
    mappings?: { tmdbId: number; imdbId?: string; tvdbId?: number }
    metadataNote?: string
}

// GET /api/media/{id}/episodes: the same lists as the entry, without the media.
export type EpisodesView = {
    episodes: EpisodeView[] | null
    specials?: EpisodeView[] | null
    others?: EpisodeView[] | null
    nextEpisode?: EpisodeView | null
}

export type CollectionItem = {
    media: Media
    listEntry: ListEntry | null
    localFiles: number
    downloaded: number[] | null
    nextEpisode: number
    nextHasFile: boolean
    lastWatched: number
}

// A title of "Continue watching": episode is the key of the episode to
// watch, index its place among the regular episodes ("13 / 24").
export type ContinueItem = {
    media: Media
    episode: number
    index: number
    total: number
    title: string
    image: string
    runtime: number
    hasFile: boolean
    filePath?: string
    resumeAt: number
    duration: number
    lastWatched: number
    source: string
}

export type CollectionView = {
    lists: { status: string; name: string; items: CollectionItem[] }[] | null
    continueWatching: ContinueItem[] | null
    unmatchedCount: number
    ignoredCount: number
    localOnly: CollectionItem[] | null
    genres: string[] | null
    // Movies and shows in the library left out for now (TMDB didn't say
    // what they are yet): the collection is asked for again.
    incomplete?: number
}

export type Settings = {
    library: {
        // This computer has no library of its own (it plays the shared ones).
        off?: boolean
        dir: string
        extraDirs: string[]
        autoRefresh: boolean
        refreshOnStartup: boolean
        matchThreshold: number
        ignorePatterns: string[]
        matchOutsideList: boolean
    }
    playback: {
        defaultPlayer: "mpv" | "builtin"
        autoUpdateProgress: boolean
        completionThreshold: number
        resumePlayback: boolean
        autoPlayNext: boolean
        rememberTracks: boolean
        // Audio in this language rather than the original (a dub), e.g.
        // "fre,fra,fr"; "": the original language.
        preferredAudioLang: string
        // Subtitles in this language ("": English, for foreign audio).
        preferredSubLang: string
        // Subtitles only when the audio isn't in a language of the user's
        // (else whenever preferredSubLang is set); forced ones always.
        subtitlesForForeign: boolean
        // Skip the chapters named as an intro or a recap / the end credits
        // or a preview.
        skipIntro: boolean
        skipCredits: boolean
    }
    mpv: { path: string; socket: string; extraArgs: string; fullscreen: boolean }
    transcode: { ffmpegPath: string; ffprobePath: string; mode: string; hwAccel: string; vaapiNode: string; preset: string }
    // Where streams are downloaded to (empty: the library folder) and with what.
    downloads: { dir: string; downloader: string }
    onlineStream: { enabled: boolean; defaultProvider: string; preferDub: boolean }
    torrent: {
        defaultClient: string
        defaultProvider: string
        showActiveCount: boolean
        createSubfolder: boolean
        preferredResolution: string
        autoDownloader: boolean
        autoDownloadMinutes: number
    }
    qbittorrent: TorrentClientConfig
    transmission: TorrentClientConfig
    server: { host: string; port: number; allowLan: boolean; password: string; webUi: boolean }
    // downloadTo: the Moviko this one's downloads go to (its ID; "" here).
    // keepAwake: don't let this computer sleep while another device plays its
    // files. autoSync: sync watch history when a paired device comes back.
    sharing: { enabled: boolean; name: string; downloadTo?: string; keepAwake?: boolean; autoSync?: boolean }
    notify?: { newEpisodes: boolean; downloads: boolean }
    // Which releases Moviko updates to: stable ones, or betas too.
    updates?: { channel: "stable" | "beta" }
    ui: {
        accentColor: string
        showAdult: boolean
        bannerType: string
        cardSize: string
        reducedMotion: boolean
        showEpisodeTitle: boolean
        blurUnwatched: boolean
        homeSections: string[]
        // lib/themes.ts: a COLOR_THEMES id; "auto", "off" or a HOLIDAYS id.
        colorTheme?: string
        seasonal?: string
        seasonalEffects?: boolean
        // Moviko's look from before 2.0.
        classicLook?: boolean
    }
    discord: { richPresence: boolean; clientId: string }
    extensions: { marketplaceUrl: string; autoUpdate: boolean }
    tmdb: TMDBSettings
    letterboxd: LetterboxdSettings
    providers: ProviderSettings
    torrentSources: TorrentSourceSettings
}

// apiKey: a TMDB v3 API key or a v4 read access token. region (ISO 3166-1)
// is for release dates, certifications and where to watch.
export type TMDBSettings = { apiKey: string; language: string; region: string }

export type LetterboxdSettings = {
    username: string
    autoSync: boolean
    syncMinutes: number
    logWatched: boolean
    importWatchlist: boolean
    apiClientId: string
    apiClientSecret: string
}

export type ProviderSettings = {
    archive: boolean
    // The streaming sites of the luffy and FreeFlix apps, built in.
    sites: StreamSiteSettings
    stremioCatalogs: string[]
    nuvioRepos: string[]
    preferredQuality: string // 2160p, 1080p, 720p, 480p or "" (best first)
    timeoutSeconds: number
    // The user's own Jellyfin, Emby and Plex servers (tokens are blank on
    // devices of the network).
    mediaServers?: MediaServer[]
}

// A built-in streaming site: on or off, and a mirror's address ("": its
// own).
export type StreamSite = { enabled: boolean; url: string }
export type StreamSiteSettings = {
    cinejoy: StreamSite
    vidlink: StreamSite
    hexa: StreamSite
    xpass: StreamSite
    mapple: StreamSite
    videasy: StreamSite
    // The enc-dec.app API Vidlink, Hexa and Videasy go through ("": its own).
    decryptUrl: string
}

export type MediaServerKind = "jellyfin" | "emby" | "plex"
export type MediaServer = { kind: MediaServerKind; name: string; url: string; token: string; userId?: string }
// POST /api/providers/mediaservers/test
export type MediaServerInfo = { name: string; version: string; userId?: string; userName?: string; libraries?: number }

export type TorznabIndexer = { name: string; url: string; apiKey: string; enabled: boolean }
export type TorrentSource = { enabled: boolean; url: string }

export type TorrentSourceSettings = {
    torznab: TorznabIndexer[]
    yts: TorrentSource
    eztv: TorrentSource
    pirateBay: TorrentSource
    torrentsCsv: TorrentSource
    archive: TorrentSource
}

export type TorrentClientConfig = {
    host: string
    port: number
    username: string
    password: string
    executable: string
    tags: string
    category: string
    useHttps: boolean
}

export type Status = {
    version: string
    appName: string
    // Without a TMDB key there is no catalog: the UI asks for one.
    tmdbConfigured: boolean
    // The connected Letterboxd member, null when none.
    letterboxd: Viewer | null
    features: { mpv: boolean; ffmpeg: boolean; ffprobe: boolean; ytDlp: boolean; xdgOpen: boolean }
    client: "desktop" | "local" | "lan"
    hostname: string
    platform?: string // "linux", "windows"…
    scanning: boolean
    dataDir: string
    listenAddr: string
    lanUrls?: string[] | null // where devices on the home network open Moviko
    // A mediaserver's movies side: where its dashboard is (on the same host).
    server?: { dashboardPort: number }
    paired?: string[] | null // the Movikos this one is paired with (library sharing): Home's Sync
    // The computers sharing their library with this one that don't answer
    // (asleep or off), and whether they can be woken up from here.
    asleep?: { id: string; name: string; canWake: boolean; waking: boolean }[] | null
    // Home's questions about the other Moviko apps (only for this computer):
    // share with a new one ("new"), or update one of the two ("older": its
    // Moviko is older than this one, "newer": this one is).
    devicePrompts?: DevicePrompt[] | null
    keptAwake?: boolean // another device is playing this computer's files, so it stays awake
    webUiForced: boolean
    setupRunning?: boolean // Windows: the window installing Moviko's programs is open
    settings: Settings
}

// A part of a video its chapters name as an intro, a recap, the credits or a
// preview of the next episode (GET /api/playback/skips).
export type SkipInterval = { type: string; start: number; end: number }

export type DevicePrompt = { kind: "new" | "older" | "newer"; id: string; name: string; platform?: string; version: string }

export type PlaybackSession = {
    id: string
    mediaId: number
    episode: number // key
    title: string // "Show — S01E02 · Pilot", "Movie (2010)"
    mediaTitle?: string
    episodeTitle?: string
    source: string
    player: string
    position: number
    duration: number
    paused: boolean
    active: boolean
    progressUpdated: boolean
    skips?: SkipInterval[] | null
}

export type TrackPrefs = {
    mediaId: number
    audioLang?: string
    audioTitle?: string
    audioIndex?: number
    subLang?: string
    subTitle?: string
    subIndex?: number
    subOff?: boolean
    streamMode?: string
}

export type ProbeStream = {
    index: number
    typeIndex: number
    type: string
    codec: string
    language: string
    title: string
    default: boolean
    forced: boolean
    channels?: number
    width?: number
    height?: number
    profile?: string
    pixFmt?: string
    bitmap?: boolean
}

export type Probe = {
    path: string
    container: string
    duration: number
    size: number
    video: ProbeStream[] | null
    audio: ProbeStream[] | null
    subtitles: ProbeStream[] | null
    externalSubs: { name: string; path: string; language: string }[] | null
    method: "direct" | "remux" | "transcode"
    reason: string
}

// A direct link as the in-app player gets it (POST /api/providers/play):
// url goes through the proxy, originalUrl is the provider's link (for mpv).
export type StreamSource = {
    id?: string
    url: string
    originalUrl: string
    type: string
    quality: string
    label?: string
    // The release (or file) name: with the provider and quality, the
    // version the next episodes are looked for in.
    release?: string
    // server: the provider's name.
    server: string
    provider?: string
    subtitles: StreamSubtitle[]
    headers: Record<string, string>
    referrer?: string
    notWebReady?: boolean
    // The link named for the conversion endpoints (/api/local/probe,
    // transcode, hls…), when the browser may not play it as it is.
    remote?: string
    // The video's file name, when the provider gives it.
    filename?: string
    // A torrent playing as it downloads (/api/torrentstream/<id>).
    torrent?: string
}

// How a torrent playing as it downloads is doing (GET /api/torrentstream/{id}).
export type TorrentStreamStatus = { id: string; name: string; size: number; done: number; peers: number; seeds: number; speed: number }

// A subtitle file of a stream, as WebVTT through the proxy (url) and as
// the provider links to it (original). addon: from a subtitle addon (GET
// /api/providers/subtitles finds those made for the very file playing),
// not the stream's own.
export type StreamSubtitle = { url: string; language: string; label?: string; isDefault?: boolean; original: string; addon?: boolean }

// ---------------------------------------------------------------------------
// Streaming providers (server/internal/providers, docs/PROVIDERS.md)

export type ProviderKind = "mediaserver" | "builtin" | "stremio" | "nuvio" | "extension"

// GET /api/providers: every provider, in the order they're asked. Ids are
// "archive", the sites' ("cinejoy", "vidlink"...), "stremio:<addon id>",
// "nuvio:<id>" and "ext:<extension id>".
export type ProviderInfo = {
    id: string
    name: string
    kind: ProviderKind
    icon?: string
    types: ("movie" | "tv")[]
    enabled: boolean
    // The extension id of Nuvio providers and extensions.
    extension?: string
}

export type ProviderSubtitle = { url: string; language: string; label?: string }

// One way to watch a movie or episode: a direct link (url), a torrent
// (infoHash/magnet), a YouTube video or a web page (externalUrl).
export type ProviderStream = {
    id: string
    kind: "direct" | "torrent" | "youtube" | "external"
    provider: string
    providerName: string
    title: string
    url?: string
    type: string
    quality: string
    size?: number
    headers?: Record<string, string>
    subtitles?: ProviderSubtitle[]
    infoHash?: string
    fileIdx: number
    magnet?: string
    externalUrl?: string
    youTubeId?: string
    notWebReady?: boolean
    filename?: string
    seeders?: number
    // The streams most like the version of the show that played last.
    preferred?: boolean
}

export type ProviderError = { provider: string; name: string; error: string }

// The provider and version of a show that played last (its next episodes
// are looked for in it first).
export type StreamVersion = { provider: string; quality?: string; source?: string; group?: string; dub?: boolean }

// GET /api/providers/streams/live, one JSON object a line: the providers
// asked, each one's answer as it comes, then the whole result.
export type LiveAsked = { provider: string; name: string; demoted?: boolean }
export type LiveAnswer = { provider: string; name: string; streams: ProviderStream[]; error?: string; ms: number; cached?: boolean }
export type LiveLine = { asking: LiveAsked[] } | LiveAnswer | { done: true; result?: StreamsResult; error?: string }

// GET /api/providers/health: how each provider's searches went.
export type ProviderHealth = ProviderInfo & {
    successes: number
    failures: number
    consecutiveFailures: number
    lastError?: string
    lastErrorAt?: number
    lastSuccessAt?: number
    latencies?: number[]
    avgMs: number
    // After 3 failures in a row: listed last, left out of automatic picks.
    demoted: boolean
}

// POST /api/providers/{id}/test
export type ProviderTestResult = { provider: string; name: string; title: string; streams: number; direct: number; error?: string; ms: number }

// The title an extension provider matched (it searches by title).
export type ProviderMatch = { provider: string; name: string; id: string; title: string; score: number; manual: boolean }

// GET /api/providers/streams
export type StreamsResult = {
    mediaId: number
    episode: number
    streams: ProviderStream[]
    errors: ProviderError[]
    subtitles: ProviderSubtitle[]
    providers: string[]
    matches?: ProviderMatch[]
    preferred?: StreamVersion
    fetchedAt: number
}

// POST /api/providers/play
export type PlayResult =
    | { action: "builtin"; sources: StreamSource[]; title: string; mediaTitle?: string; episodeTitle?: string; key?: number; resumeAt: number; tracks: TrackPrefs | null; provider: string }
    | { action: "mpv"; provider: string }
    | { action: "torrent"; savePath: string }
    | { action: "external"; url: string }

// POST /api/providers/stremio/import: a Stremio account's addons, those
// installed and those left out (with why).
export type StremioImported = { id: string; name: string; transportUrl: string; reason?: string }
export type StremioImportResult = { added: StremioImported[]; skipped: StremioImported[] }

export type StremioManifest = {
    id: string
    version: string
    name: string
    description: string
    logo?: string
    background?: string
    types: string[]
    resources: (string | { name: string; types?: string[] })[]
    behaviorHints?: { adult?: boolean; p2p?: boolean; configurable?: boolean; configurationRequired?: boolean }
}

export type StremioAddon = {
    transportUrl: string
    transportName?: string
    manifest: StremioManifest
    flags: { official?: boolean; protected?: boolean }
    installed: boolean
    enabled: boolean
    position: number
    local: boolean
    supported: boolean
}

export type NuvioEntry = {
    id: string
    name: string
    description: string
    version: string
    author: string
    supportedTypes: string[]
    enabled: boolean
    logo: string
    contentLanguage: string[] | null
    formats: string[] | null
    url: string
    extensionId: string
    installed: boolean
    installedVersion?: string
    hasUpdate: boolean
}

export type NuvioRepo = { url: string; name?: string; version?: string; providers: NuvioEntry[] }

export type DownloadItem = {
    id: string
    mediaId: number
    episode: number // key
    title?: string
    animeTitle?: string // the title, as the server still names it (downloads.Item)
    image: string
    mode: string
    source: string
    // A streaming provider's id, and the quality picked.
    provider: string
    quality?: string
    status: "queued" | "resolving" | "downloading" | "completed" | "failed" | "canceled"
    progress: number
    speed: string
    eta: string
    // How much video is downloaded when the length isn't known ("12:30").
    done?: string
    error?: string
    output?: string
    createdAt: number
}

export type TorrentResult = {
    // The ids of the sources that found it, comma-separated.
    provider: string
    name: string
    link: string
    downloadUrl: string
    magnetLink: string
    infoHash: string
    size: number
    formattedSize: string
    seeders: number
    leechers: number
    downloadCount: number
    date: string
    resolution: string
    releaseGroup: string
    // The key of the one episode it holds, -1 when it's not one episode.
    episodeNumber: number
    // A pack: several episodes, a season, the whole show.
    isBatch: boolean
    isBestRelease: boolean
    confirmed: boolean
    trusted: boolean
    remake: boolean
    dub: boolean
}

// POST /api/torrents/search: the results, best first, and the sources that
// failed ("Source: what went wrong").
export type TorrentSearchResponse = { results: TorrentResult[]; errors: string[] }

// GET /api/torrents/providers: the search sources.
export type TorrentProviderInfo = {
    id: string
    name: string
    supports: ("movie" | "tv")[]
    kind: "torznab" | "builtin" | "extension"
}

export type Torrent = {
    hash: string
    name: string
    size: number
    progress: number
    downSpeed: number
    upSpeed: number
    eta: number
    state: string
    seeds: number
    peers: number
    savePath: string
    contentPath: string
    addedOn: number
    ratio: number
}

export type ExtensionManifest = {
    id: string
    name: string
    version: string
    manifestURI: string
    language: string
    type: string
    description: string
    author: string
    icon: string
    website: string
    lang: string
    notes?: string
    userConfig?: {
        version: number
        requiresConfig: boolean
        fields: {
            type: "text" | "switch" | "select"
            name: string
            label: string
            options?: { value: string; label: string }[]
            default?: string
            description?: string
        }[]
    } | null
    plugin?: {
        version: string
        permissions: {
            scopes: string[] | null
            allow: {
                networkAccess: { allowedDomains: string[] | null; reasoning: string }
                unsafeFlags: { flag: string; reason: string }[] | null
                readPaths: string[] | null
                writePaths: string[] | null
            }
        }
    } | null
}

export type ExtensionInfo = {
    manifest: ExtensionManifest
    enabled: boolean
    userConfig: { version: number; values: Record<string, string> }
    error?: string
    configError?: string
    running: boolean
    installedAt: number
    updatedAt: number
    supported: boolean
    granted: boolean
}

export type MarketEntry = {
    id: string
    name: string
    version: string
    author: string
    description: string
    type: string
    language: string
    lang: string
    icon: string
    manifestURI: string
    website: string
    flags: string
    permalink: string
    workingTag: boolean
    brokenTag: boolean
    deprecatedTag: boolean
    official: boolean
    stars: number
    updatedAt: string
    installed: boolean
    installedVersion?: string
    hasUpdate: boolean
    supported: boolean
}

export type UINode = { id: string; type: string; props: Record<string, any> }

export type PluginState = {
    id: string
    name: string
    icon: string
    state: {
        trays?: {
            id: string
            iconUrl: string
            tooltipText: string
            withContent: boolean
            width: string
            minHeight: string
            badge?: { number?: number; intent?: string } | null
            tree: UINode | null
        }[]
        actions?: { id: string; kind: string; props: Record<string, any> }[]
        webviews?: { id: string; options: Record<string, any>; hidden: boolean; html: string }[]
    }
}

// GET /api/catalog/schedule: episodes of the shows on the list.
export type ScheduleItem = AiringEpisode & { media: Media; listStatus?: string }

// An auto downloader rule: keep a show downloaded. resolution is "2160",
// "1080", "720", "480" or "" (any); releaseGroups are preferred, in order;
// packs allows season packs when several episodes of a finished season are
// wanted; destination (empty: the library) is where torrents are saved;
// startFrom is the key of the first episode wanted (0: the one after the
// last watched).
export type AutoRule = {
    id?: number
    mediaId: number
    enabled: boolean
    title: string
    resolution: string
    releaseGroups: string[]
    mustContain: string[]
    mustNotContain: string[]
    minSeeders: number
    packs: boolean
    destination: string
    startFrom: number
}

// Something the auto downloader grabbed: episode is the episode's key, or
// the season's (season*1000) for a season pack.
export type AutoItem = { key: string; ruleId: number; mediaId: number; episode: number; pack: boolean; title: string; createdAt: number }

// POST /api/autodownloader/run.
export type AutoRunReport = { added: number; wanted: number; errors: string[]; busy?: boolean }

// A library folder as found on disk (Library tools, manual matching).
export type FolderInfo = {
    dir: string
    label: string // path below its library folder
    videos: number // video files directly inside
    notIndexed: number // of those, not in the library index yet
    problem?: string // why it has nothing to match (still downloading, unreadable…)
}

// Moviko's own updates (GET /api/update, and the "update-status" event).
// beta: a pre-release (or, on Linux, the default branch's head).
export type UpdateVersion = { version: string; commit: string; date?: string; url?: string; beta?: boolean }

export type UpdateState = "idle" | "downloading" | "building" | "installing" | "ready" | "failed"

export type UpdateStatus = {
    current: UpdateVersion
    latest: UpdateVersion | null // once a check found it
    available: boolean
    changes: string[] | null // commit subjects, newest first
    changesTotal: number
    note?: string
    checkedAt: number // unix seconds, 0 before the first check
    checking: boolean
    error?: string // the last check failed
    hint?: string
    canApply: boolean // this copy installs updates itself (applyNote: how, or why not)
    applyNote?: string
    state: UpdateState
    progress: number // 0-100, -1 when unknown
    message?: string
    log: string[] | null // the last output lines
    manualCommand?: string // a command to run in a terminal
    installFile?: string // Android: the downloaded APK the app installs
}

// Library sharing with other Moviko apps on the network.
export type SharingPeer = {
    id: string
    name: string
    address: string
    online: boolean
    lastSeen: number
    version: string
    allowed: boolean // this Moviko shares its library with it
    shares: boolean // it shares its library with this Moviko
    files: number
    manual: boolean
    error?: string
    // Granted to it (with allowed): downloading here; granted by it (it
    // shares): downloading there.
    downloads: boolean
    takesDownloads: boolean
    // It shares with this Moviko and has said how to wake it up (Wake-on-LAN);
    // waking: a wake-up was just sent.
    canWake?: boolean
    waking?: boolean
    platform?: string // what it runs on ("android": a TV)
    declined?: boolean // asked on Home whether to share with it: "not now"
}

export type SharingStatus = { enabled: boolean; id: string; name: string; listening: boolean; peers: SharingPeer[]; addresses: string[] }

export type SharedLibrary = { host: SharingPeer; files: LocalFile[] }

// A movie or show of the Local library page (GET /api/library/titles): its
// files summed up. episodes are the keys of the regular episodes with a file
// (the movie's 1), ranges them written short ("S01E01–E10, S02E01–E03").
export type LibraryTitle = {
    media: Media
    listEntry: ListEntry | null
    files: LocalFile[]
    episodes: number[]
    seasons: { season: number; count: number; total: number; ranges: string }[]
    ranges: string
    specials: number
    extras: number
    size: number
    addedAt: number
    resolutions: string[]
    releaseGroups: string[]
    dir: string
    unwatched: number
    next: LocalFile | null
}
