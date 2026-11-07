package api

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"github.com/v0-0x/mediaserver/anime/internal/stream"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

func (s *Server) routes() {
	m := s.mux
	s.peerRoutes()
	s.torrentStreamRoutes()
	s.uploadRoutes()
	s.hubRoutes()
	s.sideRoutes()
	s.parentalRoutes()
	s.noteRoutes()
	s.cleanupRoutes()
	s.backupRoutes()

	// --- core
	m.HandleFunc("GET /api/status", h(s.status))
	m.HandleFunc("GET /api/settings", h(func(r *http.Request) (any, error) { return settingsFor(r, s.app.Settings.Get()), nil }))
	// The Stats page: what was watched in Aniko, month by month.
	m.HandleFunc("GET /api/stats/monthly", h(func(r *http.Request) (any, error) {
		n := queryInt(r, "months")
		if n <= 0 {
			n = 12
		}
		n = min(n, 36)
		return s.app.History.Monthly(n, s.app.Settings.Get().Playback.CompletionThreshold, time.Now(), time.Local)
	}))
	m.HandleFunc("PUT /api/settings", h(s.saveSettings))
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("POST /api/auth/anilist", h(s.anilistLogin))
	m.HandleFunc("POST /api/auth/logout", h(func(r *http.Request) (any, error) {
		// The account is this computer's: a phone on the network logging
		// out would log it out too.
		if !isTrusted(r) {
			return nil, forbidden("log out on the computer Aniko runs on: from here, it would log that computer out")
		}
		s.app.Platform.Logout()
		s.app.Share.LoggedOut()
		return nil, nil
	}))
	m.HandleFunc("POST /api/auth/server-login", s.serverLogin)
	m.HandleFunc("GET /api/fs/dirs", h(s.listDirs))
	m.HandleFunc("POST /api/open", h(s.openPath))
	m.HandleFunc("POST /api/setup/programs", h(s.installPrograms))
	m.HandleFunc("GET /api/logs", h(func(r *http.Request) (any, error) { return s.app.Logs.Lines(), nil }))
	m.HandleFunc("POST /api/cache/clear", h(func(r *http.Request) (any, error) { return nil, s.app.DB.ClearCache() }))
	m.HandleFunc("GET /api/cache/size", h(func(r *http.Request) (any, error) {
		n, err := s.app.DB.CacheSize()
		return map[string]int{"entries": n}, err
	}))

	// --- anime / anilist
	m.HandleFunc("GET /api/anime/collection", h(s.animeCollection))
	m.HandleFunc("POST /api/continue/hide", h(s.hideContinue))
	m.HandleFunc("POST /api/continue/unhide", h(s.unhideContinue))
	m.HandleFunc("GET /api/anime/{id}", h(s.animeEntry))
	m.HandleFunc("POST /api/anime/{id}/entry", h(s.updateEntry))
	m.HandleFunc("POST /api/anime/{id}/episode", h(s.markEpisode))
	m.HandleFunc("GET /api/anime/{id}/language", h(s.getLanguage))
	m.HandleFunc("PUT /api/anime/{id}/language", h(s.putLanguage))
	m.HandleFunc("DELETE /api/anime/{id}/entry", h(s.deleteEntry))
	// Watch order (experimental): a franchise's story and release order.
	m.HandleFunc("GET /api/anime/{id}/watch-order", h(s.watchOrder))
	m.HandleFunc("PUT /api/watch-order/{key}", h(s.putWatchOrder))
	m.HandleFunc("DELETE /api/watch-order/{key}", h(s.deleteWatchOrder))
	m.HandleFunc("POST /api/watch-order/{key}/report", h(s.reportWatchOrder))
	m.HandleFunc("POST /api/anilist/search", h(s.search))
	m.HandleFunc("GET /api/anilist/discover", h(s.discover))
	m.HandleFunc("GET /api/anilist/schedule", h(s.schedule))
	m.HandleFunc("GET /api/anilist/list", h(s.rawList))
	// The profile page: a user's profile, feeds, likes and follows.
	m.HandleFunc("GET /api/anilist/profile", h(func(r *http.Request) (any, error) {
		return s.app.Platform.Profile(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/anilist/activity", h(func(r *http.Request) (any, error) {
		following := r.URL.Query().Get("feed") == "following"
		return s.app.Platform.Activity(r.Context(), following, queryInt(r, "user"), queryInt(r, "page"), s.app.Settings.Get().UI.ShowAdult)
	}))
	m.HandleFunc("POST /api/anilist/activity/{id}/like", h(func(r *http.Request) (any, error) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			return nil, badRequest("bad activity id")
		}
		count, liked, err := s.app.Platform.ToggleLike(r.Context(), id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"likeCount": count, "isLiked": liked}, nil
	}))
	m.HandleFunc("POST /api/anilist/follow/{id}", h(func(r *http.Request) (any, error) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			return nil, badRequest("bad user id")
		}
		following, err := s.app.Platform.ToggleFollow(r.Context(), id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"isFollowing": following}, nil
	}))

	// --- library
	m.HandleFunc("POST /api/library/scan", h(s.scan))
	m.HandleFunc("GET /api/library/unmatched", h(s.unmatched))
	m.HandleFunc("GET /api/library/ignored", h(func(r *http.Request) (any, error) { return s.app.Files.Ignored() }))
	m.HandleFunc("GET /api/library/files", h(func(r *http.Request) (any, error) { return s.app.Files.All() }))
	m.HandleFunc("POST /api/library/match", h(libraryEdited(s.match)))
	m.HandleFunc("GET /api/library/folders", h(func(r *http.Request) (any, error) { return s.app.Scanner.Folders() }))
	m.HandleFunc("POST /api/library/index", h(s.indexFolders))
	m.HandleFunc("POST /api/library/unmatch", h(libraryEdited(s.unmatch)))
	m.HandleFunc("POST /api/library/ignore", h(libraryEdited(s.ignore)))
	m.HandleFunc("PATCH /api/library/file", h(libraryEdited(s.patchFile)))
	m.HandleFunc("POST /api/library/open-folder", h(s.openFolder))

	// --- playback
	m.HandleFunc("POST /api/playback/local", h(s.playLocal))
	m.HandleFunc("POST /api/playback/stream", h(s.playStream))
	m.HandleFunc("GET /api/playback/status", h(func(r *http.Request) (any, error) { return s.app.Player.Status(), nil }))
	m.HandleFunc("POST /api/playback/command", h(s.playbackCommand))
	m.HandleFunc("POST /api/playback/progress", h(s.playbackProgress))
	m.HandleFunc("GET /api/playback/tracks/{mediaId}", h(s.getTracks))
	m.HandleFunc("PUT /api/playback/tracks/{mediaId}", h(s.saveTracks))
	m.HandleFunc("GET /api/playback/skips", h(s.skips))
	m.HandleFunc("GET /api/history/recent", h(func(r *http.Request) (any, error) { return s.app.History.Recent(50), nil }))
	m.HandleFunc("DELETE /api/history", h(s.clearHistory))

	// --- in-app player helpers
	// A shared file's requests go to its host (see sharing.go).
	m.HandleFunc("GET /api/local/probe", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "probe") {
			h(func(r *http.Request) (any, error) { return s.app.Local.Probe(r.Context(), r.URL.Query().Get("path")) })(w, r)
		}
	})
	m.HandleFunc("GET /api/local/file", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "file") {
			s.localFile(w, r)
		}
	})
	m.HandleFunc("GET /api/local/transcode", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "transcode") {
			s.localTranscode(w, r)
		}
	})
	m.HandleFunc("GET /api/local/seekpoint", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "seekpoint") {
			h(s.localSeekPoint)(w, r)
		}
	})
	// HLS, for Safari and iPhone/iPad (see stream.HLS).
	m.HandleFunc("POST /api/local/hls", h(func(r *http.Request) (any, error) {
		var req stream.HLSRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if share.IsRemote(req.Path) {
			return s.app.Share.StartHLS(r.Context(), req)
		}
		return s.app.HLS.Start(r.Context(), req)
	}))
	m.HandleFunc("GET /api/local/hls/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if id := r.PathValue("id"); share.IsRemoteSession(id) {
			s.app.Share.ForwardHLS(w, r, id, r.PathValue("name"))
			return
		}
		defer s.awakeFor(r)()
		s.app.HLS.Serve(w, r, r.PathValue("id"), r.PathValue("name"))
	})
	m.HandleFunc("DELETE /api/local/hls/{id}", h(func(r *http.Request) (any, error) {
		if id := r.PathValue("id"); share.IsRemoteSession(id) {
			s.app.Share.StopHLS(r.Context(), id)
			return nil, nil
		}
		s.app.HLS.Stop(r.PathValue("id"))
		return nil, nil
	}))
	m.HandleFunc("GET /api/local/subtitle", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "subtitle") {
			s.localSubtitle(w, r)
		}
	})
	// The seek bar's preview at ?t= (stream.ServeThumb).
	m.HandleFunc("GET /api/local/thumb", func(w http.ResponseWriter, r *http.Request) {
		if !s.forwardShared(w, r, "thumb") {
			s.localThumb(w, r)
		}
	})
	m.HandleFunc("GET /api/proxy", stream.ServeProxy)
	m.Handle("GET /api/img", s.app.Images)
	m.HandleFunc("GET /api/images/stats", h(func(r *http.Request) (any, error) { return s.app.Images.Stats(), nil }))
	m.HandleFunc("POST /api/images/clear", h(func(r *http.Request) (any, error) { return nil, s.app.Images.Clear() }))

	// --- online streaming & ani-cli
	m.HandleFunc("GET /api/onlinestream/providers", h(func(r *http.Request) (any, error) { return s.app.Stream.Providers(r.Context()), nil }))
	m.HandleFunc("GET /api/onlinestream/episodes", h(s.osEpisodes))
	m.HandleFunc("GET /api/onlinestream/modes", h(s.osModes))
	m.HandleFunc("GET /api/onlinestream/sources", h(s.osSources))
	m.HandleFunc("GET /api/onlinestream/search", h(s.osSearch))
	m.HandleFunc("POST /api/onlinestream/mapping", h(s.osMapping))
	m.HandleFunc("POST /api/onlinestream/play", h(s.osPlay))
	m.HandleFunc("POST /api/onlinestream/download", h(s.osDownload))
	m.HandleFunc("GET /api/anicli/status", h(func(r *http.Request) (any, error) { return s.app.AniCli.Status(r.Context()), nil }))

	// --- downloads
	m.HandleFunc("GET /api/downloads", h(func(r *http.Request) (any, error) { return s.app.Downloads.List(), nil }))
	// The downloads of the Aniko this one's go to (a TV's computer): its list,
	// and cancel or retry there.
	m.HandleFunc("GET /api/downloads/host", h(func(r *http.Request) (any, error) {
		raw, host, ok, err := s.app.Share.DownloadsHost(r.Context(), "list", nil)
		out := map[string]any{"host": host, "items": raw}
		if !ok {
			out["items"] = nil
		} else if err != nil {
			out["items"], out["error"] = nil, err.Error()
		}
		return out, nil
	}))
	m.HandleFunc("POST /api/downloads/host/{id}/{op}", h(func(r *http.Request) (any, error) {
		op := r.PathValue("op")
		if op != "cancel" && op != "retry" {
			return nil, notFound("no such thing to do")
		}
		raw, _, ok, err := s.app.Share.DownloadsHost(r.Context(), url.PathEscape(r.PathValue("id"))+"/"+op, map[string]any{})
		if !ok {
			return nil, badRequest("downloads are saved on this device")
		}
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return raw, nil
	}))
	m.HandleFunc("POST /api/downloads/{id}/cancel", h(func(r *http.Request) (any, error) { s.app.Downloads.Cancel(r.PathValue("id")); return nil, nil }))
	m.HandleFunc("POST /api/downloads/{id}/retry", h(func(r *http.Request) (any, error) { return nil, s.app.Downloads.Retry(r.PathValue("id")) }))
	m.HandleFunc("DELETE /api/downloads/{id}", h(func(r *http.Request) (any, error) { s.app.Downloads.Remove(r.PathValue("id")); return nil, nil }))
	m.HandleFunc("POST /api/downloads/clear", h(func(r *http.Request) (any, error) { s.app.Downloads.ClearFinished(); return nil, nil }))

	// --- torrents
	m.HandleFunc("GET /api/torrents/providers", h(func(r *http.Request) (any, error) { return s.app.Torrents.Providers(), nil }))
	m.HandleFunc("POST /api/torrents/search", h(s.torrentSearch))
	m.HandleFunc("POST /api/torrents/download", h(s.torrentDownload))
	// When this Aniko's downloads go to another (a TV's to its computer), its
	// torrents are that one's (hostTorrents).
	m.HandleFunc("GET /api/torrent-client/status", h(func(r *http.Request) (any, error) {
		if st, ok := s.hostTorrentStatus(r); ok {
			return st, nil
		}
		return s.app.Torrents.Status(r.Context()), nil
	}))
	m.HandleFunc("GET /api/torrent-client/list", h(func(r *http.Request) (any, error) {
		if raw, ok, err := s.hostTorrents(r, "list"); ok {
			return raw, err
		}
		return s.torrentList(r)
	}))
	m.HandleFunc("POST /api/torrent-client/action", h(func(r *http.Request) (any, error) {
		if raw, ok, err := s.hostTorrents(r, "action"); ok {
			return raw, err
		}
		return s.torrentAction(r)
	}))
	m.HandleFunc("POST /api/torrent-client/start", h(func(r *http.Request) (any, error) {
		if raw, ok, err := s.hostTorrents(r, "start"); ok {
			return raw, err
		}
		return nil, s.app.Torrents.StartClient(r.Context())
	}))
	m.HandleFunc("POST /api/torrent-client/add", h(s.torrentAdd))
	m.HandleFunc("GET /api/autodownloader/rules", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Rules() }))
	m.HandleFunc("POST /api/autodownloader/rules", h(s.saveRule))
	m.HandleFunc("DELETE /api/autodownloader/rules/{id}", h(func(r *http.Request) (any, error) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		return nil, s.app.AutoDL.DeleteRule(id)
	}))
	m.HandleFunc("POST /api/autodownloader/run", h(func(r *http.Request) (any, error) {
		n, err := s.app.AutoDL.Run(r.Context())
		return map[string]int{"added": n}, err
	}))
	m.HandleFunc("GET /api/autodownloader/items", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Items(), nil }))

	// --- extensions & plugins
	m.HandleFunc("GET /api/extensions", h(func(r *http.Request) (any, error) {
		list := s.app.Extensions.List()
		// Their settings may hold API keys: not for devices on the LAN.
		if !isTrusted(r) {
			for i := range list {
				list[i].UserConfig.Values = map[string]string{}
			}
		}
		return list, nil
	}))
	m.HandleFunc("GET /api/extensions/marketplace", h(func(r *http.Request) (any, error) {
		return s.app.Extensions.Marketplace(r.Context(), r.URL.Query().Get("url"), r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/extensions/updates", h(func(r *http.Request) (any, error) { return s.app.Extensions.CheckUpdates(r.Context()), nil }))
	m.HandleFunc("POST /api/extensions/install", h(trusted(s.installExtension)))
	m.HandleFunc("POST /api/extensions/{id}/enable", h(trusted(s.enableExtension)))
	m.HandleFunc("POST /api/extensions/{id}/config", h(trusted(s.configExtension)))
	m.HandleFunc("POST /api/extensions/{id}/grant", h(trusted(func(r *http.Request) (any, error) { return nil, s.app.Extensions.Grant(r.PathValue("id")) })))
	m.HandleFunc("POST /api/extensions/{id}/update", h(trusted(func(r *http.Request) (any, error) { return s.app.Extensions.Update(r.Context(), r.PathValue("id")) })))
	m.HandleFunc("DELETE /api/extensions/{id}", h(trusted(func(r *http.Request) (any, error) { return nil, s.app.Extensions.Uninstall(r.PathValue("id")) })))
	m.HandleFunc("GET /api/extensions/{id}/logs", h(func(r *http.Request) (any, error) { return s.app.Extensions.Logs(r.PathValue("id")), nil }))
	m.HandleFunc("GET /api/plugins/ui", h(func(r *http.Request) (any, error) { return s.app.Extensions.PluginStates(), nil }))
	m.HandleFunc("POST /api/plugins/{id}/event", h(s.pluginEvent))
	m.HandleFunc("POST /api/plugins/navigate", h(func(r *http.Request) (any, error) {
		var body map[string]any
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		body["kind"] = "navigate"
		s.app.Extensions.BroadcastPluginEvent(body)
		return nil, nil
	}))

	// --- manga
	m.HandleFunc("GET /api/manga/collection", h(func(r *http.Request) (any, error) {
		return s.app.Platform.Collection(r.Context(), "MANGA", r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/manga/providers", h(s.mangaProviders))
	m.HandleFunc("GET /api/manga/{id}", h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.app.Platform.Media(r.Context(), id, r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/manga/{id}/chapters", h(s.mangaChapters))
	m.HandleFunc("GET /api/manga/pages", h(func(r *http.Request) (any, error) {
		if r.URL.Query().Get("provider") == LocalProvider {
			return s.localPages(r.URL.Query().Get("chapterId"))
		}
		return s.app.Manga.Pages(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("chapterId"))
	}))
	s.mangaDownloadRoutes()
	m.HandleFunc("GET /api/manga/search", h(func(r *http.Request) (any, error) {
		return s.app.Manga.Search(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("q"))
	}))
	m.HandleFunc("POST /api/manga/mapping", h(s.mangaMapping))
	m.HandleFunc("POST /api/manga/{id}/progress", h(s.mangaProgress))
	m.HandleFunc("POST /api/manga/{id}/reading", h(s.mangaStartReading))
	m.HandleFunc("GET /api/manga/{id}/position", h(s.mangaPosition))
	m.HandleFunc("PUT /api/manga/{id}/position", h(s.saveMangaPosition))

	// --- updates
	m.HandleFunc("GET /api/update", h(s.updateStatus))
	m.HandleFunc("POST /api/update/check", h(s.checkUpdate))
	m.HandleFunc("POST /api/update/apply", h(s.applyUpdate))
	m.HandleFunc("POST /api/update/restart", s.restartAniko)

	// --- web UI (SPA)
	m.HandleFunc("/", s.serveUI)
}

func pathID(r *http.Request, name string) (int, error) {
	id, err := strconv.Atoi(r.PathValue(name))
	if err != nil || id <= 0 {
		return 0, badRequest("invalid id")
	}
	return id, nil
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

// ---------------------------------------------------------------------------
// Static UI

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if st, err := fs.Stat(s.ui, p); err != nil || st.IsDir() {
		p = "index.html" // SPA fallback
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	data, err := fs.ReadFile(s.ui, p)
	if err != nil {
		http.Error(w, "The web UI was not built. Run `make` in the project root.", http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, p, time.Time{}, strings.NewReader(string(data)))
}

// ---------------------------------------------------------------------------
// Core handlers

type featureStatus struct {
	Mpv     bool `json:"mpv"`
	Ffmpeg  bool `json:"ffmpeg"`
	Ffprobe bool `json:"ffprobe"`
	AniCli  bool `json:"aniCli"`
	// AniCliHost: the computer that runs ani-cli for this Aniko, when it
	// doesn't run here (Android).
	AniCliHost string `json:"aniCliHost,omitempty"`
	YtDlp      bool   `json:"ytDlp"`
	XdgOpen    bool   `json:"xdgOpen"`
}

func (s *Server) status(r *http.Request) (any, error) {
	cfg := s.app.Settings.Get()
	feat := featureStatus{}
	_, feat.Mpv = util.LookPath(cfg.Mpv.Path)
	_, feat.Ffmpeg = util.LookPath(cfg.Transcode.FfmpegPath)
	_, feat.Ffprobe = util.LookPath(cfg.Transcode.FfprobePath)
	feat.AniCli = s.app.AniCli.Available()
	feat.AniCliHost = s.app.AniCli.RemoteName()
	_, feat.YtDlp = util.LookPath("yt-dlp")
	_, feat.XdgOpen = util.LookPath("xdg-open")
	feat.XdgOpen = feat.XdgOpen || runtime.GOOS == "windows" || runtime.GOOS == "darwin" // Explorer, Finder
	kind := map[clientKind]string{clientShell: "desktop", clientLocal: "local", clientLAN: "lan", clientOwner: "owner"}[kindOf(r)]
	if s.app.HubStore != nil && kindOf(r) == clientLAN {
		return s.hubStatus(r, nil), nil
	}
	host, _ := os.Hostname()
	var lan []string
	if cfg.Server.AllowLAN {
		lan = lanURLs(cfg.Server.Port)
	}
	// Home's questions about the other Anikos: for this computer, which
	// decides about sharing.
	var prompts []share.Prompt
	if isTrusted(r) {
		prompts = s.app.Share.Prompts()
	}
	st := map[string]any{
		"version":        config.AppVersion,
		"appName":        config.AppName,
		"user":           s.app.Platform.Viewer(),
		"loggedIn":       s.app.Platform.LoggedIn(),
		"anilistAuthUrl": "https://anilist.co/api/v2/oauth/authorize?client_id=" + cfg.Anilist.ClientID + "&response_type=token",
		"features":       feat,
		// The Anikos this one is paired with (Home's Sync).
		"paired": s.app.Share.Paired(),
		// The computers sharing with this one that don't answer (asleep).
		"asleep": s.app.Share.Asleep(),
		// Share with a new one, update one (Home).
		"devicePrompts": prompts,
		// Kept awake now: another device is playing this computer's files.
		"keptAwake":    s.app.Awake.Active(),
		"client":       kind,
		"hostname":     host,
		"platform":     runtime.GOOS,
		"scanning":     s.app.Scanner.Running(),
		"dataDir":      s.app.DataDir,
		"listenAddr":   s.Addr(),
		"lanUrls":      lan,
		"webUiForced":  s.ForceWebUI,
		"setupRunning": setupRunning(),
		"settings":     settingsFor(r, cfg),
	}
	if s.app.HubStore != nil {
		return s.hubStatus(r, st), nil
	}
	return st, nil
}

func (s *Server) saveSettings(r *http.Request) (any, error) {
	// Two separate copies: decoding writes into next's slices, and cur must
	// keep the old values to restore the fields LAN devices may not change.
	cur, next := s.app.Settings.Get(), s.app.Settings.Get()
	if err := decode(r, &next); err != nil {
		return nil, err
	}
	// Devices on the LAN may not change network/security settings or
	// anything that decides which programs run or which folders are read
	// on this computer.
	if !isTrusted(r) {
		next.Server = cur.Server
		next.Mpv.Path, next.Mpv.ExtraArgs, next.Mpv.Socket = cur.Mpv.Path, cur.Mpv.ExtraArgs, cur.Mpv.Socket
		next.Transcode.FfmpegPath, next.Transcode.FfprobePath, next.Transcode.VaapiNode = cur.Transcode.FfmpegPath, cur.Transcode.FfprobePath, cur.Transcode.VaapiNode
		next.AniCli.Path, next.AniCli.DownloadDir = cur.AniCli.Path, cur.AniCli.DownloadDir
		// Torrent client logins too: they never see the passwords, and
		// pointing the client at another host would send it the password.
		next.Qbittorrent, next.Transmission = cur.Qbittorrent, cur.Transmission
		next.Library.Off, next.Library.Dir, next.Library.ExtraDirs = cur.Library.Off, cur.Library.Dir, cur.Library.ExtraDirs
		// Who this computer's library is shared with is its own business.
		next.Sharing = cur.Sharing
		// Where extensions come from, and the apps AniList and Discord
		// logins go to (another's would get the AniList login).
		next.Extensions.MarketplaceURL = cur.Extensions.MarketplaceURL
		next.Anilist.ClientID, next.Discord.ClientID = cur.Anilist.ClientID, cur.Discord.ClientID
		// Which builds this computer installs.
		next.Updates = cur.Updates
	}
	// Parental controls keep library sharing on: the computer that set them
	// hears what's watched, and can lift them.
	if cur.Sharing.Enabled && !next.Sharing.Enabled && s.app.Share.Parental().Active() {
		return nil, forbidden("parental controls are on: library sharing stays on (the computer that set them can lift them)")
	}
	// Turning off the web UI from a browser would lock the browser out.
	if kindOf(r) != clientShell && !next.Server.WebUI && cur.Server.WebUI && !s.ForceWebUI {
		return nil, forbidden("turn off the Web UI from the desktop app, otherwise this browser would lose access")
	}
	saved, err := s.app.Settings.Save(next)
	return settingsFor(r, saved), err
}

// settingsFor hides passwords from devices on the LAN.
func settingsFor(r *http.Request, cfg config.Settings) config.Settings {
	if !isTrusted(r) {
		cfg.Server.Password = ""
		cfg.Qbittorrent.Password, cfg.Transmission.Password = "", ""
	}
	return cfg
}

func (s *Server) anilistLogin(r *http.Request) (any, error) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	tok := strings.TrimSpace(body.Token)
	// Accept a pasted redirect URL too (…#access_token=XYZ&token_type=…).
	if i := strings.Index(tok, "access_token="); i >= 0 {
		tok = tok[i+len("access_token="):]
		if j := strings.IndexAny(tok, "&# "); j >= 0 {
			tok = tok[:j]
		}
	}
	v, err := s.app.Platform.Login(r.Context(), tok)
	if err != nil {
		return nil, badRequest("AniList rejected the token: " + err.Error())
	}
	s.app.Share.LoggedIn()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = s.app.Platform.Collection(ctx, "ANIME", true)
	}()
	return v, nil
}

func (s *Server) serverLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ip := clientIP(r).String()
	if wait := s.logins.wait(ip, time.Now()); wait > 0 {
		s.signedIn(r, false, "was kept out: too many wrong passwords")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("too many wrong passwords: try again in %d minute(s)", int(wait.Minutes())+1)})
		return
	}
	cfg := s.app.Settings.Get()
	if cfg.Server.Password == "" || constEq(body.Password, cfg.Server.Password) {
		s.logins.passed(ip)
		s.signedIn(r, true, "signed in to the dashboard")
		http.SetCookie(w, &http.Cookie{Name: "kumo_session", Value: s.sessionToken(), Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(365 * 24 * time.Hour)})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.logins.failed(ip, time.Now())
	s.signedIn(r, false, "tried a wrong password")
	time.Sleep(time.Second) // slow down guessing
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
}

// trusted wraps a handler only this computer may use (not the LAN).
func trusted(fn func(r *http.Request) (any, error)) func(r *http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only available on this computer")
		}
		return fn(r)
	}
}

// loginGuard counts wrong server passwords by address: after maxWrong in a
// row, that address waits (longer each time) before it may try again.
type loginGuard struct {
	mu    sync.Mutex
	tries map[string]*loginTries
}

type loginTries struct {
	wrong int
	until time.Time
}

const maxWrong = 5

func (g *loginGuard) wait(ip string, now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t := g.tries[ip]; t != nil && now.Before(t.until) {
		return t.until.Sub(now)
	}
	return 0
}

func (g *loginGuard) failed(ip string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tries == nil {
		g.tries = map[string]*loginTries{}
	}
	if len(g.tries) > 10000 {
		clear(g.tries)
	}
	t := g.tries[ip]
	if t == nil {
		t = &loginTries{}
		g.tries[ip] = t
	}
	t.wrong++
	if t.wrong >= maxWrong {
		// 1 minute, then 2, 4… up to an hour.
		d := time.Minute << min(t.wrong-maxWrong, 6)
		t.until = now.Add(min(d, time.Hour))
	}
}

func (g *loginGuard) passed(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.tries, ip)
}

func startDetached(name string, args ...string) error {
	return util.Detach(name, args...)
}

func (s *Server) listDirs(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("browsing folders is only available on this computer")
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		p, _ = os.UserHomeDir()
	}
	p = filepath.Clean(config.ExpandHome(p))
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	dirs := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil && st.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	return map[string]any{"path": p, "parent": filepath.Dir(p), "dirs": dirs, "sep": string(filepath.Separator), "roots": fsRoots()}, nil
}

func (s *Server) openPath(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	var body struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	target := body.Path
	if body.URL != "" {
		if !strings.HasPrefix(body.URL, "https://") && !strings.HasPrefix(body.URL, "http://") {
			return nil, badRequest("invalid URL")
		}
		target = body.URL
	} else {
		st, err := os.Stat(target)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		// Only ever open folders: opening a file could run it (.desktop
		// files, scripts, .exe).
		if !st.IsDir() {
			target = filepath.Dir(target)
		}
	}
	return nil, util.Open(target)
}

// ---------------------------------------------------------------------------
// Anime

func (s *Server) animeCollection(r *http.Request) (any, error) {
	// fresh: the list as AniList has it now (another device's progress), at
	// most a minute old: the copy here at once, AniList asked in the
	// background (the page hears collection-updated), so a slow AniList
	// doesn't hold the page. refresh: asked now, as you asked.
	var view *library.CollectionView
	var err error
	if r.URL.Query().Get("fresh") == "1" && r.URL.Query().Get("refresh") != "1" {
		coll, refreshing, cerr := s.app.Platform.CollectionSoon(r.Context(), "ANIME", time.Minute)
		if cerr != nil {
			return nil, cerr
		}
		if view, err = s.app.Library.CollectionWith(r.Context(), coll); err == nil {
			view.Refreshing = refreshing
		}
	} else {
		view, err = s.app.Library.Collection(r.Context(), r.URL.Query().Get("refresh") == "1")
	}
	if err == nil {
		go s.app.PrefetchCollectionArt(view)
	}
	return view, err
}

// hideContinue removes an anime from "Continue watching" until it offers
// another episode or is watched again.
func (s *Server) hideContinue(r *http.Request) (any, error) {
	var body struct {
		MediaID int `json:"mediaId"`
		Episode int `json:"episode"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.MediaID <= 0 || body.Episode <= 0 {
		return nil, badRequest("mediaId and episode are required")
	}
	if err := s.app.Library.HideContinue(body.MediaID, body.Episode); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("collection-updated", map[string]any{"mediaId": body.MediaID})
	return nil, nil
}

func (s *Server) unhideContinue(r *http.Request) (any, error) {
	var body struct {
		MediaID int `json:"mediaId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.MediaID <= 0 {
		return nil, badRequest("mediaId is required")
	}
	if err := s.app.Library.UnhideContinue(body.MediaID); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("collection-updated", map[string]any{"mediaId": body.MediaID})
	return nil, nil
}

func (s *Server) animeEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	e, err := s.app.Library.Entry(r.Context(), id, r.URL.Query().Get("refresh") == "1")
	if err == nil {
		go s.app.PrefetchEntryArt(e)
	}
	return e, err
}

func (s *Server) updateEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var u anilist.EntryUpdate
	if err := decode(r, &u); err != nil {
		return nil, err
	}
	u.MediaID = id
	return nil, s.app.Platform.UpdateEntry(r.Context(), u)
}

func (s *Server) deleteEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.app.Platform.DeleteEntry(r.Context(), id)
}

func (s *Server) search(r *http.Request) (any, error) {
	var p anilist.SearchParams
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	if !s.app.Settings.Get().UI.ShowAdult {
		f := false
		p.IsAdult = &f
	}
	return s.app.Platform.Search(r.Context(), p)
}

// discover is the Discover page, cached by the platform: only the very first
// one waits for AniList.
func (s *Server) discover(r *http.Request) (any, error) {
	return s.app.Platform.Discover(r.Context())
}

func (s *Server) schedule(r *http.Request) (any, error) {
	days := queryInt(r, "days")
	if days <= 0 || days > 14 {
		days = 7
	}
	now := time.Now()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	items, err := s.app.Platform.Schedule(r.Context(), startDay.Unix(), startDay.AddDate(0, 0, days+1).Unix())
	if err != nil {
		return nil, err
	}
	coll, _ := s.app.Platform.Collection(r.Context(), "ANIME", false)
	inList := map[int]string{}
	if coll != nil {
		for _, e := range coll.Entries() {
			inList[e.MediaID] = e.Status
		}
	}
	type item struct {
		*anilist.AiringEpisode
		ListStatus string `json:"listStatus"`
	}
	out := []item{}
	showAdult := s.app.Settings.Get().UI.ShowAdult
	for _, it := range items {
		if it.Media == nil || (it.Media.IsAdult && !showAdult) {
			continue
		}
		out = append(out, item{it, inList[it.Media.ID]})
	}
	return out, nil
}

func (s *Server) rawList(r *http.Request) (any, error) {
	t := strings.ToUpper(r.URL.Query().Get("type"))
	if t == "" {
		t = "ANIME"
	}
	return s.app.Platform.Collection(r.Context(), t, r.URL.Query().Get("refresh") == "1")
}
