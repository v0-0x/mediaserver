package api

import (
	"crypto/sha256"
	"crypto/subtle"
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

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// routes registers the API. GET requests only read (filling a cache when it
// misses): fetching again what's cached, syncing, installing, testing or
// changing anything is a POST, PUT, PATCH or DELETE, which other websites
// can't send (the middleware's Origin check), whereas an image or a link on
// any page can make a browser send a GET.
func (s *Server) routes() {
	m := s.mux
	s.peerRoutes()
	s.uploadRoutes()
	s.torrentStreamRoutes()
	s.noteRoutes()
	s.cleanupRoutes()
	s.backupRoutes()

	// --- core
	m.HandleFunc("GET /api/status", h(s.status))
	m.HandleFunc("GET /api/settings", h(func(r *http.Request) (any, error) { return settingsFor(r, s.app.Settings.Get()), nil }))
	// The Stats page: what was watched in Moviko, month by month.
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
	m.HandleFunc("POST /api/auth/server-login", s.serverLogin)
	m.HandleFunc("GET /api/fs/dirs", h(s.listDirs))
	m.HandleFunc("POST /api/open", h(s.openPath))
	m.HandleFunc("POST /api/setup/programs", h(s.installPrograms))
	// The server's log can hold addon links with their keys: this computer's.
	m.HandleFunc("GET /api/logs", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only available on this computer")
		}
		return s.app.Logs.Lines(), nil
	}))
	m.HandleFunc("POST /api/cache/clear", h(func(r *http.Request) (any, error) { return nil, s.app.DB.ClearCache() }))
	m.HandleFunc("GET /api/cache/size", h(func(r *http.Request) (any, error) {
		n, err := s.app.DB.CacheSize()
		return map[string]int{"entries": n}, err
	}))

	// --- movies & shows (the list, title pages) and the TMDB catalog
	m.HandleFunc("GET /api/media/collection", h(s.mediaCollection))
	m.HandleFunc("POST /api/continue/hide", h(s.hideContinue))
	m.HandleFunc("POST /api/continue/unhide", h(s.unhideContinue))
	m.HandleFunc("GET /api/media/{id}", h(s.mediaEntry))
	m.HandleFunc("POST /api/media/{id}/refresh", h(s.refreshMedia))
	m.HandleFunc("POST /api/media/{id}/entry", h(s.updateEntry))
	m.HandleFunc("DELETE /api/media/{id}/entry", h(s.deleteEntry))
	m.HandleFunc("POST /api/media/{id}/episode", h(s.markEpisode))
	m.HandleFunc("GET /api/media/{id}/episodes", h(s.mediaEpisodes))
	m.HandleFunc("GET /api/media/{id}/season/{n}", h(s.season))
	m.HandleFunc("GET /api/media/{id}/language", h(s.getLanguage))
	m.HandleFunc("PUT /api/media/{id}/language", h(s.putLanguage))
	m.HandleFunc("POST /api/catalog/search", h(s.search))
	m.HandleFunc("GET /api/catalog/discover", h(s.discover))
	m.HandleFunc("GET /api/catalog/schedule", h(s.schedule))
	m.HandleFunc("GET /api/catalog/list", h(s.rawList))
	m.HandleFunc("GET /api/catalog/genres", h(func(r *http.Request) (any, error) { return s.app.Platform.Genres(r.Context()) }))
	s.letterboxdRoutes()

	// --- library
	m.HandleFunc("POST /api/library/scan", h(s.scan))
	m.HandleFunc("GET /api/library/unmatched", h(s.unmatched))
	m.HandleFunc("GET /api/library/ignored", h(func(r *http.Request) (any, error) { return s.app.Files.Ignored() }))
	m.HandleFunc("GET /api/library/files", h(func(r *http.Request) (any, error) { return s.app.Files.All() }))
	// The Local library page: the titles with files, this computer's or a
	// sharing Moviko's (?host=<its id>).
	m.HandleFunc("GET /api/library/titles", h(func(r *http.Request) (any, error) {
		return s.app.Library.Titles(r.Context(), r.URL.Query().Get("host"))
	}))
	m.HandleFunc("POST /api/library/match", h(s.match))
	m.HandleFunc("GET /api/library/folders", h(func(r *http.Request) (any, error) { return s.app.Scanner.Folders() }))
	m.HandleFunc("POST /api/library/index", h(s.indexFolders))
	m.HandleFunc("POST /api/library/unmatch", h(s.unmatch))
	m.HandleFunc("POST /api/library/ignore", h(s.ignore))
	m.HandleFunc("PATCH /api/library/file", h(s.patchFile))
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
			h(func(r *http.Request) (any, error) {
				p, err := s.app.Local.Probe(r.Context(), r.URL.Query().Get("path"))
				// What this player decodes decides how it's played.
				if err == nil && r.URL.Query().Has("caps") {
					s.app.Local.Decide(p, stream.ParseCaps(r.URL.Query().Get("caps")))
				}
				return p, err
			})(w, r)
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

	// --- streaming providers (providers_routes.go)
	s.providerRoutes()

	// --- downloads
	m.HandleFunc("GET /api/downloads", h(func(r *http.Request) (any, error) { return s.app.Downloads.List(), nil }))
	// The downloads of the Moviko this one's go to (a TV's computer): its list,
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

	// --- torrents (search, client, auto downloader)
	s.torrentRoutes()

	// --- extensions & plugins (devices on the LAN use them and see what's
	// installed; this computer manages them: lan.go)
	m.HandleFunc("GET /api/extensions", h(func(r *http.Request) (any, error) { return extensionsFor(r, s.app.Extensions.List()), nil }))
	m.HandleFunc("GET /api/extensions/marketplace", h(s.marketplace))
	m.HandleFunc("POST /api/extensions/marketplace/refresh", h(thisComputer(s.refreshMarketplace)))
	m.HandleFunc("GET /api/extensions/updates", h(thisComputer(func(r *http.Request) (any, error) { return s.app.Extensions.CheckUpdates(r.Context()), nil })))
	m.HandleFunc("POST /api/extensions/install", h(s.installExtension))
	m.HandleFunc("POST /api/extensions/{id}/enable", h(thisComputer(s.enableExtension)))
	m.HandleFunc("POST /api/extensions/{id}/config", h(thisComputer(s.configExtension)))
	m.HandleFunc("POST /api/extensions/{id}/grant", h(func(r *http.Request) (any, error) {
		// A plugin's permissions (files it reads and writes, commands it
		// runs) are accepted where plugins are installed: on this computer.
		if !isTrusted(r) {
			return nil, forbidden("a plugin's permissions can only be granted on this computer")
		}
		return nil, s.app.Extensions.Grant(r.PathValue("id"))
	}))
	m.HandleFunc("POST /api/extensions/{id}/update", h(thisComputer(func(r *http.Request) (any, error) { return s.app.Extensions.Update(r.Context(), r.PathValue("id")) })))
	m.HandleFunc("DELETE /api/extensions/{id}", h(s.uninstallExtension))
	// An extension's console can hold the links and keys it works with, like
	// the server's log: this computer's.
	m.HandleFunc("GET /api/extensions/{id}/logs", h(thisComputer(func(r *http.Request) (any, error) { return s.app.Extensions.Logs(r.PathValue("id")), nil })))
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

	// --- updates
	m.HandleFunc("GET /api/update", h(s.updateStatus))
	m.HandleFunc("POST /api/update/check", h(s.checkUpdate))
	m.HandleFunc("POST /api/update/apply", h(s.applyUpdate))
	m.HandleFunc("POST /api/update/restart", s.restartMoviko)

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

// uiContentSecurityPolicy guards the web UI page. It keeps the strong
// anti-abuse directives — no framing (frame-ancestors), no plugins/embeds
// (object-src), no <base> or form hijacking — while allowing what the app and
// its plugin webviews actually need:
//   - inline script and style: plugin "css" nodes render a <style>, React
//     sets style attributes, and plugin webviews are sandboxed srcdoc frames
//     whose inline bridge and plugin scripts inherit this page's policy.
//   - img/media/connect to remote http(s): posters, addon and plugin icons,
//     and streams that play straight from a provider's URL (remote HLS is
//     fetched by hls.js, which also runs in a blob: worker).
//   - frame-src for the YouTube trailer and the plugin webview documents.
//
// It is not a defence against script injection on this origin (the app uses
// no HTML sinks and React escapes), but it blocks clickjacking and narrows
// what injected markup could reach.
const uiContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https: http:; " +
	"media-src 'self' blob: data: https: http:; " +
	"font-src 'self' data:; " +
	"connect-src 'self' blob: data: https: http: ws: wss:; " +
	"worker-src 'self' blob:; " +
	"frame-src 'self' https: data: blob:; " +
	"child-src 'self' https: data: blob:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

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
		// The UI document: no other site may frame it (clickjacking), and a
		// policy that still lets the app and its plugin webviews work.
		w.Header().Set("Content-Security-Policy", uiContentSecurityPolicy)
		w.Header().Set("X-Frame-Options", "DENY")
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
	YtDlp   bool `json:"ytDlp"`
	XdgOpen bool `json:"xdgOpen"`
}

func (s *Server) status(r *http.Request) (any, error) {
	cfg := s.app.Settings.Get()
	feat := featureStatus{}
	_, feat.Mpv = util.LookPath(cfg.Mpv.Path)
	_, feat.Ffmpeg = util.LookPath(cfg.Transcode.FfmpegPath)
	_, feat.Ffprobe = util.LookPath(cfg.Transcode.FfprobePath)
	_, feat.YtDlp = util.LookPath("yt-dlp")
	_, feat.XdgOpen = util.LookPath("xdg-open")
	feat.XdgOpen = feat.XdgOpen || runtime.GOOS == "windows" || runtime.GOOS == "darwin" // Explorer, Finder
	kind := map[clientKind]string{clientShell: "desktop", clientLocal: "local", clientLAN: "lan"}[kindOf(r)]
	host, _ := os.Hostname()
	var lan []string
	if cfg.Server.AllowLAN {
		lan = lanURLs(cfg.Server.Port)
	}
	viewer := s.app.Platform.Viewer() // the connected Letterboxd member
	// Home's questions about the other Movikos: for this computer, which
	// decides about sharing.
	var prompts []share.Prompt
	if isTrusted(r) {
		prompts = s.app.Share.Prompts()
	}
	st := map[string]any{
		"version":        config.AppVersion,
		"appName":        config.AppName,
		"user":           viewer,
		"loggedIn":       viewer != nil,
		"letterboxd":     viewer,
		"tmdbConfigured": s.app.Platform.Configured(),
		"features":       feat,
		// The Movikos this one is paired with (Home's Sync).
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
	// mediaserver's movies side: where its dashboard (the anime side) is.
	if s.Owner != nil {
		st["server"] = map[string]any{"dashboardPort": s.DashboardPort}
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
	// The Letterboxd account is connected and disconnected on its own
	// (letterboxd.go), not with the other settings: a page opened before
	// would put back the account it saw.
	next.Letterboxd.Username = cur.Letterboxd.Username
	// Devices on the LAN may not change network/security settings or
	// anything that decides which programs run or which folders are read
	// on this computer.
	if !isTrusted(r) {
		next.Server = cur.Server
		next.Mpv.Path, next.Mpv.ExtraArgs, next.Mpv.Socket = cur.Mpv.Path, cur.Mpv.ExtraArgs, cur.Mpv.Socket
		next.Transcode.FfmpegPath, next.Transcode.FfprobePath, next.Transcode.VaapiNode = cur.Transcode.FfmpegPath, cur.Transcode.FfprobePath, cur.Transcode.VaapiNode
		next.Downloads.Dir = cur.Downloads.Dir
		// Torrent client logins too: they never see the passwords, and
		// pointing the client at another host would send it the password.
		next.Qbittorrent, next.Transmission = cur.Qbittorrent, cur.Transmission
		// Nor the API keys, which they don't see either.
		next.TMDB.APIKey = cur.TMDB.APIKey
		next.Letterboxd.APIClientID, next.Letterboxd.APIClientSecret = cur.Letterboxd.APIClientID, cur.Letterboxd.APIClientSecret
		// Nor where torrents are searched (an indexer may be on the LAN, a
		// mirror their own server, sent the indexers' API keys) or where
		// provider code and addon collections are fetched from.
		next.TorrentSources, next.Providers = cur.TorrentSources, cur.Providers
		// Nor the extension settings: the marketplace is where they're
		// installed and updated from on this computer.
		next.Extensions = cur.Extensions
		next.Library.Off, next.Library.Dir, next.Library.ExtraDirs = cur.Library.Off, cur.Library.Dir, cur.Library.ExtraDirs
		// Who this computer's library is shared with is its own business.
		next.Sharing = cur.Sharing
		// Where extensions come from, and the app Discord's presence goes to.
		next.Extensions.MarketplaceURL = cur.Extensions.MarketplaceURL
		next.Discord.ClientID = cur.Discord.ClientID
		// Which builds this computer installs.
		next.Updates = cur.Updates
	}
	// Turning off the web UI from a browser would lock the browser out.
	if kindOf(r) != clientShell && !next.Server.WebUI && cur.Server.WebUI && !s.ForceWebUI {
		return nil, forbidden("turn off the Web UI from the desktop app, otherwise this browser would lose access")
	}
	saved, err := s.app.Settings.Save(next)
	return settingsFor(r, saved), err
}

// settingsFor hides passwords and API keys from devices on the LAN.
func settingsFor(r *http.Request, cfg config.Settings) config.Settings {
	if !isTrusted(r) {
		cfg.Server.Password = ""
		cfg.Qbittorrent.Password, cfg.Transmission.Password = "", ""
		cfg.TMDB.APIKey = ""
		cfg.Letterboxd.APIClientSecret = ""
		// A copy: the indexers are shared with the settings in memory.
		indexers := make([]config.TorznabIndexer, len(cfg.TorrentSources.Torznab))
		for i, ix := range cfg.TorrentSources.Torznab {
			ix.APIKey, ix.URL = "", hideURLKeys(ix.URL)
			indexers[i] = ix
		}
		cfg.TorrentSources.Torznab = indexers
		// Nor the media servers' tokens (a copy, as above).
		servers := make([]config.MediaServer, len(cfg.Providers.MediaServers))
		for i, ms := range cfg.Providers.MediaServers {
			ms.Token = ""
			servers[i] = ms
		}
		cfg.Providers.MediaServers = servers
	}
	return cfg
}

// hideURLKeys blanks the API keys, passkeys and tokens an address carries
// in its query (a Torznab feed's URL may hold its apikey).
func hideURLKeys(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	for k := range q {
		name := strings.ToLower(k)
		for _, secret := range []string{"key", "token", "pass", "secret", "auth"} {
			if strings.Contains(name, secret) {
				q[k] = []string{""}
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) serverLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// One attempt at a time, each wrong one holding the next for a second:
	// guesses sent side by side wait their turn instead of all being
	// answered at once. Asked again meanwhile, the device is told to wait.
	// An address with too many wrong ones waits longer each time.
	ip := clientIP(r).String()
	if wait := s.logins.wait(ip, time.Now()); wait > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("too many wrong passwords: try again in %d minute(s)", int(wait.Minutes())+1)})
		return
	}
	if !s.loginMu.TryLock() {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts: try again in a moment"})
		return
	}
	defer s.loginMu.Unlock()
	cfg := s.app.Settings.Get()
	if cfg.Server.Password == "" || passwordEq(body.Password, cfg.Server.Password) {
		s.logins.passed(ip)
		http.SetCookie(w, &http.Cookie{Name: "kumobox_session", Value: s.sessionToken(), Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(365 * 24 * time.Hour)})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.logins.failed(ip, time.Now())
	time.Sleep(time.Second) // slow down guessing
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
}

// passwordEq compares passwords in a time that says nothing about them, not
// even their length.
func passwordEq(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
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
// Movies and shows

// mediaCollection is the Home and library page: the list, the library's
// files, "Continue watching". type (MOVIE or TV) keeps only movies or shows.
// The list's titles still on the air are fetched again in the background
// when due; the page asks the other Movikos for their watch history
// (another computer's progress) with POST /api/sharing/refresh.
func (s *Server) mediaCollection(r *http.Request) (any, error) {
	view, err := s.app.Library.Collection(r.Context(), false)
	if err != nil {
		return nil, err
	}
	go s.app.PrefetchCollectionArt(view)
	if t := strings.ToUpper(r.URL.Query().Get("type")); t == catalog.TypeMovie || t == catalog.TypeTV {
		view = filterView(view, t)
	}
	return view, nil
}

// filterView is a copy of the view with only the titles of a type.
func filterView(v *library.CollectionView, mediaType string) *library.CollectionView {
	out := *v
	keep := func(items []*library.CollectionItem) []*library.CollectionItem {
		kept := []*library.CollectionItem{}
		for _, it := range items {
			if it.Media != nil && catalog.KindOf(it.Media.ID) == mediaType {
				kept = append(kept, it)
			}
		}
		return kept
	}
	out.Lists = nil
	for _, l := range v.Lists {
		if items := keep(l.Items); len(items) > 0 {
			nl := *l
			nl.Items = items
			out.Lists = append(out.Lists, &nl)
		}
	}
	out.LocalOnly = keep(v.LocalOnly)
	out.ContinueWatching = nil
	for _, c := range v.ContinueWatching {
		if c.Media != nil && catalog.KindOf(c.Media.ID) == mediaType {
			out.ContinueWatching = append(out.ContinueWatching, c)
		}
	}
	return &out
}

// hideContinue removes a title from "Continue watching" until it offers
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

func (s *Server) mediaEntry(r *http.Request) (any, error) { return s.entry(r, false) }

// refreshMedia is POST /api/media/{id}/refresh: the title page with the
// title's data fetched from TMDB again.
func (s *Server) refreshMedia(r *http.Request) (any, error) { return s.entry(r, true) }

func (s *Server) entry(r *http.Request, refresh bool) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	e, err := s.app.Library.Entry(r.Context(), id, refresh)
	if err == nil {
		go s.app.PrefetchEntryArt(e)
	}
	return e, err
}

// mediaEpisodes lists a title's episodes with their files and history.
func (s *Server) mediaEpisodes(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return s.app.Library.Episodes(r.Context(), id)
}

// season is a season of a show with its episodes (TMDB's).
func (s *Server) season(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || !catalog.IsShow(id) {
		return nil, badRequest("not a season of a show")
	}
	return s.app.Platform.Season(r.Context(), id, n)
}

func (s *Server) updateEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var u catalog.EntryUpdate
	if err := decode(r, &u); err != nil {
		return nil, err
	}
	u.MediaID = id
	return nil, s.app.UpdateEntry(r.Context(), u)
}

func (s *Server) deleteEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.app.Platform.DeleteEntry(r.Context(), id)
}

func (s *Server) search(r *http.Request) (any, error) {
	var p catalog.SearchParams
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
// one waits for TMDB.
func (s *Server) discover(r *http.Request) (any, error) {
	return s.app.Platform.Discover(r.Context())
}

// schedule is the TV calendar: the episodes of the shows on the list
// airing between start and end (unix seconds; by default from yesterday,
// for days days).
func (s *Server) schedule(r *http.Request) (any, error) {
	start, end := int64(queryInt(r, "start")), int64(queryInt(r, "end"))
	if start <= 0 || end <= start || end-start > 62*24*3600 {
		days := queryInt(r, "days")
		if days <= 0 || days > 14 {
			days = 7
		}
		now := time.Now()
		startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
		start, end = startDay.Unix(), startDay.AddDate(0, 0, days+1).Unix()
	}
	items, err := s.app.Platform.Schedule(r.Context(), start, end)
	if err != nil {
		return nil, err
	}
	coll, _ := s.app.Platform.Collection(r.Context(), catalog.TypeTV, false)
	inList := map[int]string{}
	if coll != nil {
		for _, e := range coll.Entries() {
			inList[e.MediaID] = e.Status
		}
	}
	type item struct {
		*catalog.AiringEpisode
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

// rawList is the list itself: its movies (type=MOVIE), shows (TV) or both.
func (s *Server) rawList(r *http.Request) (any, error) {
	return s.app.Platform.Collection(r.Context(), r.URL.Query().Get("type"), false)
}
