// Package config holds the user settings and resolves the XDG paths KumoBox uses.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/v0-0x/mediaserver/movies/internal/db"
)

const (
	AppName = "KumoBox"

	DefaultPort           = 43311
	DefaultMarketplaceURL = "https://raw.githubusercontent.com/Bas1874/Seanime-Marketplace/refs/heads/main/Marketplace/Main.json"

	// UpdateRepo is the GitHub repository KumoBox looks for updates in
	// ($KUMOBOX_UPDATE_REPO overrides it), asked for by its number
	// (UpdateRepoID): should it be renamed, the old name could be taken by
	// anyone, who would then make the updates.
	UpdateRepo   = "v0-0x/kumobox-movies"
	UpdateRepoID = "1411506405"
)

// Set when building, with -ldflags "-X <module>/internal/config.AppVersion=…
// -X <module>/internal/config.Commit=…": packaging/arch/PKGBUILD and the
// Windows release workflow do, from the commit they build.
var (
	// AppVersion is 2.0.<commits since 2.0.0's> in those builds (1.0.<number
	// of commits> before it; see update.VersionOf).
	AppVersion = "1.0.0"
	// Commit is the git commit KumoBox was built from; see BuildCommit.
	Commit = ""
)

// BuildCommit is the commit KumoBox was built from: Commit, or else the one Go
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
	Downloads    DownloadSettings     `json:"downloads"`
	OnlineStream OnlineStreamSettings `json:"onlineStream"`
	Torrent      TorrentSettings      `json:"torrent"`
	Qbittorrent  TorrentClientConfig  `json:"qbittorrent"`
	Transmission TorrentClientConfig  `json:"transmission"`
	Server       ServerSettings       `json:"server"`
	Sharing      SharingSettings      `json:"sharing"`
	UI           UISettings           `json:"ui"`
	Discord      DiscordSettings      `json:"discord"`
	Extensions   ExtensionSettings    `json:"extensions"`
	Notify       NotifySettings       `json:"notify"`
	Updates      UpdateSettings       `json:"updates"`

	// KumoBox (kumobox.go)
	TMDB           TMDBSettings          `json:"tmdb"`
	Letterboxd     LetterboxdSettings    `json:"letterboxd"`
	Providers      ProviderSettings      `json:"providers"`
	TorrentSources TorrentSourceSettings `json:"torrentSources"`
}

// UpdateSettings: which releases the updater offers (package update).
type UpdateSettings struct {
	// Channel: "stable", the releases for everyone, or "beta", which also
	// gets the newer pre-releases each change is published as first.
	Channel string `json:"channel"`
}

// UpdateChannels are the values UpdateSettings.Channel takes.
var UpdateChannels = []string{"stable", "beta"}

// NotifySettings: what KumoBox tells about as it happens (package notify).
type NotifySettings struct {
	// NewEpisodes: an episode of a show being watched aired.
	NewEpisodes bool `json:"newEpisodes"`
	// Downloads: an episode or a torrent finished downloading.
	Downloads bool `json:"downloads"`
}

type LibrarySettings struct {
	// Off: this computer has no library of its own; it plays the ones other
	// computers share with it. The folders below are kept for when it's
	// turned back on.
	Off              bool     `json:"off"`
	Dir              string   `json:"dir"`
	ExtraDirs        []string `json:"extraDirs"`
	AutoRefresh      bool     `json:"autoRefresh"`
	RefreshOnStartup bool     `json:"refreshOnStartup"`
	// Minimum similarity (0-1) required to auto-match a title.
	MatchThreshold float64 `json:"matchThreshold"`
	// Glob patterns (matched against file/dir names) that the scanner skips.
	IgnorePatterns []string `json:"ignorePatterns"`
	// Also try to match against all of TMDB, not only the user's list.
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
	// Audio in this language rather than the title's original one (a dub),
	// a list of language codes like "fre,fra,fr"; empty: the original.
	PreferredAudioLang string `json:"preferredAudioLang"`
	// Subtitles in this language, e.g. "eng,en"; empty: English, where
	// SubtitlesForForeign wants them.
	PreferredSubLang string `json:"preferredSubLang"`
	// SubtitlesForForeign turns subtitles on (in PreferredSubLang) only
	// when the audio isn't in a language of the user's; off, subtitles
	// show whenever PreferredSubLang is set. Forced subtitles in the
	// audio's language show either way (see player.ChooseTracks).
	SubtitlesForForeign bool `json:"subtitlesForForeign"`
	// Skip the parts of a video its chapter names mark as an intro or a
	// recap (SkipIntro), the end credits or a preview (SkipCredits).
	SkipIntro   bool `json:"skipIntro"`
	SkipCredits bool `json:"skipCredits"`
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

// DownloadSettings: where movies and episodes downloaded from streaming
// providers go, and whether yt-dlp may fetch them.
type DownloadSettings struct {
	// Dir is the download folder. Empty = the library folder.
	Dir string `json:"dir"`
	// Downloader is auto | yt-dlp | ffmpeg. Video files and HLS streams are
	// always fetched by KumoBox (HLS with ffmpeg); yt-dlp downloads the
	// videos of web pages, unless this is "ffmpeg".
	Downloader string `json:"downloader"`
}

type OnlineStreamSettings struct {
	Enabled bool `json:"enabled"`
	// DefaultProvider is "" to list the streams of every provider ranked
	// together, or a provider's id ("archive", "stremio:<addon id>",
	// "nuvio:<id>", "ext:<extension id>") to list its streams first.
	DefaultProvider string `json:"defaultProvider"`
	// PreferDub picks dubbed (English) audio over the original language
	// when a provider or a file has both.
	PreferDub bool `json:"preferDub"`
}

type TorrentSettings struct {
	DefaultClient   string `json:"defaultClient"`   // qbittorrent | transmission | none
	DefaultProvider string `json:"defaultProvider"` // "all" or a search source id
	ShowActiveCount bool   `json:"showActiveCount"`
	// Create a sub folder per movie or show inside the library directory.
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

// SharingSettings: library sharing with other KumoBox apps on the home network
// (package share). Which ones this one shares its library with is kept
// with the KumoBoxes it knows, not here.
type SharingSettings struct {
	Enabled bool `json:"enabled"`
	// Name: what the others call this KumoBox (empty: the computer's name).
	Name string `json:"name"`
	// DownloadTo: the KumoBox this one's downloads and torrents go to (its ID;
	// empty: saved here). Set when one allows it (package share).
	DownloadTo string `json:"downloadTo,omitempty"`
	// KeepAwake: this computer doesn't go to sleep while other devices play
	// its files (package awake).
	KeepAwake bool `json:"keepAwake"`
	// AutoSync: Continue watching is synced with the paired KumoBoxes when they
	// come back, without pressing Sync.
	AutoSync bool `json:"autoSync"`
}

type ServerSettings struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// When true KumoBox listens on every interface so other devices on the home
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
	// The colors of the app (ColorThemes), and the holiday themes: "auto"
	// (each in its month, see web/src/lib/themes.ts), "off", or one of
	// Holidays always. SeasonalEffects: their snow, bats, hearts...
	ColorTheme      string `json:"colorTheme"`
	Seasonal        string `json:"seasonal"`
	SeasonalEffects bool   `json:"seasonalEffects"`
	// KumoBox's look from before 2.0 (its components, sidebar and holiday
	// themes), with the same color themes.
	ClassicLook bool `json:"classicLook"`
}

// ColorThemes and Holidays are the values UISettings takes (their colors are
// in web/src/lib/themes.ts).
var (
	ColorThemes = []string{"kumobox", "midnight", "forest", "sakura", "ember", "ocean", "dusk", "black"}
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
	videos := filepath.Join(home, videosFolder)
	s := Settings{
		SchemaVersion: schemaVersion,
		Library: LibrarySettings{
			Dir:              videos,
			ExtraDirs:        []string{},
			AutoRefresh:      true,
			RefreshOnStartup: true,
			MatchThreshold:   0.5,
			IgnorePatterns:   defaultIgnorePatterns(),
			MatchOutsideList: true,
		},
		Playback: PlaybackSettings{
			DefaultPlayer:       "builtin",
			AutoUpdateProgress:  true,
			CompletionThreshold: 0.85,
			ResumePlayback:      true,
			AutoPlayNext:        false,
			RememberTracks:      true,
			PreferredAudioLang:  "",
			PreferredSubLang:    "",
			SubtitlesForForeign: true,
		},
		Mpv: MpvSettings{Path: "mpv"},
		Transcode: TranscodeSettings{
			FfmpegPath:  "ffmpeg",
			FfprobePath: "ffprobe",
			Mode:        "auto",
			Preset:      "veryfast",
			VaapiNode:   "/dev/dri/renderD128",
		},
		Downloads:    DownloadSettings{Downloader: "auto"},
		OnlineStream: OnlineStreamSettings{Enabled: true},
		Torrent: TorrentSettings{
			DefaultClient:       "qbittorrent",
			DefaultProvider:     "all",
			ShowActiveCount:     true,
			CreateSubfolder:     true,
			PreferredResolution: "1080",
			AutoDownloadMinutes: 20,
		},
		Qbittorrent:  TorrentClientConfig{Host: "127.0.0.1", Port: 8081, Username: "admin", Executable: findQbittorrent(home)},
		Transmission: TorrentClientConfig{Host: "127.0.0.1", Port: 9091, Executable: defaultTransmission()},
		Server:       ServerSettings{Host: "127.0.0.1", Port: DefaultPort, WebUI: true},
		UI: UISettings{
			AccentColor:      "#7c6cf2",
			BannerType:       "episode",
			CardSize:         "md",
			ShowEpisodeTitle: true,
			HomeSections:     []string{"continue", "genres", "watching", "airing", "planning", "completed"},
			ColorTheme:       "kumobox",
			Seasonal:         "auto",
			SeasonalEffects:  true,
		},
		Discord:    DiscordSettings{RichPresence: false},
		Extensions: ExtensionSettings{MarketplaceURL: DefaultMarketplaceURL},

		TMDB:           defaultTMDB(),
		Letterboxd:     defaultLetterboxd(),
		Providers:      defaultProviders(),
		TorrentSources: defaultTorrentSources(),
		Sharing:        SharingSettings{KeepAwake: true, AutoSync: true},
		Notify:         NotifySettings{NewEpisodes: true, Downloads: true},
		Updates:        UpdateSettings{Channel: "stable"},
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
	pin      func(*Settings)
}

// Pin keeps settings the way fix makes them, now and at every save (in
// mediaserver, the movies side always shares with the network).
func (s *Store) Pin(fix func(*Settings)) error {
	s.mu.Lock()
	s.pin = fix
	cur := s.current.clone()
	s.mu.Unlock()
	_, err := s.Save(cur)
	return err
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
		migrated := migrate(&merged)
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
	c.Providers.StremioCatalogs = slices.Clone(c.Providers.StremioCatalogs)
	c.Providers.NuvioRepos = slices.Clone(c.Providers.NuvioRepos)
	c.Providers.MediaServers = slices.Clone(c.Providers.MediaServers)
	c.TorrentSources.Torznab = slices.Clone(c.TorrentSources.Torznab)
	return c
}

func (s *Store) Save(next Settings) (Settings, error) {
	next = sanitize(next.clone())
	s.mu.RLock()
	pin := s.pin
	s.mu.RUnlock()
	if pin != nil {
		pin(&next)
	}
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

// defaultIgnorePatterns are the names the library scanner skips at first:
// samples, unfinished downloads, and the folders NAS boxes (Synology's
// thumbnails and recycle bin) and Windows (a drive's recycle bin) keep
// copies of videos in. Trailers and other extras are kept: the release
// parser tells them from the movie.
func defaultIgnorePatterns() []string {
	return []string{"*sample*", "*.part", "@eaDir", "#recycle", "$RECYCLE.BIN"}
}

// schemaVersion is the current settings version; add a step to migrate when
// changing a default that existing installs should pick up too.
const schemaVersion = 2

// migrate updates settings saved by an older version, and reports whether
// it changed them.
func migrate(s *Settings) bool {
	if s.SchemaVersion >= schemaVersion {
		return false
	}
	if s.SchemaVersion < 2 {
		// Version 2 drops Kumo's anime ignore patterns ("*NCED*" also hid
		// "Silenced" and "Enhanced" editions) and adds the new defaults.
		var kept []string
		for _, p := range s.Library.IgnorePatterns {
			if !strings.EqualFold(p, "*NCOP*") && !strings.EqualFold(p, "*NCED*") {
				kept = append(kept, p)
			}
		}
		for _, p := range defaultIgnorePatterns() {
			if !slices.ContainsFunc(kept, func(k string) bool { return strings.EqualFold(k, p) }) {
				kept = append(kept, p)
			}
		}
		s.Library.IgnorePatterns = kept
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
	switch s.Downloads.Downloader {
	case "auto", "yt-dlp", "ffmpeg":
	default:
		s.Downloads.Downloader = "auto"
	}
	if s.Extensions.MarketplaceURL == "" {
		s.Extensions.MarketplaceURL = DefaultMarketplaceURL
	}
	if s.Torrent.AutoDownloadMinutes < 5 {
		s.Torrent.AutoDownloadMinutes = 20
	}
	// Kumo's anime sources are gone.
	switch s.Torrent.DefaultProvider {
	case "", "nyaa", "animetosho":
		s.Torrent.DefaultProvider = d.Torrent.DefaultProvider
	}
	if s.UI.AccentColor == "" {
		s.UI.AccentColor = d.UI.AccentColor
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
	sanitizeKumoBox(&s)
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

// DataDir is ~/.local/share/kumobox on Linux, ~/Library/Application
// Support/KumoBox on macOS and %APPDATA%\KumoBox on Windows, or $KUMOBOX_DATA_DIR.
func DataDir() string {
	if d := Getenv("DATA_DIR"); d != "" {
		return d
	}
	return defaultDataDir()
}

// RuntimeDir holds the server's address, the desktop window's token and
// other temporary files: $XDG_RUNTIME_DIR/kumobox on Linux, $TMPDIR/kumobox on
// macOS, %LOCALAPPDATA%\KumoBox\run on Windows.
func RuntimeDir() string {
	dir := runtimeDir()
	_ = os.MkdirAll(dir, 0o700)
	return dir
}
