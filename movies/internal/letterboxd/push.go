package letterboxd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// With API access (Settings.Letterboxd.APIClientID and APIClientSecret,
// granted by Letterboxd) a member signs in once; the tokens are kept in the
// database and what's logged in Moviko is sent to their diary.

// newAPIClient returns a client with the app's credentials.
func (s *Sync) newAPIClient(set config.LetterboxdSettings, accessToken string) *APIClient {
	return &APIClient{
		BaseURL:      s.apiBaseURL(),
		ClientID:     strings.TrimSpace(set.APIClientID),
		ClientSecret: strings.TrimSpace(set.APIClientSecret),
		AccessToken:  accessToken,
		HTTP:         s.httpClient(),
		now:          s.now,
	}
}

// APILogin signs the member in to the API with their Letterboxd username
// (or email) and password; only the tokens are kept. With no account
// connected yet, the member's becomes the connected one.
func (s *Sync) APILogin(ctx context.Context, username, password string) (*Member, error) {
	set := s.settings()
	c := s.newAPIClient(set, "")
	tok, err := c.PasswordLogin(ctx, username, password)
	if err != nil {
		return nil, err
	}
	c.AccessToken = tok.AccessToken
	m, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.setKV(kvToken, tok); err != nil {
		return nil, err
	}
	_ = s.setKV(kvMember, m)
	if set.Username == "" && m.Username != "" {
		if u, err := ParseUsername(m.Username); err == nil {
			if err := s.saveSettings(func(l *config.LetterboxdSettings) { l.Username = u }); err != nil {
				return m, err
			}
			_ = s.Catalog.SetViewer(viewer(u, m.DisplayName, m.Avatar))
		}
	}
	s.publishStatus()
	s.requestPush()
	return m, nil
}

// APILogout forgets the API sign-in (the account stays connected).
func (s *Sync) APILogout() {
	s.deleteKV(kvToken)
	s.deleteKV(kvMember)
	s.publishStatus()
}

// apiClient returns a client signed in with the saved token, renewed when
// it has expired. ErrNoAPIAccess when there's no API access.
func (s *Sync) apiClient(ctx context.Context, forceRefresh bool) (*APIClient, error) {
	set := s.settings()
	var tok Token
	if strings.TrimSpace(set.APIClientID) == "" || strings.TrimSpace(set.APIClientSecret) == "" ||
		!s.getKV(kvToken, &tok) || tok.AccessToken == "" {
		return nil, ErrNoAPIAccess
	}
	c := s.newAPIClient(set, tok.AccessToken)
	if forceRefresh || tok.Expired(s.clock()) {
		if tok.RefreshToken == "" {
			s.deleteKV(kvToken)
			return nil, ErrAPIUnauthorized
		}
		fresh, err := c.Refresh(ctx, tok.RefreshToken)
		if errors.Is(err, ErrAPIUnauthorized) {
			// The sign-in is over: the member has to sign in again.
			s.deleteKV(kvToken)
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		if err := s.setKV(kvToken, fresh); err != nil {
			return nil, err
		}
		c.AccessToken = fresh.AccessToken
	}
	return c, nil
}

// Push sends the diary entries logged in Moviko (and not sent yet) to the
// member's Letterboxd diary, and marks them exported. It needs API access
// (ErrNoAPIAccess otherwise). Entries of films Letterboxd doesn't have are
// skipped (and stay pending, to export as CSV).
func (s *Sync) Push(ctx context.Context) (*Result, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.push(ctx)
	release()
	if err != nil && !errors.Is(err, ErrNoAPIAccess) {
		s.recordError(err)
	}
	s.publishStatus()
	return res, err
}

func (s *Sync) push(ctx context.Context) (*Result, error) {
	res := &Result{Unmatched: []string{}}
	c, err := s.apiClient(ctx, false)
	if err != nil {
		return res, err
	}
	pending, err := s.Catalog.PendingDiary(ctx)
	if err != nil {
		return res, err
	}
	media := mediaCache{s: s, m: map[int]*catalog.Media{}}
	films := map[int]string{}
	refreshed := false
	for _, e := range pending {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if e == nil || e.Source != SourceMoviko {
			continue
		}
		m := media.get(ctx, e)
		filmID, ok := films[e.MediaID]
		if !ok {
			f, err := findLetterboxdFilm(ctx, c, e.MediaID, m)
			if errors.Is(err, ErrAPIUnauthorized) && !refreshed {
				refreshed = true
				if c, err = s.apiClient(ctx, true); err != nil {
					return res, err
				}
				f, err = findLetterboxdFilm(ctx, c, e.MediaID, m)
			}
			switch {
			case errors.Is(err, ErrFilmNotFound):
			case err != nil:
				return res, err
			default:
				filmID = f.ID
			}
			films[e.MediaID] = filmID
		}
		if filmID == "" {
			res.Skipped++
			res.Unmatched = append(res.Unmatched, mediaLabel(e.MediaID, m))
			continue
		}
		req := LogEntryRequest{
			FilmID:  filmID,
			Date:    e.WatchedDate,
			Rewatch: e.Rewatch,
			Rating:  ScoreToStars(float64(e.Rating)),
			Like:    e.Liked,
			Review:  e.Review,
			Tags:    splitTags(e.Tags),
		}
		_, err := c.LogEntry(ctx, req)
		if errors.Is(err, ErrAPIUnauthorized) && !refreshed {
			refreshed = true
			if c, err = s.apiClient(ctx, true); err != nil {
				return res, err
			}
			_, err = c.LogEntry(ctx, req)
		}
		if err != nil {
			return res, err
		}
		if err := s.Catalog.MarkDiaryExported(ctx, []int64{e.ID}, s.clock().Unix()); err != nil {
			return res, err
		}
		res.Added++
	}
	return res, nil
}

// findLetterboxdFilm finds a media's film on Letterboxd: by TMDB id for a
// movie, by IMDb id otherwise (or when the TMDB id isn't known there).
func findLetterboxdFilm(ctx context.Context, c *APIClient, mediaID int, m *catalog.Media) (*Film, error) {
	imdb := ""
	if m != nil {
		imdb = m.IMDbID
	}
	if catalog.IsMovie(mediaID) {
		f, err := c.FilmByTMDB(ctx, catalog.TMDBID(mediaID))
		if !errors.Is(err, ErrFilmNotFound) || imdb == "" {
			return f, err
		}
	}
	return c.FilmByIMDb(ctx, imdb)
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// mediaCache looks the media of diary entries up once each.
type mediaCache struct {
	s *Sync
	m map[int]*catalog.Media
}

func (mc mediaCache) get(ctx context.Context, e *catalog.DiaryEntry) *catalog.Media {
	if e.Media != nil {
		return e.Media
	}
	if m, ok := mc.m[e.MediaID]; ok {
		return m
	}
	m, err := mc.s.Catalog.MediaLite(ctx, e.MediaID)
	if err != nil {
		m = nil
	}
	mc.m[e.MediaID] = m
	return m
}

// mediaLabel names a media in Result.Unmatched.
func mediaLabel(mediaID int, m *catalog.Media) string {
	if m == nil || m.PreferredTitle() == "" {
		return fmt.Sprintf("TMDB %s %d", strings.ToLower(catalog.KindOf(mediaID)), catalog.TMDBID(mediaID))
	}
	return filmLabel(exportTitle(m), m.Year())
}
