package api

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/release"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/torrentstream"
)

// Torrent streaming (package torrentstream): a torrent played (a streaming
// addon's, or a result of the Torrents tab) downloads as it plays, here,
// instead of going to the torrent client.

func (s *Server) torrentStreamRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/torrentstream/{id}", h(func(r *http.Request) (any, error) {
		st, ok := s.app.TorrentStream.Status(r.PathValue("id"))
		if !ok {
			return nil, notFound("this torrent isn't being played any more")
		}
		return st, nil
	}))
	m.HandleFunc("GET /api/torrentstream/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		s.app.TorrentStream.Serve(w, r, r.PathValue("id"))
	})
	m.HandleFunc("DELETE /api/torrentstream/{id}", h(func(r *http.Request) (any, error) {
		s.app.TorrentStream.Stop(r.PathValue("id"))
		return nil, nil
	}))
}

// streamTorrent plays a torrent as it downloads: in mpv, or in the in-app
// player (the file as it is when the browser plays it, converted
// otherwise), as providerPlay answers.
func (s *Server) streamTorrent(r *http.Request, media *catalog.Media, episode int, st stream.Stream, body providerPlayBody) (any, error) {
	name := firstLine(st.Title)
	if st.Filename != "" {
		name = st.Filename
	}
	magnet, err := torrentstream.Magnet(st.Magnet, st.InfoHash, name)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	// Its details come from its peers: up to a minute and a half.
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	ts, err := s.app.TorrentStream.Open(ctx, magnet, pickTorrentFile(media, episode, st.FileIdx))
	if err != nil {
		return nil, &apiError{http.StatusBadGateway, err.Error()}
	}
	remote := stream.RegisterRemoteHandler("torrent:"+ts.ID, s.app.TorrentStream.Handler(ts.ID))

	cfg := s.app.Settings.Get()
	playerName := body.Player
	if playerName == "" {
		playerName = cfg.Playback.DefaultPlayer
	}
	if !isTrusted(r) {
		playerName = "builtin"
	}
	t := s.app.PlayTitles(r.Context(), media.ID, episode, media.PreferredTitle())
	if playerName == "mpv" {
		target, err := stream.RemoteURL(remote)
		if err != nil {
			return nil, err
		}
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: media.ID, Episode: episode, Title: t.Window, MediaTitle: t.Media, EpisodeTitle: t.Episode,
			Source: "torrent", Target: target, Start: body.Start, Provider: st.Provider, Quality: st.Quality, Release: ts.Name,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"action": "mpv", "player": "mpv", "session": sess, "provider": st.Provider, "torrent": ts}, nil
	}
	src := map[string]any{
		"id": "torrent:" + ts.ID, "url": "/api/torrentstream/" + ts.ID + "/file", "originalUrl": "", "headers": map[string]string{}, "remote": remote, "type": providers.StreamUnknown,
		"quality": st.Quality, "label": firstLine(st.Title), "release": ts.Name, "server": "Torrent", "provider": st.Provider,
		"subtitles": []map[string]any{}, "filename": ts.Name, "notWebReady": !webPlayable(ts.Name), "torrent": ts.ID,
	}
	s.app.Player.ResetBuiltinProgress(media.ID, episode, 0)
	resume := s.app.History.ResumePosition(media.ID, episode)
	if body.Start != nil {
		resume = *body.Start
	}
	return map[string]any{
		"action": "builtin", "player": "builtin", "sources": []map[string]any{src}, "title": t.Window, "mediaTitle": t.Media, "episodeTitle": t.Episode, "resumeAt": resume,
		"tracks": s.app.Player.Tracks.Get(media.ID), "mediaId": media.ID, "episode": episode, "key": episode, "provider": st.Provider, "torrent": ts,
	}, nil
}

// webPlayable: a file browsers may play as it is (MP4, WebM); the rest
// (Matroska, AVI...) is converted at once.
func webPlayable(name string) bool {
	return slices.Contains([]string{".mp4", ".m4v", ".webm"}, strings.ToLower(filepath.Ext(name)))
}

// pickTorrentFile picks the file to play: the one the stream names, else
// for an episode the file of that episode (a season's torrent), else the
// largest video.
func pickTorrentFile(media *catalog.Media, episode, fileIdx int) torrentstream.Pick {
	return func(paths []string) int {
		if fileIdx >= 0 && fileIdx < len(paths) {
			return fileIdx
		}
		if media == nil || !media.IsShow() {
			return -1
		}
		season, ep := catalog.SplitKey(episode)
		for i, p := range paths {
			if !release.IsVideo(p) {
				continue
			}
			info := release.ParsePath(p)
			inSeason := len(info.Seasons) == 0 || slices.Contains(info.Seasons, season)
			if inSeason && slices.Contains(info.Episodes, ep) {
				return i
			}
		}
		return -1
	}
}
