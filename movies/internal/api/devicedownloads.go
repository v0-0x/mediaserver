package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/downloads"
	"github.com/v0-0x/mediaserver/movies/internal/share"
)

// A device whose downloads go to another KumoBox (a TV's, to the computer
// it uses: Settings › Devices) sends its provider downloads and torrents
// there, and its torrent page shows that one's client. The computer queues
// them (peerDownload, sharing.go).

// queueStreamDownload queues a title's movie or episodes (keys) to
// download from a streaming provider, each resolved when its turn comes.
func (s *Server) queueStreamDownload(ctx context.Context, body share.StreamDownload) ([]*downloads.Item, error) {
	if body.MediaID <= 0 {
		return nil, badRequest("missing mediaId")
	}
	media, err := s.app.Platform.MediaLite(ctx, body.MediaID)
	if err != nil {
		return nil, err
	}
	keys := make([]int, 0, len(body.Episodes))
	for _, e := range body.Episodes {
		keys = append(keys, int(e))
	}
	if !media.IsShow() {
		keys = []int{catalog.MovieEpisode}
	}
	if len(keys) == 0 {
		return nil, badRequest("no episode to download")
	}
	if body.Provider != "" {
		found := false
		for _, p := range s.app.StreamProviders() {
			found = found || p.ID() == body.Provider
		}
		if !found {
			return nil, badRequest(fmt.Sprintf("streaming provider %q is not installed or is disabled", body.Provider))
		}
	}
	items := []*downloads.Item{}
	for _, key := range keys {
		if _, e := catalog.SplitKey(key); key <= 0 || e <= 0 {
			return nil, badRequest(fmt.Sprintf("invalid episode %d", key))
		}
		it, err := s.app.Downloads.Enqueue(downloads.Item{
			MediaID: media.ID, Episode: key, Title: app.DownloadTitle(media), Image: media.CoverImage.Large,
			Source: "stream", Provider: body.Provider, Quality: body.Quality,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// sendDownload sends downloads where this KumoBox's go when it isn't here:
// sent is false when they're this one's.
func (s *Server) sendDownload(ctx context.Context, kind string, body any) (any, bool, error) {
	host, err := s.app.Share.SendDownload(ctx, kind, body)
	if err != nil {
		return nil, true, badRequest(err.Error())
	}
	if host == "" {
		return nil, false, nil
	}
	s.app.Hub.Success("Sent to " + host + " to download")
	return map[string]string{"host": host}, true, nil
}

// addTorrents adds torrents to this computer's client, saved in the title's
// folder of the library (mediaID 0: the library folder).
func (s *Server) addTorrents(ctx context.Context, mediaID int, uris []string) (string, error) {
	var media *catalog.Media
	if mediaID > 0 {
		if m, err := s.app.Platform.MediaLite(ctx, mediaID); err == nil {
			media = m
		}
	}
	save := s.app.Torrents.SavePath(media, -1, s.mediaFiles(mediaID))
	return save, s.app.Torrents.Add(ctx, uris, save)
}

// hostTorrents forwards a torrent client request (op) to the KumoBox this
// one's downloads go to; ok is false when they're this one's. A POST's body
// goes along.
func (s *Server) hostTorrents(r *http.Request, op string) (json.RawMessage, bool, error) {
	// The body stays unread for this one's own client.
	if host, err := s.app.Share.DownloadHost(); err == nil && host == "" {
		return nil, false, nil
	}
	var body any
	if r.Method == http.MethodPost {
		b := json.RawMessage("{}")
		if op != "start" {
			if err := decode(r, &b); err != nil {
				return nil, true, err
			}
		}
		body = b
	}
	raw, _, ok, err := s.app.Share.TorrentsHost(r.Context(), op, body)
	if err != nil {
		return nil, ok, badRequest(err.Error())
	}
	return raw, ok, nil
}

// hostTorrentStatus is the status of the torrent client of the KumoBox this
// one's downloads go to, with its name (host).
func (s *Server) hostTorrentStatus(r *http.Request) (map[string]any, bool) {
	raw, host, ok, err := s.app.Share.TorrentsHost(r.Context(), "status", nil)
	if !ok {
		return nil, false
	}
	st := map[string]any{}
	if err != nil {
		st["client"], st["connected"], st["error"] = "", false, err.Error()
	} else if json.Unmarshal(raw, &st) != nil {
		st["error"] = "unexpected answer"
	}
	st["host"] = host
	return st, true
}
