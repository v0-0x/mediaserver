// Package app wires every service together.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/v0-0x/mediaserver/movies/internal/awake"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/discord"
	"github.com/v0-0x/mediaserver/movies/internal/downloads"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
	"github.com/v0-0x/mediaserver/movies/internal/history"
	"github.com/v0-0x/mediaserver/movies/internal/images"
	"github.com/v0-0x/mediaserver/movies/internal/letterboxd"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/lifecycle"
	"github.com/v0-0x/mediaserver/movies/internal/notify"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/providers/stremio"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/torrent"
	"github.com/v0-0x/mediaserver/movies/internal/update"
)

type App struct {
	DB         *db.DB
	Settings   *config.Store
	Hub        *events.Hub
	Platform   *catalog.Platform
	Letterboxd *letterboxd.Sync
	Files      *library.Store
	Scanner    *library.Scanner
	Library    *library.Service
	History    *history.Store
	Player     *player.Manager
	Downloads  *downloads.Manager
	Torrents   *torrent.Manager
	AutoDL     *torrent.AutoDownloader
	Extensions *extensions.Manager
	Stream     *stream.Service
	Local      *stream.Local
	HLS        *stream.HLS
	Share      *share.Service
	Discord    *discord.Client
	Images     *images.Cache
	Logs       *LogBuffer
	Update     *update.Checker
	// Awake keeps the computer from sleeping while others play its files.
	Awake *awake.Keeper
	// Trash holds the files put away from Library tools › Clean up.
	Trash *library.Trash
	// Notify tells about aired episodes and episodes ready in the library.
	Notify *notify.Service
	// Exits ends KumoBox from inside, e.g. to restart it after an update;
	// cmd/kumobox waits for it, next to the signals.
	Exits *lifecycle.Exits

	// ShellToken authenticates the desktop window (see api middleware).
	ShellToken string
	DataDir    string

	prefetch *prefetcher
	diary    diaryLog
	presence presence

	// Stremio holds the installed Stremio addons, collections the addon
	// collections downloaded lately (see providers.go).
	Stremio     *stremio.Store
	collections *collectionCache
	// Health is how each provider's searches went (providerhealth.go),
	// versions the version of each show that played last
	// (streamversion.go).
	Health      *providers.Health
	healthStore *healthStore
	versions    *streamVersions

	ctx    context.Context
	cancel context.CancelFunc
	// bg: the background jobs Start runs, which Shutdown waits for (they
	// stop with ctx) before it closes the database.
	bg sync.WaitGroup
}

// LogBuffer keeps the last server log lines for the Logs page.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	out   *os.File
}

func (l *LogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		l.lines = append(l.lines, string(line))
	}
	if len(l.lines) > 1000 {
		l.lines = l.lines[len(l.lines)-1000:]
	}
	l.mu.Unlock()
	return l.out.Write(p)
}

// Keep records log lines without writing them out again: in mediaserver,
// both of its engines keep the one log, which the other already wrote out.
func (l *LogBuffer) Keep(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		l.lines = append(l.lines, string(line))
	}
	if len(l.lines) > 1000 {
		l.lines = l.lines[len(l.lines)-1000:]
	}
}

func (l *LogBuffer) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func New(dataDir string) (*App, error) {
	logs := &LogBuffer{out: os.Stderr}
	log.SetOutput(logs)
	log.SetFlags(log.LstdFlags)

	d, err := db.Open(filepath.Join(dataDir, "kumobox.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	settings, err := config.NewStore(d)
	if err != nil {
		return nil, err
	}
	hub := events.NewHub()
	platform := catalog.NewPlatform(d, hub, settings)
	files := library.NewStore(d)
	scanner := library.NewScanner(files, platform, settings, hub)
	hist := history.NewStore(d)
	lib := &library.Service{Store: files, Scanner: scanner, Platform: platform, History: hist, DB: d}
	pl := player.NewManager(settings, hist, d, platform, hub)
	dls := downloads.NewManager(settings, d, hub, files)
	dls.Lookup = platform.MediaLite // names files "Title (Year)"
	torrents := torrent.NewManager(settings, hub)
	exts := extensions.NewManager(d, settings, hub)
	ctx, cancel := context.WithCancel(context.Background())
	exits := lifecycle.NewExits()

	a := &App{
		DB: d, Settings: settings, Hub: hub, Platform: platform, Files: files, Scanner: scanner,
		Library: lib, History: hist, Player: pl, Downloads: dls, Torrents: torrents,
		AutoDL:     torrent.NewAutoDownloader(d, torrents, platform, files, hub),
		Extensions: exts, Stream: stream.NewService(streamOptions(settings)), Local: stream.NewLocal(settings, files, d),
		Discord: &discord.Client{}, Logs: logs,
		Images: images.New(filepath.Join(dataDir, "images")),
		Update: update.New(hub, exits), Exits: exits,
		ctx: ctx, cancel: cancel, DataDir: dataDir, prefetch: newPrefetcher(),
	}
	a.HLS = stream.NewHLS(a.Local, hlsDir(dataDir))
	a.Notify = notify.New(settings, hub, d, platform)
	a.Trash = library.NewTrash(d, files, func() []string { return a.Settings.Get().LibraryDirs() })
	a.Awake = awake.New(func() bool { return a.Settings.Get().Sharing.KeepAwake })
	if a.Share, err = share.New(d, settings, files, hub); err != nil {
		return nil, fmt.Errorf("library sharing: %w", err)
	}
	// Libraries other KumoBox apps share with this one show with its own.
	lib.Remote = a.Share.RemoteFiles
	a.ShellToken = randomToken()
	if err := a.wireProviders(); err != nil {
		return nil, fmt.Errorf("streaming providers: %w", err)
	}
	a.wire()
	return a, nil
}

// hlsDir holds the in-app player's HLS segments: on disk, in the cache
// folder, not in /tmp, which is in memory on most Linux systems.
func hlsDir(dataDir string) string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "kumobox", "hls")
	}
	return filepath.Join(dataDir, "hls")
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteShellToken stores the desktop-window token where only this user can
// read it ($XDG_RUNTIME_DIR/kumobox/shell-token).
func (a *App) WriteShellToken() string {
	p := filepath.Join(config.RuntimeDir(), "shell-token")
	_ = os.WriteFile(p, []byte(a.ShellToken), 0o600)
	return p
}

func (a *App) wire() {
	a.wireLetterboxd()
	// Titles with files get all their data, also those shared with this
	// KumoBox (prefetch.go).
	a.Scanner.OnScanned = a.prefetchLibrary
	a.Scanner.OnNewFiles = a.Notify.NewFiles
	a.Downloads.Done = func(it downloads.Item) bool {
		return a.Notify.Downloaded(it.MediaID, it.Episode, it.Title, it.Output)
	}
	a.Share.OnFiles = a.prefetchLibrary
	// KumoBoxes on the same Letterboxd account keep their watch histories
	// in step, as positions are saved, and so their lists: an episode
	// finished on another one moves this one's progress on too.
	a.Share.History = a.History
	a.History.OnChange = a.Share.HistoryChanged
	a.Share.OnWatchedElsewhere = a.progressFromHistory
	// The auto downloader sends what it finds where this KumoBox's downloads
	// go (a TV's computer), like a torrent added by hand.
	a.AutoDL.Send = func(ctx context.Context, mediaID int, uris []string) (bool, error) {
		host, err := a.Share.SendDownload(ctx, share.DownloadTorrent, share.TorrentDownload{MediaID: mediaID, URIs: uris})
		return host != "" || err != nil, err
	}
	a.Share.User = func() int {
		if v := a.Platform.Viewer(); v != nil {
			return v.ID
		}
		return 0
	}
	// Torrent search can use extension providers.
	a.Torrents.ExtraProviders = a.Extensions.TorrentProviders

	// Auto play next: the next episode's file, or stream (playback.go).
	a.Player.NextResolver, a.Player.HasNext = a.nextEpisode, a.hasNextEpisode

	// Plugins hooks after progress updates.
	a.Player.OnProgress = append(a.Player.OnProgress, func(mediaID, episode int) {
		a.Extensions.FireHook("onPostUpdateEntryProgress", map[string]any{"mediaId": mediaID, "progress": episode})
	})
	// And after playback put a title on the watching list (a status change).
	a.Player.OnWatching = append(a.Player.OnWatching, func(mediaID int) {
		a.Extensions.FireHook("onPostUpdateEntry", map[string]any{"mediaId": mediaID})
	})

	// Discord rich presence (playback.go).
	a.Player.OnStatus = append(a.Player.OnStatus, a.showPresence)
	// The version of a show that played, for its next episodes
	// (streamversion.go).
	a.Player.OnStatus = append(a.Player.OnStatus, a.rememberVersion)

	a.Extensions.Host = a.pluginServices()

	// Stable releases, or betas too (Settings › Updates & about).
	a.Update.Channel = func() string { return a.Settings.Get().Updates.Channel }
	a.Settings.OnChange(func(old, cur config.Settings) {
		if !cur.Discord.RichPresence || cur.Discord.ClientID != old.Discord.ClientID {
			a.clearPresence() // turned off: don't leave "Watching…" behind
		}
		if old.Library.AutoRefresh != cur.Library.AutoRefresh || fmt.Sprint(old.LibraryDirs()) != fmt.Sprint(cur.LibraryDirs()) {
			a.Scanner.StartWatcher()
		}
		// Folders out of the library (or the library off): their files go,
		// also when their drive isn't here.
		if now := a.Settings.Get().LibraryDirs(); fmt.Sprint(old.LibraryDirs()) != fmt.Sprint(now) {
			var out []string
			for _, d := range old.LibraryDirs() {
				if !slices.Contains(now, d) {
					out = append(out, d)
				}
			}
			a.Scanner.Forget(out)
		}
		// The library off: also what's no longer on the disk, wherever it was.
		if a.Settings.Get().Library.Off && !old.Library.Off {
			a.Scanner.ForgetGone()
		}
		// A new library folder: index it right away.
		if fmt.Sprint(old.LibraryDirs()) != fmt.Sprint(cur.LibraryDirs()) && a.Scanner.HasLibrary() {
			a.Scanner.ScanSoon()
		}
		// TMDB data in another language (or for another region): fetched
		// again. A key added: the Discover page is ready before it's opened.
		if old.TMDB.Language != cur.TMDB.Language || old.TMDB.Region != cur.TMDB.Region {
			a.Platform.ResetCache()
		}
		if old.TMDB.APIKey != cur.TMDB.APIKey && cur.TMDB.APIKey != "" {
			go a.warmDiscover()
		}
		// Another channel: what it offers, at once.
		if old.Updates.Channel != cur.Updates.Channel {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				a.Update.Check(ctx)
			}()
		}
		a.Hub.Publish(events.SettingsUpdated, nil)
	})
}

// purgeTrash empties what's been in the trash a month, now and daily.
func (a *App) purgeTrash() {
	for {
		if freed, err := a.Trash.Purge(time.Now()); err != nil {
			log.Printf("trash: %v", err)
		} else if freed > 0 {
			log.Printf("trash: emptied what was there for 30 days (%d MB)", freed>>20)
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// background runs a job of Start's, which Shutdown waits for.
func (a *App) background(job func()) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		job()
	}()
}

// Start runs the background jobs.
func (a *App) Start() {
	a.Share.Start()
	a.Letterboxd.Start(a.ctx)
	a.background(func() { a.runPrefetch(a.ctx) })
	a.Extensions.LoadAll()
	a.Downloads.Start()
	a.Update.Start()
	a.Scanner.StartWatcher()
	a.background(func() { a.Torrents.RunCounter(a.ctx) })
	a.background(func() { a.Notify.Run(a.ctx) })
	a.background(a.purgeTrash)
	a.background(func() {
		a.AutoDL.Loop(a.ctx, func() (bool, time.Duration) {
			cfg := a.Settings.Get()
			return cfg.Torrent.AutoDownloader, time.Duration(cfg.Torrent.AutoDownloadMinutes) * time.Minute
		})
	})
	a.background(a.warmDiscover)
	// The library off: nothing kept that isn't there (Continue watching
	// would offer it).
	if a.Settings.Get().Library.Off {
		a.background(a.Scanner.ForgetGone)
	}
	a.background(func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		if view, err := a.Library.Collection(ctx, false); err == nil {
			a.PrefetchCollectionArt(view)
		}
		if a.Settings.Get().Library.RefreshOnStartup && a.Scanner.HasLibrary() {
			if _, err := a.Scanner.Scan(a.ctx, library.ScanOptions{}); err != nil && !errors.Is(err, library.ErrScanRunning) {
				log.Printf("startup scan: %v", err)
			}
		}
		// What earlier runs didn't get to (a scan prefetches on its own).
		a.prefetchLibrary()
	})
}

func (a *App) Shutdown() {
	a.cancel()
	// The player first: it saves where it stopped, which sharing then sends
	// to the other KumoBoxes before it stops.
	a.Player.Stop()
	a.Images.Close()
	a.Share.Stop()
	a.Awake.Close()
	a.Update.Stop()
	a.HLS.Close()
	a.Scanner.StopWatcher()
	a.Discord.Clear()
	a.Extensions.Shutdown()
	// The background jobs end with ctx; what they write (the database, the
	// artwork in the data folder) is done before it closes.
	done := make(chan struct{})
	go func() { a.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("shutdown: background jobs still running after 10s")
	}
	a.healthStore.flush()
	_ = a.DB.Close()
}

func (a *App) Context() context.Context { return a.ctx }

// warmDiscover gets the Discover page ready before it's opened (when a TMDB
// key is set).
func (a *App) warmDiscover() {
	if !a.Platform.Configured() {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	if d, err := a.Platform.WarmDiscover(ctx); err == nil {
		a.PrefetchDiscoverArt(d)
	}
}

// progressFromHistory moves the list on for the episodes another KumoBox
// on the same account says were finished there (their positions came with
// its watch history): each KumoBox keeps its own list.
func (a *App) progressFromHistory(finished []history.Entry) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	// Oldest first: the list moves on in the order they were watched.
	sort.SliceStable(finished, func(i, j int) bool { return finished[i].UpdatedAt < finished[j].UpdatedAt })
	for _, h := range finished {
		if h.MediaID > 0 && h.Episode > 0 {
			a.progressElsewhere(ctx, h.MediaID, h.Episode)
		}
	}
}

// PrefetchCollectionArt downloads covers and banners of everything in the
// list and library so they are available offline. Cards show the medium
// or large cover (the size the screen needs), title pages up to the extra
// large one.
func (a *App) PrefetchCollectionArt(view *library.CollectionView) {
	if view == nil {
		return
	}
	var urls []string
	add := func(items []*library.CollectionItem) {
		for _, it := range items {
			if it.Media == nil {
				continue
			}
			c := it.Media.CoverImage
			urls = append(urls, c.Medium, c.Large, c.ExtraLarge, it.Media.BannerImage)
		}
	}
	for _, l := range view.Lists {
		add(l.Items)
	}
	add(view.LocalOnly)
	for _, c := range view.ContinueWatching {
		urls = append(urls, c.Image, c.Media.BannerImage) // the banner is the Home page header
	}
	a.Images.Prefetch(urls...)
}

// PrefetchDiscoverArt downloads what the Discover page shows first: the
// banners of its header and the first covers of each row. The rest load as
// they're scrolled to.
func (a *App) PrefetchDiscoverArt(d *catalog.Discover) {
	var urls []string
	banners := 0
	for _, m := range d.Trending {
		if m.BannerImage != "" && banners < 6 {
			urls = append(urls, m.BannerImage)
			banners++
		}
	}
	for _, row := range [][]*catalog.Media{d.Trending, d.PopularMovies, d.PopularShows, d.NowPlaying, d.OnTheAir, d.TopRatedMovies, d.TopRatedShows, d.Upcoming} {
		for _, m := range row[:min(len(row), 8)] {
			// The sizes cards load (the screen's density picks one).
			urls = append(urls, m.CoverImage.Medium, m.CoverImage.Large)
		}
	}
	a.Images.Prefetch(urls...)
}

// PrefetchEntryArt downloads a title's artwork and episode stills.
func (a *App) PrefetchEntryArt(e *library.EntryView) {
	if e == nil || e.Media == nil {
		return
	}
	c := e.Media.CoverImage
	urls := []string{c.Medium, c.Large, c.ExtraLarge, e.Media.BannerImage, e.Images.Fanart, e.Images.Banner, e.Images.Poster, e.Images.Clearlogo}
	for _, ep := range e.Episodes {
		urls = append(urls, ep.Image)
	}
	a.Images.Prefetch(urls...)
}
