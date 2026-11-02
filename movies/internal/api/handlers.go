package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/update"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// ---------------------------------------------------------------------------
// Server-sent events

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := s.app.Hub.Subscribe()
	defer unsub()
	trusted := isTrusted(r)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if !trusted {
				msg = eventFor(r, msg)
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// eventFor leaves out of an event what devices on the LAN don't get from
// the HTTP routes either: the update's build log and commands.
func eventFor(r *http.Request, msg []byte) []byte {
	var ev struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(msg, &ev) != nil || ev.Type != events.UpdateStatus {
		return msg
	}
	var st update.Status
	if json.Unmarshal(ev.Payload, &st) != nil {
		return msg
	}
	out, err := json.Marshal(events.Event{Type: ev.Type, Payload: updateFor(r, st)})
	if err != nil {
		return msg
	}
	return out
}

// ---------------------------------------------------------------------------
// Library

func (s *Server) scan(r *http.Request) (any, error) {
	var opts library.ScanOptions
	_ = decode(r, &opts)
	if s.app.Scanner.Running() {
		return nil, badRequest(library.ErrScanRunning.Error())
	}
	go func() {
		res, err := s.app.Scanner.Scan(s.app.Context(), opts)
		if err != nil {
			return // reported to the UI with the scan-done event
		}
		msg := fmt.Sprintf("Library scanned — %d files, %d matched", res.Total, res.Matched)
		if res.Unmatched > 0 {
			msg += fmt.Sprintf(", %d unmatched", res.Unmatched)
		}
		s.app.Hub.Success(msg)
	}()
	return map[string]bool{"started": true}, nil
}

// indexFolders adds library folders the scan skipped (or hasn't reached)
// to the index right away, so they can be matched by hand.
func (s *Server) indexFolders(r *http.Request) (any, error) {
	var body struct {
		Dirs []string `json:"dirs"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if len(body.Dirs) == 0 {
		return nil, badRequest("no folders given")
	}
	files, err := s.app.Scanner.IndexFolders(body.Dirs)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	s.app.Hub.Publish("library-updated", nil)
	if files == nil {
		files = []*library.LocalFile{}
	}
	return files, nil
}

func (s *Server) unmatched(r *http.Request) (any, error) {
	files, err := s.app.Files.Unmatched()
	if err != nil {
		return nil, err
	}
	return library.GroupUnmatched(files), nil
}

type pathsBody struct {
	Paths   []string `json:"paths"`
	MediaID int      `json:"mediaId"`
	Ignored bool     `json:"ignored"`
	// Matching to a show, all optional:
	// Absolute: the numbers in the names count from the show's first
	// episode (episode 13 of a show with 12 in season 1 is S02E01).
	Absolute bool `json:"absolute"`
	// Season: the numbers in the names are episodes of this season (0:
	// the specials).
	Season *int `json:"season"`
	// EpisodeOffset shifts the numbers of the names (e.g. -12).
	EpisodeOffset int `json:"episodeOffset"`
	// Renumber numbers the episodes 1, 2, 3… in file-name order instead
	// (for files whose names carry no usable episode number).
	Renumber bool `json:"renumber"`
}

func (s *Server) loadFiles(paths []string) ([]*library.LocalFile, error) {
	var out []*library.LocalFile
	for _, p := range paths {
		f, err := s.app.Files.Get(p)
		if err != nil {
			return nil, notFound("file not in library: " + p)
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Server) match(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.MediaID <= 0 || len(body.Paths) == 0 {
		return nil, badRequest("mediaId and paths are required")
	}
	media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID)
	if err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	if body.Season != nil && *body.Season < 0 {
		body.Season = nil
	}
	m := library.NewMatcher(s.app.Platform, nil, 0, false)
	m.AssignManual(r.Context(), files, media, library.ManualOptions{
		Absolute: body.Absolute, Season: body.Season, Offset: body.EpisodeOffset,
		Renumber: body.Renumber, Less: naturalLess,
	})
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	s.app.PrefetchMedia(body.MediaID)
	return nil, nil
}

// naturalLess compares file names with numbers in numeric order
// ("Ep 2" < "Ep 10").
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da != "" && db != "" {
			na, _ := strconv.Atoi(da)
			nb, _ := strconv.Atoi(db)
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && i < 9 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func (s *Server) unmatch(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		f.MediaID, f.Locked, f.MatchScore = 0, true, 0
	}
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return nil, nil
}

func (s *Server) ignore(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		f.Ignored = body.Ignored
		if !body.Ignored {
			f.Locked = false
		}
	}
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return nil, nil
}

func (s *Server) patchFile(r *http.Request) (any, error) {
	var body struct {
		Path    string  `json:"path"`
		Episode *int    `json:"episode"`
		Kind    *string `json:"kind"`
		MediaID *int    `json:"mediaId"`
		Locked  *bool   `json:"locked"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	f, err := s.app.Files.Get(body.Path)
	if err != nil {
		return nil, notFound("file not in library")
	}
	if body.MediaID != nil {
		f.MediaID = *body.MediaID
		f.Locked = true
	}
	if body.Episode != nil {
		if *body.Episode < 0 {
			return nil, badRequest("invalid episode")
		}
		// A key: S02E05 is 2005, a special S00E03 is 3, a movie 1. The
		// file holds that one episode now.
		f.Episode, f.EpisodeEnd = *body.Episode, 0
		f.Locked = true
		if body.Kind == nil && catalog.IsShow(f.MediaID) && f.Kind != library.KindExtra {
			f.Kind = library.KindMain
			if f.Episode > 0 && f.Episode < 1000 {
				f.Kind = library.KindSpecial
			}
		}
	}
	if body.Kind != nil {
		switch k := *body.Kind; k {
		case library.KindMain, library.KindSpecial, library.KindExtra, library.KindPart:
			f.Kind = k
		case "nc": // Kumo's name for bonus videos
			f.Kind = library.KindExtra
		default:
			return nil, badRequest("invalid kind")
		}
		if f.Kind == library.KindExtra {
			f.Episode, f.EpisodeEnd = 0, 0
		}
		f.Locked = true
	}
	if body.Locked != nil {
		f.Locked = *body.Locked
	}
	if err := s.app.Files.Save(f); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return f, nil
}

func (s *Server) openFolder(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	var body struct {
		MediaID int `json:"mediaId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, _ := s.app.Files.ByMedia(body.MediaID)
	dir := ""
	if len(files) > 0 {
		dir = files[0].Dir
	} else {
		dir = s.app.Settings.Get().Library.Dir
	}
	if dir == "" {
		return nil, badRequest("no folder")
	}
	return nil, util.Open(dir)
}

// ---------------------------------------------------------------------------
// Playback

type playLocalBody struct {
	Path    string   `json:"path"`
	MediaID int      `json:"mediaId"`
	Episode int      `json:"episode"`
	Player  string   `json:"player"` // mpv | builtin ("" = settings)
	Start   *float64 `json:"start"`
	Caps    []string `json:"caps"` // video formats the in-app player decodes
	// end is the last episode of the file when it holds several and the
	// episode played is one of them (see fileEnd).
	end int
}

// fileEnd is the last episode of a multi-episode file (S01E01E02: 1002)
// when episode of mediaID is one of its episodes, 0 otherwise: watching it
// moves the list progress on to its last episode.
func fileEnd(f *library.LocalFile, mediaID, episode int) int {
	if f.MediaID == mediaID && f.EpisodeEnd > episode && episode >= f.Episode {
		return f.EpisodeEnd
	}
	return 0
}

func (s *Server) playLocal(r *http.Request) (any, error) {
	var body playLocalBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if share.IsRemote(body.Path) {
		return s.playShared(r, body)
	}
	f, err := s.app.Files.Get(body.Path)
	if err != nil {
		return nil, notFound("file not in library")
	}
	if body.MediaID == 0 {
		body.MediaID, body.Episode = f.MediaID, f.Episode
	}
	body.end = fileEnd(f, body.MediaID, body.Episode)
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	// LAN devices can't see this computer's mpv window.
	if !isTrusted(r) {
		playerName = "builtin"
	}
	t := s.app.PlayTitles(r.Context(), body.MediaID, body.Episode, f.Name)
	if playerName == "mpv" {
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, EpisodeEnd: body.end, Title: t.Window, MediaTitle: t.Media, EpisodeTitle: t.Episode,
			Source: "local", Target: f.Path, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess}, nil
	}
	probe, err := s.app.Local.Probe(r.Context(), f.Path)
	if err != nil {
		return nil, err
	}
	return s.builtinPlay(body, probe, t), nil
}

// builtinPlay is what the in-app player needs to open an episode: the file's
// probe, where to resume, the tracks to start with (choice) and the parts
// its chapters say to skip.
func (s *Server) builtinPlay(body playLocalBody, probe *stream.Probe, t app.Titles) map[string]any {
	s.app.Local.Decide(probe, stream.CapsOf(body.Caps))
	// The in-app player opens the episode: a new viewing, which may update
	// progress again (rewatch).
	s.app.Player.ResetBuiltinProgress(body.MediaID, body.Episode, body.end)
	resume := s.app.History.ResumePosition(body.MediaID, body.Episode)
	if body.Start != nil {
		resume = *body.Start
	}
	return map[string]any{
		"player": "builtin", "probe": probe, "title": t.Window, "mediaTitle": t.Media, "episodeTitle": t.Episode, "resumeAt": resume,
		"mediaId": body.MediaID, "episode": body.Episode, "tracks": s.app.Player.Tracks.Get(body.MediaID),
		"choice": s.builtinTracks(body.MediaID, probe), "skips": chapterSkips(probe),
	}
}

type playStreamBody struct {
	MediaID   int               `json:"mediaId"`
	Episode   int               `json:"episode"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Referrer  string            `json:"referrer"`
	Subtitles []string          `json:"subtitles"`
	Title     string            `json:"title"`
	Source    string            `json:"source"`
	Start     *float64          `json:"start"`
	// The streaming provider and version (the next episode comes from
	// them), when the in-app player hands a stream over to mpv.
	Provider string `json:"provider"`
	Dub      bool   `json:"dub"`
	Quality  string `json:"quality"`
	Release  string `json:"release"`
}

func (s *Server) playStream(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("mpv can only be started from this computer")
	}
	var body playStreamBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.URL == "" {
		return nil, badRequest("missing url")
	}
	t := s.app.PlayTitles(r.Context(), body.MediaID, body.Episode, "Stream")
	if body.Title == "" {
		body.Title = t.Window
	}
	return s.app.Player.PlayMpv(player.PlayRequest{
		MediaID: body.MediaID, Episode: body.Episode, Title: body.Title, MediaTitle: t.Media, EpisodeTitle: t.Episode,
		Source: util2(body.Source, "stream"), Target: body.URL, Headers: stream.WithCredentials(body.URL, body.Headers), Referrer: body.Referrer,
		SubFiles: body.Subtitles, Start: body.Start, Provider: body.Provider, Dub: body.Dub,
		Quality: body.Quality, Release: body.Release,
	})
}

func util2(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Server) playbackCommand(r *http.Request) (any, error) {
	var body struct {
		Cmd   string  `json:"cmd"`
		Value float64 `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Player.Command(body.Cmd, body.Value)
}

func (s *Server) playbackProgress(r *http.Request) (any, error) {
	var rep player.ProgressReport
	if err := decode(r, &rep); err != nil {
		return nil, err
	}
	s.app.Player.ReportProgress(rep)
	return nil, nil
}

func (s *Server) getTracks(r *http.Request) (any, error) {
	id, err := pathID(r, "mediaId")
	if err != nil {
		return nil, err
	}
	p := s.app.Player.Tracks.Get(id)
	if p == nil {
		return map[string]any{"mediaId": id}, nil
	}
	return p, nil
}

func (s *Server) saveTracks(r *http.Request) (any, error) {
	id, err := pathID(r, "mediaId")
	if err != nil {
		return nil, err
	}
	var p player.TrackPrefs
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	p.MediaID = id
	if old := s.app.Player.Tracks.Get(id); old != nil && p.StreamMode == "" {
		p.StreamMode = old.StreamMode
	}
	return nil, s.app.Player.Tracks.Save(p)
}

// skips are the parts of a file (?path=, of the library or shared by
// another Moviko) that its chapters name as an intro, a recap, the credits
// or a preview: the players offer to skip them. Streams have none.
func (s *Server) skips(r *http.Request) (any, error) {
	path := r.URL.Query().Get("path")
	var probe *stream.Probe
	var err error
	switch {
	case path == "":
		return []player.SkipInterval{}, nil
	case share.IsRemote(path):
		probe, err = s.app.Share.Probe(r.Context(), path)
	default:
		probe, err = s.app.Local.Probe(r.Context(), path)
	}
	if err != nil {
		return []player.SkipInterval{}, nil
	}
	return chapterSkips(probe), nil
}

// chapterSkips are the parts of a probed file its chapters say to skip.
func chapterSkips(p *stream.Probe) []player.SkipInterval {
	chapters := make([]player.Chapter, 0, len(p.Chapters))
	for _, c := range p.Chapters {
		chapters = append(chapters, player.Chapter{Title: c.Title, Start: c.Start, End: c.End})
	}
	if out := player.ChapterSkips(chapters, p.Duration); out != nil {
		return out
	}
	return []player.SkipInterval{}
}

// builtinTracks are the tracks the in-app player opens a file with, as the
// settings and the choice remembered for the title say (player.ChooseTracks):
// audio is the index of the audio track (-1: the file's default), sub the
// subtitles' key in the player ("e<n>" the n-th embedded track, "x<n>" the
// n-th external file, "off"; "": the player's own pick).
func (s *Server) builtinTracks(mediaID int, p *stream.Probe) map[string]any {
	var tracks []player.Track
	for _, a := range p.Audio {
		tracks = append(tracks, player.Track{ID: a.TypeIndex + 1, Type: "audio", Lang: a.Language, Title: a.Title, Codec: a.Codec, Default: a.Default, Forced: a.Forced})
	}
	for _, sub := range p.Subtitles {
		if !sub.Bitmap { // images: mpv only
			tracks = append(tracks, player.Track{ID: sub.TypeIndex + 1, Type: "sub", Lang: sub.Language, Title: sub.Title, Codec: sub.Codec, Default: sub.Default, Forced: sub.Forced})
		}
	}
	for i, x := range p.ExternalSubs {
		tracks = append(tracks, player.Track{ID: externalSub + i, Type: "sub", Lang: x.Language, Title: x.Name, External: true})
	}
	c := s.app.Player.ChooseTracks(mediaID, "local", false, tracks)
	audio, sub := -1, ""
	if c.HasAudio {
		audio = c.Audio - 1
	}
	switch {
	case c.HasSub && c.Sub >= externalSub:
		sub = fmt.Sprintf("x%d", c.Sub-externalSub)
	case c.HasSub:
		sub = fmt.Sprintf("e%d", c.Sub-1)
	case c.SubOff:
		sub = "off"
	}
	return map[string]any{"audio": audio, "sub": sub, "restored": c.Restored, "note": c.Note}
}

// externalSub numbers the external subtitle files after the embedded tracks.
const externalSub = 10001

func (s *Server) clearHistory(r *http.Request) (any, error) {
	return nil, s.app.History.Clear(queryInt(r, "mediaId"), queryInt(r, "episode"))
}

// ---------------------------------------------------------------------------
// Streaming providers: see providers_routes.go.

// ---------------------------------------------------------------------------
// Extensions

func (s *Server) installExtension(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, errLANManage
	}
	var body struct {
		ManifestURI string `json:"manifestURI"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	info, err := s.app.Extensions.Install(r.Context(), body.ManifestURI)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	s.app.Hub.Success("Installed " + info.Manifest.Name)
	return info, nil
}

// marketplace is GET /api/extensions/marketplace?url=: an extension index
// (Settings › App › Extensions' by default), from the cache when it has it.
// Devices on the LAN read the one of the settings only.
func (s *Server) marketplace(r *http.Request) (any, error) {
	u := r.URL.Query().Get("url")
	set := s.app.Settings.Get().Extensions.MarketplaceURL
	if u != "" && !lanMayFetch(r, u, set, config.DefaultMarketplaceURL) {
		return nil, forbidden("other marketplaces can only be opened on this computer")
	}
	return s.app.Extensions.Marketplace(r.Context(), u, false)
}

// refreshMarketplace is POST /api/extensions/marketplace/refresh {url}: the
// index downloaded again. A POST, as everything that makes Moviko fetch
// again rather than read: other sites can't send one (middleware).
func (s *Server) refreshMarketplace(r *http.Request) (any, error) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return s.app.Extensions.Marketplace(r.Context(), strings.TrimSpace(body.URL), true)
}

func (s *Server) enableExtension(r *http.Request) (any, error) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Extensions.SetEnabled(r.PathValue("id"), body.Enabled)
}

func (s *Server) configExtension(r *http.Request) (any, error) {
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Extensions.SaveUserConfig(r.PathValue("id"), body.Values)
}

func (s *Server) pluginEvent(r *http.Request) (any, error) {
	var evt map[string]any
	if err := decode(r, &evt); err != nil {
		return nil, err
	}
	// Action clicks on media pages carry the media so plugins get {media}.
	if mid, ok := evt["mediaId"].(float64); ok && mid > 0 {
		if media, err := s.app.Platform.MediaLite(r.Context(), int(mid)); err == nil {
			evt["event"] = map[string]any{"media": media}
		}
	}
	return nil, s.app.Extensions.DispatchPluginEvent(r.PathValue("id"), evt)
}
