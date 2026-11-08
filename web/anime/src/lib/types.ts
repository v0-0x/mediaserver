// Shapes returned by the Aniko API (mirrors the Go structs).

export type FuzzyDate = { year?: number | null; month?: number | null; day?: number | null }

export type Media = {
    id: number
    idMal?: number | null
    type: "ANIME" | "MANGA"
    format: string
    status: string
    season?: string
    seasonYear?: number | null
    episodes?: number | null
    chapters?: number | null
    volumes?: number | null
    duration?: number | null
    isAdult: boolean
    title: { romaji?: string; english?: string; native?: string; userPreferred?: string }
    synonyms?: string[]
    coverImage: { extraLarge?: string; large?: string; medium?: string; color?: string }
    bannerImage?: string
    genres?: string[]
    averageScore?: number | null
    meanScore?: number | null
    popularity?: number
    startDate?: FuzzyDate
    endDate?: FuzzyDate
    nextAiringEpisode?: { airingAt: number; timeUntilAiring: number; episode: number } | null
    description?: string
    countryOfOrigin?: string
    source?: string
    siteUrl?: string
    trailer?: { id: string; site: string; thumbnail: string } | null
    studios?: { nodes: { id: number; name: string }[] }
    tags?: { name: string; rank: number; isMediaSpoiler: boolean }[]
    relations?: { edges: { relationType: string; node: Media }[] }
    recommendations?: { nodes: { mediaRecommendation: Media | null }[] }
    characters?: {
        edges: {
            role: string
            node: { id: number; name: { full: string }; image: { large: string } }
            voiceActors: { id: number; name: { full: string }; image: { large: string } }[]
        }[]
    }
    rankings?: { rank: number; type: string; allTime: boolean; year?: number; season?: string; context: string }[]
    mediaListEntry?: { id: number; status: string; progress: number; score: number; repeat: number } | null
}

export type ListEntry = {
    id: number
    mediaId: number
    status: string
    progress: number
    score: number
    repeat: number
    updatedAt: number
    completedAt?: { year: number | null; month: number | null; day: number | null }
    media: Media
}

export type Viewer = {
    id: number
    name: string
    avatar: { large: string; medium: string }
    bannerImage?: string
}

export type Parsed = {
    title: string
    folderTitle: string
    season: number
    part: number
    episode: number
    episodeTitle: string
    releaseGroup: string
    resolution: string
    year: number
    kind: string
}

export type LocalFile = {
    path: string
    dir: string
    name: string
    size: number
    modTime: number
    parsed: Parsed
    mediaId: number
    episode: number
    airedEpisode: number
    kind: "main" | "special" | "nc"
    locked: boolean
    ignored: boolean
    matchScore: number
    // Shared by another Aniko app on the network (path kumo://<host>/…).
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

export type EpisodeView = {
    number: number
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

export type EntryView = {
    media: Media
    listEntry: ListEntry | null
    episodes: EpisodeView[]
    specials: EpisodeView[] | null
    others: EpisodeView[] | null
    nextEpisode: EpisodeView | null
    localCount: number
    sharedCount: number // files other Aniko apps share
    images: { banner: string; poster: string; fanart: string; clearlogo: string }
    mappings: { malId: number; anidbId: number; tvdbId: number }
    metadataNote?: string
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

export type ContinueItem = {
    media: Media
    episode: number
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
    // Anime in the library left out for now (AniList didn't say what they
    // are yet): the collection is asked for again.
    incomplete?: number
    // The list is the copy here; AniList is being asked for the current one.
    refreshing?: boolean
    genres: string[] | null
}

export type Settings = {
    library: {
        // This computer has no library of its own (it plays the shared ones).
        off?: boolean
        dir: string
        extraDirs: string[]
        // The library folder new downloads go into ("" = the first).
        downloadDir?: string
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
        preferredAudioLang: string
        preferredSubLang: string
        skipIntroAniSkip: boolean
        skipOutroAniSkip: boolean
        skipRecapAniSkip?: boolean
    }
    mpv: { path: string; socket: string; extraArgs: string; fullscreen: boolean }
    transcode: { ffmpegPath: string; ffprobePath: string; mode: string; hwAccel: string; vaapiNode: string; preset: string }
    aniCli: {
        enabled: boolean
        path: string
        defaultMode: "sub" | "dub"
        quality: string
        downloadDir: string
        downloader: string
        player: "mpv" | "builtin"
    }
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
    // dir: where downloaded manga go ("": the default, ~/Manga).
    manga: { enabled: boolean; defaultProvider: string; readingMode: "long-strip" | "paged" | "double"; direction: string; dir?: string }
    anilist: { clientId: string; hideAdult: boolean }
    server: { host: string; port: number; allowLan: boolean; password: string; webUi: boolean }
    // downloadTo: the Aniko this one's downloads go to (its ID; "" here).
    // keepAwake: don't let this computer sleep while another device plays its
    // files. autoSync: sync watch history when a paired device comes back.
    // syncPrefs: the theme and player settings kept in step with the other
    // devices on the same AniList account, through an Aniko Hub.
    sharing: { enabled: boolean; name: string; downloadTo?: string; keepAwake?: boolean; autoSync?: boolean; syncPrefs?: boolean; syncLists?: boolean }
    notify?: { newEpisodes: boolean; downloads: boolean }
    // Which releases Aniko updates to: stable ones, or betas too.
    updates?: { channel: "stable" | "beta" }
    ui: {
        accentColor: string
        showAdult: boolean
        bannerType: string
        cardSize: string
        reducedMotion: boolean
        showEpisodeTitle: boolean
        blurUnwatched: boolean
        titleLanguage?: "english" | "romaji"
        homeSections: string[]
        // lib/themes.ts: a COLOR_THEMES id; "auto", "off" or a HOLIDAYS id.
        colorTheme?: string
        seasonal?: string
        seasonalEffects?: boolean
        // Aniko's look from before 2.0.
        classicLook?: boolean
    }
    discord: { richPresence: boolean; clientId: string }
    extensions: { marketplaceUrl: string; autoUpdate: boolean }
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
    user: Viewer | null
    loggedIn: boolean
    anilistAuthUrl: string
    features: { mpv: boolean; ffmpeg: boolean; ffprobe: boolean; aniCli: boolean; aniCliHost?: string; ytDlp: boolean; xdgOpen: boolean }
    // owner: a browser on the network signed in to an Aniko Hub's dashboard.
    client: "desktop" | "local" | "lan" | "owner"
    // An Aniko Hub (aniko --hub): it shows its dashboard, not the app. setup:
    // its password isn't set yet (set with its setup code).
    hub?: { setup: boolean }
    hostname: string
    platform?: string // "linux", "windows"…
    scanning: boolean
    dataDir: string
    listenAddr: string
    lanUrls?: string[] | null // where devices on the home network open Aniko
    paired?: string[] | null // the Anikos this one is paired with (library sharing): Home's Sync
    // The computers sharing their library with this one that don't answer
    // (asleep or off), and whether they can be woken up from here.
    asleep?: { id: string; name: string; canWake: boolean; waking: boolean }[] | null
    // Home's questions about the other Aniko apps (only for this computer):
    // share with a new one ("new"), or update one of the two ("older": its
    // Aniko is older than this one, "newer": this one is).
    devicePrompts?: DevicePrompt[] | null
    keptAwake?: boolean // another device is playing this computer's files, so it stays awake
    webUiForced: boolean
    setupRunning?: boolean // Windows: the window installing Aniko's programs is open
    settings: Settings
}

export type DevicePrompt = { kind: "new" | "older" | "newer"; id: string; name: string; platform?: string; version: string }

export type PlaybackSession = {
    id: string
    mediaId: number
    episode: number
    title: string
    source: string
    player: string
    position: number
    duration: number
    paused: boolean
    active: boolean
    progressUpdated: boolean
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
    // The computer the file is on can't draw its image subtitles (too old).
    noBurn?: string
}

export type StreamSource = {
    url: string
    originalUrl: string
    type: string
    quality: string
    label?: string
    server: string
    subtitles: { url: string; language: string; isDefault: boolean; original: string }[]
    headers: Record<string, string>
    referrer?: string
}

export type OnlineProvider = { id: string; name: string; icon: string; supportsDub: boolean; servers: string[]; builtin: boolean }

export type StreamEpisodes = {
    provider: string
    mapping: { id: string; title: string; score: number; query?: string; index?: number } | null
    episodes: { number: number; id: string; title: string }[] | null
    dub: boolean
}

export type DownloadItem = {
    id: string
    mediaId: number
    episode: number
    animeTitle: string
    image: string
    mode: string
    source: string
    provider: string
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
    episodeNumber: number
    isBatch: boolean
    isBestRelease: boolean
    confirmed: boolean
    trusted: boolean
    remake: boolean
    dub: boolean
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
    // A plugin's permission scopes Aniko doesn't have: what it does with them won't work here.
    unsupported?: string[]
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

export type ScheduleItem = {
    id: number
    airingAt: number
    timeUntilAiring: number
    episode: number
    media: Media
    listStatus: string
}

export type AutoRule = {
    id?: number
    enabled: boolean
    mediaId: number
    title: string
    releaseGroups: string[]
    resolutions: string[]
    additionalTerms: string[]
    episodeType: "recent" | "all"
    minSeeders: number
    provider: string
}

// Where reading a manga stopped (GET /api/manga/{id}/position).
export type MangaPosition = {
    provider: string
    chapterId: string
    chapter: string
    page: number // from 0
    pages: number
    updatedAt: number
}

export type MangaChapter = {
    id: string
    url: string
    title: string
    chapter: string
    index: number
    scanlator?: string
    language?: string
    updatedAt?: string
}

// A library folder as found on disk (Library tools, manual matching).
export type FolderInfo = {
    dir: string
    label: string // path below its library folder
    videos: number // video files directly inside
    notIndexed: number // of those, not in the library index yet
    problem?: string // why it has nothing to match (still downloading, unreadable…)
}

// Aniko's own updates (GET /api/update, and the "update-status" event).
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

// Library sharing with other Aniko apps on the network.
export type SharingPeer = {
    id: string
    name: string
    address: string
    online: boolean
    lastSeen: number
    version: string
    allowed: boolean // this Aniko shares its library with it
    shares: boolean // it shares its library with this Aniko
    files: number
    manual: boolean
    error?: string
    // Granted to it (with allowed): this Aniko's AniList account, downloading here.
    account: boolean
    downloads: boolean
    // Granted by it (it shares): its AniList account (and whether this Aniko
    // uses it), downloading there.
    sharesAccount: boolean
    usingAccount: boolean
    takesDownloads: boolean
    // It answers when asked, but none of its announcements got through in a
    // minute: found by its address alone (a firewall on it).
    noBeacons?: boolean
    // It shares with this Aniko and has said how to wake it up (Wake-on-LAN);
    // waking: a wake-up was just sent.
    canWake?: boolean
    waking?: boolean
    platform?: string // what it runs on ("android": a TV)
    declined?: boolean // asked on Home whether to share with it: "not now"
    role?: "hub" // an Aniko Hub (no player: it shares and keeps devices in step)
    parental?: boolean // parental controls are set for it here
}

export type SharingStatus = { enabled: boolean; id: string; name: string; listening: boolean; peers: SharingPeer[]; addresses: string[]; listsSynced?: { at: number; with: string } }

export type SharedLibrary = { host: SharingPeer; files: LocalFile[] }

// The profile page (server: anilist/social.go).
export type UserLite = { id: number; name: string; avatar: { large: string; medium: string } }

export type Profile = {
    id: number
    name: string
    about: string
    siteUrl: string
    createdAt: number
    bannerImage: string
    avatar: { large: string }
    options: { profileColor: string }
    isFollowing: boolean
    isFollower: boolean
    statistics: {
        anime: {
            count: number
            episodesWatched: number
            minutesWatched: number
            meanScore: number
            genres: { genre: string; count: number }[] | null
            statuses: { status: string; count: number }[] | null
        }
        manga: { count: number; chaptersRead: number; volumesRead: number; meanScore: number }
    }
    favourites: { anime: Media[]; manga: Media[]; characters: { id: number; name: { full: string }; image: { large: string }; siteUrl: string }[] }
    // The first 50 of each (by name); the counts are null when AniList
    // didn't say (followsError), and More means "at least that many".
    following: UserLite[]
    followers: UserLite[]
    followingCount: number | null
    followersCount: number | null
    followingMore?: boolean
    followersMore?: boolean
    followsError?: string
    own: boolean
}

export type Activity = {
    id: number
    kind: "list" | "text"
    status?: string
    progress?: string
    text?: string
    createdAt: number
    likeCount: number
    replyCount: number
    isLiked: boolean
    siteUrl: string
    user: UserLite
    media?: Media
}

export type ActivityPage = { items: Activity[]; hasMore: boolean }

// An Aniko Hub's dashboard (GET /api/hub/overview; server: api/hub.go).
export type HubSystem = {
    model: string // "Raspberry Pi 4 Model B Rev 1.4"
    hostname: string
    arch: string
    cores: number
    cpu: number // all cores, 0-100
    anikoCpu: number // the hub's own share
    load: number
    memTotal: number
    memUsed: number
    anikoMem: number
    temp: number // °C, 0 unknown
    throttled: boolean // the Pi slows itself down (heat, power)
    uptime: number // seconds
    started: number // ms
    netRx: number // bytes a second
    netTx: number
    disks: { path: string; total: number; free: number }[] | null
}

export type HubStream = {
    id: number
    device: string
    file: string
    kind: "file" | "remux" | "transcode" | "hls"
    started: number // ms
    ended?: number
    bytes: number
    rate: number // bytes a second
}

export type HubPlaying = {
    device: string
    name: string
    user: number
    mediaId: number
    episode: number
    position: number
    duration: number
    at: number // ms
    title: string
    cover: string
}

export type HubDevice = SharingPeer & {
    user: number // the AniList account it's on (0: none)
    accountUser: number // the login the hub gives it (with account)
}

export type HubAccount = { user: number; name: string; avatar: string; givenBy: string; addedAt: number; devices: number; history: number }

// The hub's audit log (server/internal/anikohub/audit.go). Who: a device by
// name, or by "dashboard" (its owner, at ip) or "hub"; an ip alone is
// someone not signed in.
export type HubEvent = {
    id: number
    at: number
    kind: string
    device?: string
    by?: "dashboard" | "hub"
    ip?: string
    user?: number
    text: string
    failed?: boolean
    // More about it, on a line of its own (why it didn't work).
    detail?: string
    mediaId?: number
    episode?: number
    title?: string
    files?: { name: string; path: string; size: number; episode?: number }[]
    size?: number
    // Deleted episodes: how Download again gets them.
    again?: { mediaId: number; episodes: number[]; dub?: boolean; provider?: string; quality?: string; queued?: number }
}

// Where a scan is (server: library.ScanProgress).
export type ScanProgress = { stage: "walking" | "matching" | "saving"; done: number; total: number; message: string }

export type HubOverview = {
    name: string
    version: string
    // mediaserver's version, and the port of its movies side's pages.
    server?: string
    moviesPort?: number
    addresses: string[] | null
    port: number
    system: HubSystem
    streams: HubStream[]
    recent: HubStream[]
    sent: number
    playing: HubPlaying[]
    devices: HubDevice[]
    accounts: HubAccount[]
    activity: HubEvent[] | null
    library: { folders: string[] | null; files: number; anime: number; size: number; unmatched: number; scanning: boolean; progress?: ScanProgress | null }
    encoder: string // "" no ffmpeg
    downloads: number
}

// Parental controls (server: internal/parental): what a device may watch,
// set on the computer or hub that shares with it.
export type ParentalRules = {
    noAdult?: boolean
    genres?: string[] | null
    blocked?: number[] | null
    onlyAllowed?: boolean
    allowed?: number[] | null
    setAt?: number
}

export type ParentalWatched = {
    mediaId: number
    episode: number
    position: number
    duration: number
    source: string
    at: number // unix seconds
    title: string
    cover: string
    adult: boolean
}

export type DeviceParental = {
    rules: ParentalRules | null
    hasPin: boolean
    watched: ParentalWatched[]
    anime: { id: number; title: string; cover: string }[]
}

// This device's own (GET /api/parental).
export type ParentalState = { active: boolean; rules: ParentalRules | null; from: { id: string; name: string; hasPin: boolean }[] }

// The watch order of an anime's franchise (/api/anime/{id}/watch-order).
export type WatchOrderEntry = {
    media: Media
    kind: "main" | "side" | "recap"
    // Story order: no prequel/sequel link places it; its release date does.
    byDate?: boolean
    // Story order: AniList's links contradict each other here.
    conflict?: boolean
    hidden?: boolean
    // Extras: SPIN_OFF or ALTERNATIVE.
    relation?: string
}

export type WatchOrder = {
    key: number
    story: WatchOrderEntry[]
    release: WatchOrderEntry[]
    extras: WatchOrderEntry[]
    partial?: boolean
    edited?: boolean
}

// Downloaded manga (GET /api/manga-library…) and their downloads
// (/api/manga-downloads).
export type MangaSeries = {
    anilistId: number
    title: string
    english?: string
    romaji?: string
    description?: string
    genres?: string[]
    status?: string
    year?: number
    chapters?: number
    folder: string
    count: number
    size: number
    hasCover: boolean
    latest?: string
    updated: number
}
export type LocalMangaChapter = { chapter: string; title?: string; file: string; pages: number; size: number; added: number }
export type MangaDownload = {
    id: string
    mediaId: number
    title: string
    provider: string
    chapterId: string
    chapter: string
    name?: string
    target?: string
    targetName?: string
    state: "queued" | "downloading" | "sending" | "done" | "failed"
    pages: number
    got: number
    error?: string
    added: number
}
