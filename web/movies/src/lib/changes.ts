// What's new in KumoBox, newest first: shown once on each device after an
// update (components/WhatsNew). A new entry gets the next id.

export type Change = { title: string; text: string; to?: string }
export type Release = { id: number; title: string; changes: Change[] }

export const RELEASES: Release[] = [
    {
        id: 3,
        title: "Upload to your server",
        changes: [
            {
                title: "Send files and folders",
                text: "Movies, shows (whole folders of them) and their subtitles from this computer into a server's library: Local library › Upload, or Upload next to it in Settings › Other devices. An upload cut short carries on where it stopped. The server lets this device download there first (Let it download here, on it).",
                to: "/local",
            },
            {
                title: "mediaserver",
                text: "KumoBox's home server, for a Raspberry Pi or any Linux computer that stays on, shares movies and shows with your KumoBox apps (and anime with your Aniko apps): it shows up in Settings › Other devices.",
                to: "/settings?tab=devices",
            },
        ],
    },
    {
        id: 2,
        title: "KumoBox 2.0",
        changes: [
            {
                title: "A new look",
                text: "Rounder panels, a sidebar in groups, a Back arrow, and a layout for phones with tabs along the bottom. Prefer the old one? Settings › Appearance › Classic look.",
                to: "/settings?tab=appearance",
            },
            {
                title: "Themes and holidays",
                text: "Pick the app's colors from the Themes button under the sidebar. Holiday themes come on by themselves (snow at Christmas, bats at Halloween), with a row of picks on Discover; turn them off in Settings › Appearance.",
                to: "/settings?tab=appearance",
            },
            {
                title: "Search in Settings",
                text: "Settings is regrouped, and the box at the top finds any setting by its name.",
                to: "/settings",
            },
            {
                title: "Alerts",
                text: "A notification when a new episode of a show you're watching comes out, and when a download is ready. Settings › Notifications.",
                to: "/settings?tab=notifications",
            },
            {
                title: "Beta updates",
                text: "Get each new version first, before everyone else: Settings › Updates & about › Beta updates. Off by default.",
                to: "/settings?tab=updates",
            },
            {
                title: "Backup and restore",
                text: "Your settings, watch history, list and diary, choices and extensions as one file, to keep or to move to another computer. Settings › Backup & data.",
                to: "/settings?tab=backup",
            },
            {
                title: "Clean up and missing episodes",
                text: "Library tools now shows how full the drive is, what you've watched and what's there twice, with a trash to put files back from; and the episodes missing from your shows, with their torrents a click away.",
                to: "/library?tab=cleanup",
            },
            {
                title: "Picture in picture",
                text: "In the desktop app, the player's new button (or p) keeps playing in a small window on top of the others.",
            },
            {
                title: "Subtitles your way",
                text: "Their size, background and position in Settings › Playback, and their timing while playing: z and x move them, kept for each movie or show.",
                to: "/settings?tab=playback",
            },
            {
                title: "Your other computers",
                text: "KumoBox asks on Home whether to share with a new one, wakes a sleeping one to play its files, keeps this one awake while another plays from it, and syncs Continue watching when they meet again.",
                to: "/settings?tab=devices",
            },
            {
                title: "What should I watch?",
                text: "The dice on Home pick something from your list or your library.",
                to: "/",
            },
        ],
    },
    {
        id: 1,
        title: "More free streaming sites",
        changes: [
            {
                title: "Cinejoy, Vidlink, Hexa, Xpass, Mapple and Videasy",
                text: "Streaming sites from luffy and FreeFlix, built in and off by default: turn them on in Settings › Streaming providers.",
                to: "/settings?tab=providers",
            },
        ],
    },
]
