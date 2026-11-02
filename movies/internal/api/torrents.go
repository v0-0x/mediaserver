package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/torrent"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// torrentRoutes are the torrent search, the torrent client and the auto
// downloader.
func (s *Server) torrentRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/torrents/providers", h(func(r *http.Request) (any, error) { return s.app.Torrents.Providers(), nil }))
	m.HandleFunc("POST /api/torrents/search", h(s.torrentSearch))
	m.HandleFunc("POST /api/torrents/download", h(s.torrentDownload))

	// When this Moviko's downloads go to another (a TV's to its computer), its
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
	m.HandleFunc("GET /api/autodownloader/rules/{id}/wanted", h(s.ruleWanted))
	m.HandleFunc("POST /api/autodownloader/run", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Run(r.Context()) }))
	m.HandleFunc("GET /api/autodownloader/items", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Items(), nil }))
	m.HandleFunc("DELETE /api/autodownloader/items/{key}", h(func(r *http.Request) (any, error) {
		return nil, s.app.AutoDL.ForgetItem(r.PathValue("key"))
	}))
}

// ---------------------------------------------------------------------------
// Search

// torrentSearchBody is a search from a title page (MediaID set) or for free
// text (Query alone). Episode is the key of the episode looked for; Batch
// asks for the pack of Season, or of the whole show when Season is 0.
// Query, with a title, replaces the searches made from it. Provider is a
// source id, or "all" (the default source of the settings when empty).
type torrentSearchBody struct {
	MediaID    int    `json:"mediaId"`
	Episode    int    `json:"episode"`
	Season     int    `json:"season"`
	Batch      bool   `json:"batch"`
	Resolution string `json:"resolution"`
	Query      string `json:"query"`
	Provider   string `json:"provider"`
}

// torrentSearchResponse holds the results, best first, and what went
// wrong with the sources that failed ("Source: error").
type torrentSearchResponse struct {
	Results []*torrent.SearchResult `json:"results"`
	Errors  []string                `json:"errors"`
}

func (s *Server) torrentSearch(r *http.Request) (any, error) {
	var body torrentSearchBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	body.Query = strings.TrimSpace(body.Query)
	q := torrent.MediaQuery{Query: body.Query, Resolution: body.Resolution}
	if body.MediaID > 0 {
		media, err := s.titleForTorrents(r.Context(), body.MediaID)
		if err != nil {
			return nil, err
		}
		q = mediaQuery(media, body)
	} else if body.Query == "" {
		return nil, badRequest("type something to search")
	}
	provider := body.Provider
	if provider == "" {
		provider = s.app.Settings.Get().Torrent.DefaultProvider
	}
	res, errs, err := s.app.Torrents.Search(r.Context(), provider, q)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	out := torrentSearchResponse{Results: res, Errors: []string{}}
	for _, e := range errs {
		out.Errors = append(out.Errors, e.Error())
	}
	return out, nil
}

// titleForTorrents is a movie or show with its IMDb and TVDB ids, which
// the sources search by: its page's data (usually saved already), else the
// list's.
func (s *Server) titleForTorrents(ctx context.Context, id int) (*catalog.Media, error) {
	media, err := s.app.Platform.Media(ctx, id, false)
	if err == nil {
		return media, nil
	}
	if lite, e := s.app.Platform.MediaLite(ctx, id); e == nil {
		return lite, nil
	}
	return nil, err
}

// mediaQuery is the search for a title: a movie, or the episode, season
// pack or whole show body asks for.
func mediaQuery(media *catalog.Media, body torrentSearchBody) torrent.MediaQuery {
	q := torrent.MediaQuery{
		Title:      media.EnglishOrRomaji(),
		AltTitles:  append([]string{media.Title.Native}, media.AllTitles()...),
		Year:       media.Year(),
		IMDbID:     media.IMDbID,
		TMDBID:     media.TMDBID,
		TVDBID:     media.TVDBID,
		Type:       torrent.MediaMovie,
		Resolution: body.Resolution,
		Query:      body.Query,
		Media:      media,
	}
	if !media.IsShow() {
		return q
	}
	q.Type = torrent.MediaTV
	switch {
	case body.Batch:
		q.Season = max(body.Season, 0)
	case body.Episode > 0:
		q.Season, q.Episode = catalog.SplitKey(body.Episode)
	case body.Season > 0:
		q.Season = body.Season
	}
	return q
}

// ---------------------------------------------------------------------------
// Downloading

// torrentDownload sends results to the torrent client. Each goes to its
// title's folder in the library (a show's season to its season folder);
// Season is the season searched for, for results whose names don't say
// (null: unknown, or the whole show).
func (s *Server) torrentDownload(r *http.Request) (any, error) {
	var body struct {
		MediaID int                     `json:"mediaId"`
		Season  *int                    `json:"season"`
		Results []*torrent.SearchResult `json:"results"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if len(body.Results) == 0 {
		return nil, badRequest("nothing to download")
	}
	var media *catalog.Media
	var files = s.mediaFiles(body.MediaID)
	if body.MediaID > 0 {
		if m, err := s.app.Platform.MediaLite(r.Context(), body.MediaID); err == nil {
			media = m
		}
	}
	def := -1
	if body.Season != nil {
		def = *body.Season
	}
	// Torrents going to the same folder are sent together.
	var paths []string
	byPath := map[string][]string{}
	var all []string
	for _, res := range body.Results {
		if res == nil {
			continue
		}
		m, err := s.app.Torrents.Magnet(r.Context(), res)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", res.Name, err)
		}
		all = append(all, m)
		season := -1
		if media.IsShow() {
			season = torrent.ResultSeason(res, def)
		}
		p := s.app.Torrents.SavePath(media, season, files)
		if _, ok := byPath[p]; !ok {
			paths = append(paths, p)
		}
		byPath[p] = append(byPath[p], m)
	}
	if out, sent, err := s.sendDownload(r.Context(), share.DownloadTorrent, share.TorrentDownload{MediaID: body.MediaID, URIs: all}); sent {
		return out, err
	}
	n := 0
	for _, p := range paths {
		if err := s.app.Torrents.Add(r.Context(), byPath[p], p); err != nil {
			return nil, err
		}
		n += len(byPath[p])
	}
	where := ""
	if len(paths) == 1 && paths[0] != "" {
		where = ", saving to " + paths[0]
	}
	s.app.Hub.Success(fmt.Sprintf("Sent %d torrent(s) to the torrent client%s", n, where))
	return map[string]any{"added": n, "savePaths": paths}, nil
}

// mediaFiles are a title's files in the library (nil for none).
func (s *Server) mediaFiles(mediaID int) []*library.LocalFile {
	if mediaID <= 0 {
		return nil
	}
	files, _ := s.app.Files.ByMedia(mediaID)
	return files
}

// ---------------------------------------------------------------------------
// The torrent client

func (s *Server) torrentList(r *http.Request) (any, error) {
	c, err := s.app.Torrents.Client()
	if err != nil {
		return nil, badRequest(err.Error())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []torrent.Torrent{}
	}
	return list, nil
}

func (s *Server) torrentAction(r *http.Request) (any, error) {
	var body struct {
		Hashes []string `json:"hashes"`
		Action string   `json:"action"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	c, err := s.app.Torrents.Client()
	if err != nil {
		return nil, badRequest(err.Error())
	}
	switch body.Action {
	case "pause":
		return nil, c.Pause(r.Context(), body.Hashes)
	case "resume":
		return nil, c.Resume(r.Context(), body.Hashes)
	case "remove":
		return nil, c.Remove(r.Context(), body.Hashes, false)
	case "removeData":
		return nil, c.Remove(r.Context(), body.Hashes, true)
	case "open":
		if !isTrusted(r) {
			return nil, forbidden("only available on this computer")
		}
		list, err := c.List(r.Context())
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			if len(body.Hashes) > 0 && t.Hash == body.Hashes[0] {
				target := t.ContentPath
				if st, err := os.Stat(target); err != nil || !st.IsDir() {
					target = filepath.Dir(target)
				}
				return nil, util.Open(target)
			}
		}
		return nil, notFound("torrent not found")
	}
	return nil, badRequest("unknown action")
}

// torrentAdd adds a magnet link or .torrent address, saved to the library
// folder (in the title's folder when MediaID is given).
func (s *Server) torrentAdd(r *http.Request) (any, error) {
	var body struct {
		Magnet  string `json:"magnet"`
		MediaID int    `json:"mediaId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	body.Magnet = strings.TrimSpace(body.Magnet)
	if !strings.HasPrefix(body.Magnet, "magnet:") && !strings.HasPrefix(body.Magnet, "http") {
		return nil, badRequest("enter a magnet link or a .torrent URL")
	}
	if out, sent, err := s.sendDownload(r.Context(), share.DownloadTorrent, share.TorrentDownload{MediaID: body.MediaID, URIs: []string{body.Magnet}}); sent {
		return out, err
	}
	_, err := s.addTorrents(r.Context(), body.MediaID, []string{body.Magnet})
	return nil, err
}

// ---------------------------------------------------------------------------
// The auto downloader

func (s *Server) saveRule(r *http.Request) (any, error) {
	var rule torrent.Rule
	if err := decode(r, &rule); err != nil {
		return nil, err
	}
	// Devices on the LAN don't choose folders on this computer.
	if !isTrusted(r) {
		rule.Destination = ""
		if rule.ID > 0 {
			if old, err := s.app.AutoDL.Rule(rule.ID); err == nil && old != nil {
				rule.Destination = old.Destination
			}
		}
	}
	saved, err := s.app.AutoDL.SaveRule(r.Context(), rule)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	return saved, nil
}

// ruleWanted lists the episodes a rule looks for now (keys).
func (s *Server) ruleWanted(r *http.Request) (any, error) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	rule, err := s.app.AutoDL.Rule(id)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return nil, notFound("no such rule")
	}
	keys, err := s.app.AutoDL.Wanted(r.Context(), rule)
	if err != nil {
		return nil, err
	}
	return map[string]any{"episodes": keys}, nil
}
