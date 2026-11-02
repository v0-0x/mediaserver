package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/providers/mediaserver"
	"github.com/v0-0x/mediaserver/movies/internal/providers/stremio"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// Streaming providers: the streams of a movie or an episode from every
// provider (Internet Archive, Stremio addons, Nuvio providers, online
// streaming extensions), playing, downloading or sending one to the torrent
// client, and managing Stremio addons and Nuvio repositories (Nuvio
// providers are extensions: enabling, logs, updates and removal go through
// /api/extensions with their extension id, nuvio.<id>).
//
// Devices on the LAN may use providers but not manage them (lan.go): they
// don't install, remove, turn on or off, order or refresh them, nor read
// collections and repositories other than those of the settings.

func (s *Server) providerRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/providers", h(func(r *http.Request) (any, error) { return s.app.ProviderInfos(), nil }))
	m.HandleFunc("GET /api/providers/streams", h(s.providerStreams))
	m.HandleFunc("POST /api/providers/streams/refresh", h(s.refreshProviderStreams))
	m.HandleFunc("GET /api/providers/streams/live", s.providerStreamsLive)
	m.HandleFunc("POST /api/providers/streams/live", s.providerStreamsLive)
	m.HandleFunc("GET /api/providers/health", h(func(r *http.Request) (any, error) { return s.app.ProviderHealth(), nil }))
	m.HandleFunc("POST /api/providers/{id}/test", h(s.testProvider))
	m.HandleFunc("POST /api/providers/mediaservers/test", h(s.testMediaServer))
	m.HandleFunc("POST /api/providers/play", h(s.providerPlay))
	m.HandleFunc("GET /api/providers/subtitles", h(s.providerSubtitles))
	m.HandleFunc("POST /api/providers/download", h(s.providerDownload))
	// Extensions find titles by searching: the match can be picked by hand.
	m.HandleFunc("GET /api/providers/search", h(s.providerSearch))
	m.HandleFunc("POST /api/providers/mapping", h(s.providerMapping))

	m.HandleFunc("GET /api/providers/stremio/addons", h(func(r *http.Request) (any, error) { return s.app.Stremio.List(), nil }))
	m.HandleFunc("POST /api/providers/stremio/addons", s.installStremioAddon)
	m.HandleFunc("DELETE /api/providers/stremio/addons", h(s.uninstallStremioAddon))
	m.HandleFunc("PUT /api/providers/stremio/addons/enabled", h(thisComputer(s.enableStremioAddon)))
	m.HandleFunc("PUT /api/providers/stremio/addons/order", h(thisComputer(s.orderStremioAddons)))
	m.HandleFunc("POST /api/providers/stremio/refresh", h(thisComputer(s.refreshStremioAddons)))
	m.HandleFunc("GET /api/providers/stremio/catalog", h(s.stremioCatalog))
	m.HandleFunc("POST /api/providers/stremio/catalog/refresh", h(thisComputer(s.refreshStremioCatalog)))
	m.HandleFunc("POST /api/providers/stremio/import", h(s.importStremioAccount))

	m.HandleFunc("GET /api/providers/nuvio/repo", h(s.nuvioRepo))
	m.HandleFunc("POST /api/providers/nuvio/repos", h(s.addNuvioRepo))
	m.HandleFunc("DELETE /api/providers/nuvio/repos", h(s.removeNuvioRepo))
	m.HandleFunc("POST /api/providers/nuvio/install", h(s.installNuvio))
	m.HandleFunc("POST /api/providers/nuvio/install-all", h(s.installNuvioRepo))
}

// providerMedia is the movie or show of a request, and the episode asked
// for (a key; a movie's is 1).
func (s *Server) providerMedia(ctx context.Context, mediaID, episode int) (*catalog.Media, int, error) {
	if mediaID <= 0 {
		return nil, 0, badRequest("missing mediaId")
	}
	media, err := s.app.Platform.MediaLite(ctx, mediaID)
	if err != nil {
		return nil, 0, err
	}
	if !media.IsShow() {
		return media, catalog.MovieEpisode, nil
	}
	if _, e := catalog.SplitKey(episode); episode <= 0 || e <= 0 {
		return nil, 0, badRequest("missing episode (a key: season*1000 + episode)")
	}
	return media, episode, nil
}

// providerError makes a provider-side failure a 400 with its message
// (wrong provider, nothing enabled...), keeping cancellations as they are.
func providerError(err error) error {
	var ae *apiError
	if err == nil || errors.As(err, &ae) || errors.Is(err, context.Canceled) {
		return err
	}
	return badRequest(err.Error())
}

// providerStreams is GET /api/providers/streams?mediaId=&episode=&provider=:
// the streams of a movie or episode, best first, with the providers' errors
// and the subtitles of subtitle addons (those found a moment ago, if any).
func (s *Server) providerStreams(r *http.Request) (any, error) {
	return s.streams(r.Context(), queryInt(r, "mediaId"), queryInt(r, "episode"), r.URL.Query().Get("provider"), false)
}

// refreshProviderStreams is POST /api/providers/streams/refresh {mediaId,
// episode, provider}: the same, the providers asked again.
func (s *Server) refreshProviderStreams(r *http.Request) (any, error) {
	var body struct {
		MediaID  int    `json:"mediaId"`
		Episode  int    `json:"episode"`
		Provider string `json:"provider"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return s.streams(r.Context(), body.MediaID, body.Episode, body.Provider, true)
}

func (s *Server) streams(ctx context.Context, mediaID, episode int, provider string, refresh bool) (any, error) {
	media, episode, err := s.providerMedia(ctx, mediaID, episode)
	if err != nil {
		return nil, err
	}
	if provider == "all" {
		provider = ""
	}
	res, err := s.app.Stream.Streams(ctx, media, episode, provider, refresh)
	if err != nil {
		return nil, providerError(err)
	}
	if res.Streams == nil {
		res.Streams = []stream.Stream{}
	}
	return res, nil
}

// providerStreamsLive is GET /api/providers/streams/live (same query as
// /api/providers/streams): the same search, written as the providers
// answer, as NDJSON (one JSON object a line):
//
//	{"asking": [{provider, name, demoted}]}                 the providers asked
//	{"provider", "name", "streams", "error", "ms"}          one per provider, as it answers
//	{"done": true, "result": {...}}                         what /api/providers/streams answers
//
// The providers failing again and again are asked last ("demoted": their
// streams come last). Answers kept from a moment ago come all at once
// (with "cached": true). An error before the search started is answered as
// usual (JSON, 4xx); one after it ends the lines with
// {"done": true, "error"}.
func (s *Server) providerStreamsLive(w http.ResponseWriter, r *http.Request) {
	fail := func(err error) { h(func(*http.Request) (any, error) { return nil, err })(w, r) }
	// GET reads (the providers' answers kept a few minutes do); asking the
	// providers again is a POST {mediaId, episode, provider, refresh}, like
	// /api/providers/streams/refresh, so another site can't have it done.
	var body struct {
		MediaID  int    `json:"mediaId"`
		Episode  int    `json:"episode"`
		Provider string `json:"provider"`
		Refresh  bool   `json:"refresh"`
	}
	if r.Method == http.MethodPost {
		if err := decode(r, &body); err != nil {
			fail(err)
			return
		}
	} else {
		body.MediaID, body.Episode, body.Provider = queryInt(r, "mediaId"), queryInt(r, "episode"), r.URL.Query().Get("provider")
	}
	media, episode, err := s.providerMedia(r.Context(), body.MediaID, body.Episode)
	if err != nil {
		fail(err)
		return
	}
	provider := body.Provider
	if provider == "all" {
		provider = ""
	}
	var mu sync.Mutex
	started := false
	enc := json.NewEncoder(w)
	rc := http.NewResponseController(w)
	line := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		if !started {
			started = true
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Cache-Control", "no-store")
			// Proxies in front of Moviko mustn't hold the lines back.
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
		}
		_ = enc.Encode(v)
		_ = rc.Flush()
	}
	res, err := s.app.Stream.StreamsLive(r.Context(), media, episode, provider, body.Refresh, &stream.Progress{
		Asking: func(asked []stream.Asked) { line(map[string]any{"asking": asked}) },
		Answer: func(a stream.Answer) { line(a) },
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		mu.Lock()
		wrote := started
		mu.Unlock()
		if !wrote {
			fail(providerError(err))
			return
		}
		line(map[string]any{"done": true, "error": err.Error()})
		return
	}
	if res.Streams == nil {
		res.Streams = []stream.Stream{}
	}
	line(map[string]any{"done": true, "result": res})
}

// testTitle is what a provider's Test button looks for without a title
// given: Night of the Living Dead (1968), in the public domain, which the
// Internet Archive and many addons have.
var testTitle = providers.Request{
	MediaID: 10331, TMDBID: 10331, IMDbID: "tt0063350", Type: providers.TypeMovie,
	Title: "Night of the Living Dead", Year: 1968,
}

// testProvider is POST /api/providers/{id}/test ({mediaId, episode}
// optional): asks one enabled provider right away for a title (Night of the
// Living Dead by default) and records how it went in its health:
// {provider, name, title, streams, direct, error, ms, health}. Only on
// this computer: the Providers settings are its own.
func (s *Server) testProvider(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("providers can only be tested on this computer")
	}
	var body struct {
		MediaID int `json:"mediaId"`
		Episode int `json:"episode"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return nil, err
		}
	}
	id := r.PathValue("id")
	var p providers.Provider
	for _, x := range s.app.StreamProviders() {
		if x.ID() == id {
			p = x
		}
	}
	if p == nil {
		return nil, notFound(fmt.Sprintf("streaming provider %q is not installed or is disabled", id))
	}
	req, title := testTitle, "Night of the Living Dead (1968)"
	if body.MediaID > 0 {
		media, episode, err := s.providerMedia(r.Context(), body.MediaID, body.Episode)
		if err != nil {
			return nil, err
		}
		req, title = stream.RequestFor(media, episode), app.EpisodeTitle(media, episode)
	}
	if !p.Supports(req.Type) {
		what := "movies"
		if req.Type == providers.TypeTV {
			what = "shows"
		}
		return nil, badRequest(fmt.Sprintf("%s has no %s: test it with a title it has (from its Watch online)", p.Name(), what))
	}
	a := s.app.Stream.Test(r.Context(), p, req)
	direct := 0
	for _, st := range a.Streams {
		if st.Kind == stream.KindDirect {
			direct++
		}
	}
	return map[string]any{
		"provider": a.Provider, "name": a.Name, "title": title, "streams": len(a.Streams), "direct": direct,
		"error": a.Error, "ms": a.Ms, "health": s.app.Health.Get(id),
	}, nil
}

// testMediaServer is POST /api/providers/mediaservers/test {kind, url,
// token, userId}: connects to a Jellyfin, Emby or Plex server as Settings ›
// Providers has it (saved or not) → {name, version, userId, userName,
// libraries}. Only on this computer: it's sent the server's token. Without
// a token, the saved server's at that address is used.
func (s *Server) testMediaServer(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("media servers can only be tested on this computer")
	}
	var body config.MediaServer
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	c := mediaserver.Config{Kind: body.Kind, Name: body.Name, URL: strings.TrimSpace(body.URL), Token: strings.TrimSpace(body.Token), UserID: strings.TrimSpace(body.UserID)}
	if c.Token == "" {
		for _, saved := range s.app.Settings.Get().Providers.MediaServers {
			if saved.Kind == c.Kind && strings.TrimRight(saved.URL, "/") == strings.TrimRight(c.URL, "/") {
				c.Token = saved.Token
			}
		}
	}
	info, err := mediaserver.Check(r.Context(), c)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	return info, nil
}

type providerPlayBody struct {
	MediaID int `json:"mediaId"`
	Episode int `json:"episode"`
	// StreamID is a stream of GET /api/providers/streams; Stream the stream
	// itself (when its id is gone: links found a while ago). With neither,
	// the best direct link of Provider ("" for all) plays.
	StreamID string            `json:"streamId"`
	Stream   *providers.Stream `json:"stream"`
	Provider string            `json:"provider"`
	// Player is "builtin", "mpv" or "" (the default player).
	Player string   `json:"player"`
	Start  *float64 `json:"start"`
	// Refresh asks the providers again rather than using the links found a
	// moment ago: the player does when none of them could be read (links
	// expire, often within minutes).
	Refresh bool `json:"refresh"`
	// Download: a torrent goes to the torrent client, into the library,
	// instead of playing as it downloads.
	Download bool `json:"download"`
}

// providerPlay is POST /api/providers/play. A direct link opens in mpv or
// gives the in-app player its sources (the stream picked, then the other
// direct links as fallbacks), a torrent goes to the torrent client (into the
// library), a YouTube video or a web page comes back to be opened:
// {action: "builtin" | "mpv" | "torrent" | "external", ...}.
func (s *Server) providerPlay(r *http.Request) (any, error) {
	var body providerPlayBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	media, episode, err := s.providerMedia(r.Context(), body.MediaID, body.Episode)
	if err != nil {
		return nil, err
	}
	st, res, err := s.pickStream(r.Context(), media, episode, body)
	if err != nil {
		return nil, providerError(err)
	}
	switch st.Kind {
	case stream.KindTorrent:
		if body.Download {
			return s.sendStreamTorrent(r.Context(), media, episode, st)
		}
		return s.streamTorrent(r, media, episode, st, body)
	case stream.KindYouTube:
		return map[string]any{"action": "external", "url": "https://www.youtube.com/watch?v=" + url.QueryEscape(st.YouTubeID)}, nil
	case stream.KindExternal:
		u, err := url.Parse(st.ExternalURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, badRequest("this source has no link to open")
		}
		return map[string]any{"action": "external", "url": st.ExternalURL}, nil
	}

	cfg := s.app.Settings.Get()
	playerName := body.Player
	if playerName == "" {
		playerName = cfg.Playback.DefaultPlayer
	}
	// LAN devices can't see this computer's mpv window.
	if !isTrusted(r) {
		playerName = "builtin"
	}
	var addonSubs []providers.Subtitle
	if res != nil {
		addonSubs = res.Subtitles
	}
	// The session records the provider and the episode key: auto play
	// next asks the same provider for the next episode (app/playback.go).
	t := s.app.PlayTitles(r.Context(), media.ID, episode, media.PreferredTitle())
	if playerName == "mpv" {
		headers, referrer := stream.SplitReferrer(stream.WithCredentials(st.URL, st.Headers))
		// mpv loads every subtitle file it's given when it starts: only a
		// few, in the preferred languages.
		subs := append(append([]providers.Subtitle(nil), st.Subtitles...), stream.SortSubtitles(addonSubs, cfg.Playback.PreferredSubLang, 4)...)
		var files []string
		for _, sub := range subs {
			files = append(files, sub.URL)
		}
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: media.ID, Episode: episode, Title: t.Window, MediaTitle: t.Media, EpisodeTitle: t.Episode,
			Source: "stream", Target: stream.CleanLink(st.URL), Headers: headers, Referrer: referrer, SubFiles: files, Start: body.Start,
			Provider: st.Provider, Quality: st.Quality, Release: stream.ReleaseName(st.Stream),
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"action": "mpv", "player": "mpv", "session": sess, "provider": st.Provider}, nil
	}

	// The in-app player: the stream picked first, then the other direct
	// links (its provider's first) to fall back on.
	sources := []map[string]any{playerSource(st, addonSubs, cfg)}
	if res != nil {
		var same, others []stream.Stream
		for _, x := range res.Direct() {
			switch {
			case x.ID == st.ID:
			case x.Provider == st.Provider:
				same = append(same, x)
			default:
				others = append(others, x)
			}
		}
		for _, x := range append(same, others...) {
			if len(sources) >= 10 {
				break
			}
			sources = append(sources, playerSource(x, addonSubs, cfg))
		}
	}
	// A new viewing, which may update progress again (rewatch).
	s.app.Player.ResetBuiltinProgress(media.ID, episode, 0)
	resume := s.app.History.ResumePosition(media.ID, episode)
	if body.Start != nil {
		resume = *body.Start
	}
	return map[string]any{
		"action": "builtin", "player": "builtin", "sources": sources, "title": t.Window, "mediaTitle": t.Media, "episodeTitle": t.Episode, "resumeAt": resume,
		"tracks": s.app.Player.Tracks.Get(media.ID), "mediaId": media.ID, "episode": episode, "key": episode, "provider": st.Provider,
	}, nil
}

// pickStream finds the stream a play request is about. Without one picked,
// the pick is automatic (stream.Service.BestFor): providers failing again
// and again are left out, and for a show the version that played last
// comes first.
func (s *Server) pickStream(ctx context.Context, media *catalog.Media, episode int, body providerPlayBody) (stream.Stream, *stream.Result, error) {
	if body.Refresh {
		return s.app.Stream.BestFor(ctx, media, episode, stream.Pick{Provider: body.Provider, Refresh: true})
	}
	if body.StreamID != "" {
		st, res, err := s.app.Stream.Find(ctx, media, episode, body.StreamID)
		if err == nil || body.Stream == nil {
			return st, res, err
		}
	}
	if body.Stream != nil {
		st := *body.Stream
		if !providers.Playable(st) {
			return stream.Stream{}, nil, badRequest("this source has nothing to play")
		}
		if st.InfoHash == "" && st.Magnet == "" {
			st.FileIdx = -1
		}
		return stream.Wrap(st), nil, nil
	}
	return s.app.Stream.BestFor(ctx, media, episode, stream.Pick{Provider: body.Provider})
}

// playerSource is a direct link as the in-app player takes it: through the
// proxy (headers, CORS, HLS playlists rewritten), subtitles converted to
// WebVTT. Those of subtitle addons come after the stream's own, the
// preferred languages first.
func playerSource(st stream.Stream, addonSubs []providers.Subtitle, cfg config.Settings) map[string]any {
	headers := map[string]string{}
	for k, v := range st.Headers {
		headers[k] = v
	}
	_, referrer := stream.SplitReferrer(st.Headers)
	subs := []map[string]any{}
	all := append(append([]providers.Subtitle(nil), st.Subtitles...), stream.SortSubtitles(addonSubs, cfg.Playback.PreferredSubLang, 30)...)
	for i, sub := range all {
		own := i < len(st.Subtitles)
		sp := subtitleSource(sub, headers)
		sp["isDefault"], sp["addon"] = i == 0 && own, !own
		subs = append(subs, sp)
	}
	typ := st.Type
	if typ == providers.StreamUnknown {
		typ = "unknown"
	}
	src := map[string]any{
		"id": st.ID, "url": stream.ProxyURL(st.URL, headers), "originalUrl": st.URL, "type": typ, "quality": st.Quality,
		"label": firstLine(st.Title), "release": stream.ReleaseName(st.Stream), "server": st.ProviderName, "provider": st.Provider, "subtitles": subs,
		"headers": headers, "referrer": referrer, "notWebReady": st.NotWebReady, "filename": st.Filename,
	}
	// What the player converts with ffmpeg when the browser can't play the
	// link as it is (Matroska, HEVC, AC-3/E-AC-3/DTS audio...): the link
	// named for the conversion endpoints (stream/remote.go). ffmpeg reads
	// an HLS stream through the relay, which rewrites its playlists
	// (stream/relayhls.go); one the browser plays goes on playing as it is.
	if isHLS(typ, st.URL) {
		src["remote"] = stream.RegisterRemoteHLS(st.URL, headers)
	} else {
		src["remote"] = stream.RegisterRemote(st.URL, headers)
	}
	return src
}

// subtitleSource is a subtitle file as the in-app player takes it: WebVTT
// through the proxy (fetched with headers, converted from SRT or ASS).
func subtitleSource(sub providers.Subtitle, headers map[string]string) map[string]any {
	label := sub.Label
	if label == "" {
		label = sub.Language
	}
	return map[string]any{
		"url": stream.ProxyURL(sub.URL, headers) + "&fmt=vtt", "language": sub.Language, "label": label, "original": sub.URL,
	}
}

// providerSubtitles is GET /api/providers/subtitles?mediaId=&episode=
// &remote=&filename=: the subtitles of the subtitle addons for the online
// stream the in-app player plays (remote: its "remote:" path), found for
// that very file when its size and hash can be read from it (two byte
// ranges, like the stream), the preferred languages first, as the player
// takes them.
func (s *Server) providerSubtitles(r *http.Request) (any, error) {
	q := r.URL.Query()
	media, episode, err := s.providerMedia(r.Context(), queryInt(r, "mediaId"), queryInt(r, "episode"))
	if err != nil {
		return nil, err
	}
	remote := q.Get("remote")
	if !stream.IsRemoteInput(remote) {
		return nil, badRequest("missing remote (the stream's remote: path)")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	subs, err := s.app.Stream.VideoSubtitles(ctx, media, episode, remote, q.Get("filename"))
	if errors.Is(err, stream.ErrRemoteGone) {
		return nil, notFound(err.Error())
	}
	if err != nil {
		return nil, providerError(err)
	}
	out := []map[string]any{}
	for _, sub := range stream.SortSubtitles(subs, s.app.Settings.Get().Playback.PreferredSubLang, 30) {
		sp := subtitleSource(sub, nil)
		sp["addon"] = true
		out = append(out, sp)
	}
	return out, nil
}

// isHLS reports a link to an HLS playlist, by its type or its address (as
// the player tells them).
func isHLS(typ, link string) bool {
	return typ == providers.StreamM3U8 || strings.Contains(strings.ToLower(link), "m3u8")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// sendStreamTorrent hands a torrent stream to the torrent client, saved
// into the library (the movie's folder, the show's season folder).
func (s *Server) sendStreamTorrent(ctx context.Context, media *catalog.Media, episode int, st stream.Stream) (any, error) {
	magnet := st.Magnet
	if magnet == "" {
		name := firstLine(st.Title)
		if st.Filename != "" {
			name = st.Filename
		}
		magnet = providers.MagnetURI(st.InfoHash, name, nil)
	}
	if magnet == "" {
		return nil, badRequest("this torrent has no info hash")
	}
	season := -1
	if media.IsShow() {
		season, _ = catalog.SplitKey(episode)
	}
	save := s.app.Torrents.SavePath(media, season, s.mediaFiles(media.ID))
	if err := s.app.Torrents.Add(ctx, []string{magnet}, save); err != nil {
		return nil, err
	}
	what := media.PreferredTitle()
	if media.IsShow() {
		what += " " + catalog.FormatKey(episode)
	}
	s.app.Hub.Success("Sent " + what + " to the torrent client")
	return map[string]any{"action": "torrent", "savePath": save, "magnet": magnet}, nil
}

// providerDownload is POST /api/providers/download {mediaId, episodes,
// provider, quality}: queues downloads, each resolved when its turn comes
// (links expire) from the same provider in the closest quality.
func (s *Server) providerDownload(r *http.Request) (any, error) {
	var body struct {
		MediaID  int    `json:"mediaId"`
		Episodes []int  `json:"episodes"`
		Provider string `json:"provider"`
		Quality  string `json:"quality"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	sd := share.StreamDownload{MediaID: body.MediaID, Provider: body.Provider, Quality: body.Quality}
	for _, k := range body.Episodes {
		sd.Episodes = append(sd.Episodes, float64(k))
	}
	// Downloads go to another Moviko (a TV's, say: to the computer's).
	if out, sent, err := s.sendDownload(r.Context(), share.DownloadStream, sd); sent {
		return out, err
	}
	items, err := s.queueStreamDownload(r.Context(), sd)
	if err != nil {
		return nil, err
	}
	if len(items) == 1 {
		if media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID); err == nil {
			s.app.Hub.Info("Queued " + app.EpisodeTitle(media, items[0].Episode))
		}
	} else {
		s.app.Hub.Info(fmt.Sprintf("Queued %d downloads", len(items)))
	}
	return items, nil
}

// providerSearch is GET /api/providers/search?provider=&q=&mediaId=: an
// extension's titles, to pick the right match by hand.
func (s *Server) providerSearch(r *http.Request) (any, error) {
	q := r.URL.Query()
	m, err := s.app.Stream.Matcher(q.Get("provider"))
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if strings.TrimSpace(q.Get("q")) == "" {
		return nil, badRequest("type something to search")
	}
	res, err := m.SearchTitles(r.Context(), q.Get("q"), queryInt(r, "mediaId"))
	if err != nil {
		return nil, providerError(err)
	}
	if res == nil {
		res = []stream.SearchResult{}
	}
	return res, nil
}

// providerMapping is POST /api/providers/mapping {provider, mediaId, id,
// title}: an extension uses that title for the movie or show from now on.
func (s *Server) providerMapping(r *http.Request) (any, error) {
	var body struct {
		Provider string `json:"provider"`
		MediaID  int    `json:"mediaId"`
		ID       string `json:"id"`
		Title    string `json:"title"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.MediaID <= 0 {
		return nil, badRequest("missing mediaId")
	}
	return nil, providerError(s.app.Stream.SetMatch(body.Provider, body.MediaID, body.ID, body.Title))
}

// ---------------------------------------------------------------------------
// Stremio addons

// installStremioAddon is POST /api/providers/stremio/addons {url,
// allowLocal, allowAdult}. Addons that need a confirmation or can't be
// installed answer 409 {error, code: "local" | "adult" | "configure" |
// "unsupported", configureUrl}.
func (s *Server) installStremioAddon(w http.ResponseWriter, r *http.Request) {
	out, err := s.doInstallStremioAddon(r)
	var ce *conflictError
	if errors.As(err, &ce) {
		body := map[string]any{"error": ce.msg, "code": ce.code}
		if ce.configureURL != "" {
			body["configureUrl"] = ce.configureURL
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	h(func(*http.Request) (any, error) { return out, err })(w, r)
}

func (s *Server) doInstallStremioAddon(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		URL string `json:"url"`
		stremio.InstallOptions
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	a, err := s.app.Stremio.Install(r.Context(), body.URL, body.InstallOptions)
	var cfgErr *stremio.ConfigurationError
	switch {
	case err == nil:
		s.app.Hub.Success("Installed " + a.Manifest.Name)
		return a, nil
	case errors.Is(err, stremio.ErrLocalAddon):
		return nil, conflict(err, "local", "")
	case errors.Is(err, stremio.ErrAdultAddon):
		return nil, conflict(err, "adult", "")
	case errors.As(err, &cfgErr):
		return nil, conflict(err, "configure", cfgErr.ConfigureURL)
	case errors.Is(err, stremio.ErrUnsupportedAddon):
		return nil, conflict(err, "unsupported", "")
	}
	return nil, badRequest(err.Error())
}

// conflictError is a 409 with a code telling the UI what to ask.
type conflictError struct {
	msg, code, configureURL string
}

func (e *conflictError) Error() string { return e.msg }

func conflict(err error, code, configureURL string) error {
	return &conflictError{msg: err.Error(), code: code, configureURL: configureURL}
}

func (s *Server) uninstallStremioAddon(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	key := r.URL.Query().Get("key")
	if err := s.app.Stremio.Uninstall(key); err != nil {
		if errors.Is(err, stremio.ErrNotInstalled) {
			return nil, notFound(err.Error())
		}
		return nil, err
	}
	return nil, nil
}

func (s *Server) enableStremioAddon(r *http.Request) (any, error) {
	var body struct {
		Key     string `json:"key"`
		Enabled bool   `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if err := s.app.Stremio.SetEnabled(body.Key, body.Enabled); err != nil {
		if errors.Is(err, stremio.ErrNotInstalled) {
			return nil, notFound(err.Error())
		}
		return nil, err
	}
	return s.app.Stremio.List(), nil
}

func (s *Server) orderStremioAddons(r *http.Request) (any, error) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if err := s.app.Stremio.Reorder(body.Keys); err != nil {
		return nil, err
	}
	return s.app.Stremio.List(), nil
}

// refreshStremioAddons downloads the addons' manifests again; the ones that
// couldn't be reached keep theirs: {addons, errors: {transportUrl: error}}.
func (s *Server) refreshStremioAddons(r *http.Request) (any, error) {
	errs := map[string]string{}
	for u, err := range s.app.Stremio.Refresh(r.Context()) {
		errs[u] = err.Error()
	}
	return map[string]any{"addons": s.app.Stremio.List(), "errors": errs}, nil
}

// stremioCatalog is GET /api/providers/stremio/catalog?url=: an addon
// collection (the first of Settings › Providers by default), the installed
// addons marked: {url, catalogs, addons}. Devices on the LAN read the
// collections of the settings only.
func (s *Server) stremioCatalog(r *http.Request) (any, error) {
	return s.collection(r, r.URL.Query().Get("url"), false)
}

// refreshStremioCatalog is POST /api/providers/stremio/catalog/refresh
// {url}: the same, the collection downloaded again.
func (s *Server) refreshStremioCatalog(r *http.Request) (any, error) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return s.collection(r, body.URL, true)
}

func (s *Server) collection(r *http.Request, u string, refresh bool) (any, error) {
	catalogs := s.app.Settings.Get().Providers.StremioCatalogs
	u = strings.TrimSpace(u)
	if u == "" && len(catalogs) > 0 {
		u = catalogs[0]
	}
	if u == "" {
		u = config.DefaultStremioCatalog
	}
	if !lanMayFetch(r, u, append([]string{config.DefaultStremioCatalog}, catalogs...)...) {
		return nil, forbidden("other addon collections can only be opened on this computer")
	}
	list, err := s.app.StremioCollection(r.Context(), u, refresh)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if list == nil {
		list = []stremio.Addon{}
	}
	return map[string]any{"url": u, "catalogs": catalogs, "addons": list}, nil
}

// importStremioAccount is POST /api/providers/stremio/import {email,
// password}: signs in to a Stremio account and installs the addons on it
// Moviko can use → {added, skipped: [{id, name, transportUrl, reason}]}.
// Only on this computer. The password and the session's key are used for
// the sign-in and the addon list, then forgotten: neither is saved nor
// logged.
func (s *Server) importStremioAccount(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	addons, err := stremio.AccountAddons(r.Context(), stremio.APIURL(), body.Email, body.Password)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	res := s.app.Stremio.Import(r.Context(), addons)
	if n := len(res.Added); n > 0 {
		s.app.Hub.Success(fmt.Sprintf("Imported %d addon(s) from Stremio", n))
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Nuvio repositories

// nuvioRepo is GET /api/providers/nuvio/repo?url=: a repository and its
// providers. Devices on the LAN read the repositories of the settings only.
func (s *Server) nuvioRepo(r *http.Request) (any, error) {
	u := r.URL.Query().Get("url")
	if !lanMayFetch(r, u, s.app.Settings.Get().Providers.NuvioRepos...) {
		return nil, forbidden("other repositories can only be opened on this computer")
	}
	repo, err := s.app.Extensions.NuvioRepo(r.Context(), u)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	return repo, nil
}

// addNuvioRepo is POST /api/providers/nuvio/repos {url}: checks the
// repository and keeps it in Settings › Providers.
func (s *Server) addNuvioRepo(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	repo, err := s.app.Extensions.NuvioRepo(r.Context(), body.URL)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := s.keepNuvioRepo(repo.URL); err != nil {
		return nil, err
	}
	return repo, nil
}

func (s *Server) removeNuvioRepo(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	u := strings.TrimSpace(r.URL.Query().Get("url"))
	cfg := s.app.Settings.Get()
	repos := []string{}
	for _, x := range cfg.Providers.NuvioRepos {
		if x != u {
			repos = append(repos, x)
		}
	}
	cfg.Providers.NuvioRepos = repos
	_, err := s.app.Settings.Save(cfg)
	return nil, err
}

// keepNuvioRepo adds a repository to the settings (once).
func (s *Server) keepNuvioRepo(u string) error {
	cfg := s.app.Settings.Get()
	for _, x := range cfg.Providers.NuvioRepos {
		if x == u {
			return nil
		}
	}
	// A copy: the settings' slices are shared with the ones in memory.
	cfg.Providers.NuvioRepos = append(append([]string(nil), cfg.Providers.NuvioRepos...), u)
	_, err := s.app.Settings.Save(cfg)
	return err
}

func (s *Server) installNuvio(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		Repo string `json:"repo"`
		ID   string `json:"id"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	info, err := s.app.Extensions.InstallNuvio(r.Context(), body.Repo, body.ID)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := s.keepNuvioRepo(strings.TrimSpace(body.Repo)); err != nil {
		return nil, err
	}
	s.app.Stream.Invalidate()
	s.app.Hub.Success("Installed " + info.Manifest.Name)
	return info, nil
}

// installNuvioRepo installs every provider a repository enables by default
// (and updates the installed ones): {installed, errors}.
func (s *Server) installNuvioRepo(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		Repo string `json:"repo"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	installed, err := s.app.Extensions.InstallNuvioRepo(r.Context(), body.Repo)
	if installed == nil {
		installed = []extensions.Info{}
	}
	if err != nil && len(installed) == 0 {
		return nil, badRequest(err.Error())
	}
	if err := s.keepNuvioRepo(strings.TrimSpace(body.Repo)); err != nil {
		return nil, err
	}
	s.app.Stream.Invalidate()
	errs := []string{}
	if err != nil {
		errs = strings.Split(err.Error(), "\n")
	}
	s.app.Hub.Success(fmt.Sprintf("Installed %d provider(s)", len(installed)))
	return map[string]any{"installed": installed, "errors": errs}, nil
}

// uninstallExtension is DELETE /api/extensions/{id} (Nuvio providers too).
func (s *Server) uninstallExtension(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	if err := s.app.Extensions.Uninstall(r.PathValue("id")); err != nil {
		return nil, err
	}
	s.app.Stream.Invalidate()
	return nil, nil
}
