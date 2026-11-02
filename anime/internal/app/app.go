// Package app wires every service together.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/v0-0x/mediaserver/anime/internal/awake"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anicli"
	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/discord"
	"github.com/v0-0x/mediaserver/anime/internal/downloads"
	"github.com/v0-0x/mediaserver/anime/internal/events"
	"github.com/v0-0x/mediaserver/anime/internal/extensions"
	"github.com/v0-0x/mediaserver/anime/internal/history"
	"github.com/v0-0x/mediaserver/anime/internal/images"
	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/lifecycle"
	"github.com/v0-0x/mediaserver/anime/internal/manga"
	"github.com/v0-0x/mediaserver/anime/internal/metadata"
	"github.com/v0-0x/mediaserver/anime/internal/notify"
	"github.com/v0-0x/mediaserver/anime/internal/player"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"github.com/v0-0x/mediaserver/anime/internal/stream"
	"github.com/v0-0x/mediaserver/anime/internal/torrent"
	"github.com/v0-0x/mediaserver/anime/internal/update"
)

type App struct {
	DB         *db.DB
	Settings   *config.Store
	Hub        *events.Hub
	Platform   *anilist.Platform
	Meta       *metadata.Service
	Files      *library.Store
	Scanner    *library.Scanner
	Library    *library.Service
	History    *history.Store
	Player     *player.Manager
	AniCli     *anicli.Driver
	Downloads  *downloads.Manager
	Torrents   *torrent.Manager
	AutoDL     *torrent.AutoDownloader
	Extensions *extensions.Manager
	Stream     *stream.Service
	Local      *stream.Local
	HLS        *stream.HLS
	Share      *share.Service
	Manga      *manga.Service
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
	// MangaLib is the downloaded manga (MangaDir), MangaDL their downloads.
	MangaLib *manga.Library
	MangaDL  *manga.Downloader
	// Exits ends Aniko from inside, e.g. to restart it after an update;
	// cmd/aniko waits for it, next to the signals.
	Exits *lifecycle.Exits

	// ShellToken authenticates the desktop window (see api middleware).
	ShellToken string
	DataDir    string

	// HubStore is set when Aniko runs as an Aniko Hub (aniko --hub, package
	// anikohub): it plays nothing, shares its library with the Aniko apps on
	// the network and keeps them in step; the heavy parts don't run.
	HubStore *anikohub.Store
	Streams  *anikohub.Streams
	Sampler  *anikohub.Sampler

	prefetch *prefetcher

	ctx    context.Context
	cancel context.CancelFunc
	// bg: the background jobs Start runs, which Shutdown waits for (they
	// stop with ctx) before it closes the database.
	bg sync.WaitGroup
}

// LogBuffer keeps the last server log lines for the Logs page (and, on a
// hub, all of them in File, for its audit log).
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	out   *os.File
	File  *LogFile
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
	if l.File != nil {
		_, _ = l.File.Write(p)
	}
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

// Options say how Aniko runs.
type Options struct {
	// Hub: as an Aniko Hub (see App.HubStore).
	Hub bool
}

func New(dataDir string, opts ...Options) (*App, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}
	logs := &LogBuffer{out: os.Stderr}
	if opt.Hub {
		if f, err := OpenLogFile(filepath.Join(dataDir, "logs")); err == nil {
			logs.File = f
		} else {
			fmt.Fprintf(os.Stderr, "the log won't be kept on disk: %v\n", err)
		}
	}
	log.SetOutput(logs)
	log.SetFlags(log.LstdFlags)

	d, err := db.Open(filepath.Join(dataDir, "aniko.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	settings, err := config.NewStore(d)
	if err != nil {
		return nil, err
	}
	hub := events.NewHub()
	platform := anilist.NewPlatform(d, hub)
	meta := metadata.NewService(d)
	files := library.NewStore(d)
	scanner := library.NewScanner(files, platform, settings, hub)
	hist := history.NewStore(d)
	lib := &library.Service{Store: files, Scanner: scanner, Platform: platform, Meta: meta, History: hist, DB: d}
	pl := player.NewManager(settings, hist, d, platform, hub)
	ani := anicli.New(settings)
	dls := downloads.NewManager(settings, d, hub, files)
	torrents := torrent.NewManager(settings, hub)
	exts := extensions.NewManager(d, settings, hub)
	ctx, cancel := context.WithCancel(context.Background())
	exits := lifecycle.NewExits()

	a := &App{
		DB: d, Settings: settings, Hub: hub, Platform: platform, Meta: meta, Files: files, Scanner: scanner,
		Library: lib, History: hist, Player: pl, AniCli: ani, Downloads: dls, Torrents: torrents,
		AutoDL:     torrent.NewAutoDownloader(d, torrents, platform, files, hub),
		Extensions: exts, Stream: stream.NewService(d, exts, ani, platform), Local: stream.NewLocal(settings, files, d),
		Manga: manga.NewService(d, exts, platform), Discord: &discord.Client{}, Logs: logs,
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
	if opt.Hub {
		if a.HubStore, err = anikohub.New(d); err != nil {
			return nil, fmt.Errorf("hub: %w", err)
		}
		a.Streams = anikohub.NewStreams()
		// It updates from its own releases (hub-v…).
		a.Update.GOOS = update.HubSystem
		if err := a.hubSettings(); err != nil {
			return nil, err
		}
	}
	// Libraries other Aniko apps share with this one show with its own.
	lib.Remote = a.Share.RemoteFiles
	a.ShellToken = randomToken()
	a.wireManga()
	a.wire()
	return a, nil
}

// hlsDir holds the in-app player's HLS segments: on disk, in the cache
// folder, not in /tmp, which is in memory on most Linux systems.
func hlsDir(dataDir string) string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "aniko", "hls")
	}
	return filepath.Join(dataDir, "hls")
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteShellToken stores the desktop-window token where only this user can
// read it ($XDG_RUNTIME_DIR/aniko/shell-token).
func (a *App) WriteShellToken() string {
	p := filepath.Join(config.RuntimeDir(), "shell-token")
	_ = os.WriteFile(p, []byte(a.ShellToken), 0o600)
	return p
}

func (a *App) wire() {
	// Anime with files get all their data, also those shared with this
	// Aniko (prefetch.go).
	a.Scanner.OnScanned = a.prefetchLibrary
	a.Scanner.OnNewFiles = a.Notify.NewFiles
	a.Downloads.Done = func(it downloads.Item) bool {
		return a.Notify.Downloaded(it.MediaID, it.Episode, it.AnimeTitle, it.Output)
	}
	a.Share.OnFiles = a.prefetchLibrary
	// Anikos on the same AniList account keep their watch histories in step,
	// as positions are saved. A hub keeps every account's instead (hub.go).
	if a.HubStore != nil {
		a.wireHub()
	} else {
		a.Share.History = a.History
		a.History.OnChange = a.Share.HistoryChanged
		a.wirePrefs()
	}
	a.Share.OnWatchedElsewhere = func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		if _, err := a.Platform.Collection(ctx, "ANIME", true); err == nil {
			a.Hub.Publish("collection-updated", nil)
		}
	}
	// The auto downloader sends what it finds where this Aniko's downloads
	// go (a TV's computer), like a torrent added by hand.
	a.AutoDL.Send = func(ctx context.Context, mediaID int, uris []string) (bool, error) {
		host, err := a.Share.SendDownload(ctx, share.DownloadTorrent, share.TorrentDownload{MediaID: mediaID, URIs: uris})
		return host != "" || err != nil, err
	}
	a.Share.User = func() int {
		// A hub is on each device's account, not one of its own.
		if a.HubStore != nil {
			return 0
		}
		if v := a.Platform.Viewer(); v != nil {
			return v.ID
		}
		return 0
	}
	// ani-cli for the Anikos this one shares with; and where it runs for this
	// one when it isn't here (Android).
	a.Share.AniCliReady = a.AniCli.LocalReady
	a.AniCli.SetRemote(a.Share.AniCliRemote())
	// The AniList login a host shares with this Aniko, or this one shares.
	a.Share.AccountToken = func() string {
		if !a.Platform.LoggedIn() {
			return ""
		}
		return a.Platform.Client().Token()
	}
	a.Share.UseAccount = func(ctx context.Context, token string) error {
		if _, err := a.Platform.Login(ctx, token); err != nil {
			return err
		}
		a.AccountChanged()
		return nil
	}
	a.Share.DropAccount = func() {
		a.Platform.Logout()
		a.AccountChanged()
	}

	// Torrent search can use extension providers.
	a.Torrents.ExtraProviders = a.Extensions.TorrentProviders

	// Downloads resolved through ani-cli or an online streaming extension.
	a.Downloads.Resolvers["anicli"] = func(ctx context.Context, it *downloads.Item) (*downloads.Resolved, error) {
		media, err := a.Platform.MediaLite(ctx, it.MediaID)
		if err != nil {
			return nil, err
		}
		res, err := a.Stream.Sources(ctx, stream.AniCliProvider, media, float64(it.Episode), it.Mode == "dub", "", it.Quality)
		if err != nil {
			return nil, err
		}
		src := res.Sources[0]
		r := &downloads.Resolved{URL: src.URL, Referrer: src.Referrer, Headers: src.Headers}
		if len(src.Subtitles) > 0 {
			r.SubURL = src.Subtitles[0].URL
		}
		return r, nil
	}
	a.Downloads.Resolvers["stream"] = func(ctx context.Context, it *downloads.Item) (*downloads.Resolved, error) {
		media, err := a.Platform.MediaLite(ctx, it.MediaID)
		if err != nil {
			return nil, err
		}
		res, err := a.Stream.Sources(ctx, it.Provider, media, float64(it.Episode), it.Mode == "dub", "", it.Quality)
		if err != nil {
			return nil, err
		}
		src := pickSource(res.Sources, it.Quality)
		r := &downloads.Resolved{URL: src.URL, Headers: src.Headers}
		for _, s := range src.Subtitles {
			if s.IsDefault || strings.Contains(strings.ToLower(s.Language), "eng") {
				r.SubURL = s.URL
				break
			}
		}
		return r, nil
	}

	// Auto play next local episode.
	a.Player.NextResolver = func(ctx context.Context, s *player.Session) (*player.PlayRequest, error) {
		if s.Source != "local" {
			return nil, nil
		}
		files, err := a.Files.ByMedia(s.MediaID)
		if err != nil {
			return nil, err
		}
		f := library.EpisodeFile(files, s.Episode+1)
		if f == nil {
			return nil, nil
		}
		media, _ := a.Platform.MediaLite(ctx, s.MediaID)
		title := f.Name
		if media != nil {
			title = fmt.Sprintf("%s — Episode %d", media.PreferredTitle(), f.Episode)
		}
		return &player.PlayRequest{MediaID: s.MediaID, Episode: f.Episode, Title: title, Source: "local", Target: f.Path}, nil
	}

	// Plugins hooks after progress updates.
	a.Player.OnProgress = append(a.Player.OnProgress, func(mediaID, episode int) {
		a.Extensions.FireHook("onPostUpdateEntryProgress", map[string]any{"mediaId": mediaID, "progress": episode})
	})
	// And after playback put an anime on the watching list (a status change).
	a.Player.OnWatching = append(a.Player.OnWatching, func(mediaID int) {
		a.Extensions.FireHook("onPostUpdateEntry", map[string]any{"mediaId": mediaID})
	})

	// Discord rich presence.
	var (
		presenceMu   sync.Mutex
		lastPresence time.Time
		showing      bool // a presence is set and must be cleared eventually
	)
	clearPresence := func() {
		presenceMu.Lock()
		was := showing
		showing, lastPresence = false, time.Time{}
		presenceMu.Unlock()
		if was {
			a.Discord.Clear()
		}
	}
	a.Player.OnStatus = append(a.Player.OnStatus, func(s *player.Session) {
		cfg := a.Settings.Get()
		if !cfg.Discord.RichPresence || cfg.Discord.ClientID == "" || !s.Active {
			clearPresence()
			return
		}
		presenceMu.Lock()
		if time.Since(lastPresence) < 15*time.Second {
			presenceMu.Unlock()
			return
		}
		lastPresence, showing = time.Now(), true
		presenceMu.Unlock()
		media, err := a.Platform.MediaLite(context.Background(), s.MediaID)
		act := discord.Activity{Details: s.Title, State: fmt.Sprintf("Episode %d", s.Episode)}
		if err == nil {
			act.Details = media.PreferredTitle()
			act.LargeImage = media.CoverImage.Large
			act.LargeText = media.PreferredTitle()
		}
		if s.Duration > 0 && !s.Paused {
			act.Start = time.Now().Add(-time.Duration(s.Position) * time.Second)
			act.End = act.Start.Add(time.Duration(s.Duration) * time.Second)
		}
		_ = a.Discord.SetActivity(cfg.Discord.ClientID, act)
	})

	a.Extensions.Host = a.pluginServices()

	anilist.SetTitleLanguage(a.Settings.Get().UI.TitleLanguage)
	// Stable releases, or betas too (Settings › Updates & about).
	a.Update.Channel = func() string { return a.Settings.Get().Updates.Channel }
	a.Settings.OnChange(func(old, cur config.Settings) {
		// Listeners run apart: two saves close together may arrive in either
		// order, so what's set follows the settings now.
		anilist.SetTitleLanguage(a.Settings.Get().UI.TitleLanguage)
		if !cur.Discord.RichPresence || cur.Discord.ClientID != old.Discord.ClientID {
			clearPresence() // turned off: don't leave "Watching…" behind
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

func pickSource(srcs []stream.Source, quality string) stream.Source {
	if quality != "" && quality != "best" {
		for _, s := range srcs {
			if strings.Contains(s.Quality, strings.TrimSuffix(quality, "p")) {
				return s
			}
		}
	}
	return srcs[0]
}

// AccountChanged tells the app the AniList login changed (another Aniko's
// account shared with this one): the list comes from the new one.
func (a *App) AccountChanged() {
	a.Hub.Publish(events.SettingsUpdated, nil)
	a.Hub.Publish("collection-updated", nil)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := a.Platform.Collection(ctx, "ANIME", true); err == nil {
			a.Hub.Publish("collection-updated", nil)
		}
	}()
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
	if a.HubStore != nil {
		a.startHub()
		return
	}
	a.Share.Start()
	a.background(func() { a.runPrefetch(a.ctx) })
	a.Extensions.LoadAll()
	a.Downloads.Start()
	a.Update.Start()
	a.Scanner.StartWatcher()
	a.background(func() { a.Torrents.RunCounter(a.ctx) })
	a.background(func() { a.Notify.Run(a.ctx) })
	a.background(func() { a.MangaDL.Run(a.ctx) })
	a.background(a.purgeTrash)
	a.background(func() {
		a.AutoDL.Loop(a.ctx, func() (bool, time.Duration) {
			cfg := a.Settings.Get()
			return cfg.Torrent.AutoDownloader, time.Duration(cfg.Torrent.AutoDownloadMinutes) * time.Minute
		})
	})
	a.background(func() {
		// The Discover page, ready before it's opened.
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		if d, err := a.Platform.WarmDiscover(ctx); err == nil {
			a.PrefetchDiscoverArt(d)
		}
	})
	// The library off: nothing kept that isn't there (Continue watching
	// would offer it).
	if a.Settings.Get().Library.Off {
		a.background(a.Scanner.ForgetGone)
	}
	a.background(func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		a.Platform.RefreshViewer(ctx)
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
	if a.HubStore != nil {
		a.HubStore.Log(anikohub.Event{Kind: "stop", By: "hub", Text: "stopped"})
	}
	a.cancel()
	// The player first: it saves where it stopped, which sharing then sends
	// to the other Anikos before it stops.
	a.Player.Stop()
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
	_ = a.DB.Close()
	if a.Logs.File != nil {
		_ = a.Logs.File.Close()
	}
}

func (a *App) Context() context.Context { return a.ctx }

// PrefetchCollectionArt downloads covers and banners of everything in the
// list and library so they are available offline.
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
			urls = append(urls, it.Media.CoverImage.ExtraLarge, it.Media.CoverImage.Large, it.Media.BannerImage)
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
func (a *App) PrefetchDiscoverArt(d *anilist.Discover) {
	var urls []string
	banners := 0
	for _, m := range d.Trending {
		if m.BannerImage != "" && banners < 6 {
			urls = append(urls, m.BannerImage)
			banners++
		}
	}
	for _, row := range [][]*anilist.Media{d.Trending, d.ThisSeason, d.NextSeason, d.Popular, d.TopRated} {
		for _, m := range row[:min(len(row), 8)] {
			urls = append(urls, m.CoverImage.ExtraLarge)
		}
	}
	a.Images.Prefetch(urls...)
}

// PrefetchEntryArt downloads an anime's artwork and episode thumbnails.
func (a *App) PrefetchEntryArt(e *library.EntryView) {
	if e == nil || e.Media == nil {
		return
	}
	urls := []string{e.Media.CoverImage.ExtraLarge, e.Media.CoverImage.Large, e.Media.BannerImage, e.Images.Fanart, e.Images.Banner, e.Images.Poster, e.Images.Clearlogo}
	for _, ep := range e.Episodes {
		urls = append(urls, ep.Image)
	}
	a.Images.Prefetch(urls...)
}
