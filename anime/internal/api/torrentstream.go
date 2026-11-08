package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/player"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"github.com/v0-0x/mediaserver/anime/internal/stream"
	"github.com/v0-0x/mediaserver/anime/internal/torrentstream"
)

// Torrent streaming (package torrentstream): a torrent of the Torrents tab
// played as it downloads, here, instead of going to the torrent client.
// The in-app player plays it like a library file (its path is
// torrent:<id>, see stream.TorrentPrefix): as it is, or converted, its
// subtitles and audio tracks included.

func (s *Server) torrentStreamRoutes() {
	m := s.mux
	m.HandleFunc("POST /api/torrents/stream", h(s.streamTorrent))
	// A torrent streamed on the computer the downloads go to (see
	// streamTorrent): its ID is <that Aniko's ID>.<the session there>.
	m.HandleFunc("GET /api/torrentstream/{id}", h(func(r *http.Request) (any, error) {
		if host, sid, ok := strings.Cut(r.PathValue("id"), "."); ok {
			raw, _, err := s.app.Share.AskHost(r.Context(), host, "/api/peer/torrentstream/"+sid, nil)
			if err != nil {
				return nil, notFound(err.Error())
			}
			return raw, nil
		}
		st, ok := s.app.TorrentStream.Status(r.PathValue("id"))
		if !ok {
			return nil, notFound(stream.ErrTorrentGone.Error())
		}
		return st, nil
	}))
	m.HandleFunc("DELETE /api/torrentstream/{id}", h(func(r *http.Request) (any, error) {
		if host, sid, ok := strings.Cut(r.PathValue("id"), "."); ok {
			_, _, err := s.app.Share.AskHost(r.Context(), host, "/api/peer/torrentstream/"+sid+"/stop", struct{}{})
			return nil, err
		}
		s.app.TorrentStream.Stop(r.PathValue("id"))
		return nil, nil
	}))

	// For the Anikos allowed to download onto this one (a TV: no torrent
	// client of its own): a torrent streamed here, read by that Aniko
	// alone (/api/peer/local/* with its torrent: path, see sharedFile).
	viaHost := func(fn func(r *http.Request) (any, error)) http.HandlerFunc {
		return shared(h(func(r *http.Request) (any, error) {
			c := peerOf(r)
			if !c.Downloads {
				return nil, forbidden("this Aniko doesn't take downloads from yours")
			}
			// Nothing of what a torrent holds is known: not with parental
			// controls, here or there.
			if s.app.Share.Parental().Active() || c.Rules.Active() {
				return nil, forbidden("parental controls are on: torrents can't be played")
			}
			return fn(r)
		}))
	}
	m.HandleFunc("POST /api/peer/torrents/stream", viaHost(func(r *http.Request) (any, error) {
		var body streamTorrentBody
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		magnet, err := torrentstream.Magnet(body.Magnet, body.InfoHash, body.Name)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		ts, err := s.app.TorrentStream.Open(ctx, magnet, pickEpisodeFile(body.Episode))
		if err != nil {
			return nil, &apiError{http.StatusBadGateway, err.Error()}
		}
		s.torrentMu.Lock()
		if s.torrentOwners == nil {
			s.torrentOwners = map[string]string{}
		}
		// Torrents no longer streamed (stopped, or dropped by the engine
		// when no one read them) are forgotten.
		for id := range s.torrentOwners {
			if _, ok := s.app.TorrentStream.Status(id); !ok {
				delete(s.torrentOwners, id)
			}
		}
		s.torrentOwners[ts.ID] = peerOf(r).ID
		s.torrentMu.Unlock()
		return map[string]any{"path": stream.TorrentPrefix + ts.ID, "torrent": ts}, nil
	}))
	m.HandleFunc("GET /api/peer/torrentstream/{id}", viaHost(func(r *http.Request) (any, error) {
		id := r.PathValue("id")
		if !s.ownsTorrent(peerOf(r).ID, stream.TorrentPrefix+id) {
			return nil, notFound(stream.ErrTorrentGone.Error())
		}
		st, ok := s.app.TorrentStream.Status(id)
		if !ok {
			return nil, notFound(stream.ErrTorrentGone.Error())
		}
		return st, nil
	}))
	m.HandleFunc("POST /api/peer/torrentstream/{id}/stop", viaHost(func(r *http.Request) (any, error) {
		id := r.PathValue("id")
		if s.ownsTorrent(peerOf(r).ID, stream.TorrentPrefix+id) {
			s.app.TorrentStream.Stop(id)
			s.torrentMu.Lock()
			delete(s.torrentOwners, id)
			s.torrentMu.Unlock()
		}
		return nil, nil
	}))
}

// ownsTorrent reports a torrent: path streamed here for the Aniko with
// that ID (torrentStreamRoutes): the one that may read it.
func (s *Server) ownsTorrent(peerID, path string) bool {
	if peerID == "" || !stream.IsTorrent(path) {
		return false
	}
	s.torrentMu.Lock()
	defer s.torrentMu.Unlock()
	return s.torrentOwners[path[len(stream.TorrentPrefix):]] == peerID
}

type streamTorrentBody struct {
	MediaID  int      `json:"mediaId"`
	Episode  int      `json:"episode"`
	Magnet   string   `json:"magnet"`
	InfoHash string   `json:"infoHash"`
	Name     string   `json:"name"`
	Player   string   `json:"player"` // mpv | builtin ("" = settings)
	Start    *float64 `json:"start"`
}

// streamTorrent is POST /api/torrents/stream: it starts the torrent (its
// details come from its peers) and plays the episode's file of it in mpv,
// or gives the in-app player its path ({path: "torrent:<id>"}, played as a
// library file is).
func (s *Server) streamTorrent(r *http.Request) (any, error) {
	var body streamTorrentBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	// What a device with parental controls may watch is known by its
	// library's files: a torrent isn't one.
	if s.app.Share.Parental().Active() {
		return nil, forbidden("parental controls are on: torrents can't be played on this device")
	}
	magnet, err := torrentstream.Magnet(body.Magnet, body.InfoHash, body.Name)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	ts, err := s.app.TorrentStream.Open(ctx, magnet, pickEpisodeFile(body.Episode))
	if errors.Is(err, torrentstream.ErrUnavailable) {
		// No client here (a TV): the computer the downloads go to streams
		// it, and this player reads it from there as a shared file.
		return s.streamTorrentViaHost(ctx, body)
	}
	if err != nil {
		return nil, &apiError{http.StatusBadGateway, err.Error()}
	}
	path := stream.TorrentPrefix + ts.ID
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	if !isTrusted(r) {
		playerName = "builtin"
	}
	if playerName == "mpv" {
		target, err := s.app.TorrentStream.URL(ts.ID)
		if err != nil {
			return nil, err
		}
		title := s.episodeTitle(r.Context(), body.MediaID, body.Episode, ts.Name)
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, Title: title, Source: "torrent", Target: target, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess, "path": path, "torrent": ts}, nil
	}
	return map[string]any{"player": "builtin", "path": path, "torrent": ts}, nil
}

// streamTorrentViaHost starts the torrent on the Aniko the downloads go to
// (POST /api/peer/torrents/stream there, behind its Downloads grant) and
// gives the in-app player its path there (kumo://<host>/torrent:<id>),
// played as a shared file is, and the torrent's ID here (<host>.<id>) for
// its status.
func (s *Server) streamTorrentViaHost(ctx context.Context, body streamTorrentBody) (any, error) {
	host := s.app.Settings.Get().Sharing.DownloadTo
	if host == "" {
		return nil, badRequest(torrentstream.ErrUnavailable.Error() + " (or let a computer take this device's downloads: it then streams them)")
	}
	body.Player = "builtin"
	raw, name, ok, err := s.app.Share.TorrentsHost(ctx, "stream", body)
	if !ok {
		return nil, badRequest(torrentstream.ErrUnavailable.Error())
	}
	if err != nil {
		return nil, &apiError{http.StatusBadGateway, err.Error()}
	}
	var res struct {
		Path    string               `json:"path"`
		Torrent torrentstream.Status `json:"torrent"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || !stream.IsTorrent(res.Path) {
		return nil, &apiError{http.StatusBadGateway, name + ": unexpected answer"}
	}
	res.Torrent.ID = host + "." + res.Torrent.ID
	return map[string]any{"player": "builtin", "path": share.RemotePath(host, res.Path), "torrent": res.Torrent, "host": name}, nil
}

// playTorrent is POST /api/playback/local for a torrent's path: what the
// in-app player needs to open it, or mpv playing it.
func (s *Server) playTorrent(r *http.Request, body playLocalBody) (any, error) {
	id := body.Path[len(stream.TorrentPrefix):]
	if _, ok := s.app.TorrentStream.Status(id); !ok {
		return nil, notFound(stream.ErrTorrentGone.Error())
	}
	st, _ := s.app.TorrentStream.Status(id)
	title := s.episodeTitle(r.Context(), body.MediaID, body.Episode, st.Name)
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	if !isTrusted(r) {
		playerName = "builtin"
	}
	if playerName == "mpv" {
		target, err := s.app.TorrentStream.URL(id)
		if err != nil {
			return nil, err
		}
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, Title: title, Source: "torrent", Target: target, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess}, nil
	}
	probe, err := s.app.Local.Probe(r.Context(), body.Path)
	if err != nil {
		return nil, err
	}
	return s.builtinPlay(body, probe, title), nil
}

// pickEpisodeFile picks the episode's file of a batch torrent (by the
// number in its name), else the largest video.
func pickEpisodeFile(episode int) torrentstream.Pick {
	return func(paths []string) int {
		if episode <= 0 {
			return -1
		}
		for i, p := range paths {
			if !library.IsVideo(p) {
				continue
			}
			pr := library.Parse(p, nil)
			if pr.Kind == "main" && (pr.Episode == episode || pr.Episode <= episode && episode <= pr.EpisodeEnd) {
				return i
			}
		}
		return -1
	}
}
