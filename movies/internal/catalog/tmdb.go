package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// TMDB (The Movie Database) v3 API client. Every movie and show's data
// comes from it. The key, language and region are read from the settings
// on every request, so changing them in Settings › Metadata takes effect at
// once.

const (
	defaultAPIURL   = "https://api.themoviedb.org/3"
	defaultImageURL = "https://image.tmdb.org/t/p"
)

var (
	// ErrNoAPIKey: no TMDB key is configured. The app still runs (library,
	// players, settings); the UI asks for a key.
	ErrNoAPIKey = errors.New("no TMDB API key: add your free TMDB key in Settings › Metadata (get one at themoviedb.org › Settings › API)")
	// ErrUnauthorized: TMDB refused the key.
	ErrUnauthorized = errors.New("TMDB refused the API key: check it in Settings › Metadata")
	// ErrNotFound: TMDB has no such movie, show, season or match.
	ErrNotFound = errors.New("not found on TMDB")

	errRateLimited = errors.New("TMDB is rate limiting requests, try again in a moment")
)

// Waits between attempts; tests shorten them.
var (
	// retryWait is the pause before retrying a server error or a failed
	// connection (both are retried once).
	retryWait = 500 * time.Millisecond
	// maxRetryAfter bounds the wait TMDB asks for after a 429.
	maxRetryAfter = 10 * time.Second
)

// Client talks to TMDB.
type Client struct {
	http     *http.Client
	base     string // API root, without a trailing slash
	images   string // image root, without a trailing slash
	settings func() config.Settings

	genreMu sync.Mutex
	genres  map[string]genreList // by kind and language
}

// genreList is TMDB's genres for movies or shows, in one language.
type genreList struct {
	names map[int]string
	at    time.Time
}

// NewClient returns a client reading its key, language and region from
// settings. KUMOBOX_TMDB_URL and KUMOBOX_TMDB_IMAGE_URL override the API and
// image hosts (tests, offline demos).
func NewClient(settings func() config.Settings) *Client {
	if settings == nil {
		settings = config.Defaults
	}
	c := &Client{
		http:     &http.Client{Timeout: 25 * time.Second},
		base:     defaultAPIURL,
		images:   defaultImageURL,
		settings: settings,
		genres:   map[string]genreList{},
	}
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("KUMOBOX_TMDB_URL")), "/"); u != "" {
		c.base = u
	}
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("KUMOBOX_TMDB_IMAGE_URL")), "/"); u != "" {
		c.images = u
	}
	return c
}

// Configured reports whether a TMDB key is set.
func (c *Client) Configured() bool { return strings.TrimSpace(c.settings().TMDB.APIKey) != "" }

// Image is the URL of a TMDB image (a poster_path, backdrop_path...) at a
// size (w342, w780, original...), "" without a path.
func (c *Client) Image(size, path string) string {
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.images + "/" + size + path
}

// get fetches an API path into out. A v4 read access token (a JWT, "eyJ…")
// goes in the Authorization header, a v3 key in the api_key parameter. The
// language comes from the settings unless q sets one.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	s := c.settings()
	key := strings.TrimSpace(s.TMDB.APIKey)
	if key == "" {
		return ErrNoAPIKey
	}
	if q == nil {
		q = url.Values{}
	}
	if !q.Has("language") && s.TMDB.Language != "" {
		q.Set("language", s.TMDB.Language)
	}
	bearer := strings.HasPrefix(key, "eyJ")
	if !bearer {
		q.Set("api_key", key)
	}
	target := c.base + path + "?" + q.Encode()

	const attempts = 4
	retried := false // a server error or a failed connection, retried once
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", util.UserAgent)
		if bearer {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !retried && sleepCtx(ctx, retryWait) == nil {
				retried = true
				continue
			}
			// The error quotes the URL, which may carry the key.
			return fmt.Errorf("tmdb: %s", strings.ReplaceAll(err.Error(), key, "…"))
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()

		switch code := resp.StatusCode; {
		case code == http.StatusTooManyRequests:
			wait := retryAfter(resp.Header)
			// Waiting is pointless after the last attempt, or when the
			// caller's deadline comes first.
			if attempt >= attempts-1 {
				return errRateLimited
			}
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
				return errRateLimited
			}
			if err := sleepCtx(ctx, wait); err != nil {
				return err
			}
			continue
		case code >= 500:
			if !retried && sleepCtx(ctx, retryWait) == nil {
				retried = true
				continue
			}
			return fmt.Errorf("tmdb: %s%s", resp.Status, statusMessage(raw))
		case code == http.StatusUnauthorized:
			return ErrUnauthorized
		case code == http.StatusNotFound:
			return ErrNotFound
		case code < 200 || code > 299:
			return fmt.Errorf("tmdb: %s%s", resp.Status, statusMessage(raw))
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("tmdb: unexpected answer to %s: %w", path, err)
		}
		return nil
	}
}

// statusMessage is TMDB's explanation of an error, if it gave one.
func statusMessage(raw []byte) string {
	var e struct {
		Message string `json:"status_message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return ": " + e.Message
	}
	return ""
}

// retryAfter is how long TMDB asks us to wait after a 429: Retry-After (in
// seconds), 1s when it's missing, at most maxRetryAfter.
func retryAfter(h http.Header) time.Duration {
	wait := time.Second
	if ra, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && ra >= 0 {
		wait = time.Duration(ra) * time.Second
	}
	return min(wait, maxRetryAfter)
}

// sleepCtx waits for d, or until ctx is done (returning its error).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Endpoints

// kindPath is TMDB's name for a kind of media in its URLs.
func kindPath(kind string) string {
	if kind == TypeTV {
		return "tv"
	}
	return "movie"
}

// videoLanguages asks for the trailers in the metadata language, then in
// English, then without a language: most trailers are only in English.
func videoLanguages(language string) string {
	lang, _, _ := strings.Cut(language, "-")
	if lang == "" || lang == "en" {
		return "en,null"
	}
	return lang + ",en,null"
}

// details fetches a movie's or a show's page, with everything appended
// that the title page shows.
func (c *Client) details(ctx context.Context, kind string, tmdbID int) (*tmdbItem, error) {
	appended := "credits,videos,recommendations,similar,external_ids,keywords,watch/providers,alternative_titles"
	if kind == TypeTV {
		appended += ",content_ratings"
	} else {
		appended += ",release_dates"
	}
	q := url.Values{
		"append_to_response":     {appended},
		"include_video_language": {videoLanguages(c.settings().TMDB.Language)},
	}
	var it tmdbItem
	if err := c.get(ctx, "/"+kindPath(kind)+"/"+strconv.Itoa(tmdbID), q, &it); err != nil {
		return nil, err
	}
	if it.ID == 0 {
		return nil, ErrNotFound
	}
	return &it, nil
}

// season fetches a season of a show with its episodes.
func (c *Client) season(ctx context.Context, tvID, number int) (*tmdbSeasonDetail, error) {
	var s tmdbSeasonDetail
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, number), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// collection fetches a movie collection (a franchise) with its movies.
func (c *Client) collection(ctx context.Context, id int) (*tmdbCollection, error) {
	var col tmdbCollection
	if err := c.get(ctx, "/collection/"+strconv.Itoa(id), nil, &col); err != nil {
		return nil, err
	}
	return &col, nil
}

// page fetches a list of movies and shows: a search, discover, trending or
// chart (popular, top rated...) page.
func (c *Client) page(ctx context.Context, path string, q url.Values) (*tmdbPage, error) {
	var p tmdbPage
	if err := c.get(ctx, path, q, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// find looks a title up by its IMDb id.
func (c *Client) find(ctx context.Context, imdbID string) (*tmdbFind, error) {
	var f tmdbFind
	q := url.Values{"external_source": {"imdb_id"}}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), q, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// genreNames is TMDB's genres for movies or shows, by id, in the metadata
// language. They are kept for a day.
func (c *Client) genreNames(ctx context.Context, kind string) (map[int]string, error) {
	key := kind + "|" + c.settings().TMDB.Language
	c.genreMu.Lock()
	g, ok := c.genres[key]
	c.genreMu.Unlock()
	if ok && time.Since(g.at) < 24*time.Hour {
		return g.names, nil
	}
	var out struct {
		Genres []tmdbGenre `json:"genres"`
	}
	if err := c.get(ctx, "/genre/"+kindPath(kind)+"/list", nil, &out); err != nil {
		if ok {
			return g.names, nil // an older copy beats none
		}
		return nil, err
	}
	names := make(map[int]string, len(out.Genres))
	for _, x := range out.Genres {
		names[x.ID] = x.Name
	}
	c.genreMu.Lock()
	c.genres[key] = genreList{names: names, at: time.Now()}
	c.genreMu.Unlock()
	return names, nil
}

// forgetGenres drops the genre lists (the language changed).
func (c *Client) forgetGenres() {
	c.genreMu.Lock()
	c.genres = map[string]genreList{}
	c.genreMu.Unlock()
}
