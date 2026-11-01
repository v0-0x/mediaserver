package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/history"
	"github.com/v0-0x/mediaserver/anime/internal/player"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"github.com/v0-0x/mediaserver/anime/internal/stream"
)

// Library sharing with other Aniko apps on the home network (package share).
//
// As a host, this Aniko answers the others under /api/peer/: who it is, its
// shared files, and playing them. Those requests are checked by servePeer,
// apart from the browsers': no login or Web UI needed, but a valid token
// from an Aniko, and only from the home network. Playing a shared file on the
// other Aniko updates that Aniko's list and history only: the host just sends
// the video.
//
// As a guest, its own player plays the files shared with it (kumo:// paths)
// through the usual /api/local/ endpoints, which forward them to the host.

type peerKey struct{}

// servePeer checks a request from another Aniko.
func (s *Server) servePeer(w http.ResponseWriter, r *http.Request, next http.Handler) {
	ip := clientIP(r)
	if !s.app.Settings.Get().Sharing.Enabled || ip == nil || !(ip.IsLoopback() || isLANIP(ip)) {
		http.Error(w, "library sharing is off", http.StatusForbidden)
		return
	}
	if !hostAllowed(r.Host) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	// Aniko apps don't send it; websites in a browser do.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if r.URL.Path == "/api/peer/whoami" {
		next.ServeHTTP(w, r)
		return
	}
	c, err := s.app.Share.Verify(w, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not an Aniko app this one knows: " + err.Error()})
		return
	}
	// Another device: never this computer (isTrusted).
	ctx := context.WithValue(r.Context(), ctxKey{}, clientLAN)
	r = r.WithContext(context.WithValue(ctx, peerKey{}, c))
	// Its parental controls, set here: nothing blocked is shared with it.
	if c.Rules.Active() {
		s.parentalServe(c.Rules, w, r, next)
		return
	}
	next.ServeHTTP(w, r)
}

func peerOf(r *http.Request) share.Caller {
	c, _ := r.Context().Value(peerKey{}).(share.Caller)
	return c
}

// peerAllowed reports an Aniko this one shares its library with.
func peerAllowed(r *http.Request) bool { return peerOf(r).Allowed }

// shared wraps the endpoints only the Anikos this one shares with may use.
func shared(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !peerAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "this library isn't shared with you"})
			return
		}
		fn(w, r)
	}
}

// sharedFile wraps the endpoints about a file (?path=): one this Aniko
// shares, not any of its library's (an ignored one, say).
func (s *Server) sharedFile(fn http.HandlerFunc) http.HandlerFunc {
	return shared(func(w http.ResponseWriter, r *http.Request) {
		if !s.app.Share.Shares(r.URL.Query().Get("path")) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "that file isn't shared"})
			return
		}
		fn(w, r)
	})
}

func (s *Server) peerRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/peer/whoami", h(func(r *http.Request) (any, error) { return s.app.Share.Whoami(), nil }))
	m.HandleFunc("GET /api/peer/hello", h(func(r *http.Request) (any, error) { return s.app.Share.Hello(peerOf(r)), nil }))
	m.HandleFunc("GET /api/peer/files", shared(h(func(r *http.Request) (any, error) { return s.app.Share.SharedFiles() })))
	// The watch history, for an Aniko it's paired with (it shares its library
	// with it, or that one with it) on the same AniList account: Continue
	// watching goes on there where it stopped here.
	paired := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !peerOf(r).Paired {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "this Aniko isn't paired with yours"})
				return
			}
			fn(w, r)
		}
	}
	m.HandleFunc("GET /api/peer/history", paired(h(func(r *http.Request) (any, error) {
		if !peerOf(r).SameUser {
			return nil, forbidden("not logged into the same AniList account")
		}
		q := r.URL.Query()
		c := peerOf(r)
		switch {
		case q.Get("latest") == "1":
			return s.app.Share.HistoryLatest(c), nil
		case q.Has("after"):
			after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
			return s.app.Share.HistoryAfter(c, after), nil
		}
		// An older Aniko asks by time.
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		return s.app.Share.HistorySince(c, since), nil
	})))
	// And where it's watching (this Aniko's files, or any), as it happens.
	m.HandleFunc("POST /api/peer/history", paired(h(func(r *http.Request) (any, error) {
		if !peerOf(r).SameUser {
			return nil, forbidden("not logged into the same AniList account")
		}
		var entries []history.Entry
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&entries); err != nil {
			return nil, badRequest(err.Error())
		}
		s.app.Share.TakeHistory(peerOf(r), entries)
		return nil, nil
	})))
	// The settings a hub sends on, changed on another device on the same
	// account (hub.go): taken when newer and kept in step.
	m.HandleFunc("POST /api/peer/prefs", paired(h(func(r *http.Request) (any, error) {
		if !peerOf(r).SameUser {
			return nil, forbidden("not logged into the same AniList account")
		}
		var p anikohub.Prefs
		if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&p); err != nil {
			return nil, badRequest(err.Error())
		}
		s.app.Share.TakePrefs(peerOf(r).ID, p)
		return nil, nil
	})))
	s.hubPeerRoutes()
	// This Aniko's AniList login, for an Aniko it shares it with: sealed, only
	// that one can read it.
	m.HandleFunc("GET /api/peer/anilist", shared(h(func(r *http.Request) (any, error) {
		sa, err := s.app.Share.SealAccount(peerOf(r))
		if err != nil {
			return nil, forbidden(err.Error())
		}
		return sa, nil
	})))
	// ani-cli, for an Aniko where it doesn't run (Android): see share/anicli.go.
	m.HandleFunc("POST /api/peer/anicli/{op}", shared(h(func(r *http.Request) (any, error) {
		var req share.AniCliRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		a, err := share.AnswerAniCli(r.Context(), s.app.AniCli, r.PathValue("op"), req)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return a, nil
	})))
	// The torrent client, for an Aniko allowed to download onto this one: its
	// downloads go here, its torrent page shows this one's.
	torrents := func(fn http.HandlerFunc) http.HandlerFunc {
		return shared(func(w http.ResponseWriter, r *http.Request) {
			if !peerOf(r).Downloads {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "this Aniko doesn't take downloads from yours"})
				return
			}
			fn(w, r)
		})
	}
	m.HandleFunc("GET /api/peer/torrents/status", torrents(h(func(r *http.Request) (any, error) { return s.app.Torrents.Status(r.Context()), nil })))
	m.HandleFunc("GET /api/peer/torrents/list", torrents(h(s.torrentList)))
	m.HandleFunc("POST /api/peer/torrents/action", torrents(h(s.torrentAction)))
	m.HandleFunc("POST /api/peer/torrents/start", torrents(h(func(r *http.Request) (any, error) { return nil, s.app.Torrents.StartClient(r.Context()) })))
	// Downloads from an Aniko allowed to download onto this one, and the
	// list of them (its Downloads page shows this one's).
	m.HandleFunc("POST /api/peer/downloads/{kind}", shared(h(s.peerDownload)))
	m.HandleFunc("GET /api/peer/downloads/list", torrents(h(func(r *http.Request) (any, error) { return s.app.Downloads.List(), nil })))
	m.HandleFunc("POST /api/peer/downloads/{id}/{op}", torrents(h(func(r *http.Request) (any, error) {
		switch id := r.PathValue("id"); r.PathValue("op") {
		case "cancel":
			s.app.Downloads.Cancel(id)
			return nil, nil
		case "retry":
			return nil, s.app.Downloads.Retry(id)
		}
		return nil, notFound("no such thing to do")
	})))
	m.HandleFunc("GET /api/peer/local/probe", s.sharedFile(h(func(r *http.Request) (any, error) {
		return s.app.Local.Probe(r.Context(), r.URL.Query().Get("path"))
	})))
	m.HandleFunc("GET /api/peer/local/file", s.sharedFile(s.localFile))
	m.HandleFunc("GET /api/peer/local/transcode", s.sharedFile(s.localTranscode))
	m.HandleFunc("GET /api/peer/local/seekpoint", s.sharedFile(h(s.localSeekPoint)))
	m.HandleFunc("GET /api/peer/local/subtitle", s.sharedFile(s.localSubtitle))
	m.HandleFunc("GET /api/peer/local/thumb", s.sharedFile(s.localThumb))
	m.HandleFunc("POST /api/peer/local/hls", shared(h(func(r *http.Request) (any, error) {
		var req stream.HLSRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if !s.app.Share.Shares(req.Path) {
			return nil, notFound("that file isn't shared")
		}
		sess, err := s.app.HLS.Start(r.Context(), req)
		if err == nil && s.app.Streams != nil {
			s.app.Streams.Label(sess.ID, req.Path, sess.Method)
		}
		return sess, err
	})))
	m.HandleFunc("GET /api/peer/local/hls/{id}/{name}", shared(func(w http.ResponseWriter, r *http.Request) {
		defer s.awakeFor(r)()
		if s.app.Streams != nil {
			w = s.app.Streams.Keyed(r.PathValue("id"), peerOf(r).Name).Writer(w)
		}
		s.app.HLS.Serve(w, r, r.PathValue("id"), r.PathValue("name"))
	}))
	m.HandleFunc("DELETE /api/peer/local/hls/{id}", shared(h(func(r *http.Request) (any, error) {
		s.app.HLS.Stop(r.PathValue("id"))
		return nil, nil
	})))

	// An Aniko Hub this one uses: give it this Aniko's AniList login, or
	// everything (settings, login, watch history: import).
	m.HandleFunc("POST /api/sharing/hubs/{id}/account", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this device can give its AniList login away")
		}
		if err := s.app.Share.GiveAccount(r.Context(), r.PathValue("id")); err != nil {
			return nil, badRequest(err.Error())
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/sharing/hubs/{id}/send", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this device can send its settings away")
		}
		msg, err := s.app.Share.SendToHub(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return map[string]any{"message": msg}, nil
	}))

	// This Aniko's settings and the libraries shared with it.
	// Opened: what the others share is asked for again.
	m.HandleFunc("GET /api/sharing", h(func(r *http.Request) (any, error) {
		s.app.Share.Refresh()
		return s.app.Share.Status(), nil
	}))
	m.HandleFunc("GET /api/sharing/libraries", h(func(r *http.Request) (any, error) {
		s.app.Share.Refresh()
		return s.app.Share.Libraries(), nil
	}))
	m.HandleFunc("POST /api/sharing/peers/{id}", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer decides who its library is shared with")
		}
		var body share.Grants
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if err := s.app.Share.SetGrants(r.PathValue("id"), body); err != nil {
			return nil, notFound(err.Error())
		}
		return s.app.Share.Status(), nil
	}))
	// The app opened again (a TV's, brought back from its home screen):
	// Continue watching synced with the paired Anikos, if that's on (and not
	// done a moment ago).
	m.HandleFunc("POST /api/sharing/autosync", h(func(r *http.Request) (any, error) {
		s.app.Share.AutoSyncAll()
		return nil, nil
	}))
	// Wake a computer that shares its library with this one (asleep), by
	// its ID or a file of its.
	m.HandleFunc("POST /api/sharing/libraries/{id}/wake", h(func(r *http.Request) (any, error) {
		if err := s.app.Share.Wake(r.PathValue("id")); err != nil {
			return nil, badRequest(err.Error())
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/sharing/wake", h(func(r *http.Request) (any, error) {
		var body struct {
			Path string `json:"path"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if err := s.app.Share.WakeFor(body.Path); err != nil {
			return nil, badRequest(err.Error())
		}
		return nil, nil
	}))
	// The AniList account a host shares with this Aniko: use it, or stop.
	m.HandleFunc("POST /api/sharing/libraries/{id}/account", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this device can change its AniList account")
		}
		var body struct {
			Use bool `json:"use"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if body.Use {
			if err := s.app.Share.UseHostAccount(r.Context(), r.PathValue("id")); err != nil {
				return nil, badRequest(err.Error())
			}
		} else if s.app.Share.AccountHost() == r.PathValue("id") {
			s.app.Platform.Logout()
			s.app.Share.LoggedOut()
			s.app.AccountChanged()
		}
		return s.app.Share.Status(), nil
	}))
	m.HandleFunc("DELETE /api/sharing/peers/{id}", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can change library sharing")
		}
		s.app.Share.Forget(r.PathValue("id"))
		return s.app.Share.Status(), nil
	}))
	// Sync, from Home: the watch history with the Anikos this one is paired
	// with, now, and the AniList list (Continue watching from both).
	m.HandleFunc("POST /api/sharing/sync", h(func(r *http.Request) (any, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		res, err := s.app.Share.SyncNow(ctx)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		_, aerr := s.app.Platform.Collection(ctx, "ANIME", true)
		s.app.Hub.Publish("history-updated", nil)
		out := map[string]any{"devices": res.Devices, "unreachable": res.Unreachable, "skipped": res.Skipped, "updated": res.Updated, "anilist": aerr == nil}
		if aerr != nil {
			out["anilistError"] = aerr.Error()
		}
		return out, nil
	}))
	m.HandleFunc("POST /api/sharing/connect", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can change library sharing")
		}
		var body struct {
			Address string `json:"address"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if !s.app.Settings.Get().Sharing.Enabled {
			return nil, badRequest("turn on library sharing first")
		}
		v, err := s.app.Share.Connect(r.Context(), body.Address)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return v, nil
	}))
}

// peerDownload downloads what an Aniko allowed to download onto this one asks
// for, into this one's library (which it shares back).
// What an Aniko may download onto this one in one go.
const (
	maxPeerEpisodes = 200
	maxPeerTorrents = 50
)

// hubDownloaded notes on a hub's dashboard (Activity) a device's download.
func (s *Server) hubDownloaded(c share.Caller, mediaID int, text string) {
	if s.app.HubStore == nil {
		return
	}
	s.app.HubStore.Log(anikohub.Event{Kind: "download", Device: c.Name, User: c.User, Text: text, MediaID: mediaID})
	s.app.Hub.Publish("hub-updated", nil)
}

func (s *Server) peerDownload(r *http.Request) (any, error) {
	c := peerOf(r)
	if !c.Downloads {
		return nil, forbidden("this Aniko doesn't take downloads from yours")
	}
	from := c.Name
	if from == "" {
		from = "Another Aniko"
	}
	switch r.PathValue("kind") {
	case share.DownloadStream:
		var body share.StreamDownload
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		// A season at a time, not the whole of One Piece by mistake.
		if len(body.Episodes) > maxPeerEpisodes {
			return nil, badRequest(fmt.Sprintf("at most %d episodes at once", maxPeerEpisodes))
		}
		items, err := s.queueStreamDownload(r.Context(), body)
		if err != nil {
			return nil, err
		}
		s.app.Hub.Info(fmt.Sprintf("%s is downloading %d episode(s) here", from, len(items)))
		s.hubDownloaded(c, body.MediaID, fmt.Sprintf("is downloading %d episode(s) onto the hub:", len(items)))
		return map[string]int{"queued": len(items)}, nil
	case share.DownloadTorrent:
		var body share.TorrentDownload
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		for _, u := range body.URIs {
			if !strings.HasPrefix(u, "magnet:") && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return nil, badRequest("not a magnet link or a .torrent URL")
			}
		}
		if len(body.URIs) == 0 {
			return nil, badRequest("nothing to download")
		}
		if len(body.URIs) > maxPeerTorrents {
			return nil, badRequest(fmt.Sprintf("at most %d torrents at once", maxPeerTorrents))
		}
		save, err := s.addTorrents(r.Context(), body.MediaID, body.URIs)
		if err != nil {
			return nil, err
		}
		s.app.Hub.Info(fmt.Sprintf("%s sent %d torrent(s) to the torrent client here", from, len(body.URIs)))
		s.hubDownloaded(c, body.MediaID, fmt.Sprintf("sent %d torrent(s) to the hub:", len(body.URIs)))
		return map[string]string{"savePath": save}, nil
	}
	return nil, notFound("unknown download")
}

// forwardShared forwards a player's request about a shared file to its host.
func (s *Server) forwardShared(w http.ResponseWriter, r *http.Request, endpoint string) bool {
	if !share.IsRemote(r.URL.Query().Get("path")) {
		return false
	}
	s.app.Share.Forward(w, r, endpoint)
	return true
}

// The in-app player's endpoints, for this Aniko's files (and its guests').

// awakeFor keeps this computer from sleeping while another device (a TV,
// a phone) plays its file (see package awake): until release.
func (s *Server) awakeFor(r *http.Request) (release func()) {
	if isTrusted(r) {
		return func() {}
	}
	return s.app.Awake.Hold()
}

func (s *Server) localFile(w http.ResponseWriter, r *http.Request) {
	defer s.awakeFor(r)()
	w, done := s.track(w, r, "file")
	defer done()
	s.app.Local.ServeFile(w, r, r.URL.Query().Get("path"))
}

// track counts what a video request sends a device, for a hub's dashboard
// (elsewhere it does nothing).
func (s *Server) track(w http.ResponseWriter, r *http.Request, kind string) (http.ResponseWriter, func()) {
	if s.app.Streams == nil || isTrusted(r) {
		return w, func() {}
	}
	c := peerOf(r)
	name := c.Name
	if name == "" {
		name = "A browser"
	}
	st := s.app.Streams.Begin(name, r.URL.Query().Get("path"), kind)
	return st.Writer(w), st.End
}

func (s *Server) localTranscode(w http.ResponseWriter, r *http.Request) {
	defer s.awakeFor(r)()
	q := r.URL.Query()
	start, _ := strconv.ParseFloat(q.Get("start"), 64)
	audio, _ := strconv.Atoi(q.Get("audio"))
	kind := q.Get("method")
	if kind != "remux" {
		kind = "transcode"
	}
	w, done := s.track(w, r, kind)
	defer done()
	s.app.Local.ServeTranscode(w, r, q.Get("path"), start, audio, q.Get("method"), stream.ParseCaps(q.Get("caps")))
}

func (s *Server) localSeekPoint(r *http.Request) (any, error) {
	q := r.URL.Query()
	t, _ := strconv.ParseFloat(q.Get("t"), 64)
	audio, _ := strconv.Atoi(q.Get("audio"))
	method := q.Get("method")
	start, use, err := s.app.Local.Seek(r.Context(), q.Get("path"), t, audio, method, stream.ParseCaps(q.Get("caps")), q.Get("hls") == "1")
	if err != nil {
		return nil, err
	}
	// The method the player asks the stream with, when not its own.
	if use != method {
		return map[string]any{"start": start, "method": use}, nil
	}
	return map[string]any{"start": start}, nil
}

func (s *Server) localThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t, _ := strconv.ParseFloat(q.Get("t"), 64)
	s.app.Local.ServeThumb(w, r, q.Get("path"), t)
}

func (s *Server) localSubtitle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	idx, _ := strconv.Atoi(q.Get("index"))
	s.app.Local.ServeSubtitle(w, r, q.Get("path"), idx, q.Get("external"))
}

// playShared opens a shared file: in mpv, which plays it from the host, or
// in the in-app player. Progress and history are this Aniko's, with its
// AniList account.
func (s *Server) playShared(r *http.Request, body playLocalBody) (any, error) {
	f, ok := s.app.Share.File(body.Path)
	if !ok {
		return nil, notFound("that library isn't shared with this Aniko any more")
	}
	if body.MediaID == 0 {
		body.MediaID, body.Episode = f.MediaID, f.Episode
	}
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	if !isTrusted(r) {
		playerName = "builtin"
	}
	title := s.episodeTitle(r.Context(), body.MediaID, body.Episode, f.Name)
	if playerName == "mpv" {
		target, headers, err := s.app.Share.MediaURL(body.Path)
		if err != nil {
			return nil, notFound(err.Error())
		}
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, Title: title, Source: "local", Target: target, Headers: headers, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess}, nil
	}
	probe, err := s.app.Share.Probe(r.Context(), body.Path)
	if err != nil {
		return nil, badRequest(strings.TrimSpace(f.HostName + ": " + err.Error()))
	}
	probe.Path = body.Path
	return s.builtinPlay(body, probe, title), nil
}
