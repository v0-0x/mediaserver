package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/discord"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/share"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// ---------------------------------------------------------------------------
// Titles

// Titles name what plays: Window is the players' title ("Show — S01E02 ·
// Pilot", "Movie (2010)"), Media the movie's or show's, Episode the
// episode's ("" for a movie, or when unknown).
type Titles struct {
	Window  string
	Media   string
	Episode string
}

// EpisodeTitle is how the players title an episode without its name: the
// show's title with the episode (Show — S01E05), or the movie's with its
// year (Movie (2010)).
func EpisodeTitle(media *catalog.Media, episode int) string { return PlayerTitle(media, episode, "") }

// PlayerTitle is how the players title an episode (a key) named name ("":
// unknown): "Show — S01E05 · Name", or "Movie (2010)".
func PlayerTitle(media *catalog.Media, episode int, name string) string {
	title := media.PreferredTitle()
	if !media.IsShow() {
		if y := media.Year(); y > 0 {
			return fmt.Sprintf("%s (%d)", title, y)
		}
		return title
	}
	if episode <= 0 {
		return title
	}
	title += " — " + catalog.FormatKey(episode)
	if name != "" {
		title += " · " + name
	}
	return title
}

// EpisodeName is the title of a show's episode (key), "" when unknown. The
// season's details are usually saved already; TMDB is asked only briefly.
func (a *App) EpisodeName(ctx context.Context, media *catalog.Media, key int) string {
	if media == nil || !media.IsShow() || key <= 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	season, _ := catalog.SplitKey(key)
	sd, err := a.Platform.Season(ctx, media.ID, season)
	if err != nil {
		return ""
	}
	for _, e := range sd.Episodes {
		if e.Key == key {
			return e.Title
		}
	}
	return ""
}

// PlayTitles names an episode (a key) of a title; fallback is the window's
// title when the title isn't known.
func (a *App) PlayTitles(ctx context.Context, mediaID, key int, fallback string) Titles {
	media, err := a.Platform.MediaLite(ctx, mediaID)
	if err != nil || media == nil {
		return Titles{Window: fallback}
	}
	return a.titlesOf(ctx, media, key)
}

func (a *App) titlesOf(ctx context.Context, media *catalog.Media, key int) Titles {
	name := a.EpisodeName(ctx, media, key)
	return Titles{Window: PlayerTitle(media, key, name), Media: media.PreferredTitle(), Episode: name}
}

// ---------------------------------------------------------------------------
// Auto play next

// nextKey is the episode after the session's: the show's next regular
// episode (across seasons), one that aired for a stream. 0: none (a movie,
// a special, the last episode).
func (a *App) nextKey(ctx context.Context, s *player.Session) (*catalog.Media, int, error) {
	if !catalog.IsShow(s.MediaID) || s.Episode < 1000 {
		return nil, 0, nil
	}
	media, err := a.Platform.MediaLite(ctx, s.MediaID)
	if err != nil {
		return nil, 0, err
	}
	next := media.NextEpisode(s.Episode)
	if s.Source == "local" {
		// The file just played holds the next episodes too (S01E01E02).
		if f := a.episodeFile(media.ID, next); f != nil && f.Path == s.Target && f.EpisodeEnd > 0 {
			next = media.NextEpisode(f.EpisodeEnd)
		}
	}
	if latest := media.LatestAired(); s.Source == "stream" && latest > 0 && next > latest {
		next = 0
	}
	return media, next, nil
}

// episodeFile is the file of an episode: this computer's, else one another
// Moviko shares.
func (a *App) episodeFile(mediaID, key int) *library.LocalFile {
	files, _ := a.Files.ByMedia(mediaID)
	for _, f := range a.Share.RemoteFiles() {
		if f.MediaID == mediaID {
			files = append(files, f)
		}
	}
	return library.EpisodeFile(files, key)
}

// nextEpisode is what auto play next plays after a session: the show's
// next episode, from its file for a file, from the same streaming provider
// in the same version for a stream.
func (a *App) nextEpisode(ctx context.Context, s *player.Session) (*player.PlayRequest, error) {
	media, next, err := a.nextKey(ctx, s)
	if err != nil || next == 0 {
		return nil, err
	}
	t := a.titlesOf(ctx, media, next)
	req := &player.PlayRequest{MediaID: media.ID, Episode: next, Title: t.Window, MediaTitle: t.Media, EpisodeTitle: t.Episode, Source: s.Source}
	switch s.Source {
	case "local":
		f := a.episodeFile(media.ID, next)
		if f == nil {
			return nil, nil
		}
		req.Target = f.Path
		if f.EpisodeEnd > next {
			req.EpisodeEnd = f.EpisodeEnd
		}
		if share.IsRemote(f.Path) {
			if req.Target, req.Headers, err = a.Share.MediaURL(f.Path); err != nil {
				return nil, err
			}
		}
		return req, nil
	case "stream":
		if s.Provider == "" {
			return nil, nil
		}
		// The provider's direct link for the next episode most like the
		// one that played (its quality and release, see stream.Version),
		// else its best one, with a few subtitles of subtitle addons.
		v := sessionVersion(s)
		st, res, err := a.Stream.BestFor(ctx, media, next, stream.Pick{Provider: s.Provider, Version: &v})
		if err != nil {
			return nil, err
		}
		req.Target, req.Provider, req.Dub = st.URL, s.Provider, s.Dub
		req.Quality, req.Release = st.Quality, stream.ReleaseName(st.Stream)
		req.Headers, req.Referrer = stream.SplitReferrer(stream.WithCredentials(st.URL, st.Headers))
		for _, sub := range st.Subtitles {
			req.SubFiles = append(req.SubFiles, sub.URL)
		}
		for _, sub := range stream.SortSubtitles(res.Subtitles, a.Settings.Get().Playback.PreferredSubLang, 4) {
			req.SubFiles = append(req.SubFiles, sub.URL)
		}
		if s.Dub {
			req.Title += " (Dub)"
		}
		return req, nil
	}
	return nil, nil
}

// hasNextEpisode reports whether auto play next has an episode to play
// after a session (credits at the end of a file are skipped then).
func (a *App) hasNextEpisode(s *player.Session) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	media, next, err := a.nextKey(ctx, s)
	if err != nil || next == 0 {
		return false
	}
	switch s.Source {
	case "local":
		return a.episodeFile(media.ID, next) != nil
	case "stream":
		return s.Provider != ""
	}
	return false
}

// ---------------------------------------------------------------------------
// Discord rich presence

// presence is what's shown on Discord (Settings › Discord).
type presence struct {
	mu      sync.Mutex
	last    time.Time
	lastKey string      // what was shown: a change shows at once
	showing bool        // a presence is set and must be cleared eventually
	expire  *time.Timer // the in-app player went quiet: playback is over
}

// builtinQuiet: the in-app player reports every few seconds while it plays;
// this long without a word, it was closed.
const builtinQuiet = 2 * time.Minute

func (a *App) clearPresence() {
	p := &a.presence
	p.mu.Lock()
	was := p.showing
	p.showing, p.last, p.lastKey = false, time.Time{}, ""
	if p.expire != nil {
		p.expire.Stop()
	}
	p.mu.Unlock()
	if was {
		a.Discord.Clear()
	}
}

// showPresence shows a playback status on Discord: "Watching <title>",
// the episode or the year, the poster and the time left. Updates come at
// most every 15 seconds (Discord limits them), but for a new episode or a
// pause.
func (a *App) showPresence(s *player.Session) {
	cfg := a.Settings.Get()
	if !cfg.Discord.RichPresence || cfg.Discord.ClientID == "" || !s.Active {
		a.clearPresence()
		return
	}
	p := &a.presence
	key := fmt.Sprint(s.MediaID, "/", s.Episode, "/", s.Paused)
	p.mu.Lock()
	if s.Player == "builtin" {
		if p.expire == nil {
			p.expire = time.AfterFunc(builtinQuiet, a.clearPresence)
		} else {
			p.expire.Reset(builtinQuiet)
		}
	} else if p.expire != nil {
		p.expire.Stop()
	}
	if key == p.lastKey && time.Since(p.last) < 15*time.Second {
		p.mu.Unlock()
		return
	}
	p.last, p.lastKey, p.showing = time.Now(), key, true
	p.mu.Unlock()

	w := discord.Watching{Title: s.Title, Show: catalog.IsShow(s.MediaID), Episode: s.Episode, EpisodeTitle: s.EpisodeTitle,
		Position: s.Position, Duration: s.Duration, Paused: s.Paused}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if media, err := a.Platform.MediaLite(ctx, s.MediaID); err == nil {
		w.Title, w.Year, w.Show, w.Poster = media.PreferredTitle(), media.Year(), media.IsShow(), media.CoverImage.Large
		if w.Show && w.EpisodeTitle == "" {
			w.EpisodeTitle = a.EpisodeName(ctx, media, s.Episode)
		}
	}
	_ = a.Discord.SetActivity(cfg.Discord.ClientID, discord.Presence(w, time.Now()))
}
