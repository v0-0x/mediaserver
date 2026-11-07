// Package config holds the user settings and resolves the XDG paths Aniko uses.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

const (
	AppName = "Aniko"

	DefaultPort           = 43211
	DefaultAnilistClient  = "13985"
	DefaultMarketplaceURL = "https://raw.githubusercontent.com/Bas1874/Seanime-Marketplace/refs/heads/main/Marketplace/Main.json"

	// UpdateRepo is where mediaserver looks for its updates (its releases,
	// server-v<version>), and UpdateRepoID that repository's number, which
	// the API is asked by: should the repository be renamed, its old name
	// could be taken by someone else.
	UpdateRepo   = "v0-0x/mediaserver"
	UpdateRepoID = "1441645210"
)

// Set when building, with -ldflags "-X <module>/internal/config.AppVersion=…
// -X <module>/internal/config.Commit=…": packaging/arch/PKGBUILD and the
// Windows release workflow do, from the commit they build.
var (
	// AppVersion is 2.0.<commits since 2.0.0's> in those builds (1.0.<number
	// of commits> before it; see update.VersionOf).
	AppVersion = "1.0.0"
	// Commit is the git commit Aniko was built from; see BuildCommit.
	Commit = ""
)

// BuildCommit is the commit Aniko was built from: Commit, or else the one Go
// stamps into builds made inside a git checkout, or "" when unknown (e.g.
// built from a source zip).
func BuildCommit() string {
	if Commit != "" {
		return Commit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

// Settings is the whole user configuration. It is persisted as a single JSON
// document in the kv table and edited from the Settings pages of the UI.
type Settings struct {
	// SchemaVersion lets newer versions migrate settings saved by older ones.
	SchemaVersion int `json:"schemaVersion"`

	Library      LibrarySettings      `json:"library"`
	Playback     PlaybackSettings     `json:"playback"`
	Mpv          MpvSettings          `json:"mpv"`
	Transcode    TranscodeSettings    `json:"transcode"`
	AniCli       AniCliSettings       `json:"aniCli"`
	OnlineStream OnlineStreamSettings `json:"onlineStream"`
	Torrent      TorrentSettings      `json:"torrent"`
	Qbittorrent  TorrentClientConfig  `json:"qbittorrent"`
	Transmission TorrentClientConfig  `json:"transmission"`
	Manga        MangaSettings        `json:"manga"`
	Anilist      AnilistSettings      `json:"anilist"`
	Server       ServerSettings       `json:"server"`
	Sharing      SharingSettings      `json:"sharing"`
	UI           UISettings           `json:"ui"`
	Discord      DiscordSettings      `json:"discord"`
	Extensions   ExtensionSettings    `json:"extensions"`
	Notify       NotifySettings       `json:"notify"`
	Updates      UpdateSettings       `json:"updates"`
}

// UpdateSettings: which releases the updater offers (package update).
type UpdateSettings struct {
	// Channel: "stable", the releases for everyone, or "beta", which also
	// gets the newer pre-releases each change is published as first.
	Channel string `json:"channel"`
}

// UpdateChannels are the values UpdateSettings.Channel takes.
var UpdateChannels = []string{"stable", "beta"}

// NotifySettings: what Aniko tells about as it happens (package notify).
type NotifySettings struct {
	// NewEpisodes: an episode of an anime being watched aired.
	NewEpisodes bool `json:"newEpisodes"`
	// Downloads: an episode or a torrent finished downloading.
	Downloads bool `json:"downloads"`
}

type LibrarySettings struct {
	// Off: this computer has no library of its own; it plays the ones a
	// Aniko Hub or other computers share with it. The folders below are kept
	// for when it's turned back on.
	Off       bool     `json:"off"`
	Dir       string   `json:"dir"`
	ExtraDirs []string `json:"extraDirs"`
	// DownloadDir: the library folder new downloads (streams, torrents) go
	// into, when the anime has no folder yet; empty = Dir. One of the
	// folders above (see DownloadDir()).
	DownloadDir      string `json:"downloadDir"`
	AutoRefresh      bool   `json:"autoRefresh"`
	RefreshOnStartup bool   `json:"refreshOnStartup"`
	// Minimum similarity (0-1) required to auto-match a title.
	MatchThreshold float64 `json:"matchThreshold"`
	// Glob patterns (matched against file/dir names) that the scanner skips.
	IgnorePatterns []string `json:"ignorePatterns"`
	// Also try to match against all of AniList, not only the user's list.
	MatchOutsideList bool `json:"matchOutsideList"`
}

type PlaybackSettings struct {
	// "mpv" (desktop media player) or "builtin" (in-app web player).
	DefaultPlayer       string  `json:"defaultPlayer"`
	AutoUpdateProgress  bool    `json:"autoUpdateProgress"`
	CompletionThreshold float64 `json:"completionThreshold"` // 0-1
	ResumePlayback      bool    `json:"resumePlayback"`
	AutoPlayNext        bool    `json:"autoPlayNext"`
	RememberTracks      bool    `json:"rememberTracks"`
	PreferredAudioLang  string  `json:"preferredAudioLang"` // e.g. "jpn,ja"
	PreferredSubLang    string  `json:"preferredSubLang"`   // e.g. "eng,en"
	SkipIntroAniSkip    bool    `json:"skipIntroAniSkip"`   // openings
	SkipOutroAniSkip    bool    `json:"skipOutroAniSkip"`   // endings
	SkipRecapAniSkip    bool    `json:"skipRecapAniSkip"`   // recaps (and previews)
}

type MpvSettings struct {
	Path       string `json:"path"`
	Socket     string `json:"socket"`
	ExtraArgs  string `json:"extraArgs"`
	Fullscreen bool   `json:"fullscreen"`
}

type TranscodeSettings struct {
	FfmpegPath  string `json:"ffmpegPath"`
	FfprobePath string `json:"ffprobePath"`
	// "auto" picks direct play when the browser can decode the file,
	// remux when only the container is the problem, transcode otherwise.
	Mode      string `json:"mode"`
	HwAccel   string `json:"hwAccel"` // "", "vaapi", "nvenc", "qsv", "v4l2m2m" (Raspberry Pi)
	VaapiNode string `json:"vaapiNode"`
	Preset    string `json:"preset"`
}

type AniCliSettings struct {
	Enabled     bool   `json:"enabled"`
	Path        string `json:"path"`
	DefaultMode string `json:"defaultMode"` // sub | dub
	Quality     string `json:"quality"`     // best | worst | 1080 | 720 ...
	// Directory where ani-cli downloads are written. Empty = library directory.
	DownloadDir string `json:"downloadDir"`
	Downloader  string `json:"downloader"` // auto | yt-dlp | ffmpeg | aria2c
	// Where streams open: "mpv" or "builtin".
	Player string `json:"player"`
}

type OnlineStreamSettings struct {
	Enabled         bool   `json:"enabled"`
	DefaultProvider string `json:"defaultProvider"` // "ani-cli" or an extension id
	PreferDub       bool   `json:"preferDub"`
}

type TorrentSettings struct {
	DefaultClient   string `json:"defaultClient"`   // qbittorrent | transmission | none
	DefaultProvider string `json:"defaultProvider"` // nyaa | animetosho | extension id
	ShowActiveCount bool   `json:"showActiveCount"`
	// Create a sub folder per anime inside the library directory.
	CreateSubfolder     bool   `json:"createSubfolder"`
	PreferredResolution string `json:"preferredResolution"`
	AutoDownloader      bool   `json:"autoDownloader"`
	AutoDownloadMinutes int    `json:"autoDownloadMinutes"`
}

type TorrentClientConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Executable string `json:"executable"`
	Tags       string `json:"tags"`
	Category   string `json:"category"`
	UseHTTPS   bool   `json:"useHttps"`
}

type MangaSettings struct {
	Enabled         bool   `json:"enabled"`
	DefaultProvider string `json:"defaultProvider"`
	ReadingMode     string `json:"readingMode"` // long-strip | paged | double
	Direction       string `json:"direction"`   // ltr | rtl
	// Dir is where downloaded manga go ("": ~/Manga on a computer, the
	// data folder's manga folder on a TV or a server; app.MangaDir).
	Dir string `json:"dir,omitempty"`
}

type AnilistSettings struct {
	ClientID string `json:"clientId"`
	// Hide adult entries everywhere.
	HideAdult bool `json:"hideAdult"`
}

// SharingSettings: library sharing with other Aniko apps on the home network
// (package share). Which ones this one shares its library with is kept
// with the Anikos it knows, not here.
type SharingSettings struct {
	Enabled bool `json:"enabled"`
	// Name: what the others call this Aniko (empty: the computer's name).
	Name string `json:"name"`
	// DownloadTo: the Aniko this one's downloads and torrents go to (its ID;
	// empty: saved here). Set when one allows it (package share).
	DownloadTo string `json:"downloadTo,omitempty"`
	// KeepAwake: this computer doesn't go to sleep while other devices play
	// its files (package awake).
	KeepAwake bool `json:"keepAwake"`
	// AutoSync: Continue watching is synced with the paired Anikos when they
	// come back, without pressing Sync.
	AutoSync bool `json:"autoSync"`
	// SyncPrefs: with an Aniko Hub, the theme and the like (SharedPrefs) are
	// the same on every device on this AniList account: changed here, they
	// change there.
	SyncPrefs bool `json:"syncPrefs"`
	// SyncLists: with an Aniko Hub, the local list (on no AniList account)
	// and the watch-order edits are kept in step between the devices
	// (share/hub.go, Blobs). Experimental.
	SyncLists bool `json:"syncLists"`
}

type ServerSettings struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// When true Aniko listens on every interface so other devices on the home
	// network can use it. Requests from public (non-private) addresses are
	// always refused.
	AllowLAN bool `json:"allowLan"`
	// Optional password required from LAN clients (localhost never needs it).
	Password string `json:"password"`
	// Serve the web UI to browsers (http://127.0.0.1:<port>). The desktop
	// window always works.
	WebUI bool `json:"webUi"`
}

type UISettings struct {
	AccentColor      string   `json:"accentColor"`
	ShowAdult        bool     `json:"showAdult"`
	BannerType       string   `json:"bannerType"` // episode | banner | none
	CardSize         string   `json:"cardSize"`   // sm | md | lg
	ReducedMotion    bool     `json:"reducedMotion"`
	ShowEpisodeTitle bool     `json:"showEpisodeTitle"`
	BlurUnwatched    bool     `json:"blurUnwatched"` // spoiler protection
	HomeSections     []string `json:"homeSections"`
	// The anime titles: "english" (where AniList has one) or "romaji".
	TitleLanguage string `json:"titleLanguage"`
	// The colors of the app (ColorThemes), and the holiday themes: "auto"
	// (each in its month, see web/src/lib/themes.ts), "off", or one of
	// Holidays always. SeasonalEffects: their snow, bats, hearts...
	ColorTheme      string `json:"colorTheme"`
	Seasonal        string `json:"seasonal"`
	SeasonalEffects bool   `json:"seasonalEffects"`
	// Aniko's look from before 2.0 (its components, sidebar and holiday
	// themes), with the same color themes.
	ClassicLook bool `json:"classicLook"`
}

// ColorThemes and Holidays are the values UISettings takes (their colors are
// in web/src/lib/themes.ts).
var (
	ColorThemes = []string{"aniko", "midnight", "forest", "sakura", "ember", "ocean", "dusk", "black"}
	Holidays    = []string{"newyear", "valentine", "sakura", "summer", "halloween", "christmas"}
)

type DiscordSettings struct {
	RichPresence bool   `json:"richPresence"`
	ClientID     string `json:"clientId"`
}

type ExtensionSettings struct {
	MarketplaceURL string `json:"marketplaceUrl"`
	AutoUpdate     bool   `json:"autoUpdate"`
}

// Defaults returns the settings used on first launch.
func Defaults() Settings {
	home, _ := os.UserHomeDir()
	videos := filepath.Join(home, videosFolder, "Anime")
	s := Settings{
		SchemaVersion: schemaVersion,
		Library: LibrarySettings{
			Dir:              videos,
			ExtraDirs:        []string{},
			AutoRefresh:      true,
			RefreshOnStartup: true,
			MatchThreshold:   0.5,
			IgnorePatterns:   []string{"*sample*", "*.part", "*NCOP*", "*NCED*"},
			MatchOutsideList: true,
		},
		Playback: PlaybackSettings{
			DefaultPlayer:       "builtin",
			AutoUpdateProgress:  true,
			CompletionThreshold: 0.85,
			ResumePlayback:      true,
			AutoPlayNext:        false,
			RememberTracks:      true,
			PreferredAudioLang:  "jpn,ja",
			PreferredSubLang:    "eng,en",
		},
		Mpv: MpvSettings{Path: "mpv"},
		Transcode: TranscodeSettings{
			FfmpegPath:  "ffmpeg",
			FfprobePath: "ffprobe",
			Mode:        "auto",
			Preset:      "veryfast",
			VaapiNode:   "/dev/dri/renderD128",
		},
		AniCli: AniCliSettings{
			Enabled:     true,
			Path:        "ani-cli",
			DefaultMode: "sub",
			Quality:     "best",
			Downloader:  "auto",
			Player:      "builtin",
		},
		OnlineStream: OnlineStreamSettings{Enabled: true, DefaultProvider: "ani-cli"},
		Torrent: TorrentSettings{
			DefaultClient:       "qbittorrent",
			DefaultProvider:     "nyaa",
			ShowActiveCount:     true,
			CreateSubfolder:     true,
			PreferredResolution: "1080",
			AutoDownloadMinutes: 20,
		},
		Qbittorrent:  TorrentClientConfig{Host: "127.0.0.1", Port: 8081, Username: "admin", Executable: findQbittorrent(home)},
		Transmission: TorrentClientConfig{Host: "127.0.0.1", Port: 9091, Executable: defaultTransmission()},
		Manga:        MangaSettings{Enabled: true, ReadingMode: "double", Direction: "rtl"},
		Anilist:      AnilistSettings{ClientID: DefaultAnilistClient, HideAdult: true},
		Server:       ServerSettings{Host: "127.0.0.1", Port: DefaultPort, WebUI: true},
		UI: UISettings{
			AccentColor:      "#7c6cf2",
			BannerType:       "episode",
			CardSize:         "md",
			ShowEpisodeTitle: true,
			HomeSections:     []string{"continue", "genres", "watching", "airing", "planning", "completed"},
			TitleLanguage:    "english",
			ColorTheme:       "aniko",
			Seasonal:         "auto",
			SeasonalEffects:  true,
		},
		Discord:    DiscordSettings{RichPresence: false},
		Extensions: ExtensionSettings{MarketplaceURL: DefaultMarketplaceURL},
		Sharing:    SharingSettings{KeepAwake: true, AutoSync: true, SyncPrefs: true, SyncLists: true},
		Notify:     NotifySettings{NewEpisodes: true, Downloads: true},
		Updates:    UpdateSettings{Channel: "stable"},
	}
	if runtime.GOOS == "android" {
		// The Android app (a TV's): it plays the libraries the computers
		// share with it, and has none of its own.
		s.Library.Dir, s.Library.RefreshOnStartup, s.Library.AutoRefresh = "", false, false
		s.Sharing.Enabled = true
		// A TV is slow at drawing them over the pages: on when asked for.
		s.UI.SeasonalEffects = false
	}
	return s
}

// Store loads and saves settings, keeping a cached copy in memory.
type Store struct {
	db       *db.DB
	mu       sync.RWMutex
	current  Settings
	onChange []func(old, new Settings)
}

const settingsKey = "settings"

func NewStore(d *db.DB) (*Store, error) {
	s := &Store{db: d, current: Defaults()}
	// Decode on top of the defaults so fields added in newer versions get
	// sane values.
	merged := Defaults()
	merged.SchemaVersion = 0 // missing in settings saved by older versions
	ok, err := d.GetKV(settingsKey, &merged)
	if err != nil {
		return nil, err
	}
	if ok {
		migrated := movedPaths(&merged)
		migrated = migrate(&merged) || migrated
		s.current = sanitize(merged)
		if migrated {
			if err := d.SetKV(settingsKey, s.current); err != nil {
				return nil, err
			}
		}
	} else {
		if err := d.SetKV(settingsKey, s.current); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Decode reads settings saved as JSON (a backup's, say) as they're loaded:
// on top of the defaults, brought up to this version.
func Decode(raw []byte) (Settings, error) {
	s := Defaults()
	s.SchemaVersion = 0
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), err
	}
	migrate(&s)
	return sanitize(s), nil
}

// Get returns a copy of the settings. Slices are copied too: callers decode
// JSON into the result, which would otherwise write into the live settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.clone()
}

func (c Settings) clone() Settings {
	c.Library.ExtraDirs = slices.Clone(c.Library.ExtraDirs)
	c.Library.IgnorePatterns = slices.Clone(c.Library.IgnorePatterns)
	c.UI.HomeSections = slices.Clone(c.UI.HomeSections)
	return c
}

func (s *Store) Save(next Settings) (Settings, error) {
	next = sanitize(next.clone())
	if err := s.db.SetKV(settingsKey, next); err != nil {
		return s.Get(), err
	}
	s.mu.Lock()
	old := s.current
	s.current = next
	listeners := append([]func(old, new Settings){}, s.onChange...)
	s.mu.Unlock()
	// Listeners get their own copies; none of them may touch s.current.
	for _, fn := range listeners {
		go fn(old.clone(), next.clone())
	}
	return next.clone(), nil
}

// OnChange registers a callback invoked (in a goroutine) after every save.
func (s *Store) OnChange(fn func(old, new Settings)) {
	s.mu.Lock()
	s.onChange = append(s.onChange, fn)
	s.mu.Unlock()
}

// schemaVersion is the current settings version; add a step to migrate when
// changing a default that existing installs should pick up too.
const schemaVersion = 5

func migrate(s *Settings) bool {
	if s.SchemaVersion >= schemaVersion {
		return false
	}
	if s.SchemaVersion < 2 {
		// v2: the in-app player is the default for files and streams.
		s.Playback.DefaultPlayer = "builtin"
		s.AniCli.Player = "builtin"
	}
	if s.SchemaVersion < 3 && s.Manga.ReadingMode == "long-strip" {
		// v3: manga reads two pages side by side, rather than the long
		// strip that was the default.
		s.Manga.ReadingMode = "double"
	}
	if s.SchemaVersion < 4 && s.Manga.Direction == "ltr" {
		// v4: manga reads right to left, as printed (the first page of two
		// on the right), rather than left to right, the default before.
		s.Manga.Direction = "rtl"
	}
	if s.SchemaVersion < 5 {
		// v5: endings can be skipped too, with a setting of their own; it
		// starts out like the openings one.
		s.Playback.SkipOutroAniSkip = s.Playback.SkipIntroAniSkip
	}
	s.SchemaVersion = schemaVersion
	return true
}

func sanitize(s Settings) Settings {
	d := Defaults()
	s.SchemaVersion = schemaVersion
	if s.Server.Port <= 0 || s.Server.Port > 65535 {
		s.Server.Port = d.Server.Port
	}
	if s.Server.Host == "" {
		s.Server.Host = d.Server.Host
	}
	if s.Playback.CompletionThreshold <= 0 || s.Playback.CompletionThreshold > 1 {
		s.Playback.CompletionThreshold = d.Playback.CompletionThreshold
	}
	if s.Library.MatchThreshold <= 0 || s.Library.MatchThreshold > 1 {
		s.Library.MatchThreshold = d.Library.MatchThreshold
	}
	if s.Library.ExtraDirs == nil {
		s.Library.ExtraDirs = []string{}
	}
	if s.Library.IgnorePatterns == nil {
		s.Library.IgnorePatterns = []string{}
	}
	if s.Mpv.Path == "" {
		s.Mpv.Path = "mpv"
	}
	if s.Transcode.FfmpegPath == "" {
		s.Transcode.FfmpegPath = "ffmpeg"
	}
	if s.Transcode.FfprobePath == "" {
		s.Transcode.FfprobePath = "ffprobe"
	}
	if s.Transcode.Mode == "" {
		s.Transcode.Mode = "auto"
	}
	if s.Transcode.Preset == "" {
		s.Transcode.Preset = "veryfast"
	}
	if s.AniCli.Path == "" {
		s.AniCli.Path = "ani-cli"
	}
	if s.AniCli.DefaultMode != "dub" {
		s.AniCli.DefaultMode = "sub"
	}
	if s.AniCli.Quality == "" {
		s.AniCli.Quality = "best"
	}
	if s.AniCli.Player != "mpv" {
		s.AniCli.Player = "builtin"
	}
	if s.Anilist.ClientID == "" {
		s.Anilist.ClientID = DefaultAnilistClient
	}
	if s.Extensions.MarketplaceURL == "" {
		s.Extensions.MarketplaceURL = DefaultMarketplaceURL
	}
	if s.Torrent.AutoDownloadMinutes < 5 {
		s.Torrent.AutoDownloadMinutes = 20
	}
	if s.UI.AccentColor == "" {
		s.UI.AccentColor = d.UI.AccentColor
	}
	if s.UI.TitleLanguage != "romaji" {
		s.UI.TitleLanguage = "english"
	}
	if !slices.Contains(UpdateChannels, s.Updates.Channel) {
		s.Updates.Channel = d.Updates.Channel
	}
	if !slices.Contains(ColorThemes, s.UI.ColorTheme) {
		s.UI.ColorTheme = d.UI.ColorTheme
	}
	if s.UI.Seasonal != "off" && !slices.Contains(Holidays, s.UI.Seasonal) {
		s.UI.Seasonal = "auto"
	}
	if len(s.UI.HomeSections) == 0 {
		s.UI.HomeSections = d.UI.HomeSections
	}
	if s.Playback.DefaultPlayer != "mpv" {
		s.Playback.DefaultPlayer = "builtin"
	}
	return s
}

// LibraryDirs returns the main library dir followed by the extra ones
// (none when the library is off).
func (s Settings) LibraryDirs() []string {
	if s.Library.Off {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range append([]string{s.Library.Dir}, s.Library.ExtraDirs...) {
		d = filepath.Clean(expandHome(d))
		if d == "." || d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// DownloadDir is the folder new downloads go into (see
// LibrarySettings.DownloadDir): the one chosen when it's one of the library
// folders, else the first library folder; "" when there's none.
func (s Settings) DownloadDir() string {
	dirs := s.LibraryDirs()
	if d := filepath.Clean(expandHome(s.Library.DownloadDir)); d != "." && d != "" && slices.Contains(dirs, d) {
		return d
	}
	if len(dirs) > 0 {
		return dirs[0]
	}
	if s.Library.Dir != "" {
		// The library is off: its first folder still takes downloads.
		return filepath.Clean(expandHome(s.Library.Dir))
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// ExpandHome is exported for other packages.
func ExpandHome(p string) string { return expandHome(p) }

// Paths -----------------------------------------------------------------

// DataDir is ~/.local/share/aniko on Linux, ~/Library/Application
// Support/Aniko on macOS and %APPDATA%\Aniko on Windows, or $ANIKO_DATA_DIR.
// Kumo's folder (Aniko's old name) moves there the first time.
func DataDir() string {
	if d := Getenv("DATA_DIR"); d != "" {
		return d
	}
	return MoveOld(oldDataDir(), defaultDataDir())
}

// RuntimeDir holds the server's address, the desktop window's token and
// other temporary files: $XDG_RUNTIME_DIR/aniko on Linux, $TMPDIR/aniko on
// macOS, %LOCALAPPDATA%\Aniko\run on Windows.
func RuntimeDir() string {
	dir := runtimeDir()
	_ = os.MkdirAll(dir, 0o700)
	return dir
}
